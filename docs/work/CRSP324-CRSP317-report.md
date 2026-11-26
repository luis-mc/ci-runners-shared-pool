# CRSP324 (+CRSP316) and CRSP317 report

PRs: #464 (CRSP324-ci-areas, base main), #465 (CRSP317-win-go-setup, base CRSP324-ci-areas). Both ready, not merged.

## Findings
- `scripts/delivery/ci-areas.json`, `ci-scope.json` and `tools/test_ci_speedups.py` were NOT in the repo (only the scripts). I wrote `ci-areas.json` and `scripts/ci/test_ci_areas.py` (15 cases, run by the lint job). `ci_scope.py` is not used: it needs a `ci-scope.json` and a smoke set this repo lacks; `decide` already computes `release`.
- Release pattern: `.delivery/config.json` says `^release/`, ci.yml only knew `*-integration` and `-release-N`. `release/*` now counts as release.
- actionlint: not run locally (download denied). The CI lint job runs it; it passed on the branch.

## S2 lifecycle: left exactly as is
Lanes run only when `decide.release == 'true'` (release PR), already no path list. A pass-record skip needs a main-side writer that does not exist, so not done safely. Proof for DL-112: `lifecycle` job `if` reads only `release`/`skip`, test `LifecycleStaysWhole` asserts it has no area output; release PRs plan `--all`. `lifecycle_release_check.py check --release` with both targets passing prints `OK (2 targets)`; with the arm64 result removed it prints `FAIL windows arm64 msi: no lane result`.

## Timings (windows-latest "Go tests", runs 45868920288 before, 45870301336 after)
| step | before | after |
| go-cache | 127 s | 1 s |
| setup-go | 19 s | 26 s |
| Go tests | 230 s | 256 s (cold build cache on D:) |
| job | 394 s | 302 s |
Net about 92 s saved per Windows Go job. macOS unchanged (go-cache 19 s).
Dispatch run wall: 20m22s (CRSP324), 22m49s (CRSP317); slowest job is the base image build (llvm layer 7.5 min). The areas plan runs everything on both branches because they change `.github/**`; skipping is proved by unit tests, not by a CI run.
