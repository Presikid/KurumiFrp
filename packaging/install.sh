#!/bin/sh
# Install an extracted, checksum-verified release for the current user.
set -eu
umask 077
SOURCE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
[ "$(uname -s)" = Linux ] || { echo 'Linux required.' >&2; exit 1; }
case "$(uname -m)" in x86_64) ARCH=amd64;; aarch64|arm64) ARCH=arm64;; *) echo 'Unsupported CPU.' >&2; exit 1;; esac
[ "$(cat "$SOURCE/PLATFORM")" = "linux_$ARCH" ] || { echo 'Wrong package platform.' >&2; exit 1; }
(cd "$SOURCE" && sha256sum --check --strict SHA256SUMS)
[ -f "$SOURCE/PACKAGE-MANIFEST.json" ] || { echo 'Package manifest missing.' >&2; exit 1; }
BASE="$HOME/.local/share/kurumifrp"
for P in "$HOME/.local" "$HOME/.local/share" "$BASE" "$BASE/data" "$BASE/cores" "$BASE/bin" "$BASE/bin/kurumi" "$BASE/install.receipt" "$BASE/integration-hashes" "$BASE/.uninstalling" "$HOME/.local/bin" "$HOME/.config" "$HOME/.config/systemd" "$HOME/.config/systemd/user"; do
 [ ! -L "$P" ] || { echo "Refusing linked install path: $P" >&2; exit 1; }
done
if [ -d "$BASE" ]; then
 if [ -f "$BASE/install.receipt" ]; then
  [ "$(sed -n '1p' "$BASE/install.receipt")" = 'KurumiFRP user installation v1' ] && [ "$(sed -n '2p' "$BASE/install.receipt")" = "$BASE" ] || { echo 'Invalid installation receipt.' >&2; exit 1; }
 elif [ -n "$(ls -A "$BASE")" ]; then
  [ -f "$BASE/bin/kurumi" ] && [ -f "$BASE/data/access.token" ] && [ "$("$BASE/bin/kurumi" --version)" = 0.2.0 ] || { echo 'Unrecognized existing directory; retained.' >&2; exit 1; }
 fi
fi
mkdir -p "$BASE/bin" "$BASE/data" "$BASE/cores" "$HOME/.local/bin"
chmod 700 "$BASE" "$BASE/bin" "$BASE/data" "$BASE/cores"
mkdir "$BASE/.maintenance" 2>/dev/null || { echo 'Another install/uninstall is active; no changes made.' >&2; exit 1; }
printf 'KurumiFRP user installation v1\n%s\n' "$BASE" > "$BASE/install.receipt"
trap 'rmdir "$BASE/.maintenance" 2>/dev/null || true' EXIT
BIN="$BASE/bin/kurumi"
LAUNCHER="$HOME/.local/bin/kurumifrp"
UNINSTALLER="$HOME/.local/bin/kurumifrp-uninstall"
UNIT="$HOME/.config/systemd/user/kurumifrp.service"
for P in "$BIN" "$BASE/uninstall.sh" "$BASE/install.receipt" "$BASE/integration-hashes" "$LAUNCHER" "$UNINSTALLER" "$UNIT"; do
 [ ! -L "$P" ] || { echo "Unsafe existing path: $P" >&2; exit 1; }
done
if [ -e "$LAUNCHER" ] && ! grep -Fqx 'BASE="$HOME/.local/share/kurumifrp"' "$LAUNCHER"; then echo 'Unrelated launcher exists.' >&2; exit 1; fi
if [ -e "$UNINSTALLER" ] && ! grep -Fqx '# KurumiFRP managed uninstall launcher v1' "$UNINSTALLER"; then echo 'Unrelated uninstall launcher exists.' >&2; exit 1; fi
if [ -e "$UNIT" ] && ! grep -Fqx '# KurumiFRP managed user service' "$UNIT"; then echo 'Unrelated systemd unit exists.' >&2; exit 1; fi
if [ -f "$BASE/data/manager.lock" ] && ! cmp -s "$BIN" "$SOURCE/kurumi"; then
 echo 'Exit the running workbench before upgrading. Installer files retained.' >&2; exit 1
fi
if ! cmp -s "$BIN" "$SOURCE/kurumi"; then
 cp "$SOURCE/kurumi" "$BASE/bin/.kurumi-new"
 chmod 700 "$BASE/bin/.kurumi-new"
 mv -f "$BASE/bin/.kurumi-new" "$BIN"
fi
cp "$SOURCE/uninstall.sh" "$BASE/uninstall.sh"
chmod 700 "$BASE/uninstall.sh"
cat > "$LAUNCHER" <<'LAUNCH'
#!/bin/sh
# KurumiFRP managed launcher v1
BASE="$HOME/.local/share/kurumifrp"
exec "$BASE/bin/kurumi" --data "$BASE/data" --frp-dir "$BASE/cores" --listen auto --open "$@"
LAUNCH
cat > "$UNINSTALLER" <<'UNINSTALL'
#!/bin/sh
# KurumiFRP managed uninstall launcher v1
exec sh "$HOME/.local/share/kurumifrp/uninstall.sh" "$@"
UNINSTALL
chmod 700 "$LAUNCHER" "$UNINSTALLER"
SERVICE=0
if command -v systemctl >/dev/null 2>&1 && systemctl --user show-environment >/dev/null 2>&1; then
 mkdir -p "$(dirname "$UNIT")"
 cat > "$UNIT" <<'UNIT'
# KurumiFRP managed user service
[Unit]
Description=KurumiFRP local workbench
After=network.target
[Service]
ExecStart="%h/.local/share/kurumifrp/bin/kurumi" --data "%h/.local/share/kurumifrp/data" --frp-dir "%h/.local/share/kurumifrp/cores" --listen auto
Restart=no
TimeoutStopSec=60
UMask=0077
[Install]
WantedBy=default.target
UNIT
 systemctl --user daemon-reload
 systemctl --user enable --now kurumifrp.service
 SERVICE=1
fi
mkdir -p "$BASE/integration-hashes"
record() { [ ! -L "$BASE/integration-hashes/$1" ] || { echo "Unsafe integration receipt." >&2; exit 1; }; SUM=$(sha256sum < "$2"); printf '%s\n' "${SUM%% *}" > "$BASE/integration-hashes/$1"; }
record launcher "$LAUNCHER"
record uninstall-launcher "$UNINSTALLER"
if [ -f "$UNIT" ]; then record unit "$UNIT"; fi
printf 'KurumiFRP user installation v1\n%s\n' "$BASE" > "$BASE/install.receipt"
if [ "$SERVICE" = 0 ] && [ ! -f "$BASE/data/manager.lock" ]; then
 nohup "$BIN" --data "$BASE/data" --frp-dir "$BASE/cores" --listen auto >>"$BASE/manager.log" 2>&1 </dev/null &
fi
"$BIN" --data "$BASE/data" --check-running
if [ -n "${DISPLAY:-}${WAYLAND_DISPLAY:-}" ]; then "$LAUNCHER" >/dev/null 2>&1 || true; fi
printf '\nInstalled. Open http://%s\nUninstall: %s\n' "$(cat "$BASE/data/listen.addr")" "$UNINSTALLER"
printf 'Uninstall removes all application configuration, keys, cores and logs.\n'
printf 'Manual login token: cat "%s/data/access.token"\n' "$BASE"
# This is an installed executable, not the copy being removed.
"$BIN" --cleanup-package "$SOURCE" --cleanup-archive "${KURUMI_INSTALL_ARCHIVE:-}"
printf 'Verified extracted installer payload removed. No Downloads folder was scanned.\n'
