# CRSP261 report: target-neutral cloud overflow (plan row Q, gap G1)

Branch: CRSP261-cloud-overflow (to be renamed CRSP261-integration).

## Design (decided without owner input; scheduled run)
- New global setting `cloud_overflow` in the settings table: `enabled` (default off), `label` (any one cloud runner label), `cap_minutes` (monthly spend cap in minutes across all repositories, 0 = none).
- Off = CRSP74 behaviour unchanged (per-repository opt-in, GitHub-hosted default labels). On = the label replaces the default hosted label of every opted-in repository that has no label of its own; the cap adds a pool-wide monthly limit on top of the per-repository cap.
- Per-repository opt-in (`hosted_fallback`) stays: the switch never turns a repository's overflow on by itself (money rule D52).
- No runs-on licence, no AWS: the label is a plain string.
- Doctor reads `repos.Service.OverflowState` (interface only; doctor package untouched). Audit: `cloud_overflow.on|off|changed`.
- Console: Settings > Repositories section "Cloud overflow", shared parts only.
- Tested on Linux (local) and Windows/macOS in CI; macOS compiled and unit-tested in CI, not run on a real Mac.

## Steps
- [x] branch pushed
- [ ] settings service + audit
- [ ] repos service uses label + cap, OverflowState
- [ ] API + openapi + codegen + CLI
- [ ] console screen
- [ ] spec + change note + docs_check
- [ ] CI run, PR
