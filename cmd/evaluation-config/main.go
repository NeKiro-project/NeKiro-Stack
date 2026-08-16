package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var immutableImagePattern = regexp.MustCompile(`^ghcr\.io/[a-z0-9._/-]+:[A-Za-z0-9._-]+@sha256:[0-9a-f]{64}$`)

func main() {
	root := flag.String("root", "", "absolute disposable evaluation root")
	output := flag.String("output", "", "absolute output env file")
	flag.Parse()
	if err := writeEvaluationEnvironment(*root, *output, os.Getenv); err != nil {
		fmt.Fprintln(os.Stderr, "evaluation configuration:", err)
		os.Exit(1)
	}
}

func writeEvaluationEnvironment(root, output string, getenv func(string) string) error {
	if !filepath.IsAbs(root) || !filepath.IsAbs(output) {
		return errors.New("root and output must be absolute")
	}
	root = filepath.Clean(root)
	output = filepath.Clean(output)
	if root == filepath.VolumeName(root)+string(filepath.Separator) || !strings.HasPrefix(output, root+string(filepath.Separator)) {
		return errors.New("output must be below the disposable evaluation root")
	}
	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		return errors.New("output must not already exist")
	}
	configRoot := filepath.Join(root, "config")
	tlsRoot := filepath.Join(root, "tls")
	if err := os.MkdirAll(configRoot, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(tlsRoot, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(configRoot, "cfg-v1-cm91dGVyL2luc3RhbmNlLWRpcmVjdG9yeQ.value"), []byte(`{"schemaVersion":"1","revision":"evaluation-bootstrap-1","targets":[]}`+"\n"), 0o600); err != nil {
		return err
	}

	ownerToken, err := randomToken("owner")
	if err != nil {
		return err
	}
	userToken, err := randomToken("user")
	if err != nil {
		return err
	}
	otherToken, err := randomToken("other")
	if err != nil {
		return err
	}
	routerInternalToken, err := randomToken("router")
	if err != nil {
		return err
	}
	controlPlaneToken, err := randomToken("control")
	if err != nil {
		return err
	}
	runtimeAToken, err := randomToken("runtime-a")
	if err != nil {
		return err
	}
	runtimeBToken, err := randomToken("runtime-b")
	if err != nil {
		return err
	}
	databasePassword, err := randomHex(24)
	if err != nil {
		return err
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}

	images := map[string]string{}
	for _, name := range []string{"NEKIRO_CONTROL_PLANE_IMAGE", "NEKIRO_A2A_ROUTER_IMAGE", "NEKIRO_CONSOLE_IMAGE", "NEKIRO_RUNTIME_A_IMAGE", "NEKIRO_RUNTIME_B_IMAGE", "NEKIRO_NACOS_SECURE_PROXY_IMAGE"} {
		value := getenv(name)
		if !immutableImagePattern.MatchString(value) {
			return fmt.Errorf("%s must be a GHCR tag pinned to an immutable digest", name)
		}
		images[name] = value
	}

	principals := func(values ...map[string]string) string {
		encoded, marshalErr := json.Marshal(values)
		if marshalErr != nil {
			panic(marshalErr)
		}
		return string(encoded)
	}
	values := map[string]string{
		"POSTGRES_PORT": "55432", "NACOS_PORT": "18848", "CONTROL_PLANE_PORT": "18080", "A2A_ROUTER_PORT": "18081", "NEKIRO_CONSOLE_PORT": "13000",
		"POSTGRES_USER": "nekiro_evaluation", "POSTGRES_PASSWORD": databasePassword, "POSTGRES_DB": "nekiro_evaluation",
		"NEKIRO_COMPOSE_DATABASE_URL": "postgresql://nekiro_evaluation:" + databasePassword + "@postgres:5432/nekiro_evaluation?sslmode=disable",
		"NEKIRO_PUBLIC_AGENT_ORIGIN":  "https://agents.nekiro.test", "NEKIRO_ENDPOINT_ALLOWED_PRIVATE_HOSTS_JSON": `["runtime-a","runtime-b"]`,
		"NEKIRO_DEV_AUTH_PRINCIPALS_JSON": principals(
			map[string]string{"id": "acceptance-owner", "tokenSha256": tokenHash(ownerToken)},
			map[string]string{"id": "acceptance-user", "tokenSha256": tokenHash(userToken)},
			map[string]string{"id": "acceptance-other", "tokenSha256": tokenHash(otherToken)},
		),
		"NEKIRO_INTERNAL_DEV_AUTH_PRINCIPALS_JSON": principals(map[string]string{"id": "router-internal", "tokenSha256": tokenHash(routerInternalToken)}),
		"NEKIRO_ROUTER_SERVICE_PRINCIPALS_JSON":    principals(map[string]string{"id": "control-plane", "tokenSha256": tokenHash(controlPlaneToken)}),
		"NEKIRO_ROUTER_AGENT_PRINCIPALS_JSON": principals(
			map[string]string{"workspaceId": "workspace-acceptance", "agentId": "runtime-a", "tokenSha256": tokenHash(runtimeAToken)},
			map[string]string{"workspaceId": "workspace-acceptance", "agentId": "runtime-b", "tokenSha256": tokenHash(runtimeBToken)},
		),
		"NEKIRO_ROUTER_INTERNAL_BEARER_TOKEN": routerInternalToken, "NEKIRO_CONTROL_PLANE_SERVICE_TOKEN": controlPlaneToken,
		"RUNTIME_A_ROUTER_TOKEN": runtimeAToken, "RUNTIME_B_ROUTER_TOKEN": runtimeBToken,
		"NEKIRO_CORS_ALLOWED_ORIGINS": "http://127.0.0.1:13000", "NEKIRO_ENDPOINT_CHALLENGE_TTL_SECONDS": "10", "NEKIRO_ENDPOINT_VERIFICATION_TIMEOUT_MS": "3000",
		"NEKIRO_CONTROL_PLANE_INTERNAL_REQUEST_MAX_BYTES": "1048576", "NEKIRO_GATEWAY_INVOCATION_REQUEST_MAX_BYTES": "1048576", "NEKIRO_GATEWAY_SSE_EVENT_MAX_BYTES": "65536", "NEKIRO_GATEWAY_METADATA_RESPONSE_MAX_BYTES": "1048576", "NEKIRO_GATEWAY_INVOCATION_DEADLINE_MS": "30000",
		"NEKIRO_ROUTER_INTERNAL_REQUEST_LIMIT_BYTES": "1048576", "NEKIRO_ROUTER_AGENT_REQUEST_LIMIT_BYTES": "1048576", "NEKIRO_ROUTER_CONTROL_PLANE_RESPONSE_LIMIT_BYTES": "1048576", "NEKIRO_ROUTER_AGENT_RESPONSE_LIMIT_BYTES": "1048576", "NEKIRO_ROUTER_A2A_EVENT_LIMIT_BYTES": "1048576", "NEKIRO_ROUTER_SSE_EVENT_LIMIT_BYTES": "65536", "NEKIRO_ROUTER_RESOLUTION_DEADLINE_MS": "30000", "NEKIRO_ROUTER_AGENT_DEADLINE_MS": "30000",
		"NEKIRO_ROUTER_AGENT_CREDENTIAL_ISSUER": "https://a2a-router.nekiro.test", "NEKIRO_ROUTER_AGENT_CREDENTIAL_KEY_ID": "evaluation-key-1", "NEKIRO_ROUTER_AGENT_CREDENTIAL_PRIVATE_KEY_BASE64URL": base64.RawURLEncoding.EncodeToString(privateKey), "NEKIRO_ROUTER_AGENT_CREDENTIAL_TTL_SECONDS": "30",
		"NEKIRO_AGENT_ROUTER_ISSUER": "https://a2a-router.nekiro.test", "NEKIRO_AGENT_ROUTER_KEY_ID": "evaluation-key-1", "NEKIRO_AGENT_ROUTER_PUBLIC_KEY_BASE64URL": base64.RawURLEncoding.EncodeToString(publicKey),
		"RUNTIME_A_RESPONSE_LIMIT_BYTES": "1048576", "RUNTIME_A_EVENT_LIMIT_BYTES": "65536", "RUNTIME_B_RESPONSE_LIMIT_BYTES": "1048576", "RUNTIME_B_EVENT_LIMIT_BYTES": "65536",
		"NEKIRO_ROUTER_CONFIG_CENTER_ROOT": configRoot, "NEKIRO_ROUTER_CONFIG_CENTER_MAX_PAYLOAD_BYTES": "1048576", "NEKIRO_ROUTER_INSTANCE_DIRECTORY_KEY": "router.nacos-bindings", "NEKIRO_ROUTER_INSTANCE_PORT_NAME": "a2a", "NEKIRO_ROUTER_INSTANCE_ROUTING_MODE": "nacos",
		"NEKIRO_ROUTER_NACOS_RESPONSE_LIMIT_BYTES": "1048576", "NEKIRO_ROUTER_NACOS_REQUEST_TIMEOUT_MS": "3000", "NEKIRO_ROUTER_NACOS_GRPC_REQUEST_TIMEOUT_MS": "3000", "NEKIRO_ROUTER_NACOS_PENDING_CHANGES": "64", "NEKIRO_ROUTER_NACOS_MAX_OBSERVATIONS": "1024",
		"NEKIRO_E2E_TLS_ROOT": tlsRoot, "NEKIRO_E2E_CONFIG_CENTER_ROOT": configRoot, "NEKIRO_E2E_COMPOSE_FILE": "/opt/nekiro/compose.yaml", "NEKIRO_E2E_COMPOSE_OVERRIDE_FILE": "/opt/nekiro/compose.router-nacos-secure.yaml", "NEKIRO_E2E_COMPOSE_EXTRA_FILE": "/opt/nekiro/compose.evaluation.yaml", "NEKIRO_E2E_COMPOSE_PROJECT": "nekiro-evaluation",
		"NEKIRO_E2E_CONTROL_PLANE_URL": "http://127.0.0.1:18080", "NEKIRO_E2E_PUBLIC_AGENT_ORIGIN": "https://agents.nekiro.test", "NEKIRO_E2E_ROUTER_URL": "http://127.0.0.1:18081", "NEKIRO_E2E_NACOS_URL": "http://127.0.0.1:18848/nacos", "NEKIRO_E2E_NACOS_FIXTURE_STATUS_URL": "http://127.0.0.1:19447/status",
		"NEKIRO_E2E_ROUTER_TOKEN": routerInternalToken, "NEKIRO_E2E_OWNER_TOKEN": ownerToken, "NEKIRO_E2E_USER_TOKEN": userToken, "NEKIRO_E2E_OTHER_TOKEN": otherToken, "NEKIRO_E2E_DATABASE_URL": "postgresql://nekiro_evaluation:" + databasePassword + "@127.0.0.1:55432/nekiro_evaluation?sslmode=disable",
		"NEKIRO_E2E_NACOS_TLS_PORT": "19443", "NEKIRO_E2E_NACOS_MTLS_PORT": "19444", "NEKIRO_E2E_NACOS_ROUTER_HTTP_PORT": "19445", "NEKIRO_E2E_NACOS_ROUTER_GRPC_PORT": "19446", "NEKIRO_E2E_NACOS_FIXTURE_STATUS_PORT": "19447", "NEKIRO_EVALUATION_SUMMARY_FILE": filepath.Join(root, "summary.json"),
		"VITE_NEKIRO_API_BASE_URL": "http://127.0.0.1:18080", "VITE_NEKIRO_PROVIDER_ID": "provider-acceptance", "VITE_NEKIRO_PROVIDER_NAME": "NeKiro Evaluation", "VITE_NEKIRO_PROVIDER_TOKEN": ownerToken, "VITE_NEKIRO_OWNER_TOKEN": ownerToken, "VITE_NEKIRO_DEFAULT_WORKSPACE_ID": "workspace-acceptance", "VITE_NEKIRO_PUBLIC_AGENT_ORIGIN": "https://agents.nekiro.test", "NEKIRO_CONSOLE_LISTEN_ADDRESS": "0.0.0.0:8080",
	}
	for name, value := range images {
		values[name] = value
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var content strings.Builder
	for _, key := range keys {
		fmt.Fprintf(&content, "%s=%s\n", key, shellQuote(values[key]))
	}
	return os.WriteFile(output, []byte(content.String()), 0o600)
}

func randomToken(prefix string) (string, error) {
	value, err := randomHex(24)
	if err != nil {
		return "", err
	}
	return prefix + "-" + value, nil
}

func randomHex(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func tokenHash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
