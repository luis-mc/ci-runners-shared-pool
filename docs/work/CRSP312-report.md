# CRSP312 report

S17: ci.yml docker buildx `cache-to` (7 scopes) is written only on refs/heads/main; PRs and branch runs still `cache-from`. windows-msi-check.yml: actions/cache split into restore (all runs) and save (main, on a miss only).
S13: retention-days on same-run artifacts: build.yml web-embed and crsp-* (2 days; 90 when the caller is Release, i.e. default kept), installer-unix.yml installer-linux-amd64 / installer-darwin-arm64 (2), ci.yml go test times (3).
Not touched: release.yml, package.yml, installer.yml, lifecycle.yml, package-windows.yml (held by artifact-prune), tray-smoke.yml.
setup-go/setup-node built-in caches still save on PRs; out of scope here.
Checks: YAML parse, prettier, docs_check. actionlint not installed.
