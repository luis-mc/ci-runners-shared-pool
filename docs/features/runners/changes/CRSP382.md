---
change: minor
task: CRSP382
---
A runner whose GitHub registration was deleted registers again once by itself (detected from the listener log and GitHub's runner list), gives up with the reason and offers Register again; the pool supervisor stops its restart loop; the Runners page shows not registered, registered again and failed with the reason; creating no longer sticks; the Windows listener list no longer fails (R5.152, R5.153). The pool container's scripts are compared with the release's when the controller first looks at the pool and rewritten when they differ, and the pool supervisor loads its new copy by itself while no runner has a job (an older supervisor is replaced by a container restart, only while idle) (R5.154).
