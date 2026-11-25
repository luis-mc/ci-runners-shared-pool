# CRSP294 report
- Go: TestMaintainReleasesWhenCancelledWhileRunning failed under load (~1 in 10 with 4 parallel -race runs): the fake controller answered after the cancel, and the HTTP transport can still hand that answer back, so exit 0. Fix (test only): the handler hijacks and closes the connection, never answers. 600 runs under load pass. Product code was correct.
- Web: MachineLeavePool was lazy-loaded next to MachineRemove; a pane that appears after first paint grows the page under the sticky action bar (e2e scrolls to the end first) and adds a control late to the keyboard walk. Now imported straight. Could not run e2e locally (needs Node >=24, have 22); CI decides.
- Docs: none, no behaviour change (docs_fold.py is not present in this sandbox).
