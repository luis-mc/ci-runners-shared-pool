# CRSP321 / CRSP375 / CRSP376 / CRSP377 report (RELEASE-73)

Branch CRSP321-leaner-ci, base CRSP383-integration.

## Steps
- [x] 1 CRSP321: pre-push go test is a `-short` smoke of touched packages; gate.json e2e_map.
- [x] 2 CRSP375: config watcher.merge_result=update, infra_retry=true; ci.yml push uses shipped scripts/delivery/ci_skip_check.py; PR config-only skip kept in scripts/ci/pr_config_only.py. Synced shipped delivery_common, branch_gate, fast_guards, e2e_push_check, pass_records, lifecycle_release_check, quarantine_check; gate.json local_gate=smoke.
- [ ] 3 CRSP376: pass records for image, MSI, OS lanes.
- [ ] 4 CRSP377: pre-push lint (web, GOOS=windows), shard weights, Go quarantine.
- [ ] Docs, change notes, docs check, PR.
