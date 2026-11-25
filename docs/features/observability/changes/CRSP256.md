---
change: minor
task: CRSP256
---
Adds minutes saved and money saved on screen: two Overview tiles, Analytics > Savings (today, 7 days, 30 days and a range, by repository, system and machine), Analytics > Compare (crsp against GitHub-hosted runners, R5.34), and editable rates in Settings > Repositories with the source address and date, owner-only and audited. Public repositories count minutes and save no money. Routes `GET /api/v1/savings`, `GET`, `PUT` and `DELETE /api/v1/savings/rates`. The live Overview now counts real finished pool jobs. Closes R5.16 to R5.19 and R5.34 on screen. The shipped rates were not re-copied from GitHub's page, and the fallback reason per run (gap G6) is not built; both are named in section 11.
