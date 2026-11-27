#!/usr/bin/env bash
# Smoke test of a .deb or .rpm: install, upgrade, remove, (deb) purge. Run as root.
#
#   installer/controller/linux/smoke.sh deb|rpm <old package> <new package> <old version> <new version>
#
# SMOKE_SYSTEMD=1 also checks the running service (needs a real systemd, such as
# the ubuntu-latest runner itself); without it only the files are checked,
# because a plain container has no systemd.
set -euo pipefail
kind="${1:?deb|rpm}"
old="$(realpath "${2:?old package}")"
new="$(realpath "${3:?new package}")"
oldv="${4:?old version text}"
newv="${5:?new version text}"
sysd="${SMOKE_SYSTEMD:-0}"

fail() { echo "SMOKE FAIL: $*" >&2; exit 1; }
ok() { echo "ok: $*"; }
need() { [ -e "$1" ] || fail "missing $1"; }
gone() { [ ! -e "$1" ] || fail "still there: $1"; }

check_installed() {
  want="$1"
  need /usr/bin/crsp
  need /usr/lib/systemd/system/crsp.service
  need /etc/xdg/autostart/crsp-tray.desktop
  need /usr/share/crsp/install-kind
  [ "$(cat /usr/share/crsp/install-kind)" = "$kind" ] || fail "install-kind is $(cat /usr/share/crsp/install-kind), want $kind"
  out="$(/usr/bin/crsp --version)" || fail "crsp --version failed"
  echo "$out"
  if [ -n "${EXPECT_BIN_VERSION:-}" ]; then
    echo "$out" | grep -q -- "$EXPECT_BIN_VERSION" || fail "crsp --version does not show $EXPECT_BIN_VERSION"
  fi
  case "$kind" in
    deb) have="$(dpkg-query -W -f='${Version}' crsp)" ;;
    rpm) have="$(rpm -q --qf '%{VERSION}' crsp)" ;;
  esac
  case "$have" in "$want"*) ;; *) fail "package version is $have, want $want" ;; esac
  [ "$(stat -c %a /var/lib/crsp)" = "700" ] || fail "data folder mode is $(stat -c %a /var/lib/crsp), want 700"
  [ "$(stat -c %U /var/lib/crsp)" = "root" ] || fail "data folder owner is not root"
  grep -q '^User=root$' /usr/lib/systemd/system/crsp.service || fail "unit must run as root"
  grep -q '^ExecStart=/usr/bin/crsp controller$' /usr/lib/systemd/system/crsp.service || fail "unit ExecStart"
  grep -q '^Exec=/usr/bin/crsp tray$' /etc/xdg/autostart/crsp-tray.desktop || fail "desktop Exec"
  if [ "$sysd" = 1 ]; then
    systemctl is-enabled crsp.service >/dev/null || fail "service not enabled"
    sleep 3
    systemctl is-active crsp.service >/dev/null || { journalctl -u crsp --no-pager | tail -30; fail "service not active"; }
  fi
}

install_pkg() {
  case "$kind" in
    deb) DEBIAN_FRONTEND=noninteractive apt-get install -y "$1" ;;
    rpm) rpm -U --nosignature "$1" ;;  # the rpm is not signed yet (milestone 8)
  esac
}

if [ "$kind" = deb ]; then
  apt-get update -qq
  # A plain container needs the tools the maintainer scripts call.
  DEBIAN_FRONTEND=noninteractive apt-get install -y -qq passwd >/dev/null
fi

echo "== install $old"
install_pkg "$old"
check_installed "$oldv"
ok "installed $oldv"

echo "keep" > /var/lib/crsp/keep.txt
echo "keep" > /etc/crsp/keep.txt
pid_before=""
if [ "$sysd" = 1 ]; then pid_before="$(systemctl show -p MainPID --value crsp.service)"; fi

echo "== upgrade to $new"
install_pkg "$new"
check_installed "$newv"
[ "$(cat /var/lib/crsp/keep.txt)" = keep ] || fail "upgrade lost the data folder"
[ "$(cat /etc/crsp/keep.txt)" = keep ] || fail "upgrade lost the config folder"
if [ "$sysd" = 1 ]; then
  pid_after="$(systemctl show -p MainPID --value crsp.service)"
  [ -n "$pid_after" ] && [ "$pid_after" != 0 ] && [ "$pid_after" != "$pid_before" ] || fail "upgrade did not restart the service ($pid_before -> $pid_after)"
fi
ok "upgraded to $newv, data kept"

echo "== remove"
case "$kind" in
  deb) apt-get remove -y crsp ;;
  rpm) rpm -e crsp ;;
esac
gone /usr/bin/crsp
gone /usr/lib/systemd/system/crsp.service
gone /etc/xdg/autostart/crsp-tray.desktop
gone /usr/share/crsp/install-kind
[ "$(cat /var/lib/crsp/keep.txt)" = keep ] || fail "remove deleted the data folder"
[ "$(cat /etc/crsp/keep.txt)" = keep ] || fail "remove deleted the config folder"
if [ "$sysd" = 1 ]; then
  if systemctl is-active crsp.service >/dev/null 2>&1; then fail "service still active after remove"; fi
fi
ok "removed, data kept"

echo "== reinstall over the kept data"
install_pkg "$new"
check_installed "$newv"
[ "$(cat /var/lib/crsp/keep.txt)" = keep ] || fail "reinstall lost the data"
ok "reinstalled"

if [ "$kind" = deb ]; then
  echo "== remove then purge"
  apt-get remove -y crsp
  apt-get purge -y crsp
  gone /var/lib/crsp
  gone /etc/crsp
  ok "purged, data deleted"
else
  rpm -e crsp
  [ "$(cat /var/lib/crsp/keep.txt)" = keep ] || fail "erase deleted the data"
  ok "erased, data kept (rpm has no purge)"
fi
echo "SMOKE OK ($kind)"
