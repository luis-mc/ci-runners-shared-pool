---
change: minor
task: CRSP271b
---
Logging and secrets (R5.29 to R5.31, R6.5 to R6.7, R6.14): one `log/slog` setup with JSON fields and size rotation in the data folder, crash files with `runtime/debug.SetCrashOutput`, unreported crash files listed by the doctor and shown once as a Problem notice, one redactor writing `[redacted:<kind>]` in front of the log, crash file, audit, stream and API error sinks, a planted-secret test over every sink, a no-network-imports test for logs, history and audit, and the audit fields guarded against the data inventory. Built for all systems; tested on Linux CI, Windows and macOS compiled in CI only.
