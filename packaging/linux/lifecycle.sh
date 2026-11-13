# Embedded in native package scripts, executed by the package manager as root.
set -eu
umask 077
ROOT=/opt/kurumifrp
BUNDLED=$ROOT/bundled/@PACKAGE_VERSION@
BASE=$ROOT/runtime
META=$ROOT/meta
PRIVATE=$ROOT/private
BACKUP=$ROOT/backups/legacy
OLD=/var/lib/kurumifrp
OLD_META=/var/lib/kurumifrp-package
UNIT=kurumifrp.service

plain() {
 for p in /opt "$ROOT" "$ROOT/kurumifrp" "$ROOT/bundled" "$BUNDLED" "$BUNDLED/frpc" "$BUNDLED/frps" "$BUNDLED/LICENSE" "$BUNDLED/manifest.json" "$BASE" "$META" "$META/identity" "$META/migrating" "$META/.receipt.tmp" "$PRIVATE" "$ROOT/private.purging" "$ROOT/backups" "$BACKUP" /var /var/lib "$OLD" "$OLD_META" "$OLD_META/uid"; do
  [ ! -L "$p" ] || { echo "Refusing linked package state: $p" >&2; exit 1; }
 done
}
write_receipt() {
 target=$1
 temp=$META/.receipt.tmp
 if [ -e "$temp" ]; then
  [ -f "$temp" ] && [ ! -L "$temp" ] && [ "$(stat -c %u "$temp")" = 0 ] && [ "$(stat -c %h "$temp")" = 1 ] || { echo 'Unsafe receipt staging file.' >&2; exit 1; }
  rm -f -- "$temp"
 fi
 [ ! -L "$temp" ] || { echo 'Linked receipt staging file.' >&2; exit 1; }
 printf '%s:%s\n' "$uid" "$gid" > "$temp"
 chmod 600 "$temp"; sync -f "$temp"
 mv -T -- "$temp" "$target"; sync -f "$META"
}
secure_root() {
 plain
 [ -d /opt ] && [ "$(stat -c %u /opt)" = 0 ] || { echo 'Unsafe /opt directory.' >&2; exit 1; }
 mode=$(stat -c %a /opt)
 [ "$((0$mode & 0022))" = 0 ] || { echo 'Unsafe /opt permissions.' >&2; exit 1; }
 if [ ! -e "$ROOT" ]; then install -d -m 755 -o root -g root "$ROOT"; fi
 [ -d "$ROOT" ] && [ "$(stat -c %u "$ROOT")" = 0 ] && [ "$(stat -c %a "$ROOT")" = 755 ] || { echo 'Unsafe installation directory.' >&2; exit 1; }
 if [ ! -e "$META" ]; then install -d -m 700 -o root -g root "$META"; fi
 [ -d "$META" ] && [ "$(stat -c %u "$META")" = 0 ] && [ "$(stat -c %a "$META")" = 700 ] || { echo 'Unsafe package metadata.' >&2; exit 1; }
}
read_receipt() {
 receipt=$1
 [ -f "$receipt" ] && [ ! -L "$receipt" ] && [ "$(stat -c %u "$receipt")" = 0 ] && [ "$(stat -c %h "$receipt")" = 1 ] || { echo 'Unsafe ownership receipt.' >&2; exit 1; }
 mode=$(stat -c %a "$receipt")
 [ "$((0$mode & 0022))" = 0 ] || { echo 'Writable ownership receipt.' >&2; exit 1; }
 value=$(cat "$receipt")
 case "$value" in
  *:*) uid=${value%%:*}; gid=${value#*:}; [ -n "$gid" ] && [ "$value" = "$uid:$gid" ] || { echo 'Invalid ownership receipt.' >&2; exit 1; } ;;
  *) uid=$value; gid= ;;
 esac
 case "$uid" in ''|*[!0-9]*|0) echo 'Invalid ownership receipt.' >&2; exit 1;; esac
 case "$gid" in *[!0-9]*|0) echo 'Invalid group receipt.' >&2; exit 1;; esac
}
systemd_active() { [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1; }
wait_account() {
 n=0
 while :; do
  if pgrep -u "$uid" >/dev/null; then
   n=$((n+1)); [ "$n" -lt 50 ] || { echo 'Service account still has live processes.' >&2; exit 1; }
   sleep 0.1
  else
   status=$?; [ "$status" = 1 ] || { echo 'Cannot inspect service processes.' >&2; exit 1; }
   break
  fi
 done
}
stop_workbench() {
 plain
 if systemd_active; then
  for service in kurumifrp-updater.path "$UNIT"; do
   state=$(systemctl show -p LoadState --value "$service" 2>/dev/null || true)
   if [ -n "$state" ] && [ "$state" != not-found ]; then systemctl stop "$service"; fi
  done
 fi
 for data in "$OLD/data" "$BASE/data"; do
  if [ -e "$data/manager.lock" ]; then
   [ -x /usr/bin/kurumifrp ] || { echo 'Cannot stop existing manager.' >&2; exit 1; }
   /usr/bin/kurumifrp --data "$data" --stop
  fi
 done
}
# Refuse bind mounts too, including mounts on the same underlying device.
no_mounts() {
 awk -v p="$1" '$5 == p || index($5,p "/") == 1 { found=1 } END { exit found ? 1 : 0 }' /proc/self/mountinfo
}
safe_tree() {
 tree=$1
 [ -d "$tree" ] && [ ! -L "$tree" ] && no_mounts "$tree" || { echo "Unsafe or mounted data: $tree" >&2; exit 1; }
 # Every entry must be a regular file or directory, owned by root or the
 # service account, and unavailable for writes by unrelated accounts.
 bad=$(find "$tree" \( \( ! -type d -a ! -type f \) -o \( ! -uid 0 -a ! -uid "$uid" \) -o -perm /022 -o \( -type f -a -links +1 \) \) -print -quit)
 [ -z "$bad" ] || { echo "Unsafe entry in $tree: $bad" >&2; exit 1; }
}
# No package receipt means existing destination files have no proven owner.
# Only an empty root and empty metadata directory are safe to claim.
unclaimed_target_empty() {
 if [ -d "$ROOT" ]; then no_mounts "$ROOT" || { echo 'Mounted unclaimed installation directory retained.' >&2; exit 1; }; fi
 for entry in "$ROOT"/* "$ROOT"/.[!.]* "$ROOT"/..?*; do
  [ -e "$entry" ] || [ -L "$entry" ] || continue
  [ "$entry" = "$META" ] || { echo "Unmanaged installation file retained: $entry" >&2; exit 1; }
 done
 if [ -e "$META" ]; then
  [ -d "$META" ] && [ ! -L "$META" ] || { echo 'Unmanaged metadata retained.' >&2; exit 1; }
  for entry in "$META"/* "$META"/.[!.]* "$META"/..?*; do
   [ -e "$entry" ] || [ -L "$entry" ] || continue
   echo "Unmanaged metadata retained: $entry" >&2; exit 1
  done
 fi
}
unclaimed_package_targets_empty() {
 for entry in /usr/bin/kurumifrp /usr/lib/systemd/system/kurumifrp.service /usr/lib/systemd/system/kurumifrp-updater.service /usr/lib/systemd/system/kurumifrp-updater.path /usr/share/doc/kurumifrp /usr/share/man/man1/kurumifrp.1.gz; do
  [ ! -e "$entry" ] && [ ! -L "$entry" ] || { echo "Unmanaged package file retained: $entry" >&2; exit 1; }
 done
}
legacy_package_targets_owned() {
 docdir=/usr/share/doc/kurumifrp
 [ ! -L "$docdir" ] && { [ ! -e "$docdir" ] || [ -d "$docdir" ]; } || { echo 'Unmanaged documentation path retained.' >&2; exit 1; }
 for entry in /usr/bin/kurumifrp /usr/lib/systemd/system/kurumifrp.service /usr/lib/systemd/system/kurumifrp-updater.service /usr/lib/systemd/system/kurumifrp-updater.path /usr/share/doc/kurumifrp/README.md /usr/share/doc/kurumifrp/copyright /usr/share/doc/kurumifrp/changelog.gz /usr/share/man/man1/kurumifrp.1.gz; do
  [ -e "$entry" ] || [ -L "$entry" ] || continue
  [ ! -L "$entry" ] || { echo "Linked package file retained: $entry" >&2; exit 1; }
  owned=0
  if command -v dpkg-query >/dev/null 2>&1 && dpkg-query -S "$entry" 2>/dev/null | awk -F ': ' -v p="$entry" '$1 ~ /^kurumifrp(:[a-z0-9-]+)?$/ && $2 == p { found=1 } END { exit !found }'; then
   owned=1
  elif command -v rpm >/dev/null 2>&1 && [ "$(rpm -qf --qf '%{NAME}' "$entry" 2>/dev/null)" = kurumifrp ]; then
   owned=1
  fi
  [ "$owned" = 1 ] || { echo "Unmanaged package file retained: $entry" >&2; exit 1; }
 done
}
pre_upgrade() {
 plain
 if [ ! -e "$META/identity" ]; then
  unclaimed_target_empty
  if [ ! -e "$OLD_META/uid" ]; then unclaimed_package_targets_empty; fi
 fi
 secure_root
 [ ! -e "$ROOT/private.purging" ] || { echo 'Finish the interrupted purge before installing.' >&2; exit 1; }
 if [ -e "$OLD_META/uid" ]; then
  [ -d "$OLD_META" ] && [ "$(stat -c %u "$OLD_META")" = 0 ] && [ "$(stat -c %a "$OLD_META")" = 700 ] || { echo 'Unsafe legacy metadata.' >&2; exit 1; }
  read_receipt "$OLD_META/uid"
  [ "$(id -u kurumifrp)" = "$uid" ] || { echo 'Service account changed.' >&2; exit 1; }
  legacy_uid=$uid; legacy_gid=$(id -g kurumifrp)
  case "$legacy_gid" in ''|0|*[!0-9]*) echo 'Unsafe service group.' >&2; exit 1;; esac
  [ "$(getent group kurumifrp | cut -d: -f3)" = "$legacy_gid" ] || { echo 'Service group changed.' >&2; exit 1; }
  if [ ! -e "$META/identity" ]; then legacy_package_targets_owned; fi
  stop_workbench; wait_account
  [ ! -e "$OLD/data/manager.lock" ] || { echo 'Unresolved legacy manager state.' >&2; exit 1; }
  if [ -e "$META/identity" ]; then
   read_receipt "$META/identity"; [ "$uid:$gid" = "$legacy_uid:$legacy_gid" ] || { echo 'Ownership conflict.' >&2; exit 1; }
  else
   uid=$legacy_uid; gid=$legacy_gid; write_receipt "$META/identity"
  fi
  if [ ! -e "$META/migrating" ]; then
   [ ! -e "$BASE" ] && [ ! -e "$BACKUP" ] || { echo 'Existing destination retained.' >&2; exit 1; }
   write_receipt "$META/migrating"
  fi
 elif [ -e "$META/identity" ]; then
  read_receipt "$META/identity"; [ -n "$gid" ] || { echo 'Incomplete ownership receipt.' >&2; exit 1; }
  if getent passwd kurumifrp >/dev/null; then
   [ "$(id -u kurumifrp)" = "$uid" ] && [ "$(id -g kurumifrp)" = "$gid" ] || { echo 'Service account changed.' >&2; exit 1; }
   stop_workbench; wait_account
  fi
 else
  [ ! -e "$OLD" ] && [ ! -e "$BASE" ] && [ ! -e "$BACKUP" ] || { echo 'Unmanaged installation retained.' >&2; exit 1; }
  ! getent passwd kurumifrp >/dev/null && ! getent group kurumifrp >/dev/null || { echo 'Existing account is not owned by this package.' >&2; exit 1; }
  # Record a free system UID before creating either account; retries can then
  # distinguish an interrupted package operation from an existing account.
  uid=200
  while getent passwd "$uid" >/dev/null || getent group "$uid" >/dev/null; do uid=$((uid+1)); [ "$uid" -lt 1000 ] || { echo 'No free system UID.' >&2; exit 1; }; done
  gid=$uid; write_receipt "$META/identity"
 fi
}
setup_state() {
 secure_root
 [ ! -e "$ROOT/private.purging" ] || { echo 'Finish the interrupted purge before installing.' >&2; exit 1; }
 read_receipt "$META/identity"
 [ -n "$gid" ] || { echo 'Incomplete ownership receipt.' >&2; exit 1; }
 account_uid=$uid; account_gid=$gid
 if getent passwd kurumifrp >/dev/null; then
  [ "$(id -u kurumifrp)" = "$uid" ] && [ "$(id -g kurumifrp)" = "$gid" ] || { echo 'Service account changed.' >&2; exit 1; }
  home=$(getent passwd kurumifrp | cut -d: -f6)
  shell=$(getent passwd kurumifrp | cut -d: -f7)
  [ "$shell" = /usr/sbin/nologin ] || { echo 'Service account shell changed.' >&2; exit 1; }
  if [ "$home" != "$BASE" ]; then
   [ "$home" = "$OLD" ] && { [ -e "$META/migrating" ] || [ -d "$BACKUP" ]; } || { echo 'Service account home changed.' >&2; exit 1; }
  fi
 fi
 if getent group kurumifrp >/dev/null; then
  [ "$(getent group kurumifrp | cut -d: -f3)" = "$gid" ] || { echo 'Service group changed.' >&2; exit 1; }
 else
  ! getent group "$gid" >/dev/null || { echo 'Service group ID reused.' >&2; exit 1; }
  groupadd --system --gid "$gid" kurumifrp
 fi
 if getent passwd kurumifrp >/dev/null; then
  [ "$(id -u kurumifrp)" = "$uid" ] || { echo 'Service account changed.' >&2; exit 1; }
 else
  ! getent passwd "$uid" >/dev/null || { echo 'Service user ID reused.' >&2; exit 1; }
  useradd --system --uid "$uid" --gid "$gid" --no-create-home --home-dir "$BASE" --shell /usr/sbin/nologin kurumifrp
 fi
 if [ ! -e "$PRIVATE" ]; then install -d -m 700 -o root -g root "$PRIVATE"; fi
 [ -d "$PRIVATE" ] && [ "$(stat -c %u "$PRIVATE")" = 0 ] && [ "$(stat -c %a "$PRIVATE")" = 700 ] || { echo 'Unsafe updater directory.' >&2; exit 1; }
 if [ -e "$META/migrating" ]; then
  read_receipt "$META/migrating"; [ "$uid:$gid" = "$account_uid:$account_gid" ] || { echo 'Migration identity changed.' >&2; exit 1; }
  if systemd_active; then systemctl daemon-reload; fi
  wait_account
  [ ! -e "$OLD/data/manager.lock" ] || { echo 'Unresolved legacy manager state.' >&2; exit 1; }
  if [ ! -e "$ROOT/backups" ]; then install -d -m 700 -o root -g root "$ROOT/backups"; fi
  [ -d "$ROOT/backups" ] && [ "$(stat -c %u "$ROOT/backups")" = 0 ] && [ "$(stat -c %a "$ROOT/backups")" = 700 ] || { echo 'Unsafe backup directory.' >&2; exit 1; }
  if [ ! -e "$BACKUP" ]; then
   safe_tree "$OLD"
   # Prevent a newly started service-account process from changing the source
   # while root copies it. A retry accepts this protected legacy directory.
   owner=$(stat -c %u "$OLD")
   [ "$owner" = "$uid" ] || [ "$owner" = 0 ] || { echo 'Legacy root ownership changed.' >&2; exit 1; }
   chown root:root "$OLD"; chmod 700 "$OLD"; wait_account
   safe_tree "$OLD"
   [ ! -L "$BACKUP.tmp" ] || { echo 'Linked backup staging path.' >&2; exit 1; }
   if [ -e "$BACKUP.tmp" ]; then
    [ -d "$BACKUP.tmp" ] && [ ! -L "$BACKUP.tmp" ] && no_mounts "$BACKUP.tmp" || { echo 'Unsafe interrupted backup.' >&2; exit 1; }
    rm -rf --one-file-system -- "$BACKUP.tmp"
   fi
   cp -a -- "$OLD" "$BACKUP.tmp"
   sync -f "$BACKUP.tmp"; mv -T -- "$BACKUP.tmp" "$BACKUP"; sync -f "$ROOT/backups"
  fi
  if [ ! -e "$BASE" ]; then
   safe_tree "$BACKUP"
   [ ! -L "$ROOT/runtime.tmp" ] || { echo 'Linked runtime staging path.' >&2; exit 1; }
   if [ -e "$ROOT/runtime.tmp" ]; then
    [ -d "$ROOT/runtime.tmp" ] && [ ! -L "$ROOT/runtime.tmp" ] && no_mounts "$ROOT/runtime.tmp" || { echo 'Unsafe interrupted runtime copy.' >&2; exit 1; }
    rm -rf --one-file-system -- "$ROOT/runtime.tmp"
   fi
   cp -a -- "$BACKUP" "$ROOT/runtime.tmp"
   sync -f "$ROOT/runtime.tmp"; mv -T -- "$ROOT/runtime.tmp" "$BASE"; sync -f "$ROOT"
  fi
  [ -d "$BASE" ] && [ -d "$BACKUP" ] || { echo 'Migration output missing.' >&2; exit 1; }
  owner=$(stat -c %u "$BASE")
  [ "$owner" = "$uid" ] || [ "$owner" = 0 ] || { echo 'Migrated runtime ownership changed.' >&2; exit 1; }
  chown root:root "$BASE"; chmod 700 "$BASE"; wait_account
  if [ -e "$OLD" ]; then safe_tree "$OLD"; rm -rf --one-file-system -- "$OLD"; fi
  if [ -e "$OLD_META/uid" ]; then
   read_receipt "$OLD_META/uid"; [ "$uid" = "$account_uid" ] || { echo 'Legacy ownership changed.' >&2; exit 1; }
   rm -f -- "$OLD_META/uid"; uid=$account_uid; gid=$account_gid
  fi
  if [ -d "$OLD_META" ]; then rmdir "$OLD_META"; fi
  sync -f /var/lib
  chown "$uid:$gid" "$BASE"
  rm -f -- "$META/migrating"; sync -f "$META"
 else
  if [ ! -e "$BASE" ]; then install -d -m 700 -o "$uid" -g "$gid" "$BASE"; fi
 fi
 [ -d "$BASE" ] && [ "$(stat -c %u "$BASE")" = "$account_uid" ] || { echo 'Runtime ownership changed.' >&2; exit 1; }
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
   # Keep an already enabled unit instead of resetting it to the preset.
   if ! systemctl is-enabled --quiet "$UNIT"; then systemctl preset "$UNIT"; fi
   systemctl start "$UNIT"
  fi
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
 scratch=$PRIVATE
 if [ -e "$ROOT/private.purging" ]; then
  [ ! -e "$PRIVATE" ] || { echo 'Ambiguous updater directories.' >&2; exit 1; }
  scratch=$ROOT/private.purging
 fi
 [ ! -e "$scratch" ] && return
 [ -d "$scratch" ] && [ ! -L "$scratch" ] && [ "$(stat -c %u "$scratch")" = 0 ] && [ "$(stat -c %a "$scratch")" = 700 ] || { echo 'Unsafe updater directory.' >&2; exit 1; }
 lock=$scratch/.update.lock
 [ ! -L "$lock" ] || { echo 'Linked updater lock.' >&2; exit 1; }
 if [ -e "$lock" ]; then
  [ -f "$lock" ] && [ "$(stat -c %u "$lock")" = 0 ] && [ "$(stat -c %h "$lock")" = 1 ] && [ "$(stat -c %a "$lock")" = 600 ] || { echo 'Unsafe updater lock.' >&2; exit 1; }
 fi
 exec 9>>"$lock"
 chmod 600 "$lock"
 flock -n 9 || { echo 'Update active; purge deferred.' >&2; exit 1; }
}
purge_state() {
 plain
 if [ ! -e "$ROOT" ]; then
  [ ! -e "$OLD" ] && ! getent passwd kurumifrp >/dev/null || { echo 'Ownership missing; data retained.' >&2; exit 1; }
  return
 fi
 secure_root
 lock_updater_for_purge
 [ ! -e "$META/migrating" ] || { echo 'Incomplete migration; data retained.' >&2; exit 1; }
 if [ ! -e "$META/identity" ]; then
  [ ! -e "$BASE" ] && [ ! -e "$BACKUP" ] && [ ! -e "$PRIVATE" ] && [ ! -e "$ROOT/private.purging" ] && ! getent passwd kurumifrp >/dev/null && ! getent group kurumifrp >/dev/null || { echo 'Ownership missing; data retained.' >&2; exit 1; }
  rm -f -- "$META/.receipt.tmp"; rmdir "$META"; rmdir "$ROOT"; return
 fi
 read_receipt "$META/identity"
 [ -n "$gid" ] || { echo 'Incomplete ownership receipt.' >&2; exit 1; }
 if getent passwd kurumifrp >/dev/null; then
  [ -n "$gid" ] && [ "$(id -u kurumifrp)" = "$uid" ] && [ "$(id -g kurumifrp)" = "$gid" ] || { echo 'Service account changed.' >&2; exit 1; }
 else
  ! getent passwd "$uid" >/dev/null && [ ! -e "$BASE" ] && [ ! -e "$ROOT/backups" ] || { echo 'Service account missing or UID reused; data retained.' >&2; exit 1; }
 fi
 wait_account
 if getent group kurumifrp >/dev/null; then
  [ -n "$gid" ] && [ "$(getent group kurumifrp | cut -d: -f3)" = "$gid" ] || { echo 'Service group changed.' >&2; exit 1; }
 else
  ! getent group "$gid" >/dev/null || { echo 'Service group ID reused; data retained.' >&2; exit 1; }
 fi
 [ ! -e "$BASE/data/manager.lock" ] || { echo 'Unresolved process state; purge stopped.' >&2; exit 1; }
 if [ -e "$BASE" ]; then [ -d "$BASE" ] && [ "$(stat -c %u "$BASE")" = "$uid" ] || { echo 'Runtime ownership changed; data retained.' >&2; exit 1; }; fi
 if [ -e "$ROOT/backups" ]; then [ -d "$ROOT/backups" ] && [ "$(stat -c %u "$ROOT/backups")" = 0 ] && [ "$(stat -c %a "$ROOT/backups")" = 700 ] || { echo 'Backup ownership changed; data retained.' >&2; exit 1; }; fi
 for p in "$BASE" "$ROOT/backups" "$PRIVATE" "$ROOT/private.purging"; do
  if [ -e "$p" ]; then no_mounts "$p" || { echo 'Mounted data retained.' >&2; exit 1; }; fi
 done
 # Keep the lock inode while removing its public path. A new updater then
 # cannot create a second lock while purge holds the original one.
 if [ -e "$PRIVATE" ]; then mv -T -- "$PRIVATE" "$ROOT/private.purging"; sync -f "$ROOT"; fi
 rm -rf --one-file-system -- "$BASE" "$ROOT/backups" "$ROOT/private.purging"
 if getent passwd kurumifrp >/dev/null; then userdel kurumifrp; fi
 if getent group kurumifrp >/dev/null; then groupdel kurumifrp; fi
 rm -f -- "$META/identity" "$META/.receipt.tmp"; rmdir "$META"; rmdir "$ROOT"
}
