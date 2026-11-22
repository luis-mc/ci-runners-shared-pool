# OPS85: open the runs-on pull requests from crsp

Branch `OPS85-open-runs-on-pr`, from `origin/OPS77-integration` (carries OPS73).

## Steps

1. Contract + API + CLI + service (done): `previewRunsOnPullRequests` (GET) and `openRunsOnPullRequests` (POST) on `/api/v1/runs-on-pr`, admin role; `repos.Service.PreviewRunsOnPR/OpenRunsOnPR`; routing service wired in `cmd/crsp/areas_wiring.go`; `crsp repos runs-on-pr [--repo|--all] [--preview]`.
2. Console (done): `RunsOnPr.tsx` pane on the repository page and on the list; preview, open on second press, busy, error, done with link; words in next-version content.md (C13); `RunsOnPr.test.tsx` (6 cases).
3. Specs (github-integration R5.109, web-console R5.123), change notes via docs_fold.py, docs_check (only W1 for this report), fast_guards green (G4 break in internal/agent/idle_cost_unix_test.go is old).

## Decisions

- Idempotence: `routing.Service.OpenPR` returns the stored pull request, and GitHub's `OpenPullRequest` returns an open one of the same branch; the API reports it as `existing`.
- Audit: `runs_on_pr.opened` / `runs_on_pr.existing`, one row per pull request.
- Nothing calls OpenPR except the POST handler: no loop, webhook or schedule.
- For `all`, a refusal or failure of one repository is that row's state; the others go on.
