# CRSP229 report (round 3)

Status: Part 1 (join) done locally and checked; pushing before Part 2.

- First commit: `.githooks/pre-push` line 174 gets `--diff-filter=d` (origin did not have it).
- 11 branches merged in the given order. LOOK56 and LOOK58 were merged after a renumbering commit on temporary local copies (UX-79,80 to UX-84,85; components 70 to 72 to 75 to 77; CHANGES C13 up to C22 up); OPS78 CHANGES became C28, LOOK58 C29 to C31, OPS85 C32, OPS86 C33, CRSP227 C34.
- Migrations: 00041_listener_gate, 00042_drop_access_credentials (00043 legacy_vars in Part 2). Spec IDs: github-integration R5.113 (OPS85), machines R5.80 (OPS86).
- listAssignLeases default response restored; code, docs index and design-system visuals regenerated.
- `later(load, options?)`: options optional; pages that used `true` now pass a heading.
- Checks green: go build, vet (and GOOS=windows), go test ./..., golangci-lint, pnpm typecheck and lint, web (776) and ui (297) tests, actionlint, fast_guards. docs_check: W1 only (docs/work).

## Part 2 (renamed to CRSP_RUNS_ON_*, legacy_vars dropped)
- Part 1 pushed as 7a31bcde after the e2e gate (3195 passed). The first gate run had 2 load flakes that pass alone; the rerun was green.
- Commits: rename RUNS_ON_* to CRSP_RUNS_ON_* and drop legacy_vars; migration 00043_drop_legacy_vars; prettier; specs, six CRSP229 change notes, design-system "Variable name words" and CHANGES C35.
- Tests: TestLegacyVariablesMigrated, TestPlanNeverDeletesCrspRunsOn, TestVarNamesReservePoolPrefix, TestMakePatchWritesTheNewPoolNames, TestWorkflowPerJob, TestIntegrationDropLegacyVarsMigration (plus four extra).
- Deviations: RUNS_ON_LIGHT keeps its old name (no replacement; listed as an old name). Old names are deleted only where the store lists them as crsp's and after the new ones are written. Canary prefix is CRSP_RUNS_ON_CANARY_ (reserved pool prefix; LegacyCanaryPrefix stops old canaries). Design-system entry is C35 (C22 was taken by LOOK56).
- Checks: go build, vet (and GOOS=windows), golangci-lint, go test ./..., pnpm typecheck and lint, web and ui tests, actionlint, fast_guards, docs_check (W1 only).
