# CRSP402 report
Routing: CI_RUNS_ON / CI_RUNS_ON_WINDOWS / CI_RUNS_ON_<OS>[_<SIZE>] routed; old var kept as fallback; no default stays none.
Proof on real files (7 repos, read-only, MakePatch only; nothing opened):
- comics-organizer: 18 workflows, 17 files, 50 lines
- moobable: 7 workflows, 7 files, 9 lines
- home-automation: 6 workflows, 4 files, 12 lines
- lan-link: 8 workflows, 7 files, 15 lines
- padevia: 1 workflow (hosted literal windows-latest), 0 changes
- personal-finances-management: 1 workflow (literal ubuntu-latest), 0 changes
- shared-product-framework: no workflows, 0 changes
All patched files idempotent; no skipped multi-line.
