# LOOK64 report: Setup and Doctor

Branch `LOOK64-setup-and-doctor`, from `origin/LOOK59-look54-contract` (`origin/main` merged: already up to date). Shots: `docs/work/LOOK64-shots/<page>-live.png` and `<page>-proto.png`, 1278x1248. Live shots use the e2e mock API (one machine, so sizes read "not set"); prototype shots are the approved pages.

Design system: highest version v4 (`next-version` for the words). Checked: CORE-1 to CORE-7. Rules used: UX-10, UX-58, UX-72, UX-6, UX-4, UX-71. Shared parts only: `Card`, `CardGrid`, `Section`, `Table` (with its `action`), `Facts`, `LevelBar`, `Status`, `Tag`, `LinkButton`, `TextLink`, `Pane`. No app CSS, no `style=`. New words: content.md "Setup and Doctor words", CHANGES C37.

## Gaps

| gaps.json id | Built | Shots |
|---|---|---|
| setup-summary | Machines, Runners, Repositories cards with counts and Open links; "Setup steps" list (Connect GitHub, First machine, First repository). The old "The pool" pane, "Runners per system" table and Add runners bar are removed. | setup-summary-live / -proto |
| setup-machines | Table columns Machine, System, Pool, Load budget, State; one "{name}: pool size" card per machine (CPU and memory bars, Change the pool size link). | setup-machines-live / -proto |
| setup-size | Size classes table (small to xl, from the runner classes API) and Backends table (aggregated over machines), then the Machines table, then the Demand chart last. | setup-size-live / -proto |
| setup-pool | Columns Repository, Pool, Runner type, Fallback label, Jobs now; GitHub App pane (App, Webhooks, Owners). | setup-pool-live / -proto |
| doctor | Each Needs-you row has an Open button (fix link, else the Setup page of its area); "Checks" pane with one card per area; the per-area detail tables stay as "Fix: {area}". | doctor-live / -proto |

## Not matched, with the reason

- Machines: the search and filter toolbar stays. Every list has the shared toolbar (R5.67, `toolbar.test.tsx`); the prototype has none. Backends, load and slots left this table; they show in Pool > Machines.
- Size and backends: the preset is a text line ("Preset: standard."), not the prototype's segmented control; changing it is not part of this task. The per-machine Machines table (job slots, Open) stays under Backends; the prototype has none.
- Pool setup: the prototype's Fallback label shows a variable name (`RUNS_ON_LINUX`). The API has no such field per repository, so the column shows the hosted fallback label while the hosted fallback is on, else a dash. Owner groups, search, filters and the Use pool bar stay. The last delivery and "where jobs go" moved off the list: the GitHub App pane says how many repositories missed a delivery, and the repository page still shows both.
- Doctor: the "Fix: {area}" tables are not in the prototype. They stay because the fix buttons (Remove leftovers, Remove registration, Start) live there.
- Page titles and leads are unchanged where tests and other screens use them (for example the Machines page title); the prototype's "Setup" title with a new lead is not applied.
- Live data differs from the prototype data (one machine, no jobs): data-only in gaps.json.
- `docs_check` reports W1 for the 47 files under `docs/work/` that this branch inherits from LOOK59; they are stripped before the join.

## Checks

- Web unit tests: all 783 pass (new: Doctor Open and Checks, Summary cards, Size page, Machines columns and cards, Pool setup columns).
- `pnpm -r typecheck` and `pnpm -r lint`: clean. `pnpm lint:root`: clean.
- DOM layout spec at 360, 393, 1300 px: added "setup summary" and "setup size and backends"; with machines, repositories and both doctor entries they pass (`e2e/tests/design-layout.spec.ts`).
- Spec: `docs/features/web-console/spec.md` (4.3, 4.3a, 4.4, 4.8) and change note `docs/features/web-console/changes/LOOK64.md`.
