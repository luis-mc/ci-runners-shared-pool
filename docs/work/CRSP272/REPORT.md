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
- [ ] 1 spec
