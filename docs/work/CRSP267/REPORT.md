# CRSP267 report (M3: agent update, rollout, machines)

Branch CRSP267-agent-update. Updated after every step.

## Found
- R5.11 to R5.18 (update, drain, verify, rollback, agent follow), R5.126/R5.127 and machines R5.40 to R5.42 (remove, offline remove) are built and tested. The spec header and proof tables still say "planned": corrected in the spec.
- Gaps: R5.20 (daily runner pin move, doctor at 21 days) and R5.42 (`live` step, console action).

## Done
1. Trial mode `live`, one step at a time (ErrTwoSteps), ask first (`confirmed`, 409 needs_confirmation), CLI, openapi, tests.
2. Rollout pane on the repository page (apps/web Rollout.tsx and test); design note in the operations spec.

## Not run here
- docs_fold.py and docs_check.py (luismc-docs) are not on this machine: change notes are written by hand in the existing format.
- Local pre-push needed newer tools: built gitleaks 8.21.2, golangci-lint 2.15 (Go 1.27.4) and Node 24 under the scratchpad.
- macOS: built and CI-tested only, never run on a real Mac. Windows not run locally (Linux cloud session).
