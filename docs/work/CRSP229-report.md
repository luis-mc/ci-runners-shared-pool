# CRSP229 report (round 2)

Status: BLOCKED at the push. Part 1 and Part 2 are done and committed locally on OPS77-integration (head 81a96358 plus this report); nothing of them is pushed.

## BLOCKED: pre-push hook fails on the deleted package internal/parity
`git push origin OPS77-integration` runs `.githooks/pre-push`. Its go test step takes the directories of every `*.go` file in `git diff --name-only origin/OPS77-integration...HEAD`, deleted files included, and runs `go test` on them. CRSP227 deletes `internal/parity/`, so the hook runs `go test ./internal/parity` and gets:

    # ./internal/parity
    stat /home/user/ci-runners-shared-pool/internal/parity: directory not found
    FAIL    ./internal/parity [setup failed]
    pre-push: blocked by the project gates.

Every other step of the hook passed (gofmt, go vet, golangci-lint, the other go test packages, web typecheck, the e2e gate check for head f7cd1e94). I did not edit `.githooks/pre-push`, use `--no-verify`, set `core.hooksPath` or add a stub `internal/parity` directory. One-word fix for the owner: in `.githooks/pre-push` use `git diff --name-only --diff-filter=d "$base"...HEAD ...` so deleted files are not tested. Then push; the gate result for the Part 1 head (f7cd1e94) is in `.delivery/gate-result.json`, but Part 2 changed three e2e specs (`flows.spec.ts`, `exports-stop.spec.ts`, `ux-guards.ts`), so `python3 scripts/delivery/branch_gate.py --e2e-only` has to run once more on the head before the push (about 25 min).

## Part 1: join (committed locally, e2e green on f7cd1e94: 3195 passed, 2 skipped)
- 11 branches merged in the given order; conflicts resolved (LOOK56 layout: /setup, /pool, /analytics kept, main's lazy screens kept, `later(load, {heading, compact})` with optional options).
- Renumbered: migration 00040_listener_gate to 00041; CRSP227 00038_drop_access_credentials to 00042 (it clashed with 00038_assign_leases); OPS85 github-integration R5.109 to R5.113; OPS86 machines R5.72 to R5.80.
- Design system next-version: LOOK56/58 and RELEASE-53 both defined UX-79, UX-80 and components 70 to 72; LOOK's became UX-84, UX-85 and components 75 to 77 (rules, components, tokens, CHANGES, content, specs, CSS and code comments).
- Generated files regenerated (codegen, docs_index build, design_system visuals). The openapi merge had dropped the default response of listAssignLeases; restored.
- go build, vet (and GOOS=windows), pnpm typecheck and lint, web unit tests (776), ui tests (297), touched Go tests, fast_guards: green.
- docs_check: only W1 (14 files under docs/work/ on the branch; `docs_work.py strip` is the join step of the orchestrator).

## Part 2: RUNS_ON_* to CRSP_RUNS_ON_* (commit 81a96358)
- Renamed in Go, contract (regenerated), web, e2e, specs, guides (about 300 uses). Not touched: migrations, changes/*.md history, docs/project, decisions, landscape, handover, v2 to v4, the release key, .gitleaks.toml, .gitleaksignore, gate.json, delivery scripts.
- `internal/repos/variables.go`: the CRSP25 migration (which deleted CRSP_RUNS_ON_*) is replaced by the opposite one. It deletes RUNS_ON_<OS>, RUNS_ON_LIGHT, RUNS_ON_<OS>_<SIZE>, CI_RUNS_ON and CI_RUNS_ON_WINDOWS only where the store lists them as written by crsp, and only after the new names are written. Tests: TestLegacyVariablesMigrated, TestPlanNeverDeletesCrspRunsOn.
- Per-job variables: the prefix CRSP_RUNS_ON_ is reserved (`routing.PoolVarPrefix`, `IsPoolVar`; `IsJobVar` excludes it). A job that would take it is CRSP_JOB_<NAME>_<hash> (TestVarNamesReservePoolPrefix). The routing sync never deletes a pool variable as a stale job variable.
- Fallback form `fromJSON(vars.CRSP_<JOB> || vars.CRSP_RUNS_ON_<OS> || '"ubuntu-latest"')` in the per-job PR (R5.107), the doctor patch, the doctor texts (R5.18, R5.19) and the OpenPR path. A workflow that still reads an old `RUNS_ON_<OS>` is routed too and the patch writes the new name (TestMakePatchWritesTheNewPoolNames, TestWorkflowPerJob).
- legacy_vars dropped: contract, policy, API, handlers, tests; doctor no longer reads CI_RUNS_ON as crsp's. New migration 00043_drop_legacy_vars removes it from saved policies (TestIntegrationDropLegacyVarsMigration). github-integration R5.39 marked removed with the reason.
- Specs: github-integration R5.28, R5.39, R5.102, R5.107, GI-1; doctor R5.18, R5.19, D4; scheduling, runners, web-console, cli; guides. Change notes CRSP229 for six features; design-system content.md "Variable name words" and CHANGES C22.
- Checks run on the Part 2 commit: go build, vet (and GOOS=windows), golangci-lint (0 issues), touched Go tests, pnpm typecheck and lint, web and ui unit tests, actionlint, fast_guards, docs_check (W1 only). The e2e gate for Part 2 has not run.
- Not done: watching PR #384 CI (nothing pushed).

BLOCKED: the push, see above. The last report line is not `CODE DONE`.
