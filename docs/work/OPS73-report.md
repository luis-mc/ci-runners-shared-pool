# OPS73 report: per-job routing labels and variables (job sizing wave 1, T2)

Branch `OPS73-per-job-labels`, from RELEASE-52 (085af09b). Back end only.

## Done so far
- `internal/routing`: names (label, variable), label plan with the cap, runner choice by size class and room, workflow patch and unified diff, throttle, service (sync, sweep, assign, PR), in-memory store.
- `internal/github`: runner custom labels (add, remove), branch head, one-PR opener; fake routes for them.
- `internal/store`: `routing.go` (SQL store) and migration `00036_routing_labels.sql`.
- `internal/repos`: runs-on parser and check read the fallback chain; routing labels are not checked against pool label sets.
- `internal/doctor`: runs-on parser reads the chain; new check `workflow.perjob`.

## For the join (not done here, other agents own these files)
- Wiring: build `routing.Service` with `store.Routing()`, `jobsize.Service` (adapter for `Scans`), the REST client as `Vars`/`Labeler`/`PRs`, `repos` workflow files as `Files`, and the open-run source as `Runs`. Call `OptIn` after pool opt-in, `OnPush` from the push webhook handler (`refs/heads/<default>`), `Sweep` from the 5-minute reconcile, `Assign`/`Unassign`/`ServedLabels` from admission (OPS74) and runner creation.
- API endpoints (openapi.yaml untouched): `GET /repositories/{id}/routing` (jobs, variable, label, class, retiring labels, PR), `POST /repositories/{id}/routing/pull-request` (owner repos only, `routing.Service.OpenPR`), `GET /repositories/{id}/routing/patch` (`PatchFor(...).Diff()`, download as `crsp-per-job.patch`).
- Doctor spec row `workflow.perjob` added; doctor check counts in two tests moved 51 to 52 and 53 to 54: expect a trivial conflict if another branch adds a check.

CODE DONE: pending
