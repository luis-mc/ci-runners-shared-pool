# CRSP295 report

Join of PRs 434, 433, 428, 432, 431, 416, 421, 429 into CRSP295-join.

- Merged with `git merge --no-ff`, in order. 434, 433, 428, 432, 431, 416: clean.
- 421: doc conflicts (design-system CHANGES/content/review-visuals, observability spec). Kept both sides. CRSP256's CHANGES entry renumbered C46 -> C48 (main already has C46, C47).
- 429: conflicts in cmd/crsp/areas_wiring.go (kept metrics wiring and the notifier + ConfigAsCode wiring) and operations spec status paragraph (merged both).
- Not done yet: CI run, #416 lint and docs-map fixes, docs_check, docs_index build (luismc-docs scripts not found in this sandbox so far).
- Branch CI run 45703008705 green on every job. docs_check --strict clean, docs_index build: no change. #416 lint/docs-map reds did not reproduce on the join.
