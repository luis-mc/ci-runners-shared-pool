# OPS94 / OPS95 report

Status: BLOCKED, no code changed. No PR opened.

Searched tracked sources (git grep, all branches' history for `pool-add-agent`):
`pool-add-agent`, `pool-refresh`, `could not compare`, `already added`, `agents.json`.
Only hits: docs/project/registry.json (OPS94/OPS95 entries, "Source: ... dev-standards-03"),
docs/architecture/dev-standards-handover.md (mentions pool-refresh behaviour), project HTML.
No script or Go code with these names exists in this repo. `images/persistent/add-runner.sh`
and `internal/backend/container/refresh.go` are different tools (runner add; container refresh) with
no `/agents.json` or image-id compare.

Conclusion: `pool-add-agent` and `pool-refresh` live in the dev-standards / pool main
repo (the registry source), not in ci-runners-shared-pool. Fix cannot be made or tested here.

Needed from owner: attach that repo (path/name) or confirm the fix belongs to refresh.go.
