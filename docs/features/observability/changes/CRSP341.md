---
change: minor
task: CRSP341
---
Secret scan guard (R6.15): a pull request that adds a line to `.gitleaksignore` fails in the pre-push hook and the CI lint job; removing lines passes. Planted fake secrets in tests are built at run time.
