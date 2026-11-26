---
change: minor
task: CRSP271
---
The lifecycle upgrade check leaves logs.db (the log store, R6.10) and its journal files out of the byte comparison by name, as it does the logs folder and controller.db: the service opens and migrates it on every start, and no user data is in it.
