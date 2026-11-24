---
change: patch
task: CRSP233
---
The Windows service log no longer goes nowhere when crsp.log cannot be opened after an upgrade: it falls back to crsp-<pid>.log, then the temp folder, and the controller logs the file name and the reason as its first line. The real-machine runbook step 8 uses the per-job CRSP_<JOB> form.
