---
title: Install and check a download
type: guide
---

# Install and check a download

Releases are at <https://github.com/luis-mc/ci-runners-shared-pool/releases>. `edge` is the build of `main`; `v*` are stable releases.

The installers are **not signed** (no code-signing certificate yet; owner decision D58). Windows SmartScreen and macOS Gatekeeper may warn. In place of a signature, every release gives you three checks you can run yourself: a checksum, a build attestation, and a bill of materials. Operations spec: R5.43, R5.45.

## What a release holds

The version is in every installer and archive name, without the leading `v` (`Crsp-0.1.22-win-x64.msi`; every Controller asset is named `Crsp-<version>-<os>-<arch>.<ext>`, with `<os>` one of `win`, `macos`, `linux` and `<arch>` one of `x64`, `arm64`). Open the release page and pick the file for your system; the links here go to the release page, not to a fixed file name. The agent installers have their own names, `CrspAgent-<version>-<os>-<arch>.<ext>`; a machine joins the pool with the command the console shows, so you do not download them by hand. Releases up to 0.1.14 have the plain names, and a machine on 0.1.14 or older installs the first versioned release by hand once (operations R5.119).

| Asset | What it is |
|---|---|
| `Crsp-<version>-<os>-<arch>.tar.gz` or `.zip` | The binary (linux, macos: tar.gz; win: zip) |
| `Crsp-<version>-win-<arch>.msi`, `Crsp-<version>-macos-<arch>.pkg`, `Crsp-<version>-linux-<arch>.deb`, `Crsp-<version>-linux-<arch>.rpm` | The installers |
| `CrspAgent-<version>-<os>-<arch>.tar.gz` or `.zip` | The agent, for machines that join the pool |
| `crsp-sbom-go.spdx.json`, `crsp-sbom-web.spdx.json` | The SBOMs (SPDX 2.3 JSON): Go modules in the binary, npm packages of the web console |
| `SHA256SUMS` | SHA-256 of every asset above |

## 1. Check the checksum

Put the file and `SHA256SUMS` from the same release in one folder.

Linux: `sha256sum --check --ignore-missing SHA256SUMS`

macOS: `shasum -a 256 --check --ignore-missing SHA256SUMS`

Windows (PowerShell): `Get-FileHash -Algorithm SHA256 .\Crsp-<version>-win-x64.msi`, then compare with the line for that file in `SHA256SUMS`.

Every line you checked must say `OK`. If one does not, do not install the file.

## 2. Check the attestation

The attestation says the file was built by the release workflow of this repository, from a named commit. It needs the GitHub CLI (`gh`):

```
gh attestation verify Crsp-<version>-linux-x64.tar.gz --repo luis-mc/ci-runners-shared-pool
```

It prints `Verification succeeded` and the workflow and commit. Do the same for `SHA256SUMS` and for each SBOM. A file with no valid attestation: do not install it.

`install.sh` and `install.ps1` (R5.1) do checks 1 and 2 for the archive. The updater (R5.34) does them for every update.

## 3. Read the SBOM

The SBOMs are plain JSON. List the packages with `jq -r '.packages[].name' crsp-sbom-go.spdx.json | sort`. Feed them to a scanner, for example `grype sbom:crsp-sbom-go.spdx.json`.

## When signing is on

When the owner has a certificate and sets the repository variable `SIGNING_ENABLED` to `true`, the MSI and the pkg are signed before they are checksummed and attested. Then also check the signature: Windows `Get-AuthenticodeSignature .\Crsp-<version>-win-x64.msi`, macOS `pkgutil --check-signature Crsp-<version>-macos-arm64.pkg`.
