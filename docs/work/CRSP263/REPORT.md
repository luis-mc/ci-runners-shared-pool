# CRSP263 report: uptime per machine (G7)

- [x] Step 1 back end: migration 00044 `machine_presence`, store, `machinehealth.Uptime/Observe`, machines hook, API `getMachineUptime`, Go tests green locally.
- [ ] Step 2 web: Uptime section on the machine page (shared parts), tests.
- [ ] Step 3 docs: machines spec R5.83 to R5.86, change note, design-system next-version content + CHANGES, docs_check.
- [ ] Step 4 CI on branch, rename to CRSP263-integration, PR.

Design: one row per state change (taking, not_taking + reason, offline + reason); uptime is the online share of the watched time; a gap while the controller was stopped is written offline (controller_stopped).
