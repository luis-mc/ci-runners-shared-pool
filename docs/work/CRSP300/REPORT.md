# CRSP300 report: terminal theme

Branch: `CRSP300-integration` (from origin/main). Concept: owner-approved artifact BR7ASo5YQRTvQxGdcKyKWb (read OK).

## Plan
1. Tokens: `terminal` named theme + palettes green/amber/ice (`data-pal`), generated from `scripts/design/themes.data.ts`.
2. Shared parts: Panel, Meter, Spark (new); KeyHints (exists, restyled); Table, Chip, Button, Wizard restyle only.
3. Settings > Appearance: pick `terminal`, pick palette.
4. Design system `next-version` first, CHANGES entry, content words.
5. Feature spec + change note, docs checks, screenshots, CI, one ready PR.

## Status
- [x] 1 tokens: theme `terminal` + palettes amber/ice, tokens `accent`, `warm` (scripts/design/themes.*), contrast tests
- [x] 2 parts: Panel, Meter, Spark new; Pane/Card `machine`; terminal CSS in components.css; KeyHints, Table, Button restyle only
- [x] 3 settings: Look (Colour themes / Terminal) + Palette in Settings > Appearance; theme-init.js first paint
- [x] 4 design system next-version: tokens.md, components.md 78-80, content.md, CHANGES.md
- [x] feature spec R5.172 + change note `docs/features/web-console/changes/CRSP300.md`
- [ ] 5 screenshots, CI, PR

## Notes
- `luismc-docs` plugin scripts (docs_fold.py, docs_check.py, docs_index.py) not found in this sandbox; see Blockers.

## Deviations
- Terminal is a **Look** choice beside the six colour themes, not a seventh radio: the existing rule UX-22 turns a choice of 7 or more into a drop-down, which would drop the theme previews. Stored client-side (localStorage) like the colour theme; the server `Theme` enum (system/dark/light) is unchanged. Terminal is dark in every mode.
- Concept `--dim` (#6b7a73 etc.) fails 4.5:1 as text on the selection fill. It stays the border colour; muted text is `--dim` lightened to 4.5:1 (UX-17).
- JetBrains Mono and Inter are named in the font stacks but not downloaded (offline console); a device without them falls back to its own monospace/sans.
- Meter and Spark exist as shared parts with tests; existing pages keep LevelBar/Sparkline (API unchanged, restyled). Only the machine card title uses the new `machine` prop. See PR body.

## Sandbox notes
- Pre-push needs Node >=24, gitleaks and golangci-lint built with the repo Go: installed into /tmp (node@24 via npm, gitleaks via scripts/delivery/ci_tool.py, golangci-lint v2.15.0 with GOTOOLCHAIN=go1.27.4). No hook bypass used.

## Blockers
- `luismc-docs` plugin scripts (docs_fold.py, docs_check.py, docs_index.py) are not installed in this sandbox. The change note is written in the repo's own format (`docs/features/web-console/changes/CRSP300.md`, `change_notes.py check` passes); `docs_check.py` and `docs_index.py build` were NOT run, and generated files (`docs/index.html`, `llms.txt`, `docs-map.json`) are untouched.
