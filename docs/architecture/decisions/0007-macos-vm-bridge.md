---
id: 0007-macos-vm-bridge
title: macOS VMs through a small signed helper on Virtualization.framework
status: proposed
date: 2026-11-28
supersedes:
superseded_by:
type: explanation
---
# macOS VMs through a small signed helper on Virtualization.framework

Proposed by CRSP276 for owner review. Requirements: runners R5.29, R5.161 to R5.167. Builds on decision 0003 (amended by D54: no Tart) and decision 0006.

## Context and problem

The macOS backend (`backend/macvm`) is built and tested with fakes: the two-VM limit, the APFS clone and the host check.
The call into Virtualization.framework is not built (`ErrNoBridge`).
The framework is an Objective-C and Swift API; using it needs cgo or a native helper, and a binary signed with the `com.apple.security.virtualization` entitlement.
The `crsp` binary is pure Go and cross-compiles today.

## Options considered

1. **A Go helper `crsp-vz` on the `Code-Hex/vz` bindings (MIT), started per VM, JSON lines on standard input and output.**
   Pros: Go like the rest; only the helper needs cgo and the entitlement; `crsp` keeps cross-compiling.
   Cons: a second binary to sign and ship; depends on a third-party binding.
2. **A Swift helper with the same protocol.**
   Pros: Apple's language and samples; no binding layer.
   Cons: a second language and toolchain in the repository.
3. **cgo inside `crsp`.**
   Pros: one binary.
   Cons: breaks cross-compiling; the whole agent carries the entitlement.

## Decision

Proposed: option 1.
The guest image comes from an IPSW and is set up without clicks on its attached data volume.
Each clone gets a new machine identifier.
`macvm` stays off for users until the first real-Mac run passes its checklist (runners section 5.17).

## Consequences

- The macOS package ships and signs the helper; releases need the owner's Developer ID (open question AA2).
- CI cannot boot a macOS guest (GitHub's macOS runners are VMs); the boot is proven only on a real Mac.
- Open questions AA1 to AA7 settle the details; the status becomes accepted when the owner signs off.
