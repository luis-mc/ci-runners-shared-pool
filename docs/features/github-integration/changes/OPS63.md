---
change: minor
task: OPS63
---
A RUNS_ON variable is no longer deleted just after a restart. It is deleted only when capacity is known and absent in two syncs at least 15 minutes apart; the audit entry says why (R5.28).
