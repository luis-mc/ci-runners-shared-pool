# OPS78 report: listener gate for persistent runners on the hosts (observe first)

Branch OPS78-listener-gate: origin/OPS77-integration, then OPS80-before-assignment and OPS81-crsp-timers merged one by one (octopus failed; conflicts in runners and doctor specs kept both sides, the older duplicate `runner.slots` row dropped for OPS81's).

## Steps
1. [done] Merge OPS80, OPS81 (migration 00040 is the next free number).
2. [done] `internal/listenergate`: gate (admit, completed, sweep, weekly wake, status), MemStore and SQLStore, settings; tests on fakes (labels client, service manager, clock).
3. [todo] Agent listener control (proto, backends per OS), launchd KeepAlive on failure only.
4. [todo] Controller wiring in assign, doctor info line, API, console, content.md.
5. [todo] Specs, change notes, docs_check, CI, draft PR.

## Open checks for the owner (10.6 listener tests, need a real runner; NOT run)
1. Session create and delete; how long "online" lasts after SIGINT and after kill -9 (poll the runners API every 5 s).
2. The second-job race: two jobs, one label set, listener stopped on the first completed webhook; count how often job 2 is already assigned.
3. Stop during a job and in the first second after assignment: the job's final status and message.
4. A label change on a connected listener: does it apply and how fast (add a custom label, queue a job that needs it, time the pickup, remove the label).
5. Windows: a LocalSystem agent stopping and starting a service of another account 50 times; time it, watch for logon errors (runners R5.89).
6. Update at start, JIT removal timing, organization label endpoints.
