---
change: minor
task: OPS62
---
Every exec into a pool runs as the uid the pool was made with, not the image's runner user; an existing persistent pool has its volumes re-owned (R5.103).
