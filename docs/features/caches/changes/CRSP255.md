---
change: minor
task: CRSP255
---
The shared per-machine tool cache (R5.14) is built as library code: whole-entry installs by one rename, a size cap with least-recently-used eviction that spares entries used in the last 30 minutes, a read-write mount into every pool container, and a `Checker` interface the doctor takes (R5.61 to R5.64). Not built: the agent start that prepares the folder and runs the sweep, the `tool_cache_cap_gib` setting, and the doctor check itself. Windows access list checked as a plan only; macOS compiled and unit-tested, not run on a real Mac.
