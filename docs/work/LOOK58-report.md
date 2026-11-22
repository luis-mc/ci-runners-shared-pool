# LOOK58 report: slot timeline and machine health screens (LOOK56 round 2)

Branch LOOK58-timeline-machines (from origin/LOOK56-console-layout 7c46b87, plus origin/OPS83-machine-health).

## Steps
- [x] 1. Branch, merge OPS83 (clean, no conflicts; migration 00040 is OPS83's).
- [~] 2. (agent LOOK58a-timeline-api) Back end: slot timeline API (past by time page, running, forecast placed on slots).
- [~] 3. (agent LOOK58b-timeline-ui) Shared Timeline component in packages/ui, tests, components entries, content words.
- [ ] 4. Reuse in Pool > Live, Queue Analysis, Pool Overview, Jobs why-waiting, machine page; remove duplicate chart paths.
- [~] 5. (agent LOOK58c-machines-ui) Pool > Machines health cards and machine detail page.
- [ ] 6. Specs, change note, e2e at 360/393/1300/1920, guards.
- [ ] 7. CI green, draft PR.

## Notes
- Pre-existing: origin/LOOK56-console-layout is not touched.
- Contract (OpenAPI /api/v1/timeline, Timeline.types.ts) and route stub pushed at 00fe18a; three agent branches fork from it. Field `row_id` (not `row_key`): the secret scan flags `*_key:`.
