---
change: minor
task: OPS69
---
Release assets are named Crsp-<version>-<os>-<arch>.<ext>; the release manifest is Ed25519-signed (CRSP_UPDATE_SIGNING_KEY); the Windows MSI lifecycle lanes gate a release pull request; crsp uninstall takes --delete-data (PURGE=1) and unregisters the runners, the MSI asks through the native remove window and audits actor and result.
