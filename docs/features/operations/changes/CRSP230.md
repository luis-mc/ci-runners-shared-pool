---
change: minor
task: CRSP230
---
LC-11 accepts exactly the changes a migration declares in internal/store/declared_changes.go (migration 42 drops access_credentials, 43 rewrites repos_state.body, removing LegacyVars and legacy_vars from each policy under $.policies), only across that migration; the lane prints each accepted change; every other loss still fails (R5.125). The release notes say the parity tokens are deleted on upgrade (R5.85).
