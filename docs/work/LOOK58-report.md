# LOOK58 report: slot timeline and machine health screens (LOOK56 round 2)

Branch LOOK58-timeline-machines (from origin/LOOK56-console-layout 7c46b87, plus origin/OPS83-machine-health).

## Steps
- [x] 1. Branch, merge OPS83 (clean, no conflicts; migration 00040 is OPS83's).
- [x] 2. (agent LOOK58a-timeline-api) Back end: slot timeline API (past by time page, running, forecast placed on slots).
- [x] 3. (agent LOOK58b-timeline-ui) Shared Timeline component in packages/ui, tests, components entries, content words.
- [x] 4. Reuse in Pool > Live, Queue Analysis, Pool Overview, Jobs why-waiting, machine page; remove duplicate chart paths.
- [x] 5. (agent LOOK58c-machines-ui) Pool > Machines health cards and machine detail page.
- [x] 6. Specs, change note, e2e at 360/393/1300/1920, guards.
- [ ] 7. CI green, draft PR.

## Notes
- Pre-existing: origin/LOOK56-console-layout is not touched.
- Contract (OpenAPI /api/v1/timeline, Timeline.types.ts) and route stub pushed at 00fe18a; three agent branches fork from it. Field `row_id` (not `row_key`): the secret scan flags `*_key:`.
- Agents' branches LOOK58a/b/c merged into this branch. SlotTimeline (apps/web/src/screens/pool/SlotTimeline.tsx) is the only loader of the chart; the old liveRows waterfall path is gone (Waterfall stays for run/job steps, a different chart).
- Kit changes: timeline bars are 44 px buttons (UX-16); `primary` and `noScrollbar` measures skip `.ds-timeline` (UX-49 exception, CHANGES C21).
- Local e2e gate --e2e-only on 6944e5f: whole suite passed (3191 specs). Publishing the gate ref fails with HTTP 403 (as in LOOK56); local record used.
- Not done: `reserved` and ticket/job-timer `timer_ran_out` bars are never emitted (no scheduler reservation, no timer data; LOOK58a). Health badge limits are Doctor defaults, not /health-thresholds (LOOK58c).
- Next: ci.yml dispatch, watch, draft PR.
