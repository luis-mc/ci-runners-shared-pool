---
id: observability
title: Observability
apps: [controller, agent, web-console, cli]
topics: [observability, scheduling, machines, runners, security, operations, ui]
status: building
version: 0.1.0
owner: owner
type: reference
code:
  - apps/controller/internal/events/**
  - apps/controller-console/src/screens/savings/**
  - apps/controller-console/src/screens/analytics/Savings.tsx
  - apps/controller-console/src/screens/analytics/Compare.tsx
  - apps/controller/internal/overview/**
  - apps/controller/internal/history/**
  - apps/controller/internal/audit/**
  - apps/controller/internal/logs/**
  - apps/controller/internal/compare/**
  - apps/controller/internal/metrics/**
design_system: [UX-6, UX-13, UX-22, UX-24, UX-38, UX-17, UX-18, UX-19, UX-28, UX-29, UX-31, UX-33, UX-34, UX-39, UX-41, UX-52, UX-53, UX-58]
last_verified: 2026-11-28
artifact:
generator: luismc-docs 2.6.1
---
# Observability

Partly built (CRSP18, CRSP23): the audit log (R5.24 to R5.28, with the hash chain), the History CSV export (R5.14), retention (R5.32, R6.8 for audit, job history, raw load samples and log files), the live stream (R5.1 to R5.3, R6.2 load throttle, R6.3), the metrics export (CRSP262, R5.70 to R5.77), minutes and money saved with the rate table and the report (R5.15 to R5.19, CRSP256) and the speed comparison from history (R5.34). Everything else is NOT YET IMPLEMENTED. Every requirement names the milestone that builds it.

## 1. In one paragraph

The owner and the admins of a pool see what the pool is doing right now, what it did, and who changed it. A live view shows the queue, the running jobs and each machine's load (memory, CPU, slots), pushed to the browser as it changes. A job history shows, for each job, how long it waited, how long it ran, where it ran and how it ended. The product counts the minutes the pool saved compared with GitHub-hosted runners. Runner setup logs and agent logs are kept and searchable for a limited time. An audit log records every change: who, what and when. All of this stays on the owner's machines. Nothing is sent anywhere else, and no secret is ever written to a log.

## 2. Why

**Problem.** A shared pool is a black box without this. A job that waits does not say why. A slow machine is not visible until someone logs in to it. A crash leaves no trace. A changed budget has no author. The dev-standards pool learned this the hard way: jobs queued forever because no runner carried a label (product brief, learning 7), and a change was called green before every run on the commit had finished (learning 8).

**Users.**

- The owner: runs the pool, wants to know it is healthy and worth it.
- Admins: fix stuck jobs and machines.
- Viewers: see whether their jobs are queued or running.

**Goals.**

- See the queue, running jobs and machine load live, with the reason for every wait.
- Know each job's wait time, run time, machine, backend and result.
- Know the minutes and the money saved, from a rate the owner can check and change.
- Find the log line that explains a failed runner setup.
- Know who changed what, and when.
- Keep every secret out of every log.

**Non-goals.**

- No telemetry, analytics or hosted error tracker (stack profile, section 14, `STACKSPEC 10.1`). Errors go to local logs, crash files and the doctor.
- No copy of the job's own step logs in the job history; the history links to GitHub. CI run insights may keep a redacted copy on the owner's machines (R5.38, owner decision D4 of 20 Nov 2026).
- No metrics export beyond the one set of R5.70 to R5.77: no tracing, no log export, no custom metrics, no Prometheus remote write (CRSP262).
- Notifications (email, webhook, desktop) are a separate feature (milestone 8).
- The doctor's checks are a separate feature. This spec only feeds it crash files and log health.

## 3. How it works

```mermaid
flowchart LR
  subgraph machine[Each machine]
    ag[Agent] -->|load every 2 s| ag
    ag -->|setup and agent log lines| ag
  end
  ag -->|gRPC agent link| ctl
  gh[GitHub: workflow_job events and REST] --> ctl
  subgraph ctl[Controller]
    sch[Scheduler: queue and slots] --> hub[Event hub]
    hist[History store] --> hub
    logs[Log store: logs.db]
    audit[Audit log]
    red[Redactor]
  end
  ag -.load, job state.-> hub
  ag -.log lines.-> red --> logs
  hub -->|SSE /api/v1/events| web[Web console]
  hub --> cli[CLI watch]
  ctl --> files[Log files and crash files on disk]
```

1. Each agent samples its machine's memory and CPU every 2 seconds and sends the load and its slot use over the agent link (decision 0004).
2. The scheduler changes the queue: a job is queued, gets a slot, starts, ends. Each change goes to the event hub in the same step that writes it.
3. GitHub tells the controller when a job is queued, starts and completes (decision 0002). The controller joins that with its own slot times to get the wait time and the run time.
4. The event hub gives every change a number and pushes it to every open console over server-sent events (SSE). A console that reconnects gets what it missed, or a fresh snapshot.
5. The agent sends runner setup lines and its own log lines to the controller. The redactor removes secrets first, on the agent and again on the controller. The lines go to a separate log database with full-text search.
6. Every change made through the console, CLI, tray or API writes an audit row in the same database transaction as the change.
7. Each process writes structured log files and, when it crashes, a crash file. The next start reports the crash to the doctor and the console.
8. A retention job deletes old history, logs and audit rows by the limits in section 6.

## 4. Screens and states

The screens are the Overview (screen 2), Jobs (screen 6), the Runners and Machines details (screens 4 and 5) and Settings, Audit log (screen 9) from the product brief. No images yet; mocks come with milestone 1 work.

**Overview.** Machines with load bars for memory and CPU, the budget line and slots in use ("3 of 4 slots"). The queue, then the running jobs. Two tiles, *Minutes saved today* and *Money saved today* (an estimate, with the date the rates were copied). The overview only shows and links (design system UX-52). Each status is a word, a glyph and a colour (design system UX-58): `▶ running`, `◷ waiting`, `✓ ok`, `✕ failed`.

**Savings (CRSP256).** Analytics > Savings: a range choice (*Today*, *7 days*, *30 days*, *A range* with two date pickers), three tiles (*Minutes saved*, *Money saved* with the word estimate, *Jobs*), a line that public repositories count minutes but save no money, and three tables (*By repository*, *By system*, *By machine*) with Jobs, Minutes and Money. Empty: "No job ran on the pool in this range." The last line names the page and date the rates were copied from. Analytics > Compare: *Repository* and *Days* fields, *Compare* (busy while it reads) and a table of jobs with the median run on crsp and on hosted runners. Settings > Repositories > *Savings rates*: the source line, a number field per rate, a size choice per machine, *Reset to shipped rates*. Built from the shared parts StatTile, StatGrid, Segmented, DatePicker, Table, Section, Message, NumberField, SelectField, Button; no stylesheet.

**Waits.** A queued job names what it waits for and since when (design system UX-29):

- "waits for a free slot on build-01 (4 of 4 in use)"
- "waits for the load to fall: memory 87%, budget 85%"
- "waits: no online runner carries labels [self-hosted, linux, gpu]"
- "waits for the repository's limit of 2 parallel jobs"

**Running work.** The running-jobs strip and the Overview read one value from the live stream (design system UX-34, UX-19). No screen keeps its own running flag.

**Ageing times.** "Queued 4 min ago" and "running for 12 min" tick from one shared clock (design system UX-53).

**Jobs.** History grouped by period with the range named (design system UX-33). Columns: repository, job, wait, run time, machine, backend, result. A commit row says "all runs done" only when every run on that commit has finished.

**Logs.** One search bar with filter chips (design system UX-31): machine, runner, level, time.

**Audit log.** Read-only rows: when, who, what, target, result.

**Metrics (Settings, Console, Metrics).** Off until the owner turns it on (CRSP262, R5.70 to R5.77). Two check boxes that wait for the tab's Save (UX-22): *Prometheus page* and *Push to OpenTelemetry*. With the page on: the port, *Let other computers read the page* (off by default), *Ask for a key* and a *Make a key* button that shows the key once in a copy block. With the push on: the collector address and the interval. While the page is on, a copy block holds the Prometheus scrape text. A viewer sees every control off with the reason (UX-38). The tab ends with the shared Cancel and Save bar (UX-24). Nothing here is drawn by the page: it uses the shared parts `Section`, `CheckRow`, `NumberField`, `TextField`, `FieldGrid`, `Button`, `CodeBlock`, `Message` and `ErrorSummary`.

**States.**

- Empty: "No jobs yet. Turn the pool on for a repository to start." with a link to Repositories (design system UX-6).
- Loading: the first snapshot shows a busy bar (design system UX-29).
- Live link lost: a Needs-you line "Live updates paused, reconnecting" and the time of the last update; data stays on screen (design system UX-28, UX-58).
- Could not load: says what, why and what next (design system UX-28).
- Charts and bars have a text equivalent (design system UX-17, UX-41).

## 5. Functional requirements

Milestone numbers are those of the product brief.

### Live view

**R5.1** — (M1) The live view shows: every queued job (repository, workflow, job, labels, queued since, wait reason); every running job (repository, job, machine, runner, backend, isolation, started at); every machine (online, offline or drained; memory %, CPU %, budget, slots used and total).

**R5.2** — (M1) The controller pushes the live view over server-sent events at `GET /api/v1/events` (`text/event-stream`). The first event is a full snapshot. Later events are changes. Each event has an increasing `id`.

**R5.3** — (M1) A client that reconnects with `Last-Event-ID` gets every event it missed, in order. When those events are no longer kept, it gets a fresh snapshot instead, marked as such.

**R5.4** — (M1) The web console keeps one live store fed by the stream. The Overview, the running-jobs strip and every running state read that store (design system UX-19, UX-34).

**R5.5** — (M1) Every queued job carries one wait reason from a fixed list: free slot, load over budget, no online runner with the labels, repository parallel limit, machine drained, load not measured (scheduling R5.22, decision D53), unknown. The reason names the machine, the numbers or the labels. "No runner with the labels" is raised once the job has waited 2 minutes (setting) and no online runner carries all its labels (ported from `warn_queued_jobs` in dev-standards `ci_watch.py`). The overview also returns `waiting`: each queued job with its reason in words (scheduling R5.48, CRSP76).

**R5.6** — (M1) Relative times ("4 min ago") read one shared clock that pauses while the page is hidden and catches up when shown (design system UX-53). Statuses use the word, glyph and colour set (design system UX-58).

### Job history

**R5.7** — (M1) The controller stores one history row per job attempt. Key: repository, run id, run attempt, job id. Fields: workflow, job name, head commit, trigger, labels, machine, runner, backend, isolation, queued at, slot at, started at, completed at, wait time, run time, result.

**R5.8** — (M1) Wait time is the time from GitHub's job `created_at` to the job's `started_at`. It is split into GitHub wait (until the slot was asked for) and slot wait (from `queued_at` to `slot_at` in the slot queue). Run time is `started_at` to `completed_at`, minus the slot wait spent inside "Set up job", so the gate is counted once (ported from `build_finished` in dev-standards `scripts/ci_runs.py`).

**R5.9** — (M1) Timing rules, ported from dev-standards `scripts/ci_runs.py` and `ci_watch.py`: a duration is never negative; a missing or zero time (`0001-01-01…`) gives "unknown", never 0; an end before its start gives "unknown"; a re-run counts from its own attempt start, not the first attempt.

**R5.10** — (M1) The result is one of: success, failure, cancelled, timed out, lost (the runner or machine went away). `startup_failure` and `timed_out` count as failure in totals, as in `ci_runs.py`.

**R5.11** — (M1; the Jobs screens of OPS34 show it as runs and jobs, R5.35 to R5.46) History lists newest first, filters by repository, machine, backend and result, and pages through any range. The CLI gives the same rows (`crsp history --json`).

**R5.12** — (M1) A finished history row never changes after its job completes and is never fetched again from GitHub (the cache rule of `ci_runs.py`). A failed GitHub read keeps the stored rows and says the data is stale.

**R5.13** — (M2) A commit view lists every workflow run and job of one head commit. It says "all runs done" only when every run on that commit has finished, including runs started on demand (product brief, learning 8). It names the slowest job, its share of the wall time, and its two slowest steps (ported from `summary_lines` in `ci_watch.py`). Bookkeeping steps (set up job, complete job, post steps) are left out of "now running", as in `ci_runs.py`.

**R5.14** — (M8, Built CRSP257 on the export of OPS34) History exports to CSV with every row of the filter, in the order shown (design system UX-39). The daily summaries export is R5.45. Built: the History page (the Done tab of the Jobs screen) has one Export CSV button, the shared `ExportButton` part, that exports the rows of the filter on screen, runs or jobs. The jobs file is the API route `GET /api/v1/exports/jobs` (viewer role), which walks every page of the filter newest first, never one page, and neutralises text that starts like a formula; the audit log has `GET /api/v1/exports/audit`. The page passes the filter and the file name only. No second button or route was added: the route and the button of OPS34 are the ones R5.14 asked for.

### The Jobs section data and forecast (OPS34)

Owner decisions of 2026-11-18 (RES15 review). Web screens: `web-console` section 4.6. Ingest: `github-integration` R5.96 to R5.99. Queue start range: `scheduling` R5.72.

**R5.35** (OPS34) — **What is stored.** The controller stores runs, attempts, jobs, steps, needs edges, daily summaries and the forecast made when a job started: `job_history` (one row per job attempt, R5.7, with the added columns job key, job type, matrix values, branch, event, pull request, runner kind, clean-up time, cache state, task id, worktree as reported, attempt), `job_steps` (job, number, name, started, completed, result), `job_needs` (repository, run, job name, needed job name), `job_key_daily` (job key, day, count, median and p90 run time, median wait, failures) and `forecast_log` (job, low, middle, high, basis, samples). Package `apps/controller/internal/jobsdata`; the SQLite store is in `apps/controller/internal/store`.

**R5.36** (OPS34, decisions 6, 9, 10) — **Derived fields.** The job key is repository + workflow + job name with matrix values. The job type is the job name without matrix values. The task id is parsed from a branch that starts with one (`<TASK>-<slug>`); the worktree is stored only when the job reported it. The runner kind is persistent or ephemeral. A job is light or heavy by the scheduler's class.

**R5.37** (OPS34, decisions 1, 2) — **Retention.** Job rows (with their runs and attempts) are kept 90 days, step rows 30 days, daily summaries 400 days (R6.8). Rows leave in that order: the daily summary of a job key is written before its raw rows are deleted. The Done list shows 25 rows, *Show more* adds 25 up to 100 per page, and older rows are reached by filter and time range, within the retention. This replaces the "last 100 finished jobs" rule of the earlier Jobs screen (web-console D34).

**R5.38** (OPS34; reworded by CIX7, owner decision D4 of 20 Nov 2026) — **Privacy.** Stored: repository, workflow file name, job name, matrix values, step names, branch name, commit sha, pull request number, timings, runner and machine names. CI run insights may also store, on the owner's machines only, CI job logs, test reports, guardrail reports and the datasets built from them (test results, CPU and memory samples, workflow checks) to improve its features (ci-run-insights R5.2 to R5.6). Every log and report passes the redact package (`apps/controller/internal/redact`) before it is written; the values of the runner's `env:` block are dropped, names kept. Each repository's CI data has a retention limit and a size cap, set in Settings, Data (ci-run-insights R5.5, R6.2). Never stored: secrets, environment values, actor email, commit messages. Step, job and test names are shown as text and never as markup. Everything stays on the owner's machines (R6.7).

**R5.39** (OPS34) — **Read-only API.** `listJobs` (filters: repository, machine, runner, runner kind, job key, branch, result, range, cursor), `getJob` (steps, attempts, needs, history, critical path), `getRun`, `getJobStats` (daily waits and run times, weekday by hour counts, forecast-against-actual points, filtered like `listJobs`) and `getForecast` are viewer role (R6.4).

**R5.40** (OPS34, decisions 4, 7) — **Forecast key, sample, point and range.** One explainable rule, no model. The key of a job is its job key; the key of a step is the job key plus the step name. The sample is the last 30 successful attempts of the key, no older than 90 days; cancelled, failed-early and lost attempts are left out, and a timeout is stored apart and never lowers the forecast. The same machine class is preferred when it has at least 5 samples, else all. The point is the median run time. The range is p25 to p90 of the same sample, never narrower than plus or minus 10 percent of the median, said as "about 6 to 9 min". The basis is shown with it ("median of the last 20").

**R5.41** (OPS34, decision 7) — **Fallback chain.** The job key with 5 or more samples; else the median of the workflow for the same job name; else the median of the repository; else "not enough history yet" and no number. Under 5 runs the screens say "not enough history yet" and show the workflow's or repository's typical time, labelled.

**R5.42** (OPS34) — **Run ETA and drift.** The run ETA is the longest path over the needs graph using each job's forecast; its range is the sum of the p25 values to the sum of the p75 values along the path, marked "about". If the median of the last 10 runs differs from the median of the 20 before by a ratio above 1.25 or below 0.8, and by more than 3 MAD of the older window, the forecast is marked "changed lately" and uses the last 10. A dynamic matrix or a reusable workflow is not forecast beyond what ran ("expands at run time").

**R5.43** (OPS34, decision 8) — **Queue ETA.** The start range of a queued job comes from scheduling R5.72 with the forecasts of R5.40.

**R5.44** (OPS34) — **Forecast against actual.** The forecast made when a job starts is stored in `forecast_log`. Charts show how many finished jobs landed inside their range; the aim is about 80 percent. The page says "not enough finished jobs yet" instead of a percentage below 20 jobs.

**R5.45** (OPS34, decision 12) — **Exports.** The filtered job list exports to CSV in the order shown (R5.14), and the daily summaries (`job_key_daily`) export to CSV. Both are viewer role and carry only fields of R5.38.

**R5.46** (OPS34) — **Steps and needs may be missing.** A job without stored steps or needs edges is shown without them and says why (older than the step retention, not reported, workflow file not read); no number is invented.

### Measured job peaks (OPS66)

**R5.60** (OPS66, owner decisions of 21 Nov 2026 on job sizing, build plan T3) — **Measure each job's peaks.** While a job runs on a crsp runner the agent measures its CPU and memory and reports, per job run: the **peak CPU** (cores; the 95th percentile over the sample windows, one window per sample, so a 1 s window for the cgroup and Job Object readers; the largest window is one noisy reading and is not the peak), the **peak memory** (the largest reading, or the peak the system kept for the whole job when it keeps one: cgroup `memory.peak`, Job Object `PeakJobMemoryUsed`), the **sample count** and the **duration**. All three operating systems are built together. A job the agent cannot read ends with a final batch that says why, and shows "not measured" with the reason, never a zero. Built (OPS66). Source: `apps/controller/internal/jobsize/jobsize.go` `Measure`.

**R5.61** (OPS66) — **Flags.** A run carries `oom_killed` (the system killed it for memory: a cgroup `oom_kill` count above the count at the first sample, or a Job Object that ended a process for a limit) and `cut_short` (it ended before the agent could read it to the end: the agent stopped, the runner went away, the link was lost). A run with either flag, or with fewer than 2 known samples, is not good: it is stored and shown, and counts for no summary (R5.63). Built (OPS66). Source: `apps/controller/internal/jobsize/jobsize.go` `Measurement.Good`; `apps/controller/internal/agent/jobsample_run.go`.

**R5.62** (OPS66) — **One reader per system behind one interface.** The agent's `jobsample.Sampler` has one reader per system: Linux reads the job's cgroup v2 (`cpu.stat` `usage_usec`, `memory.current`, `memory.peak`, `memory.events` `oom_kill`) for a container or VM job, and the `/proc` process tree for a persistent runner; Windows puts the runner's process into a named Job Object and reads `JOBOBJECT_BASIC_AND_IO_ACCOUNTING_INFORMATION` (user and kernel time, terminated processes) and `JOBOBJECT_EXTENDED_LIMIT_INFORMATION.PeakJobMemoryUsed`; macOS lists the process tree with `ps` (`proc_pid_rusage` needs cgo, and the agent is built without it). A Job Object keeps no current memory, only its peak, so its memory samples are the peak so far. The agent attaches at the job-started and job-completed points of its gate (`Gate.Start`, `Gate.Finish`, `Gate.FinishCut`) through the `JobObserver` seam, and sends `JobResourceSamples` over the agent link (protocol version 4; fields `oom_killed`, `cut_short`, `memory_peak_bytes`). Built (OPS66); **open:** the lookup of the runner's process id or cgroup for a started job (`JobSampling.Locate`) has no production implementation yet, so until it lands a job is reported "not measured" with the reason "no runner process for this job" (see section 11). Source: `apps/controller/internal/agent/jobsample/`, `apps/controller/internal/agent/jobsample_run.go`, `apps/controller/internal/agent/gate.go`.

**R5.63** (OPS66) — **Per job key summary.** The controller stores one row per job run (`job_run_peaks`, migration 00034), keyed by repository, workflow, job key and runner, written when the final batch arrives. The summary of a job key (repository, workflow, job key) reads its **last 30 good runs** and gives the **p90** of their peak CPU and of their peak memory, and `measured` is true from **5 good runs** on, the same 5-run rule as the time forecast (R5.41). Under 5 runs the console says "Not measured yet: {n} of 5 good runs" and later sizing work must not use the number. Built (OPS66). Source: `apps/controller/internal/jobsize/jobsize.go` `Summarise`; `apps/controller/internal/store/insights_peaks.go`.

**R5.64** (OPS66) — **Cost and interval.** Sampling stays under 1 percent of one CPU core (R6.4 of ci-run-insights). The readers that cost one or two small reads (cgroup files, a Job Object query) sample every **1 s**: `BenchmarkRealReaders` measures 6 us for the four cgroup files, 0.0006 percent of a core. The process-tree reader lists every process, about 5 us each, so about 2.5 ms on a 500-process machine and once per running job: at 1 s with 8 jobs that would be 2 percent of a core, so it samples every **5 s** (0.4 percent). A batch goes out every 15 s. Built (OPS66). Source: `apps/controller/internal/agent/jobsample/jobsample.go`.

**R5.65** (OPS66) — **Read and show.** `GET /api/v1/jobs/{jobId}/resources` (`getJobResources`, viewer role) carries an optional `measured` object (`JobMeasured`: peak CPU, peak memory, sample count, duration, flags, source, runner, and `key`, the summary of R5.63). The job detail page shows "Measured peak CPU" and "Measured peak memory" with the shared UI parts only (design system v4), the OOM and cut-short flags, and the job key line. Built (OPS66). Source: `apps/controller/internal/api/handlers_insights.go` `GetJobResources`; `apps/controller-console/src/screens/insights/JobInsights.tsx`.

**R5.66** (LOOK58a) — **Slot timeline API.** `GET /api/v1/timeline` (user role; `machine_id`, `before`, `hours`) gives one row per machine job slot (`<machine_id>:<n>`, label "<machine name> slot <n>", n from 1 to the machine's `JobSlots`, the count the queue simulation of R5.43 uses) and a row `<machine_id>:waiting` only when a bar needs it. The store keeps no slot number of a finished job, so each job of the last 24 hours takes the lowest row whose last bar has ended, in order of start, the same on every page. A page holds the jobs that ended before `before` (default now) and at most `hours` back (default 6, at most 24, never older than 24 hours); `more_before` is true while older jobs within 24 hours exist. The first call adds the running bars and the forecast. Queued jobs are placed on slots by the queue simulation of scheduling R5.72 (`forecast.SimulateQueueSlots`): `forecast` from the median start to start plus the job's median, `end_lo` and `end_hi` from the low and high simulations, all clipped at now + 24 hours; a job that would start later waits in the waiting row. `to` is the end of the last forecast, or now. States: `running`, `done` (also cancelled), `failed`, `timer_ran_out` (a job that timed out), `forecast`, `no_runner_fits` and `waiting_room` (wait reasons `no_runner_with_labels` and `load_over_budget`, waiting row, no forecast), `waiting_runner` (a job with no history, or one the simulation cannot place). `reserved` is never sent: the scheduler has no slot reservation yet. A waiting bar starts now, has no end and carries `wait_from`. `detail` is plain words with relative times ("starts in about 12 min").

### Minutes saved

**R5.15** — (M1, Built) Minutes saved counts every job that ran on the pool. Each job counts its run time rounded up to the next whole minute, the way GitHub bills hosted jobs. The Overview shows two tiles, *Minutes saved today* and *Money saved today*, each a link to Analytics > Savings. The live Overview feed counts the same finished pool jobs (`apps/controller/cmd/crsp/overview_source.go`); a finished job counts when it has a machine, a start and an end.

**R5.16** — (M8, Built by CRSP256) Money saved is minutes saved times the per-minute rate of the GitHub-hosted runner that matches the job's OS and size. The rates ship in a table copied from GitHub's published Actions runner pricing (docs.github.com, "Actions runner pricing" in the billing docs). The table stores the source URL and the date it was copied. The figure is labelled "estimate" and shows that date. The rate is picked by the job's OS and the size its machine is mapped to (default `standard`); a job whose OS has no rate adds no money and is counted in `not_priced`. Source: `apps/controller/internal/history/rates.go`, the shipped table in `apps/controller/internal/history/rates.json`. **The shipped rates are those of `apps/controller/internal/compare/prices.json` (private repositories: linux 0.008, linux arm 0.005, windows 0.016, macos 0.08 dollars a minute), dated 2026-11-15; `docs.github.com` could not be reached from the session that built this, so they were not re-copied from the page. Re-read them there and move `copied_on` in `rates.json` with them.**

**R5.17** — (M8, Built by CRSP256) The owner can change every rate, and the size each machine maps to, in Settings and in the config file. A change writes an audit row. "Reset to shipped rates" restores the table. On screen: Settings > Repositories, section *Savings rates* (one number field per rate and one size choice per machine, saved by the tab's one Save; the owner only). Routes `GET`, `PUT` and `DELETE /api/v1/savings/rates`; audit actions `savings.rates.change` and `savings.rates.reset`, written in the same transaction as the change. The config file route is not built: the table lives in the settings row `savings_rates`.

**R5.18** — (M8, Built by CRSP256) Jobs of public repositories count their minutes but save no money, because GitHub-hosted standard runners are free for public repositories. The report says so in its note and counts those minutes in `public_minutes`. A repository counts as public when GitHub says `public`; `internal` repositories are billed and save money.

**R5.19** — (M8, Built by CRSP256) A minutes-saved report covers today, 7 days, 30 days and a chosen range, per repository, OS and machine. On screen: Analytics > Savings (range choice *Today*, *7 days*, *30 days*, *A range* with two date pickers; three tables by repository, system and machine). Route `GET /api/v1/savings?range=today|7d|30d|custom&from&to`; a custom range is at most 366 days and includes its last day. There is no CLI command yet (`x-cli: none`). Source: `apps/controller/internal/history/savings.go`.

### Runner setup and agent logs

**R5.20** — (M1) The agent captures every runner setup step (prepare image, register, start, stop, clean up, deregister) and its own log lines. Each line has time, level, machine, component, and when known runner and job. Lines go to the controller over the agent link.

**R5.21** — (M2) Logs are searchable by full text and by machine, runner, job, level and time range. A result links to its runner and job.

**R5.22** — (M2) A runner's setup log shows live in its detail while setup runs.

**R5.23** — (M1) An agent that cannot reach the controller buffers lines on disk up to its cap (section 6), then sends them in order when it reconnects. When the buffer is full, the oldest lines go first and one line says how many were dropped.

### Audit log

**R5.24** — (M1) Every change writes one audit row: when (UTC), who (GitHub login and user id, or `system`, or the machine for agent actions), source (web, CLI, tray, API, system), action, target type and id, a short before-and-after summary, and the result (done or refused). Changes include: settings, budgets, retention, rates, users and roles, repositories on or off, machines joined, drained or removed, runners created or deleted, secrets rotated (never their values), sign-ins and failed sign-ins.

**R5.25** — (M1) The audit row is written in the same database transaction as the change. When the audit row cannot be written, the change is rolled back and refused.

**R5.26** — (M1) The audit log is append-only. No API, CLI or screen edits or deletes a row. Only the retention job deletes rows past their limit, and it writes its own audit row with the count.

**R5.27** — (M1) Settings, Audit log lists rows newest first and filters by who, action, target and time. The CLI gives the same rows (`crsp audit --json`).

**R5.28** — (M8, Built CRSP257; the doctor's own check is the doctor row's part) Each audit row stores the hash of the row before it. The doctor checks the chain and names the first broken row. Built: migration 00047 adds `prev_hash` and `hash` to `audit_log`. Every audit write, from `audit.Record`, `RecordTx`, the auth area and the old `Tx.Audit`, goes through one insert that reads the last hash and writes the row in the same transaction (the database serialises writers, so two rows never chain to the same row). The hash is SHA-256 over the previous hash and the time, actor, actor id, source, action, target, detail and result of the row, each written with its length (`store.AuditHash`). `audit.Log.Verify` (and `store.VerifyAuditChain`) walks the log oldest first and returns a `ChainReport`: how many rows were checked, how many older rows have no hash, and the id and reason of the first broken row ("was changed after it was written", "does not follow the row before it", "has no hash"). Rows written before the chain have no hash, are counted as "older rows with no hash" and are not an error. The retention job deletes the oldest rows, so the oldest row left starts the check: its own hash is checked, its link to a deleted row is not. The doctor hook is `audit.CheckChain(ctx, checker)`: it returns the report and one line of words, and returns a read error instead of hiding it. The doctor package calls it; this row adds no check to the doctor. Limits: a chain cannot show that rows were cut off the end of the log, and a person who can write the database file can rebuild every hash; the chain finds an edit by mistake or a careless one, it is not a signature.

### Structured logs and crash files

**R5.29** — (M1) Every mode (controller, agent, CLI, tray) logs JSON lines through `log/slog` with: time, level, message, component, version, and the ids it has (machine, runner, job, request). Files rotate by size in the data folder. A process in the foreground also writes readable lines to stderr.

**R5.30** — (M1) A panic in any long-lived goroutine is caught at the top of that goroutine and written to a crash file (`crash-<UTC time>-<mode>.txt`) with version, OS, architecture, mode, the redacted panic message and the stack. A fatal runtime error goes to the same folder through `runtime/debug.SetCrashOutput`.

**R5.31** — (M1) At the next start, the controller finds crash files it has not reported, shows one Problem notice in the console and hands them to the doctor. An agent's crash files go to the controller when it reconnects.

### Metrics export (CRSP262, gap G2 of the runs-on study)

**R5.70** — **Off by default, local by default.** Nothing is served and nothing is sent until the owner turns it on in Settings. The Prometheus page listens on `127.0.0.1` unless the owner chooses *Let other computers read the page*, and then on every address of this PC. The port is 9464 unless changed (1024 to 65535). A change applies at once, with no restart, and writes one audit row (`metrics.changed`) that names what switched and never the key.

**R5.71** — **One metric set.** Both outputs carry the same names and values: `crsp_runners{state}` (runners by state), `crsp_queue_wait_seconds` and `crsp_job_duration_seconds` (histograms with buckets 1, 5, 15, 30, 60, 120, 300, 600, 1800, 3600 s), `crsp_fallback_jobs_total`, `crsp_cache_lookups_total{result}`, `crsp_cache_hit_ratio`, `crsp_machine_uptime_seconds{machine,os}` and `crsp_machine_up{machine,os}`. A test keeps the two outputs on one list of names.

**R5.72** — **Prometheus page.** `GET /metrics` answers the Prometheus text format 0.0.4. It answers `401` with `WWW-Authenticate: Bearer` when a key is asked for and the request has none or a wrong one (constant-time comparison). It serves only `/metrics`, never the console or the API.

**R5.73** — **OpenTelemetry push.** The controller posts OTLP over HTTP with JSON encoding to `<collector>/v1/metrics` (a bare address gets that path) every 10 to 3600 seconds (default 60). Durations are delta histograms, counts are cumulative sums, the rest are gauges. The resource carries `service.name=crsp` and `service.version`. A failed push is logged, shown in the console, and the loop goes on. No library is added: the body is built by `apps/controller/internal/metrics`. A collector address with a user name or password is refused.

**R5.74** — **How the numbers are made.** Runners by state come from the runner records. Job numbers count the jobs that finished since the controller started, so every series only grows. Queue wait is the time from the queue ticket (else GitHub's creation time) to the start; job duration is start to end. A job that ran with no crsp machine and a GitHub-hosted label (`ubuntu-latest`, `windows-latest`, `macos-latest`) counts as a fallback. The cache hit ratio is warm jobs over warm and cold jobs. A machine's uptime is the time since this controller saw it come online, 0 while offline; it is not the machine's boot time. A feed that fails leaves its numbers empty and the others still come out.

**R5.75** — **The key.** *Make a key* makes 32 random hex characters, keeps them in the OS secret store (`crsp/metrics/token`) and returns them once; a new key replaces the old one and the page uses it at once. The key is never in a settings answer, a log, an audit row or the settings database. Turning the key on with none made is refused (`409`). A page that asks for a key never starts without one.

**R5.76** — **Scrape text.** The console gives a ready `prometheus.yml` block for the saved settings: the job name, `localhost` (or this PC's name when exposed), the port, and `YOUR-TOKEN` as a placeholder when a key is asked for. Copy changes nothing.

**R5.77** — **Who and where.** The three routes (`getMetricsSettings`, `putMetricsSettings`, `makeMetricsToken`) are admin role; the owner is above admin. The settings live in the settings table under `metrics_export`. The package `apps/controller/internal/metrics` is pure Go and reads other areas only through the `Feeds` functions the controller gives it (`apps/controller/cmd/crsp/metrics_wiring.go`). **Platforms:** built for Windows, Linux and macOS. Tested on Windows and Linux. macOS is compiled and unit-tested in CI and has not been run on a real Mac.

### Retention

**R5.32** — (M1) A retention job runs every hour and deletes data past the limits in R6.8. Owners change the limits in Settings, within the floor and ceiling. A value outside them is refused with the allowed range.

**R5.33** (M3, NOT YET IMPLEMENTED, CRSP92) — **Speed comparison.** The owner picks one workflow of one repository. crsp runs it on the pool and on GitHub-hosted runners, once each on the same commit, and shows the two times next to each other with the minutes saved (R5.15) and the machine it ran on. The comparison is started by hand, asks first because it uses hosted minutes, runs at most once at a time, and never runs without the owner starting it. The result is kept with the date and the commit, and the console shows a number only when both runs succeeded.

**R5.34** (M3, CRSP92; on screen CRSP256: Analytics > Compare) — **Speed comparison from history.** `crsp compare <owner/repo> [--days N]` and the read-only route `GET /api/v1/compare?repository=owner/repo&days=N` read the finished workflow-run jobs of one repository from GitHub (the window is 14 days when absent, at most 90) and match jobs by name across the two runner kinds: a job whose runner labels hold `self-hosted` ran on crsp, any other ran on a GitHub-hosted runner. Queue wait is started minus created, run time is completed minus started; only successful and failed jobs with real timestamps count. For each job name seen on both kinds the answer holds the median and the 95th percentile of queue wait and run time per kind, the minutes saved (for every crsp run, the hosted median of queue plus run minus the crsp median, so a slower pool shows a negative number) and the hosted cost avoided (each crsp run rounded up to whole minutes, times the per-minute price). The price table is one file, `apps/controller/internal/compare/prices.json`, keyed by `runs-on` label with a default. A public repository is excluded, with the reason, because hosted minutes there are free (R5.18). It reads only and starts no job. An optional workflow template for a fair A/B run is in `docs/guides/speed-comparison-workflow.md`; the by-hand run of R5.33 stays separate. The console shows it in Analytics > Compare: a repository field (owner/repo), a days field, the button *Compare* (busy while it reads), and a table of the jobs with the median run on each kind, minutes saved and cost avoided; a public repository shows the reason, no numbers.

## 6. Non-functional requirements

**R6.1** — (M1) A change on a machine or in the queue reaches an open console within 2 seconds at the 95th percentile on a local network.

**R6.2** — (M1) Load is sampled every 2 seconds and reported as the median of the last 3 samples (medians, as the product brief's load-measurement port from dev-standards `scripts/load.py` and `load_guard.py` asks; the measuring itself is the machines feature's). The hub sends at most one load event per machine per 5 seconds.

**R6.3** — (M1) Stream limits: a comment line every 15 seconds keeps the stream open; the hub keeps the last 2,000 events or 10 minutes for replay, whichever is less; one stream that falls 500 events behind is closed and reconnects to a snapshot; at most 50 open streams per controller (setting, ceiling 200). A slow stream never slows the scheduler.

**R6.4** — (M1) The stream, history, logs and audit need a signed-in session. The stream sends nothing before the session is checked, and closes when the session ends or the user's role is removed. Viewers see the live view and history. Logs and the audit log are for admins and the owner (owner question 3).

**R6.5** — (M1) No secret is ever written to any sink: log files, the log database, crash files, the audit log, the event stream, API error bodies and CLI output. One redactor sits in front of all of them. It removes every secret value the process holds (App private key, webhook secret, installation, OAuth and refresh tokens, registration and remove tokens, JIT configs, join codes, machine keys, session cookies) and every string that matches known secret shapes (`ghp_`, `gho_`, `ghs_`, `ghu_`, `ghr_`, `github_pat_`, `-----BEGIN … PRIVATE KEY-----`, `Authorization:` and `Cookie:` values, JWTs). It replaces each with `[redacted:<kind>]`.

**R6.6** — (M1) A redaction test plants one value of every secret kind in every path that writes to a sink: a log call, an error, a panic, an agent log line, an audit summary, a stream event, an API error. It then reads every sink and fails if any planted value, or its base64 form, appears. A new secret kind cannot be added without adding it to this test.

**R6.7** — (M1) Logs stay local. No logging, history or audit package makes a network call, except the agent link to its own controller. There is no telemetry and no remote log sink (stack profile, `STACKSPEC 10.1`, `10.3`).

**R6.8** — (M1) Retention defaults, floors and ceilings:

| Data | Default | Floor | Ceiling |
|---|---|---|---|
| Event replay buffer (memory) | 2,000 events or 10 min | n/a | 10,000 events |
| Raw load samples (2 s) | 24 hours | 1 hour | 7 days |
| Load per minute (median) | 30 days | 1 day | 400 days |
| Job history rows (jobs, runs, attempts, forecast log) | 90 days | 7 days | 400 days, and at most 1,000,000 rows |
| Job step rows | 30 days | 7 days | 90 days |
| Job daily summaries | 400 days | 90 days | 400 days |
| Measured peaks per job run (`job_run_peaks`, R5.63) | 400 days | 90 days | 400 days |
| Runner setup and agent logs (controller) | 14 days and 512 MiB per machine | 1 day, 64 MiB | 90 days, 10 GiB per machine |
| Agent disk buffer while offline | 64 MiB | 8 MiB | 1 GiB |
| Audit log | 400 days | 90 days | 5 years |
| Structured log files, per mode | 10 files of 50 MiB | 2 files | 50 files |
| Crash files | 30 days, newest 20 | 7 days, 5 files | 365 days, 200 files |
| CI logs and raw reports, files (ci-run-insights R6.2) | 30 days and 1 GiB per repository | 7 days, 64 MiB | 365 days, 20 GiB |
| Test results, workflow checks, guardrail reports (ci-run-insights R6.2) | 90 days | 14 days (checks 30) | 400 days |
| CPU and memory samples per job (ci-run-insights R6.2) | 7 days | 1 day | 30 days |

Whichever of a time or a size limit is reached first applies. The oldest data goes first.

**R6.9** — (M1) Logs fail open: a log write that fails, or a full log store, never stops or slows a job, a runner or the scheduler. The failure is counted and shown to the doctor. The audit log fails closed (R5.25).

**R6.10** — (M1) The log database is a separate SQLite file (`logs.db`) from the main database. Heavy log writes never block a scheduler write.

**R6.11** — (M1) The history list returns a page of 50 rows in under 200 ms with 1,000,000 rows stored. (M2) Log search returns its first page in under 1 second over 10 GiB of logs.

**R6.12** — (M1) Times are stored in UTC and shown in local 24-hour time, year-month-day (design system UX-18).

**R6.13** — (M1) Live numbers and bars have a text equivalent, and screen-reader announcements of live changes are limited to one per 10 seconds per region (design system UX-17).

**R6.14** — (M1) The audit log holds personal data (GitHub login and user id). It is listed in `docs/reference/data-inventory.md` and `docs/reference/data-retention.md` (stack profile, sections 7 and 10) and follows R6.8.

**R6.15** — (CRSP341) The secret scan is never allowlisted. A pull request that adds a line to `.gitleaksignore` fails: the guard `scripts/ci/gitleaksignore_guard.py` runs in the pre-push hook (gate.json `always_on_guards`) and in the CI lint job; removing lines passes. Planted fake secrets in tests are built at run time, never written as literals (AGENTS.md, Secret scan).

**R6.16** — (CRSP321, CRSP375, CRSP376, CRSP377) This repository's own delivery is lean: one full run per change, and a lane runs only when its inputs changed.
- The local gate is a smoke. The pre-push hook runs `go test -short` on the touched packages, the secret scan, the guards, gofmt, `go vet`, golangci-lint (also `GOOS=windows` when Go files changed), `prettier --check`, `pnpm -r lint` when web files changed, and the web typecheck. CI is the one full run (gate.json `local_gate: smoke`; `CRSP_PREPUSH_FULL=1` runs the touched packages in full).
- The watcher updates a ready pull request with the default branch before its run (`watcher.merge_result: update`), so the run tests what will merge and the push to the default branch skips by `scripts/delivery/ci_skip_check.py`. It re-runs a job once when it failed for an infrastructure reason, never a test failure (`watcher.infra_retry: true`).
- The image, Go OS (Windows, macOS) and MSI lanes are skipped only when the default branch holds a pass record with the same inputs key (`scripts/delivery/ci-shards.json`, `pass_records.py to-result`). A lane with no inputs, no key, a failed record or a manual run always runs. A skipped default-branch push copies the merged pull request's records to the default branch.
- A flaky Go test goes into `.delivery/quarantine.json` with a task and a review date. The blocking run leaves it out (`go test -skip`); a non-blocking CI step runs it. It is never retried until it passes. An entry with no task or a past date fails lint.

## 7. Design

### Parts

| Package | Job |
|---|---|
| `apps/controller/internal/events` | Event hub: numbering, replay buffer, snapshot, fan-out, SSE handler |
| `apps/controller/internal/history` | Job history store, timing rules (port of `ci_runs.py`), minutes and money saved, rate table |
| `apps/controller/internal/audit` | Audit writer (inside the caller's transaction), reader, hash chain, retention |
| `apps/controller/internal/logs` | `slog` setup, redactor, file rotation, crash files, agent log buffer, `logs.db` with search, retention |

### Live stream

- `GET /api/v1/events` returns `text/event-stream`. Event types: `snapshot`, `job.queued`, `job.waiting` (reason changed), `job.started`, `job.finished`, `machine.load`, `machine.state`, `runner.state`, `saved.today`. Each `data:` is JSON in the OpenAPI contract (`packages/contract/openapi.yaml`), so the TypeScript types are generated like the rest of the API.
- The hub numbers events from one counter. Each subscriber has a bounded channel. A full channel closes that subscriber (R6.3); it never blocks the publisher.
- The scheduler publishes after its transaction commits, so a client never sees a change that was rolled back.
- The CLI's `crsp status --watch` reads the same stream.

### History

- Table `job_history` in the main database, keyed by (repository, run id, run attempt, job id). Indexes on `completed_at`, repository, machine and result.
- Sources: the `workflow_job` events or the scale-set messages (decision 0002) give GitHub's times and conclusion; the slot queue gives `queued_at`, `slot_at` and `released_at`; the agent gives machine, runner and backend.
- Timing functions are pure and take times as input (no clock inside), like `seconds()` and `build_finished()` in `ci_runs.py`. Their tests port `test_waterfall_times`, `test_failed_step_and_conclusion`, `test_rerun_uses_the_attempt_start`, `test_other_repo_rows_in_the_slot_log_are_ignored`, `test_now_skips_bookkeeping_steps` and `test_cache_reuses_finished_runs_and_skips_job_calls` from dev-standards `claude/plugins/delivery/tests/test_ci_runs.py`.
- Rate table: `apps/controller/internal/history/rates.json`, embedded, with `source_url`, `copied_on` and rows of (OS, size, USD per minute). The owner's overrides live in the settings table.

### Audit

- Table `audit_log` in the main database. Writers call `audit.Record(ctx, tx, entry)` with the transaction of the change. A helper wraps every write handler so a change without an audit row fails a test.
- The summary is built from named fields, never from a raw request body, and passes through the redactor.

### Logs and crash files

- One `slog.Handler` chain per process: redactor, then a fan-out to the rotating JSON file and, in the foreground, stderr. Agent lines also go to the agent-link sender.
- `logs.db` uses an FTS5 table for the message text and plain columns for the filters. Retention deletes by day partitions to keep deletes fast.
- Crash files sit in `<data dir>/crash/`. A small `reported` marker file lists those already shown.

### Why SSE, not WebSocket

The live view flows one way: controller to browser. Commands go through the normal HTTPS API, which already has the contract, sign-in and audit. SSE is plain HTTPS from `net/http`, with no extra library. The browser's `EventSource` reconnects by itself and sends `Last-Event-ID`, which gives replay for free (R5.3). It passes through proxies like any HTTP response, and HTTP/2 removes the old limit of six streams per host. WebSocket would add a library, a second message format to check by hand, and a two-way channel that nothing needs.

## 8. Failure modes

| Dependency | Fails how | Behaviour | Open or closed |
|---|---|---|---|
| Console stream | Network drop, controller restart | Console shows "Live updates paused, reconnecting", keeps the last data, replays or reloads a snapshot | open: the pool keeps running |
| Agent link | Agent offline | Machine shows offline after the timeout (decision 0004); agent buffers logs on disk (R5.23) | open |
| GitHub API or webhooks | Unreachable or rate-limited | History keeps stored rows and says it is stale (R5.12); live queue still shows jobs the scheduler knows | open |
| `logs.db` | Full, locked or corrupt | Lines are dropped and counted; jobs run on; doctor reports it (R6.9) | open |
| Log files | Disk full | Same as `logs.db` | open |
| Audit write | Database error | The change is refused (R5.25) | closed: no change without its author |
| Redactor | Panics or errors | The line is dropped, not written raw | closed: never leak a secret |
| Rate table | Missing or bad | Minutes still show; money shows "rate unknown" | open |

## 9. How it is proven

Rows that name a file without "planned" are built (CRSP18, CRSP23, CRSP271c). Planned rows are NOT YET IMPLEMENTED; their paths are where each test will live.

| Requirement | Test |
|---|---|
| R5.1 | apps/controller/internal/overview/overview_test.go TestGetBuildsOverview, TestQueueListIsCappedButCountIsExact; apps/controller/internal/api/handlers_live_test.go TestGetOverview (the e2e screen test is planned) |
| R5.2 | apps/controller/internal/events/hub_test.go TestFirstEventIsSnapshotThenChangesInOrder; apps/controller/internal/api/handlers_live_test.go TestStreamEventsSnapshotThenChanges |
| R5.3 | apps/controller/internal/events/hub_test.go TestReplayByLastEventID, TestReplayGapSendsMarkedSnapshot, TestReplayAgeLimit; apps/controller/internal/api/handlers_live_test.go TestStreamEventsReplayByLastEventID |
| R5.4 | apps/controller-console/src/live/store.test.ts "overview and strip read one store" |
| R5.5 | apps/controller/internal/scheduler/waitreason_test.go TestWaitReasons, TestNoRunnerWithLabelsAfterThreshold |
| R5.6 | apps/controller-console/src/live/clock.test.tsx "ages tick and pause while hidden"; apps/controller-console/src/live/status.test.tsx |
| R5.7 | apps/controller/internal/store/jobsdata_attempt_test.go TestOneRowPerJobAttempt; apps/controller/internal/store/jobsdata_test.go TestIntegrationUpsertJobKeepsStateAndFacts |
| R5.8 | apps/controller/internal/history/timing_test.go TestWaitSplitGitHubAndSlot, TestSlotGateCountedOnce (port of test_waterfall_times) |
| R5.9 | apps/controller/internal/history/timing_test.go TestNeverNegative, TestZeroTimeIsUnknown, TestRerunUsesAttemptStart |
| R5.10 | apps/controller/internal/history/timing_test.go TestResultMapping (port of test_failed_step_and_conclusion) |
| R5.11 | apps/controller/internal/store/jobsdata_test.go TestIntegrationListJobsFiltersAndPaging; apps/controller/internal/api/handlers_jobsdata_test.go TestListJobsPassesTheFilters; apps/controller/internal/cli/resources_test.go TestHistoryJSON, TestHistoryFilters |
| R5.12 | apps/controller/internal/jobsdata/reconcile/stale_test.go TestFinishedRowNeverRefetched, TestGitHubFailureKeepsRows; apps/controller/internal/store/jobsdata_finished_test.go TestIntegrationFinishedRowNeverChanges, TestIntegrationLostRowTakesGitHubsEnd, TestIntegrationFinishedRowNeverListedForRefetch (the stale marker on a list is in section 11) |
| R5.13 | apps/controller/internal/history/commit_test.go TestAllRunsDoneNeedsEveryRun, TestSlowestJobShare, TestBookkeepingStepsSkipped; apps/controller/internal/store/jobsdata_commit_test.go TestIntegrationRunsOfCommit; apps/controller/internal/api/handlers_commit_test.go TestGetCommitView, TestGetCommitViewRefusals |
| R5.14 | apps/controller/internal/api/handlers_jobs_export_test.go TestExportJobsHoldsEveryRowNewestFirst, TestExportJobsNeutralisesFormulas; apps/controller-console/src/screens/jobs/Jobs.test.tsx (Export CSV) |
| R5.15 | apps/controller/internal/overview/overview_test.go TestRoundUpMinutes, TestGetBuildsOverview; apps/controller/internal/history/savings_test.go TestRoundUpMinutes; apps/controller/internal/store/savings_test.go TestFinishedPoolJobsOnlyPoolJobsInWindow; apps/controller-console/src/screens/savings/savings.test.tsx (Overview tiles) |
| R5.16 | apps/controller/internal/history/savings_test.go TestMoneyUsesMatchingRate, TestMachineSizeChangesTheRate; apps/controller/internal/history/rates_test.go TestRateTableHasSourceAndDate; savings.test.tsx (rates with source and date) |
| R5.17 | apps/controller/internal/history/rates_test.go TestOverrideAndResetWriteAudit, TestValidateRefusesBadTables; apps/controller/internal/api/handlers_savings_test.go TestSavingsRatesAreOwnerOnlyAndValidated |
| R5.18 | apps/controller/internal/history/savings_test.go TestPublicRepoSavesNoMoney; savings.test.tsx (public note) |
| R5.19 | apps/controller/internal/history/savings_test.go TestReportRangesAndGroups; apps/controller/internal/api/handlers_savings_test.go TestSavingsReportJSON; savings.test.tsx (ranges, tables) |
| R5.20 | apps/controller/internal/logs/agentlog_test.go TestSetupStepsCaptured, TestAgentLinesBatchOverTheLink; apps/controller/internal/machines/logbatch_test.go TestLogBatchReachesTheSink; apps/controller/internal/agentlink/logs_test.go TestLogBatchRoundTrip |
| R5.21 | apps/controller/internal/store/logsdb/search_test.go TestFullTextAndFilters; apps/controller/internal/logs/search_test.go TestQueryValidation; apps/controller/internal/api/handlers_logs_test.go TestLogSearchIsAdminOnly (the web page is planned) |
| R5.22 | planned: apps/controller/internal/events/sse_test.go TestRunnerSetupLogLive |
| R5.23 | apps/controller/internal/logs/buffer_test.go TestBufferKeepsOrderAndCountsDropped |
| R5.24 | apps/controller/internal/audit/audit_test.go TestRowFields; apps/controller/internal/api/handlers_ops_test.go TestEveryWriteHandlerRecords |
| R5.25 | apps/controller/internal/audit/audit_test.go TestAuditFailureRollsBackChange |
| R5.26 | apps/controller/internal/audit/audit_test.go TestNoUpdateOrDeletePath, TestRetentionWritesOwnRow; apps/controller/internal/store/ops_test.go TestAuditIsAppendOnly |
| R5.27 | apps/controller/internal/audit/query_test.go TestNewestFirstAndFilters; apps/controller/internal/api/handlers_ops_test.go TestEveryWriteHandlerRecords |
| R5.28 | apps/controller/internal/store/auditchain_test.go TestChainIntactAndLinked, TestChainDetectsFirstBrokenRow, TestChainOldRowsAndRetention; apps/controller/internal/audit/chain_test.go TestChainRecordsAreLinked, TestCheckChainReturnsReadError |
| R5.29 | apps/controller/internal/logs/slog_test.go TestJSONFieldsAndRotation |
| R5.30 | apps/controller/internal/logs/crash_test.go TestPanicWritesCrashFile, TestSetCrashOutputFolder |
| R5.31 | apps/controller/internal/logs/crash_test.go TestUnreportedCrashShownOnce; apps/controller/internal/doctor/checks/crashfiles_test.go TestCrashFilesListed (sending an agent's crash files on reconnect is planned) |
| R5.32 | apps/controller/internal/settings/retention_test.go TestHourlyJobDeletesPastLimit, TestRetentionUsesSettings; apps/controller/internal/settings/settings_test.go TestOutOfRangeRefusedWithRange, TestEveryRangeMatchesFloorAndCeiling |
| R5.33 | planned: apps/controller/internal/bench/compare_test.go TestRunsBothOnTheSameCommit, TestNothingShownWhenARunFails, TestNeverStartsByItself (CRSP92) |
| R5.34 | apps/controller/internal/compare/compare_test.go TestMatchingByNameAcrossKinds, TestMedianAndP95, TestRowStatsAndMinutesSaved, TestCostMath, TestPublicRepositoryExcluded, TestAPISourceEndToEnd; apps/controller-console/src/screens/savings/savings.test.tsx (Compare view); apps/controller/internal/api/handlers_compare_test.go TestSpeedComparisonJSON, TestSpeedComparisonIsReadOnly; apps/controller/internal/cli/resources_test.go TestCompareTableAndJSON |
| R5.35 | apps/controller/internal/store/jobsdata_test.go TestIntegrationJobsMigrationDownAndUp, TestIntegrationUpsertJobKeepsStateAndFacts, TestIntegrationRunsAttemptsCountsAndPaging; apps/controller/internal/jobsdata/ingest/recorder_test.go TestOnJobMapsAndStatesNeverMoveBack, TestSteps |
| R5.36 | apps/controller/internal/jobsdata/types_test.go TestSplitName, TestTaskFromBranch; apps/controller/internal/jobsdata/ingest/recorder_test.go TestOnJobSkipsUnknownRepoAndKeepsKnownFields |
| R5.37 | apps/controller/internal/store/jobsdata_test.go TestIntegrationPruneBoundaries, TestIntegrationListJobsFiltersAndPaging |
| R5.38 | apps/controller/internal/store/no_secrets_test.go TestIntegrationSchemaHasNoSecretColumn |
| R5.39 | apps/controller/internal/api/handlers_jobsdata_test.go TestListJobsPassesTheFilters, TestGetJobDetail, TestGetRunEdgesFromTheWorkflowFile, TestGroupsStatsSummariesAndForecast, TestJobsDataRoutesNeedASignedInUserAndAWiredStore |
| R5.40 | apps/controller/internal/forecast/forecast_test.go TestUsableWindowAndAge, TestPercentile, TestFromSeconds |
| R5.41 | apps/controller/internal/forecast/forecast_test.go TestChainFallback, TestSameMachinePreference |
| R5.42 | apps/controller/internal/forecast/forecast_test.go TestCriticalPath, TestCriticalPathOddGraphs, TestDrift |
| R5.43 | apps/controller/internal/forecast/forecast_test.go TestSimulateQueue, TestSimulateQueueEdges; apps/controller/internal/api/handlers_jobsdata_test.go TestQueuePositionAndEtaFromTheSimulation |
| R5.44 | apps/controller/internal/api/handlers_jobsdata_test.go TestGroupsStatsSummariesAndForecast |
| R5.45 | apps/controller/internal/api/handlers_jobsdata_test.go TestJobsExportWithTheNewFilters, TestExportJobSummariesCSV |
| R5.46 | apps/controller/internal/api/handlers_jobsdata_test.go TestGetJobDetail; apps/controller-console/src/screens/jobs/JobPage.test.tsx |
| R5.60 | apps/controller/internal/jobsize/jobsize_test.go TestMeasurePeaks, TestMeasureSystemPeak; apps/controller/internal/store/insights_peaks_test.go TestPeaksStoreRunAndKeySummary |
| R5.61 | apps/controller/internal/jobsize/jobsize_test.go TestMeasureFlags; apps/controller/internal/agent/jobsample/readers_test.go TestCgroupPeakAndOOM, TestJobObjectSampler, TestCollectorIntervalAndOutcome; apps/controller/internal/agent/jobsample_link_test.go TestJobSampleSenderOutcomeOnFinal; apps/controller/internal/agent/jobsample_run_test.go TestJobSamplingStartSampleFinish |
| R5.62 | apps/controller/internal/agent/jobsample/readers_test.go TestPSTableAndTree, TestParsePSTimeRejects, TestJobObjectSampler, TestCollectorPicksJobObject; apps/controller/internal/agent/jobsample/platform_linux_test.go, platform_darwin_test.go, platform_windows_test.go; apps/controller/internal/agent/jobsample_run_test.go TestJobSamplingUnreadableJob, TestJobSamplingOldController; apps/controller/internal/agent/jobsample_gate_test.go TestGateObserver; apps/controller/internal/agentlink/insights_test.go TestResourceSinkBatchesCarryFlags |
| R5.63 | apps/controller/internal/jobsize/jobsize_test.go TestSummaryMeasuredAtFive, TestSummaryKeepsNewest30; apps/controller/internal/store/insights_peaks_test.go TestPeaksStoreRunAndKeySummary, TestMigrationJobRunPeaks |
| R5.64 | apps/controller/internal/agent/jobsample/sampler_bench_test.go BenchmarkSample, BenchmarkRealReaders |
| R5.65 | apps/controller/internal/api/handlers_insights_test.go TestJobResourcesMeasured; apps/controller-console/src/screens/insights/JobInsights.test.tsx |
| R5.66 | apps/controller/internal/forecast/forecast_test.go TestSimulateQueueSlots; apps/controller/internal/api/timeline_test.go TestTimelineRowsAndSlotAssignment, TestTimelineFinishedAndRunningStates, TestTimelinePagingByTime, TestTimelineKeepsTheRowAcrossPages, TestTimelineMachineFilter, TestTimelineForecastPlacementAndStates, TestTimelineForecastClipsAtTwentyFourHours, TestTimelineJobWithoutHistoryAndPoolJobs, TestGetTimelineHandler |
| R6.1 | apps/controller/internal/events/latency_test.go TestP95UnderTwoSeconds (in-process hub; the network adds the rest) |
| R6.2 | apps/controller/internal/events/hub_test.go TestLoadThrottlePerMachine (the median of three is the machines feature) |
| R6.3 | apps/controller/internal/events/hub_test.go TestSSEFormatAndServe (heartbeat), TestSlowSubscriberIsClosedAndNeverBlocksPublisher, TestStreamCap; apps/controller/internal/api/handlers_live_test.go TestStreamEventsTooManyStreams |
| R6.4 | apps/controller/internal/api/handlers_live_test.go TestStreamNoDataBeforeAuth, TestStreamClosesWhenRoleRemoved; apps/controller/internal/api/handlers_jobsdata_test.go TestJobsDataRoutesNeedASignedInUserAndAWiredStore (the viewer read of the audit log is in section 11) |
| R6.5 | apps/controller/internal/logs/redact_test.go TestKnownValuesAndShapesRedacted |
| R6.6 | apps/controller/internal/logs/redact_sinks_test.go TestNoSecretInAnySink, TestSecretKindTableIsComplete (the agent log line joins with R5.20) |
| R6.7 | apps/controller/internal/logs/local_test.go TestNoNetworkImports (with the depguard rule `logs-stay-local` in .golangci.yml) |
| R6.8 | apps/controller/internal/logs/retention_test.go TestDefaultsFloorsCeilings; apps/controller/internal/store/logsdb/store_test.go TestPruneByDayAndSize |
| R6.9 | apps/controller/internal/logs/failopen_test.go TestFullStoreDoesNotBlockJobs |
| R6.10 | apps/controller/internal/store/logsdb/store_test.go TestSeparateDatabaseFile |
| R6.11 | apps/controller/internal/store/logsdb/bench_test.go BenchmarkSearchFirstPage (100,000 lines, not 10 GiB); planned: apps/controller/internal/history/bench_test.go BenchmarkHistoryPage |
| R6.12 | apps/controller/internal/store/jobsdata_utc_test.go TestJobTimesStoredUTC; apps/controller-console/src/live/format.test.ts |
| R6.13 | packages/ui/src/components/LiveText.test.tsx (the shared part holds the 10 s limit); apps/controller-console/src/screens/pool/poolWords.test.tsx |
| R6.14 | apps/controller/internal/audit/inventory_test.go TestAuditFieldsListedInDataInventory (the inventory file is generated, so the test holds the field list) |
| R6.15 | scripts/ci/test_gitleaksignore_guard.py (an added line fails, a removed line passes) |

**Metrics export (CRSP262).**

| Requirement | Test |
|---|---|
| R5.70 | apps/controller/internal/metrics/metrics_test.go TestConfig, TestServiceLifecycle; apps/controller/internal/metrics/persist_test.go TestDBStoreRoundTripAndAudit; apps/controller/internal/api/handlers_metrics_test.go TestMetricsSettingsOffByDefault |
| R5.71 | apps/controller/internal/metrics/metrics_test.go TestPrometheusText, TestOTLPSameMetricSet |
| R5.72 | apps/controller/internal/metrics/metrics_test.go TestHandlerToken |
| R5.73 | apps/controller/internal/metrics/metrics_test.go TestPushOnce, TestConfig |
| R5.74 | apps/controller/internal/metrics/metrics_test.go TestPoolSource, TestCacheHitRateZeroLookups |
| R5.75 | apps/controller/internal/metrics/persist_test.go TestSecretToken; apps/controller/internal/api/handlers_metrics_test.go TestMetricsSettingsPutChecksAndKeeps |
| R5.76 | apps/controller/internal/metrics/metrics_test.go TestScrapeConfig; apps/controller-console/src/screens/settings/MetricsSettings.test.tsx |
| R5.77 | apps/controller/internal/api/handlers_metrics_test.go TestMetricsSettingsAdminOnly; apps/controller/internal/api route and role tables |

## 10. Decisions

| ID | Question | Chosen | Date |
|---|---|---|---|
| D1 | SSE or WebSocket for the live view? | SSE: one-way flow, plain `net/http`, built-in reconnect and replay, no extra library (section 7) | 2026-11-14 |
| D2 | Where do runner and agent logs live? | A separate SQLite file `logs.db` with FTS5, so log volume never blocks the scheduler | 2026-11-14 |
| D3 | How is an audit row kept with its change? | Same transaction; no audit row, no change | 2026-11-14 |
| D4 | Where does the per-minute rate come from? | A table copied from GitHub's Actions runner pricing, with source URL and date, which the owner can change | 2026-11-14 |
| D5 | How are minutes counted? | Each job's run time rounded up to the whole minute, as GitHub bills | 2026-11-14 |
| D6 | Error tracking? | Local only: structured logs, crash files, doctor (stack profile, section 14) | 2026-11-13 |

Open owner questions:

1. Are the retention defaults in R6.8 right (history 90 days, logs 14 days and 512 MiB per machine, audit 400 days)?
2. The default rates: confirm copying them from GitHub's pricing page at build time. If GitHub charges a per-minute fee for self-hosted runners, should the estimate subtract it (a setting, default 0)?
3. Should viewers see logs and the audit log, or only admins and the owner (R6.4)?
4. Public repositories: count minutes but no money (R5.18)?
5. Is the audit hash chain (R5.28) needed before milestone 8? (Answered by CRSP257: built.)

## 11. Not built yet

CRSP256 gaps, said plainly:

- **G6, the fallback reason next to each fallback run, is not built.** crsp knows why a repository routes to hosted runners now (`GET` repository routing: `why` = `fallback_on` and so on) but stores no reason per run, so a past fallback run has none to show. Showing today's reason beside an old run would be wrong. It needs the reason written on the job row when the fallback passes (`apps/controller/internal/repos/fallback.go` `EvaluateFallback`) and a column on the Jobs and Run elsewhere tables.
- The rates are not re-copied from GitHub's page (it was unreachable from the build session), see R5.16.
- No CLI command for the savings report or the rates yet, and the rates have no config-file route.
- **Platforms.** Built for Windows, Linux and macOS: the report reads the controller's database, so it does not depend on the system. Tested by hand on Linux only here; Windows was not run in this session. macOS is compiled and unit-tested in CI and was not run on a real Mac.

CRSP271c gaps, said plainly:

- **R5.12, the stale marker.** A failed GitHub read keeps every stored row (tested), but the `stale` field of `listJobs` is still always empty: the lists read only the store, never GitHub, and the reconcile pass does not tell the API that its last read failed.
- **R6.4, the audit log for a viewer.** The contract (`listAuditEvents`, `exportAuditEvents`) lets a viewer read the audit log ("owner decision for M1"), while R6.4 and the web-console role table say admin and owner only. Owner question 3 is still open, so the role was not changed and no test pins either way. There is no log reader route yet (`apps/controller/internal/logs` is not built).
- **R5.13, the screen.** The commit view has its data and its API route. No web screen draws it yet.

Everything. The seams that change when it is built:

- `packages/contract/openapi.yaml` gains `/api/v1/events`, `/api/v1/history`, `/api/v1/logs`, `/api/v1/audit` and the event schemas.
- `packages/contract/proto/agent/v1/agent.proto` gains load samples and log-line messages.
- The main database gains `job_history`, `load_minute` and `audit_log` migrations; `logs.db` gets its own migrations.
- The scheduling feature publishes to the event hub and records slot times.
- OPS66 (measured job peaks): the lookup that tells the agent where a started job's processes live (the runner's process id, or the cgroup of a container or VM job: `JobSampling.Locate`) has no production implementation, and the machine-side job-started path does not call `Gate.Start` yet (the gate is the machine's own library, R5.23), so the readers are built and tested but a real job is "not measured" until that lookup lands. The retention of `job_run_peaks` (R6.8) is not wired into the hourly job. Windows reports an out-of-memory kill only through a Job Object limit, which crsp does not set, so `oom_killed` there stays false unless the system ended a process for a limit. The controller's HelloAck offers protocol version 4 only with the registry path (`machines.Hooks.Registry`), so a controller without a registry core gets no samples.

**Platforms (CRSP257).** The chain and the export are built for every system, because the hash is pure Go over the stored text and the export is a controller route. They were tested on Linux, and the Windows run is the CI Windows job. macOS is compiled and unit-tested in CI and was not run on a real Mac.

## 12. Code map

Built (CRSP18): `apps/controller/internal/audit/audit.go` (writer, reader, redaction, prune), `apps/controller/internal/settings/settings.go` and `apps/controller/internal/settings/retention.go` (settings and the hourly retention job), `apps/controller/internal/api/handlers_ops.go`.

Built (CRSP23):

- apps/controller/internal/events/ (hub, SSE writer)
- apps/controller/internal/overview/ (overview and snapshot builder)
- apps/controller/internal/api/handlers_live.go (overview and stream routes)

Source: apps/controller/internal/events/hub.go
Source: apps/controller/internal/events/sse.go
Source: apps/controller/internal/overview/overview.go
Source: apps/controller/internal/api/handlers_live.go

Built (CRSP262): `apps/controller/internal/metrics/` (snapshot and pool source, Prometheus text, OTLP push, handler, config, scrape text, service, settings and key stores), `apps/controller/internal/api/handlers_metrics.go`, `apps/controller/cmd/crsp/metrics_wiring.go`, `apps/controller-console/src/screens/settings/MetricsSettings.tsx`.

Built (CRSP256): `apps/controller/internal/history/` (`rates.go` the rate table and its audited changes, `savings.go` the report and the store source), `apps/controller/internal/store/savings.go` (the finished pool jobs query), `apps/controller/internal/api/handlers_savings.go` (the three routes), `apps/controller/cmd/crsp/overview_source.go` (real minutes for the live Overview), `apps/controller-console/src/screens/savings/`, `apps/controller-console/src/screens/analytics/Savings.tsx` and `Compare.tsx`.

Built (CRSP271b): `apps/controller/internal/logs/` (`slog.go` the logger with rotation, `crash.go` crash files, the `reported` marker and the fatal-error output), `apps/controller/internal/redact/` (the secret kinds `ValueKinds` and `ShapeKinds`, output `[redacted:<kind>]`), `apps/controller/internal/logging/` (tagged handler), `apps/controller/internal/doctor/checks/crashfiles.go`, redactor hooks in `apps/controller/internal/audit/` and `apps/controller/internal/api/errors.go`, the `logs-stay-local` rule in `.golangci.yml`.

Built (CRSP271c): the history rows are the jobsdata tables of R5.35 (`apps/controller/internal/jobsdata`, `apps/controller/internal/store/jobsdata*.go`, `apps/controller/internal/jobsdata/reconcile`, `apps/controller/internal/api/handlers_jobsdata*.go`), not a separate `apps/controller/internal/history` store. `crsp history` (`apps/controller/internal/cli/cmd_history.go`) prints the rows of `listJobs` that are done. The commit view of R5.13 is `apps/controller/internal/history/commit.go` (pure functions) and `GET /api/v1/commits/{sha}` (`getCommitView`, viewer role, `apps/controller/internal/api/handlers_commit.go`). `apps/controller/internal/events/latency_test.go` proves R6.1 on the in-process hub. `packages/ui/src/components/LiveText.tsx` is the shared live text of R6.13 (component 82), used for the four counts of `apps/controller-console/src/screens/pool/PoolSection.tsx`. `apps/controller/internal/api/handlers_live.go` asks the session again every 10 s and closes an open stream whose user is signed out or has no role (R6.4).

Planned files, in the globs of the front matter:

- apps/controller/internal/history/ (store, savings, rates.json)
- apps/controller/internal/audit/ (chain)
- apps/controller/internal/logs/ (the live runner setup log stream, R5.22)

Built (CRSP271d): `apps/controller/internal/logs/` (`store.go` the line and the store port, `failopen.go` the fail-open writer and its failure counter, `retention.go` limits, `buffer.go` the agent disk buffer, `agentlog.go` the agent's log handler and batch sender, `search.go` the query), `apps/controller/internal/store/logsdb/` (`logs.db`, its own migrations with an FTS5 table; it sits in `apps/controller/internal/store/` because only that folder may import the SQLite driver), `apps/controller/internal/api/handlers_logs.go` (the search route), `apps/controller/internal/logs/logsapi/` (the adapter to the API port), `apps/controller/internal/agentlink/logs.go` (the batch and the controller sink), the `OnLogBatch` hook in `apps/controller/internal/machines/link.go`, `LogForwarder` in `apps/controller/internal/agent/run.go`, and the `logs.db` open in `apps/controller/cmd/crsp/machines_wiring.go`.

Built (CRSP271): `apps/controller/internal/history/timing.go` (wait split, never negative, zero time unknown, re-run from attempt start, result mapping and totals), `apps/controller/internal/scheduler/waitreason.go` (the 2-minute no-runner-with-labels rule), the web console reuses the shared UI parts that already hold the time format (`packages/ui/src/components/format.ts`), the shared clock (`packages/ui/src/components/Clock.tsx`) and the status word, glyph and colour set (`packages/ui/src/components/Status.tsx`), proven by `apps/controller-console/src/live/format.test.ts`, `clock.test.tsx`, `status.test.tsx` and `store.test.ts`; `apps/controller/internal/store/jobsdata_utc_test.go` proves UTC storage.

Built (OPS66): `apps/controller/internal/jobsize/` (the shared peak and summary logic), `apps/controller/internal/agent/jobsample/` (the readers and the collector), `apps/controller/internal/agent/jobsample_run.go` and `apps/controller/internal/agent/gate.go` (the attach points), `apps/controller/internal/store/insights_peaks.go` and migration 00034 (the stored runs and the job key summary), `apps/controller/internal/agentlink/insights.go` (the sink), `apps/controller/internal/api/handlers_insights.go` (the field).

Ported from dev-standards (cite in each ported file): `claude/plugins/delivery/scripts/ci_runs.py`, `claude/plugins/delivery/templates/shared/scripts/delivery/ci_watch.py`, `claude/plugins/delivery/tests/test_ci_runs.py`, `claude/plugins/delivery/scripts/load.py`.

## Design-system proof

A rule this spec cites is proven by a test that exists. A rule whose guard is still `none yet` (ux-rules.md) is held by the follow guard, which fails when the rule table is malformed or a citation does not resolve.

Proof: UX-6, UX-18, UX-19, UX-28, UX-29, UX-31, UX-33, UX-34, UX-39, UX-41, UX-52, UX-53, UX-58 `scripts/design/design-system-follow.test.ts`
Proof: UX-17 `e2e/tests/web-console/ux-routes.spec.ts`
Proof: UX-13, UX-38 `scripts/design/wizard-behaviour.test.ts`
Proof: UX-22, UX-24 `scripts/design/design-system-follow.test.ts`
