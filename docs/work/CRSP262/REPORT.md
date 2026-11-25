# CRSP262 report: metrics export (gap G2)

Branch: CRSP262-metrics-export. Not part of the PR.

## Steps
1. [done] internal/metrics: snapshot source, Prometheus text, OTLP/HTTP JSON push, token handler, config, scrape text, service. Unit tests pass (Linux).
2. [todo] Controller wiring (settings store, secret token, Source adapter).
3. [todo] API routes + OpenAPI + web settings screen.
4. [todo] Spec + change note, docs_check.
5. [todo] CI on branch, rename to CRSP262-integration, PR.

## Platform
Windows and Linux: tested. macOS: compiled and unit-tested in CI only, not run on a real Mac. Package is pure Go.
