---
change: minor
task: OPS70
---
Adds R5.124: the agent lists the persistent runners of the pool container as services and the view finds a runner by the ref its record stores, then by name, so a container runner no longer shows 'its service is not on the machine'; add-runner.sh is idempotent per runner name (ends a running listener first, keeps the folder of a set up runner when a retry fails).
