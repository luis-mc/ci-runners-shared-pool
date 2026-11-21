---
change: minor
task: CIX24
---
A run is not finished while the run it started next is (owner decision D27): follow-up runs (event workflow_run, such as Installers after CI) are linked to their parent run, getRun returns started_next, run summaries carry overall_state with the word follow-up running, the Run page and Done list say Started next, and the timeline shows the runner wait of a follow-up job (R5.46 to R5.49).
