# CRSP338: UX review of Setup > Pool setup and the "Turn on the pool" wizard

Scope: the whole screen (`apps/web/src/screens/repositories/Repositories.tsx`, route `/setup/pool`) and its wizard (`TurnOnPool.tsx`, route `/setup/pool/turn-on`), plus the turn-off question card. Design system followed: v4 (highest numbered). Rules cited by ID: CORE-1..7 and UX-n from `docs/design-system/v4/ux-rules.md`.

The root cause of most findings: the page mixes three jobs in one list. It is a **status list** (is this repository using the pool?), a **selection list** (ticks) and a **command screen** (one button for two opposite commands). The user's mental model is simpler: "this repository uses the pool, or it does not, and I change that."

## Findings

### F1. One bottom button changes its meaning with the ticks (the owner's point 1)
- Rules broken: **UX-4** (one primary, labelled by what it does with its count; disabled with what to do), **CORE-4** (an action takes exactly what you see; nothing to decode), **CORE-5** (assertive: say what it does), **UX-38** (blocked says why).
- Evidence: `Use pool` / `Stop using pool` flip on `allUse`. With a mix (3 ticked, 2 on, 1 off) the label says `Use pool` and the 2 repositories already on are sent to the wizard too. With none: the bar says `Nothing selected` and the button `Use pool` is disabled.
- Mental model: "A tick on a row that already uses the pool means *keep it*, or *stop it*?" The user cannot tell what the button will do to each row, and the label has no count ("Use pool" vs "Turn on 2").
- Direction: label by action and count (`Turn on 2`, `Turn off 1`), or remove the global command (per-row switch, or one setup button).

### F2. A tick means two things, and a group tick is ambiguous (point 1)
- Rules broken: **CORE-1** (same thing looks the same), **UX-22** (right control: a switch saves at once, a check box waits for Save), **UX-12** (Select all N and Clear), **UX-7** (the count is what the user can see).
- Evidence: the row tick is "choose this row for the next command", while the next column says `pool on`. The group line `luis-mc 8 repositories, 4 use the pool` has its own tick, which can only be all or none; it has no mixed state and ticks rows hidden by search, filter or page 2 when the group is ticked. `Select all N` counts the filtered list, but `picked` survives a filter change, so a ticked row can be out of sight and still counted (`chosen`).
- Mental model: "The tick is the state" (a tick on a row that is on) versus "the tick is a selection". The page has no word that says which.

### F3. The toolbar has too many controls of the same weight (point 1)
- Rules broken: **CORE-3** (clear), **UX-14** (core content first), **UX-31** (search bar, then chips, then content, one place), **CORE-5**.
- Evidence: in one toolbar row: search, `Pool on` / `Pool off`, `Types: private`, `Select all 12`, `Clear`, a count, `Add a repository`. Eight controls before the first row. Two of them (`Pool on`, `Pool off`) are filters on the thing the page is about; the page is really two lists in one.
- Mental model: "Show me what uses the pool, and what does not."

### F4. The wizard asks for the repositories again (point 2)
- Rules broken: **CORE-4** (no redundant steps; an action takes exactly what you see), **UX-7** (never act on hidden items), **CORE-1**, **UX-5 / CORE-6** (no dead step).
- Evidence: `?repo=` carries the ticked ids, but step 1 `Repositories` is still shown with a table of its own: pool-off repositories only, public ones left out with a note (`n public repositories are not listed`), ones without admin rights left out with a second note. A ticked row that is not eligible silently disappears, so the number the user ticked is not the number step 1 starts from. The step's primary says `Next`, not what it does.
- Mental model: "I already told you which ones. Why am I choosing again?" Then: "Where did my third repository go?"
- Direction: the page owns the pick, the wizard starts at Review (or the wizard owns the pick and the page has no ticks; never both).

### F5. The page and the wizard word the same limits differently (points 1, 2)
- Rules broken: **CORE-1**, **CORE-3** (plain words), the retired-words list in `content.md`.
- Evidence: the page says `the pool serves private repositories only. List them too`; the wizard says `public repositories are off. Turn them on in Settings`. Four verbs for one act: `Use pool`, `Turn on the pool`, `pool on`, `Turn on N repositories`; and `Stop using pool`, `Turn off these repositories?`, `pool off`, `Turning off`.
- Mental model: one verb pair, "turn on" and "turn off", used the same everywhere (content.md already has `Turn off`).

### F6. The Review is developer text (point 3)
- Rules broken: **UX-30** (Review shows what will happen in numbers and words), **CORE-3** (never raw codes as the main text), **UX-3**, **CORE-5**.
- Evidence: `BulletList` of the plan steps: `Write variable CRSP_RUNS_ON_LINUX of luis-mc/moobable = [...]`, five lines per repository, 20 lines for 4 repositories, `(needs your yes)` in brackets. The line "N repositories will use the pool" is followed by no list of which repositories.
- Mental model: "Which repositories, and what changes for me?" The five steps are one fact: the repository starts using the pool.
- Direction: list repositories (name, effect, one `needs your yes` flag where true) and fold the technical steps away in a `Disclosure` (component 47).

### F7. The work step counts steps, not repositories, and says `planned` 20 times (point 4)
- Rules broken: **UX-29** (a wait names what is happening, bar fills; a count never sits still), **UX-30** (the work step shows live progress in the user's units), **UX-58** (a status is a word, a glyph and a colour; the shared words are ok, waiting, running, failed), **UX-72** (no text wraps into a stray line), **CORE-3**.
- Evidence: `Turning on 1 of 20` counts plan steps (`Math.min(done + 1, total)` shows `1 of 20` before anything is done). The bar is 1/20 of its width per step. Every line is a `Facts` row with the raw plan state as its value (`planned`, `running`, `done`). The word `planned` is not in the status vocabulary and is too wide for its cell, so it wraps onto its own line.
- Mental model: "4 repositories, 1 of 4 done." The 20 internal steps are noise.
- Direction: count repositories; one calm list with the shared `Status` word per repository; steps folded away.

### F8. Turn off is a different pattern from turn on (point 5)
- Rules broken: **CORE-1** (same action, same wizard), **UX-30** (a wizard that changes something ends with Review, the work, Done, "no exceptions"), **UX-13** (a busy word only while it runs), **UX-62** (names what goes, asks for a yes), **UX-9**, **CORE-3**.
- Evidence: turning on is a four-step wizard; turning off is a modal question card `Turn off these repositories?` listing `owner/name: pool off` and `owner/name: variable X` lines. The confirm button is red (kind `danger`) and its busy word `Turning off` is the wording the user sees first on a small card; there is no Done step with a result, the list just reloads. UX-62 asks for a question card for a destructive step and UX-30 says a changing wizard has no exception: the two rules pull in different directions, and the page follows UX-62 only.
- Mental model: "Turn off is the opposite of turn on, so it should look like it, and tell me what happened."
- Direction: both go through the same wizard (Review, Turning off, Done), with the question card's list of what goes shown in Review. Red only on the button that deletes.

### F9. Selected, focused and ticked look alike (point 6)
- Rules broken: **UX-58 / UX-26** (colour never stands alone), **UX-51** (a state is said in words, never a bare colour frame), **CORE-1**, **UX-17** (focus visible but distinct).
- Evidence (`components.css` ~941): `tr[data-list-row]:focus-within` (a click lands focus in the row) paints the full-width primary fill (reverse video, the keyboard cursor of UX-57); a ticked row `tr[data-selected]` paints `--ds-color-selection`, another green. The click flash shows the bright cursor, and after the click the quieter tick colour stays. Both are green and both are called "selected" in the code comment.
- Mental model: "Bright green means I clicked it; dark green means I ticked it" is learned by accident, and a mouse user never meant a cursor.
- Direction: a ticked row is said in words and a mark (the check box, `Selected` text on a phone), the cursor is a ring or a left bar, not a fill; or no ticks at all (options 1, 3).

### F10. A failing repository is a muted grey line (every point)
- Rules broken: **UX-6** (no dead ends: offer the next step), **UX-58**, **CORE-5**.
- Evidence: a repository with a missing or wrong variable shows `wrong variable` inside the muted second line of its name cell (`5 parallel · priority 10 · wrong variable`). There is no failure mark in the row and no `Repair` action. The one thing that needs the user is the least visible.
- Mental model: "Is it working or not?" The Pool column says `pool on`, which reads as fine.

### F11. The page buries the health of the pool, and mixes commands into a list page (point 1)
- Rules broken: **UX-14** (the core content first), **UX-52** (a page that shows and links has no command buttons; each command lives in one place), **CORE-3** (one headline says where you are).
- Evidence: under the list sit the `GitHub App`, `OrgPools`, `Runs-on PR` panes and the permissions guide. There is no sign that the pool has machines and runners, which is what decides whether `Turn on` will work. The reader must scroll to learn the state of the App.
- Mental model: "Is the pool ready? Which repositories use it? Change that."

### F12. Hidden and blocked repositories have no way back on the page (points 1, 2)
- Rules broken: **UX-7**, **UX-6**, **UX-70** (an inactive row says why).
- Evidence: public repositories are hidden behind a note and a `List them too` link that adds a query parameter; repositories without admin rights are excluded in the wizard but only named in the disabled reason of the global button.
- Mental model: "Why can I not pick this one, and what do I do?"

### F13. On a phone every row is seven lines tall and the tick is far from the name
- Rules broken: **UX-16** (works from 360 px), **UX-25** (on a phone a row folds to two lines), **UX-14**, **CORE-4**.
- Evidence (393 px screenshot `shots/0-page-selected-393.png`): the table folds each repository into a stack: a lone tick, the name, a muted line, then four labelled lines (Pool, Runner type, Fallback label, Jobs now). Eight repositories take about 2000 px; `Use pool` is far below the rows it acts on, and the click colour (F9) is the whole stacked block.
- Mental model: "a list of names I can switch", not "a record per repository".

## What already works (keep)
- One shared wizard part owns the step bar, Stop and Done (UX-54); the plan survives a reload (`?plan=`).
- The `Nothing has changed yet.` line on Review (UX-30).
- The table folds on a phone and the page does not scroll sideways.

Rule tension to decide: UX-22 says a switch is saved at once and a check box waits for Save; UX-4 says one primary per screen; UX-30 says a changing wizard has no exception while UX-62 asks for a question card on a destructive step. Options 1 (staged switches), 4 (two actions) and 8's turn-off wizard each need one of these rules clarified in `next-version` first.

## Options built from these findings
Six options on `?option=1..6` (see `index.html`). Each answers the findings as follows.

| Option | F1 | F2 | F3 | F4 | F5 | F6 | F7 | F8 | F9 | F10 | F11 | F12 |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 1 Switch per repository, changes staged | yes | yes | part | yes | part | yes | part | yes | yes | part | no | no |
| 2 Two lists, Add and Remove | yes | yes | yes | yes | part | yes | part | yes | yes | part | no | part |
| 3 Status and one setup button | yes | yes | yes | yes | yes | yes | yes | yes | yes | part | part | yes |
| 4 Bulk bar with both actions | yes | part | no | part | part | yes | yes | yes | part | no | no | part |
| 5 Status first | yes | yes | yes | yes | yes | yes | yes | yes | yes | yes | yes | yes |
| 6 Plain review and calm progress | part | no | no | part | yes | yes | yes | yes | no | no | no | no |
