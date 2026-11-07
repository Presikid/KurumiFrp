#!/bin/sh
# Full per-user removal. Never kill processes by name or erase system journals.
set -eu
umask 077
uninstall() {
 BASE="$HOME/.local/share/kurumifrp"
 LAUNCHER="$HOME/.local/bin/kurumifrp"
 UNINSTALLER="$HOME/.local/bin/kurumifrp-uninstall"
 UNIT="$HOME/.config/systemd/user/kurumifrp.service"
 WANT="$HOME/.config/systemd/user/default.target.wants/kurumifrp.service"
 for P in "$HOME/.local" "$HOME/.local/share" "$BASE" "$BASE/data" "$BASE/bin" "$BASE/install.receipt" "$BASE/integration-hashes" "$HOME/.local/bin" "$HOME/.config" "$HOME/.config/systemd" "$HOME/.config/systemd/user" "$HOME/.config/systemd/user/default.target.wants"; do
  [ ! -L "$P" ] || { echo "Refusing linked path: $P" >&2; exit 1; }
 done
 [ -d "$BASE" ] || { echo 'KurumiFRP is not installed for this user.'; return; }
 [ "$(sed -n '1p' "$BASE/install.receipt")" = 'KurumiFRP user installation v1' ] && [ "$(sed -n '2p' "$BASE/install.receipt")" = "$BASE" ] || { echo 'Installation ownership not verified. Nothing removed.' >&2; exit 1; }
 verify() {
  if [ -e "$2" ] || [ -L "$2" ]; then
   [ ! -L "$2" ] && [ -f "$2" ] && [ ! -L "$BASE/integration-hashes/$1" ] && [ -f "$BASE/integration-hashes/$1" ] || { echo "Unrecognized integration retained: $2" >&2; exit 1; }
   SUM=$(sha256sum < "$2")
   [ "${SUM%% *}" = "$(cat "$BASE/integration-hashes/$1")" ] || { echo "Modified integration retained: $2" >&2; exit 1; }
  fi
 }
 verify launcher "$LAUNCHER"
 verify uninstall-launcher "$UNINSTALLER"
 verify unit "$UNIT"
 if [ -e "$WANT" ] || [ -L "$WANT" ]; then
  [ -L "$WANT" ] && [ "$(readlink -f "$WANT")" = "$UNIT" ] || { echo 'Unrelated startup link retained.' >&2; exit 1; }
 fi
 case "${1:-}" in
  --yes) ;;
  '') printf 'Permanently delete KurumiFRP, ALL keys, configurations, downloaded cores and logs? Type UNINSTALL: ' >/dev/tty
      read -r ANSWER </dev/tty
      [ "$ANSWER" = UNINSTALL ] || { echo 'Cancelled. Nothing removed.'; return; };;
  *) echo 'Usage: kurumifrp-uninstall [--yes]' >&2; exit 1;;
 esac
 mkdir "$BASE/.maintenance" 2>/dev/null || { echo 'Another install/uninstall is active.' >&2; exit 1; }
 trap 'if [ -d "$BASE" ] && [ ! -L "$BASE" ]; then rm -f -- "$BASE/.uninstalling"; rmdir "$BASE/.maintenance" 2>/dev/null || true; fi' EXIT
 [ ! -L "$BASE/.uninstalling" ] || { echo 'Unsafe maintenance marker.' >&2; exit 1; }
 : > "$BASE/.uninstalling"
 "$BASE/bin/kurumi" --data "$BASE/data" --stop
 if [ -f "$UNIT" ] && command -v systemctl >/dev/null 2>&1 && systemctl --user show-environment >/dev/null 2>&1; then
  systemctl --user disable --now kurumifrp.service
  systemctl --user reset-failed kurumifrp.service 2>/dev/null || true
 fi
 # These are fixed, ownership-checked paths, not a recursive name search.
 rm -f -- "$WANT" "$UNIT" "$LAUNCHER" "$UNINSTALLER"
 if command -v systemctl >/dev/null 2>&1 && systemctl --user show-environment >/dev/null 2>&1; then systemctl --user daemon-reload; fi
 cd -- "$HOME"
 rm -f -- "$BASE/bin/kurumi"
 rm -rf --one-file-system -- "$BASE"
 [ ! -e "$BASE" ] && [ ! -e "$UNIT" ] && [ ! -e "$WANT" ] && [ ! -e "$LAUNCHER" ] && [ ! -e "$UNINSTALLER" ] || { echo 'Uninstall incomplete; inspect the remaining paths.' >&2; exit 1; }
 trap - EXIT
 echo 'Removed KurumiFRP files, keys, cores, logs, launchers and user startup integration.'
 echo 'System journals, browser history, backups and user exports were not altered.'
}
uninstall "$@"
