---
change: minor
task: CRSP367
---
Each overflow target (GitHub-hosted, AWS EC2) gets an optional monthly money budget (budget_usd, 0 is none) beside its minute caps. Money used is the minutes used times the per-minute rate savings keeps; a target is used only while its minute caps and budget have room, and AWS EC2, which has no rate, does not enforce it. Settings, Repositories shows the money used and the budget per target (R5.114, R5.115). Q24 money part is decided; the runner size stays open.
