# CRSP235 report: console restart and Gate maximum wait

Branch `CRSP235-console-restart-gate-wait`, from origin/main 44f1cff. No pull request opened (none was asked for).

## What was wrong (findings)

- Controller restart: the console page already existed (`/settings/updates/review?kind=restart`, shared Wizard, `POST /api/v1/service/restart`) but nothing in the console linked to it. Only the tray opened it.
- Agent restart: did not exist at all. No API, no agent command. The controller's `OnIdle` hook for `service_restart` would also have restarted the controller for any machine, so an agent machine could not use it.
- Gate maximum wait: `PATCH /api/v1/machines/{id}` could not write `gate_max_wait_minutes` (the field was read-only in the contract) and nothing validated it. "Whatever the API validates" was nothing, so the range is now defined: 1 or 2 minutes, the hold cap of scheduling R5.75.
- The doctor fix linked to `/machines/{id}` with no field to open.

## What changed

Contract first (`packages/contract`, generated files regenerated with `go tool task codegen`):
- `POST /api/v1/machines/{machineId}/restart` (`restartMachineService`, admin, `confirmed` query, answers the `MachineChange`). 409 `needs_confirmation` names the running jobs; 409 for the controller's machine and for an offline machine. Audited as `machine.service_restart` with the actor (existing `beginChange` audit).
- `agent.proto`: `RestartAgent restart_agent = 31` in `SessionResponse`.
- `MachineSettingsUpdate.gate_max_wait_minutes` 1 to 2; `MachineSettings` documents the same range.

Controller (`internal/machines`, `internal/api`):
- `Service.RestartService` (confirm, controller and offline refusals), `askRestart` at idle, `finishRestart` on the agent's next Hello, `failRestart` on an `unsupported` or `failed` update report (change becomes `needs_you`, machine stays held until Resume).
- The change stays `running` while the service restarts, so the machine takes no new job; it ends `done` and the machine is active again when the agent connects again.
- `machines.MaxGateWaitMinutes = 2` (a test keeps it equal to `slots.MaxHoldMin`); validated only when sent, so a longer stored value (older versions allowed 30) never blocks saving another field.

Agent (`internal/agent`, `internal/update`): `RestartAgent` handler calls `update.Manager.Restart` (same drain and helper as an update; never cuts a job).

Doctor: `runner.slots` fix link is `/machines/{id}#gate-max-wait` (the console maps it to `/setup/machines/{id}#gate-max-wait`).

Web console (`apps/web`): Service section and Gate maximum wait field on the machine page; new wizard page `MachineRestart.tsx` (route `setup/machines/:id/restart`); Restart the service button in Settings, Updates; the machine page scrolls to `#gate-max-wait`.

## Default Gate maximum wait (scheduling R5.75)

Already 2 minutes: `machines.DefaultSettings` (`GateMaxWaitMinutes: 2`), scheduling R5.1 and R5.75 (owner decision 22 Nov 2026, OPS81). Nothing to lower. Said in the spec, not changed.

## Design system checklist

- [x] Core UX Principles: CORE-1 to CORE-7 checked.
- [x] Design-system version: v4 (highest numbered folder) plus `next-version`. Sections used: `ux-rules` UX-4, UX-5, UX-10, UX-13, UX-15, UX-16, UX-22, UX-29, UX-30, UX-38, UX-54, UX-56; `components` (Wizard, Section, NumberField, Button, Cluster, Facts, BulletList, Message, Text, EditBar); `tokens` (none added); `content` (new words, below).
- [x] Wizard: rendered through all ten states by the shared harness (`apps/web/src/screens/wizards.test.tsx`, driver `restart-machine`): loading, first run (machine offline), planning, review, running, paused, stopped (the controller's machine), failed, done, done after reload. Later steps cannot be pressed; Done shows the result; the button that starts work is busy while it runs. Stop is not offered because the work cannot be stopped once the agent is asked (the harness requires Stop only when the job can stop).
- [x] Step, lock, busy state come from the shared `Wizard`; the page passes data only.
- [x] Shared parts by name: `Wizard`, `Section`, `NumberField` (the number keypad), `EditBar` (the Save bar), `Button`, `Cluster`, `Facts`, `BulletList`, `Message`, `Text`, `Breadcrumbs`, `PageHeader`, `Page`.
- [x] No hand-made button, bar, table, tab row, empty box or scroll box. No stylesheet, no `style=`. The one bare `<div id="gate-max-wait">` is an anchor with no look, like `At` in Settings.tsx.
- [x] Enforcement kit green, no known list changed: `npx vitest run scripts/design` (no-app-CSS scan, raw controls, raw inputs, wizard behaviour, follow guard, contrast, words lint); DOM layout at 360, 393 and 1300 px in `e2e/tests/web-console/machine-restart-gate.spec.ts` and `ux-routes.spec.ts` (reads the route table, so `/setup/machines/:id/restart` is covered). The follow guard caught the retired words "Check again" and "Checking"; the page now says "Try again" and "Looking".
- [x] New words are in `content.md` ("Restart the service and Gate maximum wait words"), `CHANGES.md` entry C37.
- [x] No rule, token or component changed.
- Human check left: the Service section sits with its siblings; the words read as a person would say them; Restart the service is the next obvious action on its page.

## Docs

Specs: machines R5.81, R5.82; operations R5.127; web-console R5.157; doctor R5.60 and scheduling R5.75 (one sentence each). Change notes with `docs_fold.py note --task CRSP235` for machines, web-console, operations, doctor and scheduling. `docs/guides/real-machine-test.md` step 12 says where the console restart is. `docs_check.py`: 0 errors, 0 warnings.

## Checks

- `go build ./...`, `go vet ./...`, `gofmt`: clean. `golangci-lint` v2.15.0 on machines, agent, api, update, doctor, cmd: 0 issues.
- `go test` machines, agent, api, update, doctor, scheduler, cmd: pass.
- `pnpm -r typecheck`, `pnpm -r lint`, `pnpm run lint:root`: clean. Web tests of the touched screens (machines, settings Updates, wizards) and `scripts/design`: pass.
- `python3 scripts/delivery/fast_guards.py --base-ref auto`: secret-scan, change-notes, guards PASS.
- e2e gate (`branch_gate.py --e2e-only --no-setup --no-publish`): PASS, 3247 passed, 2 skipped, 23.8 min, including `machine-restart-gate.spec.ts` and `ux-routes.spec.ts` at 360, 393 and 1300 px.

## Environment notes

- The sandbox blocks `cdn.playwright.dev`, and Playwright 1.65.1 wants chromium 1255 while 1243 is pre-installed. To run the e2e gate I made a local alias directory `/dev/shm/pwb` (outside the repo) that points 1255 at the pre-installed 1243 and set `PLAYWRIGHT_BROWSERS_PATH=/dev/shm/pwb` and `E2E_CHROMIUM_PATH` (the gate's own switch to skip the download). `scripts/delivery/*` is untouched.
- The gate needs `bin/crsp`: built with `go tool task build` (not committed).
