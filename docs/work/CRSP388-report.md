# CRSP388 / CRSP389 report (branch CRSP388-repair-and-fast-runners)

## CRSP388 (in progress)
- Cause 1 confirmed: `PlanChanges` skipped a repo whose pool already matched, so Repair (sent as pool on) gave ErrNothingToChange. Now the API takes `pool: repair` and plans variable writes/deletes from a fresh GitHub read.
- Cause 2 confirmed: `ReconcileVariables` left status missing/wrong after a confirmed write. Now OK at once (audit keeps the note).
- Cause 3: delete guard. Capacity comes from machine facts (Docker backend usable), not runner records; a first absent reading could be stale for hours. Added: runner-record presence guard (Config.RunnerOS, wired from the runner store) and AbsentGap reset. I could not see the owner's logs, so the exact trigger is inferred, not proven.
