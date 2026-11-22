# LOOK58c report: Pool > Machines health cards and the machine detail page

Branch LOOK58c-machines-ui (from origin/LOOK58-timeline-machines, merged again at 4707f66 "merge Timeline part").

## Steps
- [x] 1. Words and data hooks: `machineHealth.ts`, `MachineStats.tsx`.
- [x] 2. Machine card with health badges, key numbers, job stats (24 h / 7 days), runners with stats: `PoolMachineCard.tsx`; `PoolMachines.tsx` uses it and keeps `withLiveLoad`.
- [x] 3. Machine page `PoolMachineDetail.tsx`, route `/pool/machines/:machineId` (lazy, in `screens/jobs/routes.tsx`, outside the Pool tab frame like the run page), `MachineTimeline.tsx` slot mounted under "Jobs over time".
- [x] 4. Unit tests (25 new in 2 files), bundle size.
- [x] 5. e2e `pool-machines.spec.ts` (15 cases at 360, 393, 1300 px), `ux-routes` and `a11y` for the touched routes, perf spec: green.
- [x] 6. Docs: web-console spec R5.154, R5.155 with test rows and code map, content.md "Machine health words", CHANGES C20, change note `docs/features/web-console/changes/LOOK58.md`, docs_check (only W1, the branch-only reports).

## Notes
- No new shared part was needed: the health badges are the shared `Status` (word, glyph, colour), the key numbers `Facts` and `StatTile`, the bars `LevelBar`, the tables `Table`, the switch `Segmented` (no "All" option). No packages/ui change, so no components.md entry.
- The health API gives the last report and the days until full per volume; it gives no series. The machine page therefore shows bars (`LevelBar`) and tables of what was measured and draws no history.
- Unknown values: `unknown on <Linux|Windows|macOS>` from `machine.os`; the page appends the API's `unknown_reason`.
- Health badge limits are the Doctor defaults (disk free under 10%, fills in under 3 days, clock skew over 30 s), named constants in `machineHealth.ts`; they are not read from `/health-thresholds`.
- Bundle: the first JavaScript was 306 777 bytes gzipped of 307 200 on the base (margin 0.4 KB, before this task). To reach a margin over 4 KB without raising the budget, the Repositories screens (`RepositoriesList`, `RepositoryDetail`) now load with their routes (`later(..., true)` in App.tsx): 300 812 bytes, margin 6.4 KB. The loading state keeps its one h1.
- Not touched: the Timeline component, Pool > Live, Go code.
