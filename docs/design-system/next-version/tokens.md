# Tokens

> **Starter.** Every item in this file is a starter item. Keep, change or delete each one before v1 is approved.

Use tokens and nothing else: no raw colour, size or font in a screen. The values are given here and nowhere else.

**Prefix:** `--ds-`

## How themes work

- The dark values are the default. The console looks like a terminal. Six named colour themes change the colour values (see Colour themes).
- The light values apply under `@media (prefers-color-scheme: light)` and under `[data-theme=light]`. The attribute wins over the system setting.
- The tables list the light value first and the dark value second.
- A token has the same name in every theme. Only its value changes.
- Text and its background meet WCAG 2.2 AA contrast in every theme (UX-17): at least 4.5:1 for text, 3:1 for large text and for borders of controls. WCAG 2.2 AA stays the rule. A project may choose 7:1 for body text in the dark theme.

```css
:root {
  --ds-color-page: #0a0e0d;
}
@media (prefers-color-scheme: light) {
  :root:not([data-theme='dark']) {
    --ds-color-page: #f3f6f4;
  }
}
[data-theme='light'] {
  --ds-color-page: #f3f6f4;
}
```

## Colour

| Token                      | Light             | Dark              | Use                                                                                       |
| -------------------------- | ----------------- | ----------------- | ----------------------------------------------------------------------------------------- |
| `--ds-color-page`          | `#f3f6f4`         | `#0a0e0d`         | page background                                                                           |
| `--ds-color-surface`       | `#ffffff`         | `#111816`         | cards, dialogs, inputs                                                                    |
| `--ds-color-text`          | `#0d1a14`         | `#d7e2dc`         | body text                                                                                 |
| `--ds-color-text-muted`    | `#45574e`         | `#93a79e`         | secondary text, hints                                                                     |
| `--ds-color-text-inactive` | `#5c635c`         | `#8a948a`         | a row shown as inactive (UX-70)                                                           |
| `--ds-color-border`        | `#6b7b73`         | `#62766d`         | borders of controls                                                                       |
| `--ds-color-divider`       | `#d2dcd7`         | `#22302b`         | lines between sections                                                                    |
| `--ds-color-primary`       | `#006a77`         | `#5ee6f0`         | the primary action, links in text                                                         |
| `--ds-color-on-primary`    | `#ffffff`         | `#04181b`         | text on the primary action                                                                |
| `--ds-color-done`          | `#0a6b33`         | `#46e08f`         | the Done tone                                                                             |
| `--ds-color-done-bg`       | `#e3f5ea`         | `#0e2a1b`         | background of Done                                                                        |
| `--ds-color-note`          | `#006779`         | `#5ed3e8`         | the Note tone                                                                             |
| `--ds-color-note-bg`       | `#def3f7`         | `#0a2a30`         | background of Note                                                                        |
| `--ds-color-needs-you`     | `#8a5200`         | `#ffb84d`         | the Needs you tone                                                                        |
| `--ds-color-needs-you-bg`  | `#fff1d6`         | `#2e2208`         | background of Needs you                                                                   |
| `--ds-color-problem`       | `#b3261e`         | `#ff7a7a`         | the Problem tone                                                                          |
| `--ds-color-problem-bg`    | `#fde8e6`         | `#351416`         | background of Problem                                                                     |
| `--ds-color-focus`         | `#006a77`         | `#5ee6f0`         | the focus ring                                                                            |
| `--ds-color-selection`     | see Colour themes | see Colour themes | the row under the cursor (a selected row is reverse video: primary fill, on-primary text) |

A tone is never shown by colour alone (UX-26). It always has a word and a glyph too (UX-58).

Terminal mapping: Done is green (healthy), Needs you is amber (waiting), Problem is red (failed), Note is cyan (info).

### Text on a fill

Text on a fill comes in pairs. Every fill role has its own `on-` text token. A fill and its text are always set together, and never mixed with another pair. The kit's contrast guard reads these pairs.

- A solid fill is `--ds-color-X` with its text `--ds-color-on-X`.
- A tint fill is `--ds-color-X-tint` with its text `--ds-color-on-X-tint`.
- The `-bg` tokens above have the value of the tint. Keep them until every screen uses the tint pair.

| Token                          | Light     | Dark      | Use                            |
| ------------------------------ | --------- | --------- | ------------------------------ |
| `--ds-color-on-done`           | `#ffffff` | `#06110c` | text on a solid Done fill      |
| `--ds-color-done-tint`         | `#e3f5ea` | `#0e2a1b` | a quiet Done fill              |
| `--ds-color-on-done-tint`      | `#0a6b33` | `#46e08f` | text on the Done tint          |
| `--ds-color-on-note`           | `#ffffff` | `#06110c` | text on a solid Note fill      |
| `--ds-color-note-tint`         | `#def3f7` | `#0a2a30` | a quiet Note fill              |
| `--ds-color-on-note-tint`      | `#006779` | `#5ed3e8` | text on the Note tint          |
| `--ds-color-on-needs-you`      | `#ffffff` | `#06110c` | text on a solid Needs you fill |
| `--ds-color-needs-you-tint`    | `#fff1d6` | `#2e2208` | a quiet Needs you fill         |
| `--ds-color-on-needs-you-tint` | `#8a5200` | `#ffb84d` | text on the Needs you tint     |
| `--ds-color-on-problem`        | `#ffffff` | `#06110c` | text on a solid Problem fill   |
| `--ds-color-problem-tint`      | `#fde8e6` | `#351416` | a quiet Problem fill           |
| `--ds-color-on-problem-tint`   | `#b3261e` | `#ff7a7a` | text on the Problem tint       |

## Colour themes

Owner decisions LOOK D1 and D2 (2026-11-15): the console looks like TUI panes, and all six palettes are themes the user picks in Settings (UX-60). A theme is a set of token values and nothing else (UX-55).

- **Six themes:** `phosphor`, `tokyo-night`, `catppuccin`, `gruvbox`, `solarized`, `nord`; the seventh, `terminal`, is chosen as the Look (see Terminal below). Each has a dark and a light value set.
- **Default for a new install:** `phosphor`, in dark mode (owner decision LOOK D11). A page with no theme set is Phosphor; the tables of the first colour section above describe the starter values and are replaced by the Phosphor values below. The light themes keep exactly the look page values (D12).
- **A sample of a theme:** an element that carries both `data-theme-name` and `data-theme` (the theme preview) keeps its own mode inside a page in any theme or mode.
- **How a theme is applied:** the attribute `data-theme-name=<id>` on the page root picks the theme. The mode stays as written under "How themes work": `[data-theme=light|dark]` or the system setting. The two attributes are independent. The attribute may also sit on one element (the swatch row in Settings); the tokens then apply to that element and what is inside it.
- **New token:** `--ds-color-selection`, the fill of the row under the cursor. A selected row is drawn in reverse video: `--ds-color-primary` fill with `--ds-color-on-primary` text.
- **Radius:** `--ds-radius-1`, `--ds-radius-2` and `--ds-radius-3` come from the theme: `0` for Phosphor, Tokyo Night, Gruvbox and Solarized, `8px` for Catppuccin, `6px` for Nord. `--ds-radius-round` does not change.
- **Focus effects:** `--ds-glow-focus` and `--ds-scanline-focus` follow each theme's primary colour (dark: ring plus glow, scanline at 6%; light: ring only, no scanline).
- **Derived values** (the same rule for every theme, so nothing is hand-picked): `--ds-color-divider` is the border mixed 30% into the surface. Each tone's tint (`--ds-color-X-tint`, and `-bg` with the same value) is the tone mixed 14% (dark) or 12% (light) into the surface, thinner when the tone would not read 4.5:1 on it. `--ds-color-on-X` (text on a solid tone) is on-primary, else white, else black, the first that reads 4.5:1. `--ds-color-on-X-tint` is the tone itself. `--ds-color-focus` is the primary.
- **Contrast:** in every theme and mode, text pairs are at least 4.5:1 (text, muted text, inactive text and every tone on the page, the surface and the selection fill; on-primary on primary; every `on-` pair) and borders at least 3:1 on the page and the surface (UX-17). `scripts/design/themes.test.ts` proves it, and proves that every theme defines every token. The values live in `scripts/design/themes.data.ts`; `scripts/design/gen-themes.ts` writes `packages/ui/src/tokens.css` from them.

The tables list the values the CSS holds. The keys are the tokens without the prefix `--ds-color-`.

### Phosphor (`phosphor`)

Radius: `--ds-radius-1` 0, `--ds-radius-2` 0, `--ds-radius-3` 0.

| Token                      | Dark      | Light     |
| -------------------------- | --------- | --------- |
| `--ds-color-page`          | `#040a05` | `#eef5ea` |
| `--ds-color-surface`       | `#08130a` | `#f9fcf7` |
| `--ds-color-text`          | `#b9f7b4` | `#0e2a12` |
| `--ds-color-text-muted`    | `#78b874` | `#3f5e41` |
| `--ds-color-text-inactive` | `#8a948a` | `#5c635c` |
| `--ds-color-border`        | `#4a8a47` | `#5f7d61` |
| `--ds-color-primary`       | `#4dff88` | `#0f6b2c` |
| `--ds-color-on-primary`    | `#021207` | `#ffffff` |
| `--ds-color-done`          | `#6dff8f` | `#0f6b2c` |
| `--ds-color-note`          | `#7fe7ff` | `#005f73` |
| `--ds-color-needs-you`     | `#ffc94d` | `#8a5200` |
| `--ds-color-problem`       | `#ff7466` | `#b3261e` |
| `--ds-color-selection`     | `#123d1a` | `#d4ead2` |

### Tokyo Night (`tokyo-night`)

Radius: `--ds-radius-1` 0, `--ds-radius-2` 0, `--ds-radius-3` 0.

| Token                      | Dark      | Light     |
| -------------------------- | --------- | --------- |
| `--ds-color-page`          | `#16161e` | `#e1e2e7` |
| `--ds-color-surface`       | `#1a1b26` | `#f4f5f8` |
| `--ds-color-text`          | `#c0caf5` | `#343b58` |
| `--ds-color-text-muted`    | `#9aa5ce` | `#4f5574` |
| `--ds-color-text-inactive` | `#8a948a` | `#5c635c` |
| `--ds-color-border`        | `#6b74a3` | `#6b7194` |
| `--ds-color-primary`       | `#7aa2f7` | `#2350b5` |
| `--ds-color-on-primary`    | `#11121a` | `#ffffff` |
| `--ds-color-done`          | `#9ece6a` | `#3a6a17` |
| `--ds-color-note`          | `#7dcfff` | `#0f6587` |
| `--ds-color-needs-you`     | `#e0af68` | `#865500` |
| `--ds-color-problem`       | `#ff8aa0` | `#b4214a` |
| `--ds-color-selection`     | `#292e48` | `#d5d9ea` |

### Catppuccin (`catppuccin`)

Radius: `--ds-radius-1` 8px, `--ds-radius-2` 8px, `--ds-radius-3` 8px.

| Token                      | Dark      | Light     |
| -------------------------- | --------- | --------- |
| `--ds-color-page`          | `#181825` | `#e6e9ef` |
| `--ds-color-surface`       | `#1e1e2e` | `#eff1f5` |
| `--ds-color-text`          | `#cdd6f4` | `#4c4f69` |
| `--ds-color-text-muted`    | `#a6adc8` | `#5c5f77` |
| `--ds-color-text-inactive` | `#8a948a` | `#5c635c` |
| `--ds-color-border`        | `#7f849c` | `#7c7f93` |
| `--ds-color-primary`       | `#cba6f7` | `#7a2ee0` |
| `--ds-color-on-primary`    | `#1e1e2e` | `#ffffff` |
| `--ds-color-done`          | `#a6e3a1` | `#2f6d1a` |
| `--ds-color-note`          | `#89dceb` | `#0b6680` |
| `--ds-color-needs-you`     | `#f9e2af` | `#835300` |
| `--ds-color-problem`       | `#f38ba8` | `#b5163a` |
| `--ds-color-selection`     | `#313244` | `#dce0e8` |

### Gruvbox (`gruvbox`)

Radius: `--ds-radius-1` 0, `--ds-radius-2` 0, `--ds-radius-3` 0.

| Token                      | Dark      | Light     |
| -------------------------- | --------- | --------- |
| `--ds-color-page`          | `#1d2021` | `#f2e5bc` |
| `--ds-color-surface`       | `#282828` | `#fbf1c7` |
| `--ds-color-text`          | `#ebdbb2` | `#3c3836` |
| `--ds-color-text-muted`    | `#bdae93` | `#5a514a` |
| `--ds-color-text-inactive` | `#8a948a` | `#5c635c` |
| `--ds-color-border`        | `#7c6f64` | `#7c6f64` |
| `--ds-color-primary`       | `#fabd2f` | `#076678` |
| `--ds-color-on-primary`    | `#1d2021` | `#ffffff` |
| `--ds-color-done`          | `#b8bb26` | `#5d5a0a` |
| `--ds-color-note`          | `#8ec0a8` | `#076678` |
| `--ds-color-needs-you`     | `#fe8019` | `#8f3e02` |
| `--ds-color-problem`       | `#ff6b5a` | `#9d0006` |
| `--ds-color-selection`     | `#32302f` | `#ebdbb2` |

### Solarized (`solarized`)

Radius: `--ds-radius-1` 0, `--ds-radius-2` 0, `--ds-radius-3` 0.

| Token                      | Dark      | Light     |
| -------------------------- | --------- | --------- |
| `--ds-color-page`          | `#002b36` | `#fdf6e3` |
| `--ds-color-surface`       | `#073642` | `#fffbf0` |
| `--ds-color-text`          | `#d2dbd9` | `#073642` |
| `--ds-color-text-muted`    | `#a5b4b5` | `#4c6168` |
| `--ds-color-text-inactive` | `#9aa29a` | `#5c635c` |
| `--ds-color-border`        | `#7a9497` | `#6d7f83` |
| `--ds-color-primary`       | `#6cbcf5` | `#1a5f96` |
| `--ds-color-on-primary`    | `#002b36` | `#ffffff` |
| `--ds-color-done`          | `#a5c23a` | `#4f6000` |
| `--ds-color-note`          | `#4fcfc4` | `#1d6e68` |
| `--ds-color-needs-you`     | `#e0b13a` | `#7d5300` |
| `--ds-color-problem`       | `#ff9a95` | `#b0201d` |
| `--ds-color-selection`     | `#0a3f4c` | `#eee8d5` |

### Nord (`nord`)

Radius: `--ds-radius-1` 6px, `--ds-radius-2` 6px, `--ds-radius-3` 6px.

| Token                      | Dark      | Light     |
| -------------------------- | --------- | --------- |
| `--ds-color-page`          | `#20242c` | `#e5e9f0` |
| `--ds-color-surface`       | `#2e3440` | `#f6f8fb` |
| `--ds-color-text`          | `#eceff4` | `#2e3440` |
| `--ds-color-text-muted`    | `#b4bccb` | `#4c566a` |
| `--ds-color-text-inactive` | `#9aa29a` | `#5c635c` |
| `--ds-color-border`        | `#76829b` | `#6b768c` |
| `--ds-color-primary`       | `#88c0d0` | `#2f6585` |
| `--ds-color-on-primary`    | `#1b2029` | `#ffffff` |
| `--ds-color-done`          | `#a3be8c` | `#466a2c` |
| `--ds-color-note`          | `#93b4d6` | `#3a5d88` |
| `--ds-color-needs-you`     | `#ebcb8b` | `#805600` |
| `--ds-color-problem`       | `#ef9aa1` | `#a3343f` |
| `--ds-color-selection`     | `#363c4a` | `#dde3ec` |

## Type families

System monospace stacks only. Nothing is bundled and nothing is loaded from the network. All text is monospace, like a terminal.

| Token            | Value                                                                                     | Use        |
| ---------------- | ----------------------------------------------------------------------------------------- | ---------- |
| `--ds-font-body` | `ui-monospace, "Cascadia Mono", "SF Mono", Menlo, Consolas, "Liberation Mono", monospace` | all text   |
| `--ds-font-mono` | `ui-monospace, "Cascadia Mono", "SF Mono", Menlo, Consolas, "Liberation Mono", monospace` | code, logs |

## Type scale

| Token                    | Value      | Use                  |
| ------------------------ | ---------- | -------------------- |
| `--ds-size-1`            | `0.75rem`  | badge                |
| `--ds-size-2`            | `0.875rem` | hint, secondary text |
| `--ds-size-3`            | `1rem`     | body, label, button  |
| `--ds-size-4`            | `1.125rem` | subtitle             |
| `--ds-size-5`            | `1.25rem`  | section title        |
| `--ds-size-6`            | `1.75rem`  | page title           |
| `--ds-line-height`       | `1.5`      | body text            |
| `--ds-line-height-tight` | `1.25`     | titles               |

## Type roles

Each role names its weight. The ceiling is the heaviest weight the role may use. A role never goes above its ceiling, and no role uses a weight outside the weight tokens.

| Token                  | Value |
| ---------------------- | ----- |
| `--ds-weight-regular`  | `400` |
| `--ds-weight-semibold` | `600` |
| `--ds-weight-bold`     | `700` |

| Role          | Size          | Weight | Ceiling | Family           |
| ------------- | ------------- | ------ | ------- | ---------------- |
| Page title    | `--ds-size-6` | 700    | 700     | `--ds-font-body` |
| Section title | `--ds-size-5` | 600    | 600     | `--ds-font-body` |
| Subtitle      | `--ds-size-4` | 400    | 400     | `--ds-font-body` |
| Body          | `--ds-size-3` | 400    | 600     | `--ds-font-body` |
| Label         | `--ds-size-3` | 600    | 600     | `--ds-font-body` |
| Button        | `--ds-size-3` | 600    | 600     | `--ds-font-body` |
| Secondary     | `--ds-size-2` | 400    | 600     | `--ds-font-body` |
| Hint          | `--ds-size-2` | 400    | 400     | `--ds-font-body` |
| Badge         | `--ds-size-1` | 600    | 600     | `--ds-font-body` |
| Code          | `--ds-size-2` | 400    | 400     | `--ds-font-mono` |

## Spacing

| Token             | Value  | Use                                                                          |
| ----------------- | ------ | ---------------------------------------------------------------------------- |
| `--ds-space-0`    | `0`    | none                                                                         |
| `--ds-space-1`    | `4px`  | tight gaps                                                                   |
| `--ds-space-2`    | `8px`  | gap inside a control                                                         |
| `--ds-space-3`    | `12px` | gap between related items                                                    |
| `--ds-space-4`    | `16px` | default gap, page gutter on a phone                                          |
| `--ds-space-5`    | `24px` | gap between cards                                                            |
| `--ds-space-6`    | `32px` | gap between sections (`Stack gap="section"`, the sections of a Settings tab) |
| `--ds-space-7`    | `48px` | page padding on wide screens                                                 |
| `--ds-target-min` | `44px` | smallest touch target (UX-16)                                                |

## Radius

| Token               | Value   | Use             |
| ------------------- | ------- | --------------- |
| `--ds-radius-1`     | `2px`   | inputs, badges  |
| `--ds-radius-2`     | `2px`   | buttons, cards  |
| `--ds-radius-3`     | `4px`   | dialogs, sheets |
| `--ds-radius-round` | `999px` | chips, switches |

## Shadows

| Token           | Light                            | Dark                           | Use                             |
| --------------- | -------------------------------- | ------------------------------ | ------------------------------- |
| `--ds-shadow-1` | `0 0 0 rgba(0, 0, 0, 0)`         | `0 0 0 rgba(0, 0, 0, 0)`       | cards: a thin border, no shadow |
| `--ds-shadow-2` | `0 2px 6px rgba(0, 0, 0, 0.12)`  | `0 0 0 1px rgba(0, 0, 0, 0.6)` | sticky bars, menus              |
| `--ds-shadow-3` | `0 8px 24px rgba(0, 0, 0, 0.18)` | `0 0 0 1px rgba(0, 0, 0, 0.8)` | dialogs, sheets                 |

## Effects

The terminal glow and scanline appear only on the focused control (UX-59). Nothing else glows. Under `prefers-reduced-motion: reduce` the scanline is off and the glow does not animate.

| Token                 | Light               | Dark                                                                                   | Use                                                                                                                                             |
| --------------------- | ------------------- | -------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------- |
| `--ds-glow-focus`     | `0 0 0 1px #006a77` | `0 0 0 1px #5ee6f0, 0 0 8px rgba(94, 230, 240, 0.55)`                                  | added to the focus ring                                                                                                                         |
| `--ds-scanline-focus` | `none`              | `repeating-linear-gradient(0deg, rgba(94, 230, 240, 0.06) 0 1px, transparent 1px 3px)` | background layer of the focused control                                                                                                         |
| `--ds-cursor-blink`   | `1000ms`            | `1000ms`                                                                               | the block caret of every single-line text box (component 21, `CaretInput`), the command palette's included; `0ms` (steady) under reduced motion |

## Breakpoints

A screen works from 360 px up (UX-16). Write layouts mobile first.

| Token          | Value    | Use                             |
| -------------- | -------- | ------------------------------- |
| `--ds-bp-min`  | `360px`  | the smallest supported width    |
| `--ds-bp-sm`   | `600px`  | phone to tablet                 |
| `--ds-bp-md`   | `900px`  | tablet to desktop               |
| `--ds-bp-lg`   | `1200px` | wide screens                    |
| `--ds-bp-wide` | `1400px` | wide layouts start here (UX-84) |
| `--ds-bp-xl`   | `1600px` | very wide screens               |

CSS cannot read a custom property inside a media query. Write these values in the query itself.

## Layout

| Token                     | Value   | Use                                        |
| ------------------------- | ------- | ------------------------------------------ |
| `--ds-card-min`           | `280px` | smallest card width in a card grid (UX-10) |
| `--ds-width-field-short`  | `8rem`  | short box (UX-23)                          |
| `--ds-width-field-medium` | `20rem` | medium box (UX-23)                         |
| `--ds-width-field-full`   | `100%`  | full box (UX-23)                           |

### Per width

These tokens take a different value at each width. The gutter is the space at the side of the page. The padding is the space above and below the content. Write the changes in a media query (see Breakpoints).

| Token               | Below `--ds-bp-sm` | `--ds-bp-sm` and up | `--ds-bp-lg` and up | Use                                                                                              |
| ------------------- | ------------------ | ------------------- | ------------------- | ------------------------------------------------------------------------------------------------ |
| `--ds-page-gutter`  | `16px`             | `24px`              | `24px`              | space at the side of the page: the one gutter of the header, menu, page and key-hint bar (UX-69) |
| `--ds-pane-inner`   | `12px`             | `16px`              | `16px`              | the one inner edge of a pane: title, fields, help text, notes and table (UX-69)                  |
| `--ds-page-padding` | `16px`             | `24px`              | `32px`              | space above and below the page content                                                           |

### Frame widths

| Token               | Value   | Use                                                                                                                  |
| ------------------- | ------- | -------------------------------------------------------------------------------------------------------------------- |
| `--ds-frame-narrow` | `40rem` | the sign-in and other auth pages only (`Page frame="narrow"`, UX-69); no other page uses it                          |
| `--ds-frame-wide`   | `96rem` | (1536 px; was `76rem`) the one page frame: header, menu, page and key-hint bar share its left and right edge (UX-69) |

### Control heights

| Token                       | Value  | Use                                                                                                                                                                                                                               |
| --------------------------- | ------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `--ds-control-height`       | `44px` | a button, a field and a select. Never below `--ds-target-min`                                                                                                                                                                     |
| `--ds-control-height-large` | `52px` | the one primary action on a small screen                                                                                                                                                                                          |
| `--ds-row-action-width`     | `8rem` | the width of the row-action column of a table (UX-72); the label never wraps                                                                                                                                                      |
| `--ds-cell-grow-min`        | `6rem` | the narrowest a grow column of a one-line table gets before the table drops a column into the detail; a grow column takes the width its values need from the rest and wraps; the weight (`--ds-grow`) scales this minimum (UX-72) |

## Border widths

The list is closed. A screen uses one of these and no other width.

| Token | Value | Use |
|---|---|---|
| `--ds-border-thin` | `1px` | control borders, dividers, rules between rows (`--ds-hairline`) |
| `--ds-border-medium` | `2px` | the Note edge; a chosen tile |
| `--ds-border-thick` | `3px` | the focus ring; the Done and Needs you edges |
| `--ds-border-double` | `6px` | the Problem edge, always a double line |
| `--ds-border-width` | `var(--ds-border-thin)` | the one width a layout calculation subtracts for a border (`calc(var(--ds-space-3) - var(--ds-border-width))`); never a bare `1px` in a calc (UX-81) |

## Opacity

| Token | Value | Use |
|---|---|---|
| `--ds-opacity-disabled` | `0.6` | a disabled control and a row of Remove or Update that waits for a choice; the reason is always in words beside it (UX-82, UX-17) |

## Motion

| Token                    | Value                            | Use                                              |
| ------------------------ | -------------------------------- | ------------------------------------------------ |
| `--ds-duration-fast`     | `100ms`                          | hover, press                                     |
| `--ds-duration-base`     | `200ms`                          | open and close                                   |
| `--ds-duration-slow`     | `400ms`                          | sheets, page changes                             |
| `--ds-duration-loop`     | `1200ms`                         | one turn of a looping bar or spinner             |
| `--ds-duration-reduced`  | `0ms`                            | every duration under reduced motion              |
| `--ds-duration-screen`   | `300ms`                          | a screen entering                                |
| `--ds-duration-progress` | `300ms`                          | the fill of a progress bar moving to a new value |
| `--ds-duration-toast`    | `5000ms`                         | how long a passing message stays                 |
| `--ds-ease`              | `cubic-bezier(0.2, 0, 0, 1)`     | all movement                                     |
| `--ds-ease-in-out`       | `cubic-bezier(0.42, 0, 0.58, 1)` | movement that starts and ends at rest            |

Motion has a purpose: it shows where something came from or went to (UX-36).

Under `prefers-reduced-motion: reduce` every duration is `0ms` and nothing animates, except a progress bar of unknown total, which shows a static striped fill (UX-17, UX-29).

## Row heights

Starter values. Change to fit.

| Token              | Value  | Use                                    |
| ------------------ | ------ | -------------------------------------- |
| `--ds-row`         | `56px` | a list row without a picture (UX-25)   |
| `--ds-row-picture` | `88px` | a list row with a picture              |
| `--ds-hairline`    | `1px`  | a thin quiet edge or rule between rows |
| `--ds-dot-r`       | `2px`  | the radius of a dot in the dotted form of a column, an area or a heat cell (`dots.tsx` `GRID_R`) |
| `--ds-dot-gap`     | `3px`  | the gap between two such dots (`dots.tsx` `GRID_GAP`) |
| `--ds-icon-size`   | `16px` | a small icon beside text               |

## Tone shapes

A tone is also told by the shape of its edge, so it never rests on colour alone (UX-26). Starter values. Change to fit.

| Tone      | Edge         |
| --------- | ------------ |
| Done      | `3px solid`  |
| Note      | `2px solid`  |
| Needs you | `3px dashed` |
| Problem   | `6px double` |

| Token           | Value  | Use                                |
| --------------- | ------ | ---------------------------------- |
| `--ds-tone-bar` | `12px` | the bar down the left of a message |

## Focus

| Token               | Value | Use                                |
| ------------------- | ----- | ---------------------------------- |
| `--ds-focus-width`  | `3px` | width of the focus ring            |
| `--ds-focus-offset` | `2px` | gap between a control and its ring |

- Every control shows the ring on `:focus-visible`, in `--ds-color-focus` (UX-17).
- Under `forced-colors: active` the ring uses the system colour `Highlight`.

## Z-order

One scale. A screen never writes a raw `z-index` (UX-1).

| Token            | Value | Use                                    |
| ---------------- | ----- | -------------------------------------- |
| `--ds-z-base`    | `0`   | page content                           |
| `--ds-z-sticky`  | `100` | sticky bars and docked headers         |
| `--ds-z-menu`    | `200` | menus and pop-overs                    |
| `--ds-z-overlay` | `300` | the dimmed page behind a dialog        |
| `--ds-z-dialog`  | `400` | dialogs and sheets                     |
| `--ds-z-toast`   | `500` | the one app-wide banner when it floats |

## Density

A project may offer a compact density. It changes only these tokens. The touch target stays `--ds-target-min` (UX-16).

| Token          | Comfortable | Compact | Use                       |
| -------------- | ----------- | ------- | ------------------------- |
| `--ds-row`     | `56px`      | `44px`  | list row height           |
| `--ds-space-3` | `12px`      | `8px`   | gap between related items |
| `--ds-space-5` | `24px`      | `16px`  | gap between cards         |

## Use your own font

1. Add a family token, for example `--ds-font-brand`, with the font and then the system stack as fallback.
2. Point the role you want to change at it (for example, the page title) in this file.
3. Load the font file from the project's own files. Never from another site.
4. Check the contrast and the line length again.
5. Add a `CHANGES.md` entry.

### Cool Terminal (`terminal`, CRSP300, CRSP302)

Owner-approved concept of 2026-11-25 (btop look). A seventh theme that is chosen with the setting **Look** > _Cool Terminal_ or _Cool Terminal (dotted graphs)_ (Settings, Appearance), not from the six colour themes, so the colour-theme choice keeps its previews (UX-22). It has a dark and a light form (CRSP371), like the six colour themes: the Mode setting applies to it and System follows the device.

- **Look:** 1 px borders, `--ds-radius-1` to `-3` are 0, no shadow (`--ds-shadow-2` and `-3` are `none`), no glow (`--ds-glow-focus` is a 1 px ring, `--ds-scanline-focus` is `none`). The font is JetBrains Mono for the console and Inter for prose (`--ds-font-prose`); neither is downloaded, a device without them uses its own monospace or sans.
- **Panel title:** plain text in `--ds-color-text` over the border, no cut-in marks, no coloured letter. A machine name is bold and coloured (component 78).
- **Selected row:** the `--ds-color-selection` tint with a 2 px `--ds-color-primary` inset on its left edge, instead of the reverse-video fill of the other themes.
- **New tokens, in every theme:** `--ds-color-accent` (an ephemeral or new thing; the note colour in the six themes) and `--ds-color-warm` (a warm hint; the needs-you colour in the six themes).
- **Graphs (CRSP302, CRSP358):** the two graph styles differ only in how graphs are drawn. `data-graphs` on the page root is absent for _Blocks_ (blocks: `■` segments, `▁▂▃▄▅▆▇█` sparks) and `dotted` for _Dots_ (round dot segments, braille sparks `⢀⣀⣄⣤⣦⣶⣷⣿`). It is a presentation choice only and changes no colour (UX-55). The segment gap is the token `--ds-seg-gap` (`2px`, every theme).
- **Palettes:** `data-pal=green` (default, no attribute), `amber`, `ice` on the page root, picked with the cards **Terminal Green**, **Terminal Amber** and **Terminal Ice** of the one Theme choice (CRSP358). A theme preview of a Terminal card carries `data-pal` itself, so the palette rules also match `.ds-theme-preview[data-theme-name=terminal][data-pal]`. A palette is a set of token values and nothing else (UX-55). Each palette has a dark and a light value set; light is paper-like in the hue of the palette (green, amber or ice), with the same flat look, and the segment blocks, dotted charts, divider and empty segments keep their tokens, so they stay clear.
- **Contrast:** the concept's `--dim` is the border (3:1). The muted text is `--dim` lightened to 4.5:1 on the page, the surface and the selection fill, so UX-17 holds in all three palettes.

| Token (without `--ds-color-`) | green | amber | ice |
| --- | --- | --- | --- |
| `page` | `#0b0e0d` | `#0e0b06` | `#070b10` |
| `surface` | `#0f1413` | `#140f08` | `#0b1219` |
| `text` | `#cfd8d3` | `#e8d9b8` | `#c9d6e2` |
| `text-muted`, `text-inactive` | `#8b9892` | `#9f8c67` | `#7f92a3` |
| `border` | `#6b7a73` | `#8a7650` | `#62778a` |
| `primary`, `focus`, `done` | `#4fd18b` | `#e8b44a` | `#5fd0c4` |
| `needs-you` | `#d9c04a` | `#f0d060` | `#e0c868` |
| `problem` | `#e0605a` | `#e0705a` | `#e86a7a` |
| `note` | `#4fc3d9` | `#d9a24f` | `#5aa9f0` |
| `accent` | `#b48ae0` | `#c8907a` | `#a98ae8` |
| `warm` | `#e8944a` | `#e8944a` | `#f0a060` |
| `selection` | `#17332a` | `#33260f` | `#112a3d` |

Light forms (CRSP371):

| Token (without `--ds-color-`) | green light | amber light | ice light |
| --- | --- | --- | --- |
| `page` | `#e9efe5` | `#f2eadb` | `#e7edf3` |
| `surface` | `#f4f8f1` | `#faf5e8` | `#f2f6fa` |
| `text` | `#14221a` | `#2b200c` | `#0e1e2c` |
| `text-muted`, `text-inactive` | `#3d5646` | `#5a4620` | `#394f63` |
| `border` | `#5a7263` | `#7d6535` | `#587086` |
| `primary`, `focus`, `done` | `#1b6a3d` | `#865400` | `#08666f` |
| `needs-you` | `#785a00` | `#6a5400` | `#675700` |
| `problem` | `#a52f2a` | `#a5352a` | `#a3253d` |
| `note` | `#0d6277` | `#7a5516` | `#1b5ca6` |
| `accent` | `#74429f` | `#8a4a3a` | `#5a3aaa` |
| `warm` | `#9c4f10` | `#9c4f10` | `#9a4710` |
| `selection` | `#cde4d2` | `#ebd8a6` | `#c9dcec` |

Every text pair is 4.5:1 and the border 3:1 on the page and the surface in light too (UX-17).

The values live in `scripts/design/themes.data.ts` (`TERMINAL`, `TERMINAL_LIGHT`); `scripts/design/themes.test.ts` proves the CSS and the contrast.

## Add a named theme

1. Choose a name, for example `high-contrast`.
2. Give every colour token a value under `[data-theme=high-contrast]`. A theme never leaves a token out.
3. Check WCAG 2.2 AA contrast for each pair (UX-17).
4. Add a way to pick the theme in the settings.
5. Add a `CHANGES.md` entry.
