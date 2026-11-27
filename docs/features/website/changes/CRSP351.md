---
change: patch
task: CRSP351
---
The site build skips the agent packages CrspAgent-<version>-<os>-<arch> (CRSP304), which the Controller fetches, instead of failing on them; any other agent-named asset still fails the build (R5.7).
