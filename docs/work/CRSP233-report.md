# CRSP233 report

Branch CRSP233-doctor-windows-crashes, from origin/main.

## 1. Doctor checks crash
- Cause: not in the checks. kvm.module, kvm.wslconfig and machine.engine are the only checks fed by `reportSources` (cmd/crsp/remote_wiring.go), which `areas_wiring.go` built with `doctorFleet{machines: d.Machines}`. `doctorFleet.Machines` called `f.runners.ListRunners` on the nil runner store: nil pointer, turned into "Check crashed: runtime error" by `Engine.runCheck`'s recover.
- Fix: `doctorFleet.Machines` skips the runner facts when it has no runner store; the wiring now passes the one full fleet to the KVM and engine sources.
- Tests: `cmd/crsp/doctor_fleet_test.go` (red before, green after); `internal/doctor/checks/nocrash_test.go` runs every registered check (Register, RegisterKVM, RegisterVM, HealthChecks) against an empty report, a Windows report (no KVM facts, Docker Desktop) and a Windows machine without reports, and asserts no panic; a Windows PC with no KVM facts gets skip with a reason from the two reported KVM checks.
- Note: the task text says KVM checks on Windows give skip because KVM is Linux only. The spec (machines R5.53) keeps kvm.wslconfig and kvm.module for WSL2 when the agent reports facts, so I left that: with no facts they skip with a reason; with facts they report. The checks themselves did not crash.

## 2. Controller log stops after an update
- Cause not found from the code. Candidates found: `openServiceLog` returned nil on any failure (folder or file unwritable after the MSI replaced permissions, or locked) and the service then logged to its invalid stderr, so every line vanished without a trace. Retention does not touch a live crsp.log (top folder, age only).
- Fix: `openLogFile` (cmd/crsp/servicelog.go) tries `logs\crsp.log`, `logs\crsp-<pid>.log`, then `<temp>\crsp-service-<pid>.log`; the controller's first log line names the file (`controller log path=...`) and warns why an earlier file was not used. Tests in cmd/crsp/servicelog_test.go.
- Next step on the PC: after the next restart read the first line of the newest crsp*.log under C:\ProgramData\crsp\logs, or crsp-service-*.log in the service account's temp folder; the warning line gives the real cause.

## 3. Runbook
- docs/guides/real-machine-test.md already had CRSP_RUNS_ON_* in steps 4, 7, 8 on origin/main. Step 8 now shows the per-job form `vars.CRSP_<JOB> || vars.CRSP_RUNS_ON_<OS> || '"ubuntu-latest"'`.
- Other plain RUNS_ON_ in docs/guides and docs/features: only change notes (history), the removed-requirement text and the legacy `CI_RUNS_ON` names (move-to-crsp.md, intended). speed-comparison-workflow.md `RUNS_ON` is an env key in the workflow, not a variable.
- Step 7 light-runner lines match the code (light runners removed, D110/OPS49; `crsp-light-*` only migrated away by internal/runners/legacy_light.go).

## Docs
Change notes: features/doctor and features/operations (CRSP233); spec lines in doctor R5.32 and operations R5.41.
