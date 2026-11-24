---
change: patch
task: CRSP233
---
The KVM and engine checks (kvm.module, kvm.wslconfig, machine.engine) no longer crash with 'runtime error' on a Windows PC: the controller's report sources read machines through a fleet without a runner store, which was a nil pointer. A test runs every registered check against an empty and a Windows report.
