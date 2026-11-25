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
- 2026-11-25 built: DEP9 PreparePool (+tests), dnf path (Fedora, Rocky/Alma), Homebrew cask path (macOS) + wait for the app, start of stopped Docker (macOS, WSL distro), undo kinds rpm-*/brew-cask, doctor mac text (DEP14), DEP16 tests. Local: go test of agent, install, depsetup, backend, doctor, cmd green on Linux. Windows paths: fakes only. macOS: not run on a real Mac.
- Spec section 7.1 and 11 updated; change notes dependency-setup and doctor written by hand in the docs_fold format: luismc-docs scripts (docs_fold.py, docs_check.py) are not installed in this cloud sandbox, so docs_check was NOT run. CI/owner should run it.
- Next: CI on branch, rename to CRSP265-integration, PR.
