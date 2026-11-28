# CRSP395 report (rides RELEASE-76, CRSP393) and CRSP396

Status: implemented, checks running (see the end).

## CRSP396 (commit 94cfa40d)
- One root token `--ds-type-scale: 0.85` in packages/ui/src/tokens.css; every `--ds-size-*` and `--ds-icon-size` is its old value times it.
- Floors held: `--ds-size-1` (badge) 12 px -> 11 px (10.2 would be below the floor); `--ds-size-2` 14 -> 11.9 px (above the 11 px floor, not held); `--ds-size-3` body 16 -> 13.6 px (floor 12 not reached). Sizes 4, 5, 6: 15.3, 17, 23.8 px.
- Not scaled: touch targets, control heights, rows, spacing (UX-16, 44 px).
- tokens.md Type scale, CHANGES CRSP396, gallery rebuilt. gen-themes.ts leaves the file unchanged (the scale lives outside the generated themes block).

## CRSP395
- Rule UX-92 (ux-rules.md), component 92 Meter list (components.md), CHANGES entry; no new word.
- Part: `MeterList` + `LevelBar` split value (`splitLevelText`) in packages/ui Progress.tsx; `BarList` on the same grid; `Meter` uses the shared Segments. CSS: components.css `.ds-meters`, `.ds-level`, `.ds-bars` (container query at 26 rem for the stacked form).
- Screens moved: Overview machine cards, Pool > Machines machine page (Disks, Memory and swap, Job stats), Setup machines pool size card, Images and caches (cache size), Registry budgets, Cache hub budget, Analytics Insights bar list.
- Judge: `meter-align` rule in scripts/design/dom-layout.ts (labels left edge, bars left and width, values right edge, numbers and "of" words, every LevelBar inside a MeterList). Specs: e2e/tests/web-console/ux-meter-lists.spec.ts. Unit: packages/ui/src/components/meterList.test.tsx. No known-list entry added.

## e2e spec location
The new spec is at docs/work/CRSP395/ux-meter-lists.spec.ts.txt (copy it to e2e/tests/web-console/ux-meter-lists.spec.ts at the join; agent-common rule: the pre-push gate wants the whole suite for a spec under e2e/, and packages/ui is in the full_on list). The join moves it to e2e/tests/web-console/ux-meter-lists.spec.ts; the docs already cite that final path. It ran locally (copied into e2e/): 6 tests passed.

## CRSP399 (text size)
- Settings > Appearance: ChoiceField "Text size" 80/90/100/110/120/130 % (added between Graphs and Mode; additive edit only), saved at once in `crsp.text-size`, applied by `data-text-size` on the root which sets `--ds-type-scale` (0.68, 0.765, base 0.85, 0.935, 1.02, 1.105) in tokens.css; theme-init.js applies it before first paint. A slider does not exist in the design system, so the existing segmented ChoiceField is used.
- Tests: packages/ui/src/textSize.test.ts, Settings.test.tsx; e2e spec (docs/work/CRSP395/ux-text-size.spec.ts.txt) ran locally: 20 passed (5 screens x 80/130 % x phosphor/terminal, DOM judges at 360/393/1300).
- Words in content.md Theme words; CHANGES; change note web-console CRSP399.
