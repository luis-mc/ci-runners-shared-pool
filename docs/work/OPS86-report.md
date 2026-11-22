# OPS86 report: disk floor per machine in the console

Status: CI green (run 45002364505), draft PR #389 open.

## Done
- Step 1: contract (openapi + agent proto), regenerated code; per-machine `disk_floor_gib` in machine settings; service range 1 to 2000 GiB; push to agent; agent applies live in `Gate.SetSettings`; agent reports the floor in force and `disk_floor_off`; doctor `machine.floor.disk` uses the machine value. Go tests: machines, agent, API, doctor.

## Decisions
- No migration. Machine settings live as a JSON `doc` in `ctl_machine_settings` (migration 00032), so the new field persists with no schema change. A no-op migration would only risk a number clash (main ends at 00035, OPS77-integration at 00037, LOOK58 branches use 00040). If a column is wanted anyway, use 00041.
- Precedence: a console value > 0 replaces the value of the start; 0 means none set. A floor that is off in config.toml / flag stays off and the console value is refused (400).
- Default for new machines unchanged (10 GiB).

- Step 2: console field on the machine page (Disk section, `Machines.tsx`), shared parts only; words in design-system `next-version/content.md` (C21); web test `MachineDiskFloor.test.tsx` (4 cases).
- Step 3: specs runners R5.83, machines R5.72 (requirement + test rows), change notes for machines, runners, doctor, web-console. docs_check passes except W1 (this report; strip before the join, by rule).
- Checks run locally: go test ./internal/... ./cmd/..., golangci-lint, GOOS=windows go vet ./..., web typecheck/lint/unit (724 pass), scripts/design (129 pass), fast_guards (G1 warn is old: conftest, ci.yml go test, go-test.sh timeout).

## Notes
- The pre-push hook once failed on `TestSessionRefusesUnknownRevokedAndMismatchedKeys` (revoked key accepted); it passed 9 reruns, the test does not touch the floor. Looks like an old flake.
- The free space line uses 2^30 bytes per GB so it matches the GiB floor; formatSize uses 10^9.

## Left
- Strip this report before the join.

CODE DONE 263cf186675af04bedcaaff32eff4ddefe47599b
