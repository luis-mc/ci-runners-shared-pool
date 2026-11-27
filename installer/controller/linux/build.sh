#!/usr/bin/env bash
# Builds crsp-linux-<arch>.deb and crsp-linux-<arch>.rpm with nfpm.
#
#   installer/controller/linux/build.sh <amd64|arm64> <crsp binary> <release version> <run number> <out dir>
#
# Run from the repository root. nfpm is the version pinned in
# scripts/delivery/tools.json, fetched (sha256-checked) by scripts/delivery/ci_tool.py;
# set NFPM to use another binary.
set -euo pipefail
arch="${1:?arch amd64|arm64}"
bin="$(realpath "${2:?crsp binary}")"
release="${3:?release version}"
run="${4:-0}"
out="${5:?out dir}"
here="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "$here/../../.." && pwd)"
cd "$root"

if [ -z "${NFPM:-}" ]; then
  NFPM="$(python3 scripts/delivery/ci_tool.py tool nfpm)"
fi

mkdir -p "$out"
out="$(realpath "$out")"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/scripts"

# One file per maintainer script: shebang, shared helpers, the body.
for body in "$here"/scripts/*.body; do
  name="$(basename "$body" .body)"
  { printf '#!/bin/sh\n'; cat "$here/scripts/common.sh"; cat "$body"; } > "$work/scripts/$name.sh"
  chmod 0755 "$work/scripts/$name.sh"
done

for kind in deb rpm; do
  printf '%s\n' "$kind" > "$work/install-kind-$kind"
  version="$("$here/version.sh" "$release" "$run" "$kind")"
  # nfpm does not expand variables in file paths, so fill the template ourselves.
  ARCH="$arch" VERSION="$version" RELEASE=1 KIND="$kind" BIN="$bin" SCRIPTS="$work/scripts" \
    KINDFILE="$work/install-kind-$kind" python3 -c '
import os, re, sys
t = open(sys.argv[1]).read()
sys.stdout.write(re.sub(r"\$\{(\w+)\}", lambda m: os.environ[m.group(1)], t))
' "$here/nfpm.yaml" > "$work/nfpm-$kind.yaml"
  "$NFPM" package --config "$work/nfpm-$kind.yaml" --packager "$kind" --target "$work/crsp.$kind"
  cp "$work/crsp.$kind" "$out/crsp-linux-$arch.$kind"
  echo "built $out/crsp-linux-$arch.$kind (version $version)"
done
