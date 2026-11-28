---
change: minor
task: CRSP382
---
A runner whose GitHub registration was deleted registers again once by itself (detected from the listener log and GitHub's runner list), gives up with the reason and offers Register again; the pool supervisor stops its restart loop; the Runners page shows not registered, registered again and failed with the reason; creating no longer sticks; the Windows listener list no longer fails (R5.152, R5.153).
