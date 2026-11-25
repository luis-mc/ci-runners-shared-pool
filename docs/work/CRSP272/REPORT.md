# CRSP272 report (plan row V, M7, G5)

Started 2026-11-25. Branch CRSP272-cache-mirrors (renamed CRSP272-integration at the end).

## Plan
1. Spec design in docs/features/caches/spec.md (new section 7.4 and R5.65 to R5.72).
2. internal/mirror: settings, git mirror (refresh, rewrite, miss refresh), warmth, wipe plan, status rows. Windows, Linux, macOS behind interfaces.
3. lancache: Actions receiver hit/miss stats, wipe by scope.
4. API: caches routes list the mirror rows; settings route; wipe by kind.
5. Web: Caches tab controls (turn on, interval, wipe). CLI: crsp caches.
6. Docs note, docs_check, CI on branch.

## Notes
- luismc-docs scripts (docs_fold.py, docs_check.py, docs_index.py) not found in this sandbox: see Status.

## Status
- [x] 1 spec (design R5.65-R5.72, section 7.4, adjusted to what is built)
- [x] 2 internal/mirror: git mirror, warmth, wipe plan, settings, rows; tests pass on linux, vet clean for windows and darwin
- [x] 3 lancache: counters, RemovePrefix, WipeRepo/WipeAll
- [x] 4 caches.Service merges holder rows into list and wipe (existing routes); wired in cmd/crsp/mirror_wiring.go and machines_wiring.go
- [ ] 5 BLOCKED: settings API route, web switches, CLI `caches` group. They need packages/contract/openapi.yaml, generated API and TS files, internal/api/authz.go, internal/api/server.go, which open PRs #426-#429 own. Reverted my edits there. Follow-up task after those merge.
- [ ] 6 docs_fold/docs_check/docs_index not found in this sandbox (luismc-docs plugin absent): change note written by hand at docs/features/caches/changes/CRSP272.md; docs_check NOT run.
- [ ] 7 CI on branch

