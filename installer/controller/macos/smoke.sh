#!/usr/bin/env bash
# Smoke test of the .pkg on a macOS runner: expand, install, upgrade, uninstall.
# Run as a user that has passwordless sudo.
#
#   installer/controller/macos/smoke.sh <old pkg> <new pkg>
set -euo pipefail
old="$(cd "$(dirname "${1:?old pkg}")" && pwd)/$(basename "$1")"
new="$(cd "$(dirname "${2:?new pkg}")" && pwd)/$(basename "$2")"
DATA="/Library/Application Support/crsp"

fail() { echo "SMOKE FAIL: $*" >&2; exit 1; }
ok() { echo "ok: $*"; }
need() { [ -e "$1" ] || fail "missing $1"; }

echo "== expand $old (no install)"
tmp="$(mktemp -d)"
pkgutil --expand-full "$old" "$tmp/expanded"
need "$tmp/expanded/crsp-component.pkg/Payload/usr/local/bin/crsp"
need "$tmp/expanded/crsp-component.pkg/Payload/Library/LaunchDaemons/crsp.plist"
need "$tmp/expanded/crsp-component.pkg/Payload/Library/LaunchAgents/dev.luismc.crsp.tray.plist"
need "$tmp/expanded/crsp-component.pkg/Scripts/postinstall"
[ "$(cat "$tmp/expanded/crsp-component.pkg/Payload/usr/local/share/crsp/install-kind")" = pkg ] || fail "payload install-kind"
rm -rf "$tmp"
ok "expanded"

check_installed() {
  need /usr/local/bin/crsp
  need /usr/local/share/crsp/install-kind
  need /usr/local/share/crsp/uninstall.sh
  need /Library/LaunchDaemons/crsp.plist
  need /Library/LaunchAgents/dev.luismc.crsp.tray.plist
  [ "$(cat /usr/local/share/crsp/install-kind)" = pkg ] || fail "install-kind is not pkg"
  plutil -lint /Library/LaunchDaemons/crsp.plist /Library/LaunchAgents/dev.luismc.crsp.tray.plist
  /usr/local/bin/crsp --version
  [ "$(sudo stat -f %Lp "$DATA")" = 700 ] || fail "data folder mode"
  [ "$(sudo stat -f %Su "$DATA")" = root ] || fail "data folder owner"
  sudo launchctl print system/crsp >/dev/null || fail "service crsp is not loaded"
  sleep 3
  sudo launchctl print system/crsp | grep -E "state =|pid =|last exit" || true
  sudo launchctl print system/crsp | grep -q "state = running" || { sudo tail -30 /var/log/crsp.log || true; fail "service is not running"; }
}

echo "== install $old"
sudo installer -pkg "$old" -target / -verbose
check_installed
ok "installed"
sudo sh -c "echo keep > '$DATA/keep.txt'"
pid_before="$(sudo launchctl print system/crsp | awk '/pid =/ {print $3; exit}')"

echo "== upgrade with $new"
sudo installer -pkg "$new" -target / -verbose
check_installed
[ "$(sudo cat "$DATA/keep.txt")" = keep ] || fail "upgrade lost the data"
pid_after="$(sudo launchctl print system/crsp | awk '/pid =/ {print $3; exit}')"
[ -n "$pid_after" ] && [ "$pid_after" != "$pid_before" ] || fail "upgrade did not restart the service ($pid_before -> $pid_after)"
ok "upgraded, data kept"

echo "== uninstall (keeps data)"
sudo /usr/local/bin/crsp-uninstall
[ ! -e /usr/local/bin/crsp ] || fail "binary left"
[ ! -e /Library/LaunchDaemons/crsp.plist ] || fail "daemon plist left"
[ ! -e /Library/LaunchAgents/dev.luismc.crsp.tray.plist ] || fail "agent plist left"
[ ! -e /usr/local/share/crsp/install-kind ] || fail "marker left"
if sudo launchctl print system/crsp >/dev/null 2>&1; then fail "service still loaded"; fi
[ "$(sudo cat "$DATA/keep.txt")" = keep ] || fail "uninstall deleted the data"
ok "uninstalled, data kept"

echo "== reinstall, then uninstall --delete-data"
sudo installer -pkg "$new" -target /
[ "$(sudo cat "$DATA/keep.txt")" = keep ] || fail "reinstall lost the data"
sudo /usr/local/bin/crsp-uninstall --delete-data
[ ! -e "$DATA" ] || fail "data folder left after --delete-data"
ok "data deleted"
echo "SMOKE OK (pkg)"
