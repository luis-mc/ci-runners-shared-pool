---
change: minor
task: OPS75
---
Tool store with a private job home for Windows and macOS (tier 1), back end only: tool plan from the scan and .crsp.yml tools:, a read-only per-machine store filled once with each tool's own manager (R5.110 to R5.113), a private job home wiped after the job, caches restored by hash and saved only by a trusted green run, a job that never runs a manager, housekeeping after 30 days and the doctor tool probe (R5.114 to R5.119). Console screens, the hook call and the agent link message are not built.
