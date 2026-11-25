# CRSP259 report: config as code

Branch CRSP259-config-as-code. Updated after each step.

## Done
- Step 1: `internal/configio` engine (export, plan, apply, stale check, removal yes, audit per change, secret check) with tests for R5.28 to R5.30 and R6.10.

## Notes
- github-integration R5.7 in the spec is key rotation, already built (CRSP115). It is not config as code. Not touched; flagged for the owner.
