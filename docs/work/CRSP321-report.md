# CRSP321 / CRSP375 / CRSP376 / CRSP377 report (RELEASE-73)

Branch CRSP321-leaner-ci, base CRSP383-integration.

## Steps
- [x] 1 CRSP321: pre-push go test is a `-short` smoke of touched packages; gate.json e2e_map.
- [ ] 2 CRSP375: watcher.merge_result=update, watcher.infra_retry=true, ci.yml uses shipped ci_skip_check.py.
- [ ] 3 CRSP376: pass records for image, MSI, OS lanes.
- [ ] 4 CRSP377: pre-push lint (web, GOOS=windows), shard weights, Go quarantine.
- [ ] Docs, change notes, docs check, PR.
