---
change: minor
task: OPS81
---
crsp owns the job and run timers with a 30 s grace (R5.99 to R5.106): job timer 30 min from the first real step, run timer 45 min, stop that job or cancel the run after the grace, Stop now / Cancel now / Extend 10 min once, audit rows, settings with check-while-typing, clocks stored (migration 00039), timer API. The hold cap drops from 10 to 2 minutes (R5.75). Queued and start-up timers: types, settings and interfaces only, wired with OPS80.
