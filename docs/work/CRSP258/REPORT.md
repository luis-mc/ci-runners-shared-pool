# CRSP258 rescue report (branch CRSP258-notifications-2)

Re-applied the 8 commits of origin/CRSP258-notifications onto origin/main f909588f.
- Conflict cmd/crsp/areas_wiring.go: kept metrics wiring from main, notify service now built once and passed to config-as-code.
- Migration 00044_notices clashed with main's 00044_dependency_jobs: renamed 00047_notices.sql; spec updated.
- Generated Go (oapi-codegen api+client, sqlc) regenerated; schema.d.ts auto-merged, NOT regenerated (needs Node >=24; install denied).
- go build ./..., go test notify/store/api/cmd: green.
- Not run: web typecheck/tests (Node 22 only), docs_check.py/docs_fold.py (luismc-docs scripts not on this machine).
