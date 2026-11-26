# Security policy

## Supported versions

We support the latest stable release.
We also support the `edge` pre-release.
Older releases get no fixes. Please upgrade.

## How to report a vulnerability

Use GitHub private vulnerability reporting.
Open the repository's Security tab, then choose Report a vulnerability.
Do not open a public issue for a security problem.
Do not post details in public discussions or pull requests.

## What to expect

We acknowledge critical and high reports within 2 days.
We triage every report as set out in `docs/architecture/stack-profile.md`.
Critical and high issues are handled within 2 days; others within 7 days.
We will tell you when a fix ships.

## Scope notes

Secrets are never stored in the database or written to logs.
This covers GitHub App keys, webhook secrets and tokens.
If you see a secret in either place, report it as a vulnerability.
All product data stays on the operator's own machines.

## What a job can reach

A job on a pool runner runs in a container with limits. This is what it can still touch. Each item says what to do about it.

Every job container has a memory limit, a CPU limit and a process-count limit (`--pids-limit`), taken from the machine's pool size. A machine with no pool size gets defaults, so no container is uncapped. A job container has no host folder mounted except its own work folder, the read-only crsp binary and the cache folders you turned on. All job containers of a machine join one network, `crsp-jobs`, with no traffic between containers.

- **Services on the host's loopback address.** A container can reach them through the host gateway unless the machine's firewall blocks it. What to do: bind such services to a private socket, or block the container network to the host in the firewall.
- **Setuid programs inside an image.** `no-new-privileges` limits them, but the image you choose decides what is in it. What to do: use images you build or trust, and keep setuid programs out of them.
- **Who chooses where code runs.** Anyone with write access to a repository can edit its `CRSP_RUNS_ON_*` variables or its workflow, and so choose which pool runs their code. This is the same trust boundary as the workflow files. What to do: give write access only to people you trust with the pool, and protect the branches that hold workflows.
