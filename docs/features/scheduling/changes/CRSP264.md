---
change: minor
task: CRSP264
---
Adds R5.67a: free-form size labels `cpu=N` and `ram=N` in `runs-on`. They are checked as generic labels, lower the job container's CPU and memory caps (never raise them), and `GET /api/v1/size-labels` lists them with an example and the room of each machine. Not built: choosing a machine by the label, the console screen, the CLI command.
