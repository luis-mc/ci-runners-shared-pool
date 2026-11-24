#!/usr/bin/env bash
# CRSP224, TRANSITION: next to every Crsp-<version>-<os>-<arch>.<ext> installer and archive in DIR,
# publish a byte-identical copy under the name the updater of 0.1.21 and older looks for,
# crsp-<goos>-<goarch>_<version>.<ext>. Those updaters read that name from the release assets and
# from SHA256SUMS, then check the SHA-256 and the build provenance attestation for it. The new
# updater (0.1.22+) prefers the Crsp-* names and the signed manifest. The copies stay until no
# supported install is on 0.1.21 or older (operations R5.128).
# Usage: bash scripts/ci/release-legacy.sh DIR VERSION
set -euo pipefail
dir="${1:?usage: release-legacy.sh DIR VERSION}"
version="${2:?usage: release-legacy.sh DIR VERSION}"
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=release-names.sh
source "$here/release-names.sh"
n=0
for f in "$dir"/Crsp-*.msi "$dir"/Crsp-*.deb "$dir"/Crsp-*.rpm "$dir"/Crsp-*.pkg "$dir"/Crsp-*.tar.gz "$dir"/Crsp-*.zip; do
  [ -f "$f" ] || continue
  cp "$f" "$dir/$(release_to_legacy "$version" "$f")"
  n=$((n + 1))
done
echo "release-legacy: $n legacy copies in $dir"
