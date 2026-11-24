# OPS89 report: ephemeral Linux runner on a Windows host

Branch `OPS89-ephemeral-linux-on-windows`. Source: GameRoomPC, crsp 0.1.24, job 138144007503 of luis-mc/lan-link.

## Root causes

1. **`chown` not found.** `Backend.ownLane` (`internal/backend/container/identity.go`) ran `chown -R uid:uid <lane>` on the host. A Windows host has no `chown`, and NTFS has no uid. Fix: on a Windows host `ownLane` does nothing; the engine owns the bind mount. Linux hosts keep the chown (R5.72). `Config.HostOS` selects the host in tests.
2. **409 Already exists.** `internal/demand` named the runner `crsp-job-<job id>` for every try. `StartJobRunner` got a JIT config (this creates the GitHub registration), the agent start failed, and nothing deleted the registration, so the next try asked for the same name. Fix: the name is `crsp-job-<job id>-<try>` (R5.127 already says so), and `StartJobRunner` deletes the registration when the start fails after the JIT config was made (not for a scale-set JIT).
3. **Endless retry, many rows.** Each try inserted a new Failed runner record and nothing removed it. `maxFails` (5) existed, but a repeated `queued` event made a fresh `waiting{}`, so the count restarted. Fix: `JobRunner.JobKey` + `Service.ClearFailedStarts` (a try replaces the earlier failed row of its job; the end of the job removes it); `Loop.gaveUp` keeps a left job out until it completes.
4. **"no repository uses the pool yet" on idle ephemeral runners.** `poolViewBase` (`internal/runners/pool.go`) used that text whenever a pool runner had no hidden registrations. An ephemeral pool runner is a slot and never has any (JIT runner per job). Fix: `poolIdleReason(kind)`: ephemeral says `a container starts for each job of the pool`.

## Files

`internal/backend/container/identity.go`, `internal/demand/demand.go`, `internal/runners/jit.go`, `internal/runners/pool.go`, tests beside each, `docs/features/runners/spec.md` (R5.150) and its change note. No file of OPS87 or OPS88 touched.

## Proof

Tests: TestOwnLaneNeverRunsChownOnAWindowsHost, TestFailedStartDeletesTheRegistration, TestOneFailedRowForEachJob, TestEachTryHasItsOwnName, TestRetryLimitHoldsAndFailedRowsClear, TestEphemeralPoolRunnerViewDoesNotBlameTheRepositories. golangci-lint v2.15.0 clean (linux and GOOS=windows); vet and `test -c` with GOOS=windows pass for the three packages.

CODE DONE 3e81147e47e3a62218f17015edd46d01fe460fb6
