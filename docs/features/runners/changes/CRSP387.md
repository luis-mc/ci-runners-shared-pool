---
change: patch
task: CRSP387
---
A Windows runner service can read the folders above its own (the runner group may list the runners folder and its parent), a start that does not stay up fails with the runner service's own message, and a stopped runner has Start in the console and crsp runner start (R5.156, R5.157).
