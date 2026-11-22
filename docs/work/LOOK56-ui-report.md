# LOOK56 ui part

- Links: base :where(a) rules + per-state rules for .ds-link, .ds-topmenu__link, .ds-boxbadge (UX-80, C13).
- Frame: --ds-frame-wide 96rem, --ds-bp-wide 1400px, UX-79 (C14). No wide-layout CSS added yet (pages in apps/web use it).
- Header: phone status column min-width 0, wraps (C15).
- Guard: packages/ui/src/components/link-frame.test.tsx.
- Tests: ui 233 pass, scripts/design 129 pass, typecheck, eslint, prettier, docs_check clean.

CODE DONE
