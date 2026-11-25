---
title: runs-on.com versus CRSP (November 2026)
type: explanation
---
# runs-on.com versus CRSP (November 2026)

Study CRSP246, 25 Nov 2026. Owner request: compare runs-on with CRSP, say where each is better, and list what CRSP is missing.

## 0. Read this first: evidence limits

- `runs-on.com` and `betterstack.com` could **not** be opened in this session (the network policy blocks them). The product site, pricing page, docs, changelog and SLA were **not read**.
- Facts marked **verified (README)** come from the runs-on GitHub README ([github.com/runs-on/runs-on](https://github.com/runs-on/runs-on)), which the vendor wrote. Numbers in it are vendor claims.
- Facts marked **inferred** come from search snippets ([dev.to](https://dev.to/kalemi/runson-github-actions-runners-explained-k4i), AWS Marketplace and Product Hunt listings), or are reasoning. Where nothing was found the text says **not documented**.
- To upgrade this study: allow `runs-on.com` in the environment network settings and re-run CRSP246.
- CRSP facts cite spec IDs (`runners R5.28`) or task IDs (`CRSP74`) from `docs/features/*/spec.md` and `docs/project/registry.json`.
- Builds on, and does not repeat: [landscape-2026-11](../../architecture/landscape-2026-11.md) (which already lists RunsOn as a label-driven sizing source, gap G20), [components](../../architecture/components.md), [feature-crsp-features-research](../../project/feature-crsp-features-research.html) and [speed-comparison-workflow](../../guides/speed-comparison-workflow.md).

## 1. Summary

runs-on is a **cloud product**: it launches a fresh AWS server for each job in the buyer's AWS account. CRSP is a **pool manager for the owner's own PCs**. They solve the same problem in opposite ways.

Verdicts over 32 rows: **runs-on better 13**, **same 9**, **CRSP better 10**.

- runs-on wins on scale, isolation, sizing, cache, cost controls, reliability and docs.
- CRSP wins on cost at small scale, data staying home, Windows and macOS on real hardware, console, CLI, tray, updates and no lock-in.
- The best idea to take: let CRSP send overflow jobs to a cloud runner (G1 below). It is small, and it removes CRSP's weakest point, a pool that is full or off.

## 2. Architecture side by side

| | runs-on | CRSP |
|---|---|---|
| Where it runs | The buyer's AWS account (([README](https://github.com/runs-on/runs-on))) | The owner's PCs (components.md) |
| Control part | Control plane in AWS (([README](https://github.com/runs-on/runs-on))) | Controller on one pool machine |
| Runner | New EC2 server per job (([README](https://github.com/runs-on/runs-on))) | Agent starts runners (container, VM, persistent) |
| Pays | Licence plus AWS bill | Hardware and power already owned |
| GitHub link | Private GitHub App; `runs-on` label | GitHub App; `CRSP_RUNS_ON_*` variables |

## 3. Verdict matrix


### Functional

| Item | runs-on | CRSP | Verdict | Why | Evidence |
|---|---|---|---|---|---|
| Setup and onboarding | One CloudFormation stack or Terraform module, a private GitHub App, then change one `runs-on` value. ([README](https://github.com/runs-on/runs-on)) | Native installers, web console wizard, GitHub App wizard (docs/overview/product.md; operations spec). | **same** | Both are short. runs-on needs an AWS account; CRSP needs a PC. | verified (README) |
| OS and CPU coverage | Linux, Windows, ARM64, GPU. macOS: not documented. ([README](https://github.com/runs-on/runs-on)) | Windows, Linux, macOS runners (runners spec R5.13-R5.24); `gpu` label (R5.36); macOS VMs only partly built (R5.29). | **CRSP better** | CRSP covers macOS on real Macs. runs-on is cloud-only. | verified (README) / CRSP spec |
| Machine sizes | Label picks family, CPU, RAM, disk per job. ([README](https://github.com/runs-on/runs-on)) | Size labels small to xl (scheduling R5.67, CRSP89), limited to what the owner's PCs hold. | **runs-on better** | runs-on can rent any size. CRSP is capped by its hardware. | verified (README) / CRSP spec |
| Scaling and scheduling | Fresh EC2 runner per job, scales to zero. Vendor claim: 2.13M jobs in one customer day. ([README](https://github.com/runs-on/runs-on)) | Fixed pool, load budget, slots, scale-set demand (CRSP79), warm pool (CRSP84), queue reasons (CRSP76). | **runs-on better** | Cloud has no ceiling. CRSP queues when the PCs are full. | verified (README, vendor claim) / CRSP registry |
| Ephemeral runners | One fresh runner per job by default. ([README](https://github.com/runs-on/runs-on)) | Ephemeral and persistent runners; ephemeral Linux jobs on Windows (OPS89); VM-per-job planned (runners R5.28). | **same** | Both have it. runs-on makes it the only mode. | verified (README) / CRSP spec |
| Cache | S3 cache, Docker pull-through cache, EFS, EBS sticky disks. ([README](https://github.com/runs-on/runs-on)) | Per-runner caches, LAN cache between machines, image registry (caches and registry specs). Actions cache receiver specified, not built (caches R5.1). | **runs-on better** | runs-on has the Actions cache working today. | verified (README) / CRSP spec |
| Docker layer and image cache | Pull-through cache. ([README](https://github.com/runs-on/runs-on)) | Own image registry with promote, SBOM and scan (registry spec, REG8). | **same** | Different design, same job. CRSP adds scanning. | verified (README) / CRSP spec |
| Isolation | One EC2 VM per job. ([README](https://github.com/runs-on/runs-on)) | Per machine: microVM, VM, container or persistent (runners R5.2). Container hardening done (CRSP81). | **runs-on better** | A fresh VM per job is the strongest default. CRSP persistent runners are weaker. | verified (README) / CRSP spec |
| GitHub integration and labels | Private GitHub App; rich label syntax per job. ([README](https://github.com/runs-on/runs-on)) | GitHub App; repo variables `CRSP_RUNS_ON_*`; doctor reads every workflow's `runs-on` (doctor spec). | **same** | runs-on labels are richer. CRSP doctor catches mistakes. | verified (README) / CRSP spec |
| Fallback when capacity is gone | Cloud capacity; spot with on-demand fallback. ([README](https://github.com/runs-on/runs-on)) | Hosted fallback, opt-in per repository (CRSP74) when machines are off. | **runs-on better** | CRSP falls back to paid GitHub minutes. runs-on just rents another server. | verified (README) / CRSP registry |
| Observability | OpenTelemetry export, CloudWatch. ([README](https://github.com/runs-on/runs-on)) | Console jobs view, doctor, CI run insights, `crsp compare` (observability R5.34). No metrics export (observability spec, line 53). | **same** | runs-on exports to your tools. CRSP shows more in its own console. | verified (README) / CRSP spec |
| Cost controls | Spot, per-job cost estimate, AWS budget enforcement. ([README](https://github.com/runs-on/runs-on)) | Load budget 85% memory / 60% CPU, minutes saved and hosted cost avoided (CRSP92). | **runs-on better** | runs-on controls real money. CRSP has none to control. | verified (README) / CRSP registry |
| Admin console | Not documented in the README. Product site not readable. | Web console, users and roles, audit log (web-console spec). | **CRSP better** | CRSP has a full console. runs-on: not documented, so inferred. | inferred |
| CLI and tray | Not documented. | `crsp` CLI (cli spec) and a tray app (tray spec). | **CRSP better** | Inferred: not documented for runs-on. | inferred |
| Updates and operations | Stack update through CloudFormation or Terraform. Not documented in detail. | Verified self-update, backup and restore, recovery (operations spec). | **CRSP better** | CRSP updates itself. runs-on: inferred. | inferred |
| Multi-repo and org sharing | App installed on the org; Fleet uses an enterprise token. ([README](https://github.com/runs-on/runs-on)) | One pool serves every opted-in repository (runners R5.88). | **same** | Both share one pool across repositories. | verified (README) / CRSP spec |
| Policy and approved catalog | Fleet: platform team publishes a runner catalog in Terraform (early access). ([README](https://github.com/runs-on/runs-on)) | Per-repository policy and size classes. | **runs-on better** | Fleet is a formal catalog. Early access, though. | verified (README) |
| Public repo and fork safety | Not documented. | Private first, public only after a toggle (CRSP131); stop serving repos that turn public (CRSP82); fork guard (CRSP108). | **CRSP better** | Inferred for runs-on. | inferred |
| Debug access to a job machine | Controlled SSH access. ([README](https://github.com/runs-on/runs-on)) | SSH is used to join machines (CRSP123), not to debug a running job. | **runs-on better** | A gap in CRSP. | verified (README) / CRSP registry |
| Windows builds | Windows runners supported. ([README](https://github.com/runs-on/runs-on)) | Windows first: own Windows hardware, Windows-build layers (CRSP56), WSL2 and Docker disk compaction (CRSP136). | **CRSP better** | CRSP is built for Windows and Docker pools. | verified (README) / CRSP registry |

### Non-functional

| Item | runs-on | CRSP | Verdict | Why | Evidence |
|---|---|---|---|---|---|
| Cost model | Licence from €300/year, 15-day trial, free non-commercial use, AWS bill at cost. ([README](https://github.com/runs-on/runs-on)) | No licence, no cloud bill. The owner pays for power and hardware already owned. | **CRSP better** | At small scale CRSP costs nothing extra. | verified (README) |
| Performance and startup | Claims 30-50% faster than hosted runners (listing); single-thread up to 89% higher (README). Startup time per job: not documented. | Warm pool and hot local caches (CRSP84); no startup benchmark in this study. | **same** | Not comparable without a measured run. Use `crsp compare`. | inferred |
| Reliability | AWS availability; spot can be reclaimed, on-demand fallback. ([README](https://github.com/runs-on/runs-on)) | Depends on the owner's PCs and home network being on. | **runs-on better** | A data centre beats a desk PC. | inferred |
| Security and compliance | Runs in your AWS account under your IAM and network rules; private networking, static IPs. Certifications: not documented. ([README](https://github.com/runs-on/runs-on)) | OS secret store, own CA, certificate pinning, hashed one-time join codes (product.md). No certifications. | **runs-on better** | Cloud IAM and private networks are mature. Neither shows certifications. | verified (README) / CRSP docs |
| Data location and privacy | Control plane, runners, cache and credentials stay in the owner's AWS account. ([README](https://github.com/runs-on/runs-on)) | Everything stays on the owner's machines. | **CRSP better** | No cloud account holds the code or cache. | verified (README) |
| Maintenance burden | AWS stack plus licence renewals; no PC care. | Owner keeps PCs, Docker, disks and updates healthy. | **same** | Different burden, similar size. | inferred |
| Scalability limit | 2.13M jobs in one customer day (vendor claim). ([README](https://github.com/runs-on/runs-on)) | Bounded by the pool. Fine for one owner. | **runs-on better** | No comparison at CRSP's scale. | verified (README, vendor claim) |
| Portability and lock-in | AWS only. ([README](https://github.com/runs-on/runs-on)) | Any OS on any PC. GitHub only. | **CRSP better** | CRSP has no cloud lock-in. Both are tied to GitHub. | verified (README) |
| Licence | CloudFormation assets MIT; server and agent commercial, source access by sponsorship. ([README](https://github.com/runs-on/runs-on)) | Owner's own code. | **CRSP better** | CRSP is fully in the owner's control. | verified (README) |
| Support and SLA | Commercial licence. Support tiers and SLA: not documented here. | Owner only. | **runs-on better** | Inferred: a paid vendor gives someone to ask. | inferred |
| Docs quality | Public docs site with linked guides. Not read here. [README links] | Detailed specs, but written for builders. No public quickstart. | **runs-on better** | runs-on documents for users. | inferred |
| Time to first runner | Stack deploy plus one label change. ([README](https://github.com/runs-on/runs-on)) | Install, run wizard, add machine. | **same** | Both are minutes. Not timed. | inferred |

## 4. Where CRSP wins (so it is not lost)

- No licence and no cloud bill. Worked example in section 6.
- Code, cache and secrets never leave the owner's machines.
- Windows and macOS on real hardware, with Windows-build layers and Docker disk care.
- One console, a CLI and a tray, with verified self-update and backup.
- No AWS lock-in.

## 5. Gaps: what CRSP lacks

Ranked. Impact is usability or capability. Effort: S under a day of agent work, M a few days, L a milestone.

| Rank | ID | Missing feature | Why it helps the owner | Impact | Effort | Priority | Already exists as |
|---|---|---|---|---|---|---|---|
| 1 | G1 | Overflow to the cloud: let the hosted-fallback variable point at a runs-on (or other cloud) label when the pool is full or off | Jobs never wait for a busy or sleeping PC and never need paid GitHub minutes | high | S | P1 | Related: CRSP74 hosted fallback (built). New: a cloud target choice. |
| 2 | G2 | Metrics export (Prometheus or OpenTelemetry) | Graphs and alerts in tools the owner may already use | medium | S | P2 | Landscape G24; observability spec excludes it. No task. |
| 3 | G3 | Public quickstart and user docs page | A new user finds the first step in minutes | medium | S | P2 | Not found in registry. |
| 4 | G4 | Debug access to a running job (controlled SSH) | Fix a failing job without reproducing it | medium | M | P3 | Not found. CRSP123 is joining only. |
| 5 | G5 | Actions cache receiver on the pool | Faster `actions/cache` with no S3 bill | high | L | P3 | Exists: caches R5.1, M7. Do not re-propose. |
| 6 | G6 | Monthly savings report vs hosted and vs cloud runners | Shows the value of the pool in money | medium | S | P3 | Partly exists: CRSP92 compare; ci-run-insights spec (proposed). |
| 7 | G7 | Uptime and availability per machine | Shows why jobs went to the fallback | medium | S | P3 | Partly exists: OPS83 machine health; machines spec. |
| 8 | G8 | Package proxy (npm, PyPI, crates, apt) | Faster installs, less traffic | medium | M | P4 | Exists: caches spec, no milestone. |
| 9 | G9 | Free-form size labels (cpu=4, ram=8) | Match runs-on style sizing | low | S | P4 | Partly exists: size classes CRSP89, OPS73. |
| 10 | G10 | VM per job on Linux and Windows by default | Closer to runs-on isolation | high | L | P4 | Exists: runners R5.28 (M4). Do not re-propose. |

Not proposed again because they already exist as tasks (landscape-2026-11 section 4 proposals now Live): hosted fallback CRSP74, laptop policy CRSP75, why-waiting CRSP76, scale-set demand CRSP79, hardening CRSP81, public-repo guard CRSP82, warm pool CRSP84, size labels CRSP89, signed installers and SBOM CRSP90, speed comparison CRSP92.

## 6. Worked cost example

Assumptions (stated so you can change them):

- 10,000 minutes a month of 2-vCPU Linux jobs (about 330 a day).
- runs-on prices from its README (vendor numbers, US East spot, 30 GB gp3 disk, AWS prices change): $0.0009 a minute on `c8a.large`, against $0.0060 a minute on GitHub-hosted Linux. Source: ([README](https://github.com/runs-on/runs-on)).
- runs-on licence: from €300 a year, about €25 a month, same source. Taken as $25 here (rate not looked up).
- A GitHub fee of $0.002 a minute for self-hosted runners, announced for March 2026 according to a runs-on blog post seen in search results. **Not confirmed**; shown separately. If it applies it hits runs-on and CRSP alike.
- CRSP: the owner's PCs, so $0 cloud bill. Power and hardware are not counted.

| Per month | GitHub-hosted | runs-on | CRSP |
|---|---|---|---|
| Compute | $60 | $9 | $0 |
| Licence | $0 | about $25 | $0 |
| **Total** | **$60** | **about $34** | **$0** |
| With the unconfirmed $0.002 fee | $60 | about $54 | $20 |

Reading: runs-on cuts the hosted bill by about 40% at this size, because the licence is a fixed part. CRSP cuts it to zero but only while the PCs are on and large enough.

## 7. Options and proposals

### G1 Overflow to the cloud
- A. Do nothing; keep hosted fallback (CRSP74). No new work.
- B. **Recommended.** Let the fallback variable point at a runs-on label (or any cloud label the owner chooses). Small change in `repos`; the doctor shows the target. Uses runs-on, so it needs its licence.
- C. Build an AWS provider into CRSP. Large, and copies a product that exists.

### G2 Metrics export
- A. Nothing.
- B. **Recommended.** Read-only `/metrics` in Prometheus format.
- C. OpenTelemetry push. More setup for the user.

### G3 Public quickstart
- A. Nothing.
- B. **Recommended.** One page: install, add machine, first job, from `docs/guides/install.md`.
- C. Separate docs site.

### G4 Debug access to a job
- A. Nothing.
- B. **Recommended.** Keep a finished container for N minutes and offer a shell command in the console.
- C. Live SSH into a running job. Needs network and access rules.

### Roadmap
- **M1 (small, now):** G1 option B, G3, G2 option B.
- **M2:** G6 and G7 (savings and uptime views), G4 option B.
- **M3:** cache receiver (G5, exists), package proxy (G8), VM per job (G10, exists).

## 8. Sources
- runs-on README, verified: https://github.com/runs-on/runs-on
- Search summary of pricing and features (inferred): https://dev.to/kalemi/runson-github-actions-runners-explained-k4i
- Not readable here: https://runs-on.com, https://runs-on.com/pricing/, https://runs-on.com/docs/
