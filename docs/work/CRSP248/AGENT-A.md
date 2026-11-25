# CRSP248 part A (agent side) report

Done: DetectDeps (DEP1), install package (DEP5, DEP7 agent part, DEP8, DEP10, DEP11, DEP16), link dispatch of DepsCheck/DepsInstall/DepsUndo/DepsRestartHost, `DependencySetup: true`, pinned.json manual steps.

Not done / open:
- Docker Desktop url/sha256 empty: desktop.docker.com is blocked by the proxy (403). Installer is refused with "not pinned in this release".
- Prepare hook is nil (no simple exported ensure-image/StartPersistent function); job emits "made when the first runner starts".
- Proto has no field for "remove conflicting Docker packages: yes". Config.RemoveConflicts exists (default off); conflicts are refused and named.
- docker-in-wsl2 lives in the crsp-owned distro `crsp-docker`, not the default distro. DetectEngine only tries `wsl docker` (default distro); deps code (depsEngine) also probes `wsl -d crsp-docker`, but the container backend elsewhere does not: needs follow-up.
- After a WSL restart resume with resume_from="install" (idempotent) when docker-in-wsl2 is in the plan; "verify" skips install.
- Windows/WSL and Docker Desktop silent flags are unverified on a real PC. `wsl --install -d Ubuntu-24.04 --name crsp-docker --no-launch` is unverified.
- golangci-lint cannot run here (built with go1.25, repo targets go1.27.4); gofmt and go vet are clean on linux; the install package also vets on windows and darwin (full agent cross-vet was not finished).

Files: internal/agent/deps.go, deps_test.go, deps_link.go, deps_link_test.go, install/{system,plat,osrelease,events,runner,undo}.go, install/{install_test,ossystem_test}.go, internal/depsetup/pinned.json, plus run.go (field `deps`, 4 dispatch cases) and machine_report.go (flag).
Tests: `go test ./internal/agent/... ./internal/depsetup/...` pass.
