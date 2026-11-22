---
title: Handover from the dev-standards runner pool
type: explanation
---
# Handover from the dev-standards runner pool

This page records what crsp takes over from the runner pool in the dev-standards `luismc-delivery` plugin (see [ADR 0005](decisions/0005-product-boundary.md)), the contract between the two projects after the move, and the lessons from that pool. The dev-standards owner decided these points on 14 Nov 2026 (dev-standards decisions D299 to D305).

## 1. What changes and when

- dev-standards removes its pool, and deletes the runners on its owner's PC, only when the owner says so (D299). There is no automatic trigger.
- Until crsp has Linux containers (M2), Linux jobs of the projects on that PC run on GitHub-hosted runners.
- crsp then creates its pool from nothing. It reuses no state, folder, account or volume of the old pool.

## 2. The contract

dev-standards stops knowing about pools. These are the only shared items.

| Item | Written by | Read by | Shape |
|---|---|---|---|
| Repository variables `RUNS_ON_LINUX`, `RUNS_ON_WINDOWS`, `RUNS_ON_MACOS` | crsp (or the owner by hand) | delivery workflow templates, in `runs-on:` | JSON label list, for example `["self-hosted","linux","x64","pool-main"]`. Unset or empty means GitHub-hosted. |
| Runner environment value `CI_TOOLS_DIR` | crsp, on its runners, with a per-runner quota | delivery `ci_tool.py` (pinned downloads) and `rust_ci.py` (`CARGO_TARGET_DIR`) | An absolute folder the job may write. |
| Job class (light, normal, heavy) | crsp, from its own per-repo config | crsp hooks only | Delivery declares nothing; template job keys stay stable so crsp can match them. |

The delivery template line is:

```yaml
runs-on: ${{ fromJSON(vars.RUNS_ON_LINUX || '"ubuntu-latest"') }}
```

### How crsp fills the variables

1. The admin turns the pool on for a repository.
2. For each OS where that repository has at least one online runner, crsp writes `RUNS_ON_<OS>` with the labels that route to its runners: `self-hosted`, the OS, the architecture, the pool name, and only capability labels the agent has verified.
3. For an OS with no runner, crsp deletes that variable, so those jobs use GitHub-hosted runners.
4. When the admin turns the pool off for a repository, crsp deletes all three.
5. Every 10 minutes crsp restores a changed value; the hourly check reports workflows whose `runs-on` does not use the variables.

This replaces the `CRSP_RUNS_ON_*` names in the specs today.

### What moves into crsp (D302)

- `vm_check.py` and `vm-images.json`: boot test VMs on `kvm` runners.
- The Rust build-cache cap and the shared tool-cache mode: per-runner cache quotas and eviction belong to the pool.
- The pinned download and build logic of `ci_tool.py` and `rust_ci.py` stay in delivery, because projects also run them on GitHub-hosted runners. Confirmed by the dev-standards owner (D308): only the cache parts move (the shared-cache mode, the Rust cache cap, the per-runner quotas); jobs read `CI_TOOLS_DIR`. See [caches](../features/caches/spec.md) R5.44.

### What crsp does not take

- The Claude Code local heavy-work guard stays in delivery (D301).
- The cloud CI fallback's slot-wait trigger is removed (D301); crsp needs no `ci-wait` markers.

## 3. Lessons to build in from the start

Each item is a mistake the dev-standards pool made and then patched.

1. **Disk is a resource.** One persistent runner's Rust cache reached 320 GB, filled the system drive and stopped every runner (14 Nov). Per-runner cache quotas with eviction, and a disk guard in the gate, belong in the first release that has persistent runners.
2. **One registry for every OS**: os, kind, repo, route, account, labels, size class. Windows services were added late and in steps.
3. **Route separate from the runner name.** Tying routing to the agent slug broke a second runner for the same repository.
4. **Leases and heartbeats, never process ids**, for slots and queue tickets. A cancelled Windows job blocked the queue for 120 minutes because a pid check only works on one host. Keep a fixed lock order when a job takes two slot kinds.
5. **Declared job classes with weights.** Matching job names by pattern was fragile, and a job-level env value never reaches the job-started hook.
6. **Hooks without runtime dependencies**, tested as the service account on the real OS. PowerShell execution policy broke every Windows job twice; a per-user Python path broke the gate for service accounts.
7. **Hooks never fail silently.** If the gate cannot run, the job may go on, but the job log, the controller and the doctor all say so.
8. **Declared sizing, validated, never measured live.** Live sizing gave 12, 2.5 and 6.5 GiB within minutes on the same PC. Export the CPU limit to jobs and split it per runner; a container CPU limit alone left `nproc` at 20 and test runners were OOM-killed.
9. **Desired state with a plan and a diff.** Setup, apply and doctor share one code path. A refresh driven by the calling repository dropped other repositories' image layers.
10. **Zero-cut upgrades.** Build aside, drain through a versioned agent protocol, re-check, swap. One drain timeout value; after it, keep waiting and warn. A rebuild once cut a running job, and the first drain needed a force because the old supervisor had no drain mode.
11. **Images as declared capability layers** with a self-check and an image build in CI before release. Missing LLVM, Node, corepack, Playwright and Wine pieces were each found by a project's failing job.
12. **Identities in persistent storage with a reserved uid/gid range.** A package took two agent users' ids; users vanished when the container was recreated.
13. **KVM on WSL2**: after `wsl --shutdown`, `/dev/kvm` is gone until `kvm_intel` or `kvm_amd` is loaded in the WSL VM; `nestedVirtualization=true` must be in `.wslconfig`. Privilege stays in a one-shot helper; runners are never privileged.
14. **Mount folders, not single files**; compare labels without case.
15. **Truthful results.** Exit non-zero when an action was refused; say "unchanged" when nothing changed; attribute exit 137 to the memory cap that caused it.
16. **Observability in the first release**: status, and a structured event log of slot waits, drains and OOM kills.

### Learned from real project runs

Added as the old pool keeps running real projects' CI. Newest last.

17. **Hooks make no network calls that can ask for a login** (14 Nov). The job-start gate pushed a git marker branch after taking a slot. On a Windows service account with no stored git credential, Git Credential Manager waited for a window that never appears. Three jobs sat in "Set up runner" for 65, 95 and 15 minutes; the owner killed `git-credential-manager.exe` by hand. Rules: a hook does only local work; anything remote is optional, non-interactive (`GIT_TERMINAL_PROMPT=0`, `GCM_INTERACTIVE=never`), time-limited (seconds), and fails open with a log line. Never do remote work nobody reads: the marker had a reader only when the cloud fallback was on.
18. **Log the decision before the slow step** (14 Nov). The gate logged "slot taken" only after the marker call, so the hang showed as silence. Write the state change first, then do anything that can block.
19. **Supervise the listener, not its wrapper** (14 Nov). In the Linux container, the wrapper scripts of three runners exited while their listeners kept running and held the GitHub session. The supervisor saw the wrapper gone and started a second listener every few minutes; GitHub refused each with "a session for this runner already exists". Jobs still ran, but the log filled and the pool looked broken. Track the listener process (or the runner's own session state) and never start a second listener for a registered runner.
20. **A stuck job blocks every drain** (14 Nov). An image swap waited the full 45-minute drain behind a job that was itself hung (lesson 17), then gave up, and the pool stayed on the old image. A drain should show which job holds it and for how long, flag jobs far past their usual time, and offer the owner a named choice; it should never wait silently.
21. **An operation waits only for the runners it touches** (14 Nov). A Linux container recreate was refused because the same repository's Windows service runner was busy, although the recreate never touches it. Busy and drain checks are scoped to the runners the operation stops; unrelated runners are shown, not counted.
22. **Light jobs need their own lane** (14 Nov). A repository had one Linux runner. A Rust test job held it for over 35 minutes (it usually takes 15), and two artifact-prune runs, each a few seconds of API calls, sat queued behind it. A per-repo runner that takes one job at a time makes housekeeping wait on the slowest build. Give light jobs (prune, labels, notices) a small lane that is always free, so they never wait behind a heavy job.
23. **An account switch resets file owners** (14 Nov). A Windows runner moved from NETWORK SERVICE to its own account. The files in its `_work` tool cache kept the old owner and explicit ACLs, so every Rust job failed "Access is denied" on `lld-link.exe`. On an account switch, take ownership of the runner folder and reset every file to inherit (`takeown /R`, `icacls /reset /T`) before the lock; the doctor checks that the runner account can read its tool cache.
24. **One repository filled the disk in a day** (15 Nov). A Rust repository kept a per-runner build cache with a cap on old keys only. One run that rebuilt the whole workspace grew the current key to 308 GB, next to a 220 GB stale key. Docker's disk file reached 720 GB and the host had 4.8 GB free. Cap the current key too (incremental folders first, then the whole key), set CARGO_INCREMENTAL=0 and line-tables-only test debug info in CI, and enforce a pool-wide disk budget on a timer, not only at pool start. A deleted cache does not shrink the disk file: trim inside the VM, then compact the file with Docker stopped (admin). Watch the disk file size and alert past a set limit.
25. **The pool must survive a VM restart on its own** (15 Nov). After `wsl --shutdown`, a reboot or a Docker restart, Docker's new VM has no kvm_intel module. The pool container's restart policy started it without /dev/kvm and it exited (255) until someone ran it by hand. Without a host scheduled task (the owner declined one): a privileged loader service in the compose file, restart unless-stopped, loads the module at every Docker start; the pool waits for /dev/kvm for a bounded time, then starts without KVM and logs it; the doctor reports a missing loader.
26. **A drain must stop the listener, not only its wrapper** (15 Nov). A pool refresh drained the pool, timed out on a busy job and gave up. Its stop of the idle agents ended run.sh, but Runner.Listener survived, reparented to init. When the drain ended, the supervisor started second listeners; three duplicates looped on "A session for this runner already exists". Stop the whole process group (or signal the listener directly), and never spawn an agent whose listener process still runs. The health check counts listeners per agent, not in total.
27. **A drain must not wait on a stopped pool** (15 Nov). After a Docker restart the pool container was Exited. pool-refresh wrote the drain file and waited for the supervisor's drained file, up to 45 minutes, but no supervisor was running to answer. When the pool container is not running, treat the pool as drained at once.
28. **Never map /dev/kvm as a device at container start** (15 Nov). In a real `wsl --shutdown` test, Docker restarted the pool (restart policy) before the KVM loader service; depends_on is not honoured at daemon boot. The `devices: /dev/kvm` mapping then failed the start ("error gathering device information ... no such file or directory"), so the in-container wait never ran. Grant the device with `device_cgroup_rules: ['c 10:232 rwm']` instead, and let the supervisor create /dev/kvm (mknod 10:232) once the host lists kvm in /proc/misc, within its bounded wait.
29. **Expect stale runner sessions for about two minutes after a host shutdown** (15 Nov). After a real `wsl --shutdown` the pool came back on its own with KVM, but every listener logged "A session for this runner already exists" for about two minutes, until GitHub dropped the sessions it still held from before the shutdown. Then all four logged "Runner reconnected". Treat these lines as normal for a few minutes after an unclean stop, and alert only when they last longer or when the listener count goes above one per runner.
30. **Host maintenance needs a hold, not a cancel** (15 Nov). To compact Docker's disk file, Docker had to stop while a repository had required CI jobs queued. Cancelling them would have cost that project a re-run. Instead the pool was put in drain mode: the running job finished, no runner took a new job, the queued jobs stayed queued, the drained file appeared within minutes, Docker stopped for the compact, and lifting the drain let the queued jobs start on their own. Offer this as one maintenance command (hold, wait for drained, run the maintenance, release) and tell each project before and after.
31. **A cache cap must run when the build fails** (16 Nov). A Rust repository capped its build cache after the build. One run succeeded and the cap took the key from 101 GB to 6 GB. The next run failed after 100 minutes, the cap step was skipped, and the key stayed at 101 GB until the next job. Run cache caps on every outcome (always, or a pool-side budget on a timer), never only after success.
32. **A runner service account needs "Log on as a service", checked after every boot** (18 Nov). After a host restart, two Windows runner services set to start automatically failed with a logon failure: their own accounts (one per runner) lacked the "Log on as a service" right. The runners stayed offline and their jobs queued until someone with admin rights acted; nobody was told. Grant the right when the account is created (LsaAddAccountRights, SeServiceLogonRight), re-check it in the doctor and after every boot, and when a runner service fails to start, say so to the owner and the projects it serves with the Windows error, instead of leaving the runner silently offline.
33. **The Linux pool must start without anyone signing in** (20 Nov). Windows Update restarted the host at 02:59 and again at 03:05. The Windows runner services came back at boot, but Docker Desktop starts only when a user signs in, so the Linux pool stayed down until 06:54, almost four hours. A CI run that needed both kinds of runner stood still for that time: its Windows job was cut off by the restart and failed, and its next Linux job waited until sign-in. Nothing reported the outage. Rules: the Linux pool starts at boot without a signed-in user (a Docker engine that runs as a service, or a scheduled task at startup that starts Docker and the pool); the KVM loader from lesson 25 runs after it; and a check after every boot reports a pool that is not listening within a few minutes. Also set Windows Update active hours or a restart window so planned restarts do not land in the middle of a CI run.

## 4. Capabilities projects use today

These run on the old pool and have no crsp requirement yet: Linux containers with KVM; a Wine prefix per runner with `TMPDIR` unset; LLVM and clang-cl for cargo-xwin, plus the symlink right on Windows; Playwright browsers in the image; a Postgres per runner with its own roles; WSL memory sizing. Until crsp has them, those jobs use GitHub-hosted runners.

There is no parity gate (removed in CRSP227): the owner moves each repository by the [move runbook](../guides/runbooks/move-to-crsp.md), validates on real use, and chooses when the old pool goes. The requirements for these are written, marked not built, after v0.1.0: [runners](../features/runners/spec.md) R5.41 to R5.45 (containers with KVM, Wine, LLVM, Playwright, Postgres roles), [machines](../features/machines/spec.md) R5.53 and R5.54 (KVM on WSL2, WSL memory), and [caches](../features/caches/spec.md) R5.41 (Playwright browsers folder).
