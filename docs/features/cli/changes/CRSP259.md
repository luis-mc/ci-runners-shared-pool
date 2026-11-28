---
change: minor
task: CRSP259
---
crsp config export, plan, apply and import --dry-run are built: plan and --dry-run change nothing and exit 8 when the file has changes; apply asks for a yes and sends each removal by name; import without --dry-run exits 2 and points to apply.
