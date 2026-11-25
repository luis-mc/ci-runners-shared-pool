# CRSP240 report

Task: `TestMaintainReleasesWhenCancelledWhileRunning` flaky under `-race` (maintain exited 0).

## Root cause
Test race, not a bug in `maintain`. The fake handler cancelled, then waited for the
server's `r.Context().Done()` before answering. That is no proof the client saw the cancel.
Under CPU load the answer still reached the client (reproduced: ~7 fails per 1500 runs with
`GOMAXPROCS=2` and 4 busy loops), so `maintain` got a 200 and rightly exited 0
(a command that finished is a finished command).

## Fix (test only)
Handler cancels, then blocks on a `release` channel that the test closes after `f.run`
returns. The client can never see the answer; the cancel is the only way out.
Same change in `TestMaintainReleasesWhenCancelledWhileDraining`, which had the same pattern.
No sleeps, no retries.

## Checks
- `go test -race -count=200 -run TestMaintain ./internal/cli`: ok.
- Under load (4 busy loops, GOMAXPROCS=2): 2000 runs of both cancel tests, 0 fails; before the fix, fails.

Docs: none, test-only. No behaviour change, no change note.

PR: see below.
