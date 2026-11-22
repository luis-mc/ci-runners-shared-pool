# OPS80 report: runners per job before assignment

Branch OPS80-before-assignment (merge commits of OPS73 and OPS74 on origin/main).

## Plan
1. runners: routing labels pass ValidateLabels; JobRunner carries the job label.
2. scheduler: CheckRoom (pure admission entry point: class, pool CPU and memory, load, reservation, backfill, warm runners released first).
3. assign: controller, leases (migration 00038), poller (one for all repos), warm runner, unused-runner sweep, queue reasons.
4. github: workflow_job assign events, conditional job reads (ETag).
5. store: assign leases and warm runners.
6. doctor: unserved job, 12 h alert, RUNS_ON_<OS> check.
7. openapi: queue reason and leases; codegen.
8. specs: runners, github-integration; change notes.

## Progress
- [ ] steps above, see git log.
