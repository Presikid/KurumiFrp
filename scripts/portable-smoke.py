"""Start cross-compiled Linux workbenches under QEMU and check core selection."""
import json
import socket
import subprocess
import tarfile
import tempfile
import time
import urllib.error
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
VERSION = (ROOT / "VERSION").read_text().strip()
EMULATORS = {
    "arm": "qemu-arm-static", "arm_hf": "qemu-arm-static",
    "riscv64": "qemu-riscv64-static", "loong64": "qemu-loongarch64-static",
    "mips": "qemu-mips-static", "mipsle": "qemu-mipsel-static",
    "mips64": "qemu-mips64-static", "mips64le": "qemu-mips64el-static",
}

def exercise(binary, arch, emulator, root, label):
    actual = subprocess.check_output([emulator, str(binary), "--version"], text=True, timeout=30).strip()
    if actual != VERSION:
        raise RuntimeError(f"{label}: unexpected version {actual!r}")
    data = root / ("data-" + label)
    cores = root / ("cores-" + label)
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    process = subprocess.Popen(
        [emulator, str(binary), "--data", str(data), "--frp-dir", str(cores),
         "--listen", f"127.0.0.1:{port}"],
        stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True)
    try:
        deadline = time.monotonic() + 30
        while True:
            if process.poll() is not None:
                raise RuntimeError(f"{label}: workbench exited: {process.stderr.read()}")
            try:
                token = (data / "access.token").read_text().strip()
                req = urllib.request.Request(f"http://127.0.0.1:{port}/api/core",
                                             headers={"Authorization": "Bearer " + token})
                with urllib.request.urlopen(req, timeout=2) as response:
                    status = json.load(response)
                break
            except (FileNotFoundError, OSError, urllib.error.URLError):
                if time.monotonic() >= deadline:
                    raise RuntimeError(f"{label}: workbench did not become ready")
                time.sleep(0.2)
        if status["host"] != "linux_" + arch or not status["supported"]:
            raise RuntimeError(f"{label}: wrong core platform: {status}")
        print(f"PASS: {label} starts and selects its official core platform", flush=True)
    finally:
        process.terminate()
        try:
            process.wait(timeout=10)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=10)
        process.stderr.close()

with tempfile.TemporaryDirectory(prefix="kurumi-emulated-") as tmp:
    root = Path(tmp)
    binary = root / "kurumifrp"
    for arch, emulator in EMULATORS.items():
        package = ROOT / "dist-native" / f"KurumiFRP-{VERSION}-linux-{arch}.tar.gz"
        with tarfile.open(package, "r:gz") as tar:
            assert sorted(tar.getnames()) == ["README.txt", "kurumifrp"]
            binary.write_bytes(tar.extractfile("kurumifrp").read())
        binary.chmod(0o755)
        exercise(binary, arch, emulator, root, "portable-" + arch)
    for deb_arch, core_arch in (("armhf", "arm_hf"), ("riscv64", "riscv64")):
        package = ROOT / "dist-native" / f"kurumifrp_{VERSION}_{deb_arch}.deb"
        actual_arch = subprocess.check_output(["dpkg-deb", "-f", str(package), "Architecture"], text=True).strip()
        if actual_arch != deb_arch:
            raise RuntimeError(f"unexpected Debian architecture: {actual_arch}")
        dest = root / ("extract-" + deb_arch)
        subprocess.run(["dpkg-deb", "-x", str(package), str(dest)], check=True)
        exercise(dest / "usr/bin/kurumifrp", core_arch, EMULATORS[core_arch], root, "deb-" + deb_arch)
