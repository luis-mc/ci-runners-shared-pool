---
change: patch
task: CRSP351
---
LC-11 (upgrade keeps config and data) leaves out the agent-installers download cache under the data folder, as it leaves out the logs: the controller refills it from the release after each start, so an upgrade from v0.1.29 adds agent packages without any user data changing.
