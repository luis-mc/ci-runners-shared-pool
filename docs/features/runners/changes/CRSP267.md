---
change: minor
task: CRSP267
---
New runners.Service.RemoveMachineRunners deregisters every runner of a machine at GitHub when the machine is removed from the pool (online: its folder is cleaned too; offline: its files stay), and the pinned runner version is read from the stored pins first (runnerpin, operations R5.20).
