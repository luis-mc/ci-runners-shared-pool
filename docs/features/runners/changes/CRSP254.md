---
change: minor
task: CRSP254
---
Closes the runner gaps R5.34, R5.63, R5.64, R5.92, R5.93 and R6.5 in the spec test table. Adds the registry-mirror fallback with one notice (R5.34), default container limits, the one job network and the job-container check (R5.63), the `What a job can reach` section of `SECURITY.md` (R5.64), and the start-time budget check (R6.5). The code is the same for Windows, Linux and macOS behind functions and fakes; it was tested on Linux and Windows builds, and macOS is compiled and unit-tested in CI but not run on a real Mac.
