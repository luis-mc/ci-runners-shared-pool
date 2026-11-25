---
change: minor
task: CRSP264
---
R5.36: `cpu=N` and `ram=N` size labels pass the generic label check (a bad value is refused with the reason) and lower the job container's CPU and memory caps, never raise them (scheduling R5.67a).
