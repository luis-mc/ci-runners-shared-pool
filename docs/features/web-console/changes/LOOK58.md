---
change: minor
task: LOOK58
---
Pool > Machines shows the health and the job stats of each machine (24 h or 7 days) with its runners, and a machine page at /pool/machines/<id> with every health metric and a Jobs over time slot; a value the API leaves out reads unknown on the machine's system (R5.154, R5.155).

One shared slot timeline (R5.156) draws Jobs over time in Pool > Live, Queue Analysis, Pool Overview, the Jobs why-waiting inspector and the machine page; the old live waterfall path is removed.
