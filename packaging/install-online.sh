#!/bin/sh
# Public release bootstrap: temporary archive and extracted payload are removed
# only after verified successful installation. No Python/Go/gh CLI is required.
set -eu
umask 077
VERSION=0.3.0
[ "$(uname -s)" = Linux ] || { echo 'This bootstrap supports Linux.' >&2; exit 1; }
case "$(uname -m)" in x86_64) ARCH=amd64;; aarch64|arm64) ARCH=arm64;; *) echo 'Unsupported CPU.' >&2; exit 1;; esac
for TOOL in curl sha256sum tar mktemp; do command -v "$TOOL" >/dev/null 2>&1 || { echo "Required system tool missing: $TOOL" >&2; exit 1; }; done
WORK=$(mktemp -d "${TMPDIR:-/tmp}/kurumifrp-download.XXXXXXXX")
SUCCESS=0
trap 'if [ "$SUCCESS" = 1 ]; then rm -rf -- "$WORK"; else printf "Installation not confirmed. Download files retained in: %s\n" "$WORK" >&2; fi' EXIT
ASSET="KurumiFRP_${VERSION}_linux_${ARCH}.tar.gz"
BASE="https://github.com/Presikid/KurumiFrp/releases/download/v${VERSION}"
curl --fail --show-error --location --proto '=https' --proto-redir '=https' --retry 3 --connect-timeout 20 --max-time 600 "$BASE/$ASSET" -o "$WORK/$ASSET"
curl --fail --show-error --location --proto '=https' --proto-redir '=https' --retry 3 --connect-timeout 20 --max-time 120 "$BASE/checksums.txt" -o "$WORK/checksums.txt"
(cd "$WORK" && grep "  $ASSET\$" checksums.txt | sha256sum --check --strict)
tar -xzf "$WORK/$ASSET" -C "$WORK" --no-same-owner
KURUMI_INSTALL_ARCHIVE="$WORK/$ASSET" sh "$WORK/KurumiFRP/install.sh"
SUCCESS=1
printf 'Installation verified. Temporary archive and extraction directory are being removed.\n'
