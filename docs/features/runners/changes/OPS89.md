---
change: patch
task: OPS89
---
An ephemeral Linux runner starts on a Windows host (no host chown); a failed start deletes its JIT registration, each try has its own name, tries stop at a limit with one failed row for each job, and an idle ephemeral pool runner no longer says no repository uses the pool (R5.150).
