---
change: minor
task: CRSP229
---
The pool variables are renamed to CRSP_RUNS_ON_<OS> and CRSP_RUNS_ON_<OS>_<SIZE> (GI-1, R5.28). The opposite of the CRSP25 migration deletes RUNS_ON_<OS>, RUNS_ON_LIGHT, RUNS_ON_<OS>_<SIZE>, CI_RUNS_ON and CI_RUNS_ON_WINDOWS only where the store lists them as written by crsp and only after the new names are written. The prefix CRSP_RUNS_ON_ is reserved: a job that would take it is named CRSP_JOB_<NAME>_<hash>, and the routing sync never deletes a pool variable as a stale per-job one (R5.102). The per-job pull request writes fromJSON(vars.CRSP_<JOB> || vars.CRSP_RUNS_ON_<OS> || '"ubuntu-latest"'), also for a workflow that still reads an old name (R5.107). The setting legacy_vars is removed (R5.39); migration 00043 removes it from saved policies.
