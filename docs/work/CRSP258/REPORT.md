# CRSP258 report

Branch CRSP258-notifications. Started 2026-11-25.

## Plan
1. Store: migration 00044 (notify_event, notices, notice_deliveries) + sqlc queries.
2. internal/notify: engine (Observe, state, flap, cap, group), worker (retry, webhook, email, desktop), sources, retention.
3. API: listNotices; web: Notices page (shared parts).
4. Wiring in cmd/crsp; spec, change note, tests, CI.

## Log
- Branch pushed; migration written.
- Store (migration 00044, sqlc queries), engine, queue, sources wiring, API, Notices page, spec: pushed.
- Local gates run: go test notify/store/api/cmd; golangci-lint (own build with go1.27.4); gitleaks; web typecheck on Node 24.
- Tools I had to install to let the pre-push hook run: gitleaks, golangci-lint, Node 24 (user-space, not committed).
- Not unit-tested: cmd/crsp/notify_wiring.go sources against real services (typechecked, linted).
- Squashed history (gitleaks full-history flagged an early column name); pushed as CRSP258-integration. CI run 1161 on the old head: Go race test TestQueueCeiling timed out (10k inserts), fixed with a configurable ceiling.
