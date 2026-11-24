# OPS92 report: PowerShell stdin blank line

## Changed
- `internal/backend/windows/errtext.go`: `wrapScript` ends with `}\n\n`; `dropBlankLines` removes blank lines inside the script (a blank line inside the open `try {` would end it early in `-Command -` mode).
- `internal/leftovers/os_windows.go`: `psStdin` ends the script with `\n\n`.
- `docs/features/runners/spec.md` R5.87 + change note OPS92 (patch).

## Tests
- `ps_stdin_test.go` (all OS): stdin ends with one blank line, no blank line inside for account, rights, logon, lock scripts.
- `ps_windows_test.go` (windows): real powershell.exe runs `wrapScript("Write-Output 'ready x'")`; script with inner blank lines; throw gives exit 1 and `CRSP-ERROR: boom` on stderr.
- `internal/leftovers/os_windows_test.go` (windows): stdin ends with a blank line.

## Script review (step 3)
Read the register, account, lock, rights, logon, listener scripts. Nothing provably wrong on PowerShell 5.1 from code (cmdlet parameters exist in 5.1, C# has no C# 6+ syntax). Not provable here (no powershell on this box): every script now really runs for the first time, so the first real Windows add is the test.

## CI
(pending)
