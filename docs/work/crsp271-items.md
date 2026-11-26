# CRSP271 items (plan row J): remaining observability rows

Source: docs/features/observability/spec.md, compared with internal/, apps/web and changes/ on origin/main (2026-11-26).
Excluded: R5.16-R5.19, R5.34, G6 (CRSP256, owner holds). Skipped: R5.14, R5.28 (done in CRSP257).
Status after survey: S = spec/test rows only (behaviour exists), P = partly built, N = not built.
Order: cheapest first. PR = integration branch that builds it.

## PR 1 — CRSP271-integration: timing, status, clock, wait reasons (pure functions, small)
1. R5.9  (N) pure timing rules: never negative, zero time unknown, rerun from attempt start. internal/history/timing.go
2. R5.8  (N) wait split (GitHub wait / slot wait), run time minus slot gate counted once. internal/history/timing.go
3. R5.10 (P) result mapping, startup_failure and timed_out count as failure in totals. internal/history/timing.go (jobsdata.Result exists)
4. R5.5  (P) wait reasons exist (scheduler.WaitReason, overview.Waiting); add TestWaitReasons and the 2-minute no-runner threshold test. internal/events/waitreason_test.go
5. R6.12 (N) UTC storage test + one shared 24-hour local time format. apps/web/src/live/format.ts
6. R5.6  (N) shared clock that pauses while hidden + status word/glyph/colour set. apps/web/src/live/clock.ts, status.ts
7. R5.4  (P) one live store feeds Overview and strip: add the proving test. apps/web/src/live/store.test.ts

## PR 2 — CRSP271b-integration: logging and secrets (internal/logs, no UI except one Problem notice)
8.  R5.29 (P) slog setup: JSON fields (component, version, ids), size rotation in data folder, stderr in foreground. internal/logs/slog.go
9.  R5.30 (N) crash file on goroutine panic + SetCrashOutput. internal/logs/crash.go
10. R5.31 (N) unreported crash files shown once as a Problem notice, handed to the doctor. internal/logs/crash.go, doctor check
11. R6.5  (P) one redactor in front of every sink (internal/redact exists): kinds from spec, `[redacted:<kind>]`. 
12. R6.6  (N) planted-secret test over every sink. internal/logs/redact_sinks_test.go
13. R6.7  (N) no network imports in logging, history, audit + depguard rule. internal/logs/local_test.go
14. R6.14 (N) audit fields listed in data inventory + test. internal/audit/inventory_test.go

## PR 3 — CRSP271c-integration: logs.db, agent log capture, search
15. R6.10 (N) logs.db separate SQLite file. internal/logs/store.go
16. R6.8  (P) retention defaults, floors, ceilings for logs (audit/history done). internal/logs/retention.go
17. R6.9  (N) logs fail open, failures counted for the doctor. internal/logs/failopen.go
18. R5.23 (N) agent disk buffer with cap, oldest dropped, one dropped-count line. internal/logs/buffer.go
19. R5.20 (N) agent captures setup steps + own lines, sends over the agent link. internal/agent, agent.proto
20. R5.21 (N) full-text + filter search (FTS5), API + page. internal/logs/search.go
21. R5.22 (N) runner setup log live in runner detail. internal/events
22. R6.11 (N) benchmarks: history page of 50 under 200 ms at 1M rows; log search first page.

## PR 4 — CRSP271d-integration: history, commit view, access, latency
23. R5.7  (S/P) history row per attempt = jobsdata tables (R5.35): map the row, add TestOneRowPerJobAttempt.
24. R5.11 (S) filters repository, machine, backend, result; `crsp history --json`. CLI missing.
25. R5.12 (S) finished row never refetched; stale marker on GitHub failure.
26. R5.13 (N) commit view: all runs done, slowest job, share, two slowest steps.
27. R6.4  (P) stream sends nothing before auth, closes on role removed; logs/audit admin only.
28. R6.1  (N) p95 under 2 s latency test. internal/events/latency_test.go
29. R6.13 (N) live regions announce at most once per 10 s (e2e).
30. R5.33 (N, CRSP92) speed comparison on hosted runners: needs owner decision (spends hosted minutes). NOT planned in this task.

## Out of reach in this task (reported at end)
R5.33 (30). Anything not done within ~3 h.
