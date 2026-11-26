---
change: minor
task: CRSP271
---
Wait reasons (observability R5.5): `internal/scheduler/waitreason.go` adds `NoRunnerWithLabels`, the rule that raises "no online runner with the labels" once a job has waited 2 minutes (default, setting), with a test of every wait reason. Pure function; not wired into the gate yet.
