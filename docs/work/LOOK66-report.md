# LOOK66 report: Pool > Machines runners and Analytics > Insights waits

Design system v4 followed (changes in next-version, C38). CORE-1, CORE-4, CORE-5, CORE-7 checked; rules UX-1, UX-17, UX-29, UX-72 (shared Table, no app CSS, no hand-made parts). Shared parts: Card, Stack, Table, Text, Strong, Empty, Section, SlotTimeline (Timeline).

## Gap 1: Pool > Machines
- Each machine card holds `Runners on <machine>` with the shared Table (Runner, Type, Size, Doing now); the shared Runners table is gone. Machine cards stack one per row with no Machines wrapper pane (a card grid was too narrow for the table; a card in a pane body breaks title-overlap). The pool-machines shot predates dropping the wrapper title.
- `size_class` added to the Runner contract (additive) and filled from the runner record; shown, default medium.
- Bug: the card got `[]` for runners until the list loaded and said "No runner is on this machine." The card now gets the loaded list and shows loading / error / empty / rows. Tests: `PoolMachines.test.tsx` (late list, four runners, per-machine cards, no shared table).
- Shots: `docs/work/LOOK66-shots/pool-machines-live.png` / `pool-machines-proto.png`.

## Gap 2: Analytics > Insights
- Cause of "not built yet": the controller never mounted the waits reader (`InsightsArea.Waits`). Wired over `store.InsightJobSource` (`cmd/crsp/insights_wiring.go`). Contract first: `getWaitSplit` group `label`, `p90_seconds`. Go: `waits.ByLabel`, P90. Tests in waits, api, cmd/crsp.
- Waits pane: Label, Median, Slowest 10%, slowest first, empty and error states. Runner timeline pane: the shared slot timeline (compact, one row per slot, reserved bars as on Pool > Live; owner decision 22 Nov). Both panes load apart from the overview, so a failed overview no longer hides them.
- Old per-day Waits view and insights-timeline mock panes removed from this page (`insights/Waits.tsx`, `Timeline.tsx`, mocks).
- Shots: `analytics-insights-live.png` / `analytics-insights-proto.png`.

## Not matched
- Prototype shows the timeline row per job with machine tags (M2 BuildTower); live shows one row per slot (owner decision 22 Nov, kept).
- `/api/v1/insights/timeline` stays unwired (no runner-state history table); the console no longer uses it.
- Live pane titles are 2-level panes; prototype's side-by-side split has no shared part, so they stack.
- Prototype machine cards (budget text, CPU/memory charts) and the Failed jobs table are other gaps, not mine.
- Shot data is a mock shaped like the prototype; the live shots use the built bin/crsp with e2e mocks.
