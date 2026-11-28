---
change: minor
task: CRSP403
---
A Windows runner service that stopped (as at a boot before crsp is up) is started again by itself on every machine including the controller's own, with a growing wait after failures, the reason it stopped, and audit entries (R5.178).
