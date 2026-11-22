---
change: minor
task: OPS81
---
crsp owns the job and run timers with a 30 s grace (R5.99 to R5.106): job timer 30 min from the first real step, run timer 45 min, stop that job or cancel the run after the grace, Stop now / Cancel now / Extend 10 min once, audit rows, settings with check-while-typing, clocks stored (migration 00039), timer API. The hold cap drops from 10 to 2 minutes (R5.75). Queued and start-up timers: types, settings and interfaces only, wired with OPS80. Timers start in observe mode (R5.107): the new setting `enforce` is false on a new install and on upgrade; a job or run over its timer is written as `timer.job.would_stop` or `timer.run.would_cancel` with a note and nothing is stopped or cancelled; Stop now, Cancel now and Extend still act; `enforce` on is the behaviour of R5.100.
