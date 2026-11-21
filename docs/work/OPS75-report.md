# OPS75 report: Windows and macOS tool store (tier 1, back end only)

Branch `OPS75-tool-store` from origin/main (RELEASE-52, 085af09b).

## Built
- `internal/toolstore` (new): `plan.go` (tool plan from stack files, version files, `.crsp.yml` `tools:`), `managers.go` (pure install recipes per OS: rustup, nvm/nvm-windows, pyenv/pyenv-win, SDKMAN, official archives, dotnet-install, xcodes; job layout), `store.go` (read-only store, install once via staging, markers, usage, prune), `exec.go` + `unpack.go` (recipe runner, SHA-256, safe unpack), `jobhome.go`, `wipe.go`, `cache.go` (key, trust, restore, save-new-key-only), `probe.go`, `fake.go`; OS files `seal_*`, `clone_*`, `identity_*`, `tree_*`, `fetch.go`.
- `internal/agent/toolstore.go`: `JobToolFlow` (Prepare = steps 1-5, Finish = 6-8, ProbeToolPlan, Housekeep).
- Doctor: `runner.tools.plan` (doctor R5.65), `doctor.Runner.ToolPlan`.
- Spec: runners R5.110 to R5.119 (+ open questions 66, 67), doctor R5.65, change notes, generated docs rebuilt with `docs_index.py build`.

## For the join
- Hard-coded check counts in `internal/doctor/checks/checks_test.go` (51 to 52) and `internal/doctor/wire/wire_test.go` (51/53 to 52/54) conflict if another branch adds a doctor check.
- Generated docs files (`docs/index.html`, `llms.txt`, `features/index.md`, `architecture/decisions.md`, `docs-map.json`) were rebuilt by the tool: regenerate at the join.
- New API endpoint needed (not written): a setting for the housekeeping days (`tool_store_unused_days`, default 30) on machine settings; and an agent link message for the scan input (stack files + `.crsp.yml` text) and the `ToolPlanReport`. Both are listed in the spec under "Not built yet".
- Runner hook / backend call of `Prepare` and `Finish` is not wired (no console, no backend edits in this task).

## Not verified
- The real install, seal (icacls), ReFS block clone, `clonefile`, registry and keychain wipe and `taskkill` paths are compiled and vetted only; no Windows or macOS machine ran them.
- Tests never download, never run a manager and write only in `t.TempDir()`.
