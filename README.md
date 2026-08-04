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

Production Compose contains no `build:` block. Browser acceptance runs from
the exact Console checkout prepared from the manifest, against the same
Gateway-only route used by production.

Cleanup is explicit and recoverable at the database boundary:

```bash
docker compose --project-name nekiro-stack --file compose.yaml down --volumes --remove-orphans
```

The preparation work directory remains caller-owned and is never deleted by a
script. Remove it only after checking the exact absolute path.

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
