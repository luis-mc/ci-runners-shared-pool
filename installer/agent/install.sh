#!/bin/sh
# CRSP304: the agent installer for Linux and macOS. Run as root (sudo sh crsp-agent/install.sh).
# It copies crsp to its place and stops. Joining the controller is the next command
# (`crsp agent install --controller ... --join-code ...`), which the controller shows or runs over SSH.
set -eu
[ "$(id -u)" -eq 0 ] || { echo "run this as root: sudo sh install.sh" >&2; exit 1; }
here="$(cd "$(dirname "$0")" && pwd)"
dest=/usr/local/bin
mkdir -p "$dest"
install -m 0755 "$here/crsp" "$dest/crsp.new"
mv -f "$dest/crsp.new" "$dest/crsp"
echo "crsp is in $dest"
