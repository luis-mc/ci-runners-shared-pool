# CRSP289: performance audit, round 2

Branch `CRSP289-integration`, built from `origin/main` at `4dca9b17` (RELEASE-70). Nothing was fixed. This file stays on this branch and never goes into a pull request for `main`.

## 1. Method and limits

Target: the web console served by a controller built from `origin/main` (`go build ./cmd/crsp`, console built with `pnpm --filter controller-console build` and embedded with `scripts/ci/embed-web.mjs`).

Setup: own instance on `127.0.0.1:18460`, agent link `127.0.0.1:18461`, own data dir `C:/GitHub/crsp-289/data`. The owner's installed crsp was not touched.

Seed (synthetic, written straight into `controller.db` with a Python script; no seed tool exists in the repo, the 10k/100k recipe of `store/jobsdata_bench_test.go` was scaled up):

| Table | Rows |
|---|---|
| runs | 20,000 (90 days; the newest 60 are open) |
| jobs | 150,000 (50 running, 90 queued, rest done) |
| job_steps | 320,000 |
| repositories / machines / runners | 40 / 12 / 36 |

Tools: a Python HTTP client (per endpoint, 20 sequential requests; concurrent runs of 8 to 24 clients), Playwright with headless Chromium (1300x900, no CPU throttle) for TTFB, FCP, LCP and the API calls a page makes, and a temporary query-logging SQL driver wrapper to count queries per request. The wrapper was reverted and is not in this branch.

Caveats that matter for reading the numbers:

- **The round-1 report was not found.** Searched: `git log --all --grep` for perf and audit, `docs/work` on every ref, files named `*perf*`. The only trace is the project record (CRSP286 to CRSP288 and CRSP296 task rows, "P-1..11") and `docs/work/CRSP288/REPORT.md` in git history (commit `74cb2251^`). So "compare with round 1" is done against what each fix task says it does, and each claim is checked by measurement.
- `origin/CRSP287-perf-api-web` is a stale branch (tip is CRSP263, no CRSP287 commit). Nothing of CRSP287 is in `main`.
- Seed jobs carry no stored forecast (`fc_basis = ''`). A finished job with a stored forecast skips the sample query (`jobs_forecast.go:45`), so the forecast query storm on done jobs is partly a seed effect. The storm on queued jobs and on open runs is not (queued jobs are always re-forecast).
- The seed has 8 distinct `job_ident` values, so `Samples` queries match more rows than in a real database. Treat the 8 ms per sample query as an upper bound; the query count is exact.
- gzip ratios below are optimistic: seed rows repeat.
- The production overview source is `overview.EmptySource` for machines, queue and running jobs (`cmd/crsp/overview_source.go:14`), so the overview was measured empty.
- Machine: 6+ cores shared with 2 other agents. Start of audit: memory 60%, CPU about 22%. All runs stayed under 85% memory and 60% CPU except the 20 s concurrent load bursts (CPU saturated by the controller under test, 10 to 20 s each).

## 2. Round-1 findings: fixed or not

| Round-1 item | Task | Verdict | Evidence |
|---|---|---|---|
| P-1..P-4 run and job list count and page | CRSP286 | Fixed in the store, undone one layer up | Store: `count(*) FROM runs` 6.3 ms, page by sort-key index 0.3 ms, `state=running` count 10.8 ms, `jobs` count 4.6 ms, jobs page 0.08 ms on 20k runs / 150k jobs. `TestListPagesP95` passes. Endpoint: `runs?result=failure` p50 14 ms (all rows done). But `runs?limit=50` p50 404 ms, p95 1131 ms and `jobs?state=queued` p50 947 ms, because the API handler adds a per-row N+1 (F1, F2). |
| P-5 API compression, ETag, cache headers | CRSP287 | **Not fixed** | `git grep` for gzip, `Content-Encoding`, `ETag`, `If-None-Match` in `internal/api` and `cmd`: none. Every API response: no `Content-Encoding`, no ETag, no `Cache-Control` (F5). |
| P-5 static assets | CRSP296 | Fixed | One JS file `index-*.js` 922 KB raw, 227 KB br (served `Content-Encoding: br`), 281 KB gzip (budget 307,200 B: 91.4% used). `Cache-Control: public, max-age=31536000, immutable` on hashed assets, `no-cache` plus strong ETag on `index.html`. |
| P-6 debounce and coalesce live reloads, drop redundant poll | CRSP287 | Poll dropped, debounce **not done** | `useTick` only ticks while the stream is not live (`screens/jobs/api.ts:23-33`). `LiveStore.apply` bumps a version per event with no debounce or in-flight merge (`live/store.ts:160-178`, `live/LiveProvider.tsx:41-44`) (F6). |
| P-7 overview concurrent reads, count in SQL | CRSP288 | Code done, cannot be measured | Overview reads run in an errgroup (`overview/overview.go:108`). Overview answers in 56 to 69 ms but it is empty: `EmptySource` serves queue, machines and running; `QueuePager` has no SQL adapter (F9). |
| Unchecked handlers: timeline, insights, forecast, fillFollowUps, machines | CRSP288 | Partly | Timeline reads are concurrent but it is still the slowest endpoint (F3). `fillFollowUps` parent lookup is batched (`jobsdata_followups.go:213`); `followUpsOf` is still per run inside `GetRun`. `machines` 3 ms p50, `insights/waits` 97 ms, `savings` 53 ms are fine. |

## 3. Findings

Ranked by severity. Latencies are server-side wall time from a loopback client. Query counts come from the SQL wrapper.

### P0

**F1. Runs list does a full run-detail build per open run (N+1, 582 queries per page)**
- Location: `apps/controller/internal/api/handlers_runs.go:158-166` (loop), `:177-184` (`openRunEta` calls `GetRun`, then `buildRun`), `apps/controller/internal/store/jobsdata_read.go:229-260` (`GetRun`), `apps/controller/internal/api/jobs_forecast.go:117-133` (`queueFor`).
- Measured: `GET /runs?limit=50` with 50 open runs on page one = 582 queries (307 forecast sample queries, 51 run reads, 51 follow-up reads, 50 job reads, 50 workflow-graph reads, 50 attempt reads, 19 `SELECT * FROM jobs WHERE state IN ('queued','running')`). Sequential p50 404 ms, p95 1,131 ms, max 1.9 s with `state=running`. Same page when all runs are done (`result=failure`): 6 queries, p50 14 ms.
- Under 24 concurrent clients on the run and job lists for 20 s: runs p50 10.4 s, p95 14.2 s; all endpoints together 4.9 req/s; controller used about 100 CPU-seconds in the 20 s window. During a 16-client list load a trivial `GET /me` went from 3 ms to 51 ms p50 and `GET /overview` from 60 ms to 197 ms.
- Scale: a list is one request per open console, and the live reload (F6) repeats it on every job event. With dozens of builds open, one tab costs about 0.4 to 1 s of CPU per refresh.

**F2. Jobs list: per-job forecast and per-running-job timer reads**
- Location: `handlers_jobs.go:296-312` (`jobItems`), `jobs_forecast.go:44-67` (`forJob`, one to three `Samples` calls per job), `jobs_forecast.go:117-133` (`queueFor` reads and forecasts every open job again), `reserved.go:57-66` (`flagsOf`: `Timers.Job` once per running job), `store/jobsdata_read.go:456-518` (`Samples` on the 4-connection write pool `s.db`).
- Measured: `GET /jobs?limit=50` 172 queries, p50 242 ms, p95 1,259 ms. `state=queued` 145 queries (140 sample queries), p50 947 ms, p95 1,710 ms. `state=running` 104 queries (50 timer reads, 50 samples), p50 345 ms. 24 clients: p50 2.3 s (running) to 4.4 s (all).
- Each sample query takes about 8 ms on the seed because the `jobs_key (job_ident, completed_at)` index does not include `repository_id` and the repository fallback sorts the whole repository (`USE TEMP B-TREE FOR ORDER BY`). Sample reads are not cached across requests.
- Scale: a queue of 50 queued jobs reruns 140 or more sample queries on every refresh of every open tab.

**F3. Timeline: 1.8 to 4 s, and the home page calls it**
- Location: `handlers_timeline.go:68-84` (`finishedJobs`), `store/jobsdata_read.go:338-366` (`ListJobs` counts on every page), `screens/pool/SlotTimeline.tsx:78,94` (caller; mounted on `/` and `/pool/live`).
- Measured: `GET /timeline` 277 queries (140 samples, 50 timer reads, 32 `count(*)`, 32 job pages), 1.8 s single request after warm-up, p50 4.0 s and p95 6.4 s in a 20-request run, p50 6.9 s with 8 concurrent clients (1.6 req/s). Playwright: the home page makes this call on every load and it takes 1.83 to 1.98 s, 20 to 30 times longer than any other call of that page.
- The default window is 6 hours but `finishedJobs` reads up to 50 pages x 100 jobs (5,000 rows) created in the last 48 hours, each page with its own `count(*)`.

**F4. `GET /api/v1/exports/jobs` holds the whole export in memory (about 250 MB per request at 150k jobs)**
- Location: `handlers_jobs_export.go:38-45` (`jobsExportFile.body []byte`), `:192-205` (`bytes.Buffer`), `:104-135` (pages gathered into one slice first).
- Measured: one export = 32.2 MB, 1,502 queries, 1.13 s. Controller working set 411 MB before, 1,408 MB after 4 concurrent exports (about 250 MB per export), 1,334 MB 3 s later (the Go runtime keeps it).
- Scale: any signed-in user can start it, no row cap, and the export is the "every row of the filter" contract. At 1M historic jobs this is 1.6 GB per click. Rated P0 under the OOM rule; it is an explicit user action, so it is a P1 if the owner prefers to rank by traffic.

### P1

**F5. No compression, no ETag, no cache headers on any API response**
- Location: no code at all; `internal/api/server.go` (no middleware). Compare the static handler `internal/webui/webui.go:99-136`.
- Measured: every response `Content-Encoding` absent, no `Etag`, no `Cache-Control`. `runs?limit=50` 19,389 B (gzip 1,698 B), `jobs?limit=50` 35,930 B (gzip 2,268 B), `jobs/summaries` for 88 days 586,392 B (gzip 15,354 B, 11 times over the 50 KB budget), `jobs/groups?by=repository` 10,058 B. The `/pool/jobs` page loads 132,841 B of JSON in 3 job-list calls; the home page 24,191 B.
- Scale: remote use over the TLS listener (any non-loopback client); live reloads (F6) resend the same bytes. A 304 would also skip the handler work if the ETag were computed from a cheap version.

**F6. Every job event reloads every job-backed list, with no debounce and no in-flight merge**
- Location: `controller-console/src/live/store.ts:160-178` (`apply` bumps `versions[kind]` per event), `live/LiveProvider.tsx:41-44` (`useVersion`), `screens/jobs/api.ts:43,61,79,95,105,115` (six hooks keyed on `jobs`), `api/hooks.ts:21-42` (`useLoad` drops the stale result but the request still runs on the server).
- Measured: not driven from a real burst (no event source on the seeded instance); counted from the code. A push that queues 60 jobs sends about 60 `job.queued` events, each makes `/pool/jobs` fetch 3 lists (observed 339, 211, 253 ms each, uncompressed, and see F1 and F2 for server cost) = about 180 refetches in a few seconds per open tab. With 40 builds finishing, add 3 events each (`job.queued`, `job.started`, `job.finished`).
- Also every event replaces the whole `LiveState` object (`store.ts:102-106`) so all 7 components that call `useLiveState()` re-render on every `machine.load` (one per machine every 2 s, `events/hub.go` `DefaultLoadEvery`), including ones that do not use it.

**F7. Reads of large history on the 4-connection write pool, computed in Go**
- Location: `store/jobsdata_read.go:541-630` (`Groups`: every done job of 90 days, sorted), `:651-750` (`Stats`: every done job of the window plus a second scan at `:722`), `:430`, `:495`, `:524`, `:559`, `:670` (all `s.db`, not `s.rdb`), `store/store.go:91,99` (write pool 4, read pool 8).
- Measured: `jobs/groups?by=repository` 290 ms, `by=machine` 308 ms, `jobs/stats?days=30` 194 ms (160 ms p50 over 20 requests) on 150k jobs, 3 to 4 queries each. Cost grows with the number of jobs in the window (about 1.6k per day in the seed).
- Scale: these hold a write-pool connection for the whole scan while webhook ingestion, the scheduler and session touches (F8) need the same 4 connections. At 4 concurrent group or stats requests the writer is blocked.

**F8. Every authenticated request does a write**
- Location: `auth/signin.go:326` (`TouchSession` after every `Authenticate`), `store/authstore.go:209-213`, one `UPDATE sessions SET last_seen_at` per request through the single-writer path (`_txlock=immediate`).
- Measured: `GET /me` is 3 queries (2 auth, 1 profile), `GET /machines` 2 (both auth); the `TouchSession` write took 1.6 ms in the first logged request. A 24-client run on `/me` plateaus at 312 req/s, on `/machines` 494 req/s (client included).
- Scale: with the list traffic above, each page load (about 7 to 16 API calls) is 7 to 16 serialised write transactions; the event stream rechecks the session every 10 s per stream (`handlers_live.go:36`, up to 50 streams) with the same write.

### P2

**F9. Overview cannot show the queue, so the P-7 fix is unproven**
- Location: `cmd/crsp/overview_source.go:14-20` (`EmptySource`), `overview/overview.go:43-47,117-124` (`QueuePager` has no implementer outside tests).
- Measured: `GET /overview` answers 207 B and 56 to 69 ms with 90 queued and 50 running jobs in the database. The shell and the home page read the queue from the event stream snapshot.
- Scale: when the SQL source is written, `Queued`/`Running` come back unpaged unless `QueuedJobsPage` is implemented; there is no test at 1,000 queued jobs.

**F10. Duplicate API calls on every page load**
- Location: `controller-console/src/shell/Shell.tsx:153,295` (repositories, twice), `screens/repositories/plans.ts:19` (a third), `api/machines.ts:59` plus `screens/analytics/Insights.tsx:29` and the shell's own machines read.
- Measured (Playwright): `/` makes 16 API calls: `machines` x4, `github/repositories` x3, plus `me`, `me/preferences`, `github/app-health`, `runners`, `overview`, `setup`, `update/status`, `savings`, `timeline`. `/pool/queue` (an idle page) makes 7; `/pool/machines` 11.
- Scale: about 40% of the requests of a page load are repeats; with no ETag (F5) every repeat is a full response.

**F11. `queueFor` forecasts every open job again for each run it is asked about**
- Location: `jobs_forecast.go:117-133`, called from `buildRun` (`handlers_runs.go:23-45`) once per open run.
- Measured: 19 `SELECT * FROM jobs WHERE state IN ('queued','running')` plus the forecast of every row in one runs page. O(open runs x open jobs) with dozens of runs and hundreds of jobs; `OpenJobs` also selects `*` (`jobsdata_read.go:524`).
- Same root as F1; listed apart because it also hits `GET /runs/{id}` (148 queries, 410 ms) and `GET /jobs/{id}` for a queued job (155 queries, 409 ms).

**F12. `timeline` and the list handlers read more than the screen shows**
- Location: `handlers_timeline.go:68` (`From: now.Add(-2 * timelineHorizon)` for a 6-hour view), `store/jobsdata_read.go:521-528` (`OpenJobs` unbounded, `SELECT *`), `jobsdata_read.go:343` (`count(*)` per list page, also on every keyset page after page one).
- Measured: `count(*) FROM jobs` 4.6 ms on 150k rows, 32 of them in one timeline request.
- Scale: grows with history. Each count is a full index scan when the filter is `state = 'done'`.

### P3

**F13. Static server hashes and copies the file on every request**
- Location: `webui/webui.go:111-122` (`fs.ReadFile` and `sha256` of the 227 KB `.br` for every request, including 304s).
- Measured: not visible in latency (static requests under 40 ms); about 0.2 MB hashed per page load. Compute the ETag once at start.

**F14. One 922 KB script for every screen**
- Location: `controller-console/vite.config.ts` (`inlineDynamicImports`, owner request CRSP296), `perf-budgets.json`.
- Measured: 281 KB gzip against a 307,200 B budget (8.6% headroom), 227 KB brotli, CSS 147.5 KB raw / 13.9 KB br. First screen: TTFB 1 to 2 ms, FCP 84 to 172 ms, LCP 188 to 556 ms (unthrottled loopback, so network and CPU cost are excluded). One more large screen would break the budget test.

**F15. Page-load numbers are good except the timeline**
- Playwright, unthrottled, 12 loads: TTFB 1 to 2 ms, FCP 84 to 172 ms, LCP: `/` 188 ms, `/pool/live` 400 to 416 ms, `/pool/queue` 392 to 400 ms, `/pool/jobs` 508 to 556 ms, `/pool/machines` 480 to 488 ms, `/analytics` 192 to 200 ms. JS heap about 10 MB. No CPU throttle was run, so INP is not measured.

## 4. What is new since round 1

New in this round: F1 and F2 as handler-level N+1s that survive the CRSP286 store fix, F3 (timeline), F4 (export memory), F5 as a confirmed non-fix of CRSP287, F6 (no coalescing), F7 (reads on the write pool), F8 (write per request), F9 to F12. Fixed and confirmed: static asset compression and immutable caching, the store-level list queries and indexes (00046), the `parentRunsOf` batch, concurrent timeline and overview reads, the tick poll.

## 5. Suggested order for CRSP290

1. F1 + F2 + F11 together: one batched forecast and queue read per request (samples per `(repository, job_ident)` fetched once for the page; `OpenJobs` once; timers in one query), a cheap open-run ETA (no `GetRun` per row), and the index `jobs (repository_id, job_ident, completed_at)`. Target: `runs?limit=50` and `jobs?limit=50` under 50 queries and 200 ms p95 at 50 open runs.
2. F3: timeline reads only the visible window, one count or none; do not mount it before the first paint of `/`.
3. F5: gzip middleware for responses over 1 KB and an ETag per response on the list routes; compression must not break the SSE route.
4. F6: coalesce events per kind (trailing 500 ms to 1 s), one in-flight reload per list.
5. F4: stream the export page by page to the response writer.
6. F7 + F8: move reads to `rdb`, compute group and stats in SQL, touch the session at most once a minute.
7. F9 to F15 as capacity allows.
