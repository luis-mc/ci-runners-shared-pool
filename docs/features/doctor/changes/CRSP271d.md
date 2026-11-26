---
change: minor
task: CRSP271d
---
A new controller check, `controller.log_store`, shows how many log lines were lost and how many writes to `logs.db` failed (observability R6.9). Logs fail open, so jobs are never affected. The controller adds it through the extra checks, like the crash file check.
