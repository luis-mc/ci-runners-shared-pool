# CRSP248 part B (controller side), M1

## Done
- `internal/depsetup/offer.go`: `BuildOffer` (needs per spec 3.3, why sentences, choices with facts, one recommended, licence tick, manual steps, start_only, blocked reasons: admin, disk = sum of disk_bytes + 1 GiB, virtualization; DEP16 ready when no need or engine ok).
- `internal/depsetup/job.go, memrepo.go, service.go`: job state machine, `Check`, `Start` (no agent call before `confirmed`; audit `dependency.consent`), `Get`, `List` (newest first, 20), `Stop`, `Undo` (recorded list only; audit `dependency.undo`), `RestartHost` (paused only, confirmed, refuses running jobs; audit `dependency.restart`), `HandleEvent` (300-event ring, final outcomes, added, engine_version), `OnAgentConnected` (resume with recorded added list).
- Store: migration `00044_dependency_jobs.sql`, `internal/store/deps.go` (`Store.DepsJobs()`).
- Agent link: `internal/agentlink/deps.go` (SendCheck/Install/Undo/Restart/Stop, gated on `dependency_setup`, "update the agent first" error; DepsReport and DepsEvent routed; resume hook on machine report). Small edits in `agentlink.go` (Deps field, pendingDeps map, two hooks in OnMessage).
- API: `internal/api/handlers_dependencies.go` replaces the stubs (+ `Options.Deps`).
- Wiring: `cmd/crsp/machines_wiring.go`, `areas_wiring.go`, `main.go`.
- Doctor DEP14: `machine.engine` fix keeps its text, adds a sentence and link `/machines/<id>?setup=deps`.

## Not done / notes
- Stop: agent.proto has no stop message, so `Link.SendStop` returns "this agent cannot stop an install in progress yet" (409). Needs a `DepsStop` in the proto to finish.
- `DependencyEvent.pct` is nullable in openapi but generated as a plain number: unknown pct is sent as 0 (the `reason` says why).
- Docker Desktop in `pinned.json` has no URL or SHA-256 yet; it is offered, and the agent will refuse it until part A pins it.
- Resume of `paused_restart` happens on any returning agent report, not only after Restart now (a plain reconnect before the restart would verify early and fail; retry works).
- Doctor Fix has one `Link` string and no label; the label "Get this machine ready" is in the fix text.

## Tests
`go test -race ./internal/depsetup/... ./internal/agentlink/... ./internal/store/... ./internal/api/... ./cmd/... ./internal/doctor/...`; gofmt, go vet, golangci-lint clean on touched packages.
Docs: no feature spec or change note written here (orchestrator).
