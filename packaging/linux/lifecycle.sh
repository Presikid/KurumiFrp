# Lifecycle helper functions embedded into DEB/RPM maintainer scripts.
ROOT="/opt/kurumifrp"
DATA_DIR="/opt/kurumifrp/data"

systemd_running() {
  [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1
}

setup_service() {
  if ! getent group kurumifrp >/dev/null 2>&1; then
    groupadd --system kurumifrp || true
  fi
  if ! getent passwd kurumifrp >/dev/null 2>&1; then
    useradd --system --gid kurumifrp --home-dir "$ROOT" --shell /usr/sbin/nologin kurumifrp || true
  fi
  mkdir -p "$DATA_DIR/configs" "$DATA_DIR/runtime"
  chown -R kurumifrp:kurumifrp "$DATA_DIR"
  chmod 0700 "$DATA_DIR"

  if systemd_running; then
    systemctl daemon-reload || true
    systemctl enable kurumifrp.service || true
    systemctl restart kurumifrp.service || true
  fi
}

stop_service() {
  if systemd_running; then
    systemctl stop kurumifrp.service || true
  elif [ -x "$ROOT/kurumifrp" ]; then
    "$ROOT/kurumifrp" -stop -data "$DATA_DIR" || true
  fi
}

cleanup_unit() {
  if systemd_running; then
    systemctl disable kurumifrp.service || true
    systemctl daemon-reload || true
  fi
}

purge_all() {
  rm -rf "$DATA_DIR" "$ROOT/runtime" "$ROOT/native-install.json"
  rmdir "$ROOT" 2>/dev/null || true
  if getent passwd kurumifrp >/dev/null 2>&1; then
    userdel kurumifrp || true
  fi
  if getent group kurumifrp >/dev/null 2>&1; then
    groupdel kurumifrp || true
  fi
}
