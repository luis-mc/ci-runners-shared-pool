# CRSP265 report: dependency install, step one

Branch: CRSP265-dep-install (renamed CRSP265-integration at the end). Base: main at c808196 (CRSP248 in).
Scope: DEP1, DEP2, DEP3, DEP9, DEP14, DEP16 for Windows, Linux, macOS.

## Finding
CRSP248 already built DEP1/2/3/14/16 for Windows and Ubuntu/Debian. What was missing for "all systems":
- DEP9 prepare hook was empty.
- Linux families other than Debian/Ubuntu: "Do it myself" only.
- macOS: Docker Desktop not pinned; always "Do it myself", no detect of a stopped Docker Desktop app start.

## Plan
1. DEP9: `container.Backend.PreparePool`; agent `Config.PreparePool`; wired in cmd/crsp; Prepare in the installer.
2. DEP1-3, DEP16 Linux: dnf family (Fedora, RHEL-like) via Docker's rpm repo; zypper/others stay "Do it myself".
3. DEP1-3 macOS: Docker Desktop through Homebrew cask when brew exists; start stopped app; else "Do it myself".
4. DEP14: keep; test per OS.
5. Spec, change note, docs_check, CI (Windows, Linux, macOS).

## Verification status
- Local: Linux. Windows paths: tested with fakes on Linux (no Windows machine in this session).
- macOS: built and CI-tested only, NOT run on a real Mac.

## Log
- 2026-11-25 branch pushed, code read.
