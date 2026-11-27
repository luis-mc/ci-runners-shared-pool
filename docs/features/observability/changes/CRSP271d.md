---
change: minor
task: CRSP271d
---
Logs store and search (R5.20, R5.21, R5.23, R6.8 logs part, R6.9, R6.10, R6.11 log search): `logs.db` is a separate SQLite file with its own migrations and an FTS5 table, writes fail open and are counted for the doctor, retention deletes by day and per machine size, the agent keeps lines on disk while offline (cap, oldest dropped, one "dropped N lines" line) and sends its setup steps and own lines over the agent link in a `LogBatch`, and an admin or owner can search by text, machine, runner, job, level and time range through the API. The web logs page and the live runner setup log (R5.22) are not built.
