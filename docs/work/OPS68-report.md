# OPS68: updater passes the lifecycle conformance runner

Branch OPS68-updater-conformance. Owns LC-3 to LC-7, LC-25, LC-26, LC-28.

## What was built
- `internal/update/lifecycle`: the state machine (14 states), check schedule and backoff, policies, retry rule, Ed25519 manifest check, audit rows. No clock inside; Tick takes the time.
- `internal/update/service.go`: runs the core on the real clock (15 s tick), keeps policy and window in `updates/lifecycle.json`, restores the outcome of an install after a restart (result.json, failures.json, `install-actor.json`), writes every row to audit.
- `internal/update/manifest.go`: `Manager.prepare` now also needs the signed manifest of the asset (`RequireManifest`); `ManifestFailure` carries the refusal reason. SHA256SUMS and sigstore provenance are unchanged.
- `internal/update/keys/release-ed25519.pub` (placeholder, PEM block missing: every manifest is refused until the orchestrator commits the real key) and `keys.go` (embed).
- Asset names (`source.go`): `Crsp-<version>-<os>-<arch>.<ext>` first, `crsp-<os>-<arch>_<version>.<ext>` second, plain name third. Manifest asset name: `<asset>.manifest.json`.
- API under `/api/v1/update/`: status, policy, install, history, fleet, and `/api/v1/remove`. Old `/update`, `/update/check`, `/update/apply` stay; check and a confirmed apply now go through the lifecycle service when it is wired.
- `internal/lifecycletest` + `cmd/crsp-lifecycle-test`: the HTTP test interface (fake clock, fake server). Not imported by cmd/crsp.
- `scripts/delivery/lifecycle_conformance.py` copied; CI step "Lifecycle conformance (updater)" in ci.yml after the Go tests.
- Spec: operations R5.124, change note OPS68.

## Notes for the orchestrator and the other tasks
- Paths: the brief says `/api/update/...`; this API is versioned, so the routes are `/api/v1/update/...` (same for remove).
- Placeholder key: until the real key is committed AND release.yml (OPS69) publishes `<asset>.manifest.json` signed with CRSP_UPDATE_SIGNING_KEY, no update installs on a real machine. This is by the owner decision (missing signature means refused).
- OPS69: the release must publish per asset a manifest `{"manifest": {product, channel, version, required, expires_at, url, sha256}, "signature": "<hex Ed25519 over canonical JSON>"}` named `<asset>.manifest.json`. `sha256` must be the SHA-256 of the asset. `remove` rows should use `update.AuditAction("remove", result)` and `update.AuditTarget`.
- OPS69: `api.RemoveService` is the hook for POST /api/v1/remove; nothing is wired, so it answers 501 until OPS69 implements it.
- TRAY12: the tray still calls `Manager.Apply` directly, so a tray install is not a lifecycle row. Route it through `update.Service.Install` to audit it.
- Not built: the 60 min wait. The core waits for idle with no time limit; the Manager's drain limit (R5.33) applies once an install starts. The Manager's old retry record (3 tries, 1 h, 4 h, 16 h) stays for the legacy path.
- `paired` = this machine has joined the pool (`machineDrainer.policy` finds it).
- Windows install itself (MSI, self-test) is the existing path and was not run here.

## Checks run
- go test -race: internal/update/..., internal/lifecycletest, internal/api, cmd/crsp: pass.
- golangci-lint (linux and GOOS=windows) on the touched packages: clean.
- lifecycle_conformance.py against cmd/crsp-lifecycle-test: LC-3, LC-4, LC-5, LC-6, LC-7, LC-25, LC-26, LC-28 pass.
- docs_check --strict: only W1 (this report, stripped before the join).
