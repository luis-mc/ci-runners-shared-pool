#!/usr/bin/env bash
# CRSP348: builds crspagent-darwin-<arch>.pkg with pkgbuild and productbuild (macOS only, no user interface).
# It puts crsp in /usr/local/bin and stops; joining the controller is the next command.
#
#   installer/agent/macos/build.sh <amd64|arm64> <crsp binary> <release version> <run number> <out dir>
#
# The release workflow renames the file CrspAgent-<version>-macos-<x64|arm64>.pkg. Not signed yet (CRSP90).
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
mkdir -p "$stage/usr/local/bin"
install -m 0755 "$bin" "$stage/usr/local/bin/crsp"
pkgbuild --root "$stage" --identifier dev.luismc.crsp.agent --version "$version" \
  --install-location / --ownership recommended "$work/crspagent-component.pkg"
productbuild --package "$work/crspagent-component.pkg" --identifier dev.luismc.crsp.agent --version "$version" \
  "$out/crspagent-darwin-$arch.pkg"
echo "built $out/crspagent-darwin-$arch.pkg (version $version)"
