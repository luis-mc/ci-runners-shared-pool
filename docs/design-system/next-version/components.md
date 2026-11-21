# Components

> **Starter.** Every item in this file is a starter item. Keep, change or delete each one before v1 is approved.

One numbered section per shared component. A part that nearly fits is extended with its section, never copied (UX-2). Each section has the same five lines. The Source paths are placeholders: replace them with the real export path or file.

## 1. Page header

- **Used for:** the headline that says where you are, with an optional line below it and the page's one primary action. (`Stack` of the layout part takes the gap `section`, 32 px, for the sections of a page such as a Settings tab; LOOK16.)
- **Looks:** a large title with a quiet line below it. The primary action sits at the end of the title row, right-aligned. An optional hint is one muted line right-aligned under the action, inside the same slot; the slot is at most 22 rem wide, so a hint never widens it and never moves the action. A page that swaps one action for another (Stop new jobs and Resume) passes a hint in both states, so the header keeps its height and nothing below it moves. At phone width the slot is full width and the hint reads from the left. Under a tab row (component 3) the header sits one page gap (`--ds-space-5`) below the row's underline, on every screen with sub-tabs, whether the tab row and the header share one `Page` or the tab row is its own (a layout route's `Page` followed by the screen's `Page`; `.ds-page + .ds-page`). LOOK36.
- **Source:** `packages/ui/src/components/layout.tsx` (`PageHeader`: `title`, `lead`, `action`, `hint`)
- **Rules:** UX-3, UX-4, UX-17.
- **Guard:** none yet.

## 2. Top menu and breadcrumbs

- **Used for:** moving between the main areas, and showing the path back up. The console has exactly seven areas: Overview, Shared Pool, Repositories, Jobs, Doctor, Settings, Help (LOOK44). An area with parts (Shared Pool, Jobs, Settings) shows them as one tab row (component 3) under the menu, never as more menu items.
- **Looks:** a row of the main areas, starting on the frame edge, with the current one marked as a tab is marked: bold primary text and a 3 px underline, never a box (UX-69). A quiet trail of links shows the path back up. The top bar above it holds the product name and status on the left and Commands and the login on the right inside the same frame; at phone width it is two lines, name and login, then status and Commands. On a phone the areas are one Menu button (`TopMenuButton`), its box on the same frame edge as the title and the tab row; it opens the list under it (UX-15, UX-69). LOOK36.
- **Source:** `packages/ui/src/components/Shell.tsx` (`Header`, `TopMenu`, `TopMenuButton`)
- **Rules:** UX-1, UX-3, UX-16, UX-17, UX-69.
- **Guard:** none yet.

## 3. Tabs

- **Used for:** switching between views of the same thing. A tab never starts work. Tabs keep their panels mounted, so a tab keeps its state when the user comes back.
- **Looks:** labels over one panel. When the labels do not fit the width the row wraps onto as many rows as it needs; it never scrolls sideways (UX-68, owner decision LOOK D6). The first label starts on the frame's left edge, where the page title and the top menu start (UX-69): a tab has no side padding, and the gap between labels is `--ds-space-5`. A short label keeps the touch target: a tab is at least `--ds-target-min` wide (UX-16). A label is never cut: one that does not fit goes to the next row whole (only a label wider than the whole row breaks, at its spaces), so at 360 and 393 px the row is two or three lines. The page header or the first pane under the row is one page gap (`--ds-space-5`) below it (LOOK36). The chosen tab is bold, in the primary colour, with a 3 px underline, so colour is not the only sign. The row is a tab list with arrow-key movement. The top menu wraps the same way.
- **Source:** `packages/ui/src/components/Controls.tsx` (`Tabs`)
- **Rules:** UX-1, UX-8, UX-17, UX-68.
- **Guard:** `packages/ui/src/components/Pane.test.tsx` (Tabs); `e2e/tests/web-console/ux-routes.spec.ts` (`tabEdges`, `tabGap`); `e2e/tests/design-layout.spec.ts` (tab row, title gap and left edges).

## 4. Wizard

- **Used for:** a guided flow with a step bar on top, only the current step, and Back and the primary action at the bottom. A wizard that changes something ends with three steps, in order: Review, then the work (its own verb in the "-ing" form), then Done (UX-30). Review shows what will happen in numbers and words, and the line "Nothing has changed yet." Earlier steps stay reachable, and Review counts again after a change. Its one primary is named by the work and its count. The work step shows live progress and has no Back. Done shows what really happened, never the plan, and its one primary is the next action. A job under about a second opens Done at once. A stopped or failed job still ends at Done and says what was done and what is left. A step that only reads before Review is named by what it reads ("Reading files"), never "Checking". Loading the data of the current step is a state of that step, never a numbered step of its own: the step row starts at 1 with the first step the user acts on and shows no check while loading, the step shows one wait line, its primary is locked with the reason, and Back is free.
- **Looks:** a step bar on top, one step in the middle, and Back and the primary action at the bottom. From 601 px up the step bar is one row with every step the same width (UX-15); on a phone the steps wrap. A wizard in the tray's small window (`Page frame="compact"`) keeps the wrapped bar at every width. The left button says Back, or the words the page gives it: `Not now` on a Review that only asks, `Close` after a failure, `Open Updates` on Done. A wizard whose work goes on without its window (an update the service runs) has `Hide` beside the busy primary while it works; Hide stops nothing, so it is not Stop.
- **Failed step:** with the phase `failed` a page may give `failedAt`, the index of the step that failed. That step is marked failed (✕), is the current one and is not ticked; later steps stay locked. Without it a failure shows as before.
- **Source:** `src/ui/wizard`
- **Rules:** UX-5, UX-4, UX-24, UX-30, UX-35.
- **Guard:** none yet.

## 5. Sticky action bar

- **Used for:** keeping the primary action in reach on a long page or a small screen. Its reason line sits above the buttons.
- **Looks:** a bar fixed to the bottom edge with a quiet reason line above the buttons. It has a soft shadow.
- **Source:** `src/ui/action-bar`
- **Rules:** UX-4, UX-14, UX-16, UX-42.
- **Guard:** `e2e/tests/web-console/ux-routes.spec.ts` (`stickyWalk`: the bar stays in the window while the page scrolls, and the last text ends above it).

## 6. Action rows

- **Used for:** a list where each row has a name, a short line of facts and its own action.
- **Looks:** a plain row with a name, a quiet line of facts and one button at the end.
- **Source:** `src/ui/action-row`
- **Rules:** UX-8, UX-25, UX-16.
- **Guard:** none yet.

## 7. Buttons

- **Used for:** every action. Primary, secondary and quiet styles. A running button is not pressable and says it is running.
- **Looks:** a filled primary, an outlined secondary and a text-only quiet button, all with a clear focus ring. A button and a chip are as wide as their label (`white-space: nowrap`, `flex: none`, `inline-size: max-content`, `min-inline-size: var(--ds-target-min)`): a long label gets a short word (*Set up parity*), it never wraps or shrinks.
- **Source:** `src/ui/button`
- **Rules:** UX-4, UX-8, UX-13, UX-16.
- **Guard:** `e2e/tests/web-console/ux-routes.spec.ts` (`wrapped`, `wordBroken`: a button label is on one line and no word is broken inside).

## 8. Card, card grid and facts list

- **Used for:** a pane: a boxed section; the grid that lays panes out; and a short list of name and value pairs inside a pane. Every titled part of a page is a pane (`Pane`; `Section` and `Card` are panes). A pane may carry a count after its title and actions that belong to the pane as a whole (a Make or Add button); they sit in the pane's one action row at the bottom, never on the border (UX-69). A note stays a note (component 5) inside the pane.
- **Looks:** a box border (`--ds-color-border`, corners from `--ds-radius-2`) with the title cut into the top border: `┤ Machines 3 ├`. The title and count are drawn in `--ds-color-primary` over `--ds-color-page`; the `┤ ├` marks are the border colour. A pane has one inner edge (`--ds-pane-inner`: 16 px, 12 px at phone width) for its title, fields, help text, notes and table. The action row is at the pane bottom, right aligned, Cancel then the one primary; at phone width the buttons stack full width, primary first (UX-37). Help text is as wide as its field. When focus is inside a pane, its border takes the focus ring colour (`--ds-color-focus`), so the user sees which pane has focus (UX-17). Panes sit in an even grid. A pane that holds panes or a card grid leaves half a touch target plus a gap (`--ds-target-min` / 2 + `--ds-space-2`) above its first row, so the title of a card never covers the title of its section (OPS48). The facts list has the name above its value. 
- **Source:** `packages/ui/src/components/Pane.tsx` (`Pane`), `Card.tsx`, `layout.tsx` (`Section`)
- **Rules:** UX-10, UX-1, UX-15, UX-37.
- **Guard:** none yet.

## 9. Dialog and sheet

- **Used for:** one overlay component with three forms, chosen by screen width and by purpose (UX-43). On a small screen it is a bottom sheet. On a wider screen it is a centred dialog when it changes data, or an anchored popover when it only changes the view. The one question card that asks first (Cancel left, the action right) is the dialog form (UX-9).
- **Looks:** a panel over a dimmed page, with a title and close button on top, a body in the middle and the buttons at the bottom. On a small screen it rises from the bottom edge.
- **Source:** `src/ui/dialog`
- **Rules:** UX-9, UX-17, UX-16, UX-35, UX-43, UX-44, UX-45, UX-46, UX-47, UX-48.
- **Guard:** none yet.

### Forms

| Form | When | Behaviour |
|---|---|---|
| Bottom sheet | Below `--ds-bp-sm` (600px), for both purposes | Slides up from the bottom edge. Full width. The page behind is dimmed. |
| Dialog | At `--ds-bp-sm` and wider, when it changes data (a question, a form, a confirm) | Centred. The page behind is dimmed. It is modal. |
| Popover | At `--ds-bp-sm` and wider, when it only changes the view (a filter, a sort, a display choice) | Anchored to its opener. No dimming. It closes on a click outside. |

A small-screen popover is a bottom sheet, so a thumb always reaches it. One component renders all three; a screen never builds its own overlay (UX-2).

### Anatomy

1. **Handle.** A short bar at the top edge of a bottom sheet that can be dragged down to close it. A dialog and a popover have none. A sheet without a drag has none either. The handle is decoration: the close button is the real control (UX-17).
2. **Header.** The title (a section title, one line) and a close button (at least `--ds-target-min` square, labelled "Close"). The title is what the overlay is named by.
3. **Body.** The content. It is the only part that scrolls. The header and footer stay in view.
4. **Footer.** The one primary action on the right, and Cancel on the left (UX-4, UX-24). A view-only popover has no footer: its choices apply at once, and the close button ends it.

### Sizes

| Size | Width | Use |
|---|---|---|
| Sheet | the full screen width, at most 90% of the screen height | small screens |
| Small dialog | `28rem` | a question with a short line |
| Medium dialog | `40rem` | a form |
| Popover | the width of its content, at most `20rem` | a few view choices |

The body scrolls when the content is taller than the space. The overlay never grows past the screen (UX-16).

### Tokens

- Surface: `--ds-color-surface`. Dimmed page: a `--ds-color-text` layer at a low opacity. Edge: `--ds-color-divider` and `--ds-hairline`.
- Corners: `--ds-radius-3`. Shadow: `--ds-shadow-3`.
- Gaps: `--ds-space-4` inside the header, body and footer; `--ds-space-3` between footer buttons.
- Z-order: `--ds-z-overlay` for the dimmed page, `--ds-z-dialog` for the overlay, `--ds-z-menu` for a popover.
- Motion: `--ds-duration-slow` for a sheet, `--ds-duration-base` for a dialog or popover, with `--ds-ease`.
- Focus ring: `--ds-focus-width`, `--ds-focus-offset`, `--ds-color-focus`.

### Behaviour

- **Focus trap.** While a modal form is open, Tab and Shift+Tab move only between controls inside it. The page behind is inert.
- **Escape closes.** Escape closes every form and counts as Cancel. A form with unsaved input asks first.
- **Focus in and out.** On open, focus goes to the safe choice: the first field of a form, or Cancel in a question. On close, focus returns to the opener (UX-35). If the opener is gone, focus goes to the page's heading.
- **Scroll lock.** While a modal form is open, the page behind does not scroll, and it does not jump when the lock starts or ends. A popover does not lock.
- **Outside click.** A click on the dimmed page closes a popover. It closes a sheet or dialog only when nothing is entered.
- **Motion.** A sheet slides up and a dialog fades in, both short (UX-36). Under `prefers-reduced-motion` the overlay appears and goes at once (UX-17).

### Accessibility

- The overlay has `role="dialog"`. A question that needs an answer before anything else uses `role="alertdialog"`.
- A modal form has `aria-modal="true"`. A popover is not modal and does not set it.
- It is labelled by its title (`aria-labelledby`). A line of explanation is its description (`aria-describedby`).
- The close button has a text name. The handle is hidden from assistive technology.
- Contrast and the focus ring follow UX-17 in every theme.

### When not to use it

- To tell the user something: use a message or the banner (component 18).
- For a guided flow of steps: use a wizard (component 4).
- For content the page needs to work: put it on the page (UX-14). An overlay is for a short, focused task.
- For a long form or a task of several steps: use a page.
- For a list of actions at the end of a card: use buttons in the card (UX-37).
- For one more overlay on top of an overlay: change the first one instead. Never stack them.

## 10. Pager

- **Used for:** moving through a long list. A preview ends with "See all N". The pager takes a noun ("Files 1-24 of 32").
- **Looks:** a small bar with Back and Next, and a line that names what is shown and how many there are.
- **Source:** `src/ui/pager`
- **Rules:** UX-11, UX-7.
- **Guard:** none yet.

## 11. List toolbar

- **Used for:** search, filters, the count, and the grid or list toggle above a list. The filter bar extends it: the search field, a Filter button that shows its active count, and a labelled Sort that is always visible. Active filters show as chips, with Clear all after them. The filters themselves open in a sheet (component 9) that ends with a primary "Show N items". The bar is not sticky.
- **Looks:** one row above a list with a search field, the filter button, the count and the view toggle. Every list screen (Repositories, Machines, Runners, Jobs, Images and caches, Users, Audit log) uses this one part and passes the parts in its slots: search, filters, selection (Select all N, Clear, "N selected" and the buttons that act on the selection), count, and the list's own actions (Add). The row is one line on a wide screen and wraps only when it does not fit (360 and 393 px). A screen never lays the toolbar out itself.
- **Source:** `packages/ui` `ListToolbar` (`src/ui/list-toolbar`)
- **Rules:** UX-7, UX-11, UX-16.
- **Guard:** none yet.

## 12. Segmented choices and filter chips

- **Used for:** picking one of a few views (segmented), or turning filters on and off (chips).
- **Looks:** a row of joined buttons with one chosen (segmented), or small round-ended chips that show on or off. A filter with two states, such as Pool on and Pool off, is segmented with a first choice All, never two loose chips. Chips stay for filters with more than two answers. A view switch (Runs and Jobs, 7, 14 and 30 days) is segmented without the All choice (`allLabel={null}`): one view is always chosen.
- **Source:** `packages/ui` `Segmented` (`src/ui/segmented`), `Chips`
- **Rules:** UX-8, UX-17, UX-22.
- **Guard:** none yet.

## 13. One-choice list

- **Used for:** picking one answer from rows (up to 6) or a drop-down (7 or more). A multi-choice list starts with Select all N and Clear.
- **Looks:** rows with a round mark where one is chosen, or a drop-down when there are many.
- **Option with detail (LOOK14):** an option may carry a name, a value and a description. The radio sits on the first line. Line 1 is the name in bold. Line 2 is the value in the normal colour, when the option has one. Line 3 is the description in the muted colour. Options are set apart by a space token and a divider line. The whole option is the click target, with a hover and a selected state (a bar on the start edge). An option's own field (such as Another address) shows under its description only while the option is chosen. A page passes the three parts as data (`Choice.label`, `Choice.detail`, `Choice.description`, `Choice.extra`) and never joins them into one label.
- **Source:** `src/ui/choice-list`, `packages/ui/src/components/Fields.tsx`
- **Rules:** UX-12, UX-22, UX-7.
- **Guard:** none yet.

## 14. Form field family

- **Used for:** a field (name, help, box, error), a toggle (saved at once) and a check box (waits for Save). Boxes come in three widths. A text area is the same field with several lines, for a list with one item per line; it keeps the same name, help and error places and takes the full width. The number field of the number keypad (37) takes `decimals` for a measure such as 16.5 GiB. A field for a secret (a password) hides what is typed; the screen never fills it in again and says beside it whether one is set. A file field lets the person choose one file in place of the browser's own file box: empty (a dashed drop zone with "Drop the file here, or" and a Choose a file button), chosen (file icon, name, size, Change and a remove button) and refused (a Problem border and the reason the caller's `validate` gave). It keeps a real hidden input for the picker, drag and drop, the `accept` filter, and the same name, help and error places; the screen reads the file and never shows what is in it, and it is locked with the rest of the form when the person may not use it. Fields that sit in a row (component 43) share one height, and a name that is too long wraps whole. Fields side by side in a row of any kind share one width class and one box width, and their boxes share one top line: the fields top-align and the row gives the name, the box, the help and the mistake one grid row each, so a name or help that wraps never pushes a box down or leaves a gap above it (CORE-7, UX-23, UX-78; LOOK49). A number field is the short width. A check box or radio is drawn on the left edge of the field column, not indented by its hit area (LOOK25). On an edit screen a field whose value differs from what is saved takes `changed` and is marked with the word "Changed" at the end of its name line, in the primary colour (the box border follows); the mark is drawn by the shared parts, never by a page, and does not change the field's name for a screen reader (LOOK21, UX-71).
- **Looks:** a name above a box, a help line below it and an error line in the Problem tone when needed. A switch is a button with On or Off and its name always beside it; pressing the name works the switch. A check box and a radio are drawn 16 px inside a 44 px hit area (UX-16).
- **Source:** `src/ui/field`; `packages/ui/src/components/FileField.tsx`, `packages/ui/src/components/Fields.tsx`
- **Rules:** UX-20, UX-21, UX-22, UX-23, UX-24, UX-10, UX-16.
- **Guard:** `packages/ui/src/components/look6-8.test.tsx` (file field states, drop and accept).

## 15. Tables and list rows

- **Used for:** a table to compare three or more facts; rows for everything else. Rows become a stack of name and value lines on a phone. Column heads sort.
- **Looks:** a plain grid with light lines and a bold head row. Heads stay on one line and are bottom-aligned; inside a pane the first column starts on the pane's inner edge, with no extra inset (UX-69). On a phone (under 600 px) each row is a stack of `name  value` lines, the first column as the bold title line; a column marked `secondary` is left out of the stack. Given a `title` the table is drawn inside a pane with the title and the row count. The head of a sortable column is a button at least 44 px wide and high (UX-16); the row under the cursor stays in reverse video (component 41). A row that cannot be used now is shown in grey (`inactive`, UX-70): its text and links use `--ds-color-text-inactive` and links stay underlined; the reason and the link that fixes it sit in the row itself, with a `Tag`, never only in the grey. **Long text in a cell (LOOK19):** a column's `long` text is one line with an ellipsis (`ds-table__clip`, at most 40 characters wide, the full cell width at phone width) plus a button (`expandLabel`: *Full error* for a failed runner, *Details* elsewhere) with `aria-expanded` and `aria-controls`. The button opens the full text in a full-width row under the row (`expandedRow`, `role="region"`, named by the button's word and the row), pressed again it closes; no dialog, focus stays on the button, the row is not a list row for `j` and `k`. The one line is the server's short reason when the record has one, else the part before the first " (" or ": ", cut at 60 characters (`summarize`); text of 40 characters or less shows whole, with no button. `CappedList` shows at most two items and a count (*self-hosted, windows +2*) with the full list in visually hidden text. A page passes the text as data, it never draws the clip, the button or the row (UX-49: nothing scrolls sideways). **A row that opens to more than text (RES16):** a table given `detail` (`label`, `render`) ends every row with a button (*Jobs* for a run) with `aria-expanded` and `aria-controls`; it opens the page's own content, for example the waterfall of a run's jobs, in a full-width row under the row (`role="region"`, named by the button's word and the row) and closes again. A cell shows one number, not a range, and a long name is cut with component 56. **One line a row (RES17, UX-72):** a table given `oneLine` keeps every cell on one line, with a value made for one line (see column kinds below) and the full text as hover text (`title`, from the column's `title` or its text); the style never cuts a cell (CRSP210): a value that does not fit wraps at a word or anywhere, so the row grows, a row of one list has one height (a button's height plus the cell padding), the long-text button sits inline at the right of its cell, and the last column is the row action. **Column kinds (RES18, UX-72):** each column of a one-line table is `fit` or `grow` (`kind`; without one, grow for a column with `long` text, else fit). A fit column is as wide as its widest value or head, on one line, never cut: dates, times, counts (*7 of 7*), the result, a short id, the row action. A grow column takes the width its values need from what is left, at least `--ds-cell-grow-min` times its weight, and wraps its text, the full text on hover (repository, workflow, job, reason). The weight sets the narrowest the column gets (CRSP210); it was the share (`grow`, a number, default 1, D102): a column with `grow: 2` takes twice the width of one with 1. Repository, Workflow and the group name have weight 2, Branch and the other grow columns 1, so a long repository name is not cut hard at 1220 to 1300 px. The weight changes only the split of the free width; a fit column is never cut. A branch is cut in the middle (`middle`: the start and the last six characters stay). `Cut` keeps a fixed end next to a cut name: the run number after a workflow (*Docs #8133*), the time after a job (*docs-check, 39 s*). The page declares the kind and passes data; at phone width the kinds do not apply. At 1300 px a table whose fit columns do not fit moves its least useful column into the detail. A table given `action` (`label`, `to`, `name`) ends every row with component 57 in a column of `--ds-row-action-width`; at phone width it is the last line of the card, full width, in the same place in every card. The page passes data only.
- **Source:** `packages/ui/src/components/Table.tsx`, `packages/ui/src/components.css`
- **Rules:** UX-25, UX-16, UX-17, UX-18, UX-39.
- **Guard:** none yet.

## 16. Progress bar

- **Used for:** a wait. Filled when the total is known, animated when it is not. It always names what is happening.
- **Looks:** a thin bar that fills from the start, with a line of text that names the work.
- **Source:** `src/ui/progress`
- **Rules:** UX-29, UX-17, UX-19.
- **Guard:** none yet.

## 17. Badges and counts

- **Used for:** a short status word or number next to a name. A badge is information: it never looks like a button.
- **Looks:** a small flat label with no hover, set next to a name.
- **Source:** `src/ui/badge`
- **Rules:** UX-8, UX-26, UX-17.
- **Guard:** none yet.

A `Tag` is the small outlined label that names a kind next to a word, such as `inactive type` after Public on an inactive row (UX-70). It is flat, like a badge, and has the radius of an input. Source: `Tag` in the shared UI package. Guard: `packages/ui/src/components/look13.test.tsx`.

## 18. Messages and banner

- **Used for:** the four tones (Done, Note, Needs you, Problem); a button's one-line result; and the one app-wide banner slot.
- **Looks:** a box with a bar down its left side, an icon and a word for the tone, then one line of text.
- **Source:** `src/ui/message`
- **Hide a note:** a Note without an action has a small x button named `Hide` (v1 content: Dismiss is retired), reachable by keyboard. It is never on Done, Needs you or Problem. A dismissed note stays hidden in that browser (kept per note in local storage, safe when storage is blocked) and comes back when its text changes. A short state note (saving, looking for a release, stopped, live updates paused) passes `dismissible={false}`.
- **Rules:** UX-26, UX-27, UX-14, UX-17.
- **Guard:** none yet.

## 19. Empty, failed and error states

- **Used for:** a place with nothing to show, something that failed, or content that could not load. It says what, why and what next.
- **Looks:** a block with a short title, one line of why and one button for what next, shown once. A page passes its headline as `pageTitle` and its next action to the state only, so a failed or empty page keeps its h1 (UX-17) and the action is never also in the page header (UX-64).
- **Source:** `packages/ui/src/components/State.tsx`
- **Rules:** UX-6, UX-28, UX-3.
- **Guard:** none yet.

## 20. Selection bar

- **Used for:** acting on the items the user selected. It shows the count of what is selected and visible.
- **Looks:** a bar that appears when something is selected, with the count and the actions.
- **Source:** `src/ui/selection-bar`
- **Rules:** UX-7, UX-12, UX-13, UX-38.
- **Guard:** `e2e/tests/web-console/ux-routes.spec.ts` (`selectAll`: Select all N and Clear; `stickyWalk`: the bar stays in the window).

## 21. Search bar and find layout

- **Used for:** the search or filter bar, and the layout of a page that finds things: the bar, then the chips or toolbar rows, then the content, always in that order at the same distances. The bar has an icon, the field, and one small x inside the field that empties it and keeps focus.
- **Looks:** a field with a search icon on the left and a small x inside on the right, then the chips, then the content.
- **Caret:** every single-line text box (the search bar, the command palette box of component 38, the text field of component 14, the typed date of the date picker) shows the Phosphor block caret instead of the browser's thin line (look review option A, LOOK33). The block is `--ds-color-primary`, sits at the real insertion point, follows typing, the arrow keys, Home, End and clicks, and shows only while the box has focus. The character under it is drawn in `--ds-color-on-primary`, so it stays readable like on a terminal. It blinks with `--ds-cursor-blink` (1000 ms, steady under reduced motion). While text is selected the normal selection shows and the block does not. Right-to-left text, a password and an IME composition keep the browser's own caret, and so does a text area. The block is hidden from screen readers. It is drawn by the shared `CaretInput`; a page never draws a caret.
- **Source:** `src/ui/search-bar`, `packages/ui/src/components/CaretInput.tsx`
- **Rules:** UX-31, UX-7, UX-16, UX-17.
- **Guard:** none yet.

## 22. Check box row

- **Used for:** one check box beside its label, with the explanation on its own line below. A choice that waits for Save uses it. A row tick and a filter chip use the same part with a hidden or styled label. It has a partial state (announced as mixed), an invalid state and a disabled state.
- **Looks:** a square box beside its label, with a quiet explanation on the line below. A row may show its label in bold over the explanation and a quiet note at the end (amber when it needs the person's attention).
- **Source:** `src/ui/check-row`
- **Rules:** UX-22, UX-16, UX-17.
- **Guard:** none yet.

## 23. Slider

- **Used for:** choosing one value on a line by dragging or pressing. It has a visible name, a track and a handle, and the current value in words given by the caller. Arrow keys step by one, Page Up and Page Down by a tenth, Home and End go to the ends. On a phone the value sits above the track so a thumb never covers it.
- **Looks:** a labelled track with a round handle and the value in words.
- **Source:** `src/ui/slider`
- **Rules:** UX-40, UX-16, UX-17.
- **Guard:** none yet.

## 24. Time axis list

- **Used for:** things that happen over time, read as one axis. It has a range bar (the chosen range, Clear, zoom out and zoom in), a chart of the range with one bar per period, and the rows under a period header with its count. The range bar and the period header dock under the top bar while the rows scroll. An empty period says so and offers to widen the range. An error keeps the rows already drawn.
- **Looks:** a range bar on top, a bar chart under it, then rows grouped under period headers that show a count.
- **Source:** `src/ui/time-axis`
- **Rules:** UX-33, UX-6, UX-16, UX-18.
- **Guard:** none yet.

## 25. Page jump map

- **Used for:** jumping to any page of a long paged thing by seeing all of them. It is a grid of page tiles in reading order, each a small picture with its number. The current page is ringed and says "Now". A tile jumps there and closes the map. A page that cannot be drawn keeps its number and a quiet "No picture". It opens from one button as a sheet. On a phone there are four tiles across.
- **Looks:** a grid of small page pictures, each with its number. The current one has a ring and the word Now.
- **Source:** `src/ui/page-map`
- **Rules:** UX-32, UX-16, UX-17.
- **Guard:** none yet.

## 26. Live log

- **Used for:** a program's lines as they are written. It has a title, a Show choice (everything, warnings and errors, errors only), Follow new lines, a Full window button that grows the card over the page (Esc and "Back to normal size" shut it), and one quiet note on what is kept and that secrets are taken out. Each line has a time, the level written out, and the message. Lines come from one helper, so a job writes one started line, a progress line at most once a minute, and one finished line.
- **Looks:** a card with a title, a row of controls, and lines that each show a time, a level word and a message, in a monospace face.
- **Source:** `src/ui/live-log`
- **Rules:** UX-16, UX-17, UX-35.
- **Guard:** none yet.

## 27. Activity strip

- **Used for:** the one place that says what runs now. Closed, it is one line: the label, one running mark, the lead job's name, its bar, the count and time left, "+K more", and a button "Show all N jobs". Open, the lead shows in full and the others are aligned rows. One job shows no button. The choice is remembered per person, and a missing store never breaks it. A new or failed job opens it once. The lead is the job that ends last.
- **Looks:** a one-line strip with a running mark, the lead job's name and bar, and a button to open the rest. Open, it shows aligned rows.
- **Source:** `src/ui/activity-strip`
- **Rules:** UX-34, UX-19, UX-29, UX-17.
- **Guard:** none yet.

## 28. Dates, times and numbers

- **Used for:** the one formatter for every date, time, number, size, percent and time left. No screen builds its own string. The formats are in content.md.
- **Looks:** no look of its own. It gives every screen the same text for a date, a size and a count.
- **Source:** `src/lib/format`
- **Rules:** UX-18.
- **Guard:** none yet.

## 29. Table export

- **Used for:** the Export button of a table. It writes every row in the order shown, neutralises text that looks like a formula, and names the file for what it holds, then the date and time. A page that offers it does not name the file format in its lead.
- **Looks:** one quiet button at the end of a table's toolbar.
- **Source:** `src/ui/table-export`
- **Rules:** UX-39, UX-25, UX-18.
- **Guard:** none yet.

## 30. Picture card, grid and rail

- **Used for:** a thing that is a picture (a cover, a portrait, a photo) shown as a tile with its title and one quiet line. The whole tile is one link. A small badge may sit on the corner and a bar may show progress. Tiles sit in a grid that fills whole rows, or in one horizontal rail. A tile lifts a little on hover and focus, and never over the next tile. Placeholders keep the final size, so nothing moves while loading. Items that are not pictures are rows, not tiles.
- **Looks:** a picture tile with its title and one quiet line below. Tiles sit in an even grid or one sideways rail.
- **Source:** `src/ui/picture-card`
- **Rules:** UX-10, UX-11, UX-8, UX-16, UX-36.
- **Guard:** none yet.

## 31. Chart with a text alternative

- **Used for:** numbers over time or in shares, drawn as inline SVG to scale, labelled, in token colours. Each chart has a text or table alternative. A chart shows the top N and then "See all N". Key numbers are lettered figures, never tiles.
- **Looks:** a drawn chart with labelled axes and token colours, and a table or text under it. Key numbers are large lettered figures.
- **Source:** `packages/ui/src/components/Charts.tsx`
- **Rules:** UX-41, UX-17, UX-18, UX-11.
- **Guard:** none yet.

## 32. Shared component rules

- **Used for:** the rules every component above follows. A component that breaks one is extended, never copied (UX-2).
- **Looks:** no look of its own. It sets how the parts above treat each other.
- **Source:** `src/ui`
- **Rules:** UX-1, UX-2, UX-8, UX-13, UX-37.
- **Guard:** none yet.

| Component | Rule |
|---|---|
| Primary button | One per region. It is named by what it does. |
| Secondary button | Grouped in one actions row next to the primary. In a card, one per line at the card width. |
| Danger button | Always followed by the in-page question card (UX-9), never a browser box. |
| Link | Used for navigation, and inside running text. An action is never a plain link. |
| Metadata line | Quiet text with a middle dot between facts. Equal years collapse ("2015", not "2015-2015"). |
| Status text | Plain text, emphasis by weight. A warning is a quiet line with its action. |
| Tag | A small flat label with no hover and no focus. It never looks like a button. |
| Section | A heading with a hairline. No heavy boxes round ordinary content. |
| Panel | Only for a self-contained unit that needs framing. It is the one shared card. |
| Progress | A thin bar plus text ("page 12 of 24"), not a boxed chip. |
| Bulk action | Names its scope in the label ("Move all 12 files"), then a preview, then a question, then Undo. |

## 33. Other shared parts

- **Used for:** smaller parts that every screen shares. Reuse them, extend them, never copy them. The icon set: one set, and an icon beside a tone always has a word too (UX-26). The help line: one quiet line under a control, in a sentence that ends in a full stop. The compact row: one layout for an item and a group of items, with a 44 px tick target, a title on one line, one quiet second line, and at the end a size or a chevron that opens the group in place. The facts list: name and value pairs with a thin line between rows, each name above its value in a narrow card. The data hooks: the one way a screen loads and pages a list, so loading, empty and failed states are the same everywhere. Emoji are never functional icons: an icon that does a job is from the icon set. An emoji may mark a celebration.
- **Looks:** small shared parts: an icon, a quiet help line, a compact row and a facts list.
- **Source:** `src/ui`
- **Rules:** UX-2, UX-6, UX-8, UX-16, UX-17.
- **Guard:** none yet.

## 34. Change review

- **Used for:** reviewing a list of proposed changes before they are applied, such as the Review step of a wizard (component 4). A summary bar shows the counts. Each count filters the list when pressed. Change chips show the kinds of change, each with its count. A changed part is marked by more than colour: a word, an icon or a line style (UX-26). A conflict is never ticked by default, and a line says why it is a conflict. One sticky action at the bottom applies the ticked changes and shows their count.
- **Looks:** a bar of counts across the top, a row of chips under it, then the rows with a tick, a name and a mark for the kind of change. A bar fixed to the bottom holds the one action.
- **Source:** `src/ui/change-review`
- **Rules:** UX-4, UX-7, UX-12, UX-26, UX-17, UX-16.
- **Guard:** none yet.

<!-- luismc-docs component: date-picker -->
## 35. Date picker

- **Used for:** every date the user enters: a date of birth, a start date, moving something to another day. It replaces a native date input and a keypad for dates. Counts, measures and codes use the number keypad; times use the time picker.
- **Looks:** the field name and the chosen date in words over a rule; a row with previous and next month buttons around a month select and a year select; a month grid of day buttons with the weekday initials on top; and a typed entry under the grid. The chosen day is the primary fill. Today has a thicker edge. A day outside the range has a dashed edge.
- **Source:** `packages/ui/src/pickers/DatePicker.tsx` (logic in `date-picker-logic.ts`, styles in `date-picker.css` and `picker-base.css`)
- **Rules:** UX-56, UX-16, UX-17, UX-20, UX-22, UX-43.
- **Guard:** the template's unit tests (`DatePicker.test.tsx`): keyboard, value and limits. With the enforcement kit, `scripts/design/raw-inputs.test.ts` bans a native date input (UX-56).

### Behaviour

- **Value.** An ISO day (`YYYY-MM-DD`) or nothing. A day is picked by pressing it, or by typing it and pressing Enter or leaving the box.
- **Bounds.** `min` and `max`. Days outside are disabled, the month buttons stop at the range, and the year select offers only the years in it. A typed day outside the range is refused with a line that names the range.
- **Typed entry.** Three numbers in the locale's order (`DD/MM/YYYY`, `MM/DD/YYYY`, `YYYY-MM-DD`), separated by `/`, `.`, `-` or a space. A two-digit year and a day that does not exist are refused with a line that shows the pattern.
- **Shown value.** The header shows the chosen day through `formatValue`. Without it, the header shows the locale's long date ("14 March 2009"). This default is on purpose: a long date is easier to check, for example a date of birth. `formatValue` is the hook for the project's one date formatter; pass it when the header must match the content.md format (year-month-day).
- **Locale.** Month and weekday names, the order of the typed numbers and the week start (Monday by default) follow the locale.
- **Where it opens.** Inline, or inside the project's one overlay component (a sheet on a phone, a popover or dialog on a wider screen) with Cancel and Done. It builds no overlay of its own.

### Keyboard and assistive tech

- The picker is a group named by its field name. The day grid is an ARIA grid with one tab stop (WAI-ARIA APG, date picker).
- Arrow keys move a day or a week. Home and End go to the start or end of the week. Page Up and Page Down change the month; with Shift, the year. Keys never leave the range.
- Enter or Space picks the focused day. Escape cancels when the picker sits in an overlay.
- Each day is named in full ("Saturday 14 March 2009"). Today is `aria-current="date"`. The chosen day is `aria-selected`.
- Every button, select and box is at least `--ds-target-min` square and shows the focus ring (`--ds-focus-width`, `--ds-color-focus`) on keyboard focus.

### Words (content.md)

"Previous month", "Next month", "Month", "Year", "Or type the date", "Type the date as {pattern}.", "Pick a date from {min} to {max}." `design_system.py components` adds each word that is not there yet to the Component words table of content.md. A project changes them there and passes them through the `labels` prop.

<!-- luismc-docs component: time-picker -->
## 36. Time picker

- **Used for:** times of day and durations. `units` sets the columns: `h` for an hour setting, `hm` for a time of day such as a reminder, `hms` for a duration such as a rest or a timed hold. It replaces a native time input and a keypad for times.
- **Looks:** the field name and the value over a rule; then one column per unit, each an up key, the number in a large tabular figure, and a down key, with ":" between columns. The clock is 24-hour by default (UX-18); the locale never changes it. A caller may ask for 12-hour with `hourCycle`, which adds an AM/PM switch after the last column. An optional row of quick-pick chips sits below; the chip that matches the value is pressed.
- **Source:** `packages/ui/src/pickers/TimePicker.tsx` (logic in `time-picker-logic.ts`, styles in `time-picker.css` and `picker-base.css`)
- **Rules:** UX-56, UX-16, UX-17, UX-20, UX-22, UX-43.
- **Guard:** the template's unit tests (`TimePicker.test.tsx`): keyboard, value and limits for `h`, `hm` and `hms`. With the enforcement kit, `scripts/design/raw-inputs.test.ts` bans a native time input (UX-56).

### Behaviour

- **Value.** Always 24-hour text: `HH`, `HH:MM` or `HH:MM:SS` by `units`. The 12-hour clock is display only.
- **Clock.** 24-hour by default; 12-hour when the locale's clock is 12-hour, or when `hourCycle="h12"` is set.
- **Steps.** `step` sets the minute and second step (5 for a reminder). Hours step by 1. A value off the step snaps to the next step.
- **Limits.** Hours 0 to 23, minutes and seconds 0 to 59. Up and Down wrap (23 to 00).
- **Known limit.** There is no `maxHours` prop: a duration of 24 hours or more cannot be entered. Add it when a real need comes.
- **Where it opens.** Inline, or inside the project's one overlay component with Cancel and Done. On a dense settings screen the quick picks may also sit under the closed field.

### Keyboard and assistive tech

- The picker is a group named by its field name. Each number is a `spinbutton` with `aria-valuenow`, `aria-valuemin`, `aria-valuemax` and `aria-valuetext` ("30 minutes") (WAI-ARIA APG, spinbutton).
- Up and Down change the value by the step. Page Up and Page Down by 3 hours, 15 minutes or 15 seconds. Home and End go to the lowest or highest value. Left and Right move between columns.
- Enter is Done and Escape cancels, when the caller passes `onDone` and `onCancel`.
- The up and down keys are for touch and pointer; they are out of the tab order because the keyboard already steps the number. Each is at least `--ds-target-min` square and named ("More minutes").

### Words (content.md)

"Hours", "Minutes", "Seconds", "More {unit}", "Fewer {unit}", "Morning or afternoon", "Quick picks", "{n} hours", "{n} minutes", "{n} seconds". The AM and PM words come from the locale. `design_system.py components` adds each word that is not there yet to the Component words table of content.md. A project changes the others there and passes them through the `labels` prop.

<!-- luismc-docs component: number-keypad -->
## 37. Number keypad

- **Used for:** counts, measures and codes only: a number of repetitions, a weight, a distance, a quantity, a one-time code. Many quick entries in a row. Never for a date, a time or a duration: those use the date picker and the time picker. It replaces a native number input.
- **Looks:** the field name; the value in a large tabular figure in a box, with its unit after it; a range line under the value ("From {min} to {max}."); a 3 by 4 grid of keys filling the width (1 to 9, then Clear in the bottom-left key, or "." when there are decimals, then 0 and Delete last digit in the bottom-right key); a quiet reason line. Inside the one overlay (`embedded`) the keypad shows no title and no buttons of its own: the overlay has the one title, and its footer has one Cancel on the left and Done as the primary on the right (UX-44). The overlay is about 420 px wide (`Dialog size="keypad"`) and a full-width sheet on a phone. Stand-alone, the keypad keeps its own title, Cancel and Done.
- **Source:** `packages/ui/src/pickers/NumberKeypad.tsx` (logic in `number-keypad-logic.ts`, styles in `number-keypad.css` and `picker-base.css`); the overlay use is `NumberField` in `packages/ui/src/components/Controls.tsx`
- **Rules:** UX-56, UX-16, UX-17, UX-20, UX-22, UX-43, UX-44.
- **Guard:** the template's unit tests (`NumberKeypad.test.tsx`): keyboard, value and limits for a number and a code. With the enforcement kit, `scripts/design/raw-inputs.test.ts` bans a native number input (UX-56).

### Behaviour

- **Value.** Text, so a code keeps its leading zeros. `kind="number"` (default) or `kind="code"`.
- **First key.** It opens with the current value; the first key replaces it, as a native field does.
- **Limits.** `decimals` (0 has no "." key), `min`, `max` and `maxLength`. A key that would pass `max`, the decimals or `maxLength` is refused. Done is disabled below `min`, on an empty value, or on a code shorter than `maxLength`, and the reason line says why.
- **Known limit.** There is no minus key and no `allowNegative` prop: a negative number cannot be entered. Add it when a real need comes.
- **Where it opens.** Inline, or inside the project's one overlay component (a sheet on a phone). It builds no overlay of its own.

### Keyboard and assistive tech

- The keypad is a group named by its field name. The value is an `output` that is announced when it changes.
- Digits, "." and Backspace type from anywhere inside it. The keys are one tab stop; arrow keys move between them, Home and End go to the first and last key. Enter on a key presses it; Enter elsewhere is Done. Escape cancels.
- Every key and button is at least `--ds-target-min` high and shows the focus ring on keyboard focus.

### Words (content.md)

"Delete last digit", "Clear", "Cancel", "Done", "Type a number.", "The lowest is {min}.", "The highest is {max}.", "Type all {n} digits.", "From {min} to {max}." `design_system.py components` adds each word that is not there yet to the Component words table of content.md. A project changes them there and passes them through the `labels` prop.

## 38. Command palette and shortcut list

- **Used for:** the keyboard-first entry of UX-57. `Ctrl+K` (`Cmd+K` on macOS) opens a search over the areas, the items found by name and the commands the user's role allows. Focus starts in the search box. Typing filters; Down and Up (and `j` and `k` outside the box) move through the results, `Enter` opens one, `Escape` closes. `?` opens the list of every shortcut. Both sit in the one overlay of component 9 and use the search bar of component 21. A command opens the one place where it lives (UX-52).
- **Looks:** a dialog with a search bar over a list of results. A result names the thing and says its kind in a quiet word (area, machine, command). The chosen result has a weight and a tint, never colour alone. The shortcut list is a facts list of key and action.
- **Parts:** `SearchList` (`packages/ui/src/components/SearchList.tsx`) is the search box, the filtered list, the arrow movement and `Enter`; the palette uses it and a list page may too. `ArrowNavigation` (`packages/ui/src/components/Navigation.tsx`) is mounted once in the shell and gives every page Down, Up, `j` and `k` (UX-57), and with `handleRowKey` (`RowKeys.ts`) Left, Right, Home and End along every horizontal row. A page and the `Tabs` part never wire their own arrow keys.
- **Source:** `packages/ui/src/components/Shell.tsx` (`ResultList`, `ShortcutTable`), `packages/ui/src/components/SearchList.tsx`, `packages/ui/src/components/Navigation.tsx`, `apps/web/src/shell/Shell.tsx`
- **Rules:** UX-17, UX-43, UX-44, UX-46, UX-47, UX-57.
- **Guard:** none yet.

## 39. Copyable command block

- **Used for:** a command the user copies and runs elsewhere, such as the install command of a machine. The Copy button copies the text and says "Copied". It changes nothing else.
- **Looks:** a monospace block that wraps long lines, with the Copy button under it.
- **Source:** `packages/ui/src/components/Controls.tsx` (`CodeBlock`)
- **Rules:** UX-1, UX-17, UX-27.
- **Guard:** none yet.

## 40. Level meter

- **Used for:** a level against a limit that is not a wait: memory, CPU and slots against a machine's budget (UX-41). The value is also said in words, so the bar never stands alone (UX-17).
- **Looks:** a name, a thin bar filled to the level, and the value in words ("40% of 80%"). Over the limit the fill changes tone and the words say so.
- **Source:** `packages/ui/src/components/Progress.tsx` (`LevelBar`)
- **Rules:** UX-17, UX-41, UX-58.
- **Guard:** none yet.

## 41. Selected row and key-hint bar

- **Used for:** showing which list row is selected, and which keys work (UX-57).
- **Looks:** the selected row of a table or a result list, the row under the cursor (`j` and `k`, or a click or Tab), is in reverse video: `--ds-color-primary` fill with `--ds-color-on-primary` text. A row the user has ticked (a choice list) takes the `--ds-color-selection` fill and keeps its text colour. The key-hint bar is fixed at the window foot, with a thin top border; its keys sit inside the page frame (UX-69). Each key is listed once, whatever the spelling (`j / k` and `j k` are one key). It lists only the keys that work on the page: a page registers its keys (`useKeyHints`) and a part that adds keys registers them too, so `j / k` and `Enter` show only where a list exists. Each key is in the key style of component 38. The hints are text, never buttons (UX-8). The bar is not drawn on a touch or coarse pointer, where there are no keys. The page reserves the bar's real height, which is read from the bar itself (it wraps onto two lines on a phone), not a fixed 44 px.
- **Source:** `packages/ui/src/components/Table.tsx`, `packages/ui/src/components/KeyHints.tsx` (`KeyHints`, `useKeyHints`), the `hints` slot of `Frame`, `packages/ui/src/components.css`
- **Rules:** UX-8, UX-50, UX-57, UX-60.
- **Guard:** none yet.

## 42. Theme choice

- **Used for:** the Theme choice in Settings, Appearance (UX-60): a one-choice list (component 13) of the six themes.
- **Looks:** each row shows the theme name and, under it, a small row of that theme's colours (surface, primary, Done, Note, Needs you, Problem) in a box in the theme's page colour. The swatches are drawn from the theme's own tokens; the row is decoration and the name carries the meaning. The theme row may show a `ThemePreview` of that theme in dark and one in light (`Choice.preview`) instead of the swatch row; Settings, Appearance does. A choice with previews is drawn as cards in a grid, 3 per row on a desktop, 2 on a tablet and 1 on a phone; each card holds the radio, the theme name and the dark and light previews side by side, the whole card is the choice, and the selected card has a 2 px primary border. The mode (system, dark, light) is a second one-choice list beside it. `ThemePreview` is a small live sample of a theme in dark or light: a pane with its title in the border, a selected row in reverse video and a primary button, all from that theme's tokens. It is a picture with one label, not a control.
- **Source:** `packages/ui/src/components/Fields.tsx` (`Choice.swatch`), `packages/ui/src/components/ThemePreview.tsx`, `packages/ui/src/themes.ts`
- **Rules:** UX-22, UX-55, UX-60.
- **Guard:** `scripts/design/themes.test.ts` (tokens and contrast); `apps/web/src/screens/settings/Settings.test.tsx` (picking and keeping a theme).

## 43. Field grid

- **Used for:** a form's short fields (numbers, short text) that belong together, such as the budget and slots of a machine.
- **Looks:** the fields sit in equal-width columns (auto-fill, at least 200 px), as many as fit the width, and fold to one column on a phone. Each name sits on one line above its box (an ellipsis and a title when it is too long, UX-20), every box has the same height and the boxes sit on one baseline. Fields never touch (UX-10). Settings, Retention uses it.
- **Source:** `packages/ui/src/components/layout.tsx` (`FieldGrid`), `packages/ui/src/components.css`
- **Rules:** UX-10, UX-15, UX-16, UX-20.
- **Guard:** `e2e/tests/design-layout.spec.ts` (layout at 360, 393 and 1300 px).

A field's width caps its box, never its name and help: a lone field's name and help use the width of the section, one line each on a desktop, and the box keeps its own width below (component 14, LOOK10). Side by side in a row, a field is as wide as its box.

## 44. GitHub grant

- **Used for:** every place that needs an App permission or repository access it lacks (Set up parity, Turn on the pool, Repositories, the pool per organization).
- **Looks:** a needs-you message with one step list: each step has its label on the left and, next to it, its state word (done, missing) and its text (UX-15). One primary external button opens the page of the next missing step in a new tab; a later step is plain text. When everything is granted it is a done message that says Ready.
- **Behaviour:** it checks again when the tab regains focus and every 15 seconds while it waits, and calls the page once when everything is granted. Only addresses on `https://github.com/` are opened.
- **Source:** `packages/ui/src/components/Grant.tsx` (`GithubGrant`).
- **Rules:** UX-15, UX-26, UX-38, UX-58.
- **Guard:** `packages/ui/src/components/Grant.test.tsx`, `e2e/tests/web-console/a11y.spec.ts`.

## 45. Ordered list

- **Used for:** choosing the order of a fixed set of items, where each item is used once (the queue order in Budgets).
- **Looks:** one list of numbered rows, `1  Repository priority  [↑] [↓]`. The two icon buttons sit at the right of the row and stay there at phone width; each is 44 px (UX-16). Move up is disabled on the first row, Move down on the last. An item cannot be chosen twice, so a duplicate is impossible.
- **Behaviour:** Move up and Move down swap the row with its neighbour. With a row focused, Alt+ArrowUp and Alt+ArrowDown do the same and focus follows the row. A live line says the new place. A stored value with a repeated or missing item is normalised on load: the first of each is kept and the missing are appended in the default order.
- **Source:** `packages/ui/src/components/OrderList.tsx` (`OrderList`, `normaliseOrder`), `packages/ui/src/components.css`
- **Rules:** UX-16, UX-20.
- **Guard:** `packages/ui/src/components/OrderList.test.tsx`, `apps/web/src/screens/settings/Budgets.test.tsx`.
- **Words:** see content.md, Ordered list words.

## 46. Open row

- **Used for:** a list where each row is one item that opens its own screen: a bold name, one status line (a Status or quiet text) and a quiet note at the end. The whole row is the button; Enter opens it. No table, no tool lists in the row.
- **Looks:** a plain row with a hairline below, the name above the status line, the note at the end followed by a chevron. On a phone the note drops under the line.
- **Source:** `packages/ui/src/components/Rows.tsx`
- **Rules:** UX-16, UX-25, UX-57, UX-58.
- **Guard:** `e2e/tests/web-console/ux-routes.spec.ts` (touch, layout).

## 47. Folded section

- **Used for:** detail a person rarely needs: one line that opens it in place.
- **Looks:** a triangle and one line; the opened part sits under it with the same spacing as a pane body.
- **Source:** `packages/ui/src/components/Rows.tsx`
- **Rules:** UX-16, UX-10.
- **Guard:** none yet.

## 48. Action bar

- **Used for:** the main action of a screen, always in the same place and the same order (UX-71, UX-4). Selection screens, edit screens, list screens with one main action, and every wizard.
- **Looks:** one bar, sticky at the foot of the main content, above the key-hint bar (component 41), with a thin top border and the page fill. It never scrolls away: `Frame` draws it as a direct child of the main column, whatever wrapper the page puts it in, so it sticks to the window, the last row of the page ends above it, and it sits above the key-hint bar at its real height (no bar: no gap). Left to right: the status line in muted text ("2 selected", "Unsaved changes (3)"; none when there is nothing to say), the secondary actions (Cancel, Back, Stop), the one primary action. A disabled primary says why next to it (UX-38). From 600 px down the bar is full width and the buttons stack at equal width, the primary on top, the secondary actions under it. A wizard draws its Back, Stop and primary through this part; a wizard inside a screen that has its own bar sits in the flow (not sticky) and draws default buttons. An edit screen draws its bar through `EditBar`: the status "Unsaved changes (n)" from the number of changes, Cancel (puts every field back to what is saved) and Save, both disabled until something changed, and the leave guard: leaving by a link, a tab, browser Back or by closing the browser tab asks "Discard changes?" with Keep editing and Discard (the tab close shows the browser's own question). The page passes the number and what Save and Cancel do. A tab of many sections (Settings) has one bar: each section registers what it changed with the tab, the status counts them all, and Save sends each changed section in turn.
- **Source:** `packages/ui/src/components/ActionBar.tsx` (`ActionBar`, `selectedStatus`, `unsavedStatus`), `packages/ui/src/components/EditBar.tsx` (`EditBar`, `LeaveGuard`), `packages/ui/src/components/Wizard.tsx`, `packages/ui/src/components.css`
- **Rules:** UX-4, UX-13, UX-38, UX-71.
- **Guard:** `scripts/design/action-bar.test.ts` (a primary button outside the bar, or two in it, fails; `action-bar.known.json` only shrinks); `e2e/tests/web-console/ux-routes.spec.ts` (`stickyWalk`: the bar never scrolls away and never covers the last row or the key-hint bar).

## 49. Native update window and installer art

- **Used for:** the update on Windows: one native window for the whole update (Review, Downloading, Waiting for jobs, Installing, Restarting, Done, Failed), and the banner and dialog bitmaps of the MSI. Outside the shared UI package because Windows draws it; it still wears the same tokens.
- **Looks:** the Phosphor dark theme: page fill, text and muted text, a progress bar with a border and a filled part, a divider line above one row of buttons on the right. At the top a band: the crsp icon and the product name in the primary colour on the page colour with faint scanlines. The heading is bold in the primary colour (done colour on Done, problem colour on a failure). The primary button is filled, the others outlined, with a focus ring just inside the button; a busy button is dim and says so. The window has a title, the crsp icon and a close button only, no minimise or maximise, is centred on the screen of the tray icon, comes to the front once and is never always on top. It is laid out in units of the font height, so no text is cut off at 100, 125, 150 or 200 %. The installer bitmaps keep their light side where the stock dialogs write text, and carry the same band art (icon, name, scanlines) on the other side.
- **Source:** `internal/updatewindow/` (`model.go` the states, `layout.go` the units, `art.go` the band and the installer bitmaps, `window_windows.go` the window), `internal/updatewindow/tokens_gen.go` (generated from `packages/ui/src/tokens.css` by `internal/updatewindow/genart`), `installer/build.ps1` and `installer/crsp.wxs` (`WixUIBannerBmp`, `WixUIDialogBmp`)
- **Rules:** CORE-1 to CORE-7, UX-30 (steps in order, later steps cannot be pressed), UX-71 (the buttons on one bar at the foot), UX-4.
- **Guard:** `internal/updatewindow/layout_test.go` and `art_test.go` (nothing clipped at every DPI, the palette equals the tokens), and `crsp update-window --layout-check` on the Windows runner (`.github/workflows/tray-smoke.yml`).

## 50. Waterfall

- **Used for:** the steps of a job or the jobs of a run on one time axis: where each started, how long it ran, how long it waited, the forecast for its end and "now" on a live run.
- **Looks:** one row per step: a glyph, the name, a muted second line and the detail in words, and a bar on a shared axis. A wait is a lighter hatched segment before the bar. The forecast is a bracket with end caps after the bar. The critical path has a thicker outline and the words "critical path". "now" is a dashed line. A row that opens is a button. At phone width the label stacks above the bar. A table under "Show as table" says every row in words; a page that already has a table of the same jobs passes its id (`altTable`), and the waterfall then folds no table of its own and points to that one (aria-describedby).
- **Source:** `packages/ui/src/components/Waterfall.tsx` (`Waterfall`), `packages/ui/src/components/VizAlt.tsx`, `packages/ui/src/components.css`
- **Rules:** UX-41, UX-17, UX-18, UX-11, UX-49, UX-57, UX-58.
- **Guard:** `packages/ui/src/components/Viz.test.tsx`.

## 51. Job graph

- **Used for:** the jobs of a run and what each needs.
- **Looks:** the stages as columns, a job one stage after the last job it needs, with connectors between them; the critical path has thicker outlines. A loop in the needs never breaks it: the edge that closes it is dropped. At phone width the stages are a vertical list and each job says "after: a, b". A job that opens is a button. A table under "Show as table" lists stage and needs; a page that already has a table of the same jobs passes its id (`altTable`), and the graph then folds no table of its own and points to that one (aria-describedby).
- **Source:** `packages/ui/src/components/JobGraph.tsx` (`JobGraph`, `layoutGraph`), `packages/ui/src/components.css`
- **Rules:** UX-41, UX-17, UX-18, UX-49, UX-57, UX-58.
- **Guard:** `packages/ui/src/components/Viz.test.tsx`.

## 52. Trend chart

- **Used for:** one to four series over time or over categories, as lines or columns, with an optional range band.
- **Looks:** inline SVG to scale with the top and bottom value and the first and last time named; lines differ by dash as well as colour, and a legend names them. A table under "Show as table" lists every point. A `stacked` mode stacks the parts of one total (component 63).
- **Source:** `packages/ui/src/components/Charts.tsx` (`TrendChart`), `packages/ui/src/components.css`
- **Rules:** UX-41, UX-17, UX-18, UX-11, UX-58.
- **Guard:** `packages/ui/src/components/Viz.test.tsx`.

## 53. Sparkline

- **Used for:** a small trend in a table cell or a facts row.
- **Looks:** one line with no axes; the last value can be marked with a dot. It always has its label and the last, lowest and highest value as text. No values say "no data yet".
- **Source:** `packages/ui/src/components/Charts.tsx` (`Sparkline`), `packages/ui/src/components.css`
- **Rules:** UX-41, UX-17, UX-11.
- **Guard:** `packages/ui/src/components/Viz.test.tsx`.

## 54. Heatmap

- **Used for:** a weekday by hour grid of counts or seconds ("busiest hours").
- **Looks:** seven rows of 24 cells in five steps of one colour; the highest step has an outline. It fits 360 px without scrolling. A table under "Show as table" gives each day's total and busiest hour.
- **Source:** `packages/ui/src/components/Charts.tsx` (`Heatmap`), `packages/ui/src/components.css`
- **Rules:** UX-41, UX-17, UX-18, UX-49, UX-58.
- **Guard:** `packages/ui/src/components/Viz.test.tsx`.

## 55. Forecast chart

- **Used for:** how good a forecast was: one dot per finished job, forecast across and actual up.
- **Looks:** a dashed diagonal for "forecast = actual"; a bracket with end caps at the height of each dot for its range. A dot inside its range is round, one outside is a diamond. A table under "Show as table" says each job in words.
- **Source:** `packages/ui/src/components/Charts.tsx` (`ForecastChart`), `packages/ui/src/components.css`
- **Rules:** UX-41, UX-17, UX-18, UX-58.
- **Guard:** `packages/ui/src/components/Viz.test.tsx`.

## 56. Short text

- **Used for:** a long name in a table cell, such as a branch (`dependabot/npm_and_yarn/vite-8.3.8`), that would otherwise wrap over many lines.
- **Looks:** the name cut in the middle with an ellipsis, so its start and its end both stay readable (`RES15-jobs…ection`), with a dotted underline when it is cut. The full name is the hover text and the text a screen reader reads; the detail of the item always shows it whole. A cut name carries its full text the three ways of component 62 (tooltip on hover and focus, tap on touch, screen-reader text). A name that fits is shown as it is. **A name that may be long and has no middle to cut (LOOK38, `ClampText`):** a machine or runner name in a table cell or a card title breaks anywhere to fit its box (`overflow-wrap: anywhere`) and stops at two lines; the whole name is the hover text. A card title is clamped the same way, and the grid keeps a two-line title off the card above it.
- **Source:** `packages/ui/src/components/ShortText.tsx`, `packages/ui/src/components.css`
- **Rules:** UX-49, UX-17, UX-72.
- **Guard:** `packages/ui/src/components/parts.test.tsx`.

## 57. Row action

- **Used for:** the one button at the end of a table row that opens the thing the row is: a run, a group (repository, machine, runner, runner type, job type) or a job.
- **Looks:** a normal button on one line, in a last column of fixed width (`--ds-row-action-width`), the same size in every row and every list. Its words come from the Row action words of content.md: *Open run*, *Open*, *Open job*; an action that acts on the row and opens no page (*Remove*, *Edit*, *Replace*, *Delete registration*, *Revoke*) is a press in the same column, with the same one-line word. A group's name is already in the row, so the button says only *Open*; a screen reader hears the word and the name (`Open: runner-1`). At phone width it fills the last line of the card.
- **Source:** `packages/ui/src/components/RowAction.tsx`, `packages/ui/src/components.css`
- **Rules:** UX-72, UX-16, UX-8.
- **Guard:** `packages/ui/src/components/parts.test.tsx`.

## 58. Stat tile

- **Used for:** one key number of a page with its change and its trend: the four tiles of the Jobs Overview (UX-75). Owner decisions D9, D10 of feature ci-run-insights.
- **Anatomy:** a card (the shared card, CORE-7) with, top to bottom: the label (*Waiting now*), the value in the large type role with its unit, the change line (an arrow glyph, the amount and *vs the previous 7 days*), a sparkline (component 53) and, when the tile leads somewhere, the whole tile is one link whose name says the target (*Open the jobs that wait now*). Tiles of one row share one width and one height.
- **States:** *normal*; *better* (`✓`, Done tone), *worse* (`◷`, Needs you tone; never the Problem tone for a number that is only slower) and *same* (no glyph tone), each a word, a glyph and a colour (UX-58); *no data yet* (the value is a dash, the change and the sparkline say *no data yet*); *loading* (the label stays, the value area says *Loading*); *failed* (a quiet line with the reason and a retry link, UX-4).
- **Tokens:** `--ds-color-surface`, `--ds-color-divider`, `--ds-color-text`, `--ds-color-text-muted`, the tone tokens `--ds-color-done`, `--ds-color-needs-you` with their tints, `--ds-color-note` for the sparkline line, `--ds-space-3`, `--ds-space-4`, `--ds-space-5`, `--ds-radius-2`, the type roles for a large value and a quiet label. No new token.
- **Keyboard and screen reader:** the tile is one tab stop; Enter opens its link. The sparkline is hidden from assistive technology; the tile says the same in words: *Waiting now: 3 jobs, up 2 against the previous 7 days, worse. Oldest 4 min.* (UX-17). The focus ring is the shared one (UX-17).
- **Phone (360, 393 px):** two tiles per row, equal width, row gap `--ds-space-3`; the label and the change line wrap (UX-72), the value never wraps and the unit goes under it only when it does not fit; a tile never gets narrower than 160 px. Four tiles make two rows, never one long column.
- **Looks:** the tiles sit in the card grid (component 8) above every other card.
- **Source:** `packages/ui/src/components/StatTile.tsx` (`StatTile`, `StatGrid`), `packages/ui/src/components.css` (built in LOOK41; the Jobs Overview uses it).
- **Rules:** UX-75, UX-52, UX-58, UX-17, UX-41, UX-16, CORE-2, CORE-7.
- **Guard:** `packages/ui/src/components/overview-parts.test.tsx`.

## 59. Ranked findings list

- **Used for:** the things crsp found that cost the pool time, best first: the *Needs you* strip of the Overview (top 3) and the to-do list of Run insights (D8, D16 of feature ci-run-insights).
- **Anatomy:** an ordered list (component 45). Each item: the rank number; the title in plain words (*Add one runner of type docker on pc-1*); the minutes saved a week (*about 40 min a week*, a number with its unit); the confidence word *sure*, *likely* or *a guess* as a badge (component 17) with a glyph; one line of the fix; a fold *How we know* (component 47) with the data behind it; the link *Show the jobs behind it*. In the full variant (Run insights only) the item also has the secondary button *Dismiss* (UX-52 keeps every command off the Overview; in the strip variant a finding has the link only and its own page holds Dismiss and Mark done).
- **States:** *to do* (default), *done*, *dismissed* (the three lists of Run insights are a segmented choice, component 12; a dismissed item is grey and says why, UX-70, and *Restore* brings it back); *empty* (*Nothing needs you now.* in the strip, content.md); *loading*; *failed*.
- **Confidence words:** *sure* (the data proves it: every case, no exception), *likely* (the data points to it), *a guess* (an estimate; it is said in the words and never drawn like a fact). Text first, colour second (UX-58, UX-76).
- **Tokens:** `--ds-color-divider`, `--ds-color-text`, `--ds-color-text-muted`, `--ds-color-note-tint` (likely), `--ds-color-needs-you-tint` (a guess), `--ds-color-done-tint` (sure), the matching `on-*-tint` tokens, `--ds-space-3`, `--ds-space-4`, `--ds-radius-1`. No new token.
- **Keyboard and screen reader:** Down and Up move through the items (UX-57); Tab reaches the fold, the link and *Dismiss* of the item in that order. A screen reader reads the rank, the title, the minutes, the confidence word and the fix as the item's name, in that order (*1 of 3: …, about 40 minutes a week, likely*). *Dismiss* names its finding (`Dismiss: Add one runner…`) and moves focus to the next item; the change is announced (*Dismissed. Restore*) in the polite live region.
- **Phone (360, 393 px):** each item is a card with the same lines in the same place (UX-72): rank and title, then minutes and confidence word on one line, then the fix, then the fold and the link, each a full-width 44 px target. The title wraps; it is never cut.
- **Looks:** no table: the item is a row of words, not columns (UX-25).
- **Source:** `packages/ui/src/components/FindingList.tsx` (`FindingList`, the `strip` variant, built in LOOK41; the `full` variant with Dismiss stays as the Run insights table), `packages/ui/src/components.css`.
- **Rules:** UX-76, UX-52, UX-58, UX-57, UX-70, UX-17, UX-72, CORE-3, CORE-4, CORE-5.
- **Guard:** `packages/ui/src/components/overview-parts.test.tsx`.

## 60. Bar list

- **Used for:** a ranking of named things by one or two values: the *Busiest repositories* card (wait and run minutes, runs), where the time goes by part.
- **Anatomy:** one row per item: the label (a name, with the long-name rule of component 62), a horizontal bar on a shared scale, the value as text (*212 min*). **Every row links** to the filtered list behind it (UX-52); the row is one link and its name says the target (*Open the 124 jobs of repo-a*). A row of two values (wait and run) draws two segments in one bar, as in the stacked mode (component 63), with the values said in words. The scale starts at zero. A *Show as table* fold (component 47) lists every row (UX-17).
- **States:** *normal*; *hover and focus* (the shared hover and focus ring on the whole row); *empty* (*Nothing to rank in this range.*); *loading*; *no data yet*. The bar of a row never replaces its number.
- **Tokens:** `--ds-color-note` and `--ds-color-needs-you` for the two segments with a hatch on the second (a pattern as well as colour, UX-58), `--ds-color-divider` for the track, `--ds-color-text`, `--ds-color-text-muted`, `--ds-space-2`, `--ds-space-3`, `--ds-control-height` as the row's smallest height. No new token.
- **Keyboard and screen reader:** each row is one tab stop; Down and Up move through the rows (UX-57); Enter opens the list. A row reads *repo-a: 212 minutes, 124 jobs. Open the 124 jobs.* The bars are hidden from assistive technology; the table under *Show as table* is the text equivalent.
- **Phone (360, 393 px):** the label sits above its bar (the bar fills the row width), the value to the right of the label; the label wraps and never cuts (UX-72); rows are at least `--ds-target-min` high.
- **Looks:** a card body, never a bordered box inside a box.
- **Source:** `packages/ui/src/components/BarList.tsx` (`BarList`), `packages/ui/src/components.css` (built in LOOK41; one value a row, the two-segment bar and *Show as table* come with the stacked mode).
- **Rules:** UX-41, UX-17, UX-52, UX-57, UX-58, UX-72, UX-16, CORE-2, CORE-4.
- **Guard:** `packages/ui/src/components/overview-parts.test.tsx`.

## 61. Runner timeline

- **Used for:** what each runner did over time, so a wait can be seen next to a runner that stood idle. On Jobs it shows all machines; on each machine page it shows that machine only (D20 of feature ci-run-insights, UX-77).
- **Anatomy:** one lane per runner, a time axis shared by all lanes (the range is 24 hours or 7 days; the first and last time are named, UX-18). In a lane: a bar per job (a run is a solid bar; the wait before it is a lighter hatched segment, as in the Waterfall, component 50); a stretch where the runner was **idle while a job queued** is a dashed outline with the words *idle while queued* in its legend and its text; "now" is the dashed line of the Waterfall. The lane label is the runner name (component 62 for a long name) with its machine as a muted second line (hidden on a machine page). A bar that opens a job is a button. The heat of a busy hour is read from the Heatmap (component 54): the part reuses its colour steps for a busy-percent strip under the lanes, not a new scale.
- **States:** *live* (the right edge is now and moves); *a past range*; *a runner with no job in the range* (the lane says *no jobs*); *empty* (*No runner has worked in this range.*); *loading*; *failed*; *filtered to one machine*.
- **Tokens:** `--ds-color-note` (run), `--ds-color-needs-you` (wait, hatched), `--ds-color-problem` is not used; `--ds-color-border` for the dashed idle box, `--ds-color-divider` for the axis and lane lines, `--ds-color-text`, `--ds-color-text-muted`, `--ds-space-1`, `--ds-space-2`, `--ds-space-3`, `--ds-control-height` as a lane's height. No new token.
- **Keyboard and screen reader:** the lanes are one list; Down and Up move between runners, Left and Right between the bars of a lane in time order, Enter opens the job (UX-57). Each bar names itself (*build, repo-a, waited 2 min, ran 5 min, on runner-1*). *Show as table* lists every bar and every idle stretch in words (UX-17); *idle while queued* is said in the table, not by the pattern alone.
- **Phone (360, 393 px):** the lane names do not fit beside the bars, so each lane is two rows: the name above, the bars below at the full width (never hidden: the study's lane names hidden at 393 px are not allowed, CORE-3). The range is 24 hours; a long range is reached by *Show as table*. A bar is at least `--ds-target-min` high.
- **Looks:** the card has the legend (run, wait, idle while queued, now) above the lanes.
- **Source:** `packages/ui/src/components/RunnerTimeline.tsx` (`RunnerTimeline`), `packages/ui/src/components.css` (to be built; the apps do not follow this yet; data from the slot log).
- **Rules:** UX-77, UX-41, UX-17, UX-18, UX-57, UX-58, UX-49, UX-72, CORE-1, CORE-2.
- **Guard:** `packages/ui/src/components/Viz.test.tsx` (to be written with the part).

## 62. Cell full-text

- **Used for:** a table cell, card title or list line whose text may be longer than its box. It is the way UX-72 (D14, D15) makes the full text reachable.
- **Anatomy:** by default the text is whole: it wraps and a long path, id or word with no space breaks anywhere to fit. Only where the box has a line limit (a name at two lines, `ClampText`, component 56) or a very narrow column does the text end in an ellipsis. A cell that ends in an ellipsis is a *cut cell*, and a cut cell always carries the full text three ways: **a tooltip** with the whole text on mouse hover and on keyboard focus (the cell is focusable with `tabindex="0"` only when it is cut); **a tap** on touch expands the row to show the cell whole, and a second tap folds it; and **the text itself** for a screen reader (the whole text is in the DOM, the cut is drawn by the style). A cell that is not cut has none of these. Rows may differ in height.
- **States:** *whole* (not cut); *cut* (ellipsis, a dotted underline, focusable); *tooltip shown* (hover or focus; Escape closes it, UX-57); *expanded* (touch; the row grows, the cell shows whole text, `aria-expanded` is true); *folded*. The middle cut of a branch (`ShortText`, component 56) is a cut cell too and follows the same three ways.
- **Tokens:** `--ds-color-surface` and `--ds-color-border` for the tooltip, `--ds-color-text`, `--ds-space-2`, `--ds-radius-2`, `--ds-z-menu` for the tooltip layer, `--ds-color-focus`. No new token.
- **Keyboard and screen reader:** Tab reaches only cut cells; the tooltip opens on focus and on hover and stays while the pointer is over it (it is readable and can be dismissed with Escape, WCAG 1.4.13); Enter or Space on a cut cell expands the row as a tap does. The full text is the cell's accessible text, so a screen reader reads it whole, never the ellipsis. A tooltip is not the only way: on touch the tap is, and the row's detail page always shows the text whole.
- **Phone (360, 393 px):** no hover exists, so the tap is the way; the expanded row pushes the rows below it down, never over them; the tap target is the whole cell, at least `--ds-target-min` high. A long path or id breaks anywhere and needs no tap while it fits in two lines.
- **Looks:** a cut cell is told from a whole one by the dotted underline and the ellipsis only; nothing else changes.
- **Source:** `packages/ui/src/components/CellText.tsx` (`CellText`), `packages/ui/src/components/ShortText.tsx`, `packages/ui/src/components.css` (to be built; the apps do not follow this yet).
- **Rules:** UX-72, UX-17, UX-49, UX-57, UX-16, CORE-3, CORE-4.
- **Guard:** `packages/ui/src/components/parts.test.tsx` and the `cutText` guard of `e2e/tests/web-console/ux-routes.spec.ts`, which asks that every cut cell has a tooltip and a tap (to be written with the part).

## 63. Stacked mode of the trend chart

- **Used for:** two or more parts of one total over time, such as wait and run time per day (*Where the time goes*) or the wait parts (GitHub, queue, slot hold and start, cleanup). It extends the Trend chart (component 52); it is not a new chart.
- **Anatomy:** the Trend chart with the mode `stacked`: columns, one per day or hour, stacked from zero in a fixed part order, the same on every column; a legend names the parts and the total. A column opens the filtered list of its day (*Open the 124 jobs of that day*, UX-52). The top and bottom values and the first and last time are named as in component 52. *Show as table* lists every part of every column (UX-17).
- **States:** as component 52 (*no data yet*, *loading*, *failed*) plus *a column picked* (the selected day and its list under the chart, as in the Charts tab).
- **Tokens:** the same as component 52: `--ds-color-needs-you` (wait, with a hatch) and `--ds-color-note` (run) for two parts; a third and fourth part use `--ds-color-done` and `--ds-color-text-muted`. Parts differ by pattern and by the stacking order as well as by colour (UX-58). The chart colours must pass the contrast check of the gallery in light and dark (see the open points of the LOOK40 report). No new token.
- **Keyboard and screen reader:** each column is a tab stop in time order; Left and Right move between columns; Enter opens the list. A column reads *Tue 18 Nov: waiting 46 min, running 62 min, 124 jobs*. The table is the text equivalent.
- **Phone (360, 393 px):** it fits 360 px without scrolling: at most 14 columns, labels on every other column; a column is at least 20 px wide and `--ds-target-min` high to press.
- **Source:** `packages/ui/src/components/Charts.tsx` (`TrendChart`, `mode="stacked"`), `packages/ui/src/components.css` (to be built; the apps do not follow this yet).
- **Rules:** UX-41, UX-17, UX-18, UX-52, UX-58, UX-16.
- **Guard:** `packages/ui/src/components/Viz.test.tsx` (to be written with the mode).

## 64. Job path

- **Used for:** the picture of how a job gets run, on the Help page and on the first-run Overview (LOOK44, owner pick I, D57). It shows the parts a job passes through, in order: Repository, Run, Job (*On GitHub*) and Queue, Slot, Runner, Machine (*In your pool*).
- **Anatomy:** a list of two framed groups with a label each (*On GitHub*, *In your pool*), each holding numbered boxes in order: the number, the part's name in bold and one short muted line: a live example from real data when the page has one (*octo/app*, *2 waiting*), otherwise the meaning (*one start of a workflow*). An example stays on one line; a long one is cut with … and its full text is the hover text (LOOK48). An arrow sits between two boxes. The boxes are HTML, not a drawing, so the words wrap and a screen reader reads them (UX-17).
- **States:** *read only* (the first-run Overview: the boxes are plain boxes); *with a choice* (on Help each box is a button that scrolls to the question card of its part; nothing is locked); *lit* (a part the page lights has the selection fill and the primary border; the rest stay plain), told by the fill and by `aria-pressed`, never by colour alone (UX-58).
- **Tokens:** `--ds-color-surface`, `--ds-color-border`, `--ds-color-divider`, `--ds-color-primary`, `--ds-color-selection`, `--ds-color-on-primary`, `--ds-color-text`, `--ds-color-text-muted`, `--ds-space-1` to `--ds-space-5`, `--ds-radius-2`, `--ds-radius-3`, `--ds-target-min`. No new token.
- **Keyboard and screen reader:** each box is one tab stop; Down, Up, `j` and `k` move through them in page order (UX-57); Enter or Space presses. A box is named by its visible words (*Slot room for one job*), so its name holds its visible text. The list is named *How a job gets run*.
- **Phone (360, 393 px):** the groups stack and the boxes run top to bottom with a down arrow between them; a box is at least `--ds-target-min` high and its words wrap, never cut (UX-72).
- **Source:** `packages/ui/src/components/Help.tsx` (`JobPath`), `packages/ui/src/components.css`
- **Rules:** UX-41, UX-17, UX-57, UX-58, UX-72, UX-16, CORE-2, CORE-5.
- **Guard:** `packages/ui/src/components/Help.test.tsx`, `e2e/tests/web-console/help.spec.ts` and the DOM layout checks of `ux-routes.spec.ts` at 360, 393 and 1300 px.

## 65. Stepper (removed)

Removed in LOOK47 (owner decision 21 Nov 2026, D58): Help is no wizard, so nothing uses a read-only step bar any more. The number stays free; the wizard's own step bar (component 4) is unchanged.

## 66. Term help

- **Used for:** the ? beside a word the user may not know (*Slots*, *Load budget*, *Queue*, *Runner type*, *Run elsewhere*): a short answer where the word is, and a link to the same card on the Help page (LOOK44, D57).
- **Anatomy:** a small button with the sign ? and the name *What does {term} mean?*, placed after the word. It opens a popover (component 31) with the term in bold, one or two short sentences and the link *More about this in Help*. It is not a command: the screen's primary-button check and the rule of no command buttons on the Overview (UX-52) leave it out (`data-term-help`).
- **States:** *closed* (`aria-expanded="false"`), *open*. Escape and a press outside close it; Escape puts focus back on the ? (LOOK30).
- **Tokens:** `--ds-color-primary`, `--ds-color-surface`, `--ds-color-border`, `--ds-space-2`, `--ds-space-3`, `--ds-space-4`, `--ds-radius-3`, `--ds-target-min`, `--ds-z-menu`. No new token.
- **Keyboard and screen reader:** one tab stop; Enter or Space opens; the open answer is a group named by the term (UX-43, UX-44).
- **Phone (360, 393 px):** the answer is a sheet at the bottom of the screen, full width (UX-43); the button is `--ds-target-min` square (UX-16).
- **Source:** `packages/ui/src/components/Help.tsx` (`TermHelp`), `packages/ui/src/components/Popover.tsx`, `packages/ui/src/components.css`
- **Rules:** UX-43, UX-44, UX-46, UX-47, UX-16, UX-17, UX-57, CORE-3.
- **Guard:** `apps/web/src/screens/help/Help.test.tsx`, `e2e/tests/web-console/help.spec.ts`, and the `primary` guard of `e2e/tests/web-console/ux-guards.ts`.

## 67. Search table

- **Used for:** the one list of the Jobs section (Live, Done, Run elsewhere) and any later list that has a search, a state filter and a panel of details (owner decision of 20 Nov 2026, option 11 of CRSP211; feature ci-run-insights).
- **Anatomy:** one list toolbar (component 11) above one table (component 15). In the toolbar, in this order: the search box (component 21), the state filter as a segmented choice (component 12: *All*, *Queue*, *Running*, *Done*), the page's other choices, a short count, and at the end the **details button**: an icon button with the panel-right icon (a box with a bar at its right) named *Show details* (*Hide details* while the panel is open) by its `aria-label` and its tooltip. The button opens a **details panel** at the right of the table with the full information of the chosen row: a title, *Close details*, and the facts (component 8) the page passes. The chosen row is marked (`aria-current`); the first row is chosen until another is pressed or reached with the keyboard. The page gives the rows, the columns (component 15), what a row says in words for the search, and what its details show; it draws nothing itself. **The search** is the one of option 9: free words match any text of a row; `repo:`, `state:`, `runner:`, `branch:` and `job:` narrow one field; every word must match. One list shows waiting rows first, then running rows, then the latest finished rows.
- **States:** *panel closed* (default); *panel open*; *no match* (*Nothing matches. Clear the search or choose All.*, or the page's own words when the filter holds nothing); *loading*; *failed* (component 19).
- **Keyboard and screen reader:** focus starts in the search box when the page opens (the first act on a list is to search). Down, Up, `j` and `k` move through every control and every row in page order (UX-57; `j` and `k` type in the box); the target may be an SVG icon, so the movement reads the focus from the nearest control. Enter opens the row; Escape in the panel closes it and returns focus to the details button. The panel is a landmark named *Details: {row}*; the button is a toggle (`aria-pressed`).
- **Phone (360, 393 px):** the panel is a sheet fixed to the bottom of the screen, at most 72 per cent of its height, scrollable, with *Close details* in its first line (UX-43); the table keeps its cards (UX-49).
- **Looks:** the toolbar is one row at 1300 px; the panel is one bordered box of the width of a form field and a half; the chosen row has a primary outline.
- **Source:** `packages/ui/src/components/SearchTable.tsx` (`SearchTable`, `parseSearch`, `matchesSearch`), `packages/ui/src/components/Table.tsx` (`current`, `onSelect`), `packages/ui/src/components.css`.
- **Rules:** UX-31, UX-57, UX-43, UX-72, UX-74, UX-49, CORE-1, CORE-2, CORE-4.
- **Guard:** `packages/ui/src/components/SearchTable.test.tsx`, `e2e/tests/web-console/jobs-doctor.spec.ts`, `e2e/tests/web-console/ux-keyboard.spec.ts`.

## 68. Date and time

- **Used for:** one moment shown as text: the date, a space, the time (content.md, Dates, times and numbers), in a `time` element that carries the instant. It never breaks across lines, at any width, in a table, a card or a sentence. Shared part: `DateTime` of `@crsp/ui`. A page never writes a bare formatted string where a moment is shown as a cell or a line of its own.
- **Rules:** UX-78, UX-72.

## 69. Help question card

- **Used for:** a question card of the Help page and any later card that explains one box of the job path (LOOK48, owner decision 21 Nov 2026, D58 proposal G).
- **Anatomy:** a card (component 8) whose first line is a **box badge**: the numbered circle and *Box N: Name* (*Box 5: Slot*), a link to that box in the job path. Then the answer and, last, one **small picture** drawn in the theme's own colours with the shared pictures `PictureShared`, `PictureRun`, `PictureLabels`, `PictureElsewhere`, `PictureQueue`, `PictureSlots` (*N of M slots busy*), `PictureBudget` (memory and CPU bars against the budget line), `PictureRunnerType`, `PictureTray` and `PictureDoctor`. The page passes the numbers and names it has from real data; a picture with no data draws a short example with no invented number. The card grid takes `columns={4}`: 4 columns on a wide screen, 2 on a tablet, 1 on a phone.
- **States:** *badge pressed* (the page lights the box, as in component 64's *lit*); a picture with data, or an example when there is none.
- **Tokens:** `--ds-color-surface`, `--ds-color-border`, `--ds-color-primary`, `--ds-color-text`, `--ds-color-text-muted`, `--ds-color-needs-you`, `--ds-color-needs-you-tint`, `--ds-color-on-primary`, `--ds-space-2`, `--ds-space-5`, `--ds-target-min`. No new token.
- **Keyboard and screen reader:** the badge is one tab stop; Enter presses it. Each picture is an image named by words that hold its numbers (*build-1: 2 of 4 slots busy.*) (UX-17, UX-41).
- **Phone (360, 393 px):** one column; a picture is at most 20 rem wide and scales to the card; the badge is `--ds-target-min` high.
- **Source:** `packages/ui/src/components/Help.tsx` (`HelpQuestionCard`), `packages/ui/src/components/HelpPictures.tsx`, `packages/ui/src/components/Card.tsx` (`CardGrid columns`), `packages/ui/src/components.css`
- **Rules:** UX-41, UX-10, UX-51, UX-17, UX-16, UX-72, CORE-1, CORE-5.
- **Guard:** `packages/ui/src/components/Help.test.tsx`, `apps/web/src/screens/help/Help.test.tsx`, `e2e/tests/web-console/help.spec.ts` and the DOM layout checks of `ux-routes.spec.ts` at 360, 393 and 1300 px.

## How to add a component

- Give it the next number.
- Write the five lines: Used for, Looks, Source, Rules, Guard.
- Looks says in words what it looks like. It holds no values: a token name or a size belongs in tokens.md.
- Cite the UX rules it follows by ID.
- Add a `CHANGES.md` entry.
