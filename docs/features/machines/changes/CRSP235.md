---
change: minor
task: CRSP235
---
The console restarts the service of an agent machine (R5.81): POST /api/v1/machines/{id}/restart drains the machine, then the agent restarts its own service when no job runs, and the change ends when the agent connects again. Gate maximum wait (1 or 2 minutes) is saved from the machine page (R5.82); the default stays 2.
