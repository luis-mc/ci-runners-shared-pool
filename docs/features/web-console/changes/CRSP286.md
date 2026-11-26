---
change: minor
task: CRSP286
---
The runs and jobs lists answer fast at 10,000 runs and 100,000 jobs: a runs page is cut first and only its rows are counted, the jobs list reads only its columns, new indexes (migration 00046) serve both, and list reads use their own read-only connections so writes do not hold them up. The API output is unchanged.
