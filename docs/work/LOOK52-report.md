# LOOK52 report: lifecycle contract, words and DS234 guards

Branch `LOOK52-lifecycle-contract-ds`. Retry of the first run, which could not push (G4).

## Step 0: G4 fixed honestly
- `TestIdleAgentCostIsSmall` (`internal/agent/idle_cost_unix_test.go`, `time.Sleep(3 * time.Second)`) is deleted. It was a soak measurement, not a unit test. No `time.After`, ticker or other wait was put in a test file.
- New program `cmd/crsp-idle-soak`: starts the controller link and one idle agent at the production intervals (all intervals left zero, so the defaults apply), waits for the agent to be online, measures CPU (getrusage) and heap over `-window` (default 60 s), exits 1 over 1% of one core or 50 MB. A 3 s run gave 0.06% and 2 MB. It uses `secretstest` and `tlsca` from non-test packages, so no helper had to be exported. Off Unix `cpuTime` is a stub (the soak runs on Linux); `GOOS=windows go vet` passes.
- New workflow `.github/workflows/soak.yml`: `workflow_dispatch` (input `window`) and a nightly cron, never on pull requests, `timeout-minutes: 10`. Actions pinned to the SHAs the repo already uses.
- Machines spec test row R6.5 names the soak program and workflow; change note `docs/features/machines/changes/LOOK52.md`.
- `fast_guards --base-ref auto`: secret-scan, change-notes, guards PASS; G4 PASS. G1 shows 3 breaks that are already on main (`conftest.py`, `ci.yml:302`, `scripts/ci/go-test.sh:40`); none is from my files (my `go test` step has `-timeout 10s`).

## Design system (next-version only; v1 to v4 untouched)
- `docs/design-system/next-version/lifecycle.json`: valid (`lifecycle_check.py validate` prints ok). Product Crsp/crsp; targets Windows x64 and arm64 MSI; paths `%ProgramFiles%\crsp`, `%ProgramData%\crsp`, `config.toml`, `logs`, service `crsp` (from `installer/crsp.wxs` and `internal/config` `DefaultPaths`); flows, defaults, `keep_on_remove: [data]` per the brief; words = shared table plus Crsp additions (`up_to_date`, `waiting`, `skipped`, `unpaired`, `available.required`); test interface `crsp-lifecycle-test` (OPS68).
- **ID deviation, needs the owner's eye:** UX-78 and components 68 and 69 were already taken in next-version (C5 date and time, C8 Help question card), and IDs are never reused. The lifecycle set therefore uses **UX-79 to UX-83** and **components 70 to 74**: 70 update window, 71 page sections, 72 card grid, 73 lifecycle parts, 74 question window and tray menu. The form role is the field grid, component 43 (`FieldGrid`). Rules 79 and 80 say `none yet (2026-11-21)`; 81 to 83 name real guards.
- Tokens `--ds-border-width` (`var(--ds-border-thin)`) and `--ds-opacity-disabled` (`0.6`) in tokens.md.
- content.md: `Install now` and `Later` replaced by `Update to {version}` and `Not now` everywhere (action vocabulary, update dialog words, native update window words); new "Lifecycle words" table.
- CHANGES.md C10 to C17, each citing CORE ids. Gallery rebuilt (`design_system.py --apply visuals next-version`).

## Guards (DS234) and words lint
- `scripts/design/guards/design-guards/*.mjs` (A to E), `scripts/design/guards/lifecycle/*.mjs`, `scripts/design/layout-roles.json` (roles PageSections, CardGrid, FieldGrid; pages `apps/web/src/screens`), `tools/design-guards.json` (`none_yet_days` 30).
- `scripts/design/wordslint` (Go; root path fixed to `../../..`, one De Morgan lint fix) scans `internal/lifecyclewords`, a new package holding every lifecycle string as a const from the words table.
- `.github/workflows/dev-standards.yml`: ref bumped to `89711a43d7eb101770b9e17317670bf2fec98a0d`; new steps: lifecycle validate, words lint, guards B to E + layout roles + tray-modal + parts-tokens, and the rendered guard A + states-render. Guard A and states-render run only when `docs/design-system/next-version/lifecycle-states/*.html` exist (LOOK53 builds them); until then the step prints "no lifecycle state pages yet". The gallery was not used as a page: it took over 10 minutes at four widths with every field focused.
- Local results: D ok on next-version; C ok; B fails on one app line (below); E ok; tray-modal ok but **it only scans `*.rs`; the Crsp tray is Go, so it checks nothing today** (gap for the guard kit and TRAY12).

## Docs fixes
- Every `none yet` in next-version/ux-rules.md is dated `none yet (2026-11-21)`.
- **v4 flag (report only, not edited):** `docs/design-system/v4/ux-rules.md` has 50 bare `none yet` rows; guard D fails on v4. The owner's approval decides (v5 or a link-only relink). CI runs guard D on next-version only.
- Guard E: 12 feature specs (caches, ci-run-insights, doctor, github-integration, machines, notifications, observability, operations, registry, runners, scheduling, tray, web-console) got a "Design-system proof" section, `Proof: UX-n ... \`path\``. 76 cited rules map to the real guard file named in ux-rules.md. 47 ids (CORE-1/3/4/5/7 and 42 UX rules whose guard is `none yet`) are held only by `scripts/design/design-system-follow.test.ts`; that is a weak proof and each needs a real test later. `docs_check --strict`: 0 errors, 0 warnings.

## App-side breaks for LOOK53 (not fixed here)
1. `packages/ui/src/components.css:1215` `padding: calc(var(--ds-space-3) - 1px)` fails guard B; use `--ds-border-width` (blocks the dev-standards check until fixed).
2. `packages/ui/src/components.css:524, 1324, 1354` `opacity: 0.6` should be `var(--ds-opacity-disabled)`.
3. `PageSections` (component 71) does not exist in `packages/ui` yet (`Section` and `Stack` do); `CardGrid` is in `Card.tsx` and `FieldGrid` in `layout.tsx`. Add `PageSections`, and add `data-ds-section`.
4. Build the lifecycle parts in `packages/ui/src/components/lifecycle` (`ds-part`, tokens only) and the fixture pages `docs/design-system/next-version/lifecycle-states/*.html` so guard A, LC-23 and LC-24 run.
5. Show every date and time with `DateTime` (UX-78) in the lifecycle history.

## Table

| ID | Status | Evidence |
|---|---|---|
| LC-1 | pass | `lifecycle_check.py validate docs/design-system/next-version/lifecycle.json` prints ok; CI step added |
| LC-2 | pass | `go test ./scripts/design/wordslint` scans `internal/lifecyclewords`; no banned word; native and tray code must read that package (TRAY12, OPS68) |
| LC-31 | pass | same test: no error code, HTTP status or doubled punctuation in the table |
| LC-22 | partial | `tray-modal.mjs` installed, scans Rust only; Go tray unchecked |
| LC-23 | not run | needs LOOK53 parts folder; step is wired |
| LC-24, LC-30 | not run | needs lifecycle state pages; step is wired |
| DS234 A | not run | wired for LOOK53 pages; gallery too slow |
| DS234 B | fail | `components.css:1215` (LOOK53) |
| DS234 C | pass | `layout-roles.mjs` |
| DS234 D | pass | next-version; v4 flagged |
| DS234 E | pass | 12 specs fixed |
| G4 | pass | `fast_guards --base-ref auto` |

PR: https://github.com/luis-mc/ci-runners-shared-pool/pull/373. CI: ci.yml started on the branch.

CODE DONE 5d0de7ec1bdd6ab3908ea5339d5679b2099c54ba
