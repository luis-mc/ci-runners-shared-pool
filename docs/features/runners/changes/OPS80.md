---
change: minor
task: OPS80
---
A runner is made for a job only when there is room (wait before assignment): one lease per job, JIT runner with the job-name label on Linux, Windows and macOS, runner moved to the real job on in_progress, unused runners deleted after start-up plus 2 minutes, one warm runner per busy label set, 12 h alert and 24 h cancel, queue Doctor checks (R5.125 to R5.133).
