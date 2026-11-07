#!/bin/sh
# Run from an extracted official KurumiFRP release. No Go/Docker/root required.
set -eu
umask 077
SOURCE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
[ "$(uname -s)" = Linux ] || { echo 'This installer requires Linux.' >&2; exit 1; }
case "$(uname -m)" in x86_64) ARCH=amd64;; aarch64|arm64) ARCH=arm64;; *) echo 'Unsupported CPU architecture.' >&2;exit 1;; esac
[ "$(cat "$SOURCE/PLATFORM")" = "linux_$ARCH" ] || { echo 'Download the package matching this machine.' >&2;exit 1; }
(cd "$SOURCE" && sha256sum --check --strict SHA256SUMS)
BASE="$HOME/.local/share/kurumifrp"
for P in "$BASE" "$BASE/data" "$BASE/cores" "$BASE/bin"; do
 [ ! -L "$P" ] || { echo "Refusing symlinked install directory: $P" >&2;exit 1; }
done
mkdir -p "$BASE/bin" "$BASE/data" "$BASE/cores" "$HOME/.local/bin"
chmod 700 "$BASE" "$BASE/bin" "$BASE/data" "$BASE/cores"
BIN="$BASE/bin/kurumi"
if [ -f "$BASE/data/manager.lock" ]; then
 if [ -f "$BIN" ] && cmp -s "$BIN" "$SOURCE/kurumi"; then
  "$BIN" --data "$BASE/data" --frp-dir "$BASE/cores" --listen auto --open
  echo 'Already installed; opened the existing workbench.';exit 0
 fi
 echo 'Exit the running workbench using its WebUI Quit button, then rerun this installer.' >&2
 echo 'Do not remove manager.lock while any managed frp process is running.' >&2;exit 1
fi
[ ! -L "$BIN" ] || { echo 'Unsafe binary path.' >&2;exit 1; }
cp "$SOURCE/kurumi" "$BASE/bin/.kurumi-new"
chmod 700 "$BASE/bin/.kurumi-new"
mv -f "$BASE/bin/.kurumi-new" "$BIN"
cat > "$HOME/.local/bin/kurumifrp" <<'LAUNCH'
#!/bin/sh
BASE="$HOME/.local/share/kurumifrp"
exec "$BASE/bin/kurumi" --data "$BASE/data" --frp-dir "$BASE/cores" --listen auto --open "$@"
LAUNCH
chmod 700 "$HOME/.local/bin/kurumifrp"
# A user service is optional. Do not change system services or require linger.
SERVICE=0
if command -v systemctl >/dev/null 2>&1 && systemctl --user show-environment >/dev/null 2>&1; then
 mkdir -p "$HOME/.config/systemd/user"
 UNIT="$HOME/.config/systemd/user/kurumifrp.service"
 if [ -e "$UNIT" ] && ! grep -q '^# KurumiFRP managed user service' "$UNIT"; then
  echo 'An unrelated user unit exists; refusing to replace it.' >&2;exit 1
 fi
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
if [ "$SERVICE" = 0 ]; then
 nohup "$BIN" --data "$BASE/data" --frp-dir "$BASE/cores" --listen auto --open >>"$BASE/manager.log" 2>&1 </dev/null &
 PID=$!
 sleep 2
 kill -0 "$PID" 2>/dev/null || { echo "Startup failed; see $BASE/manager.log" >&2;exit 1; }
else
 sleep 2
 if [ -n "${DISPLAY:-}${WAYLAND_DISPLAY:-}" ]; then "$HOME/.local/bin/kurumifrp" >/dev/null 2>&1 || true;fi
fi
printf '\nInstalled. Open http://%s\nLauncher: %s\n' "$(cat "$BASE/data/listen.addr")" "$HOME/.local/bin/kurumifrp"
printf 'Remote server: use SSH port forwarding; no public management port was opened.\n'
printf 'Manual login token: cat "%s/data/access.token"\n' "$BASE"
printf 'Next: Core management -> choose frp -> Download -> Activate.\n'
