# CRSP382 report (RELEASE-72), in progress

Branch CRSP382-runner-reregister (from origin/CRSP329-integration).

## Causes
1. persistent.Decide only compared config: a deleted registration with unchanged config is "Already", nothing registered again.
2. The pool supervisor restarted a runner with a dead registration for ever (back-off 60 s) and never told the controller.
3. Runners page: pool runner views took the best child state; a runner whose service list said "stopped" hid the real cause; Creating had no timeout.
4. Windows "list listeners": scriptListeners used Join-Path, which throws for a folder on a drive that is not there and for an empty folder (exit 1 through the wrapper). Reproduced with real PowerShell.

## Done (commit c9ad6e3)
- backend/persistent/reglost.go, Found.RegistrationLost, Linux/Windows Find, container list.sh third field.
- Start: RegisterLost action in linux, windows, macos backends; add-runner.sh cleans stale files.
- Supervisor: .lost marker, no restart loop; list.sh reads the log too (older supervisor).
- runners/reregister.go: RecoverLostRegistrations (once), give-up with reason, ReregisterRunner, SettleStuckCreating; loop in cmd/crsp/sweep_wiring.go.
- Windows scripts fixed.

## To do
API endpoint + CLI parity + console button + design-system words + spec + change note + docs checks + PR.
