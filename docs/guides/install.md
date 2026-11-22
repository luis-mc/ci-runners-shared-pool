---
title: Install and check a download
type: guide
---

# Install and check a download

Releases are at <https://github.com/luis-mc/ci-runners-shared-pool/releases>. `edge` is the build of `main`; `v*` are stable releases.

The installers are **not signed** (no code-signing certificate yet; owner decision D58). Windows SmartScreen and macOS Gatekeeper may warn. In place of a signature, every release gives you three checks you can run yourself: a checksum, a build attestation, and a bill of materials. Operations spec: R5.43, R5.45.

## What a release holds

The version is in every installer and archive name, without the leading `v` (`crsp-windows-amd64_0.1.15.msi`). Open the release page and pick the file for your system; the links here go to the release page, not to a fixed file name. Releases up to 0.1.14 have the plain names, and a machine on 0.1.14 or older installs the first versioned release by hand once (operations R5.119).

| Asset | What it is |
|---|---|
| `crsp-<os>-<arch>_<version>.tar.gz` or `.zip` | The binary (linux, darwin: tar.gz; windows: zip) |
| `crsp-windows-<arch>_<version>.msi`, `crsp-darwin-<arch>_<version>.pkg`, `crsp-linux-<arch>_<version>.deb`, `crsp-linux-<arch>_<version>.rpm` | The installers |
| `crsp-sbom-go.spdx.json`, `crsp-sbom-web.spdx.json` | The SBOMs (SPDX 2.3 JSON): Go modules in the binary, npm packages of the web console |
| `SHA256SUMS` | SHA-256 of every asset above |

Transition (CRSP224): every installer and archive is published twice, once as `Crsp-<version>-<os>-<arch>.<ext>` and once, byte for byte the same, as `crsp-<goos>-<goarch>_<version>.<ext>`. Both names are in `SHA256SUMS` and attested. The second name is for installs on 0.1.21 or older, whose updater looks for it. Use the `Crsp-` name for a new install. The legacy names stay until no supported install is on 0.1.21 or older.

## 1. Check the checksum

Put the file and `SHA256SUMS` from the same release in one folder.

Linux: `sha256sum --check --ignore-missing SHA256SUMS`

macOS: `shasum -a 256 --check --ignore-missing SHA256SUMS`

Windows (PowerShell): `Get-FileHash -Algorithm SHA256 .\crsp-windows-amd64_<version>.msi`, then compare with the line for that file in `SHA256SUMS`.

Every line you checked must say `OK`. If one does not, do not install the file.

## 2. Check the attestation

The attestation says the file was built by the release workflow of this repository, from a named commit. It needs the GitHub CLI (`gh`):

```
gh attestation verify crsp-linux-amd64_<version>.tar.gz --repo luis-mc/ci-runners-shared-pool
```

It prints `Verification succeeded` and the workflow and commit. Do the same for `SHA256SUMS` and for each SBOM. A file with no valid attestation: do not install it.

`install.sh` and `install.ps1` (R5.1) do checks 1 and 2 for the archive. The updater (R5.34) does them for every update.

## 3. Read the SBOM

The SBOMs are plain JSON. List the packages with `jq -r '.packages[].name' crsp-sbom-go.spdx.json | sort`. Feed them to a scanner, for example `grype sbom:crsp-sbom-go.spdx.json`.

## When signing is on

When the owner has a certificate and sets the repository variable `SIGNING_ENABLED` to `true`, the MSI and the pkg are signed before they are checksummed and attested. Then also check the signature: Windows `Get-AuthenticodeSignature .\crsp-windows-amd64_<version>.msi`, macOS `pkgutil --check-signature crsp-darwin-arm64.pkg`.
