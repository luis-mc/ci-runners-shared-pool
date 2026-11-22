# LOOK58a report: timeline API (back end)

Branch LOOK58a-timeline-api (from LOOK58-timeline-machines). Contract unchanged, no generated file touched.

## Done
- internal/forecast/queue.go: SlotJob.Slot (optional, 1-based) and SimulateQueueSlots (SimulateQueue plus the slot of each queued job in the median simulation). SimulateQueue unchanged.
- internal/api/timeline_build.go: pure buildTimeline (rows, greedy slot assignment, paging, forecast placement, 24 h clip, states, plain-word details).
- internal/api/handlers_timeline.go: GetTimeline replaces the 501 stub; reads machines, finished jobs (ListJobs paged), OpenJobs, forecasts; clock is s.now().
- Tests: internal/forecast/forecast_test.go TestSimulateQueueSlots; internal/api/timeline_test.go (9 tests, no sleeps).
- Docs: observability spec R5.66 with test row; change note docs/features/observability/changes/LOOK58.md.

## Decisions
- Slot count per machine = Settings.JobSlots (same as slotCounts / queue simulation; light and heavy not told apart). Rows grow if past jobs overlapped more.
- Store has no slot number for finished jobs (slots table holds current, per-kind numbers only): slots assigned greedily over the last 24 h so rows are stable across pages.
- Page window: jobs whose end is in [max(before-hours, now-24h), before); more_before = older finished job within 24 h exists (for the filtered machine).
- States emitted: running, done, failed, timer_ran_out (finished TimedOut only), forecast, no_runner_fits, waiting_room, waiting_runner (no history, not placeable, or starts after now+24 h).
- reserved is never emitted: the scheduler has no slot reservation concept. Ticket-expiry timer_ran_out not emitted either: jobsdata carries no ticket expiry or job timer.
- Queued jobs with no machine yet are simulated over the whole pool (slots numbered machine after machine) and placed on the machine owning the slot; unplaced ones go to the first machine's waiting row. Queued jobs of a removed machine are skipped.
- Waiting bars: start = now, no end, wait_from = queued_at (else created_at).
- hours > 24 is clamped to 24, < 1 to 6 (the server does not validate query ranges).

## Checks
go test ./internal/forecast/... ./internal/api/... ./internal/store/... green; golangci-lint 0 issues. docs_check: only W1 (docs/work files on branch, expected; pre-existing LOOK56 reports too).

Push note: the pre-push guard test-guardrails (G1, G4) reports pre-existing breaks (conftest.py, ci.yml, go-test.sh, idle_cost_unix_test.go); mode warn, not mine.

CODE DONE f1f2632
