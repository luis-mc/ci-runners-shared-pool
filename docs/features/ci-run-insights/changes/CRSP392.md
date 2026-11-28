---
change: minor
task: CRSP392
---
The stale-job reconcile is started with the controller: one pass at start, then every 5 min; it closes jobs GitHub finished or no longer has, writes job.reconciled to the audit log and stops a pass at a rate-limit refusal.
