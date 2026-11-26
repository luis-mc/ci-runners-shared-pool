# CRSP313: balanced e2e shards, Go race tests split by package

Branch CRSP313-integration. Source: docs/work/ci-leaner-findings.html (S5, S7), CI run 45775090128.

## S5 e2e shards
- Cause: the 223 s a11y file ran alone on shard 1 while every shard also ran a quarter of ux-routes.spec.ts (about 340 s each). Files were balanced by serial seconds although 4 workers run files side by side.
- Change: `scripts/ci/e2e_plan.py` models a shard as max(longest file, file seconds / 4) plus its slices of ux-routes (`--shard=a/16`, one run per slice), improves the split by local search. Weights refreshed from the run's reports (`e2e-weights.json`, plus `routes_wall_seconds`).
- Modelled shard cost (s): old 597 / 494 / 445 / 379 (measured test step) to 462 / 405 / 457 / 377. Slowest step about -2.2 min. Setup time per shard is unchanged and is CRSP312's area.
- Every file and every slice 1..16 runs once (test_ci_tiers.py).

## S7 Go race split
- `go-test.sh` honours `GO_TEST_SHARD=K/N`; `go-tiers.mjs` gives each shard its share of the unit and integration packages and of each slow package's chunks (weights `seconds` in `go-tiers.json`, from the run). Windows and macOS jobs run unsharded as before.
- ci.yml: new matrix job `go-race` (4 shards, `-race` everywhere). Job `tests` becomes the web tests plus lifecycle conformance ("web tests and lifecycle conformance"); new aggregate job `tests-result` is named `tests`; `verify` needs it. Names `verify`, `tests`, `e2e (Playwright)` unchanged.
- Race step 11.7 min to about 3 to 4 min per shard (total weight 754 s over 4); `tests` check now ends about 5 min after start instead of 14.
- Go times artifact is now `ci-go-test-times-linux-<shard>`.

## Checks
test_ci_tiers.py (9 tests) pass; prettier clean on ci.yml and the JSON/mjs. Other scripts/ci tests (release, image scope) fail the same on main on this Windows PC. No e2e, no Docker.

## Risk
Modelled, not measured: first CI run on this branch is the measurement; refresh weights after it. Slice runs add about 6 s each for server start. Branch protection that names the old `tests` job still matches the new aggregate by name.
