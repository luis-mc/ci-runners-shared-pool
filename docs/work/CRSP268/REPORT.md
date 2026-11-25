# CRSP268 report

Plan row D (CRSP251). Branch CRSP268-ci-on-pool.

## Change
- `ci.yml`: lint, types, tests, e2e-shards use `fromJSON(vars.CRSP_<JOB> || vars.CRSP_RUNS_ON_LINUX || '"ubuntu-latest"')` (R5.65).
- Untouched: decide, verify, e2e, e2e-times, test-os, image, release workflows, internal/store, apps/web, PRs #425-#429 files. Job names unchanged, so required checks unchanged.
- Docs: caches spec R5.65, change note CRSP268.

## M2 evidence
- Run https://github.com/luis-mc/ci-runners-shared-pool/actions/runs/45665719815 (head 89fa260, variable unset = hosted fallback path): decide, lint, types, test-os (win, mac), 7 builds green; tests and e2e shards were running at last check; base image amd64 (untouched job) still running after ~20 min, lifecycle skipped. Final result: see the PR's CI run.
- Pool path (variable set to crsp-home labels): NOT proven. No pool access from this cloud session; owner must set `CRSP_RUNS_ON_LINUX` and re-run.

## Checks
- docs_check.py / docs_fold.py: `<luismc-docs>` scripts not found in this sandbox; note written by hand in the standard format.
