# OPS74 report: runner classes, admission, reservation, memory cap (back end only)

Branch `OPS74-classes-admission`. Steps (pushed after each):

1. Class table, presets, hard rules, hints, pool fit, lowering confirm: `internal/runners/sizeclass_table.go`. DONE
2. Memory cap behind one interface with fakes: `internal/memcap` (cgroup, job object, watcher). DONE
3. Admission, reservation, backfill: `internal/scheduler`. IN PROGRESS
4. Store, migration 00037, API, openapi, codegen. TODO
5. Spec R5.89 and up, change note. TODO

## Status (final)

Done and pushed: steps 1 to 5. Checks run: go test of touched packages, `go vet ./...` (linux, windows; darwin vet of touched packages, the full darwin vet fails in `fyne.io/systray` without cgo, not touched), golangci-lint 2.15.0 on touched packages, codegen drift (sqlc models regenerated for 00037, API and TS clean), docs_check (only W1: this report, stripped before the join), fast_guards.

Built: R5.89 to R5.96 and R5.98 (controller side). Files outside the owned list that I touched, each a small addition: `internal/store/schedadmit.go` (new) and 1 line in `schedstore.go` (the store side of 00037), `internal/api/authz.go`, `server.go`, `handlers_jobs.go` (route, option, over_memory wait word), `cmd/crsp/areas_wiring.go` + new `classes_wiring.go` (wiring), `apps/web/src/screens/jobs/words.ts` (two words for the new wait reason, needed by the typecheck; the words are not yet in design-system `content`), `internal/runners/sizeclass_service.go`.

NOT built (for the owner to decide):
- Agent starts the `memcap` guard when a job takes its slot and calls `ReportOverMemory` (needs the agent protocol: `Decision.Class` and `MemoryCapGiB` are in the gate answer, not yet in `agent.proto`).
- Forecast source for backfill (a) in production: `scheduler.WithForecast` is set by tests only. The gate runs inside the SQLite transaction, so a live read of job samples there needs a cached forecast; without it backfill uses rule (b) only and the expected start reads "not known yet" when a held job has no forecast.
- Doctor check for a reserved job no runner can run (`admit.CanEverRun`, `UnservedText` are ready; doctor package is not in my list).
- A `WithZone` wiring from `tz` setting: wait lines use UTC until wired.
- Docs M1 for runners, cli and web-console specs: covered by `Docs: none` in the commit message (runners spec belongs to OPS75).
- No console screens.

CODE DONE 061b556a6e0826d281ef06dd497c4d38d0f51a48
