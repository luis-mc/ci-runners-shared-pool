---
change: minor
task: CRSP222
---
RELEASE-53 joins the lifecycle work: the updater reads the signed manifest's assets table and an arm64 machine takes its own package, the release key is committed, POST /api/v1/remove starts the uninstall and writes an audit row, and the OPS68 requirement is numbered R5.126.

Round 6: `crsp uninstall` on an MSI install hands the removal to Windows Installer (`msiexec /x`, `PURGE=1` for `--delete-data`, started detached); the Start Menu shortcut and the tray Run value live in the `crsp.exe` component (ICE43, ICE57, ICE69 clean); the MSI purge runs after the service is deleted and removes the whole data folder, logs included.
