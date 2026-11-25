---
change: minor
task: CRSP288
---
Overview snapshot and timeline read their independent sources at the same time (bounded), a source may cap the queued list and count the rest in SQL, and the follow-up parent link of a run page is read in one query instead of one per run. API output is unchanged.
