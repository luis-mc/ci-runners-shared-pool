---
change: minor
task: CRSP234
---
Windows add: the SID of the runner account is read with the machine-qualified name (MACHINE\name) and, when that lookup finds nothing, from Get-LocalUser by the exact name, so a freshly made account such as crsp-<machine>-<hash> no longer stops the folder reset with 'No mapping between account names and security IDs'. The account name rule (R5.13: at most 20 characters, unique, stable per runner) is unchanged and now has a test.
