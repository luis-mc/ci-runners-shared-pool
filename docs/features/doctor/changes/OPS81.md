---
change: minor
task: OPS81
---
The timeout-minutes guideline moves from 30 to 60 (workflow.timeout) and runner.slots compares the gate maximum wait with the 2-minute hold cap (R5.60). New info check `timers.mode` (R5.65): "Timers are in observe mode: jobs over the limit are reported, not stopped".
