# CRSP262 report: metrics export (gap G2)

All steps done. CI (ci.yml, dispatch on the branch, head 5e585b6) green after two fixes (openapi prettier, golangci-lint; an absolute address in help text).

1. internal/metrics: pool source, Prometheus text, OTLP/HTTP JSON push, bearer key, config, scrape text, service, settings and key stores. Unit tests (Linux, race).
2. Controller wiring (cmd/crsp/metrics_wiring.go), API routes + OpenAPI + generated Go/TS, authz tables.
3. Settings > Console > Metrics screen, shared parts only, vitest.
4. Observability spec R5.70 to R5.77, change note by hand (luismc-docs scripts not installed in this sandbox: docs_fold.py and docs_check.py NOT run).

Limits: Windows not run here (only Linux ran locally; CI test-os covers Windows and macOS). macOS compiled and unit-tested in CI only. Local darwin build of cmd/crsp fails on cgo systray (unrelated, needs a Mac toolchain). Layout and wizard enforcement kit not run locally (CI e2e covers). Fallback count is derived from hosted labels on jobs with no crsp machine. Machine uptime is time since the controller saw the machine online.
