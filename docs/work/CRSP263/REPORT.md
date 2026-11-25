# CRSP263 report: uptime per machine (G7)

- [x] Step 1 back end: migration 00044 `machine_presence`, store, `machinehealth.Uptime/Observe`, machines hook, API `getMachineUptime`, Go tests green locally.
- [x] Step 2 web: Uptime section (MachineUptime.tsx), tests green, tsc and eslint clean.
- [x] Step 3 docs: spec R5.83 to R5.86, change note, next-version content + C46. docs_fold.py and docs_check.py are not installed in this sandbox (luismc-docs plugin missing): change note written by hand in the same format as OPS83.md; docs_check not run, CI dev-standards will.
- [ ] Step 4 CI on branch, rename to CRSP263-integration, PR.

Design: one row per state change (taking, not_taking + reason, offline + reason); uptime is the online share of the watched time; a gap while the controller was stopped is written offline (controller_stopped).

CI: dispatched ci.yml on CRSP263-uptime 14:46 UTC, run 45637803249; watching with ci_watch.py.
