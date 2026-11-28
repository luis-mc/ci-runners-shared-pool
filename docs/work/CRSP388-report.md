# CRSP388 / CRSP389 report (branch CRSP388-repair-and-fast-runners)

## CRSP388 (in progress)
- Cause 1 confirmed: `PlanChanges` skipped a repo whose pool already matched, so Repair (sent as pool on) gave ErrNothingToChange. Now the API takes `pool: repair` and plans variable writes/deletes from a fresh GitHub read.
- Cause 2 confirmed: `ReconcileVariables` left status missing/wrong after a confirmed write. Now OK at once (audit keeps the note).
- Cause 3: delete guard. Capacity comes from machine facts (Docker backend usable), not runner records; a first absent reading could be stale for hours. Added: runner-record presence guard (Config.RunnerOS, wired from the runner store) and AbsentGap reset. I could not see the owner's logs, so the exact trigger is inferred, not proven.

## CRSP388 update (done)
- Repair: API takes `pool: repair` (OpenAPI, Go server and client, TS regenerated). `planRepair` reads GitHub fresh: write step per missing or wrong variable, delete step (needs a yes) per variable the policy turns off or a size/old name crsp wrote; nothing differs means the plan is stored `done` with no steps and the wizard says "Nothing to repair: the variables are already right." with rows `done`.
- Restore: `ReconcileVariables` records OK right after the confirmed write; the audit entry is the note.
- Delete guard: `Config.RunnerOS` (runner records of any state keep their OS variable) and `AbsentGap` (30 min) so a stale first reading cannot satisfy the 15 min hold. Exact trigger on the owner PC is inferred from code, no logs seen.

## CRSP389 (done)
- `ListView` answers from stored records plus a fact cache (`internal/runners/factcache.go`); `FactsLoop` (20 s, started in buildController) refreshes GitHub lists and agent scans with at most 4 at once; a request older than 15 s starts one refresh and does not wait. No staleness field in the runner API, none added (documented in runners R5.79: about 15 to 35 s). `GetView` stays live. CRSP378 events are not in this branch, so not reused.
- Console: SetupSummary cards load on their own; Pool setup tiles already did.
