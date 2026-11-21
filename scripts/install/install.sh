#!/bin/sh
# One command installs crsp on Linux and macOS (operations R5.1):
#   curl -fsSL <release>/install.sh | sh
# It downloads the archive for this OS and architecture, checks its SHA-256
# against SHA256SUMS, checks its build provenance (operations R5.1, R5.34),
# and runs `crsp install`. It does nothing else.
set -eu

REPO="${CRSP_REPO:-luis-mc/ci-runners-shared-pool}"
BASE="${CRSP_RELEASE_URL:-https://github.com/${REPO}/releases/latest/download}"

fail() {
  printf 'crsp install: %s\n' "$1" >&2
  exit 1
}

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) fail "unsupported OS $(uname -s); Windows uses install.ps1" ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) fail "unsupported architecture $(uname -m)" ;;
esac

target="${os}-${arch}"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT INT TERM

# The archive is named Crsp-<version>-<os>-<arch>.tar.gz (OPS69: os linux or macos, arch x64 or arm64).
# SHA256SUMS has a fixed name and lists it, so it says which file to fetch. For one transition release the
# older names are tried second: crsp-<os>-<arch>_<version>.tar.gz and, up to 0.1.14, crsp-<os>-<arch>.tar.gz.
case "$os" in darwin) new_os=macos ;; *) new_os="$os" ;; esac
case "$arch" in amd64) new_arch=x64 ;; *) new_arch="$arch" ;; esac
curl -fsSL "${BASE}/SHA256SUMS" -o "${work}/SHA256SUMS" || fail "download of SHA256SUMS failed"
name="$(awk -v t="${new_os}-${new_arch}" '{ f = $2; sub(/^\*/, "", f) } f ~ ("^Crsp-[0-9A-Za-z.+-]+-" t "[.]tar[.]gz$") { print f; exit }' "${work}/SHA256SUMS")"
if [ -z "$name" ]; then
  name="$(awk -v t="$target" '{ f = $2; sub(/^\*/, "", f) } f ~ ("^crsp-" t "(_[0-9A-Za-z.+-]+)?[.]tar[.]gz$") { print f; exit }' "${work}/SHA256SUMS")"
fi
[ -n "$name" ] || fail "no archive for ${target} is in SHA256SUMS"
curl -fsSL "${BASE}/${name}" -o "${work}/${name}" || fail "download of ${name} failed"

# Step 1: the SHA-256 must match the line for this file.
want="$(awk -v n="$name" '$2 == n || $2 == "*" n { print $1; exit }' "${work}/SHA256SUMS")"
[ -n "$want" ] || fail "${name} is not in SHA256SUMS"
if command -v sha256sum >/dev/null 2>&1; then
  have="$(sha256sum "${work}/${name}" | awk '{ print $1 }')"
else
  have="$(shasum -a 256 "${work}/${name}" | awk '{ print $1 }')"
fi
[ "$want" = "$have" ] || fail "SHA-256 of ${name} does not match SHA256SUMS; nothing installed"
printf 'crsp install: SHA-256 of %s matches SHA256SUMS\n' "$name"

# Step 2: build provenance. With the GitHub CLI, a failure stops the install.
# Without it, say so and how to check by hand, then go on.
if command -v gh >/dev/null 2>&1; then
  if gh attestation verify "${work}/${name}" --repo "$REPO"; then
    printf 'crsp install: build provenance of %s verified\n' "$name"
  else
    fail "build provenance of ${name} did not verify; nothing installed"
  fi
else
  printf '%s\n' \
    "crsp install: only the checksum was checked. The GitHub CLI (gh) is not installed, so the build provenance was not." \
    "crsp install: To check by hand, install gh (https://cli.github.com) and run:" \
    "crsp install:   gh attestation verify ${name} --repo ${REPO}" >&2
fi

tar -xzf "${work}/${name}" -C "$work" || fail "cannot unpack ${name}"
"${work}/crsp-${target}/crsp" install "$@"
