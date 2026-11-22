# OPS85: open the runs-on pull requests from crsp

Branch `OPS85-open-runs-on-pr`, from `origin/OPS77-integration` (carries OPS73).

## Steps

1. Contract + API + CLI + service (done): `previewRunsOnPullRequests` (GET) and `openRunsOnPullRequests` (POST) on `/api/v1/runs-on-pr`, admin role; `repos.Service.PreviewRunsOnPR/OpenRunsOnPR`; routing service wired in `cmd/crsp/areas_wiring.go`; `crsp repos runs-on-pr [--repo|--all] [--preview]`.
2. Console (todo).
3. Specs, change note, checks (todo).

## Decisions

- Idempotence: `routing.Service.OpenPR` returns the stored pull request, and GitHub's `OpenPullRequest` returns an open one of the same branch; the API reports it as `existing`.
- Audit: `runs_on_pr.opened` / `runs_on_pr.existing`, one row per pull request.
- Nothing calls OpenPR except the POST handler: no loop, webhook or schedule.
- For `all`, a refusal or failure of one repository is that row's state; the others go on.
