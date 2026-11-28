---
change: patch
task: CRSP385
---
A runner a release could not set up is set up again by the next release, once: the register again in place removes the runner's .runner_migrated and .credentials_migrated copies (config.sh said already configured), a failed add keeps the pool runner listed as not registered, a failed or stuck runner and a failed pool registration are tried once per release (the reason ends with the release that tried), and every failure logs its reason (R5.155).
