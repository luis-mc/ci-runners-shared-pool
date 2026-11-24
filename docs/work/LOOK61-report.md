# LOOK61 report: Overview as approved

Branch `LOOK61-overview-as-approved` (from `origin/LOOK59-look54-contract`, `origin/main` merged: already up to date).
Followed: design system v4 (the highest folder); words and the CHANGES entry C37 went into `next-version`.
Checked: CORE-1 to CORE-7; UX-10 (every titled part is a pane), UX-52 (no command on the Overview), UX-58 (a state is a word, a glyph and a colour), UX-16 (phone widths).
Shared parts used: Page, PageHeader, StatGrid/StatTile (via `PoolCountTiles`), Message, Section, Card, CardGrid, LevelBar, Status, Tag, Code, Text, Strong, Cluster, Stack, TextLink, Timeline (via `SlotTimeline`). No app CSS, no `style=`.

Screenshots at 1278x1248, live vs. prototype: `docs/work/LOOK61-shots/overview-live.png` / `overview-proto.png`.
(Live shot uses the e2e mock API with 3 machines, 2 running jobs, 4 queued jobs, one job in its timer grace; the stream is not connected, so the header says "updates paused".)

## Gaps (gaps.json id `overview`)

| Gap | Built | Shots |
|---|---|---|
| Section order | Counts, then timer alert, then Best next change as one line (title, saving, link on the right at `Open it in Analytics > Suggestions`), then machine cards, Next to start and Routing labels, then Jobs over time. The large pane above the tiles is gone. Timer alert: for each running job `GET /jobs/{id}/timer`; a job in `grace` gives a problem message with the server's words and `Open {job} in Pool > Jobs`. | overview-live / overview-proto |
| Machine cards | Title `{name} {cpus} CPU / {GiB} GiB` from `facts`; CPU and Memory bars as cores and GiB in use of the total (percent fallback while facts are missing); running job names; link `Open in Pool > Machines` (`/pool/machines/{id}`). The Slots bar and the always-on `online` mark are gone (a state word shows only when the machine is not plainly online, UX-58). | same |
| Missing panes | `Next to start` (first 3 queued jobs in start order, a word for why, the clock time, link to Queue Analysis with the count) and `Routing labels` (first 5 labels in use as tags, link with the total). The pane `Jobs over time` (the shared slot timeline, one row per slot, owner decision 22 Nov) sits where the prototype's `Jobs over the next 30 minutes` is. `Queue and running jobs` and `Why jobs wait` are removed (the prototype has neither; both live in Pool). | same |
| Subtitle | `The pool at a glance. Open a card for the detail.` | same |

Tests: `Overview.test.tsx` (order, card in cores and GiB, percent fallback, Next to start, Routing labels, timer alert), `OverviewPanes.test.tsx` (helpers), `OverviewLine.test.tsx`; DOM layout spec gets `overview with sizes, a queue and labels` (360, 393, 1300 px, passes with the existing `overview` entry). Docs: web-console R5.20 rewritten, change note `changes/LOOK61.md`, content.md rows, CHANGES C37.

## Not matched, and why

- **Tile sublines** (`1 timer ran out`, `1 reserved, 2 for room`): gap kind data-only, and `StatTile` has no subline in the shared UI package (not in my screens). Not built.
- **Tag `sure` on Best next change**: the suggestion has no confidence field in the data (findings also still come from the mock in `insights/api.ts`). Not shown rather than invented.
- **`Reserved` word in Next to start and `about 14:16` wording**: a job has no reserved flag; the time reads `starts about 14:16` from the queue estimate, `start not known` without one.
- **Card title with the size as a count** (`GameRoomPC` + `6 CPU / 9 GiB` apart): `Pane` `count` takes a number only; the size is in the title text. A string count needs a shared-UI change (outside my screens).
- **Next to start and Routing labels side by side at half width each, three machine cards filling the row**: `CardGrid` sets the column count itself, so the cards are narrower than in the prototype. Needs a shared-UI change.
- **Jobs over time** shows the console's 24 h past and forecast window, not 30 minutes (later-decision, LOOK58).
- A header `live updates` word is the shell's, not mine.
- `Updates.test.tsx` "names the running jobs" failed once in a full parallel run and passes alone and on the base: load flake, not touched.
- The Overview no longer has the `?` help of `Queued jobs` (that fact is gone with the old section). `help.spec.ts` (Escape closes the answer) now uses the Slots `?` on the machine page; `perf.spec.ts` reads the Queued tile. The shared `StatTile` has `changeText` for the sublines, but `PoolCountTiles` is in Pool (not my screen) and does not pass it.
