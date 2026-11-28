---
id: 0006-vm-per-job
title: A fresh VM per job through one VM core, with the hypervisor each host OS ships
status: proposed
date: 2026-11-28
supersedes:
superseded_by:
type: explanation
---
# A fresh VM per job through one VM core, with the hypervisor each host OS ships

Proposed by CRSP275 for owner review. Requirements: runners R5.28, R5.152 to R5.160.

## Context and problem

Decision 0003 puts a VM per job first on Windows and second on Linux, but no VM backend exists.
What exists: the clone runner for jobs that need Docker (`vmclone`), the tool image hook (`ToolBooter`), and the test VM boot `crsp vm` with QEMU and KVM.
The owner rule says crsp sets everything up itself from the console.

## Options considered

1. **QEMU started directly by the agent on Linux, Hyper-V on Windows, one shared VM core.**
   Pros: reuses the `crsp vm` command builder and cache; no daemon; the hypervisors are what the OS ships.
   Cons: crsp writes the lifecycle code (stop and status) itself.
2. **libvirt on Linux, Hyper-V on Windows.**
   Pros: mature lifecycle and tooling.
   Cons: one more daemon and package; XML that differs across distribution versions; the test boot stays on plain QEMU, so two code paths.
3. **Cloud Hypervisor for every Linux VM (the same monitor as the microVMs), Hyper-V on Windows.**
   Pros: one monitor on Linux for VMs and microVMs.
   Cons: Windows guests on it are less proven; the test boot moves off QEMU.
4. **A third-party VM product (VirtualBox, VMware).**
   Pros: one tool on every OS.
   Cons: licences, an install crsp does not own, weaker automation.

## Decision

Proposed: option 1.
Every VM backend implements `backend.Backend` once in `backend/vm`, with one `vm.Hypervisor` seam per host OS.
Every VM job is a copy-on-write clone of a base disk keyed by its contents.
The JIT config reaches the guest only over vsock or Hyper-V sockets, never a disk.
The base disk is built only from the default branch.

## Consequences

- The `crsp vm` QEMU code moves into a shared package used by the test boot and the job VM.
- A Windows guest needs an ISO and a licence the owner gives; crsp never ships Windows.
- The macOS backend (decision 0007) and the microVM backend (decision 0008) plug into the same seam.
- The owner's answers to runners open questions Z1 to Z10 settle the details; the status becomes accepted when the owner signs off.
