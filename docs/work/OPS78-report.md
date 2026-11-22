# OPS78 report: listener gate for persistent runners on the hosts (observe first)

Branch OPS78-listener-gate: origin/OPS77-integration, then OPS80-before-assignment and OPS81-crsp-timers merged one by one (octopus failed; conflicts in runners and doctor specs kept both sides, the older duplicate `runner.slots` row dropped for OPS81's).

## Steps
1. [done] Merge OPS80, OPS81 (migration 00040 is the next free number).
2. [done] `internal/listenergate`: gate (admit, completed, sweep, weekly wake, park, status), MemStore and SQLStore, settings, `Control` for the API; tests on fakes (labels client, service manager, injected clock, no sleeps).
3. [done] Agent: proto `ListenerControl` (+ `RunnerResult.listener`), `persistent.Control` shared rules, Linux/macOS/Windows backends, `LocalAgent.ControlListener`, agentlink `ControlListener`. launchd restarts on failure only.
4. [done] `assign.Gate` hook (launch, Completed, Vanished, unused sweep), doctor `listener.gate` (info), API `GET/PUT /api/v1/listener-gate`, `cmd/crsp/listenergate_wiring.go`.
5. [done] Console: Listener section of the runner page, Persistent only on the machine itself, content.md + CHANGES C14.
6. [done] Specs (runners R5.134 to R5.141, scheduling R5.108, doctor R5.66), change notes, docs_check (only W1).
7. [done] CI: first run red on e2e shard 3 (3 Add runners specs assumed persistent Linux on a Windows machine) and prettier; fixed, local e2e gate pass, `ci.yml` dispatch run 45000844163 on 696fdd1 green. Draft PR opened.

## Decisions and deviations (owner please read)
- **launchd**: not "without KeepAlive" but `KeepAlive {SuccessfulExit: false}` (restart on failure only, like systemd `Restart=on-failure`). A clean stop stays stopped; a crash is still restarted. Removing the key is a one-line change.
- **Wiring in assign**: `assign.Controller` is not built in `cmd/crsp` yet (OPS80 left that for the join). `wireListenerGate` returns the `assign.Gate` adapter; set `Controller.Gate` when the join wires the controller. Until then the gate's API, doctor line, sweep and wake run, and no job reaches `Admit`.
- **Observe mode** writes `listener.would_start` / `listener.would_stop` once for each job (the lease row is the marker) and for each weekly wake; changes no label or listener; the JIT path goes on as before.
- **Maintenance start**: `--disableupdate` is already set at registration (pin kept). The wake passes `maintenance=true` through the whole chain, but no backend changes behaviour on it yet: the runner file update stays the existing `UpdateRunnerFiles` path (runners R5.38). Open item.
- **Old agents** do not know `ListenerControl`; a call to one times out (3 min). Not gated by a capability flag.
- **Settings screen** does not exist (LOOK56): the enforce setting is the API only; the Doctor fix links to `/settings`, like OPS81.
- **Persistent Linux on a Windows machine** is no longer offered in Add runners (R5.139); four web tests changed with it.
- Doctor check counts: 55 core, 57 with KVM (was 53 and 55 in the test, one check behind the union after the merge).
- The merge left two doctor rows `R5.65` (OPS75 and OPS81 collide); not renumbered.

## Open checks for the owner (10.6 listener tests, need a real runner; NOT run)
1. Session create and delete; how long "online" lasts after SIGINT and after kill -9 (poll the runners API every 5 s).
2. The second-job race: two jobs, one label set, listener stopped on the first completed webhook; count how often job 2 is already assigned.
3. Stop during a job and in the first second after assignment: the job's final status and message.
4. A label change on a connected listener: does it apply and how fast (add a custom label, queue a job that needs it, time the pickup, remove the label).
5. Windows: a LocalSystem agent stopping and starting a service of another account 50 times; time it, watch for logon errors (runners R5.89).
6. Update at start, JIT removal timing, organization label endpoints.

PR: https://github.com/luis-mc/ci-runners-shared-pool/pull/387 (draft)

CODE DONE 696fdd12636467e5eb71beaab48c1950695ba31d (observe mode; report commit ba5a2fe)

After merging the moved base (RELEASE-53): ci.yml run 45003713775 on 4111cbb green; later commit 9e33b0c is docs only (review visuals). dev-standards red only for W1 (docs/work), cleared at the join.

CODE DONE 9e33b0cb07ba5b5ef29bad41cc7f3b3293201cd8
