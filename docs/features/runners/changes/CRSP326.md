---
change: minor
task: CRSP326
---
Delete registration on an offline pool runner works: it deletes the hidden per-repository registrations at GitHub with the same safety checks, then the pool runner record (R5.88). A runner request that fails unexpectedly now logs the real error.
