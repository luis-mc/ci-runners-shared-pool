---
change: minor
task: CRSP304
---
Controller and agent installers are split: only the Controller installer is user-facing (installer/controller); agent installers are release assets (installer/agent, CrspAgent-<version>-<os>-<arch>) that the controller fetches, checks, caches and serves, pushes over SSH without the owner seeing them, and shows in the manual command with a LAN link; offline import by hand (R5.128 to R5.133). The Linux and macOS download from the controller's own address is pinned to its certificate (curl --pinnedpubkey), in the manual command and over SSH; the TLS check is never skipped (R5.130, R5.131).
