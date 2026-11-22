# OPS81 report: crsp timers with a 30 s grace

Branch OPS81-crsp-timers, from origin/main plus merge commits of OPS73 and OPS74.

## Steps
1. [done] Hold cap 2 min (`slots.MaxHoldMin`), machine default 2 min, timeout-minutes guideline 60 (doctor `runner.slots`, `workflow.timeout`).
2. [done] `internal/timers`: settings, .crsp.yml override, grace engine, adapters, fake-clock tests.
3. [done] Migration 00039 (tables job_clocks, run_clocks: ALTER on jobs/runs breaks sqlc struct casts in code OPS80 may touch), store, GitHub run cancel (needs App Actions: write, see notes). Agent stop: existing StopRunner via StopService.
4. [todo] OpenAPI + codegen, handlers, audit.
5. [todo] Specs, change notes, checks, draft PR, CI.
