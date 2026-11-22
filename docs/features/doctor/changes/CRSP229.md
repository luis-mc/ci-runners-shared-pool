---
change: minor
task: CRSP229
---
The doctor reads and names CRSP_RUNS_ON_<OS> (D4). Its fix texts for workflow.labels and workflow.generic give the per-job form fromJSON(vars.CRSP_<JOB> || vars.CRSP_RUNS_ON_<OS> || '"ubuntu-latest"') (R5.18, R5.19). A workflow that still reads RUNS_ON_<OS> counts for that OS and gets the patch that writes the new name (workflow.perjob). CI_RUNS_ON and CI_RUNS_ON_WINDOWS are no longer read as crsp's.
