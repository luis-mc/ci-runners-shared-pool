# OPS81 report: crsp timers with a 30 s grace

Branch OPS81-crsp-timers, from origin/main plus merge commits of OPS73 and OPS74.

## Steps
1. [done] Hold cap 2 min (`slots.MaxHoldMin`), machine default 2 min, timeout-minutes guideline 60 (doctor `runner.slots`, `workflow.timeout`).
2. [todo] `internal/timers`: settings, grace engine, fake-clock tests.
3. [todo] Migration 00039, store, GitHub run cancel, agent stop adapter.
4. [todo] OpenAPI + codegen, handlers, audit.
5. [todo] Specs, change notes, checks, draft PR, CI.
