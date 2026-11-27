# CRSP291: performance audit, round 3

Branch `CRSP291-integration`, built from `origin/CRSP290-integration` at `17346b5f` (the round-2 fixes, not yet in `main`). Nothing was fixed. This file stays on this branch and never goes into a pull request for `main`.

## 1. Method and limits

Target: the web console and API of a controller built from `17346b5f` (`pnpm --filter controller-console build`, `scripts/ci/embed-web.mjs`, `go build ./cmd/crsp`). Own instance on `127.0.0.1:18460`, agent link `18461`, data dir `C:/GitHub/crsp-291/data` (a copy of `C:/GitHub/crsp-290/data-seed`; the original was not touched; the owner's crsp on 8460/8461 was not touched).

Seed (unchanged from round 2): 20,000 runs, 150,000 jobs (149,756 done, 140 open: 50 running, 90 queued), 8 distinct `job_ident`, 40 repositories, 12 machines, no `run_follow_ups` rows, no `sqlite_stat1` (the controller never runs ANALYZE, so production plans match). `jobs/summaries` is empty in this seed; `from`/`to` params return 599,872 B (see R3-11).

Tools:

- Python client, keep-alive, one session cookie (the sign-in cookie path, not the bearer path): 40 sequential requests per endpoint (p50/p95), 8 and 24 concurrent clients for 10 s.
- A temporary SQL-driver wrapper (query count and per-query time) in a separate binary, `bin/crsp-qlog.exe`. A temporary debug publisher on `127.0.0.1:18498` in `bin/crsp-dbg.exe` (publishes N `job.queued` events into the hub; there is no other event source, see R3-6). Both were reverted in source and are not in this branch. Latency numbers come from the plain build (`bin/crsp.exe` / `crsp-dbg.exe` without the wrapper); query counts from the wrapper build.
- Playwright 1.65 with headless Chromium 1300x900: page load (TTFB, FCP, LCP, API calls, bytes), 4x CPU throttle (CDP `Emulation.setCPUThrottlingRate`), INP from the Event Timing API (real clicks and keystrokes), a real SSE burst of 60 job events.
- `sqlite3` `EXPLAIN QUERY PLAN` and timing against a read-only open of the seed.

Caveats:

- Loopback, so network cost is excluded; the 4x CPU throttle is the only client cost model.
- Seed rows repeat; gzip ratios are optimistic. `Samples` queries match more rows than in real data (8 job idents): the 6 to 12 ms per chunk is an upper bound, the query count is exact.
- Machine shared with 2 other agents. Before the runs: memory 56%, CPU about 4%. Memory stayed at 56 to 59% and CPU under 40% (the 4x throttle runs) throughout; the 10 s load bursts were short. Controller working set: 98 MB at start, 133 MB at the end.
- Single runs of the 10 s load tests; the p50/p95 of sequential runs are 40 samples each.
- Sequential numbers are lower than the CRSP290 report's known leftovers for some endpoints and higher for others (groups 548 ms here against 394 ms; stats 359 against 290). Same seed, so treat the machine load of the other agents as part of the variance (about plus or minus 30%).

## 2. Round-2 findings F1 to F15 measured again

Latencies are server wall time, p50 / p95, gzip accepted. `q` is the number of SQL statements.

| ID | Round 2 | Round 3 | Verdict |
|---|---|---|---|
| F1 runs list N+1 | `runs?limit=50` 582 q, 404 ms / 1,131 ms | 18 q, 93 ms / 99 ms. `state=running` 17 q, 135 / 139 ms. `result=failure` 5 q, 14 ms. `GET /runs/20000` 148 q / 410 ms -> 12 q / 36 ms | Fixed (leftovers R3-3, R3-4) |
| F2 jobs list forecast | `jobs?limit=50` 172 q, 242 ms; `state=queued` 145 q, 947 ms; `state=running` 104 q, 345 ms | 8 q, 36.5 / 41 ms; queued 8 q, 33.7 / 35 ms; running 5 q, 14.9 / 25 ms; done 5 q, 26 ms. `GET /jobs/150000` 155 q / 409 ms -> 16 q / 34 ms | Fixed |
| F3 timeline | 277 q, 1.8 to 4 s | 9 q, 144 / 157 ms. The home page's call: 106 ms (was 1.83 to 1.98 s) | Fixed in query count; 144 ms is now one missing index (R3-1) |
| F4 export memory | 32 MB, 1,502 q, 1.13 s, about 250 MB per export | 32,184,673 B streamed chunked, 306 q, 1.73 s, first byte 45 ms. Working set 98 MB before, 84 MB after one export, 88 MB after 4 concurrent | Fixed |
| F5 no gzip, ETag, cache headers | none | `Content-Encoding: gzip`, `Vary`, `ETag`, `Cache-Control: no-cache`. `runs?limit=50` 19,404 B -> 1,957 B; `jobs?limit=50` 35,930 -> 3,066; `jobs/groups` 10,054 -> 644; `jobs/stats` 9,079 -> 1,137; bodies under 1 KB go out plain; SSE is not compressed | Fixed. A 304 still runs the handler (R3-8); `timeline` and `savings` answers never 304 (their body changes every call) |
| F6 live reload storm | about 180 refetches per 60 events | Browser, `/pool/jobs`, 60 `job.queued` published at once: 3 calls (one reload of the 3 lists). Paced 60 events over about 3.6 s: 27 calls (9 reloads). `/`, `/pool/queue`, `/pool/live`: 0 calls. 4x CPU: longest task 0 ms | Fixed for the console. But see R3-6: nothing in the controller publishes job events |
| F7 history reads on write pool | `groups` 290 ms, `stats` 194 ms | Groups and stats now on `rdb`. `groups?by=repository` 548 / 566 ms, `by=machine` 541 / 586 ms, `stats?days=30` 359 / 369 ms | Moved, not fixed: still every row in Go (R3-2). Three history reads still on `s.db` (R3-9) |
| F8 write per request | `UPDATE sessions` per call | `GET /me` 2 q, both SELECT; `touchEvery` one minute (`auth/signin.go:326`, `auth/service.go:76`) | Fixed |
| F9 overview empty | `EmptySource` | `poolOverviewSource` still embeds `overview.EmptySource` (`cmd/crsp/overview_source.go:15`). Overview and the SSE snapshot answer `machines: []`, `queued: []`, `running: []`; only `minutes_saved_today` is real | Not fixed (R3-7) |
| F10 duplicate page-load calls | `/` 16 calls | `/` 16 calls: `machines` x4, `github/repositories` x3. `/pool/jobs` 12, `/pool/machines` 11, `/pool/live` 9, `/pool/queue` 7, `/analytics` 7 | Not fixed (R3-10) |
| F11 `queueFor` re-forecast | 19 `OpenJobs` per page | 1 `OpenJobs` per request | Fixed |
| F12 timeline and count(*) | 32 `count(*)`, 32 job pages | Timeline reads its window (`FinishedBetween`, `AnyFinishedBetween`). `count(*)` per list page remains (jobs 4.2 ms unfiltered, 9.7 ms `state=done`; runs 0.6 ms) | Partly fixed (R3-13) |
| F13 static hashing | ReadFile + sha256 per request | Unchanged (`webui/webui.go:111,120`): 0.4 ms for a 304, 1.15 ms for the 233 KB br file | Open, no measurable cost (R3-15) |
| F14 one 922 KB script | 281 KB gzip | 923,859 B raw, 278,560 B gzip, 232,624 B br; budget 307,200 B gzip: 90.7% used, 28,640 B left. CSS 147,524 B raw, 14,232 B br | Open (R3-16) |
| F15 page load | LCP 188 to 556 ms | Table 4 below | Same; INP now measured |

Load comparison (same endpoints, same seed):

| Load | Round 2 | Round 3 |
|---|---|---|
| 24 clients, run and job lists, 10 to 20 s | runs p50 10.4 s, p95 14.2 s; 4.9 req/s all endpoints | 24 clients on runs/jobs/queued/timeline/runs-running mix, 10 s: runs p50 1,252 ms, p95 1,579 ms; jobs 504 / 897 ms; queued 574 / 942 ms; timeline 563 / 1,069 ms; 29.9 req/s, 0 errors |
| 8 clients, 4-endpoint list mix | 1.6 req/s on timeline alone | 32.4 req/s: runs 429 / 549 ms; jobs 142 / 235; queued 122 / 232; timeline 283 / 365 |
| `GET /me` while the lists run | 3 -> 51 ms p50 | p50 0.8 to 1.1 ms; p95 32 ms (8 clients), 124 ms (24 clients); max 852 ms with 24 clients |
| 8 clients, history mix (groups, stats, overview, savings, waits) | not run | 23.5 req/s: groups p50 668 ms, stats 496 ms, overview 159 ms, savings 161 ms, waits 237 ms; `/me` p95 73 ms |

## 3. Findings

Ranked by severity. No P0: no timeout, OOM, deadlock or pool exhaustion was reached, and no request exceeded 2.3 s at 24 clients.

### P1

**R3-1. No index on `jobs (state, completed_at)`: five hot reads scan all 149,756 done jobs (about 100 ms each)**
- Severity: P1. Metrics: API latency, DB CPU, home-page load, SSE connect.
- Location: `apps/controller/internal/store/savings.go:20-30` (`FinishedPoolJobs`, `s.db`), `apps/controller/internal/store/jobsdata_batch.go:233` (`FinishedBetween`), `:245-252` (`AnyFinishedBetween`); callers `cmd/crsp/overview_source.go:20` (overview and the SSE snapshot), `internal/api/handlers_timeline.go:48,68-84`. Indexes: only `jobs_state (state, id)`, `jobs_stale (state, updated_at)`, `jobs_completed (completed_at)`.
- Defect: SQLite picks `jobs_stale (state=?)` and scans every done job to filter on `completed_at`, although `jobs_completed` answers the same predicate in 0.5 ms.
- Measured (`EXPLAIN` and timing on the seed): `SEARCH j USING INDEX jobs_stale (state=?)` 101 ms for 783 rows of today (`FinishedPoolJobs`); `FinishedBetween` 89 ms for 438 rows (plus a temp b-tree sort); `AnyFinishedBetween` `LIMIT 1` 86 ms (it finds nothing and walks 149,756 rows); with `INDEXED BY jobs_completed` 0.5 ms. Per request (query log): overview 106 ms (one query, 106.1 ms; p50 107 ms), savings 105 ms (110 ms), timeline two queries of 107 and 105 ms in parallel (144 ms p50, 160 ms p95, one request 246 ms of SQL time). The home page `/` calls all three, 3 x about 100 ms of CPU per load. Every new event stream pays it once more: the SSE snapshot took 61 ms to the first event. 8 clients on overview/savings: p50 159 / 161 ms.
- Scale: grows linearly with done jobs (150k now, 90 days of history). Every open console tab and every reconnect adds three 100 ms scans to a single-core-bound controller (about 30 req/s ceiling, section 5). With `(state, completed_at)` or planner statistics the same reads are under 1 ms.

**R3-2. `jobs/groups` and `jobs/stats` aggregate 150k rows in Go: 548 ms and 359 ms p50**
- Severity: P1. Metrics: API latency (budget 200 ms p95), CPU, allocation.
- Location: `store/jobsdata_read.go:561-606` (`Groups`: every non-done job and every done job of 90 days, 9 columns, `ORDER BY completed_at, id`, one `groupAcc` per key with growing `wait`, `run`, `trend` slices), `:672-714` and `:724-740` (`Stats`: all done jobs of the window with 9 columns, then a second scan of `created_at`).
- Defect: the engine returns every row and Go does the counts, the percentiles and the hour grid.
- Measured: `groups?by=repository` 548 / 566 ms (max 638), `by=machine` 541 / 586 ms, `stats?days=30` 359 / 369 ms. The query itself: 149,896 rows qualify for groups (88 ms to count in `sqlite3`); the SQL plan is `SCAN jobs USING INDEX jobs_completed`. Under 8 clients: groups 668 ms, stats 496 ms. Responses are 10 KB and 9 KB (644 B and 1,137 B gzip), so about 99.9% of the work is wasted rows. The second `stats` query `SELECT created_at FROM jobs WHERE created_at >= ?` is a full scan (`SCAN jobs`, 49,246 rows, 48 ms, no `created_at` index, see R3-3).
- Scale: linear in jobs of the window; the seed has 1.6k jobs a day. These are the two slowest JSON endpoints, behind the Analytics and Jobs pages (grouping, stats panel).

### P2

**R3-3. No index on `jobs.created_at`: `stats` hours, insights waits and the jobs `from`/`to` filter scan the table**
- Severity: P2. Metrics: API latency, DB CPU, write-pool occupancy.
- Location: `store/jobsdata_read.go:724` (stats hour grid), `store/insights_jobsource.go:42-56` (`JobsBetween`, 28 columns, two LEFT JOINs, `ORDER BY created_at, id`, on `j.s.db`), `store/jobsdata_read.go:325-330` (`jobWhere` `created_at >=` and `<=`).
- Measured: `SCAN jobs`, 49 ms for 49,246 rows (hours query); `SCAN j` plus `USE TEMP B-TREE FOR ORDER BY` 39 ms for 12,586 rows (8-day window) in `sqlite3`; `insights/waits` 169 / 198 ms (one query of 98 ms, the rest Go work); `jobs?from=2026-11-20` 52 / 57 ms against 36 ms unfiltered; `jobs?job_type=build&state=done` 54 ms.
- Scale: linear in total jobs, not in the window; a 30-day waits window on a year of history scans all 12 months.

**R3-4. Follow-up lookup drives from `runs`: 29 to 36 ms of every runs page (a third of `ListRuns`)**
- Severity: P2. Metrics: API latency.
- Location: `store/jobsdata_followups.go:166-170` (`followUpsOf`), `:155` (called per page and per `GetRun`).
- Defect: the join `run_follow_ups f JOIN runs r` plus the `latestAttempt` subquery is planned as `SEARCH r USING INDEX runs_repository (repository_id=?)` with `LIST SUBQUERY` of the 50 pairs probed per run; the small side (`f`, index `run_follow_ups_parent`) does not drive.
- Measured: query log for `runs?limit=50`: 29.4 to 36.7 ms for this one query, on a table with 0 rows. In `sqlite3` with 50 pairs: 64 ms before cache warm-up, the plan above. It is the largest single statement of the runs list (total SQL about 86 ms of the 93 ms).
- Scale: grows with the runs of the repositories on the page, not with follow-ups.

**R3-5. Forecast samples are re-read on every request and are the rest of the list cost**
- Severity: P2. Metrics: API latency, DB CPU under concurrency.
- Location: `api/jobs_forecast.go:59-127` (`prefetch`, three levels), `store/jobsdata_batch.go:64-140` (`SamplesMany`, chunks of 35 terms, `sampleTerms`/`sampleWorkers` at `:22-25`), `:56` (`sampleSQL`).
- Measured: `runs?limit=50` 9 chunk queries, 2.9 to 10.3 ms each (about 53 ms of the 86 ms of SQL); `jobs?limit=50` 4 chunks of 5.6 to 9.9 ms; `GET /jobs/150000` 4 chunks of 7.3 to 10.5 ms. Nothing is kept between requests; with the 8-client mix `runs?limit=50` rises to p50 429 ms and with 24 clients to 1,252 ms, so the chunks, not the query count (18), set the CPU cost.
- Scale: one runs page of 50 distinct keys reruns about 300 sample lookups per refresh and per tab. A stored forecast on queued jobs, or a short per-`(repository, job_ident)` cache invalidated on `job.finished`, removes them.

**R3-6. Nothing publishes `job.*`, `machine.*` or `saved.today` events: F6 is fixed but its source does not exist**
- Severity: P2 (functional and freshness; found while driving the F6 burst). Metrics: network chattiness, stale data.
- Location: non-test `Publish` callers in `apps/controller`: `internal/api/handlers_export.go:157` (plan), `internal/doctor/engine.go:267`, `internal/registry/builds/record.go:206`, `cmd/crsp/areas_wiring.go:257` (runner state). No caller for `events.JobQueued`, `JobStarted`, `JobFinished`, `MachineLoad`, `MachineState`, `SavedToday`. The console's lists stop polling while the stream is live (`controller-console/src/screens/jobs/api.ts:23-33`, `useTick`).
- Measured: with no debug publisher, `/pool/jobs` open and idle for 15 s makes 6 calls (`machines` x3, `setup`, `update/status`, `github/repositories`) and none of the job lists; the SSE snapshot has `queued: []`, `running: []`. With the debug publisher the coalescing works as described in section 2.
- Scale: in this build a job that starts, finishes or queues does not move any open list until the user reloads; the event pressure the F6 fix guards against cannot yet reach a browser. When a producer is wired it must publish through the existing coalescing (about 7.5 requests per second per tab for a continuous stream, 3 lists per reload every 400 ms).

**R3-7. Overview still has no queue, machines or running jobs (F9 unchanged)**
- Severity: P2. Metrics: correctness of the measurement, future cost.
- Location: `cmd/crsp/overview_source.go:14-17` (`poolOverviewSource` embeds `overview.EmptySource`), `internal/overview/overview.go:108-137`.
- Measured: `GET /overview` 207 B, `machines: []`; its 107 ms is the R3-1 scan. The concurrent-read code is unproven at real sizes; `QueuePager` has no SQL adapter.

**R3-8. A 304 still runs the whole handler**
- Severity: P2. Metrics: API latency, CPU.
- Location: `internal/api/transport.go:126-132` (the ETag is the sha256 of the rendered body, so the handler runs first).
- Measured: conditional requests with `If-None-Match`: `runs?limit=50` 304 in 93.5 ms (18 q), `jobs?limit=50` 304 in 38 ms (8 q), `jobs/groups` 304 in 549 ms, `jobs/stats` 304 in 360 ms, `runs/20000` 304 in 36 ms; `timeline` and `savings` return 200 (their bodies carry a changing value). The 304 saves bytes only (a 20 KB body to 0), not CPU or latency; the browser sends a conditional request on every load because the responses are `no-cache`.
- Scale: a page load of `/pool/jobs` re-runs 3 list handlers even when nothing changed. A version stamp (last job change id, runs max `updated_at`) as the ETag source would let a 304 skip the handler.

**R3-9. Three history reads still use the 4-connection write pool**
- Severity: P2. Metrics: DB connections, write latency.
- Location: `store/savings.go:21` (`s.db`), `store/insights_jobsource.go:42` (`j.s.db`), `store/jobsdata_followups.go:226` (`parentRunsOf`, `s.db`).
- Measured: not saturated here (the 8-client history mix ran without errors; `/me` p95 73 ms); each call holds a write-pool connection for 100 ms (savings, overview) or 98 ms (waits). `rdb` has 8 query-only connections.
- Scale: at 4 concurrent savings or overview calls (two console tabs refreshing) every write (webhooks, scheduler, session touch) queues behind them.

**R3-10. Duplicate and background API calls on every page load (F10 unchanged)**
- Severity: P2. Metrics: network chattiness, server CPU.
- Location: `controller-console/src/shell/Shell.tsx:111,266` (`useMachines`), `:157` and `:299` (`github/repositories`), `screens/repositories/plans.ts:19` (a third), `api/machines.ts:50`, `screens/pool/PoolLive.tsx:12`, `screens/pool/PoolMachines.tsx:13`.
- Measured (Playwright, identical at 4x CPU): `/` makes 16 calls (`machines` x4, `github/repositories` x3, plus `me`, `me/preferences`, `github/app-health`, `runners`, `overview`, `setup`, `update/status`, `savings`, `timeline`), 19.6 KB decoded; `/pool/jobs` 12 calls, 129,241 B decoded for 3 job lists (about 9 KB gzip on the wire); `/pool/machines` 11; `/pool/live` 9; `/pool/queue` and `/analytics` 7. An idle `/pool/jobs` adds `machines` x3 and 3 other calls per 15 s. With an ETag the repeats are 304s but each still runs its handler (R3-8); `machines` costs 2.3 ms so only `repositories`, `overview` and `savings` matter.
- Scale: about 40% of a page load's requests are repeats; with R3-1 and R3-8 each repeated `overview` or `savings` costs 100 ms.

**R3-11. `jobs/summaries` is unpaginated: 599,872 B JSON for 88 days**
- Severity: P2. Metrics: payload size (budget 50 KB), serialization.
- Location: `internal/api/handlers_jobstats.go:112-120` (`ListJobSummaries`, `Summaries` in `store/jobsdata_read.go:758`).
- Measured: `jobs/summaries?from=2026-08-30&to=2026-11-27`: 599,872 B raw (12x the budget), 23,200 B gzip, 8.8 ms; `limit=5` is accepted and ignored. Same size class as the round-2 586 KB. A seed gap: with no `from`/`to` the endpoint returns `{"items":[]}`, so the console path that calls it with a range is the one that pays.

### P3

**R3-12. LCP of idle pool screens sits 300 ms after the data (LCP 340 to 570 ms, FCP 80 to 240 ms)**
- Severity: P3. Metrics: LCP.
- Location: `controller-console/src/screens/pool/PoolQueue.tsx`, `PoolLive.tsx`, `PoolMachines.tsx` (candidates; cause not isolated).
- Measured (unthrottled): `/pool/queue` paints a paragraph at 108 ms and a larger one at 412 ms, so the LCP moves to 412 ms while every API call finished at 106 ms; the SSE snapshot arrives at about the same time (61 ms to first event, R3-1). 4x CPU: LCP `/` 236 to 356 ms, `/pool/live` 688 to 1,148 ms, `/pool/queue` 796 to 852 ms, `/pool/jobs` 212 to 528 ms, `/pool/machines` 844 to 872 ms, `/analytics` 376 to 700 ms. All inside the `firstScreenUsableMs` budget of 2,000 ms. Long tasks were only recorded during the event bursts (largest 51 ms, on `/` with 60 events at once).

**R3-13. `count(*)` on every list page, including keyset pages**
- Severity: P3. Location: `store/jobsdata_read.go:344` (`ListJobs`, skipped only by `NoCount` in the export, `api/handlers_jobs_export.go:139`), `:122` (`ListRuns`). Measured: jobs 4.2 ms unfiltered and 9.7 ms for `state=done` (covering index scan of 149,756 entries) on every page; runs 0.6 ms. A small share today (10 to 25% of a jobs page).

**R3-14. `OpenJobs` selects `*`**
- Severity: P3. Location: `store/jobsdata_read.go:526`. Measured: 0.5 to 1.3 ms for 140 rows, once per request. Grows with the open queue; no current effect.

**R3-15. Static server hashes the file on each request**
- Severity: P3. Location: `internal/webui/webui.go:111-120`. Measured: 304 for the 233 KB br file 0.4 ms (p95 0.5), 200 1.15 ms (p95 1.7); hashed assets are `max-age=31536000, immutable`, so browsers ask once.

**R3-16. One 924 KB script for every screen**
- Severity: P3. Location: `controller-console/vite.config.ts` (`inlineDynamicImports`), `perf-budgets.json`. Measured: 278,560 B gzip against 307,200 B (90.7% used, 28,640 B headroom); 232,624 B brotli; JS heap about 10 MB; FCP 80 to 240 ms unthrottled and 212 to 356 ms at 4x.

## 4. Page load and INP

Playwright, headless Chromium, 3 loads each, cookie-signed-in, ranges. TTFB is 1 to 4 ms on every route.

| Route | Calls | JSON decoded | FCP (1x) | LCP (1x) | FCP (4x) | LCP (4x) |
|---|---|---|---|---|---|---|
| `/` | 16 | 19.6 KB | 84 to 108 ms | 100 to 108 ms | 236 to 356 ms | 236 to 356 ms |
| `/pool/live` | 9 | 11.4 KB | 80 to 100 | 388 to 404 | 228 to 300 | 688 to 1,148 |
| `/pool/queue` | 7 | 6.2 KB | 80 to 84 | 392 to 404 | 216 to 228 | 796 to 852 |
| `/pool/jobs` | 12 | 129.2 KB | 96 to 108 | 96 to 220 | 212 to 292 | 212 to 528 |
| `/pool/machines` | 11 | 11.0 KB | 184 to 240 | 516 to 572 | 216 to 272 | 844 to 872 |
| `/analytics` | 7 | 6.2 KB | 168 to 252 | 224 to 316 | 232 to 356 | 376 to 700 |

Transfer per load: about 253 to 265 KB, of which 232,924 B is the script (br) and 1,827 B `theme-init.js`.

INP (Event Timing API, real clicks and typing, 4x CPU throttle, durations rounded to 8 ms by the browser): `/pool/jobs` filter buttons Queue 32 ms, Running/Done/All/Runs/Jobs 16 ms each, tabs Machines 16 ms, Live 40 ms, Overview 40 ms, typing in the search box 24 ms per key (worst 64 ms over a burst of keys), the "Why" button 48 ms, a click while 60 job events arrive 24 ms. The worst interaction is 64 ms: INP is good (under 200 ms). No `/` tab interaction was driven (the selectors there are different); no INP finding.

Event burst (F6 check): section 2, row F6. With the debug publisher: 60 `job.queued` at once on `/pool/jobs` = 3 requests and 0 long tasks; 60 events over 3.6 s = 9 reloads of the 3 lists (27 requests); no reload on `/`, `/pool/queue`, `/pool/live`. The coalescing window is a fixed 400 ms (`live/store.ts:80`, `hold` at `:190-200`), so a stream that never pauses reloads every 400 ms per tab.

## 5. Capacity

- Throughput on the list endpoints saturates near 30 requests per second at both 8 and 24 clients (32.4 and 29.9), a single busy core, as the p50 rises 4x with 3x the clients. Round 2: 4.9 requests per second.
- The history endpoints are the heavier ones: 23.5 requests per second with groups and stats in the mix.
- Controller memory 98 MB -> 133 MB working set after the whole run (4 concurrent 32 MB exports included).

## 6. Suggested order for CRSP292

1. R3-1: an index on `jobs (state, completed_at)` (or `jobs_completed` forced), with a plan test on the seed; takes overview, savings, timeline and the SSE snapshot from about 100 ms to under 5 ms.
2. R3-2 with R3-3: aggregate groups and stats in SQL (or a daily summary table); add `jobs (created_at)`; target groups and stats p95 under 150 ms.
3. R3-4 and R3-5: drive the follow-up join from `run_follow_ups`; cache or persist the samples; target `runs?limit=50` under 40 ms and flat under concurrency.
4. R3-6 first as a decision: who publishes job events. Then R3-7 (overview source), R3-8 (version-based ETag), R3-9 (read pool), R3-10 (one shared fetch for machines and repositories), R3-11 (page the summaries).
5. R3-12 to R3-16 as capacity allows.
