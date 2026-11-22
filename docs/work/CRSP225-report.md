# CRSP225: Settings > Updates handles every server state

Branch `CRSP225-update-state-words` from origin/main (068bdc1).

## Cause
`/api/v1/update/status` sends the lifecycle vocabulary (`up_to_date`, `waiting`, ...). The console typed it as the UI
vocabulary (`idle`, `waiting_for_jobs`, ...), so `lw('state.up_to_date')` had no word and `.replace` crashed.

## Change
- `apps/web/src/api/lifecycle.ts`: one adapter maps wire to UI vocabulary. `STATE_FROM_WIRE` is a `Record` over the
  generated `LifecycleStatus.state` union (typecheck fails when the contract adds a value). Policy, action, history
  result and fleet state are mapped too; an unknown value warns once and falls back to a neutral state.
  Mapping, no new words: unpaired/up_to_date -> Up to date; review -> Update ready; waiting -> Waiting;
  rolling_back/skipped -> failed-update text.
- `packages/ui/src/lifecycleWords.ts`: `lw()` returns the key and warns once for an unknown key.
- `Lifecycle.tsx`: STATE/RESULT/FLEET kind lookups fall back to `off`.
- Tests: `apps/web/src/api/lifecycle.test.tsx` (enums read from openapi.yaml, renders card/history/fleet),
  `packages/ui/src/lifecycleWords.test.ts`. Mocks in `UpdatesLifecycle.test.tsx`, e2e spec and fixtures use `up_to_date`;
  the e2e status mock is typed by the contract schema.
- Docs: web-console R5.123 + test row, change note CRSP225. `docs_check`: 0 errors.

## Not touched
`/api/v1/update` (old compact update page) uses a different vocabulary; out of scope. Update key and delivery scripts untouched.
CODE DONE d6cb469e886aa33f0451a17368183286e09f5297
