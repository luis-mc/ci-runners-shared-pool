# OPS69: MSI lifecycle lanes, release check, signed manifest, renamed assets, remove

Branch OPS69-msi-lifecycle-lanes. Code commit 949b1ab. Not run on a real Windows machine: this sandbox is Linux with no WiX and no dotnet, so the lanes, the WiX changes and the PowerShell have only been checked by parsing, unit tests and review. The first CI run of `lifecycle.yml` is the first real proof.

## What was built

1. **Lanes.** `.github/workflows/lifecycle.yml` (called by `ci.yml` job `lifecycle` on a release pull request, also `workflow_dispatch`). One lane per target: `windows-latest` x64 and `windows-11-arm` arm64, `timeout-minutes: 30`, `fail-fast: false`, no retries. `scripts/lifecycle/` has `windows.ps1` and `leftovers.ps1` (copied from the template; `windows.ps1` now reads the ProductCode from the Apps list because every WiX build has its own, and has a `lc15cli` check that runs `crsp uninstall --delete-data`), `fetch-packages.ps1` (builds this build, a broken build, an older build, and downloads the previous release, new name first, old second) and `lifecycle.json` (product, the two targets, paths, keep_on_remove).
2. **Release check.** `lifecycle_release_check.py` and `lifecycle_optout.py` copied unchanged to `scripts/delivery/`. The `release-check` job in `lifecycle.yml` fails a release pull request unless both lanes passed; it also runs `lifecycle_optout.py check`. `lifecycle.off` is empty (no `.delivery/config.json` exists, and none was added). Tested: one lane missing gives `FAIL windows arm64 msi: no lane result`, exit 1.
3. **Asset names.** `scripts/ci/release-names.sh` (`asset_name`, `stable_to_asset`). `release.yml` publish job uses it for archives, MSIs and native installers; the checksum step fails on any installer or archive not named `Crsp-<version>-<os>-<arch>.<ext>`; counts are now 18 (14 packages, 2 SBOMs, manifest, signature). `package-windows.yml`, `installer.yml` (previous-release download: new name first, old second), `windows-msi-check.yml` and `windows-msi-linux.sh` (comments), `scripts/install/install.sh` and `install.ps1` (new name first, old names second) and `install_test.sh` follow. CI artifacts and build-script files keep their stable names (DL-22). `scripts/ci/test_release_scripts.py` is rewritten for the new names (17 tests, all pass).
4. **Signed manifest.** `scripts/ci/release_manifest.py` builds the canonical manifest, `scripts/ci/release-sign.sh` signs it with `openssl pkeyutl -sign -rawin` using `CRSP_UPDATE_SIGNING_KEY` and checks the signature with the derived public key and with `internal/update/keys/release-ed25519.pub` when that file exists. `release.yml` attaches `manifest.json` and `manifest.json.sig` (hex), covers both in `SHA256SUMS` and the attestation, and fails in the `guard` job (before any build) when the secret is missing. Tests: canonical form, verify, one changed byte fails, missing secret fails, wrong key fails.
5. **Remove.** `internal/uninstall` (steps, audit, ask logic, Docker, WSL, data folders) and `crsp uninstall` / `crsp remove` in `internal/cli/cmd_uninstall.go`. `--delete-data` or `PURGE=1` deletes settings, database, logs, caches, runner images, containers and the `crsp` WSL distro; without it data is kept. Both unregister the runners from GitHub through the controller. Interactive with no flag: `crsp lifecycle-window remove`, exit 10 keep, 11 delete, 1 cancel; any other code or no window falls back to a terminal question with no default. Audit rows (actor, result) go to `logs/remove-audit.jsonl` and to the system log (Application event log, source `crsp`); the first row must succeed or nothing is removed. WiX: `PURGE` property (`REMOVEDATA=1` kept as alias), `AskRemove` (UI sequence, `Return="check"`, cancel ends the uninstall), `UnregisterRunners` (before the service stops, as the user), `RemoveData` now runs `crsp uninstall --from-installer` with `--delete-data` when PURGE=1, `PurgeFolder` fallback. The old checkbox dialog and `installer/ui-test.ps1` are gone; `installer.yml` no longer runs it. `smoke.ps1` gains a `PURGE=1` pass and an event-log check.

## Things the owner or other tasks must know

- **TRAY12 dependency.** Until `crsp lifecycle-window remove` exists, an interactive MSI uninstall (no terminal) cannot ask and ends with nothing removed (by design: no default). Silent and `PURGE=1` uninstalls work.
- **OPS68 dependency.** `lifecycle.json` `test_interface.selftest` and `pair` point at `crsp.exe lifecycle-test selftest|pair`, which is my guess for OPS68's interface. LC-8 and LC-29 fail until they match it.
- **Manifest shape.** Beyond the contract keys I added `assets` (one `{url, sha256}` per MSI, keyed `win-x64`, `win-arm64`), so the arm64 updater finds its package; `url` and `sha256` are the win-x64 MSI. `product` is `crsp`. If OPS68's parser rejects unknown keys, either drop `assets` or add it there. The public key file `internal/update/keys/release-ed25519.pub` does not exist yet; signing checks it only when present.
- **POST /api/remove** is not implemented. It needs `packages/contract/openapi.yaml` and the generated files, which OPS68 and LOOK53 also edit; I left it out to avoid conflicting generated code. The CLI works without it.
- **Runner unregistration depends on a crsp login.** `crsp uninstall` uses the CLI's signed-in context. A machine where nobody has run `crsp login` keeps the runners registered; the command says so and the audit row records a warning. The MSI step runs as the person who started the uninstall, so that user's login is used.
- **Not covered by delete:** runner OS accounts and agent services made on the machine (the `internal/leftovers` scan needs the controller's list of owned items); the lane's LC-15 leftover scan checks services, tasks, keys, firewall rules, ports and folders.
- **LC-32** (artifact names, public versus private stores): the name check is done by the release workflow (regex on every asset); `lifecycle_release_check.py --artifacts` expects `<Product>-<version>-<arch>.<ext>` without the OS part, so I did not call it, and `.delivery/config.json` with `lifecycle.stores` does not exist. Marked partial.
- `scripts/install/install_test.sh` fails 4 of 8 here; 3 of those failed before my change (`rm: command not found` in the test's restricted PATH in this sandbox) and my new `_<version>` case fails the same way.
- Pre-existing: `fast_guards` test-guardrails prints warn-mode breaks (G1, G4) in files I did not touch.
- The WiX changes use `Directory`+`ExeCommand` custom actions, `SetProperty ... After="SetPURGE"` and `[LogonUser]`; none could be built here. If `wix build` rejects one, `PurgeFolder` and the silent paths are the parts to look at first.

## Checks run

- `go test -race` of `internal/uninstall`, `internal/cli`, `installer`, `cmd/crsp`: pass. `golangci-lint` (v2.15.0) on those, also with GOOS=windows: 0 issues. `GOOS=windows go vet`: pass. gofmt clean.
- `python3 -m unittest` over `scripts/ci`: 40 tests pass.
- `docs_check --strict`: 0 errors, 0 warnings. Change notes: operations, cli.
- All edited workflows parse as YAML. No actionlint here.

CODE DONE 949b1ab2a5535ec979563df2015e3dcfc69333b4

| ID | status | evidence |
|---|---|---|
| LC-8 | fail (blocked) | lane step exists; needs OPS68's selftest command (`test_interface.selftest`) |
| LC-9 | not run | lane step `lc9`; first CI run decides |
| LC-10 | not run | lane step `lc10`; checks config and CA checksums |
| LC-11 | not run | lane step `lc11`; needs a published previous release MSI |
| LC-12 | not run | lane step `lc12`; broken build 0.9.1 built with `-FailAfterInstall` |
| LC-13 | not run | lane step `lc13`; older build 0.0.9 |
| LC-14 | not run | lane step `lc14`; `keep_on_remove` in `scripts/lifecycle/lifecycle.json` |
| LC-15 | not run | lane steps `lc15` (PURGE=1) and `lc15cli` (`--delete-data`); unit tests in internal/uninstall cover the data and WSL/Docker steps |
| LC-16 | not run | lane step `lc16` |
| LC-17 | not run | lane step `lc17`; a hosted runner may have a signed-in user, so it can fail there |
| LC-18 | not run | lane step `lint` (wix msi validate, one Apps entry) |
| LC-19, LC-20, LC-21 | not applicable | Linux and macOS are not lifecycle targets |
| LC-27 | pass | `lifecycle_optout.py check` in the release-check job; no opt-out exists |
| LC-29 | fail (blocked) | lane step exists; needs OPS68's pair command |
| LC-32 | partial | release asset name regex in release.yml; `--artifacts` mode and `lifecycle.stores` not wired (see above) |
| LC-33 | not run | lane step `lc33` |
| DS234 A-E | not applicable | no UI touched |
