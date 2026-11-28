---
change: minor
task: CRSP278
---
DEP15 offline machines: the controller keeps each pinned download (Docker Desktop installer, runner packages, agent packages) after checking its SHA-256, serves it to its machines over the agent link, and a machine installs from it first. The console tab Downloads and crsp dependencies show each dependency and import a file when the controller is offline.
