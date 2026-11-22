---
id: product
title: Product scope
type: explanation
---
# Product scope

This page is the product brief. Feature specs in `docs/features/` turn it into requirements with IDs. Decision records in `docs/architecture/decisions/` hold the cross-cutting choices.

## What it is

A manager for shared, self-hosted GitHub Actions runners on the user's own computers. One person or a small team runs CI for many GitHub repositories on a pool of their own machines (Windows, Linux, macOS), instead of paying for GitHub-hosted minutes.

It replaces the runner-pool feature of the dev-standards `luismc-delivery` plugin (decision 0005). It never depends on that plugin. Its logic and tests are ported from it.

Parts:

1. **Controller**: one service. It holds the configuration, talks to GitHub, decides which runner starts where, and serves the web console and an API.
2. **Node agent** on every computer in the pool. It reports what the machine can do and how loaded it is, and starts and stops runners there in the isolation the machine supports.
3. **Web console**: the user signs in with GitHub, picks repositories, manages machines and runners, and watches jobs live.
4. **CLI** with the same powers as the web console (scriptable, for headless machines).
5. **Tray** on desktop machines: opens the console, restarts, updates (decision 0005).

All five are one Go binary, `crsp` (decision 0001).

## Owner decisions (2026-11-13 and 2026-11-14)

- Machines first. The user adds machines (any computer on the local network that runs the agent; not only the controller's machine). Runners are then created from the web console, per OS, only on machines that support that OS and isolation. Cloud machines come later.
- The product manages self-hosted runners only. It does not manage GitHub-hosted runners.
- The web console is reachable on the local network over TLS. Every user signs in with GitHub. The tray's one-time link also works on the controller's machine.
- Users: the first person to sign in becomes the owner. The owner allows other GitHub users as admin or viewer. Turning the pool on for a repository also needs admin rights on that repository in GitHub.
- Ports: 8460 for the console and API (plain HTTP on 127.0.0.1 and ::1, HTTPS on every other address), 8461 for the agent link (gRPC over TLS). Both can be changed in config.
- Repository variables the product writes: `CRSP_RUNS_ON_LINUX`, `CRSP_RUNS_ON_WINDOWS`, `CRSP_RUNS_ON_MACOS` (JSON lists of lowercase labels, such as `["self-hosted","linux","x64"]`). A variable exists only while the repository has a crsp runner for that OS and the pool is on for it; unset means GitHub-hosted. Workflows say `runs-on: ${{ fromJSON(vars.CRSP_RUNS_ON_LINUX) }}`.
- Milestone 1 builds persistent runners for Windows, Linux and macOS together.
- Task IDs and branches: `CRSP<n>-<slug>`. CRP1 to CRP14 keep their labels.

## Owner decisions for milestone 1 (2026-11-14)

- Drain: a change that must restart runners waits for running jobs. After the drain timeout (one per machine, default 60 minutes) it keeps waiting, warns, and names the blocking job. It never kills a job.
- Default job slots for a new machine are sized from its cores and memory within the load budget, as dev-standards does. The user can change them.
- When a job-started hook cannot reach the controller or agent, it waits on local load only and logs it. A controller outage never stalls CI.
- Queue order: repository priority first, then fair share (round-robin by repository) within a priority. The per-repository parallel limit always applies. Fair share, priority and limits are in M1. Queue order and every repository attribute are configurable in the web console.
- Windows runner accounts get "create symbolic links" only when a machine or label setting asks for it.
- Runners not made by CRSP are shown as not managed. A Replace action removes each one with a remove token after a yes and creates a managed one. Nothing is changed silently.
- Controller config is one TOML file with one validated schema. Config as code uses the same format.
- Daily backup of the database and config, and the restore drill, are in M1. Backups never hold secrets; a restore re-imports them from the OS store or asks for them.
- Console TLS: the controller makes its own small CA and server certificate. The installer or tray adds the CA to the local trust store after a yes. Other computers get a one-click CA download. Agents pin the certificate at join.
- Console sessions never expire by time. They end at sign-out.
- The tray's Open link signs in as the GitHub user bound to that OS user. The binding is made once, by a GitHub sign-in from the tray. Loopback only.
- Join codes and the install command are in M1. The M1 agent on the controller's machine uses the same join path.
- Turning the pool off for a repository deletes its `CRSP_RUNS_ON_*` variables after a yes. The question names the variables and the workflows still using them.
- The doctor starts in M1 with core checks: machine, agent, persistent runners, GitHub App, variables, workflow labels. Each later milestone adds its checks.
- CLI and console use the verb "add", never "create" (`crsp runner add`, `crsp machine add`).
- Retention: job history 90 days, logs 14 days and 512 MiB per machine, audit log 400 days. Viewers may read logs and the audit log. All changeable in Settings.
- Dev GitHub App: redirect `http://127.0.0.1:8460/auth/github/callback` (GitHub refuses `https://localhost`); webhooks off for the dev controller.

## Accounts and GitHub

See decision 0002.

- Sign in with GitHub through a GitHub App that each controller registers for itself (manifest flow in the first-run wizard). Web: the App's user OAuth flow. CLI: the device flow.
- After sign-in, list the installations and repositories the user can administer. The user turns the pool on or off per repository and per organization.
- Never store a personal access token. The App private key and webhook secret live in the OS secret store (Windows Credential Manager or DPAPI, macOS Keychain, Linux Secret Service or a file with mode 0600), never in the database or logs.
- Registration and remove tokens are fetched when needed and dropped after use. GitHub needs a remove token to deregister a runner; a registration token is refused there.

## Repositories and policies

- Per repository: on or off, which runner groups and labels it may use, maximum parallel jobs, a priority, and whether jobs from public forks may run (default: never).
- Generic labels only: `self-hosted`, OS, architecture, `docker`, `kvm`, `gpu`, a pool name. Never a project name.
- The product keeps the repository variables correct and warns when a workflow names a label that no runner carries.
- Runner groups at organization level when the user has an organization; repository-level runners otherwise.

## Machines (nodes)

- Add a machine by running one install command that the console shows, with a one-time join code. The agent connects outbound to the controller over TLS; no inbound port opens on the machine (decision 0004).
- The agent detects and reports: OS and version, CPU and architecture, memory, disk, isolation backends (Docker, Podman, containerd or nerdctl, WSL2, Hyper-V, KVM, Apple Virtualization, Firecracker), GPU, and measured load.
- Per machine: active or drained, the load budget (default: at or below 85% memory and 60% CPU in total), the number of job slots, the backends allowed, a cache size cap, and maintenance windows.
- Drain, never kill: changes that restart runners (image rebuilds, agent updates, a new cap) wait until the machine has no running job and say what they wait for. The drain timeout (one per machine, default 60 minutes) only starts a warning; crsp never stops the job.

## Runners and isolation backends

See decision 0003. Each backend implements one interface: prepare image, start runner with JIT config, stream logs, stop, clean up, report usage.

| OS | Backends, best first | Notes |
|---|---|---|
| Linux | Firecracker or Cloud Hypervisor microVM (with KVM); rootless Podman or Docker container; persistent runner | microVM per job is the default when `/dev/kvm` exists |
| Windows | Hyper-V VM; WSL2 Linux microVM or container; Windows container; persistent runner as a service account | jobs that need the full desktop toolchain often need a persistent runner or a VM |
| macOS | macOS VM on Apple's Virtualization.framework, crsp's own code, not Tart (not yet implemented); persistent runner | at most two macOS VMs per Mac at once; enforce it |

- Images: a few base images per OS with tool layers the user turns on (build tools, browsers for Playwright, packaging tools, cross-compilers). Build once per machine, keep the newest few, pull through a local registry mirror.
- Snapshots: boot a VM once, snapshot it, start each job from the snapshot (about 11 s saved per VM start in dev-standards).

## Scheduling and load

- One queue per machine across all runners and repositories, with a machine-wide slot count and the load budget. A job-started hook waits for a free slot and for the load to fall below the budget. A job-completed hook frees the slot. Dead holders are found and freed. This is the core of the product; port it from dev-standards.
- Place jobs by label match, then by free capacity, then by cache warmth (the machine that ran this repository last has its caches).
- Fair share between repositories, priority per repository, and a per-repository parallel limit.

## Caching and speed

- A local Actions cache server compatible with `actions/cache`, per pool, with a size cap and eviction.
- A git mirror per repository, so checkouts fetch from the local network.
- A shared tool cache with SHA-256 pinned downloads (port `ci_tool.py` and `tools.json`).
- A registry mirror for container images; a package proxy (npm, PyPI, crates, apt) later.

## Security

- Ephemeral by default; persistent runners are marked as such in the console.
- Each persistent Windows runner runs as its own local non-admin account with only the rights it needs (log on as a service, create symbolic links when a tool needs it); its folder is locked to that account.
- No privileged containers. KVM is passed as a device, never with `--privileged`.
- Public repositories: fork pull requests never reach the pool unless the user turns it on per repository, with a warning.
- Audit log of every change: who, what, when.
- The agent runs with the least rights it can; the parts that need admin rights are one install step the user approves.

## Observability

- Live view of the queue, running jobs and each machine's load (memory, CPU, slots), updated by server-sent events or WebSocket.
- Job history with wait time, run time, machine and backend; minutes saved compared with GitHub-hosted runners.
- Logs of runner setup and of the agent, searchable.
- Doctor: checks every machine, backend, runner, GitHub registration, repository variable and workflow label, with a clear fix for each finding. Port the checks from dev-standards' `delivery_doctor.py`.
- Notifications (email, webhook, desktop) for: machine offline, job waiting longer than N minutes, disk nearly full, runner version outdated.

## Operations

- Auto-update the GitHub runner version (pinned with SHA-256, updated when GitHub deprecates the old one) and the agent itself, with drain.
- Backup and restore of the controller's configuration.
- Export and import of the configuration as a file (config as code), with a dry-run plan that shows each change before it is applied.

## Web console screens

Design system v1 (`docs/design-system/v1/`) is binding. Screens:

1. Sign-in and first-run wizard (GitHub App, first machine, first repository).
2. Overview: machines, queue, running jobs, load bars, today's minutes saved.
3. Repositories: list from GitHub, pool on or off, labels, limits, repository variable status.
4. Machines: each machine with its backends, load, slots, drain, budget, install command.
5. Runners: live list with backend, isolation, labels, job, age.
6. Jobs: queue and history, filter by repository, machine, result.
7. Images and caches: layers, sizes, rebuild with drain.
8. Doctor: all findings with fixes.
9. Settings: budgets, notifications, updates, users, audit log, config export.

## What to reuse from dev-standards

dev-standards (github.com/luis-mc/dev-standards, public; same owner, so its code may be reused here) runs a working shared runner pool across several repositories on one Windows PC with Docker Desktop. Port the logic and its tests. Paths are in that repository:

| Area | Files | What to take |
|---|---|---|
| Machine slots and queue | `claude/plugins/delivery/scripts/machine_slots.py`, `runner_load_gate.py`, `runner_job_done.py` and their `.ps1`/`.sh` wrappers | one machine config, one slot queue for every runner on the machine, the job-started gate and job-completed release, dead-holder detection |
| Load measurement | `scripts/load.py`, `load_guard.py` | memory and CPU as medians, the WSL VM measured by the larger of working set and private bytes, Docker read through the Windows CLI first |
| Pool plan and setup | `scripts/delivery_runner_pool.py`, `delivery_ci_setup.py` | detect, plan (each step with a yes, deletions named), run, refresh; sizing a pool from the machine's budget; the Windows runner service setup (dedicated account, rights, locked folder, `.env` hooks, remove token, reruns) |
| Doctor | `scripts/delivery_doctor.py` | machine, pool, runner, hook, variable and label checks with fixes |
| Pool image | `templates/runner-pool/` | image layers, an agent user per runner, KVM without privileged mode, Wine prefix per agent |
| Tools | `templates/shared/scripts/delivery/ci_tool.py`, `tools.json` | SHA-256 pinned tool cache shared by all runner users, safe under races |
| CI watching | `ci_watch.py`, `scripts/ci_runs.py`, `runner_health.py`, `runner_plan.py` | follow every workflow run of a commit, timing per job, runner health |
| VM checks | `templates/shared/scripts/delivery/vm_check.py` | boot a cloud VM under KVM, check it, snapshot and restore |
| Specs | `docs/features/runner-pool/spec.md`, `docs/architecture/DELIVERYSPEC.md` | the requirements behind the code, with IDs |
| Tests | `claude/plugins/delivery/tests/test_*pool*`, `test_dl310_windows_runner.py`, `test_dl315_runner_remove_token.py`, `test_ci_runs.py`, `test_dl115_ci_tool.py` | behaviour to keep; port each test with the code it covers |

## Learnings to keep (each one cost a failure)

1. Never cut a running job. Check for an idle machine right before a restart, not minutes earlier; a 3.5-minute image build after an idle check once cut a job. Drain with a timeout instead.
2. Queue tickets die. A cancelled job can leave a slot ticket behind; detect dead holders across hosts, not only by a local process id, and expire tickets.
3. Memory caps count page cache. A container at its memory cap may be full of page cache, not leaks; thread spawning can then fail. Measure, and size caps from the machine's real budget.
4. Windows runner services: the runner rejects `.cmd` hooks, so use `.ps1`. A hook must call a program installed for all users, not one in a user profile. A dedicated non-admin account per runner needs "log on as a service", denied interactive logon, and sometimes "create symbolic links" (cargo-xwin needs it). Lock the runner folder to that account. Remove an old runner with a remove token, check the result, and do it before locking the folder, so a failure leaves the old runner working.
5. PowerShell 5.1 mangles quoted arguments to native programs. Register scheduled tasks and services with the PowerShell cmdlets, not with cmd-quoted command lines.
6. A locked runner folder hides the runner from a non-admin health check; detect runners from the service list, and mark checks that need admin as such.
7. Labels must stay generic. A replaced runner lost a project-specific label that the workflows needed and the jobs queued forever. Target runners by repository variables, and check every workflow's `runs-on` against the labels that exist.
8. Wait for every run on the head commit before calling a change green, including runs started on demand.
9. Wine aborts when `TMPDIR` is set; give each runner user its own Wine prefix.
10. Agent users clash with image users on uid or gid; move the clashing system user at start.
11. Shared tool caches need sticky, world-writable folders and atomic renames so any runner user can add a tool during a race.
12. SELinux enforcing tests need a real VM (KVM); containers on WSL2 have no SELinux. A cached cloud image plus a snapshot boots in about one second.
13. Measure before heavy work on a shared machine, and hold new work near the budget; never stop work you did not start.

## Milestones

1. **One machine, persistent runners**: controller and agent, GitHub App sign-in, list repositories, turn the pool on for one repository, register persistent runners (Windows, Linux, macOS), slot queue and load budget, live overview. Ship it. It also has join codes and the install command, and the daily backup with the restore drill.
2. **Ephemeral containers on Linux and WSL2**: JIT runners, one container per job, images with layers, tool cache, doctor. Move this repository's CI onto it.
3. **More machines**: outbound agents on more machines (join codes are built in M1), placement by label and load, drain, agent auto-update.
4. **Windows**: service-account runners with the learnings above, then Hyper-V or WSL2 VMs.
5. **macOS**: macOS VMs (crsp's own code on Apple's Virtualization.framework, not Tart) and persistent runners on Apple Silicon.
6. **MicroVMs**: Firecracker or Cloud Hypervisor per job where KVM exists, with snapshots.
7. **Caches**: Actions cache server, git mirror, registry mirror.
8. **Product polish**: notifications, config as code with a dry-run plan, minutes-saved report, public docs site, installers per OS, code signing.

Each milestone ends with: a feature spec updated, tests for every requirement, the doctor covering the new parts, a release with notes, and a real run on real machines.

## Rules

- Plain, short English in docs and UI: one idea per sentence, no marketing words.
- Every behaviour change updates its feature spec and adds a change note in the same branch.
- Never store or log a secret. Never run a privileged container.
- Every destructive step (delete a runner, remove a machine, wipe a cache) names what goes and asks for a yes.
- Test as the real caller: a Windows service hook is tested as the service runs it, then the first real job is watched.
