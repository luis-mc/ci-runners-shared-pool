---
change: minor
task: CRSP266
---
New feature spec (proposed, nothing built): debug access to a running job (gap G4 of the runs-on study). Controlled sessions into a job's container, VM or runner account on Windows, Linux and macOS, through the agent link with no inbound port. Off by default, per-repository opt-in, admins and owner only, time and idle limits, hold-failed-job option, every session in the audit log with a redacted transcript, never the job's secrets or other jobs. Console screens first, `crsp debug` second. macOS is not run on a real Mac yet. Requirements R5.1 to R5.34 and R6.1 to R6.9, tests all planned, nine open questions for the owner.
