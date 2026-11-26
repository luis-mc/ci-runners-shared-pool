# CRSP264 rescue report (branch CRSP264-size-labels-2, PR #454)

Kept: all 3 old commits' code (cherry-pick, no conflicts): sizelabel package, ValidateLabels hook, container cap, API route, openapi, generated Go gen/client.
Dropped: old docs/work/CRSP264/REPORT.md (branch-only, stale).
Changed: regenerated TS schema.d.ts (missing, would fail codegen drift); added spec R5.67a + trace row + change note CRSP264 (written by hand: docs_fold.py not installed here).
Found: sizelabel.Place/Fit not called by scheduler; spec says so.
Not verified: docs_check.py (plugin missing), full CI, Windows/macOS, VM/native backends, web suites.
