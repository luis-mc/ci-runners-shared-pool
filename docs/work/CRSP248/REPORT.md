# CRSP248 report

Started 2026-11-25 13:05 UTC. Branch CRSP248-dependency-setup-m1. Owner decisions D-DEP-1..11 at recommended defaults.

## Done
- [x] Read spec, rules, code map
- [x] Contract (13:20 UTC): agent link messages DepsCheck/DepsReport/DepsInstall/DepsUndo/DepsRestartHost/DepsEvent + `dependency_setup` report flag; 7 API routes under /machines/{id}/dependencies; pinned list `internal/depsetup/pinned.json`; CRSP247 `progress.ts` taken as-is (identical file, drops on merge).
- [x] Local tools: pre-push hook needed gitleaks, golangci-lint built with go1.27 and Node 24; installed in /tmp (not in the repo). No hook bypass.

## Running (3 agents, disjoint files)
- A agent side: detect, install package behind fakes, undo, events
- B controller: offer, job state machine, store, link, API, doctor link
- C web: wizard "Get this machine ready" and doors

## Open
- Docker Desktop installer URL/SHA-256 not pinned unless a real published checksum is readable; empty hash = agent refuses, wizard shows "Do it myself".
- CRSP247 Terminal/ProgressBar/DownloadProgress parts: not landed at 13:20 UTC; wizard wired to the contract.
