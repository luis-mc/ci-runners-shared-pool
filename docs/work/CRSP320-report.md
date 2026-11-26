# CRSP320 report
Flake 1: TestControllerMachineIsListed (internal/machines), `TempDir RemoveAll cleanup: directory not empty` under -race (CI runs 45806200097, 45736294647).
Root cause: agent.runSession started 6 loops and several per-command goroutines with bare `go`; Run returned while they still wrote into DataDir (health-state.json, settings, caches). The test removed the dir meanwhile. Fix: runSession cancels and waits for them (link.spawn + WaitGroup).
Flake 2: Windows internal/mirror: no failure found in the last ~300 CI runs (all lanes); not reproduced. No change made; see PR.
Runs (Linux, -race): machines+agent -count=20 ok, mirror -count=20 ok.
