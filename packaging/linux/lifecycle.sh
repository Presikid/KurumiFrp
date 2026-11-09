# Package-manager root context only.
set -eu
ROOT=/opt/kurumifrp
BASE=$ROOT/runtime
META=$ROOT/meta
OLD_BASE=/var/lib/kurumifrp
OLD_META=/var/lib/kurumifrp-package
UNIT=kurumifrp.service
umask 077
plain() {
 for p in /opt "$ROOT" "$BASE" "$META" "$ROOT/root-tmp" "$META/uid" "$META/migration-intent" "$META/legacy-cleanup" "$META/purge-intent" /var /var/lib "$OLD_BASE" "$OLD_META" "$OLD_META/uid"; do
  [ ! -L "$p" ] || { echo "Refusing linked package state: $p" >&2; exit 1; }
 done
}
systemd_active() { [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1; }
no_mounts() {
 # mountinfo encodes whitespace but both managed prefixes contain no spaces.
 # Unlike --one-file-system, this also detects bind mounts on the same device.
 awk -v p="$1" '$5 == p || index($5, p "/") == 1 { found=1 } END { exit found ? 1 : 0 }' /proc/self/mountinfo
}
receipt_uid() {
 file=$1
 [ -f "$file" ] && [ ! -L "$file" ] && [ "$(stat -c %u "$file")" = 0 ] && [ "$(stat -c %h "$file")" = 1 ] || { echo 'Invalid package ownership receipt.' >&2; exit 1; }
 [ -z "$(find "$file" -prune -perm /022 -print)" ] || { echo 'Writable package receipt.' >&2; exit 1; }
 uid=$(cat "$file")
 case "$uid" in ''|*[!0-9]*|0) echo 'Invalid package uid.' >&2; exit 1;; esac
 [ "$(id -u kurumifrp)" = "$uid" ] || { echo 'Service account changed.' >&2; exit 1; }
}
wait_account() {
 command -v pgrep >/dev/null 2>&1 || { echo 'Cannot inspect service-account processes.' >&2; exit 1; }
 n=0
 while :; do
  if pgrep -u "$uid" >/dev/null; then
   n=$((n+1)); [ "$n" -lt 50 ] || { echo 'Service account still has live processes.' >&2; exit 1; }
   sleep 0.1
  else
   status=$?; [ "$status" = 1 ] || { echo 'Cannot inspect service-account processes.' >&2; exit 1; }
   break
  fi
 done
}
stop_unit_if_present() {
 unit=${1:-$UNIT}
 if systemd_active; then
  state=$(systemctl show -p LoadState --value "$unit" 2>/dev/null || true)
  if [ -n "$state" ] && [ "$state" != not-found ]; then systemctl stop "$unit"; fi
 fi
}
stop_workbench() {
 plain
 stop_unit_if_present
 if [ -e "$BASE/data/manager.lock" ]; then /usr/bin/kurumifrp --stop; fi
}
pre_upgrade() {
 plain
 if [ -e "$OLD_META/uid" ]; then
  [ "$(stat -c %u "$OLD_META")" = 0 ] && [ "$(stat -c %a "$OLD_META")" = 700 ] || { echo 'Untrusted legacy metadata.' >&2; exit 1; }
  receipt_uid "$OLD_META/uid"
  stop_unit_if_present kurumifrp-updater.path
  stop_unit_if_present
  if [ -e "$OLD_BASE/data/manager.lock" ]; then /usr/bin/kurumifrp --stop; fi
  wait_account
  [ ! -e "$OLD_BASE/data/manager.lock" ] || { echo 'Legacy manager state unresolved.' >&2; exit 1; }
  # Establish the migration guard before the new service unit is unpacked.
  [ "$(stat -c %u /opt)" = 0 ] && [ -z "$(find /opt -prune -perm /022 -print)" ] || { echo 'Unsafe /opt directory.' >&2; exit 1; }
  if [ ! -e "$ROOT" ]; then install -d -m 755 -o root -g root "$ROOT"; fi
  [ -d "$ROOT" ] && [ "$(stat -c %u "$ROOT")" = 0 ] && [ "$(stat -c %a "$ROOT")" = 755 ] || { echo 'Unsafe installation directory.' >&2; exit 1; }
  if [ ! -e "$META" ]; then install -d -m 700 -o root -g root "$META"; fi
  [ -d "$META" ] && [ "$(stat -c %u "$META")" = 0 ] && [ "$(stat -c %a "$META")" = 700 ] || { echo 'Unsafe metadata directory.' >&2; exit 1; }
  if [ -e "$META/uid" ]; then
   receipt_uid "$META/uid"
   [ -e "$META/legacy-cleanup" ] || { echo 'Ambiguous legacy data retained.' >&2; exit 1; }
  elif [ ! -e "$META/migration-intent" ]; then
   [ ! -e "$BASE" ] || { echo 'Unexpected target runtime retained.' >&2; exit 1; }
   printf '%s\n' "$uid" > "$META/migration-intent"
   sync -f "$META/migration-intent"; sync -f "$META"
  else receipt_uid "$META/migration-intent"; fi
 elif [ -e "$META/uid" ]; then
  receipt_uid "$META/uid"; stop_unit_if_present kurumifrp-updater.path; stop_workbench; wait_account
 else
  [ ! -e "$OLD_BASE" ] && [ ! -e "$OLD_META" ] && [ ! -e "$BASE" ] && [ ! -e /usr/bin/kurumifrp ] && [ ! -L /usr/bin/kurumifrp ] || { echo 'Unmanaged installation retained; refusing replacement.' >&2; exit 1; }
 fi
}
setup_state() {
 plain
 [ "$(stat -c %u /opt)" = 0 ] && [ -z "$(find /opt -prune -perm /022 -print)" ] || { echo 'Unsafe /opt directory.' >&2; exit 1; }
 [ "$(stat -c %u "$ROOT")" = 0 ] && [ "$(stat -c %a "$ROOT")" = 755 ] || { echo 'Unsafe installation directory.' >&2; exit 1; }
 for p in "$META" "$ROOT/root-tmp"; do
  if [ -e "$p" ]; then
   [ -d "$p" ] && [ "$(stat -c %u "$p")" = 0 ] && [ "$(stat -c %a "$p")" = 700 ] || { echo "Unsafe protected directory: $p" >&2; exit 1; }
  else install -d -m 700 -o root -g root "$p"; fi
 done
 [ "$(stat -c %u "$META")" = 0 ] && [ "$(stat -c %a "$META")" = 700 ] || { echo 'Unsafe metadata directory.' >&2; exit 1; }
 [ "$(stat -c %u "$ROOT/root-tmp")" = 0 ] && [ "$(stat -c %a "$ROOT/root-tmp")" = 700 ] || { echo 'Unsafe updater directory.' >&2; exit 1; }
 if [ -e "$OLD_META/uid" ] && [ ! -e "$META/uid" ]; then
  [ "$(stat -c %u "$OLD_META")" = 0 ] && [ "$(stat -c %a "$OLD_META")" = 700 ] || { echo 'Untrusted legacy metadata.' >&2; exit 1; }
  receipt_uid "$OLD_META/uid"; wait_account
  [ ! -e "$OLD_BASE/data/manager.lock" ] || { echo 'Legacy manager state unresolved.' >&2; exit 1; }
  if [ ! -e "$META/migration-intent" ]; then
   [ ! -e "$BASE" ] || { echo 'Unexpected target runtime retained.' >&2; exit 1; }
   printf '%s\n' "$uid" > "$META/migration-intent"
   sync -f "$META/migration-intent"; sync -f "$META"
  else receipt_uid "$META/migration-intent"; fi
  for stage in "$ROOT"/.kurumi-migration-*; do
   [ ! -e "$stage" ] || { echo 'Interrupted migration staging retained; manual review required.' >&2; exit 1; }
  done
  "$ROOT/kurumifrp" --migrate-linux-legacy
  printf '%s\n' "$uid" > "$META/legacy-cleanup"
  sync -f "$META/legacy-cleanup"; sync -f "$META"
  mv "$META/migration-intent" "$META/uid"
  sync -f "$META"
 elif [ -e "$META/uid" ]; then
  receipt_uid "$META/uid"
  [ -d "$BASE" ] || { echo 'Installed runtime missing; refusing empty state.' >&2; exit 1; }
 else
  [ ! -e "$BASE" ] && [ ! -e "$OLD_BASE" ] && [ ! -e "$OLD_META" ] || { echo 'Unmanaged runtime retained.' >&2; exit 1; }
  if getent passwd kurumifrp >/dev/null || getent group kurumifrp >/dev/null; then echo 'Existing account not owned by package.' >&2; exit 1; fi
  useradd --system --user-group --no-create-home --home-dir "$BASE" --shell /usr/sbin/nologin kurumifrp
  uid=$(id -u kurumifrp); printf '%s\n' "$uid" > "$META/uid"
  install -d -m 700 -o kurumifrp -g kurumifrp "$BASE"
  sync -f "$META/uid"; sync -f "$META"; sync -f "$ROOT"
 fi
 if [ -e "$META/legacy-cleanup" ]; then
  receipt_uid "$META/legacy-cleanup"; wait_account
  [ ! -e "$OLD_BASE/data/manager.lock" ] || { echo 'Legacy lock retained.' >&2; exit 1; }
  if [ -e "$OLD_BASE" ]; then
   # Recheck source equality after waiting: an old process may have changed it
   # since the initial copy. A partial cleanup is retained for manual review.
   "$ROOT/kurumifrp" --migrate-linux-legacy
   wait_account
   [ "$(stat -c %u "$OLD_BASE")" = "$uid" ] && no_mounts "$OLD_BASE" || { echo 'Legacy runtime untrusted or mounted; retained.' >&2; exit 1; }
   rm -rf --one-file-system -- "$OLD_BASE"
   [ ! -e "$OLD_BASE" ] || { echo 'Legacy runtime not fully removed.' >&2; exit 1; }
  fi
  if [ -e "$OLD_META" ]; then
   if [ -e "$OLD_META/uid" ]; then receipt_uid "$OLD_META/uid"; rm -f -- "$OLD_META/uid"; fi
   rmdir "$OLD_META"
  fi
  sync -f /var/lib
  rm -f -- "$META/legacy-cleanup"
  sync -f "$META"
 fi
}
start_workbench() {
 if systemd_active; then
  systemctl daemon-reload; systemctl enable --now kurumifrp-updater.path
  if command -v deb-systemd-helper >/dev/null 2>&1; then
   deb-systemd-helper unmask "$UNIT" >/dev/null || true
   if deb-systemd-helper --quiet was-enabled "$UNIT"; then deb-systemd-helper enable "$UNIT" >/dev/null; fi
   deb-systemd-invoke start "$UNIT"
  else systemctl preset "$UNIT"; systemctl start "$UNIT"; fi
 else echo 'Package installed. No active systemd was found.'; fi
}
remove_unit_state() {
 if command -v deb-systemd-helper >/dev/null 2>&1; then deb-systemd-helper purge "$UNIT" >/dev/null; fi
 if systemd_active; then
  systemctl disable --now kurumifrp-updater.path >/dev/null 2>&1 || true
  systemctl disable "$UNIT" >/dev/null 2>&1 || true
  systemctl daemon-reload; systemctl reset-failed "$UNIT" >/dev/null 2>&1 || true
 fi
}
lock_updater_for_purge() {
 stop_unit_if_present kurumifrp-updater.path
 if systemd_active; then
  update_state=$(systemctl show -p ActiveState --value kurumifrp-updater.service 2>/dev/null) || { echo 'Cannot inspect updater service.' >&2; exit 1; }
  case "$update_state" in inactive|failed) ;; *) echo 'Updater service is active; purge stopped.' >&2; exit 1;; esac
 fi
 scratch=$ROOT/root-tmp
 if [ -e "$scratch" ]; then
  [ -d "$scratch" ] && [ "$(stat -c %u "$scratch")" = 0 ] && [ "$(stat -c %a "$scratch")" = 700 ] && no_mounts "$scratch" || { echo 'Unsafe updater scratch; retained.' >&2; exit 1; }
  command -v flock >/dev/null 2>&1 || { echo 'Cannot lock updater scratch.' >&2; exit 1; }
  lock=$scratch/.updater.lock
  [ ! -L "$lock" ] || { echo 'Linked updater lock; retained.' >&2; exit 1; }
  if [ ! -e "$lock" ]; then (set -C; : > "$lock") || { echo 'Cannot create updater lock.' >&2; exit 1; }; fi
  [ -f "$lock" ] && [ "$(stat -c %u "$lock")" = 0 ] && [ "$(stat -c %g "$lock")" = 0 ] && [ "$(stat -c %h "$lock")" = 1 ] && [ "$(stat -c %a "$lock")" = 600 ] || { echo 'Unsafe updater lock; retained.' >&2; exit 1; }
  exec 9>>"$lock"
  flock -n 9 || { echo 'An update or purge is active; retained.' >&2; exit 1; }
 fi
}
purge_state() {
 plain
 [ ! -e "$META/migration-intent" ] && [ ! -e "$META/legacy-cleanup" ] || { echo 'Incomplete migration; data retained.' >&2; exit 1; }
 if [ ! -e "$META" ]; then
  [ ! -e "$BASE" ] && [ ! -e "$ROOT/root-tmp" ] && ! getent passwd kurumifrp >/dev/null && ! getent group kurumifrp >/dev/null || { echo 'Ownership missing; retained.' >&2; exit 1; }
  if [ -d "$ROOT" ]; then rmdir "$ROOT"; fi
  return
 fi
 [ -d "$META" ] && [ "$(stat -c %u "$META")" = 0 ] && [ "$(stat -c %a "$META")" = 700 ] || { echo 'Unsafe metadata; retained.' >&2; exit 1; }
 for p in "$ROOT"/* "$ROOT"/.[!.]* "$ROOT"/..?*; do
  [ -e "$p" ] || [ -L "$p" ] || continue
  case "${p##*/}" in runtime|meta|root-tmp) ;; *) echo 'Unknown installation entry; retained.' >&2; exit 1;; esac
 done
 for p in "$META"/* "$META"/.[!.]* "$META"/..?*; do
  [ -e "$p" ] || [ -L "$p" ] || continue
  [ ! -L "$p" ] || { echo 'Linked metadata entry; retained.' >&2; exit 1; }
  case "${p##*/}" in uid|purge-intent) ;; *) echo 'Unknown metadata entry; retained.' >&2; exit 1;; esac
 done
 if [ ! -e "$META/uid" ] && [ ! -e "$META/purge-intent" ]; then
  [ ! -e "$BASE" ] && [ ! -e "$ROOT/root-tmp" ] && ! getent passwd kurumifrp >/dev/null && ! getent group kurumifrp >/dev/null || { echo 'Purge receipt missing; retained.' >&2; exit 1; }
  rmdir "$META"; rmdir "$ROOT"; return
 fi
 if [ -e "$META/purge-intent" ]; then
  file=$META/purge-intent
  [ -f "$file" ] && [ ! -L "$file" ] && [ "$(stat -c %u "$file")" = 0 ] && [ "$(stat -c %h "$file")" = 1 ] && [ -z "$(find "$file" -prune -perm /022 -print)" ] || { echo 'Invalid purge intent.' >&2; exit 1; }
  pair=$(cat "$file"); uid=${pair%%:*}; gid=${pair#*:}
  [ "$pair" = "$uid:$gid" ] || { echo 'Invalid purge intent.' >&2; exit 1; }
  case "$uid:$gid" in *[!0-9:]*|:*|*:|*:*:*) echo 'Invalid purge intent.' >&2; exit 1;; esac
  [ "$uid" != 0 ] && [ "$gid" != 0 ] || { echo 'Invalid purge owner.' >&2; exit 1; }
  if [ -e "$META/uid" ]; then
   [ -f "$META/uid" ] && [ ! -L "$META/uid" ] && [ "$(stat -c %u "$META/uid")" = 0 ] && [ "$(cat "$META/uid")" = "$uid" ] || { echo 'Purge receipt mismatch.' >&2; exit 1; }
  fi
 else
  receipt_uid "$META/uid"
  gid=$(id -g kurumifrp)
 fi
 if getent passwd kurumifrp >/dev/null; then
  [ "$(id -u kurumifrp)" = "$uid" ] && [ "$(id -g kurumifrp)" = "$gid" ] || { echo 'Service account changed; retained.' >&2; exit 1; }
 else
  [ ! -e "$BASE" ] && [ -e "$META/purge-intent" ] || { echo 'Service account missing; retained.' >&2; exit 1; }
  ! getent passwd "$uid" >/dev/null || { echo 'Service uid reused; retained.' >&2; exit 1; }
 fi
 if getent group kurumifrp >/dev/null; then
  [ "$(getent group kurumifrp | cut -d: -f3)" = "$gid" ] || { echo 'Service group changed; retained.' >&2; exit 1; }
 fi
 wait_account
 [ ! -e "$BASE/data/manager.lock" ] || { echo 'Unresolved process state; purge stopped.' >&2; exit 1; }
 [ ! -e "$BASE" ] || { [ "$(stat -c %u "$BASE")" = "$uid" ] && no_mounts "$BASE"; } || { echo 'Runtime untrusted or mounted; retained.' >&2; exit 1; }
 lock_updater_for_purge
 if [ -e "$scratch" ]; then
  for p in "$scratch"/* "$scratch"/.[!.]* "$scratch"/..?*; do
   [ -e "$p" ] || [ -L "$p" ] || continue
   case "${p##*/}" in .updater.lock) continue;; update-*) ;; *) echo 'Unknown updater scratch; retained.' >&2; exit 1;; esac
   [ -d "$p" ] && [ ! -L "$p" ] && [ "$(stat -c %u "$p")" = 0 ] && [ "$(stat -c %a "$p")" = 700 ] || { echo 'Unsafe updater staging; retained.' >&2; exit 1; }
  done
 fi
 if [ ! -e "$META/purge-intent" ]; then printf '%s:%s\n' "$uid" "$gid" > "$META/purge-intent"; sync -f "$META/purge-intent"; sync -f "$META"; fi
 if [ -e "$scratch" ]; then
  for p in "$scratch"/update-*; do [ ! -e "$p" ] || rm -rf --one-file-system -- "$p"; done
 fi
 rm -rf --one-file-system -- "$BASE"
 [ ! -e "$BASE" ] || { echo 'Runtime not fully removed.' >&2; exit 1; }
 if getent passwd kurumifrp >/dev/null; then userdel kurumifrp; fi
 if getent group kurumifrp >/dev/null; then groupdel kurumifrp; fi
 if [ -e "$scratch" ]; then rm -f -- "$scratch/.updater.lock"; rmdir "$scratch"; fi
 rm -f -- "$META/uid"; sync -f "$META"
 rm -f -- "$META/purge-intent"; rmdir "$META"; rmdir "$ROOT"
}
