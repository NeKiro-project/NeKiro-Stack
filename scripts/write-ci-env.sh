#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 4 ]]; then
  echo 'usage: write-ci-env.sh <backend|browser|compose> <github-env-file> <absolute-stack-root> <compose-project>' >&2
  exit 2
fi

scenario=$1
output=$2
stack_root=$3
compose_project=$4
if [[ "$stack_root" != /* ]] || [[ -z "$compose_project" ]]; then
  echo 'stack root must be absolute and compose project must be non-empty' >&2
  exit 2
fi

write_common() {
  cat >>"$output" <<'EOF'
POSTGRES_PORT=55432
CONTROL_PLANE_PORT=18080
A2A_ROUTER_PORT=18081
NEKIRO_PUBLIC_AGENT_ORIGIN=https://agents.nekiro.test
NEKIRO_ENDPOINT_ALLOWED_PRIVATE_HOSTS_JSON=["runtime-a","runtime-b"]
NEKIRO_CONTROL_PLANE_INTERNAL_REQUEST_MAX_BYTES=1048576
NEKIRO_GATEWAY_INVOCATION_REQUEST_MAX_BYTES=1048576
NEKIRO_GATEWAY_SSE_EVENT_MAX_BYTES=65536
NEKIRO_GATEWAY_METADATA_RESPONSE_MAX_BYTES=1048576
NEKIRO_GATEWAY_INVOCATION_DEADLINE_MS=30000
NEKIRO_ROUTER_INTERNAL_REQUEST_LIMIT_BYTES=1048576
NEKIRO_ROUTER_AGENT_REQUEST_LIMIT_BYTES=1048576
NEKIRO_ROUTER_CONTROL_PLANE_RESPONSE_LIMIT_BYTES=1048576
NEKIRO_ROUTER_AGENT_RESPONSE_LIMIT_BYTES=1048576
NEKIRO_ROUTER_A2A_EVENT_LIMIT_BYTES=1048576
NEKIRO_ROUTER_SSE_EVENT_LIMIT_BYTES=65536
NEKIRO_ROUTER_RESOLUTION_DEADLINE_MS=30000
NEKIRO_ROUTER_AGENT_DEADLINE_MS=30000
NEKIRO_ROUTER_AGENT_CREDENTIAL_ISSUER=https://a2a-router.nekiro.test
NEKIRO_ROUTER_AGENT_CREDENTIAL_PRIVATE_KEY_BASE64URL=AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8DoQe_884Qvh1w3RjnS8CZZ-TWMJulDV8d3IZkElUxuA
NEKIRO_ROUTER_AGENT_CREDENTIAL_TTL_SECONDS=30
NEKIRO_AGENT_ROUTER_ISSUER=https://a2a-router.nekiro.test
NEKIRO_AGENT_ROUTER_PUBLIC_KEY_BASE64URL=A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg
RUNTIME_A_RESPONSE_LIMIT_BYTES=1048576
RUNTIME_A_EVENT_LIMIT_BYTES=65536
RUNTIME_B_RESPONSE_LIMIT_BYTES=1048576
RUNTIME_B_EVENT_LIMIT_BYTES=65536
EOF
  printf 'NEKIRO_E2E_COMPOSE_FILE=%s/compose.yaml\n' "$stack_root" >>"$output"
  printf 'NEKIRO_E2E_COMPOSE_PROJECT=%s\n' "$compose_project" >>"$output"
}

case "$scenario" in
  backend)
    write_common
    cat >>"$output" <<'EOF'
POSTGRES_USER=nekiro_acceptance
POSTGRES_PASSWORD=acceptance-only-password
POSTGRES_DB=nekiro_acceptance
NEKIRO_COMPOSE_DATABASE_URL=postgresql://nekiro_acceptance:acceptance-only-password@postgres:5432/nekiro_acceptance?sslmode=disable
NEKIRO_DEV_AUTH_PRINCIPALS_JSON=[{"id":"acceptance-owner","tokenSha256":"465aedffb32a2cb642cbca8fc75b806bcd33f703d70c49dcfb05e9db88df32d2"},{"id":"acceptance-user","tokenSha256":"2af4f9af4fa535905378ccee817aa532244dcf102f3d3ebeaf9a2a92abdeb42d"},{"id":"acceptance-other","tokenSha256":"7f85fe19123d4f88c475cb754dd30f422877ba3b3e2d5eed8ff8c2f9453ebeaf"}]
NEKIRO_INTERNAL_DEV_AUTH_PRINCIPALS_JSON=[{"id":"router-internal","tokenSha256":"f9232718425b5ebee721187a79703448bce513ecf0600eb161f9256ddac27c4d"}]
NEKIRO_ROUTER_SERVICE_PRINCIPALS_JSON=[{"id":"control-plane","tokenSha256":"5abfd00de27c6b2f57d45fdc90999134e4e088414ba1f39bf67ee0d1c9cec554"}]
NEKIRO_ROUTER_AGENT_PRINCIPALS_JSON=[{"workspaceId":"workspace-acceptance","agentId":"runtime-a","tokenSha256":"e304d0370532633d535824a897d5c03445b636e8d1649064aa35a8fb50fef200"},{"workspaceId":"workspace-acceptance","agentId":"runtime-b","tokenSha256":"9b990de9bb74efd4e1d26a43a01e132deb60d563d49faf6878dca4af40858a38"}]
NEKIRO_ROUTER_INTERNAL_BEARER_TOKEN=router-internal-token
NEKIRO_CONTROL_PLANE_SERVICE_TOKEN=control-plane-internal-token
NEKIRO_CORS_ALLOWED_ORIGINS=http://127.0.0.1:3000
NEKIRO_ENDPOINT_CHALLENGE_TTL_SECONDS=10
NEKIRO_ENDPOINT_VERIFICATION_TIMEOUT_MS=3000
NEKIRO_ROUTER_AGENT_CREDENTIAL_KEY_ID=ci-acceptance-key-1
NEKIRO_AGENT_ROUTER_KEY_ID=ci-acceptance-key-1
RUNTIME_A_ROUTER_TOKEN=runtime-a-router-token
RUNTIME_B_ROUTER_TOKEN=runtime-b-router-token
NEKIRO_E2E_CONTROL_PLANE_URL=http://127.0.0.1:18080
NEKIRO_E2E_PUBLIC_AGENT_ORIGIN=https://agents.nekiro.test
NEKIRO_E2E_ROUTER_URL=http://127.0.0.1:18081
NEKIRO_E2E_ROUTER_TOKEN=router-internal-token
NEKIRO_E2E_OWNER_TOKEN=acceptance-owner-token
NEKIRO_E2E_USER_TOKEN=acceptance-user-token
NEKIRO_E2E_OTHER_TOKEN=acceptance-other-token
NEKIRO_E2E_DATABASE_URL=postgresql://nekiro_acceptance:acceptance-only-password@127.0.0.1:55432/nekiro_acceptance?sslmode=disable
EOF
    ;;
  browser)
    write_common
    cat >>"$output" <<'EOF'
POSTGRES_USER=nekiro_console_acceptance
POSTGRES_PASSWORD=nekiro-console-acceptance-only
POSTGRES_DB=nekiro_console_acceptance
NEKIRO_COMPOSE_DATABASE_URL=postgresql://nekiro_console_acceptance:nekiro-console-acceptance-only@postgres:5432/nekiro_console_acceptance?sslmode=disable
NEKIRO_DEV_AUTH_PRINCIPALS_JSON=[{"id":"root-console-provider","tokenSha256":"4def860d949646b1515e6d28096af112224f06cf6a5941ab0ac51b9a458b1252"},{"id":"root-console-owner","tokenSha256":"4162f45cb0487cc2205850cc622fbecaa976a87f7aae8a96fa1676e2a984d2ac"}]
NEKIRO_INTERNAL_DEV_AUTH_PRINCIPALS_JSON=[{"id":"root-console-router-internal","tokenSha256":"4285e7349a2517fbdbfac9c1bc072a5ff1ef702d6cfe1826d62f02d73334bdc0"}]
NEKIRO_ROUTER_SERVICE_PRINCIPALS_JSON=[{"id":"root-console-control-plane","tokenSha256":"e3a864d8b9d70000e50e42d94761cc5d5996a36f39137735dca3a830f23cf4ba"}]
NEKIRO_ROUTER_AGENT_PRINCIPALS_JSON=[{"workspaceId":"root-console-workspace","agentId":"runtime-a","tokenSha256":"fd59489764dad19b9c276a8fb0187fdb187f3de6b426467c0af9659edbe4159f"},{"workspaceId":"root-console-workspace","agentId":"runtime-b","tokenSha256":"43a0b80acc0b4436c4433b8313bc4c41cbb5534751c458f6e2cfe4af602ca34f"}]
NEKIRO_ROUTER_INTERNAL_BEARER_TOKEN=root-console-router-internal-token
NEKIRO_CONTROL_PLANE_SERVICE_TOKEN=root-console-control-plane-token
NEKIRO_CORS_ALLOWED_ORIGINS=http://127.0.0.1:4173
NEKIRO_ENDPOINT_CHALLENGE_TTL_SECONDS=300
NEKIRO_ENDPOINT_VERIFICATION_TIMEOUT_MS=10000
NEKIRO_ROUTER_AGENT_CREDENTIAL_KEY_ID=root-console-browser-key-1
NEKIRO_AGENT_ROUTER_KEY_ID=root-console-browser-key-1
RUNTIME_A_ROUTER_TOKEN=root-console-runtime-a-token
RUNTIME_B_ROUTER_TOKEN=root-console-runtime-b-token
NEKIRO_E2E_BASE_URL=http://127.0.0.1:4173
NEKIRO_E2E_GATEWAY_PROXY_TARGET=http://127.0.0.1:18080
VITE_NEKIRO_API_BASE_URL=http://gateway.nekiro.test:18080
VITE_NEKIRO_PROVIDER_ID=root-console-provider
VITE_NEKIRO_PROVIDER_NAME=Root Console Provider
VITE_NEKIRO_PROVIDER_TOKEN=root-console-provider-token
VITE_NEKIRO_OWNER_TOKEN=root-console-owner-token
VITE_NEKIRO_DEFAULT_WORKSPACE_ID=root-console-workspace
VITE_NEKIRO_PUBLIC_AGENT_ORIGIN=https://agents.nekiro.test
EOF
    ;;
  compose)
    write_common
    cat >>"$output" <<'EOF'
POSTGRES_USER=compose_config_check
POSTGRES_PASSWORD=compose-config-check-only
POSTGRES_DB=compose_config_check
NEKIRO_COMPOSE_DATABASE_URL=postgresql://compose_config_check:compose-config-check-only@postgres:5432/compose_config_check?sslmode=disable
NEKIRO_DEV_AUTH_PRINCIPALS_JSON=[{"id":"compose-check","tokenSha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]
NEKIRO_INTERNAL_DEV_AUTH_PRINCIPALS_JSON=[{"id":"compose-router-check","tokenSha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}]
NEKIRO_ROUTER_SERVICE_PRINCIPALS_JSON=[{"id":"compose-control-plane","tokenSha256":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"}]
NEKIRO_ROUTER_AGENT_PRINCIPALS_JSON=[{"workspaceId":"workspace-acceptance","agentId":"runtime-a","tokenSha256":"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"},{"workspaceId":"workspace-acceptance","agentId":"runtime-b","tokenSha256":"cc7c23956db7e7fbf8d5ee1948b5f056d62599d6fbd4af42c8a73ed9a1dff7e0"}]
NEKIRO_ROUTER_INTERNAL_BEARER_TOKEN=compose-router-token
NEKIRO_CONTROL_PLANE_SERVICE_TOKEN=compose-control-plane-token
NEKIRO_CORS_ALLOWED_ORIGINS=http://127.0.0.1:3000
NEKIRO_ENDPOINT_CHALLENGE_TTL_SECONDS=300
NEKIRO_ENDPOINT_VERIFICATION_TIMEOUT_MS=10000
NEKIRO_ROUTER_AGENT_CREDENTIAL_KEY_ID=ci-compose-key-1
NEKIRO_AGENT_ROUTER_KEY_ID=ci-compose-key-1
RUNTIME_A_ROUTER_TOKEN=runtime-a-token
RUNTIME_B_ROUTER_TOKEN=runtime-b-router-check
NEKIRO_CONTROL_PLANE_IMAGE=nekiro-control-plane:compose-check
NEKIRO_A2A_ROUTER_IMAGE=nekiro-a2a-router:compose-check
NEKIRO_RUNTIME_A_IMAGE=nekiro-runtime-a:compose-check
NEKIRO_RUNTIME_B_IMAGE=nekiro-runtime-b:compose-check
EOF
    ;;
  *)
    echo "unsupported CI environment scenario: $scenario" >&2
    exit 2
    ;;
esac
