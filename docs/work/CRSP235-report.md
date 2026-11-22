# CRSP235 report: console restart and Gate maximum wait

Status: in progress (branch CRSP235-console-restart-gate-wait). Updated as each step lands.

## Findings
- The controller's own restart already has a console page (`/settings/updates/review?kind=restart`, shared Wizard) but nothing in the console linked to it: only the tray opened it.
- No agent restart existed at all (no API, no agent command).
- The API could read `gate_max_wait_minutes` but PATCH could not write it and nothing validated it.
- The default is already 2 minutes (scheduling R5.1, owner decision 22 Nov 2026 in R5.75): nothing to lower.
