---
change: minor
task: CRSP292
---
Round 3 of the performance fixes: window reads of finished jobs, the hour grid and the follow-up lookup use indexes (R6.20); the grouped table, the stats and the forecast samples are kept for 10 seconds and dropped on a job or run write; a caller with the tag gets a 304 before the read; the Overview and the event snapshot show machines, queue and running jobs (R6.21); the console shares one request for the same GET in flight (R6.22).
