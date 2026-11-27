#!/usr/bin/env bash
# CRSP304: builds one agent installer: the crsp binary, an install script and the licence in a
# folder named crsp-agent, as a .zip (Windows) or a .tar.gz (Linux, macOS). No user interface:
# the script copies the binary where the controller installer would and says nothing else. The
# release workflow renames the file CrspAgent-<version>-<os>-<arch>.<ext> (scripts/ci/release-names.sh).
# Usage: build.sh <goos> <goarch> <crsp binary> <out dir>
# Writes <out dir>/crspagent-<goos>-<goarch>.<zip|tar.gz> (the stable name, DL-22).
set -euo pipefail
goos="${1:?goos (windows|linux|darwin)}"
goarch="${2:?goarch (amd64|arm64)}"
bin="${3:?crsp binary}"
out="${4:?out dir}"
here="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "$here/../.." && pwd)"
case "$goos" in
  windows) exe=crsp.exe; script=install.ps1; ext=zip ;;
  linux|darwin) exe=crsp; script=install.sh; ext=tar.gz ;;
  *) echo "build.sh: unknown goos $goos" >&2; exit 2 ;;
esac
[ -f "$bin" ] || { echo "build.sh: $bin not found" >&2; exit 2; }
mkdir -p "$out"
out="$(cd "$out" && pwd)"
stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT
mkdir "$stage/crsp-agent"
cp "$bin" "$stage/crsp-agent/$exe"
cp "$here/$script" "$stage/crsp-agent/$script"
cp "$root/LICENSE" "$stage/crsp-agent/LICENSE"
chmod 0755 "$stage/crsp-agent/$exe" "$stage/crsp-agent/$script"
file="$out/crspagent-$goos-$goarch.$ext"
rm -f "$file"
if [ "$ext" = zip ]; then
  (cd "$stage" && zip -qr "$file" crsp-agent)
else
  # fixed owner and order so the same inputs make the same bytes
  tar -C "$stage" --owner=0 --group=0 --numeric-owner --sort=name -czf "$file" crsp-agent 2>/dev/null \
    || tar -C "$stage" -czf "$file" crsp-agent
fi
echo "$file"
