---
change: minor
task: CRSP268
---
This repository's own CI (`ci.yml` jobs lint, types, tests, e2e-shards) routes to the pool through `fromJSON(vars.CRSP_<JOB> || vars.CRSP_RUNS_ON_LINUX || '"ubuntu-latest"')`; unset variable means GitHub-hosted fallback (R5.65).
