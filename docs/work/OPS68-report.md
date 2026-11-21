# OPS68: updater passes the lifecycle conformance runner

Branch OPS68-updater-conformance. Owns LC-3 to LC-7, LC-25, LC-26, LC-28.

## Done
- internal/update/lifecycle: state machine, schedule, policies, retry rule, Ed25519 manifest, audit rows.
- internal/lifecycletest + cmd/crsp-lifecycle-test: the test interface over HTTP (fake clock, fake server).
- scripts/delivery/lifecycle_conformance.py copied; all 8 checks pass against the test binary.

## Open
- embedded key, asset names, audit wiring, API routes, ci.yml step, docs.
