# Shared helpers. build.sh puts this file after the shebang of every
# maintainer script (nfpm takes one file per script).
crsp_dirs() {
  mkdir -p /var/lib/crsp /etc/crsp
  chown root:root /var/lib/crsp /etc/crsp
  chmod 0700 /var/lib/crsp
  chmod 0750 /etc/crsp
}
have_systemd() { [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1; }
crsp_stop() { if have_systemd; then systemctl stop crsp.service >/dev/null 2>&1 || true; fi; }
crsp_start() {
  if have_systemd; then
    systemctl daemon-reload || true
    systemctl enable crsp.service || true
    systemctl restart crsp.service || echo "crsp: the service did not start; see: journalctl -u crsp" >&2
  else
    echo "crsp: systemd is not running; the service starts at the next boot" >&2
  fi
}
