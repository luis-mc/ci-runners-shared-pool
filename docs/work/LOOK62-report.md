# LOOK62: Pool > Jobs as approved (rides RELEASE-63)

Branch `LOOK62-pool-jobs-as-approved`, from `origin/LOOK59-look54-contract`, with `origin/main` merged (already up to date).
Screen: Pool > Jobs (`/pool/jobs`) and its inspector only. Shots at 1278x1248: `docs/work/LOOK62-shots/<page>-live.png` and `<page>-proto.png`.

## Gaps

| Gap | What was built | Shots |
|---|---|---|
| Default view is the Jobs list (Job, Status, Machine, Start, Why) with a Why link on every row | `useView` defaults to jobs; `inspectorColumns` gives Job (name over `repo #run`), Status, Machine, Start (`Starts about 14:16`, `since 14:09`, `finished 14:02`); the table adds a Why button per row through `inspect` | `pool-jobs-filters` |
| Filters panel open by default with Repository, Machine, Runner type, Branch | `SearchTable` takes `filterPane`: a Filters pane (search, State, Jobs/Runs, Range, Group by, Repository, Machine, Runner type, Branch as `SelectField`); open at first; first column from 1400 px, top row below; closed state shows tags and Open the filters; each choice is in the address; Repository and Machine options come from the API, Branch from the loaded rows | `pool-jobs-filters` |
| Inspector BESIDE the list; job page stays reachable by URL | A row or its Why opens the details panel as the next column (bottom sheet on a phone); title `job: status`, facts, why it waits, Open job. `/pool/jobs/<id>` and `/pool/runs/<id>` are unchanged | `pool-jobs-inspector` |
| Runs table is a second view, not the default | Switch order is Jobs, Runs; `?view=runs` shows the Runs table with its Open run action and details toggle (the toggle sits in the Filters pane) | (switch visible in `pool-jobs-filters`) |
| Reserved row when the API reports `reserved` | A queued job whose `wait_reason` is `reserved` shows status Reserved, its Start time and the reason line; read as a plain string because the contract list has no `reserved` yet; tested with a fixture | `pool-jobs-reserved` |

## Could not match

- **The `reserved` field.** The brief says "status 'reserved'". The Job schema has `state` queued/running/done and `wait_reason`; the existing assign reason `reserved` ("another job holds the machine's room") is the closest field, so I read `wait_reason === 'reserved'` (one function, `isReserved` in `words.ts`). If LOOK65 sends it elsewhere, change that one function. I did not edit `openapi.yaml`, to avoid a conflict with LOOK65.
- **Status words of the prototype** (Timer ran out, Waiting for room, Behind another job, Runner starting, No runner fits) need data the Jobs API does not send; live keeps waiting, running, success, failure and the reserved word.
- **Why not each machine table and the "Needs you" message** of the prototype inspector: no per-machine reasons in the Jobs API; the inspector shows the facts and the reason line, and the Job page keeps the slot timeline (a later decision, gaps.json).
- **Machine of a waiting job** reads an em dash: no runner has it yet, the API names no planned machine.
- **Export CSV** stays in the page's action bar, not in the Filters pane (UX-78 keeps it in the toolbar area).
- **Filters pane count** reads `open` / `{n} set`.
- **Elsewhere tab** (Analytics > Run elsewhere) is untouched: another agent owns Analytics.

## Design system

Followed: highest numbered version (v4), with the draft in `next-version`. CORE-1, CORE-2, CORE-3, CORE-5, CORE-7; UX-4, UX-10, UX-16, UX-43, UX-49, UX-58, UX-72, UX-74, UX-78, UX-84. Shared parts used: `SearchTable` (new `filterPane`, `inspect`), `SelectField` (new), `Segmented`, `Status`, `Code`, `Text`, `Message`, `Facts`, `LinkButton`, `ExportButton`, `ActionBar`, `Tag`, `Pane`. No app CSS, no `style=`. New component behaviour and words went into `next-version` (CHANGES C37, components.md 67, content.md "Jobs list as approved words" and the row action Why).

## Checks

- `pnpm -r typecheck`, eslint of the touched folders: clean.
- Web tests: `apps/web` jobs screens 111 pass (new `inspector.test.tsx`, 9 cases; the old Runs-default tests now name `?view=runs`; the Queue and Running Jobs-view tests say Why and the start time in place of Open job and Progress); `packages/ui` 297 pass (new filter pane and inspector cases in `SearchTable.test.tsx`).
- e2e with `bin/crsp` built by `go tool task build`: `pool-jobs.spec.ts` (new: Jobs list first, open filters, inspector beside the list, reserved row, Runs second view, DOM layout at 360, 393 and 1300 px), `jobs-doctor.spec.ts`, `ux-jobs-rows.spec.ts`, `ux-table-cells.spec.ts`: 74 pass. Chromium came from a `/dev/shm/pw` alias of the preinstalled build.
- Keyboard note: the filter fields are drop-downs, which keep Up and Down for their own options (UX-57 lets a select own them), so Tab passes the fields; the keyboard test says so.
- Full e2e suite (3205 specs) on the branch head through `branch_gate.py --e2e-only`: green after two fixes the full run found (one section gap between the Filters pane and the list, UX-10; the list-toolbar check of `design-layout.spec.ts` no longer names `/pool/jobs`, whose toolbar is the Filters pane).
- Branch CI run 45305992356 on fd57d04: every job green except the prettier check of two e2e specs; fixed in 11e6a10.
- Branch CI run 45311120361 on 11e6a10: green (all jobs).
- Shots (1278x1248, mocked API with the prototype's sample jobs): `docs/work/LOOK62-shots/pool-jobs-{filters,inspector,reserved}-{live,proto}.png`. The live header says machines offline because the mock has no event stream.

CODE DONE 11e6a101fd
