# crsp Windows installer (MSI)

`crsp-windows-amd64.msi` and `crsp-windows-arm64.msi`, built with WiX Toolset v5 (pinned to 5.0.2 in `dotnet-tools.json`; the UI, Util and Firewall extensions at the same version). The release workflow builds them on `windows-latest` and adds the version to the name of the published file (`crsp-windows-amd64_0.1.15.msi`, R5.119); the build script keeps the plain name; the feature spec is `docs/features/operations/spec.md` (R5.7, R5.35 to R5.41).

## Identity (never change)

|                |                                                                                                   |
| -------------- | ------------------------------------------------------------------------------------------------- |
| UpgradeCode    | `C1D62442-6DB4-4FF0-9F28-A1CE3121B332`                                                            |
| Product name   | `crsp`                                                                                            |
| Service        | `crsp` (`crsp.exe controller`, account LocalSystem)                                               |
| Install folder | `%ProgramFiles%\crsp`                                                                             |
| Data folder    | `%ProgramData%\crsp` (kept by upgrade and by a silent uninstall)                                  |
| Firewall rule  | `crsp console and agent link (private networks)`, inbound TCP 8460 and 8461, Private profile only |

The UpgradeCode is what lets a newer MSI replace an older one. Changing it makes the next install a second product next to the first.

## Build

```powershell
# a crsp.exe for the target (the release build, or any local `go build`)
$env:GOOS = 'windows'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'
go build -trimpath -ldflags "-s -w -X main.version=0.1.0" -o installer/controller/out/crsp.exe ./apps/controller/cmd/crsp
pwsh installer/controller/build.ps1 -Arch x64 -Exe installer/controller/out/crsp.exe -Version 0.1.0 -Out installer/controller/out
```

Needs the .NET SDK (the WiX tool is restored from `dotnet-tools.json`). `-Arch arm64` makes the arm64 MSI from an arm64 `crsp.exe`.

### Version stamp

`build.ps1` maps the release version to the MSI `ProductVersion`:

| Release version                | ProductVersion                                                                  |
| ------------------------------ | ------------------------------------------------------------------------------- |
| `1.2.3`, `v1.2.3`, `1.2.3-rc1` | `1.2.3.0`                                                                       |
| `edge-<sha>`                   | `<next patch after the latest stable tag>.<run number>`, for example `0.1.1.57` |

Windows Installer compares the first three fields only. An edge build has the same `a.b.c` as the next stable release, so the stable release (same `a.b.c`) replaces it, and a later edge build replaces an earlier one (`AllowSameVersionUpgrades`). A lower `a.b.c` is refused (see below).

## Silent install, upgrade and uninstall

Run from an elevated process (the package is per machine):

```text
msiexec /i crsp-windows-<amd64|arm64>.msi /qn /norestart /l*v <log>
```

This is both the first install and the upgrade. On an upgrade it stops the `crsp` service and the tray, replaces the files, and starts the service again. Data is kept. The self-updater (CRSP19) runs exactly this command; its log path is its choice (suggested: `%ProgramData%\crsp\logs\msi-update.log`).

Exit codes: `0` done; `1603` refused (a newer version is installed, the log has `A newer version of CI Runners Shared Pool is already installed.`) or failed; `3010` never (no run needs a reboot).

```text
msiexec /x crsp-windows-<amd64|arm64>.msi /qn /norestart /l*v <log>                 keeps the data folder
msiexec /x crsp-windows-<amd64|arm64>.msi /qn /norestart PURGE=1 /l*v <log>         also deletes it (REMOVEDATA=1 is the older name)
msiexec /x crsp-windows-<amd64|arm64>.msi                                           asks "Keep my data" or "Delete my data too" in the native remove window (no default; Cancel changes nothing)
crsp uninstall [--delete-data]                                                      the same from the command line (PURGE=1 is honoured)
```

Settings > Apps > crsp > Uninstall runs the last form (the package adds its own Apps entry that starts it with the dialogs).

## Tests

`smoke.ps1` (elevated) installs, checks the service, PATH, firewall rule, shortcut and autostart, upgrades, refuses a downgrade, uninstalls (data kept), reinstalls, uninstalls with `REMOVEDATA=1`, and checks that nothing is left. CI runs it for x64 on every pull request that touches `installer/**` (`.github/workflows/installer.yml`). The remove window is the native window of TRAY12 (`crsp lifecycle-window remove`); the lifecycle lanes (`.github/workflows/lifecycle.yml`, `scripts/lifecycle/`) test install, upgrade, rollback, downgrade, remove, purge and leftovers on windows-latest and windows-11-arm.

## Controller and agent installers (CRSP304)

This folder is the **Controller** installer, the only one a person downloads. The agent installers (no window, release assets that the controller fetches and hands to machines) are in `installer/agent` (operations R5.128 to R5.133).
