# CRSP224 report: legacy asset names so 0.1.21 and older can update

Branch CRSP224-legacy-asset-names from origin/main.

## Cause
OPS69 (RELEASE-53) renamed release assets to Crsp-<version>-<os>-<arch>.<ext>. The 0.1.21 updater looks for
crsp-<goos>-<goarch>_<version>.<ext> in the assets and in SHA256SUMS, then checks SHA-256 and attestation.

## Change
- scripts/ci/release-names.sh: legacy_asset_name, release_to_legacy (exact v0.1.21 mapping; `--legacy` CLI).
- scripts/ci/release-legacy.sh: byte-identical copy of each of 14 installers/archives under the legacy name.
- release.yml: new step after the manifest (manifest.json and signature unchanged). Checksum step: exact counts
  (32 files, 14 Crsp-* + 14 legacy, 32 SHA256SUMS lines). SHA256SUMS and attestation subjects cover `crsp-*`.
- Docs: operations R5.127 (TRANSITION) + proof row, change note, install guide note. docs_check 0/0.
- Tests: internal/update/legacy_assets_test.go (0.1.21 lookup fixture finds an asset for every os/arch/kind and
  its SHA256SUMS line; exact legacy name set; new updater PickAsset/PickFromSums prefer Crsp-* with both names);
  scripts/ci/test_release_scripts.py (order, counts, mapping).
- internal/update in main: PickAsset already prefers Crsp-*; manifest lists only Crsp-* (release_manifest.py NAME
  regex skips legacy names). install.sh/install.ps1 try Crsp-* first. No code change needed there.

## Checks
actionlint ok; go test ./internal/update/... ok; golangci-lint ./internal/update ok; test_release_scripts ok;
docs_check ok; fast_guards: see CI.
Untouched: release-ed25519.pub, e2e_push_check, delivery_common, lifecycle_*, .gitleaksignore.

## Result
PR #386 draft. CI run 44999793174 green (first run 44997656824 hit an unrelated internal/cli flake).

CODE DONE 18f52563ed4e8611315e8a0cfc82b02791560fb0
