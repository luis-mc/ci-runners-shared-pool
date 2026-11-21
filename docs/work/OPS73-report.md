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

## Checks run
- `go test` of routing, github, github/fake, repos, store, doctor/...: pass. `go vet ./...` and `GOOS=windows go vet ./...`: clean. golangci-lint on the touched packages: 0 issues. `docs_check.py`: only W1 (this report; strip with `docs_work.py strip` before the join). `fast_guards.py --base-ref auto`: lint and typecheck pass; test-guardrails G1 and G4 breaks are older files (ci.yml, go-test.sh, agent idle_cost test), none of mine.
- The generated docs files were rebuilt with `docs_index.py build` (docs-map.json, index, llms, decisions, features index): small diffs.

## Decisions to check
- `Reserved` labels per runner default 10 (self-hosted, OS, arch, pool name, read-only ones); the join may pass the real count.
- Scan class to size class: light small, normal medium, heavy large (own helper `routing.SizeClassName`; OPS74 owns `sizeclass`).
- Over the cap, class labels are used for the whole repository, and the largest classes are dropped first when even those do not fit.
- The sweep treats any run created before the change as blocking, not only runs that used the old label (conservative).
- `OnPoolOn` runs after the plan is done; if `RUNS_ON_<OS>` is not written yet the jobs are skipped and the next push or reconcile writes them.
- Owner for the pull request: `luis-mc` (`Service.PROwners`).

CODE DONE b65a79abc4b2a3aca600ff913814139e2d407b18
