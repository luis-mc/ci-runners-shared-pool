# LOOK53 report: lifecycle UI parts

Status: code done, CI run requested. Branch LOOK53-lifecycle-ui.

## What changed
- Words table: `packages/ui/src/lifecycleWords.ts` (template words.json plus the keys the parts need). Entry `@crsp/ui/lifecycle`, so the parts load on demand and stay out of the first JavaScript (budget 307200 B gzip held; the parts added about 4.3 KB, so a plain barrel export broke the budget).
- Parts in `packages/ui/src/components/Lifecycle.tsx`: UpdateBanner, UpdatesCard, UpdateHistory, RemoteUpdate, RemoveDialog, FleetRows. Built on crsp's Pane, Button, Dialog, Status, Progress, Message, ChoiceField, TextField, Table: no new CSS, no `style=`, no class (LC-23). Not the template CSS: crsp's own tokens and look (v4).
- Screens: Settings > Updates (`UpdatesLifecycle.tsx`, no Save bar, each change saves at once), the app-wide banner (`shell/UpdateBannerHost.tsx` in the one banner slot), the fleet and remote update on Settings > Updates, Remove on Settings > Updates and on the machine page (`MachineRemove.tsx`).
- API: `api/lifecycle.ts` uses the brief's contract (`/api/update/*`, `/api/remove`), mocked in tests. Remove always sends `delete_data`.
- DS234 fixes: B, one break in `components.css` (a `2px` border and a `1px` calc) now on tokens. A: the new e2e layout spec measures the page, the banner and the dialog at 360, 393, 1300, 1800 px with no known-list entry. C: no break (checked with a roles config in scratch: Stack, CardGrid, FieldGrid).

## Needs another task or owner
- The contract has no data size or path for the "Delete my data too ({size} in {path})" line. The UI shows them when `/api/update/status` carries `data_path` and `data_size_bytes`, else the short label. OPS68 or OPS69 should add them.
- The brief calls the button Check; the words table and crsp's retired-word list say "Look for updates", so the button says that.
- `scripts/design/dom-layout.ts` measures 360, 393, 1300 only. LOOK52 owns it; my spec loops the four widths itself.
- Machine page: the old "Remove this machine" (take it out of the pool, keep the agent) is replaced by Remove crsp (POST /api/remove with machine_id). The old DELETE alias stays on the server.
- The compact update page and Review step (`/update`, `/settings/updates/review`) stay for the tray window until TRAY12 replaces them.
- Local push needed golangci-lint built with go1.27.4 (setup step). The test-guardrails guard prints warnings for files I do not own (`conftest.py`, `ci.yml`, `scripts/ci/go-test.sh`, `internal/agent/idle_cost_unix_test.go`); mode warn.
- docs_check W1 on this report is expected: strip it before the join.

## Checks run
- Full e2e gate green on the head (3399 passed). ui: typecheck, lint, vitest (254). web: typecheck, lint, vitest (682), bundle budget. Prettier. `scripts/design` vitest (128). e2e: updates-lifecycle (6) and design-layout (65) green. docs_check: only W1.


| ID | status | evidence |
|---|---|---|
| LC-22 | pass | every word from `lifecycleWords.ts`; words test (no banned word, no doubled punctuation, no codes); retired-word guard green |
| LC-23 | pass | parts use only shared Pane, Button, Dialog, Status, Progress; no class, no `style=`; parts-tokens guard ok; spacing guard B ok |
| LC-24 | pass | vitest renders every state of each part; e2e layout at 360, 393, 1300, 1800 px |
| LC-30 | pass | UI side only: Remove disabled with its reason until a data choice; no modal beyond RemoveDialog; no fixed-size window |
| Guard A | pass | `updates-lifecycle.spec.ts` layout (page, banner, dialog), design-layout spec, ux-routes |
| Guard B | pass | one break in `components.css` fixed; apps/web and packages/ui clean |
| Guard C | pass | clean with a scratch roles config (Stack, CardGrid, FieldGrid); the repo has no `layout-roles.json` yet |

CODE DONE 82fc3981084a10ef27c756a7bb5674ee82cfe609
