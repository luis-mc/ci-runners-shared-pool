# OPS81 report: crsp timers with a 30 s grace

Branch OPS81-crsp-timers, from origin/main plus merge commits of OPS73 and OPS74 (kept as merge commits).

## Steps
1. [done] Hold cap 2 min (`slots.MaxHoldMin`, new constant), machine default 2 min, timeout-minutes guideline 60 (`runner.slots`, `workflow.timeout`).
2. [done] `internal/timers`: settings + check-while-typing, `.crsp.yml` per-job override, grace engine (fake clock/agent/GitHub), adapters, API service.
3. [done] Migration 00039, store, GitHub run cancel, scheduler lifecycle hook (job start = gate's first go), wiring in `cmd/crsp/timers_wiring.go`.
4. [done] OpenAPI + codegen (no drift), handlers, authz table, API tests.
5. [done] Scheduling spec (R5.99 to R5.106, R5.75, R5.1, R5.20, test table), doctor spec (R5.60, timeout row), change notes.
6. [done] Draft PR #380, `ci.yml` dispatch run 44972052066 green on 5423be4.

## Checks run
- unit tests: timers, api, github, scheduler, store (with -race), doctor, insights, cmd/crsp: pass.
- go vet linux and windows: clean. darwin: clean for every touched package; `cmd/crsp` darwin cannot be vetted on this Linux host because `fyne.io/systray` needs cgo (same on the base).
- golangci-lint v2.15.0 on the whole tree: 0 issues. fast_guards: pass. codegen drift: none.
- docs_check: only W1 (reports under docs/work on a branch; stripped at the join, by design).

## Decisions and deviations (owner please read)
- **Tables, not columns.** The clocks are in new tables `job_clocks` and `run_clocks` (migration 00039), not ALTER columns on `jobs` and `runs`: added columns change the sqlc row types that `internal/store/jobsdata.go` and `insights_jobs.go` convert between, and those files are shared with other work. The fields are the ones asked for. `repository_id` is 0 (the scheduler names a job by `owner/name`); the console joins by job id, run id and repository name.
- **Agent stop-runner command already exists** (`StopRunner`, through `StopService` of the agent link). No new agent command; `timers.AgentAdapter` uses it. The runner ref is the runner's service name, looked up from the runners service at start.
- **GitHub permission.** Cancelling a run needs the App's Actions permission at *write*; `github.RequiredPermissions` has `actions: read`. Not changed here (github-integration area, OPS80 territory). Until granted GitHub answers 403; the engine records `timer.run.cancel_failed`, retries every 30 s, and `github.ErrCancelNeedsWrite` names the cause. Needs an owner decision and an App permission re-accept.
- **Job start** is the gate's first go (`scheduler.Lifecycle.JobStarted`, new `internal/scheduler/lifecycle.go`, small hooks in gate.go and release.go). The `.crsp.yml` per-job override is parsed (`timers.ParseFile`, `timers: jobs: {pattern: minutes}`), but nothing reads the file at job start yet: `Hook.LimitMin` is nil in the wiring. A repository file source belongs with the stack scan reader (OPS67/OPS80).
- **Run end** (`Engine.RunEnded`) has no caller yet: it needs the run-completed webhook (OPS80 owns it). Until then a run clock only ends by the cancel or stays open.
- **Extend for a run**: the brief says "once per job"; I gave the run the same single extension (`ExtendRun`, `POST /runs/{id}/timer/extend`). Easy to remove.
- **Queued and start-up timers**: types, settings, `QueuedHandler`/`StartupHandler` interfaces and the sweeps `Engine.SweepQueued`/`SweepStartup`. Not wired: OPS80 supplies the handlers and calls the sweeps at the join. Queued and start-up durations of a job come in through `JobStart` from OPS80's code.
- `workflow.timeout` lives in `internal/insights/workflows` (limit constant 30 to 60); I changed it and its tests, and the one stale "over 30" line in the ci-run-insights spec.
- Requirement numbers R5.99 to R5.106 (the scheduling spec had none free from R5.99).
- Commit trailers: none, as the brief says (no Co-Authored-By, no attribution).

## Not done / follow-ups
- Console screens (LOOK56), `.crsp.yml` source, run-end caller, queued/start-up handlers, App permission (see above).

## CI
- `ci.yml` (workflow_dispatch) on 5423be4: success.
- `dev-standards` (PR run) is red only for docs_check W1: reports under `docs/work/` on the branch (mine and OPS74's), stripped at the join by design. No other error.

CODE DONE 5423be4789b977a70e05c34183fdc630902dd6c6

## Follow-up: crsp timers start in OBSERVE mode (owner decision 22 Nov 01:20)
- New timer setting `enforce` (bool), default false on new install and upgrade (`Parse` reads a saved set without it as false). Exposed in openapi `TimerSettings`/`TimerSettingsInput`, codegen regenerated with `go tool task codegen` (no hand merge), handler maps it.
- Observe: grace still runs and shows ("would stop in 30 s"); when the grace ends the engine writes `timer.job.would_stop` / `timer.run.would_cancel`, sets the note (`would stop at 30:00 (timers in observe mode)` / `would cancel at 45:00 ...`), sends no stop and no cancel; the clock returns to `running` carrying that note, which is the "reported once" marker (survives a restart, no schema change). Stop now, Cancel now and Extend act in both modes. Enforce: unchanged.
- Doctor: new check `timers.mode` (info in observe mode, link /settings, never warn). Docs: scheduling R5.107, doctor R5.65, content.md "Timer mode words", CHANGES C13, change notes edited (the notes for OPS81 already existed, so edited instead of `docs_fold note`).
- Deviations: (1) brief said OPS81 added a doctor timer check file; none exists, so I added `internal/doctor/checks/timers.go` and the small wiring it needs (`doctor.Env.Timers`, `wire.DoctorDeps.Timers`, `cmd/crsp/areas_wiring.go`, `Service.TimersEnforced`) plus the check-count tests (52 to 53, 54 to 55). (2) `internal/api/handlers_timers.go` and the generated files changed to map the field. (3) The run's note is on the run clock only; open jobs of the run get no note. (4) The console Settings screen does not exist yet (LOOK56); the doctor link is `/settings`.
