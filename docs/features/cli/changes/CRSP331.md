---
change: patch
task: CRSP331
---
The controller wires the debug access area at start: it ends sessions left open by the last run and sweeps due sessions every 5 seconds. No crsp command changed; crsp debug follows in its own task (debug-access).
