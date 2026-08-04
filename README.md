# NeKiro Stack

NeKiro Stack is the only repository that assembles independently released
NeKiro components and proves the complete product loop. It owns the immutable
component manifest, Compose wiring, backend acceptance, production browser
acceptance, and sanitized product logs. It does not own or copy component
production source.

## Components

`components.json` records exact full commits for core, Console, Go SDK,
Samples, and transport. The transport tag must resolve to its declared commit.
The validator rejects missing owners, partial SHAs, branches, `latest`, local
paths, unknown fields, mutable tags, and mismatched image digests.

```bash
go run ./cmd/manifest-validator components.json
go test ./internal/manifest
```

To validate an unreleased or newly merged Core commit without changing the
tracked release manifest, render a temporary immutable manifest:

```text
go run ./cmd/manifest-validator -core-sha <40-character-core-sha> -format json components.json > prepared-core.json
go run ./cmd/manifest-validator prepared-core.json
```

The override updates both `components.core.commitSha` and
`contractIdentity`; every other component remains at its tracked immutable
revision.

## Prepare exact images

Preparation requires Git, Go, Docker, Bash, and network access to the public
component repositories. Supply three absolute paths; the work directory must
be empty and the output environment file must not already exist.

```bash
work_root=$(mktemp -d)
env_file="$work_root/prepared.env"
./scripts/prepare.sh "$(pwd)/components.json" "$work_root/checkouts" "$env_file"
source "$env_file"
```

The script fetches each exact commit into the caller-owned work directory,
verifies the resolved object and transport tag, runs `git fsck --full`, and
builds the Control Plane, Router, Runtime A, and Runtime B images from those
checkouts. It never follows a branch or substitutes an older component.

## Run the stack

Copy `.env.example` to an untracked file and explicitly supply every required
service setting documented by the core and Samples repositories. Source the
prepared image environment, then validate and start the stack:

```bash
docker compose --file compose.yaml config --quiet
docker compose --project-name nekiro-stack --file compose.yaml up --detach --wait --wait-timeout 120
go test -tags=e2e -count=1 ./tests/backend
```

## Test matrix and success signals

| Check | Command or workflow | Successful result |
|---|---|---|
| Manifest | `go run ./cmd/manifest-validator components.json` | All five component owners and immutable revisions validate |
| Compose | `docker compose --file compose.yaml config --quiet` | Configuration exits `0` with no `build:` or floating image |
| Backend acceptance | `go test -tags=e2e -count=1 ./tests/backend` | Register → publish → discover → install → invoke → record passes, including cross-runtime lineage |
| Browser acceptance | Console `pnpm test:e2e` in Stack CI | Every production Console scenario passes through the live Gateway |

A healthy container set alone is not acceptance. Backend success requires the
trusted publication loop, Router-mediated Runtime A/Runtime B invocation, and
queryable committed Ledger lineage. Browser success additionally requires the
exact production Console build to pass its Playwright suite against that same
assembly.

The `Core integration` reusable workflow accepts a full Core commit SHA,
renders a temporary immutable manifest, and runs both backend and browser
acceptance. Core calls it after every merge to `main`; the tracked Stack
manifest is not silently rewritten by that compatibility run.

Production Compose contains no `build:` block. Browser acceptance runs from
the exact Console checkout prepared from the manifest, against the same
Gateway-only route used by production.

Cleanup is explicit and recoverable at the database boundary:

```bash
docker compose --project-name nekiro-stack --file compose.yaml down --volumes --remove-orphans
```

The preparation work directory remains caller-owned and is never deleted by a
script. Remove it only after checking the exact absolute path.

## Pull requests

Pull requests must list every component revision changed, why the revisions
are mutually compatible, which backend/browser checks ran, and the observed
success signals. A green manifest-only check is not evidence that the product
loop passed.

## Provenance

The Stack history was exported from
`NeKiro-project/NeKiro@aad73c450435a9b6c76c26cc6c525fa811b0e7ad`.
The original `deploy/` tree is
`b9de236d41ebf3f507b9f49ca97f0a723b84182f`; the original `tests/e2e/` tree is
`5cd8d7931a73dc9db00067422ed9fffd22343099`; and the union export commit is
`9f0c933102f83ed24d13d64eb3e47ce5f3651c2f`. The source repository retains
the annotated tag `pre-repository-split-2026-08-04` for original commit and
signature provenance.

Licensed under Apache-2.0. See `LICENSE`.
