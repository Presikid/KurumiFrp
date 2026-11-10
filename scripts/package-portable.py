"""Build portable Linux archives with the in-process frp runtime."""
import io
import os
import re
from pathlib import Path
import subprocess
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parents[1]
VERSION = (ROOT / "VERSION").read_text().strip()
if not re.fullmatch(r'[0-9]{1,5}\.[0-9]{1,5}\.[0-9]{1,5}\.[0-9]{1,5}', VERSION):
    raise ValueError('invalid package version')
OUT = ROOT / "dist-native"
# Go architecture, ARM instruction baseline, MIPS floating point.
TARGETS = {
    "arm": ("arm", "5", "softfloat"),
    "arm_hf": ("arm", "7", "softfloat"),
    "riscv64": ("riscv64", "7", "softfloat"),
    "loong64": ("loong64", "7", "softfloat"),
    "mips": ("mips", "7", "softfloat"),
    "mipsle": ("mipsle", "7", "softfloat"),
    "mips64": ("mips64", "7", "softfloat"),
    "mips64le": ("mips64le", "7", "softfloat"),
}
README = """KurumiFRP portable Linux edition

Extract this archive, then run ./kurumifrp from a writable directory.
The local WebUI listens on 127.0.0.1:9000. Use --show-token to print
the local access token. Choose an explicit --data path when you need a
persistent location. This portable build does not register a
system service or update itself. To upgrade, stop the old workbench, then
extract the complete new archive into a new empty directory. Start the new
executable with --data pointing to the original data directory (use an absolute
path). If you used the default, it was ./data relative to the old process
working directory. Do not extract over existing files. Back up your data before
upgrading. Existing external core files are not removed or used by this build.
For remote access use an SSH tunnel; do not expose the management port.
"""

def add_bytes(tar, name, data, mode):
    info = tarfile.TarInfo(name)
    info.size = len(data)
    info.mode = mode
    info.mtime = 0
    tar.addfile(info, io.BytesIO(data))

def main():
    OUT.mkdir(exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="kurumi-portable-") as tmp:
        binary = Path(tmp) / "kurumifrp"
        for name, (goarch, goarm, gomips) in TARGETS.items():
            flags = "-s -w"
            env = dict(os.environ, GOOS="linux", GOARCH=goarch, CGO_ENABLED="0",
                       GOARM=goarm, GOMIPS=gomips, GOMIPS64=gomips, GOFLAGS="")
            subprocess.run(["go", "build", "-trimpath", "-ldflags=" + flags,
                            "-o", str(binary), "./cmd/kurumi"], cwd=ROOT, env=env, check=True)
            path = OUT / f"KurumiFRP-{VERSION}-linux-{name}.tar.gz"
            with tarfile.open(path, "w:gz") as tar:
                add_bytes(tar, "kurumifrp", binary.read_bytes(), 0o755)
                add_bytes(tar, "README.txt", README.encode("utf-8"), 0o644)
                add_bytes(tar, "LICENSE-frp", (ROOT / "internal/embedded/LICENSE-frp").read_bytes(), 0o644)
            print(f"Built {path.name}", flush=True)

if __name__ == "__main__":
    main()
