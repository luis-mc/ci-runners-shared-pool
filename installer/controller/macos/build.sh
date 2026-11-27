#!/usr/bin/env bash
# Builds crsp-darwin-<arch>.pkg with pkgbuild and productbuild (macOS only).
#
#   installer/controller/macos/build.sh <amd64|arm64> <crsp binary> <release version> <run number> <out dir>
#
# Run from the repository root. The package is not signed or notarized yet
# (operations R5.7: milestone 8).
set -euo pipefail
arch="${1:?arch amd64|arm64}"
bin="$(cd "$(dirname "${2:?crsp binary}")" && pwd)/$(basename "$2")"
release="${3:?release version}"
run="${4:-0}"
out="${5:?out dir}"
here="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "$here/../../.." && pwd)"
cd "$root"

version="$("$root/installer/controller/linux/version.sh" "$release" "$run" pkg)"
mkdir -p "$out"
out="$(cd "$out" && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

stage="$work/root"
mkdir -p "$stage/usr/local/bin" "$stage/usr/local/share/crsp" "$stage/Library/LaunchDaemons" "$stage/Library/LaunchAgents"
install -m 0755 "$bin" "$stage/usr/local/bin/crsp"
install -m 0755 "$here/uninstall.sh" "$stage/usr/local/share/crsp/uninstall.sh"
ln -s ../share/crsp/uninstall.sh "$stage/usr/local/bin/crsp-uninstall"
printf 'pkg\n' > "$stage/usr/local/share/crsp/install-kind"
chmod 0644 "$stage/usr/local/share/crsp/install-kind"
install -m 0644 "$here/crsp.plist" "$stage/Library/LaunchDaemons/crsp.plist"
install -m 0644 "$here/dev.luismc.crsp.tray.plist" "$stage/Library/LaunchAgents/dev.luismc.crsp.tray.plist"

scripts="$work/scripts"
mkdir -p "$scripts"
install -m 0755 "$here/scripts/preinstall" "$here/scripts/postinstall" "$scripts/"

pkgbuild --root "$stage" --identifier dev.luismc.crsp --version "$version" \
  --install-location / --ownership recommended --scripts "$scripts" "$work/crsp-component.pkg"
productbuild --package "$work/crsp-component.pkg" --identifier dev.luismc.crsp --version "$version" \
  "$out/crsp-darwin-$arch.pkg"
echo "built $out/crsp-darwin-$arch.pkg (version $version)"
