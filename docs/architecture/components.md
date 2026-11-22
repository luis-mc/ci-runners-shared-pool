---
title: Product components
type: explanation
---
# Product components

crsp is **one program**, `crsp`, that runs in different modes. One machine runs the **controller**; every pool machine runs an **agent**; a person's desktop runs the **tray**. The diagram shows what each part holds and how they talk. A dashed box is planned, not built.

```mermaid
flowchart LR
  subgraph people["People"]
    owner["Owner<br/>browser"]
    cliuser["Scripts and terminal<br/>crsp CLI"]
  end

  subgraph github["GitHub"]
    app["GitHub App<br/>webhooks, API"]
    repos["Repositories<br/>CRSP_RUNS_ON_* variables, workflows"]
    actions["Actions service<br/>job queue, runner registrations"]
    releases["Releases<br/>installers, SHA256SUMS"]
  end

  subgraph ctl["Controller: crsp controller (one per pool)"]
    direction TB
    api["HTTPS API /api/v1<br/>OpenAPI contract"]
    webui["Web console<br/>React app, embedded"]
    auth["Sign-in, users, sessions, CLI tokens"]
    sched["Scheduler<br/>slots, load budget, queue"]
    runnersvc["Runners service<br/>pool runners, plans, leftovers"]
    reposvc["Repositories<br/>pool switch, policy, CRSP_RUNS_ON_*"]
    jobs["Jobs data<br/>runs, jobs, steps, forecast"]
    doctor["Doctor<br/>health checks, findings"]
    caches["Caches<br/>per-runner caches"]
    images["Images<br/>profiles, stacks, builds"]
    registry["Image registry /v2<br/>Distribution v3, staging, promote<br/>(planned, RES20)"]
    update["Self-update<br/>release check, restart"]
    backup["Backup and restore"]
    notify["Notifications"]
    tlsca["Own certificate authority"]
    store[("Store<br/>SQLite")]
    secrets[("OS secret store<br/>App key, tokens")]
    trayserver["Tray channel<br/>local only"]
  end

  subgraph machine["Pool machine: crsp agent (one per machine)"]
    direction TB
    agent["Agent<br/>reports load, runs commands"]
    backends["Isolation backends<br/>container (Docker, Podman), Windows service,<br/>Linux, macOS, VM clone, persistent"]
    runner["GitHub runners<br/>ephemeral and persistent"]
    cpucap["CPU cap, process cleanup, runner cache trim"]
    lancache["LAN cache<br/>served to other machines"]
    engine["Container engine<br/>job images crsp-pool:*"]
    hooks["Job hooks<br/>job-started, job-completed"]
  end

  subgraph desk["Desktop: crsp tray"]
    tray["Tray icon and menu<br/>status, update window"]
  end

  owner -->|HTTPS| webui
  webui --> api
  cliuser -->|HTTPS, token| api
  api --- auth
  api --- sched & runnersvc & reposvc & jobs & doctor & caches & images & update & backup & notify
  sched & runnersvc & reposvc & jobs & doctor & caches & images & backup --> store
  auth --> store
  reposvc & runnersvc --> secrets

  app -->|webhooks: workflow_job| api
  reposvc -->|set CRSP_RUNS_ON_* variables| repos
  runnersvc -->|register and remove runners| actions
  jobs -->|read runs and jobs| actions
  update -->|check and download| releases

  agent <-->|agent link, TLS from own CA| runnersvc
  tlsca -.->|certificates| agent
  agent --> backends --> runner
  backends --> engine
  agent --- cpucap
  runner -->|long poll for jobs| actions
  runner --- hooks
  hooks -->|job events| sched
  lancache <-->|cache hits| runner

  engine -.->|pull by digest, push to staging| registry
  registry -.-> store

  tray <-->|local channel| trayserver
  tray -->|opens| webui

  classDef planned stroke-dasharray: 5 5
  class registry planned
```

## The parts

| Part | Mode | Runs on | What it does |
|---|---|---|---|
| Controller | `crsp controller` | one machine of the pool, as a service | Holds all state (SQLite store), serves the web console and the API, talks to GitHub, decides which runners exist and where, runs the doctor, backups and the self-update. |
| Web console | embedded in the controller | the owner's browser | Every setup and admin step: machines, runners, repositories, images, jobs, doctor, settings. |
| CLI | `crsp <command>` | any computer with a token | The same API for scripts and the terminal. |
| Agent | `crsp agent` | every pool machine, as a service | Connects to the controller over the agent link (TLS with the controller's own CA), reports load, creates and removes runners through an isolation backend, caps CPU, cleans after jobs, serves the LAN cache. |
| Isolation backends | inside the agent | the pool machine | Container (Docker or Podman), Windows service, Linux, macOS, VM clone and persistent runners. |
| GitHub runners | started by the agent | the pool machine | The official runner; it long-polls GitHub for jobs that match its labels. The job hooks report job start and end to the controller. |
| Tray | `crsp tray` | a person's desktop | Status icon and menu, the update window; it talks to the controller over a local channel and opens the console. |
| Image registry (planned) | inside the controller, under `/v2/` | the controller | Owner decisions of 19 Nov 2026 (RES20): Distribution v3 embedded in the controller; a machine pushes only to its own staging area, the controller checks and promotes; other machines pull by digest instead of rebuilding. Never public. |

## How a job reaches the pool

1. A repository that opted in has `CRSP_RUNS_ON_*` variables that crsp sets to the pool's labels; its workflow uses `runs-on: ${{ vars.CRSP_RUNS_ON_LINUX }}`.
2. GitHub queues the job and sends a `workflow_job` webhook to the controller through the GitHub App.
3. The scheduler checks the slots and load budget; for an ephemeral runner the runners service tells an agent to start one.
4. The runner picks the job from GitHub, the job hooks report start and end, and the Jobs section shows it.
