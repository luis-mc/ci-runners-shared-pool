# CRSP332: pool offline and Windows "failed register", diagnosis and plan

Read-only diagnosis from the repo at `origin/main` (3ca50336, 26 Nov 2026). The PC was not reachable. Nothing here is confirmed on the machine; each cause says how the owner confirms it with one command.

## What the repo shows (read this first)

1. `pool-add`, `pool-add-agent` and `pool-refresh` are **not in this repository**. `git grep` finds them only in `docs/project/registry.json` (OPS94, OPS95), `docs/architecture/dev-standards-handover.md` (lessons 26, 27) and `docs/features/dependency-setup/spec.md:58` ("`pool-refresh` is the dev-standards script"). They are scripts of the old dev-standards pool repo.
2. Branch `origin/OPS94-pool-agent-fixes` holds one commit, `docs/work/OPS94/REPORT.md`, status **BLOCKED, no code**: the fix belongs to the dev-standards repo, which was not attached. There is **no `OPS95-pool-refresh-missing` branch** on origin, and no PR for OPS94 or OPS95. So neither fix exists anywhere yet.
3. OPS93 (verb `configure`, PR merged, v0.1.27) and OPS96 (PR #414, `092dd84d`, direct `Runner.Listener.exe` call with 5 min limit) are merged on `main`.
4. The crsp pool code that does the same job as the old scripts is `internal/backend/container/persistent.go` (`StartPersistent`, `ensurePersistentPool`) and `images/persistent/add-runner.sh`. It creates a missing container (`persistent.go:178-196`), so "missing container" in crsp is not the OPS95 bug.
5. No open PR touches the pool start or the Windows register path (open PRs: #471, 470, 469, 468, 466, 465, 464, 461, 456, 455, 454, 450, 449; none is about register or pool start). The only one that helps this outage is #450 (CRSP326, delete an offline pool runner's registration).

Which tool made the "pool" on GameRoomPC matters. The owner must say (checklist step 0): the old dev-standards pool (`pool-add`, compose), or crsp's own `crsp-persistent-pool`. The causes below are split by that.

## (a) Pool containers absent or not registered

| # | Cause | File and line | Confirm on the PC (one command) | Fix / holder |
|---|-------|---------------|---------------------------------|--------------|
| a1 | Docker engine not running. Docker Desktop starts only when a user signs in; after the Windows Update restart the Linux pool stays down (handover lesson 33). crsp never starts an engine. | `docs/architecture/dev-standards-handover.md` lesson 33; `docs/features/dependency-setup/spec.md:50` ("never starts or changes an engine"); `internal/agent/backends.go` `detectDocker` | `docker version` (a Server section means up) | Sign in / start Docker Desktop. Boot-start of Docker is not built in crsp. No PR. |
| a2 | Pool container Exited after a Docker/WSL restart (no `/dev/kvm` at start, lesson 25, 28). | handover lessons 25, 28; `internal/backend/container/poolstart.go:189-203` (`kvmFacts` waits a bounded time); `persistent.go:158-164` starts an Exited pool | `docker ps -a --filter name=pool --format "{{.Names}} {{.Status}}"` | `docker start <name>`. crsp: next `StartPersistent` does it. Old pool: compose loader service (lesson 25). |
| a3 | **Container gone** (volume loss 24 Nov, "pool main volume loss"). Old `pool-refresh` builds the image then fails "could not compare the image ids" and does not create the container. | OPS95 in `docs/project/registry.json:6686`; `docs/features/dependency-setup/spec.md:58`; script in dev-standards repo, not here | `docker ps -a` shows no pool; `docker images` shows the new image | Hand fix: `docker compose up -d` in the old pool folder. Code fix: OPS95, **not written** (no branch). crsp path already creates it: `persistent.go:178-196`, `refresh.go` ("no pool container yet"). |
| a4 | **Agents volume lost, runners unregistered**: `pool-add-agent` says "already added" because the slug is in `/agents.json` but `/agents/<slug>` is missing. | OPS94 in `docs/project/registry.json:6675`; script in dev-standards repo | `docker exec <pool> sh -c 'cat /agents.json; ls /agents'` (slug listed, folder missing) | OPS94, **not written**. Hand fix: remove the slug from `/agents.json`, run `pool-add-agent` again. |
| a5 | crsp path: registration of the in-container runner failed (token expired, 1 h; wrong URL; no network). `add-runner.sh` removes only a folder it made. | `internal/backend/container/persistent.go:131` (step "register"); `images/persistent/add-runner.sh` (`config.sh --unattended --replace`) | `docker exec crsp-persistent-pool ls /state/runners.d` and `docker logs --tail 50 crsp-persistent-pool` | Add again from the console (new token). No PR needed. |
| a6 | Stale GitHub session after unclean stop: "A session for this runner already exists" for about 2 min (normal), longer means a second listener. | handover lessons 19, 26, 29; runners R5.46, R5.67; `internal/doctor/checks/runner.go:375` `runnerListener` | `docker exec <pool> sh -c "ps -eo pid,args \| grep [R]unner.Listener"` (more than one per runner = bug) | Wait 2 min; if longer, stop pool and `docker start`. Doctor check built (HND23). |
| a7 | Offline registrations left in GitHub block re-adding the same name (`--replace` helps, a busy ghost does not). | PR #450 (CRSP326), `internal/runners/pool.go:82,120` | GitHub, repo Settings, Actions, Runners: grey "Offline" entries | Merge #450, delete the registration from the console. |
| a8 | Docker data disk full or WSL VM not started. | handover lesson 24; machines spec R5.66 | `wsl -l -v` and `docker system df` | Free space / compact (lesson 24). |
| a9 | Agent offline: controller marks machine offline after 30 s without heartbeat; an offline machine gets no job. | machines spec R5.35 (`docs/features/machines/spec.md:225`) | `Get-Service crsp*` (Status Running?) and `crsp doctor` | Start the agent service. |

## (b) Windows runner gameroompc-win-1, "failed register"

The step name `register` is `internal/backend/windows/windows.go:371`. The error text is `register: runner exited N: <last lines>` (`windows.go:513-515`) or `register: Runner.Listener configure did not finish within 5m0s ... last output: ...` (`windows.go:488`). The message tail tells which cause. (The console must show the real text; OPS96 added it.)

| # | Cause | File and line | Confirm on the PC | Fix / holder |
|---|-------|---------------|-------------------|--------------|
| b1 | **PC runs a crsp agent older than v0.1.27/OPS96.** Register called `config.cmd` or `Runner.Listener.exe` without `configure`: "Not configured", exit 1 (OPS93, OPS96). | `windows.go:459-466` `ConfigureArgs`; `windows.go:526` `ListenerCommand`; `docs/features/runners/changes/OPS93.md`, `OPS96.md` | `crsp version` or `(Get-Item "$env:ProgramFiles\crsp\crsp.exe").VersionInfo.FileVersion`; needs a build with commit `092dd84d` | OPS93 + OPS96 are merged; update the agent (tag after v0.1.27; **the tag needs the owner's yes**, registry.json:5445). |
| b2 | Registration token expired (valid 1 h) or wrong scope; add retried with an old plan. Listener says "Http response code: NotFound/Unauthorized". | `windows.go:371` (token = `cfg.Registration`) | Console error text; or `Get-Content <runner folder>\_diag\Runner_*.log -Tail 40` | Add the runner again (new token). No code. |
| b3 | Runner folder left by an earlier failed add (half-configured, `.runner` present or missing, name taken). | `windows.go:296-335` (`persistent.Decide`: fresh, re-register, rewrite env); `internal/backend/persistent` | `Get-ChildItem C:\Work\runners` (or the runner root) and `dir <folder>\.runner` | Remove the runner in the console (drain and remove), then add. Deleting the folder by hand is the last resort. |
| b4 | Name `gameroompc-win-1` already registered on GitHub by an offline ghost: listener exits "runner already exists". | `windows.go:371`; PR #450 | GitHub runners page (grey entry of that name) | Merge #450 or delete the entry on GitHub, add again. |
| b5 | Listener did not finish in 5 min (antivirus/Defender scans the new folder, network proxy, TLS inspection). Message says "did not finish within 5m0s". | `windows.go:472` `RunnerStepTimeout`; `windows.go:497-520` | `Test-NetConnection github.com -Port 443` and `Get-MpComputerStatus \| select RealTimeProtectionEnabled`; run `Runner.Listener.exe configure --check --url https://github.com/<owner>/<repo> --pat <x>` to test the path | Exclude the runner folder from Defender; fix proxy. No code. |
| b6 | `Runner.Listener.exe` missing: unpack failed or archive blocked (the runner zip pinned in `internal/runners/pins.json`; download failed offline). | `windows.go:355-366` (`scriptExtract`), `internal/runners/pins.json` | `Test-Path "<folder>\bin\Runner.Listener.exe"` | Check the download and the disk; add again. |
| b7 | Failure is really a **later step**, shown as "failed register" in the list: `register service` (`windows.go:427`), `makeAccount`, rights grant ("Log on as a service" missing, handover lesson 32), tool-folder share. | `windows.go:376-430`; `scripts.go`; `internal/backend/windows/rights*.go` | `Get-Service` for the runner's service; `Get-WinEvent -LogName System -MaxEvents 20 \| ? ProviderName -like "*Service Control*"`; console error text gives the step name | Re-add; if rights are missing, the doctor `runner.service` check names it. |
| b8 | Agent service account lacks admin rights (cannot make the account, set ACL, `takeown`). | `windows.go:376-400`; machines spec | `whoami /groups` in the agent's session; `Get-CimInstance Win32_Service -Filter "Name='crsp-agent'" \| select StartName` | Run the agent as an admin account. |
| b9 | Windows runner needs Docker too? **No**: the Windows runner is a native service; it does not depend on a Docker engine. If both are down, causes are independent. | `windows.go` (no Docker call); `internal/backend/container/persistent.go:152` (Linux only) | n/a | Treat (a) and (b) separately. |

## Owner checklist at GameRoomPC, in order

Run each step, note the output, stop at the first red one and fix it before moving on.

0. Decide which pool is meant: old dev-standards pool (compose, `pool-add`) or crsp (`crsp-persistent-pool`). Run `docker ps -a --format "{{.Names}} {{.Status}}"`.
1. Host is up and signed in: `docker version`. No Server section: start Docker Desktop (a1), wait until `docker version` answers.
2. WSL and disk: `wsl -l -v` (state Running) and `docker system df` (a8).
3. Pool container: `docker ps -a --filter name=pool --format "{{.Names}} {{.Status}}"`.
   - Exited: `docker start <name>` (a2). Wait 2 min for stale sessions (a6).
   - Absent: old pool, in the pool folder `docker compose up -d` (a3). crsp pool: add any runner from the console (it makes the container).
4. Registered inside the pool: old pool `docker exec <pool> sh -c 'cat /agents.json; ls /agents'`. Slug in the list but folder missing: remove the slug from `/agents.json`, run `pool-add-agent` again (a4). crsp pool: `docker exec crsp-persistent-pool ls /state/runners.d`.
5. GitHub side: open Settings, Actions, Runners of each repo. Delete grey offline entries of the pool and of `gameroompc-win-1` (b4, a7). Quicker once PR #450 is merged and deployed.
6. Agent service: `Get-Service crsp*` Running; version has OPS96: `crsp version` must be a build with `092dd84d` in it (b1). If older, update the agent first (needs the owner's yes for the tag).
7. Windows runner: in the console, remove `gameroompc-win-1` (drain, remove) if a folder is left (b3), then Add runner again so it gets a fresh token (b2). Read the exact message when it fails:
   - "Not configured": step 6 not done (b1).
   - "did not finish within 5m0s": b5.
   - "register service" or access denied: b7, b8.
8. Check: `Get-Service` shows the runner service Running; GitHub shows the runner Idle; `crsp doctor` shows no red on `runner.*`.
9. When Windows and Linux runners are Idle: tell the orchestrator. CRSP226 can go on (switch comics-organizer, home-automation, lan-link, moobable to `crsp-home`).

## Merge order of the open PRs

None of the open PRs repairs the register path; they need no merge for the pool to come back. Suggested order (all need green CI on their head, no auto-merge, red waits for the owner):

1. **#450 CRSP326** delete an offline pool runner's registration: helps a4/a7/b4, merge first.
2. #455 CRSP261 target-neutral cloud overflow label, #454 CRSP264 size labels: they change labels that the `crsp-home` switch in CRSP226 uses, so merge before CRSP226.
3. #464 CRSP324 CI areas, #468 CRSP318 (draft), #465 CRSP317 (draft, Go caches on Windows runners, matters for the Windows runner once it is back), #449 CRSP323 (draft): CI speed, after the above; drafts need the owner to mark ready.
4. Docs and observability: #456 CRSP258, #470 CRSP271, #471 CRSP271b, #469 CRSP269, #466 CRSP260, #461 CRSP296: no effect on the outage, any order, status sweep (#469) last.

Not covered by any PR, to create:
- **OPS94 and OPS95**: attach the dev-standards repo to the task (the OPS94 report asks for this) or decide the old scripts are retired in favour of crsp's own path (`persistent.go:178-196` already creates a missing container). Until then, the hand fixes in a3 and a4 are the cure.
- A tag with OPS96 (after v0.1.27): the owner's yes is needed.
- Docker start at boot without sign-in (a1): handover lesson 33, not built in crsp.

## Top 3 likely causes

1. a1/a2 Docker engine down or the pool container Exited after a reboot (lessons 25, 28, 33), the usual reason for "pool offline" after a Windows Update restart.
2. a3/a4 Container and agents volume lost on 24 Nov: `pool-refresh` and `pool-add-agent` fail to recreate (OPS94, OPS95). No code fix exists; the hand fix is in the checklist.
3. b1 the PC agent is older than OPS96, or b2/b4 an expired token / offline ghost registration of `gameroompc-win-1`. Fix: update the agent, delete the ghost (PR #450), add the runner again.
