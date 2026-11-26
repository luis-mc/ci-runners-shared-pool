---
change: minor
task: CRSP259
---
Config as code built for the API and the console (R5.28 to R5.31, R6.10): `internal/configio` exports the setup (settings, pool, queue, runner sizes, machine settings, image layers, repository policies, notification channels) as one versioned file with secrets named by reference, plans a file (added, changed with old and new value, removed; no secret value accepted), and applies exactly that plan (refuses a stale plan, needs a yes per removal, one audit row per change). Routes: `GET /api/v1/config/export`, `POST /api/v1/config/plans`, `POST /api/v1/config/plans/{id}/apply`. Settings > Data > Config as code holds the export button and the wizard File, Review, Applying, Done. The `crsp config` commands are not built yet.
