# OPS71 report: delivery sync to luismc-delivery 1.54.1

- Source: luis-mc/dev-standards, commit `28a7dba5` (RELEASE-162, plugin `delivery` 1.54.1; checked out at `0333478a`).
- Track: `go` (`scripts/delivery/gate.json` says `"track": "go"`). crsp has no `.delivery/config.json` or install record
  (`.delivery/` is in `.gitignore`), so no installed version is recorded; I compared every file with the 1.54.1 template
  and, where a file was an older template, the difference is only crsp edits (see table).
- `delivery_ci_install.py update --track go --root .` wrote nothing over the project (no install record: every differing file kept).
  I then merged by hand. The `.new` files were deleted.
- All 55 other managed files, including `e2e_push_check`, `delivery_common`, `lifecycle_release_check`, `lifecycle_optout`
  and `lifecycle_conformance`, are byte-identical to 1.54.1 ("same"). Nothing to do for them.
- `.gitleaksignore` untouched.

## Vendored files that differ from the 1.54.1 template

| File | Result | Note |
| --- | --- | --- |
| `.gitleaks.toml` | template | Owner: as-is. Drops the three crsp allowlist entries for fake example secrets (CRSP32, CRSP37; see "Risks"). |
| `.github/workflows/package.yml` | template + per-repo value | Build line set to `-o dist/crsp ./cmd/crsp` (PRODUCT value, matches `smoke.json`). Checkout and setup-python pins go back to the template's `v4`/`v5` pins. |
| `scripts/delivery/gate.json` | template + per-repo value | `ui_paths: ["apps/web/", "packages/ui/"]`. Keeps `-timeout 10s` on the guards step (not weakening G1). Drops `bash scripts/ci/go-test.sh -short` as unit step, and the `e2e` step (see "Risks"). |
| `scripts/delivery/smoke.json` | template + per-repo values | `build`, `start`, `expect`, `health` restored (the template says "Fill start..."). Only `_note` text and `example_start` differ from before. |
| `.github/workflows/artifact-prune.yml` | template + crsp edit | New header comments and the "Check the workflow list" step from the template. Edit kept: runs `scripts/ci/artifact-prune.sh` with `GH_REPO` instead of `artifact_prune.py`; the workflow list note is crsp's 8 workflows; checkout pin stays `v7.0.1`. |
| `.githooks/commit-msg` | kept (= template + crsp edit) | Missing `.delivery/config.json` still enforces the no-attribution rule. Template has no newer content. |
| `.githooks/pre-push` | kept (= template + crsp edit) | Appends `project_gates` (gofmt, go vet, golangci-lint, go test of changed packages, web typecheck). No newer template content. |
| `.github/workflows/ci-fallback.yml` | kept (= template + crsp edit) | Adds the Go, pnpm, node and frozen-install setup before the claim; prettier quote style. |
| `.github/workflows/package-windows.yml` | kept (= template + crsp edit) | OPS69 release names via `scripts/ci/release-names.sh`; no tag trigger (release.yml publishes the MSI, CRSP145); installer gate; newer checkout pin. Template's DL-96 `versioned_names` option is not used. |
| `.github/workflows/train-cleanup.yml` | kept (= template + crsp edit) | Newer action pins (`checkout v7.0.1`, `setup-python v7.1.0`), comment alignment. |
| `.github/workflows/windows-msi-check.yml` | kept (= template + crsp edit) | Gate steps until the MSI is wired; `actions/cache` pin; programs-list authoring check for `installer/crsp.wxs`. |
| `scripts/delivery/smoke_packaged.py` | kept (= template + crsp edit) | `probe_context`: a loopback https URL is probed without certificate verification. |
| `scripts/delivery/tools.json` | kept (= template + crsp edit) | Adds `syft` 1.33.0 and `nfpm` 2.49.0 (release SBOM and packages). |
| `scripts/delivery/windows-msi-linux.sh` | kept (= template + crsp edit) | Comment only (OPS69 names instead of DL-96). |
| `.github/workflows/ci.yml` | kept | Project-owned (the template says the project edits it); crsp's 712-line workflow shares no structure with the 254-line template. Not touched. |

That is 15 files that differ; 12 carry a crsp edit once pin-only and format-only differences are set aside
(`.gitleaks.toml`, `gate.json`, `smoke.json`, `package.yml`, `artifact-prune.yml`, `commit-msg`, `pre-push`, `ci-fallback.yml`,
`package-windows.yml`, `windows-msi-check.yml`, `smoke_packaged.py`, `tools.json`), plus `ci.yml`, `train-cleanup.yml` and
`windows-msi-linux.sh` as pin/comment/own-workflow differences.

## Risks from taking the four template files as-is (owner decision)

- `gate.json` no longer has the `e2e` step, so the pre-push hook no longer asks for the e2e gate, and the `unit` step is the template's `go test -short ./...` (G1 warns on it, as it did on crsp's 150 s script).
  CI (`ci.yml`) still runs e2e and tests on its own. The guardrail check below says whether G1 still passes.
- `.gitleaks.toml` has no allowlist for the three fake values; the check below says whether the history scan still passes.
- `package.yml` uses the template's older action pins.

## Checks

(filled in below)
