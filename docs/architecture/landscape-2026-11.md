---
title: Similar projects and what crsp should do next (November 2026)
type: explanation
---
# Similar projects and what crsp should do next (November 2026)

Research task CRSP73, 15 Nov 2026. Four research passes: a close reading of two projects, a wide search, a fact check of every outside claim, and a check of every idea against crsp's own specs and code. Ideas only: crsp keeps its own code and copies nothing.

## 1. The landscape

| Project | Licence | What it is | Windows | Closest to crsp in |
|---|---|---|---|---|
| [ephemerd](https://github.com/ephemerd/ephemerd) | MIT, Go | One binary, one ephemeral job per container or VM. Windows jobs in Hyper-V isolated containers, Linux jobs in a Hyper-V VM through HCS, macOS via Virtualization.framework. Runs as LocalSystem. Polls by default, webhook optional. Also Forgejo, Gitea, GitLab, Woodpecker. No web UI or tray; Prometheus only. | Yes | Technical twin. The project to watch. |
| [local-ci-pool](https://github.com/Nezhinskiy/local-ci-pool) | MIT, Go | Ephemeral container runners on the owner's Macs for private personal repos. GitHub is the control plane. Hosted fallback by heartbeat variable, no token. Detailed SECURITY.md. One maintainer, releases daily. | No (macOS only, Linux arm64 jobs) | Same audience: your own machines, your private repos. |
| [RunnerRunner](https://github.com/Redth/RunnerRunner) | none (all rights reserved) | Fleet manager: Linux server (PostgreSQL, Orleans, Blazor) plus host workers over gRPC. Docker, Tart and native backends. GitHub, Gitea, Azure DevOps. Webhooks with polling backfill, JIT runners, warm pools. | Host worker yes | Fleet console and host wizards. |
| [switchable-ci-runner](https://github.com/dumaat/switchable-ci-runner) | MIT, Rust | Windows tray: Automatic / Always / Disabled, CPU cap 25-100%, on battery send jobs to GitHub, popup when you use the PC. Fallback through a route job that lists runners with a stored token. No isolation. | Yes | The tray and "my PC is busy" UX. |
| [Unyoke (tart-runner-fleet)](https://github.com/vitalyiegorov/tart-runner-fleet) | AGPL-3.0 + commercial | Multi-node fleet (Tart on macOS, Podman on Linux). Admission by memory, swap and CPU pressure that fails closed. Rollout modes observe, shadow, canary, authority. SQLite WAL with an operation outbox. LAN cache gate. | No | Scheduler and safe rollout. |
| [runscaler](https://github.com/ysya/runscaler) | MIT, Go | Built on actions/scaleset. Docker or Tart. Own cache-v2 receiver. | No | Local Actions cache. |
| [garm](https://github.com/cloudbase/garm) | Apache-2.0, Go | Mature runner manager (465 stars). Providers are standalone executables. Webhook pools and native scale sets, SQLite, GitHub, GHES, Gitea. | Through providers | Plug-in backends. |
| [actions-runner-controller](https://github.com/actions/actions-runner-controller) | Apache-2.0, Go | GitHub's reference, Kubernetes only. | Linux only | Scale-set API usage. |
| [actions/scaleset](https://github.com/actions/scaleset) | MIT, Go | Go client for the runner scale-set API, extracted from ARC, usable without Kubernetes. Public Preview: interfaces may change. | n/a | The library for crsp's M2 demand. |
| [melodic-software/ci-runner](https://github.com/melodic-software/ci-runner) | MIT, Go | Personal fleet on Windows hosts, one fresh Linux container per job, scale sets, JIT config over stdin. | Host yes | JIT handling. |
| [RunsOn](https://github.com/runs-on/runs-on), [Ubicloud](https://github.com/ubicloud/ubicloud), [terraform-aws-github-runner](https://github.com/github-aws-runners/terraform-aws-github-runner) | MIT / AGPL / MIT | Cloud VMs in your account or theirs. | Varies | Label-driven sizing (`runs-on: .../cpu=4`). |
| [tartelet](https://github.com/framna-dk/tartelet), [graftery](https://github.com/diranged/graftery) | MIT | macOS menu-bar apps that run ephemeral runners. | No | Native app UX on macOS. |
| [Tart and Orchard](https://github.com/openai/tart) | FSL-1.1-ALv2 (source available, Apache 2.0 later) | macOS VMs. Moved from Cirrus Labs to OpenAI. | No | Licence risk for any Tart-based macOS backend. |
| [runner-fallback-action](https://github.com/jimmygchen/runner-fallback-action) | MIT, archived (fork: mikehardy) | Picks a hosted label when self-hosted runners are offline; needs a token that lists runners. | n/a | Fallback pattern. |

Facts checked against GitHub's docs: `runs-on` may read `vars.*`; GitHub has no built-in fallback, and a queued job waits up to 24 hours before it is cancelled. The cache-v2 protocol is not documented for self-hosting; compatible receivers are unofficial and can break when GitHub changes it.

## 2. Where crsp already leads

- The only product that combines a Windows-first service, a web console, a tray, a GitHub App wizard, native installers (MSI, deb, rpm, pkg) and verified auto-update (SHA-256 plus build provenance, built).
- A pool across several of the owner's machines with the isolation chosen per machine (microVM, VM, container, persistent), not one host type.
- Repository variables (`RUNS_ON_*`) as the switch, with a doctor that reads every workflow's `runs-on`.
- Secrets in the OS secret store; join codes single use, hashed, 15 minutes; certificate pinning (built).
- A load gate with the owner's 85% memory / 60% CPU budget, light slots and per-repository policy (built).
- A fake GitHub server and recorded fixtures for tests (built).

## 3. Gaps, checked against crsp's specs and code

Status: **built**, **specified** (in a spec, not built), **partial**, **absent**. Requirement IDs are crsp's own.

| # | Gap | Status in crsp | Seen in |
|---|---|---|---|
| G1 | Jobs run on GitHub when every crsp machine is off or asleep | absent: `RUNS_ON_*` is static; a dead pool makes jobs queue up to 24 h | local-ci-pool, switchable, runner-fallback-action |
| G2 | Demand by the scale-set API with JIT runners | specified (github-integration R5.35, M2); M1 polls (R5.37, built) | ARC, actions/scaleset, runscaler, ci-runner |
| G3 | JIT config never on disk or in `docker inspect` (stdin only) | specified loosely (R5.34 allows a short-lived file) | ci-runner |
| G4 | Ask GitHub "is it idle?" before removing a runner; a 422 for a busy runner means "leave it" | partial: drain never cuts a job (built); no 422 path | RunnerRunner |
| G5 | Automatic sweep of offline runner registrations | specified only after a yes (R5.33, not built) | RunnerRunner |
| G6 | Watchdog for a demand source that goes quiet while jobs queue | absent | local-ci-pool issue #30, RunnerRunner issue #102 |
| G7 | "Why is this job waiting?" per queued job in the console | partial: the gate logs reasons (built); console view is M3 (scheduling R5.48) | RunnerRunner |
| G8 | Admission when load cannot be measured | built as fail-open on purpose (scheduling R5.22, R5.61: a broken install must not fail CI) | Unyoke fails closed |
| G9 | Laptop and desk policy: pause on battery, ask when the user is active, tray mode Automatic / Always / Paused, CPU cap | partial: no quiet hours (dropped, OPS51) | switchable |
| G10 | Refuse and drain when a served repository turns public; fork guard | partial: fork approval setting built, hook guard not built (R5.26) | local-ci-pool |
| G11 | Container hardening: non-root, no Docker socket, no host folders, memory, CPU and process limits, no traffic between job containers | specified in part (runners R5.27, M2); limits and network isolation not written | local-ci-pool SECURITY.md |
| G12 | Docker inside a job through a throwaway VM cloned from a base | specified (R5.28 Hyper-V differencing disk M4, R5.29 macOS M5) | local-ci-pool docker class, ephemerd |
| G13 | Disk floor before a VM or job starts | partial: cache volume floor built (caches R5.38) | local-ci-pool |
| G14 | Warm pool: N ready runners or pre-booted VMs | absent | RunnerRunner, terraform-aws-github-runner |
| G15 | Local Actions cache service and LAN sharing | specified (caches R5.1, M7); protocol is unofficial | runscaler, Unyoke |
| G16 | Shared tool cache, package proxy (npm, PyPI, crates, apt) | partial: per-runner tool folder built; shared M2; proxy has no milestone | — |
| G17 | Safe rollout of routing: observe, shadow, canary, then switch | partial: manual move by the runbook; no parity gate (CRSP227) | Unyoke |
| G18 | Backends as separate programs | partial: one in-process interface (runners R5.8) | garm |
| G19 | Windows jobs in Hyper-V isolated containers | specified (M4 VMs); containers not considered | ephemerd |
| G20 | Labels that size the runner (`cpu=4`, `ram=8`) | absent | RunsOn |
| G21 | Signed installers (Authenticode, notarization), SBOM, install script that checks the attestation | partial: attestation checked by `crsp update` (built); signing M8; script checks SHA-256 only | local-ci-pool install.sh |
| G22 | Recovery path for an agent stuck on an old version | absent | RunnerRunner manual update wizard |
| G23 | Benchmark: this job on crsp vs on GitHub | absent; minutes saved is built (observability R5.15) | local-ci-pool bench |
| G24 | Prometheus metrics | out of scope today (observability spec) | ephemerd |
| G25 | Other forges (Gitea, Forgejo) and GHES | out of scope (GitHub only) | garm, ephemerd, RunnerRunner |

## 4. Proposed changes

Ranked by value for the owner's case (private repositories, several home and office PCs, Windows first). Effort: S under a day of agent work, M a few days, L a milestone.

### Now (before more parity work)
1. **Hosted fallback (G1), M.** Each machine's agent writes a heartbeat; the controller rewrites `RUNS_ON_<OS>` to the hosted label when no machine of that OS has a fresh heartbeat, and back when one returns, after a grace time. No route job, no extra token: crsp already owns the variables. Doctor shows which way each variable points and why. Risk: a write-access user can edit variables; same trust boundary as workflows.
2. **Laptop and desk policy (G9), M.** Tray mode Automatic / Always / Paused; pause on battery; an "I'm using this PC" pause with a timer; a CPU cap per machine. Paused machines drop out of the heartbeat, so jobs go to GitHub or another PC.
3. **Why is this job waiting (G7), S-M.** Bring scheduling R5.48 forward: the console shows the first blocking reason for each queued job (no machine for this OS, budget, slot, label mismatch, paused).
4. **Quiet-demand watchdog (G6), S.** A doctor check and console notice when jobs are queued on GitHub but crsp has seen no demand for N minutes; restart the demand loop once.
5. **Install script checks the attestation (G21 part), S.** Same check `crsp update` already does, before first install.

### Next (with M2 ephemeral runners)
6. **Scale-set demand on actions/scaleset (G2), M.** Pin a version; wrap it behind crsp's existing `Demand` seam because the library is in Public Preview. Keep polling as the fallback.
7. **JIT config over stdin only (G3), S.** Tighten R5.34: never a file, never in env visible to `inspect`.
8. **Remove safely (G4) and sweep stale registrations (G5), S.** Idle check, deregister before stop, treat 422 as busy; sweep offline runners older than a set age without asking each time (one setting, on by default).
9. **Container hardening written into the spec (G11), S to spec, M to build.** Add memory, CPU and process limits and a job network with no traffic between containers to runners R5.27; write a SECURITY.md that lists what a job can still reach.
10. **Public-repo guard (G10), S.** Re-check visibility before each runner starts; drain and stop serving a repository that turned public; finish the fork hook guard.
11. **Disk floor for VMs and jobs (G13), S.** Hold new work under a free-disk floor; doctor warns before it bites.

### Later (M4 and beyond)
12. **Warm pool (G14), M.** Keep N runners or pre-booted VMs ready per OS on machines with spare budget.
13. **Windows Hyper-V isolated containers (G19), L.** Evaluate next to the M4 Hyper-V VM backend; lighter than a full VM for Windows jobs.
14. **Docker in a job through a cloned VM (G12), part of M4/M5.** Base built from the default branch only, copy-on-write clone per job, orphan sweep at start.
15. **Shared caches (G15, G16), L.** LAN tool cache first (owned protocol), package proxy second, Actions cache-v2 receiver last behind a contract test, because its protocol is unofficial.
16. **Safe rollout modes (G17), M.** Observe (log what crsp would do), shadow, canary one repository, then switch. Fits the open dev-standards migration gate.
17. **Size labels (G20), S-M.** `RUNS_ON_LINUX_LARGE` style variables or `crsp/cpu=4` labels mapped to machines with room.
18. **Signed installers and SBOM (G21), M.** Authenticode for the MSI, notarized pkg, SBOM in each release.
19. **Agent recovery path (G22), S.** A documented reinstall-in-place command and a console card for an agent that stays on an old version.
20. **Benchmark (G23), S.** Run one chosen workflow on crsp and on GitHub, show both times next to minutes saved.

### Decisions for the owner
- **G8 fail-open vs fail-closed.** Today an unmeasurable load still runs the job, so CI never fails because crsp broke. Fail-closed would protect the PC but can stall CI. Option: fail closed with a short timeout, then run.
- **G24 metrics endpoint.** Out of scope today; a read-only Prometheus endpoint is cheap if wanted.
- **G25 other forges.** Out of scope today; garm and ephemerd show the demand exists.
- **macOS VM backend and Tart's new licence.** Tart is now OpenAI's, under FSL-1.1-ALv2. Use Virtualization.framework directly (as ephemerd does) or accept FSL.

## 5. Risks seen in others to avoid
- A demand source can fail quietly while health stays green (local-ci-pool #30, RunnerRunner #102): covered by proposal 4.
- Runners re-provisioned for jobs that had already finished, and limits reverting after a sync (RunnerRunner #103-#105): crsp tests should cover both.
- A removed base image makes every start fail until rebuilt (local-ci-pool #31): the doctor should check the base image exists.
