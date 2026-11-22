# LOOK56 report: console layout (Setup, Pool, Analytics, 96rem frame)

Branch LOOK56-console-layout (from origin/main). Source: origin/LOOK54-consolidated-layout.

## Steps
- [x] Branch, tools installed (gitleaks 8.24.2, golangci-lint 2.15.0), dev-standards cloned.
- [x] Load sampling 2 s (sampler.go, hub.go, observability spec, machines spec lines).
- [x] Shared UI: themed links in every state + guard test, --ds-frame-wide 96rem, UX-79, phone header status wraps.
- [x] Routes: /setup/..., /pool/..., /analytics/...; old addresses removed, no redirects; menu of seven.
- [x] Pool screens (Overview, Live, Queue Analysis, Jobs, Machines), Analytics tabs, Doctor, Help, Overview.
- [x] Realtime (stream, 1 s timers, 5 s poll when paused).
- [x] Suggestion steps in data and content.md.
- [x] Tests, old-path guard, e2e specs, web-console spec, change notes.
- [ ] CI green, draft PR.

## Notes
- /setup keeps the first-run page when it has a first-run parameter (code, step, app, app_url, error, wizard): the controller emits those links. Without one it opens Setup > Summary.
- Doctor fix links still come from the controller (Go, not ours) with the old addresses; `apps/web/src/screens/doctor/serverLinks.ts` maps them to the new ones. Follow-up for the owner of the Go side: emit the new addresses, then delete the mapper.
- Kit: /setup/runners table narrowed (Name and State weight 1) so it fits the 96rem frame at 1300 px; the tab-gap guard skips the sticky action bar; full-page lazy screens show a heading while loading (`later(load, true)`).
- e2e-only branch gate on 852c581: 3112 passed. Publishing the gate record to the remote failed (HTTP 403 on the gate-result ref); the local record satisfied the push hook.
- Full CI: ci.yml dispatched on LOOK56-console-layout after 852c581.
