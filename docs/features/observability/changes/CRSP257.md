---
change: minor
task: CRSP257
---
Audit hash chain (R5.28): each audit row stores the hash of the row before it, `audit.Log.Verify` names the first broken row, and `audit.CheckChain` is the hook the doctor calls. History CSV export (R5.14) confirmed built on the OPS34 route and Export CSV button. Built for all systems; tested on Linux and Windows CI, macOS compiled and unit-tested in CI only.
