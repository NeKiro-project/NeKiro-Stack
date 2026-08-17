#!/bin/sh
set -eu

evaluation_root=/var/lib/nekiro-evaluation
environment_file="$evaluation_root/evaluation.env"
compose_files="--file /opt/nekiro/compose.yaml --file /opt/nekiro/compose.router-nacos-secure.yaml --file /opt/nekiro/compose.evaluation.yaml"

cleanup() {
  docker compose --env-file "$environment_file" --project-name nekiro-evaluation $compose_files --profile router-nacos-secure --profile runtime-registration --profile watch-refresh down --volumes --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

if [ -e "$evaluation_root" ]; then
  echo 'NeKiro evaluation: disposable root already exists inside the fresh evaluator container.' >&2
  exit 1
fi
mkdir -p "$evaluation_root"

dockerd-entrypoint.sh >/tmp/nekiro-dockerd.log 2>&1 &
dockerd_pid=$!
ready=false
for _ in $(seq 1 60); do
  if docker info >/dev/null 2>&1; then
    ready=true
    break
  fi
  if ! kill -0 "$dockerd_pid" 2>/dev/null; then
    echo 'NeKiro evaluation: isolated Docker daemon failed to start.' >&2
    exit 1
  fi
  sleep 1
done
if [ "$ready" != true ]; then
  echo 'NeKiro evaluation: isolated Docker daemon readiness timed out.' >&2
  exit 1
fi

if [ -z "${GHCR_USERNAME:-}" ] || [ -z "${GHCR_TOKEN:-}" ]; then
  echo 'NeKiro evaluation: GHCR_USERNAME and GHCR_TOKEN are required to pull released component images.' >&2
  exit 1
fi
printf '%s' "$GHCR_TOKEN" | docker login ghcr.io --username "$GHCR_USERNAME" --password-stdin >/dev/null
unset GHCR_TOKEN

/opt/nekiro/evaluation-config -root "$evaluation_root" -output "$environment_file"
set -a
. "$environment_file"
set +a

/opt/nekiro/nacos-secure-fixture generate "$NEKIRO_E2E_TLS_ROOT"
docker compose --env-file "$environment_file" --project-name "$NEKIRO_E2E_COMPOSE_PROJECT" $compose_files --profile router-nacos-secure up --detach --wait --wait-timeout 180

echo 'NeKiro evaluation: running Register -> Discover -> Install -> Invoke -> Record...'
/opt/nekiro/backend.test -test.v -test.run '^TestInvokeToRecordAcceptance$'

echo
echo 'NeKiro evaluation result: PASS'
jq -r '"  platform_api: /v1\n  root_task_id: \(.rootTaskId)\n  parent_invocation_id: \(.parentInvocationId)\n  trace_id: \(.traceId)\n  explicit_failure: \(.explicitFailure)\n  components: \(.components | join(", "))"' "$NEKIRO_EVALUATION_SUMMARY_FILE"
echo 'NeKiro evaluation: disposable containers, volumes, credentials, and PKI will now be removed.'
