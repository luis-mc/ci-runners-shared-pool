# LOOK65 report: Analytics and reserved jobs

Branch `LOOK65-analytics-and-reserved`, from `origin/LOOK59-look54-contract`, merged with `origin/main` (already up to date). Rides RELEASE-63.

Design system: highest numbered folder `v4`, plus the draft `next-version` (the words and the CHANGES entry C37 went there). Core UX Principles checked: CORE-1 to CORE-7. Rules used: UX-4 (one primary), UX-10 (card), UX-25 and UX-72 (tables), UX-41 (charts have a table view), UX-58 (state is never colour alone), UX-75 (insight tiles), UX-76 (confidence words). Components: Pane, Section, Table, Tag, Status, LinkButton, TrendChart, Timeline (component 77), Segmented. No app CSS, no `style=`, no hand-made table or button.

## What I built, by gap

Screenshot pairs are in `docs/work/LOOK65-shots/` as `<page>-live.png` and `<page>-proto.png`.

| Gap | Built | Screenshots |
|---|---|---|
| Suggestions columns | The pane *Suggestions, best first* is a table with `#`, `Change` (title linking to the suggestion, then `Fix: ...` inline), `Sure?` (a Tag), `Saves a week` (`about N min`) and `Who decides`. One primary, *Review the first change*, in the action bar at the foot (UX-71; the `analytics-suggestions-live` shot was taken before it moved there, so it shows the button under the table). The segments Do next, Tests, Guardrails, the project chips and the weekly digest stay (not in the brief). | `analytics-suggestions` |
| Charts | New first: one pane `{machine}: CPU today` for each machine (real `cpu_pct` since midnight from `getMachineLoad`) and `Free CPU, with and without a reservation` (two charts of a worked example). The existing run charts stay below. | `analytics-charts` |
| Elsewhere | `Elsewhere.tsx`: the plain table Job, Repository, Runner, Took, Result. Search, State, Runs/Jobs and Show more are gone from this tab. `routes.tsx` points at it. | `analytics-elsewhere` |
| Insights | Below the fold the live page had *Wait, hold and run per day* with range buttons and long tables per lane. Now two full-width panes, one under the other: *Waits* (Label, Median, Slowest 5%) and *Runner timeline* (the shared timeline, one row for each runner). | `analytics-insights` |
| Reserved, Go and API | See below. | `pool-queue`, `pool-live` |
| Queue chip | `Reserved` chip in Pool > Queue Analysis; *Waiting for room* leaves reserved jobs out. | `pool-queue` |
| Slot timeline | A `reserved` bar in the waiting row of the machine; a `timer_ran_out` bar for a running job whose timer ran out, ending at now. The shared `Timeline` now ends a `timer_ran_out` bar that has no end at now. | `pool-live` |

### Reserved jobs and timer ran out, end to end

- Contract first (pushed as the first commit, so LOOK62 can build on it): `WaitReason` gains `reserved`; `Job` gains `reserved_since` and `timer_deadline`; `TimelineBar` gains `expires_at` and `reserved_since`. Generated Go (server and client) and TS regenerated with `oapi-codegen` and the contract codegen.
- Reservation lives in two places: the scheduler admission (`internal/scheduler/admission.go`, R5.93: a waiter becomes the machine's reserved job after 3 passes or 10 minutes) and the waiting lease of the assign controller (`internal/assign`, `ReservedSince`). The scheduler now answers `WaitReserved` (`reserved`) for the reserved job; the API overlays the lease's `ReservedSince` on the job list, the overview (and its `waiting` reasons) and the timeline (`internal/api/reserved.go`).
- The timer that runs out is the job timer in the timer engine (`internal/timers`, state `grace`, R5.100). The API reads `JobByID` for running jobs and sets `timer_deadline` on the job and a `timer_ran_out` bar with `expires_at` on the timeline.
- Go unit tests: `internal/scheduler/admission_test.go` (the reserved job waits as `reserved`, the jobs behind it as `free_slot`), `internal/api/reserved_test.go` (leases to reserved map, grace only for a timer that ran out, the reserved bar, the timer ran out bar, job marks, overview marks). Web tests: `Timeline.test.tsx`, `QueuePlan.test.tsx`.

## What I could not match, and why

- **Sure?** The API has no confidence field. The word is derived in the browser (`confidenceOf`): a change crsp can make from measured numbers (it has an `action_kind`) is *sure*, a finding that saves minutes is *likely*, the rest *a guess*. Replace with a field when the engine gives one.
- **Who decides** is also derived: *your yes* when the finding has an `action_kind`, else *you, in the repository*. The prototype says *your yes* for every row.
- **Suggestions keeps the segments, the project chips and the weekly digest**, which the prototype does not show. They are outside the brief's list.
- **Slowest 5%, not 10%** in the Waits pane: `getWaitSplit` gives the 95th percentile, and the word says what the number is. The prototype's *Label* is a runner label; the API splits waits by repository, job key or day, so *Label* shows the job key.
- **Waits and Runner timeline are stacked, not side by side.** In a half-width pane the timeline's table view wraps its heads (the `tableHead` guard fails), so each pane has the full width.
- **Charts: the 7/14/30 day switch sits in the first run chart's pane**, not above the page, so the panes keep the 32 px section gap (`section-gap` guard).
- **CPU today is percent, not cores.** `getMachineLoad` gives `cpu_pct` only, so the panes show percent of the CPU the machine gives the pool, not "5 of 6".
- **Free CPU with and without a reservation is a worked example** with fixed numbers (labelled as one). The controller records no free-CPU series that would let it draw the real thing.
- **Runner timeline has no idle bars** (idle time is the gap) and no *idle while a job waited* mark; the old per-lane tables with those words are gone from Insights.
- **Failed jobs on Insights** is still a card with a sentence; the prototype shows a table (Job, Repository, When, Seen). The gap list had it as present, so I did not change it.
- **The reserved bar has no end**: the job starts when its room is free, which the controller cannot say beforehand. The bar sits at now in the waiting row. The expected start of a reservation is not on the API yet (`Lease` has none).
- The insight hooks (`findings`, `summary`, `digests`, `waits`, `timeline`) read `mock.ts`; they now call the API. A finding page, plans, sizing, tests, guardrails and savings still read `mock.ts`.
- `docs_check` reports only W1 (the files under `docs/work/` on this branch, which the join strips).

## Proof

- `bin/crsp` built with `go tool task build`. The console has no demo data of its own: the e2e specs run against the binary with the browser-side API mocks of `e2e/tests/web-console/`. The shots use richer mocks shaped like the prototype's sample data (3 machines, 4 suggestions, 4 queued jobs with one reserved, a timer ran out bar), at 1278x1248, full page.
- New e2e spec `analytics-layout.spec.ts` (DOM layout at 360, 393 and 1300 px: no sideways scroll, the columns and panes, the Reserved chip, the reserved and timer ran out bars) and fixtures `insights-fixtures.ts` (also wired into `ux-guards.ts`, so the UX guards see the insights API).
- Branch e2e gate, whole suite (3212 cases), before the last two fixes: 3207 passed, 3 failed (`tableHead` on Insights, `section-gap` on Charts in dark and light). Both fixed; the 420 Analytics and queue cases pass.
