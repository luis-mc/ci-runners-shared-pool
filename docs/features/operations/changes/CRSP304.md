---
change: minor
task: CRSP304
---
Controller and agent installers are split: only the Controller installer is user-facing (installer/controller); agent installers are release assets (installer/agent, CrspAgent-<version>-<os>-<arch>) that the controller fetches, checks, caches and serves, pushes over SSH without the owner seeing them, and shows in the manual command with a LAN link; offline import by hand (R5.128 to R5.133).
