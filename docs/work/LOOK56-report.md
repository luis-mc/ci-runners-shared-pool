# LOOK56 report: console layout (Setup, Pool, Analytics, 96rem frame)

Branch LOOK56-console-layout (from origin/main). Source: origin/LOOK54-consolidated-layout.

## Steps
- [x] Branch, tools installed (gitleaks 8.24.2, golangci-lint 2.15.0), dev-standards cloned.
- [x] Load sampling 2 s (sampler.go, hub.go, observability spec, machines spec lines).
- [x] Shared UI: themed links in every state + guard test, --ds-frame-wide 96rem, UX-79, phone header status wraps.
- [x] Routes: /setup/..., /pool/..., /analytics/...; old addresses removed, no redirects; menu of seven.
- [ ] Pool screens (Overview, Live, Queue Analysis, Jobs, Machines), Analytics tabs, Doctor, Help, Overview.
- [ ] Realtime (stream, 1 s timers, 5 s poll when paused).
- [ ] Suggestion steps in data and content.md.
- [ ] Tests, old-path guard, e2e specs, web-console spec, change notes.
- [ ] CI green, draft PR.

## Notes
- /setup keeps the first-run page when it has a first-run parameter (code, step, app, app_url, error, wizard): the controller emits those links. Without one it opens Setup > Summary.
