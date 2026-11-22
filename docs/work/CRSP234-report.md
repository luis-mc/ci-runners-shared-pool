# CRSP234 report

**Root cause.** On Windows the folder reset reads the SID of the new runner account with an in-process `LookupAccountName` on the bare name (`nativeLookupSID`, `windows.LookupSID("", name)`). When that call answers "No mapping between account names and security IDs" for an account that was just made, `accountSID` treated it as final: it failed the add and the rollback removed the account and folder. The name rule is not the cause: `runners.AccountName` already gives at most 20 characters (crsp-gameroompc-33e8 is 20), is stable and unique per runner, and the create and lookup paths use the same name. I could not run Windows here, so the exact trigger of the empty mapping (machine-less lookup, SAM timing) is inferred, not reproduced.

**Fix.** The native lookup now uses `MACHINE\name` (`QualifyAccount`), and a native miss falls back to the PowerShell `Get-LocalUser` lookup by exact name; the add fails only if both miss. Cleanup on failure is unchanged (account, folder removed), so a retry starts clean.

**Tests.** `TestQualifyAccount`, native-miss fallback in `TestAccountSIDGoodAndNative`, `TestAccountNameRule` (max 20, unique, stable). The Windows-only lookup stays behind `sid_windows.go`; no Windows go-test lane was used here.

**Docs.** Change note `docs/features/runners/changes/CRSP234.md`; R5.13 rule unchanged.

**Checks.** go build, go vet (also GOOS=windows), golangci-lint (also GOOS=windows), go test of touched packages, fast_guards, docs_check: green.
