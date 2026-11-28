#!/usr/bin/env bash
# CRSP348: builds crspagent-linux-<arch>.deb with nfpm (no user interface).
#
#   installer/agent/linux/build.sh <amd64|arm64> <crsp binary> <release version> <run number> <out dir>
#
# Run from anywhere. nfpm is the pinned version of scripts/delivery/tools.json; set NFPM to use another binary.
# The release workflow renames the file CrspAgent-<version>-linux-<x64|arm64>.deb (scripts/ci/release-names.sh).
set -euo pipefail
arch="${1:?arch amd64|arm64}"
bin="$(realpath "${2:?crsp binary}")"
release="${3:?release version}"
run="${4:-0}"
out="${5:?out dir}"
here="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "$here/../../.." && pwd)"
cd "$root"
[ -f "$bin" ] || { echo "build.sh: $bin not found" >&2; exit 2; }
if [ -z "${NFPM:-}" ]; then
  NFPM="$(python3 scripts/delivery/ci_tool.py tool nfpm)"
fi
mkdir -p "$out"
out="$(realpath "$out")"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
version="$("$root/installer/controller/linux/version.sh" "$release" "$run" deb)"
ARCH="$arch" VERSION="$version" RELEASE=1 BIN="$bin" python3 -c '
import os, re, sys
t = open(sys.argv[1]).read()
sys.stdout.write(re.sub(r"\$\{(\w+)\}", lambda m: os.environ[m.group(1)], t))
' "$here/nfpm.yaml" > "$work/nfpm.yaml"
"$NFPM" package --config "$work/nfpm.yaml" --packager deb --target "$work/crspagent.deb"
cp "$work/crspagent.deb" "$out/crspagent-linux-$arch.deb"
echo "built $out/crspagent-linux-$arch.deb (version $version)"
