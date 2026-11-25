# CRSP257 report (plan row G)

## Done
- Step 1: audit hash chain. Migration 00044 (prev_hash, hash), chained insert in `internal/store/ops.go`, `store.VerifyAuditChain`, `audit.Verify`, `audit.CheckChain` (doctor hook), tests green (store, audit, auth).

## Next
- History CSV export (route + button), spec, change note, docs_check, CI.

## Done (cont.)
- Step 2: spec R5.14/R5.28, change note by hand (luismc-docs scripts not in this sandbox; docs_fold/docs_check NOT run).
- R5.14: export route and Export CSV button already existed (OPS34); no duplicate built.
- CI run 45635447424 on a7d4857: all Linux/Windows/macOS test, build, e2e jobs green when read; run overall still finishing.
- Branch renamed CRSP257-integration; docs/work removed from the head before the PR.
