## Summary

<!-- Describe the assembly, manifest, or acceptance behavior changed. -->

## Component revisions

| Component | Before | After | Compatibility evidence |
|---|---|---|---|
| Core | | | |
| Console | | | |
| Go SDK | | | |
| Samples | | | |
| Transport | | | |

## Verification

Commands or workflows run:

```text
go run ./cmd/manifest-validator components.json
docker compose --file compose.yaml config --quiet
go test -tags=e2e -count=1 ./tests/backend
# production Console Playwright suite through Stack CI
```

Observed success signals:

<!-- Record manifest, Compose, backend loop, browser, and Ledger lineage evidence. -->

## Security and failure semantics

- [ ] Every component uses a full commit, immutable SemVer tag, or image digest.
- [ ] No source `build:` block, floating branch, `latest`, local path, or alternate component was introduced.
- [ ] Logs are sanitized and required credentials/configuration remain explicit.

Fallback delta: removed 0, retained 0, added 0, net 0

Added fallback evidence: none

## Checklist

- [ ] Backend acceptance proves Register → Discover → Install → Invoke → Record.
- [ ] Cross-runtime parent/child Invocation lineage is queryable.
- [ ] Browser acceptance uses the exact production Console checkout.
- [ ] README and component manifest describe the resulting supported assembly.
