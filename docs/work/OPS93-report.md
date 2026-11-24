# OPS93 report

- Fix: `windows.ConfigureArgs` puts `configure` before `persistent.ConfigArgs`; Register uses it. Linux/macOS call config.sh (adds the verb itself), unchanged. Remove paths already pass `remove`.
- Tests: unit test checks CRSP_ARGS starts `["configure","--unattended"`; all scripts read only known CRSP_* vars (Linux); Windows-only: ParseInput of every wrapped script, and real run of the read-only scripts (detect, find, usage, removeAccount, listeners, workers, tail, endListeners).
- Review of later steps (account, rights, share tools, hooks, ACL reset, lock, logon check, register/start service, stop/remove): no further defect provable from code; none changed.
- Not verified (needs admin Windows): LSA rights script, LogonUser check, `Set-Acl` with a fresh DirectorySecurity (no owner) in scriptLock, `Get-LocalGroupMember` on a group with orphaned SIDs under PS 5.1, New-Service with credential, RunnerService.exe start, scriptRunner against a real Runner.Listener.
