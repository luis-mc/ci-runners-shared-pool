---
id: 0008-microvm-monitor
title: Firecracker first for microVMs, behind the VM seam, with Cloud Hypervisor as the next option
status: proposed
date: 2026-11-28
supersedes:
superseded_by:
type: explanation
---
# Firecracker first for microVMs, behind the VM seam, with Cloud Hypervisor as the next option

Proposed by CRSP277 for owner review. Requirements: runners R5.30, R5.172 to R5.177. Builds on decision 0006.

## Context and problem

Decision 0003 makes a microVM per job the default on Linux with `/dev/kvm`.
It names Firecracker or Cloud Hypervisor without choosing.
Jobs come from pull requests, so the attack surface of the monitor matters; the start budget is 5 seconds from a snapshot (runners R6.5).

## Options considered

1. **Firecracker first.**
   Pros: the smallest device set; snapshot restore with lazy memory loading, proven at scale; the `jailer` ships with it.
   Cons: Linux guests only; no shared folder, so tools and caches go in as disks.
2. **Cloud Hypervisor first.**
   Pros: virtio-fs, Windows guests, device passthrough, hot-plug, live migration.
   Cons: a larger device model; the agent builds the jail itself.
3. **Both in M6.**
   Pros: the owner picks per machine.
   Cons: twice the tests and the doctor checks for one milestone.

## Decision

Proposed: option 1, behind the `vm.Hypervisor` seam of decision 0006 so option 2 can follow without a redesign.
The root disk is made from the container backend's OCI image; the snapshot is taken before any secret enters the guest; each restore gets a fresh clock, entropy and identity, its own jail and its own network namespace.

## Consequences

- Tools come as the squashfs disk of runners R5.80; each cache lane is an ext4 disk held by one microVM.
- The monitor binary and the guest kernel are pinned by version and SHA-256 and downloaded by crsp; no package manager is needed.
- Open questions AB1 to AB7 settle the details; the status becomes accepted when the owner signs off.
