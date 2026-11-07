# Embedded in native DEB/RPM maintainer scripts; never run by a user installer.
set -eu
BASE=/var/lib/kurumifrp
META=/var/lib/kurumifrp-package
UNIT=kurumifrp.service
plain() {
 for p in /var /var/lib "$BASE" "$META"; do
  [ ! -L "$p" ] || { echo "Refusing linked package state: $p" >&2; exit 1; }
 done
}
systemd_active() { [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1; }
setup_state() {
 plain
 if [ -e "$META" ]; then
  [ -f "$META/uid" ] && [ ! -L "$META/uid" ] && [ "$(stat -c %u "$META/uid")" = 0 ] || { echo 'Unrecognized package ownership receipt.' >&2; exit 1; }
  [ "$(id -u kurumifrp)" = "$(cat "$META/uid")" ] || { echo 'Service account ownership changed.' >&2; exit 1; }
 else
  [ ! -e "$BASE" ] || { echo 'Unmanaged /var/lib/kurumifrp retained.' >&2; exit 1; }
  if getent passwd kurumifrp >/dev/null || getent group kurumifrp >/dev/null; then
   echo 'Existing kurumifrp account/group is not owned by this package.' >&2; exit 1
  fi
  mkdir -m 700 "$META"
  if ! useradd --system --user-group --no-create-home --home-dir "$BASE" --shell /usr/sbin/nologin kurumifrp; then rmdir "$META"; exit 1; fi
  id -u kurumifrp > "$META/uid"
 fi
 install -d -m 700 -o kurumifrp -g kurumifrp "$BASE"
}
stop_workbench() {
 plain
 if systemd_active; then systemctl stop "$UNIT"; fi
 if [ -e "$BASE/data/manager.lock" ]; then
  # The authenticated manager owns its children. Unknown/stale state stops removal.
  /usr/bin/kurumifrp --stop
 fi
}
start_workbench() {
 if systemd_active; then
  systemctl daemon-reload
  systemctl enable --now kurumifrp-updater.path
  if command -v deb-systemd-helper >/dev/null 2>&1; then
   deb-systemd-helper unmask "$UNIT" >/dev/null || true
   if deb-systemd-helper --quiet was-enabled "$UNIT"; then deb-systemd-helper enable "$UNIT" >/dev/null; fi
   deb-systemd-invoke start "$UNIT"
  else
   systemctl preset "$UNIT"
   systemctl start "$UNIT"
  fi
 else
  echo 'Package installed. No active systemd was found; no running service is claimed.'
 fi
}
remove_unit_state() {
 if command -v deb-systemd-helper >/dev/null 2>&1; then deb-systemd-helper purge "$UNIT" >/dev/null; fi
 if systemd_active; then
  systemctl disable --now kurumifrp-updater.path >/dev/null 2>&1 || true
  systemctl disable "$UNIT" >/dev/null 2>&1 || true
  systemctl daemon-reload
  systemctl reset-failed "$UNIT" >/dev/null 2>&1 || true
 fi
}
purge_state() {
 plain
 [ -d "$META" ] || { [ ! -e "$BASE" ] || { echo 'State ownership missing; data retained.' >&2; exit 1; }; return; }
 [ -f "$META/uid" ] && [ ! -L "$META/uid" ] && [ "$(stat -c %u "$META/uid")" = 0 ] || { echo 'Unsafe ownership receipt.' >&2; exit 1; }
 uid=$(cat "$META/uid")
 case "$uid" in ''|*[!0-9]*) echo 'Invalid account receipt.' >&2; exit 1;; esac
 if getent passwd kurumifrp >/dev/null; then
  [ "$(id -u kurumifrp)" = "$uid" ] || { echo 'Account changed; no data removed.' >&2; exit 1; }
  n=0
  while pgrep -u "$uid" >/dev/null; do
   n=$((n+1)); [ "$n" -lt 50 ] || { echo 'Service account still has live processes; purge stopped.' >&2; exit 1; }
   sleep 0.1
  done
 fi
 [ ! -e "$BASE/data/manager.lock" ] || { echo 'Unresolved process state; purge stopped.' >&2; exit 1; }
 rm -rf --one-file-system -- "$BASE"
 [ ! -e "$BASE" ] || { echo 'State directory was not fully removed.' >&2; exit 1; }
 if getent passwd kurumifrp >/dev/null; then userdel kurumifrp; fi
 if getent group kurumifrp >/dev/null; then groupdel kurumifrp; fi
 rm -f -- "$META/uid"
 rmdir "$META"
}
