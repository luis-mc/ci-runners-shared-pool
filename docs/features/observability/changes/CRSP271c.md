---
change: minor
task: CRSP271c
---
Section 9 now names the real tests of R5.7 and R5.35 to R5.46, which the jobsdata tables already build. New and proven: one history row per job attempt (R5.7), `crsp history` with the same rows as the API (R5.11), a finished row never read again and a failed GitHub read keeping the rows (R5.12), the commit view data and route `getCommitView` (R5.13), the p95 latency of the event hub (R6.1), the stream sending nothing before the session check and closing when the role is removed (R6.4), and the shared `LiveText` part that announces a live region at most once per 10 seconds (R6.13). Section 11 lists the stale marker, the viewer read of the audit log and the commit screen as not built.
