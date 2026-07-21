//go:build e2e

package invokerecord_test

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Nene7ko/NeKiro/contracts"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const acceptanceWorkspace = "workspace-acceptance"

type acceptanceEnv struct {
	controlPlane string
	ownerToken   string
	userToken    string
	otherToken   string
	databaseURL  string
	composeFile  string
}

type httpResult struct {
	status int
	header http.Header
	body   []byte
}

func TestInvokeToRecordAcceptance(t *testing.T) {
	env := loadAcceptanceEnv(t)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Timeout: 45 * time.Second}
	if result := doRequest(t, client, env.controlPlane+"/readyz", http.MethodGet, "", "", nil); result.status != http.StatusOK {
		t.Fatalf("Control Plane readiness status=%d body=%s", result.status, result.body)
	}

	runtimeA := acceptanceCard("runtime-a", "Runtime A", "http://runtime-a:8091", "runtime.cross", nil, false)
	runtimeB := acceptanceCard("runtime-b", "Runtime B", "http://runtime-b:8092", "runtime.echo", []string{"text.read"}, true)
	runtimeProtocol := acceptanceCard("runtime-protocol", "Runtime Protocol Fixture", "http://runtime-b:8092", "runtime.protocol", nil, false)
	runtimeRoute := acceptanceCard("runtime-route", "Runtime Route Fixture", "http://runtime-unavailable:8099", "runtime.route", nil, false)
	runtimeTimeout := acceptanceCardWithTimeout("runtime-timeout", "Runtime Timeout Fixture", "http://runtime-b:8092", "runtime.timeout", nil, true, 50)
	registerAndPublish(t, client, env, runtimeA)
	registerAndPublish(t, client, env, runtimeB)
	registerAndPublish(t, client, env, runtimeProtocol)
	registerAndPublish(t, client, env, runtimeRoute)
	registerAndPublish(t, client, env, runtimeTimeout)

	discovery := doRequest(t, client, env.controlPlane+"/v3/agents?capability=runtime.echo", http.MethodGet, env.userToken, "", nil)
	if discovery.status != http.StatusOK || !bytes.Contains(discovery.body, []byte(`"agentId":"runtime-b"`)) {
		t.Fatalf("discovery status=%d body=%s", discovery.status, discovery.body)
	}
	createWorkspace(t, client, env, acceptanceWorkspace, env.ownerToken)
	install(t, client, env, acceptanceWorkspace, "runtime-a", []string{})
	install(t, client, env, acceptanceWorkspace, "runtime-b", []string{"text.read"})
	install(t, client, env, acceptanceWorkspace, "runtime-protocol", []string{})
	install(t, client, env, acceptanceWorkspace, "runtime-route", []string{})
	install(t, client, env, acceptanceWorkspace, "runtime-timeout", []string{})

	direct := invokeJSON(t, client, env, "runtime-b", "runtime.echo", map[string]any{"fixture": "success", "value": "direct-json-value"})
	if direct.result.Status != "succeeded" || !bytes.Contains(direct.result.Result, []byte("direct-json-value")) {
		t.Fatalf("direct JSON result=%s", direct.result.Result)
	}
	assertRecord(t, client, env, direct.result.InvocationID, acceptanceWorkspace, "runtime-b", "succeeded", "")

	stream := invokeSSE(t, client, env, "runtime-b", "runtime.echo", map[string]any{"fixture": "stream-success", "value": "direct-sse-value"})
	if len(stream) < 3 || stream[0].Type != contracts.ResultStreamEventAccepted || stream[len(stream)-1].Type != contracts.ResultStreamEventCompleted {
		t.Fatalf("SSE sequence=%#v", stream)
	}
	for index, event := range stream {
		if event.Sequence != int64(index) || event.InvocationID == "" || event.RootTaskID == "" || event.TraceID == "" {
			t.Fatalf("SSE event[%d]=%#v", index, event)
		}
	}
	assertRecord(t, client, env, stream[0].InvocationID, acceptanceWorkspace, "runtime-b", "succeeded", "")

	nested := invokeJSON(t, client, env, "runtime-a", "runtime.cross", map[string]any{"fixture": "success", "value": "nested-value"})
	if nested.result.Status != "succeeded" || !bytes.Contains(nested.result.Result, []byte(`"runtime-a"`)) || !bytes.Contains(nested.result.Result, []byte(`"childInvocationId"`)) {
		t.Fatalf("nested result=%s", nested.result.Result)
	}
	trace := readTrace(t, client, env, nested.result.TraceID)
	if len(trace.Invocations) != 2 {
		t.Fatalf("nested trace invocations=%d body=%s", len(trace.Invocations), trace.raw)
	}
	if trace.Invocations[0].ParentInvocationID != "" || trace.Invocations[1].ParentInvocationID != trace.Invocations[0].InvocationID || trace.Invocations[0].RootTaskID != trace.Invocations[1].RootTaskID || trace.Invocations[0].TraceID != trace.Invocations[1].TraceID {
		t.Fatalf("nested lineage=%#v", trace.Invocations)
	}
	assertRecord(t, client, env, nested.result.InvocationID, acceptanceWorkspace, "runtime-a", "succeeded", "")

	otherWorkspace := "workspace-other"
	createWorkspace(t, client, env, otherWorkspace, env.otherToken)
	isolation := doRequest(t, client, env.controlPlane+fmt.Sprintf("/v4/workspaces/%s/traces/%s", acceptanceWorkspace, nested.result.TraceID), http.MethodGet, env.otherToken, "", nil)
	if isolation.status != http.StatusForbidden {
		t.Fatalf("foreign trace read status=%d body=%s", isolation.status, isolation.body)
	}

	restartRouter(t, env.composeFile)
	waitForReady(t, client, env.controlPlane+"/readyz")
	readAfterRestart := readTrace(t, client, env, nested.result.TraceID)
	if len(readAfterRestart.Invocations) != 2 {
		t.Fatalf("trace after Router restart=%#v", readAfterRestart.Invocations)
	}

	assertFailureMatrix(t, client, env)
	assertConcurrentCalls(t, client, env)
	assertStorageAndLogsAreMetadataOnly(t, env, []string{"direct-json-value", "direct-sse-value", "nested-value", "acceptance-owner-token"})
}

func loadAcceptanceEnv(t *testing.T) acceptanceEnv {
	t.Helper()
	return acceptanceEnv{
		controlPlane: requiredEnv(t, "NEKIRO_E2E_CONTROL_PLANE_URL"),
		ownerToken:   requiredEnv(t, "NEKIRO_E2E_OWNER_TOKEN"),
		userToken:    requiredEnv(t, "NEKIRO_E2E_USER_TOKEN"),
		otherToken:   requiredEnv(t, "NEKIRO_E2E_OTHER_TOKEN"),
		databaseURL:  requiredEnv(t, "NEKIRO_E2E_DATABASE_URL"),
		composeFile:  requiredEnv(t, "NEKIRO_E2E_COMPOSE_FILE"),
	}
}

func requiredEnv(t *testing.T, name string) string {
	t.Helper()
	value, exists := os.LookupEnv(name)
	if !exists || value == "" || strings.TrimSpace(value) != value {
		t.Fatalf("%s must be explicitly configured", name)
	}
	return value
}

func acceptanceCard(agentID, name, endpoint, capability string, permissions []string, streaming bool) []byte {
	return acceptanceCardWithTimeout(agentID, name, endpoint, capability, permissions, streaming, 30000)
}

func acceptanceCardWithTimeout(agentID, name, endpoint, capability string, permissions []string, streaming bool, timeoutMS int64) []byte {
	card := contracts.AgentCard{
		SchemaVersion: contracts.AgentCardSchemaVersion, AgentID: agentID, Name: name,
		Description: "Deterministic acceptance Agent", Owner: contracts.AgentOwner{ID: "acceptance-owner", DisplayName: "Acceptance Owner"}, Version: "1.0.0",
		Protocol:       contracts.AgentProtocol{Type: "a2a", Version: contracts.A2AProtocolVersion, Transport: "JSONRPC", Endpoint: endpoint},
		Skills:         []contracts.AgentSkill{{ID: capability, Name: capability, Description: "Acceptance capability", InputSchema: contracts.JSONSchema{"type": "object"}, OutputSchema: contracts.JSONSchema{"type": "object"}, RequiredPermissions: permissions}},
		Authentication: contracts.AgentAuthentication{Type: "none"}, Limits: contracts.AgentLimits{TimeoutMS: timeoutMS, MaxInputBytes: json.Number("1048576"), MaxOutputBytes: json.Number("1048576"), Streaming: streaming},
	}
	for _, permission := range permissions {
		card.Permissions = append(card.Permissions, contracts.PermissionDeclaration{ID: permission, Description: permission})
	}
	encoded, err := json.Marshal(card)
	if err != nil {
		panic(err)
	}
	return encoded
}

func registerAndPublish(t *testing.T, client *http.Client, env acceptanceEnv, card []byte) {
	t.Helper()
	registered := doRequest(t, client, env.controlPlane+"/v3/agents", http.MethodPost, env.ownerToken, "application/json", map[string]any{"card": json.RawMessage(card)})
	if registered.status != http.StatusCreated && registered.status != http.StatusConflict {
		t.Fatalf("register status=%d body=%s", registered.status, registered.body)
	}
	var value contracts.AgentCard
	if err := json.Unmarshal(card, &value); err != nil {
		t.Fatal(err)
	}
	published := doRequest(t, client, env.controlPlane+fmt.Sprintf("/v3/agents/%s/versions/%s/publish", value.AgentID, value.Version), http.MethodPost, env.ownerToken, "", nil)
	if published.status != http.StatusOK && published.status != http.StatusConflict {
		t.Fatalf("publish %s status=%d body=%s", value.AgentID, published.status, published.body)
	}
}

func createWorkspace(t *testing.T, client *http.Client, env acceptanceEnv, workspaceID, token string) {
	t.Helper()
	result := doRequest(t, client, env.controlPlane+"/v3/workspaces", http.MethodPost, token, "application/json", map[string]any{"workspaceId": workspaceID})
	if result.status != http.StatusCreated && result.status != http.StatusConflict {
		t.Fatalf("create Workspace %s status=%d body=%s", workspaceID, result.status, result.body)
	}
}

func install(t *testing.T, client *http.Client, env acceptanceEnv, workspaceID, agentID string, permissions []string) {
	t.Helper()
	result := doRequest(t, client, env.controlPlane+fmt.Sprintf("/v3/workspaces/%s/installations", workspaceID), http.MethodPost, env.ownerToken, "application/json", map[string]any{"agentId": agentID, "versionConstraint": "=1.0.0", "acceptedPermissions": permissions})
	if result.status != http.StatusCreated && result.status != http.StatusConflict {
		t.Fatalf("install %s status=%d body=%s", agentID, result.status, result.body)
	}
}

type jsonInvocation struct {
	result contracts.InvocationResult
}

func invokeJSON(t *testing.T, client *http.Client, env acceptanceEnv, agentID, capability string, input map[string]any) jsonInvocation {
	t.Helper()
	result := doRequest(t, client, env.controlPlane+"/v4/workspaces/"+acceptanceWorkspace+"/invocations", http.MethodPost, env.ownerToken, "application/json", map[string]any{"agentId": agentID, "capability": capability, "input": input, "stream": false})
	if result.status != http.StatusOK {
		t.Fatalf("JSON invoke %s status=%d body=%s", agentID, result.status, result.body)
	}
	var invocation contracts.InvocationResult
	if err := json.Unmarshal(result.body, &invocation); err != nil {
		t.Fatalf("decode JSON invocation: %v body=%s", err, result.body)
	}
	if invocation.InvocationID == "" || invocation.RootTaskID == "" || invocation.TraceID == "" {
		t.Fatalf("JSON correlation=%#v", invocation)
	}
	return jsonInvocation{result: invocation}
}

func invokeSSE(t *testing.T, client *http.Client, env acceptanceEnv, agentID, capability string, input map[string]any) []contracts.InvocationResultStreamEventV2 {
	t.Helper()
	body, err := json.Marshal(map[string]any{"agentId": agentID, "capability": capability, "input": input, "stream": true})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, env.controlPlane+"/v4/workspaces/"+acceptanceWorkspace+"/invocations", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+env.ownerToken)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(response.Body)
		t.Fatalf("SSE invoke status=%d body=%s", response.StatusCode, data)
	}
	reader := bufio.NewReader(response.Body)
	var events []contracts.InvocationResultStreamEventV2
	for {
		line, err := reader.ReadString('\n')
		if errors.Is(err, io.EOF) && line == "" {
			break
		}
		if err != nil && !errors.Is(err, io.EOF) {
			t.Fatal(err)
		}
		if !strings.HasPrefix(line, "data: ") || !strings.HasSuffix(line, "\n") {
			t.Fatalf("invalid SSE data line=%q", line)
		}
		blank, blankErr := reader.ReadString('\n')
		if blankErr != nil || blank != "\n" {
			t.Fatalf("invalid SSE delimiter=%q err=%v", blank, blankErr)
		}
		var event contracts.InvocationResultStreamEventV2
		if err := json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimPrefix(line, "data: "), "\n")), &event); err != nil {
			t.Fatalf("decode SSE event: %v", err)
		}
		events = append(events, event)
		if event.Type == contracts.ResultStreamEventCompleted || event.Type == contracts.ResultStreamEventFailed || event.Type == contracts.ResultStreamEventCanceled || event.Type == contracts.ResultStreamEventTimedOut {
			break
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}
	return events
}

type traceRead struct {
	contracts.TraceResponseV4
	raw []byte
}

func readTrace(t *testing.T, client *http.Client, env acceptanceEnv, traceID contracts.TraceID) traceRead {
	t.Helper()
	result := doRequest(t, client, env.controlPlane+fmt.Sprintf("/v4/workspaces/%s/traces/%s", acceptanceWorkspace, traceID), http.MethodGet, env.ownerToken, "", nil)
	if result.status != http.StatusOK {
		t.Fatalf("trace read status=%d body=%s", result.status, result.body)
	}
	var trace contracts.TraceResponseV4
	if err := json.Unmarshal(result.body, &trace); err != nil {
		t.Fatalf("decode trace: %v", err)
	}
	return traceRead{TraceResponseV4: trace, raw: result.body}
}

func assertRecord(t *testing.T, client *http.Client, env acceptanceEnv, invocationID, workspaceID, agentID, status, errorCode string) {
	t.Helper()
	result := doRequest(t, client, env.controlPlane+fmt.Sprintf("/v4/workspaces/%s/invocations/%s", workspaceID, invocationID), http.MethodGet, env.ownerToken, "", nil)
	if result.status != http.StatusOK {
		t.Fatalf("record read status=%d body=%s", result.status, result.body)
	}
	var detail contracts.InvocationDetailResponseV4
	if err := json.Unmarshal(result.body, &detail); err != nil {
		t.Fatalf("decode record: %v", err)
	}
	if detail.Invocation.TargetAgentID != agentID || detail.Invocation.Status != status || len(detail.Events) == 0 {
		t.Fatalf("record projection=%#v events=%#v", detail.Invocation, detail.Events)
	}
	if errorCode != "" && string(detail.Invocation.ErrorCode) != errorCode {
		t.Fatalf("record error=%q want=%q", detail.Invocation.ErrorCode, errorCode)
	}
	for index, event := range detail.Events {
		if event.Sequence != int64(index) || event.InvocationID != invocationID || event.WorkspaceID != workspaceID {
			t.Fatalf("record event[%d]=%#v", index, event)
		}
	}
}

func assertFailureMatrix(t *testing.T, client *http.Client, env acceptanceEnv) {
	t.Helper()
	missing := doRequest(t, client, env.controlPlane+"/v4/workspaces/"+acceptanceWorkspace+"/invocations", http.MethodPost, env.ownerToken, "application/json", map[string]any{"agentId": "not-installed", "capability": "runtime.echo", "input": map[string]any{"fixture": "success", "value": "policy"}, "stream": false})
	assertErrorCode(t, missing, contracts.ErrorCodeAgentNotInstalled)
	protocol := doRequest(t, client, env.controlPlane+"/v4/workspaces/"+acceptanceWorkspace+"/invocations", http.MethodPost, env.ownerToken, "application/json", map[string]any{"agentId": "runtime-protocol", "capability": "runtime.protocol", "input": map[string]any{"fixture": "protocol", "value": "protocol"}, "stream": false})
	assertErrorCode(t, protocol, contracts.ErrorCodeA2AProtocol)
	agent := doRequest(t, client, env.controlPlane+"/v4/workspaces/"+acceptanceWorkspace+"/invocations", http.MethodPost, env.ownerToken, "application/json", map[string]any{"agentId": "runtime-b", "capability": "runtime.echo", "input": map[string]any{"fixture": "failure", "value": "agent"}, "stream": false})
	assertErrorCode(t, agent, contracts.ErrorCodeAgentExecutionFailed)
	route := doRequest(t, client, env.controlPlane+"/v4/workspaces/"+acceptanceWorkspace+"/invocations", http.MethodPost, env.ownerToken, "application/json", map[string]any{"agentId": "runtime-route", "capability": "runtime.route", "input": map[string]any{"fixture": "success", "value": "route"}, "stream": false})
	assertErrorCode(t, route, contracts.ErrorCodeAgentUnavailable)
	timedOut := invokeSSE(t, client, env, "runtime-timeout", "runtime.timeout", map[string]any{"fixture": "hold", "value": "timeout"})
	if len(timedOut) == 0 || timedOut[len(timedOut)-1].Type != contracts.ResultStreamEventTimedOut {
		t.Fatalf("timeout stream=%#v", timedOut)
	}
	canceledInvocationID := invokeCanceledSSE(t, client, env, "runtime-timeout", "runtime.timeout")
	waitForRecord(t, client, env, canceledInvocationID, "canceled")
}

func invokeCanceledSSE(t *testing.T, client *http.Client, env acceptanceEnv, agentID, capability string) string {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	body, err := json.Marshal(map[string]any{"agentId": agentID, "capability": capability, "input": map[string]any{"fixture": "hold", "value": "cancel"}, "stream": true})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, env.controlPlane+"/v4/workspaces/"+acceptanceWorkspace+"/invocations", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+env.ownerToken)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(response.Body)
	line, err := reader.ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "data: ") {
		response.Body.Close()
		t.Fatalf("cancel stream accepted read err=%v line=%q", err, line)
	}
	blank, err := reader.ReadString('\n')
	if err != nil || blank != "\n" {
		response.Body.Close()
		t.Fatalf("cancel stream delimiter=%q err=%v", blank, err)
	}
	var accepted contracts.InvocationResultStreamEventV2
	if err := json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimPrefix(line, "data: "), "\n")), &accepted); err != nil || accepted.Type != contracts.ResultStreamEventAccepted {
		response.Body.Close()
		t.Fatalf("cancel stream accepted=%#v err=%v", accepted, err)
	}
	cancel()
	_ = response.Body.Close()
	return accepted.InvocationID
}

func waitForRecord(t *testing.T, client *http.Client, env acceptanceEnv, invocationID, status string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		result := doRequest(t, client, env.controlPlane+fmt.Sprintf("/v4/workspaces/%s/invocations/%s", acceptanceWorkspace, invocationID), http.MethodGet, env.ownerToken, "", nil)
		if result.status == http.StatusOK {
			var detail contracts.InvocationDetailResponseV4
			if err := json.Unmarshal(result.body, &detail); err == nil && detail.Invocation.Status == status {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("Invocation %s did not reach %s", invocationID, status)
}

func assertErrorCode(t *testing.T, result httpResult, want contracts.PlatformErrorCode) {
	t.Helper()
	if result.status == http.StatusOK {
		t.Fatalf("failure unexpectedly succeeded: %s", result.body)
	}
	var value struct {
		Code contracts.PlatformErrorCode `json:"code"`
	}
	if err := json.Unmarshal(result.body, &value); err != nil || value.Code != want {
		t.Fatalf("error status=%d code=%q want=%q body=%s err=%v", result.status, value.Code, want, result.body, err)
	}
}

func assertConcurrentCalls(t *testing.T, client *http.Client, env acceptanceEnv) {
	t.Helper()
	const calls = 100
	start := make(chan struct{})
	results := make(chan httpResult, calls)
	var wait sync.WaitGroup
	wait.Add(calls)
	for index := 0; index < calls; index++ {
		index := index
		go func() {
			defer wait.Done()
			<-start
			results <- doRequest(t, client, env.controlPlane+"/v4/workspaces/"+acceptanceWorkspace+"/invocations", http.MethodPost, env.ownerToken, "application/json", map[string]any{"agentId": "runtime-b", "capability": "runtime.echo", "input": map[string]any{"fixture": "success", "value": fmt.Sprintf("concurrent-%03d", index)}, "stream": false})
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	ids := make([]string, 0, calls)
	for result := range results {
		if result.status != http.StatusOK {
			t.Fatalf("concurrent status=%d body=%s", result.status, result.body)
		}
		var value contracts.InvocationResult
		if err := json.Unmarshal(result.body, &value); err != nil {
			t.Fatalf("concurrent decode: %v", err)
		}
		ids = append(ids, value.InvocationID)
		if !bytes.Contains(value.Result, []byte("runtime-b")) {
			t.Fatalf("concurrent result=%s", value.Result)
		}
	}
	sort.Strings(ids)
	for index := 1; index < len(ids); index++ {
		if ids[index] == ids[index-1] {
			t.Fatalf("duplicate concurrent Invocation ID=%s", ids[index])
		}
	}
	if len(ids) != calls {
		t.Fatalf("concurrent accepted=%d want=%d", len(ids), calls)
	}
}

func assertStorageAndLogsAreMetadataOnly(t *testing.T, env acceptanceEnv, forbidden []string) {
	t.Helper()
	database, err := sql.Open("pgx", env.databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := database.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := database.QueryContext(ctx, `SELECT event_id, event_type, status, invocation_id, root_task_id, COALESCE(parent_invocation_id, ''), trace_id, caller_id, workspace_id, target_agent_id, agent_card_version, capability, COALESCE(error_code, '') FROM ledger.invocation_events`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var fields [13]string
		args := make([]any, len(fields))
		for index := range fields {
			args[index] = &fields[index]
		}
		if err := rows.Scan(args...); err != nil {
			t.Fatal(err)
		}
		serialized := strings.Join(fields[:], "|")
		for _, literal := range forbidden {
			if strings.Contains(serialized, literal) {
				t.Fatalf("forbidden literal %q in Ledger row %q", literal, serialized)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	logs := exec.CommandContext(ctx, "docker", "compose", "--file", env.composeFile, "logs", "--no-color")
	output, err := logs.Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, literal := range forbidden {
		if bytes.Contains(output, []byte(literal)) {
			t.Fatalf("forbidden literal %q appeared in process logs", literal)
		}
	}
}

func restartRouter(t *testing.T, composeFile string) {
	t.Helper()
	command := exec.CommandContext(t.Context(), "docker", "compose", "--file", composeFile, "restart", "a2a-router")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("restart Router: %v output=%s", err, output)
	}
}

func waitForReady(t *testing.T, client *http.Client, endpoint string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, endpoint, nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Do(request)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("endpoint %s did not become ready", endpoint)
}

func doRequest(t *testing.T, client *http.Client, endpoint, method, token, contentType string, payload any) httpResult {
	t.Helper()
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(t.Context(), method, endpoint, body)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if method == http.MethodPost && strings.Contains(endpoint, "/invocations") {
		request.Header.Set("Accept", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return httpResult{status: response.StatusCode, header: response.Header, body: data}
}
