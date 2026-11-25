---
change: minor
task: CRSP258
---
Real notices are built: the six events of R5.1 are read from the machines, the job queue, the health reports, the doctor and the update check, kept as one state per key, and made only on a change; dedupe (flap window), the hourly cap with one summary, grouping of a burst, a delivery queue with retries and backoff for webhook and email, the failing-endpoint finding, fail-closed signing, the 30-day retention and queue ceiling, the pool thresholds, and the Notices page with its API. The tray notice stream, the mail provider adapter with bounce feedback and address confirmation, per-event role defaults and the CLI stay planned, with the reason in section 11. Built for every system; run on Windows and Linux; the macOS build is compiled and unit-tested in CI, not run on a real Mac.
