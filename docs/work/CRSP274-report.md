# CRSP274, CRSP348, CRSP279 report (RELEASE-73)

Branch CRSP274-native-packages. Left over from the plan: Controller MSI, pkg, deb, rpm already existed (OPS1); the
missing parts were the agent native packages, release notes from change notes and the doctor check.

- CRSP348: agent deb (nfpm, `installer/agent/linux`), agent pkg (`installer/agent/macos`), agent MSI (`installer/agent/windows`, WiX v5).
  Release has 12 CrspAgent assets (6 archives unchanged + 6 native), 26 installers/archives, 30 assets, SHA256SUMS 30 lines.
  Archive names unchanged; ParseName and release.ts skip the native names (release.ts now accepts them as known agent assets).
- CRSP274: `scripts/ci/release_notes.py` makes the "What's new" part of the release notes from unfolded change notes; used by release.yml (tag).
- CRSP279: doctor check `install.agent_packages` (57 core checks), operations R5.134 to R5.136, doctor spec row.
- CI smoke: installer.yml (agent MSI install/uninstall), installer-unix.yml (agent deb, agent pkg).
- Tests run: go test -race doctor/..., agentinstallers, cmd/crsp, installer/...; python unittest scripts/ci (92); vitest release.test.ts; deb built and read with dpkg-deb.
- Not run locally: MSI and pkg builds (Windows, macOS), CI does.
