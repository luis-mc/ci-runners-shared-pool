---
change: patch
task: OPS66
---
The agent's gate tells a job observer when a job starts, ends or is cut short, and the agent link carries three new fields on JobResourceSamples (oom_killed, cut_short, memory_peak_bytes). The behaviour is in observability R5.60 to R5.65.
