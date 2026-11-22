# OPS80 report: runners per job before assignment

Branch OPS80-before-assignment: merge commits of OPS73 and OPS74 on origin/main, then OPS80 code.

## Built
- `internal/assign`: Controller (queued, in_progress, completed, vanished, sweep), leases (`assign.Store`, `MemStore`), Poller (one for all repos, 30 to 60 s, at start), Events (webhook worker), warm runner, 12 h alert and 24 h cancel, unused-runner deletion (start-up + 2 min), label plan for persistent runners (`PlanLabels`, `SyncLabels`, interface `RunnerLabels`).
- `internal/scheduler/room.go`: `CheckRoom`, the admission entry point (class, pool CPU and memory, load, reservation and backfill, warm runners released first). Console reason `waiting for room on M1: needs 6 CPU, 5 free`.
- `internal/runners`: routing labels pass `ValidateLabels`, 100-label cap, `JobRunner.OS` (Linux container, Windows process, macOS VM), `StartupAllowance` (macOS 60 s).
- `internal/store`: migration 00038 (assign_leases, assign_warm, assign_labelsets), `AssignStore` (INSERT OR IGNORE and one UPDATE are the compare-and-set).
- `internal/github`: jobs list of each run is ETag-conditional (fake serves ETag).
- `internal/api`: `GET /api/v1/assign/leases` (listAssignLeases), codegen regenerated.
- `internal/doctor/checks/assign.go`: `repo.runson_always`, `assign.unserved`, `assign.waiting` (`RegisterAssign`, `AssignChecks`; not in `Register`, so the check counts that OPS81 may change stay untouched).
- Specs: runners R5.125 to R5.133, github-integration R5.109 to R5.112, test tables, change notes.

## Not done (outside ownership, for the join)
- Wiring in `cmd/crsp/demand_wiring.go`: build `assign.Controller` over `store.Assign()`, `runners.Service`, an `assign.Env` (machines, repos, sizes), `assign.Events` as the webhook `JobDetailEvents`, `assign.Poller`; stop the old `demand.Loop` when it runs, or two controllers would each make a runner. `Env.Requirement` should read `slots.Settings.RequirementOf`; `Env.Machines` the pool, load budget and class ceiling.
- Doctor: pass `checks.AssignChecks(controller)` in `DoctorDeps.Extra`.
- `docs_check` W1 (docs/work reports on the branch) is expected until `docs_work.py strip` at the join.

## Checks run
go vet linux, windows, darwin; golangci-lint on touched packages; tests of assign, scheduler, runners, store, github, api, doctor, repos, routing; codegen drift none; docs_check (only W1).
