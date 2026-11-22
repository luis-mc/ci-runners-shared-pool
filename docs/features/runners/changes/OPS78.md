---
change: minor
task: OPS78
---
Listener gate for persistent runners on the machine itself (R5.134 to R5.141): admission starts a gated runner's listener and labels it for the job, the completed event stops it; agent start, stop and status per host OS; weekly wake-up; launchd restarts on failure only; observe mode first; Persistent offered only on the machine itself; console Listener section.
