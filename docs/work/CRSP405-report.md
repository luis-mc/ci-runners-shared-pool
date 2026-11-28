# CRSP405 report (PC-bound)

Root causes
1. ApplyPlan runs the whole plan inside the apply POST; the console awaited it, so its poll loop never ran and the bars jumped from empty to Done. The running state of a step was never saved. A pool switch was one plan step (switch, group sync and runner registrations together), and variable steps carried the variable name as target so a card never counted them.
2. The two-line card title in the border (280 px column) covered the bar above.

Changes
- Go: steps split (pool_on/off, sync_runners, variables, check_runs_on for a turn on); the running step is saved and published before it starts; ApplyPlan keeps running when the page closes (context.WithoutCancel); plan steps carry repository_id (openapi + generated code + TS).
- Console: apply is posted and the plan read at once; a reload during the run resumes reading and shows the step in hand; one live phase per repository (main bar = repositories finished plus the part of the one in hand); card shows status, step text and its own step bar; failure names the step and error; CardGrid columns={1} (full width).
- Docs: web-console R5.175 and github-integration R5.20, change notes, next-version components 72, content, CHANGES, gallery rebuilt; docs_check strict 0/0.

Tests: Go -race repos, api, cli pass (new TestApplyPlanReportsEachStepAsItHappens); vitest ChangePool (3 new), console screens 104 pass; e2e pool-change-progress.spec.ts 6 pass at 360/393/1300 (judge at all three widths per state).
Known: at 360 px the 32-character name needs two lines (UX-51 clamp, no cut); it still clears the bar. Same 9 vitest failures exist on the base without this branch (oldPaths, lifecycle, five ui css tests), they are environment related.
Review page: docs/work/CRSP405/index.html
