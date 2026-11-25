---
change: minor
task: CRSP267
---
The update refuses a build whose database schema is older than the database (crsp version prints a schema line, R5.13); agents follow the controller one machine at a time (R5.18); the daily check moves the runner pin to the newest actions/runner release after 7 days, stored as runner_pins, audit runner.pin.move (R5.20); the rollout modes gain the live step, move one step at a time and ask first, with the pane Rollout on the repository page (R5.42). macOS is built and CI-tested, not run on a real Mac.
