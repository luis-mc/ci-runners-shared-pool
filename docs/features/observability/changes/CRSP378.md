---
change: minor
task: CRSP378
---
Job lists follow the live stream: the controller publishes job.queued, job.started and job.finished with run and job ids after each job write (R5.78). The groups and stats answers are built at start and rebuilt in the background after writes, so no page scans past jobs.
