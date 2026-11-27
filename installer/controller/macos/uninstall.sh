#!/bin/sh
# Uninstalls crsp (installed by crsp-darwin-<arch>.pkg). Run with sudo:
#
#   sudo crsp-uninstall                 removes the program and the service, keeps the data
#   sudo crsp-uninstall --delete-data   also deletes the data folder, and the log
#
# The data folder is /Library/Application Support/crsp (config, database,
# audit log, backups, CA).
set -u
LABEL=crsp
TRAY=dev.luismc.crsp.tray
DATA="/Library/Application Support/crsp"

delete_data=0
for a in "$@"; do
  case "$a" in
    --delete-data) delete_data=1 ;;
    -h|--help) sed -n '2,9p' "$0"; exit 0 ;;
    *) echo "crsp-uninstall: unknown option $a" >&2; exit 2 ;;
  esac
done
if [ "$(id -u)" != 0 ]; then
  echo "crsp-uninstall: run it with sudo" >&2
  exit 1
fi

launchctl bootout "system/$LABEL" >/dev/null 2>&1 || true
for uid in $(ps -axo uid=,comm= | awk '$2 ~ /crsp$/ {print $1}' | sort -u); do
  if [ "$uid" != 0 ]; then launchctl bootout "gui/$uid/$TRAY" >/dev/null 2>&1 || true; fi
done
# The tray may also have written its own per-user entry (crsp tray autostart).
for home in /Users/*; do
  rm -f "$home/Library/LaunchAgents/$TRAY.plist"
done

rm -f "/Library/LaunchDaemons/$LABEL.plist" "/Library/LaunchAgents/$TRAY.plist"
rm -f /usr/local/bin/crsp /usr/local/bin/crsp-uninstall
rm -f /usr/local/share/crsp/install-kind
# This script is the last file of the program folder.
rm -f /usr/local/share/crsp/uninstall.sh
rmdir /usr/local/share/crsp >/dev/null 2>&1 || true
pkgutil --forget dev.luismc.crsp >/dev/null 2>&1 || true

if [ "$delete_data" = 1 ]; then
  rm -rf "$DATA"
  rm -f /var/log/crsp.log
  echo "crsp removed. Data deleted."
else
  echo "crsp removed. Data kept in $DATA (delete it with: sudo crsp-uninstall --delete-data, or install again to use it)."
fi
