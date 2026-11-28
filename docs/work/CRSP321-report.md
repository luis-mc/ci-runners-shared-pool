# CRSP321 / CRSP375 / CRSP376 / CRSP377 report (RELEASE-73)

Branch CRSP321-leaner-ci, base CRSP383-integration.

## Steps
- [x] 1 CRSP321: pre-push go test is a `-short` smoke of touched packages; gate.json e2e_map.
- [x] 2 CRSP375: config watcher.merge_result=update, infra_retry=true; ci.yml push uses shipped scripts/delivery/ci_skip_check.py; PR config-only skip kept in scripts/ci/pr_config_only.py. Synced shipped delivery_common, branch_gate, fast_guards, e2e_push_check, pass_records, lifecycle_release_check, quarantine_check; gate.json local_gate=smoke.
- [x] 3 CRSP376: scripts/delivery/ci-shards.json (image, test-os, msi inputs); ci.yml lane-records + pass-records jobs; lifecycle.yml records job writes record-backed lane results (pass_records.py to-result, DL-118); copy-to-main on a skipped main push.
- [x] 4 CRSP377: pre-push web lint + GOOS=windows lint, gate.json lint-windows, lint-web, quarantine-valid; Go quarantine (.delivery/quarantine.json, go-test.sh -skip, non-blocking CI step). S5 weights NOT refreshed: see open questions.
- [x] Docs, change notes (observability CRSP321, 375, 376, 377), docs_check strict 0/0.

## Tests
- pytest scripts/ci: 133 passed (new: test_pass_record_results, test_quarantine_check, test_pr_config_only, test_ci_workflow_records).
- actionlint ci.yml: clean. lifecycle.yml: only the pre-existing unknown label windows-11-arm (old actionlint label list).
- go-test.sh quarantine: `-skip` leaves the test out (0 runs), `GO_TEST_QUARANTINE=only` runs just it; verified locally with a temporary entry.
- docs_check --strict: 0 errors 0 warnings.

## Open questions
- S5 (e2e weights) not done: the artifact host (productionresultssa13.blob.core.windows.net) is blocked in the agent sandbox, and the Playwright line reporter in job logs has no per-test times. Latest green full run with times: 46107908241 (artifacts ci-e2e-times-1..4). shard_balance.py update drops `routes_wall_seconds` and Playwright report paths (tests/...) differ from the weights keys (e2e/tests/...), so it needs a careful run where artifacts download.
- pass-records job writes to the orphan branch ci-pass-records with `contents: write` (this job only). Confirm the branch is not protected.
- Pass-record lane results are untested in a real CI run; first release PR after merge shows the hit or miss in the `lane pass record(s)` jobs.
