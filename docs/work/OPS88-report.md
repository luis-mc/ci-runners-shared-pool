# OPS88 report

## Root cause (A): "stale state"
There was no stale record. A pool runner (`gameroompc-pool-1`) has no account of its own: the pool sync adds one registration per repository, named `<pool>-r<repository id>` (`regName`, `internal/runners/pool.go`), and the agent derives the service, account and folder from that registration name (`runners.AccountName`, `Backend.Folder`). So every attempt used `gameroompc-pool-1-r1378816711`, account `crsp-gameroompc-33e8` and its folder, deterministically. The plan text (`addText`) showed `AccountName` of the parent name, `crsp-gameroompc-da79`, an account that is never made. The Review step therefore disagreed with what the agent did, and removing the failed runner could not change a name the repository gives. The cleanup also said "Cleaned up: account …, folder …" for things already gone (the account script and `RemoveAll` succeed on nothing) and the message ended in a doubled "." because the cause already ends with one.

## Root cause (B): empty fallback
`scriptAccountSID` read `(Get-LocalUser …).SID.Value` and the Go side saw exit 0 with empty stdout, so a missing account, or a script whose output never arrives, looked like a PowerShell host fault. The account step (`scriptAccount`) had the same weakness: exit 0 with no output was taken as "account made", so the account may never have existed when the SID was read (the order create, then SID, was right, but the creation was never verified). The first failure ("No mapping") fits an account that was not there.

## Changes
- `scriptAccount` ends by checking the account exists and printing `account-ready <name>`; `makeAccount` requires that line ("<name> was not made" otherwise). Every attempt, also retries 2 and 3 after a cleanup that deleted the account, runs it before the SID is read.
- `scriptAccountSID` throws `account not found: <name>`; an empty answer is the error "account not found (powershell exited 0 but printed nothing…)".
- Cleanup: removes the account script prints removed/absent, folder is checked with `Exists`; only real removals are listed; the trailing "." of the cause is trimmed.
- Plan text of a pool runner names no account (one for each repository that uses the pool).
- `sid_windows.go` untouched, behind its build tag. Tests: `internal/backend/windows/ops88_test.go`, `internal/runners/ops88_test.go`; `sysfake.Commands.Fallback` added.
- Spec R5.58 and a change note.

Not changed: server-side discard of a failed registration with no service ref (the failed add's own cleanup already removed its account and folder).

CODE DONE 4c15af198ed1d0034060388509a5fe96d2a9745f
