# LOOK58b report: shared Timeline part

Branch `LOOK58b-timeline-ui` (from `LOOK58-timeline-machines`). Scope: `packages/ui` and the next-version design-system docs only.

## Done

- `packages/ui/src/components/Timeline.tsx`: `Timeline`, exported constants `TIMELINE_PX_PER_MIN` (4, so 240 px an hour), `TIMELINE_LABEL_PX` (144), `TIMELINE_FOLLOW_AT` (0.75), `TIMELINE_STEP_PX` (240), `TIMELINE_STATES` (glyph and word of the nine states), `TIMELINE_WORDS`. Exported from `index.ts`. `Timeline.types.ts` unchanged; no optional props added.
- CSS: `.ds-timeline*` in `packages/ui/src/components.css`, tokens only.
- Tests: `Timeline.test.tsx`, 24 cases in 3 describe blocks (<= 20 each). `pnpm --filter @crsp/ui test` 260 passed, typecheck and eslint clean, prettier clean.
- Design guards: `npx vitest run scripts/design` 129 passed (no-app-css, raw controls, contrast, follow, freeze).
- Layout kit: no specimen registry exists (nothing in e2e or apps/web names Waterfall), so a throwaway Vite page (deleted) rendered the part inside Page, PageHeader and Pane. In real Chrome, `measureDomLayout` with the console's options found 0 problems at 360, 393 and 1300 px; the page has no sideways scroll at 360, 393, 1300 and 1920 px; now sat at 75 % of the box at each width; arrows, Home, End and Back to now behaved.
- Docs: component 72 in `components.md`, Timeline words in `content.md`, C19 in `CHANGES.md`, a README "apps do not follow yet" line. No new UX rule or token. `docs_check`: only W1 (docs/work files on the branch, expected; from LOOK56).

## Notes

- Time labels use the existing `formatTime` and `formatShortDateTime` (24 h, local zone); there is no locale-aware helper in `format.ts`.
- Component 61 (runner timeline, "to be built") is superseded in practice by 72; its entry was left for the owner to retire.
- A tick every hour; bars wider than their text clip with an ellipsis; the state word and detail are visually hidden text in each bar.

CODE DONE (sha below)
