# CRSP257 report (plan row G)

## Done
- Step 1: audit hash chain. Migration 00044 (prev_hash, hash), chained insert in `internal/store/ops.go`, `store.VerifyAuditChain`, `audit.Verify`, `audit.CheckChain` (doctor hook), tests green (store, audit, auth).

## Next
- History CSV export (route + button), spec, change note, docs_check, CI.
