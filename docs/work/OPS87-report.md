# OPS87 report: persistent Linux on Windows, failed-row display

Branch OPS87-persistent-linux-on-windows (rides RELEASE-63).

## Work 1
- `kinds.ts`: Persistent for Linux on a Windows machine (engine runs Linux containers) is offered when `containerReady`; else the reason `Persistent Linux runners on this Windows machine need the container backend, <why>.` (already in content.md). WSL2, Hyper-V, macOS VM keep `PERSISTENT_ELSEWHERE`.
- Server already accepted the create (R5.93, OPS28). Gate: `onMachineItself` excludes docker backend / pool scope, so the listener is always on. Pool budget: `Peak` counts the runner by machine, any kind. Tests added for both.
- Spec R5.134, R5.139 amended; change note `docs/features/runners/changes/OPS87.md`.
- Tests: `Kinds.test.tsx` (offered / reason / other places), Go `TestPersistentLinuxContainerRunnerOnWindowsIsNeverGated`, `TestPersistentLinuxOnWindowsCountsAgainstTheJobSlots`.

## Work 2
- (a) own System column in the Runners list (fit column).
- (b) `nameInfix`: pool names `-pool` Linux, `-win` Windows, `-mac` macOS; existing runners untouched.
- (c) `components.css`: a Status in a focused/active row inherits the row text colour; the opened Full error row has `--ds-color-text` on `--ds-color-selection` (AA in every theme, computed 4.6+). On-primary on primary is 6.3+. next-version CHANGES C38, components.md 15, content.md.
- (d) `oneStop` collapses a final `..` to `.` in the row's full error; `sentence` uses it.

## Design system checklist
- Core UX Principles checked: CORE-1 to CORE-7.
- Version followed: highest folder v4; sections ux-rules, components, tokens, content; rules UX-25, UX-61, UX-69, UX-72.
- Shared parts: Table, Status, Section, Message, Facts. No app CSS, no style=. Words in content.md. Change in next-version with CHANGES C38.
- Not a wizard change; the wizard parts are untouched.

## Checks
- CI run 45299603318 on 292db9e: green except e2e shard 3 (two Add runners specs assumed Ephemeral is the only Linux kind on Windows).
- Fix: `exports-stop.spec.ts` picks the Ephemeral radio on step 1 in the two Windows-machine specs that expect the ephemeral plan text. No other spec goes through /setup/runners/add with a Windows machine (design-layout uses the default machine; the OPS28 kind specs already choose the kind).
- Chromium: symlinks in /dev/shm/pw to /opt/pw-browsers (chromium 1255 -> 1243, ffmpeg 1013 -> 1011), PLAYWRIGHT_BROWSERS_PATH=/dev/shm/pw.
- Local gate `branch_gate.py --e2e-only`: PASS, 3247 passed, 2 skipped (23.6m).
- Branch CI run 45316940800 on 858762b: green (24m39s).

CODE DONE 858762b0df
