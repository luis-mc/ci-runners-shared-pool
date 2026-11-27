# Design-system enforcement kit (TypeScript and Vitest)

Copied into `scripts/design/` by `design_system.py guards --stack typescript-vitest-kit --apply`.

**Stacks: TypeScript with Vitest only, for now (owner decision D243).** There are no templates for other stacks. A project on another stack ports the same checks by hand, or waits.

What is here:

- `no-app-css-scan.ts`, `no-app-css.test.ts`, `no-app-css.known.json`: no stylesheet, CSS-in-JS, layout `style=` or unknown class in app folders.
- `dom-layout.ts`, `dom-layout-judge.test.ts`, `dom-layout-example.spec.ts`, `layout.known.json`: layout rules at 360, 393 and 1300 px, measured in the DOM (Playwright).
- `wizard-behaviour.ts`, `wizard-behaviour.test.ts`, `wizard-behaviour.known.json`: every wizard is driven through ten required states (loading, first run, planning, ready to review, running, paused, stopped, failed, done, done after reload). A project may add states. It never drops one; the harness fails a driver that lists fewer.
- `raw-controls-scan.ts`, `raw-controls.test.ts`, `raw-controls.known.json`: no raw `<button>`, `<input>`, `<select>` or `<textarea>` in app code; apps use the shared UI components. Test files and the shared UI folders are not judged. How to install: copy the three files to `scripts/design/` (the `guards` command does), set `CONFIG` in `raw-controls-scan.ts` (app paths, shared UI paths, file globs), and run the test with Vitest.
- `raw-inputs-scan.ts`, `raw-inputs.test.ts`, `raw-inputs.known.json`: no native date, time or number input (`type="date"`, `"time"`, `"datetime-local"` or `"number"`) in the app or shared UI folders (design system UX-56). Dates use the date picker, times use the time picker, and the number keypad is only for counts, measures and codes (`design_system.py components`). Test files are not judged. How to install: copy the three files to `scripts/design/` (the `guards` command does), set `CONFIG` in `raw-inputs-scan.ts` (scanned paths, file globs), and run the test with Vitest.
- `contrast.ts`, `contrast.test.ts`, `contrast.known.json`: every text-on-fill token pair (`--ds-color-X` with `--ds-color-on-X`) meets WCAG 2.2 AA (4.5:1) in the light theme, the dark theme and any `[data-theme=NAME]` theme. A theme inherits what it does not set from `:root`. A translucent colour is reported as "cannot judge". How to install: copy the three files to `scripts/design/` (the `guards` command does), set `CONFIG` in `contrast.ts` (token file path, token prefix), and run the test with Vitest.

Known-breach lists (the `*.known.json` files, all read by the kit's tests) only shrink. A diff that adds an entry or raises a count fails `change_notes.py check`, except the change that first installs the list. No screenshot comparison anywhere.

## Console guard specs (Playwright, `e2e/tests/web-console/`)

The kit's checks run on real screens through these specs (CRSP164). They read the route table, so a new screen is covered at once.

- `ux-routes.spec.ts`: every route at 360, 393 and 1300 px in the dark and the light mode: DOM layout (UX-10, UX-15; LOOK46 adds `section-gap`, top-level panes are `--ds-space-6` apart, and `title-clear`, no pane title under the focus outline or glow of a control above it), touch targets of 44 px (UX-16), one primary (UX-4), one h1 in the data, empty and failed states (UX-17), Select all N and Clear on tickable rows (UX-12), no sideways scroll in nav, menus and tab rows (UX-49), every section, list and table inside a Pane (UX-51).
- `ux-look.spec.ts`: a sample screen in each of the six themes, dark and light; the computed colours must equal the swatches of `docs/project/content/look/02-options.html` (UX-55, UX-60).
- `ux-keyboard.spec.ts`: Down and Up walk every control in order; the Ctrl+K palette starts in its search box (UX-57).
- `ux-static.spec.ts`: no `window.alert`, `confirm` or `prompt` in `apps/controller-console/src` or `packages/ui/src` (UX-9).

A case that main fails is a `test.fixme` through `ux-fixme.ts`, with the task that fixes the screen. Delete the line when that task has merged. `UX_NOFIXME=1` runs every case. The layout spec uses no known list: `layout.known.json` stays empty.
