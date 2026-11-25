# CRSP248 part C: web wizard "Get this machine ready"

## Done
- `apps/web/src/screens/dependencies/`: `GetReady.tsx` (wizard + page `/setup/machines/:id/ready?os=&kind=&job=&return=`), `plan.ts` (words, `toProgressEvent`, progress line), `ReadyDoor.tsx` (door note + link), tests.
- All states of spec 4: nothing missing (no wizard, `onDone`), first run, engine stopped (Start Docker), Review (facts per item, "Nothing has changed yet.", no install call before the primary, licence tick, non-admin sees why-not), running (poll 1 s, step name, bytes, time left, Stop, busy primary), paused (ask first, `restart-host?confirmed=true`, keeps polling to Verify/Done, Later), stopped (Continue, Undo asks and names each item), failed (what/why/next, Try again, steps for myself, Undo), done, done after reload (`?job=`), offline (409).
- Doors: Add runners (note + link; choices kept via address), Add machine Done, Setup first-machine step, `?setup=deps` on the machine page redirects to the wizard.
- Wizard harness: driver `get-this-machine-ready` runs all ten states, known list empty (in `GetReady.test.tsx`).

## Shared parts used
Wizard, Button, LinkButton, Message, Card, CardGrid, Facts, CheckRow, ChoiceField, CodeBlock, Disclosure, Section, Stack, Text, Loading, Page, PageHeader, Breadcrumbs, Ask/QuestionCard (`useQuestion`). Folded log: Disclosure + CodeBlock.

## Changes outside my folder
- `packages/ui/src/components/Wizard.tsx`: new optional prop `endPrimary` (label of the primary in paused and stopped; "Restart this PC now", "Continue"). Additive.
- `apps/web/src/timing.ts`: `dependencyPollMs`. `App.tsx`: lazy route. `Machines.tsx`: `?setup=deps` redirect. AddRunners, AddMachine, Setup: door notes (AddRunners also reads `machine, os, kind, count` back from the address).
- `docs/design-system/next-version/content.md`: section "Dependency setup words". No CHANGES.md entry (not my file).

## Not done / decisions
- CRSP247 Terminal / ProgressBar part has NOT landed (branch has the contract only). Live line goes through Wizard `progress` (text + Progress value from `overallPct`), folded log through Disclosure + CodeBlock, from `toProgressEvent`. When it lands, swap the Disclosure/CodeBlock in the `running` branch of `GetReady.tsx` for the Terminal and feed it `job.log.map(toProgressEvent)`.
- Retired words: the guard retires "Check" and "Check again", so the first step is "Look" and the button is "Try again" (spec text says Check / Check again). Owner may want to reword the spec.
- "Start Docker" starts a job with the check's recommended choice (`confirmed: true`); it assumes part B offers a choice for the stopped engine.
- Setup has no runner-type pick, so its door (D-DEP-11) is the note on the controller's own machine when no Docker answers; not tied to a runner choice.
- Done after reload needs `?job=` (the page keeps it in the address); with no `job` a finished job is not shown again, a running one is resumed.
- Nothing was run at 360/393/1300 (no e2e locally); only shared layout parts are used and the raw-controls / no-app-css scans pass.
- Doctor link text is part B's.

## Checked
CORE-1, CORE-3, CORE-4, CORE-5, CORE-6, CORE-7; UX-5, UX-6, UX-9, UX-13, UX-26, UX-29, UX-30, UX-38, UX-54, UX-62; components 4, 16, 18, 19, 26, 39. Design system v4 (next-version content).

## Tests
- `pnpm exec vitest run` in apps/web: src/screens (76 files, 780 tests) green; includes GetReady.test.tsx (21 tests + harness).
- packages/ui: 32 files, 300 tests green. scripts/design: all green.
- `pnpm -r typecheck` green; eslint and prettier clean on touched files.
