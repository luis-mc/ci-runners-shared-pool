---
change: minor
task: OPS66
---
Adds measured job peaks: per job run the p95-over-windows peak CPU, the peak memory, sample count, duration and the OOM and cut-short flags, from one reader per system (cgroup v2, Job Object, process tree) at 1 s (5 s for the process tree), stored per repository, workflow, job key and runner (migration 00034), with a per job key summary of the last 30 good runs (p90, measured from 5). Requirements R5.60 to R5.65; the lookup of a started job's processes is not built yet (section 11).
