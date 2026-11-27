# Agent rules

Standing rules for every agent working in this repository. `CLAUDE.md` imports this file.

## Documentation (DOCSPEC)

- The docs live in `docs/`, laid out by reader question: `overview/`, `features/`, `design-system/`, `architecture/`, `guides/`, `reference/`, `project/`. Read `docs/README.md` first.
- A task that changes behaviour updates the feature spec in the same branch and adds a change note: `python3 <luismc-docs>/scripts/docs_fold.py note --doc features/<feature> --task <TASK> --change minor --text "..."`. Never edit a version table by hand; the release fold writes it.
- A change that needs no doc says so in a commit message: `Docs: none, <reason>`.
- UX rules live only in `docs/design-system/`. A feature spec shows its screens and cites rules by ID.
- Agent briefs and reports live in `docs/work/` on your branch only. Never add them to a pull request for the default branch.
- Do not edit generated files (`docs/index.html`, `docs/llms.txt`, `docs/features/index.md`, `docs/architecture/decisions.md`, `docs/docs-map.json`, `docs/reference/`).
- Before you finish: `python3 <luismc-docs>/scripts/docs_check.py`.

<!-- design-system-checklist:start -->
## Secret scan

- Planted fake secrets in tests are built at run time (concatenate or generate), never written as literals.
- An agent that cannot pass the scan changes the code, never `.gitleaksignore` or `.gitleaks.toml`. A pull request that adds a line to `.gitleaksignore` fails `scripts/ci/gitleaksignore_guard.py`.

## Design system checklist (UI briefs and pull request bodies)

- [ ] Core UX Principles checked: CORE-1 to CORE-7 (name the IDs you checked).
- [ ] Design-system version followed: `v<N>` (the highest numbered folder), and the sections used: `ux-rules`, `components`, `tokens`, `content` (name the rule IDs).
- [ ] For a wizard: I rendered it in first run, running, paused, stopped, failed, done and done after reload. Later steps could not be pressed. Stop was there when the job can stop. Done showed the result. Each button that starts work was disabled and said so while it ran.
- [ ] The step, lock, Stop and busy state come from the shared wizard part. The page passes data only.
- [ ] Shared parts used, by name: `<part>`, `<part>`.
- [ ] No hand-made button, bar, table, tab row, empty box or scroll box. No stylesheet, no `style=` with layout or colour, no class the shared UI package does not define.
- [ ] The enforcement kit is green for what I touched: no-app-CSS scan, DOM layout at 360, 393 and 1300 px, wizard behaviour. No entry was added to a known list; the entries I fixed are removed.
- [ ] Any new word the user sees is in `content`.
- [ ] A change to a rule, token or component went into `next-version` first, with a `CHANGES` entry.

What stays a human check (no test can say it): the page looks like one frame with its siblings; the words read as a person would say them; the next action is the obvious one.
<!-- design-system-checklist:end -->
