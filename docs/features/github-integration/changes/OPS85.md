---
change: minor
task: OPS85
---
The owner opens the runs-on pull request (R5.109): previewRunsOnPullRequests and openRunsOnPullRequests on /api/v1/runs-on-pr (admin), idempotent, one audit row per pull request, and crsp repos runs-on-pr [--repo|--all] [--preview]. Nothing else opens it; crsp never merges.
