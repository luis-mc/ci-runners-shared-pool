# OPS86 report: disk floor per machine in the console

Status: in progress (branch OPS86-disk-floor-console).

## Done
- Step 1: contract (openapi + agent proto), regenerated code; per-machine `disk_floor_gib` in machine settings; service range 1 to 2000 GiB; push to agent; agent applies live in `Gate.SetSettings`; agent reports the floor in force and `disk_floor_off`; doctor `machine.floor.disk` uses the machine value. Go tests: machines, agent, API, doctor.

## Decisions
- No migration. Machine settings live as a JSON `doc` in `ctl_machine_settings` (migration 00032), so the new field persists with no schema change. A no-op migration would only risk a number clash (main ends at 00035, OPS77-integration at 00037, LOOK58 branches use 00040). If a column is wanted anyway, use 00041.
- Precedence: a console value > 0 replaces the value of the start; 0 means none set. A floor that is off in config.toml / flag stays off and the console value is refused (400).
- Default for new machines unchanged (10 GiB).

## Left
- Web field, content.md, specs (runners R5.83, machines), docs_fold note, docs_check, checks, CI.
