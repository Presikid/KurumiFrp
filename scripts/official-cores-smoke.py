"""Verify and run every added official Linux frp architecture under QEMU."""
import hashlib
import io
import json
from pathlib import Path
import subprocess
import tarfile
import tempfile
from download_retry import get

TAG = "v0.72.0"
VERSION = TAG.removeprefix("v")
API = "https://api.github.com/repos/fatedier/frp/releases/tags/" + TAG
EMULATORS = {
    "arm": "qemu-arm-static", "arm_hf": "qemu-arm-static",
    "riscv64": "qemu-riscv64-static", "loong64": "qemu-loongarch64-static",
    "mips": "qemu-mips-static", "mipsle": "qemu-mipsel-static",
    "mips64": "qemu-mips64-static", "mips64le": "qemu-mips64el-static",
}

release = json.loads(get(API, 4 << 20))
if release["tag_name"] != TAG or release["draft"] or release["prerelease"]:
    raise RuntimeError("unexpected upstream release")
assets = {asset["name"]: asset for asset in release["assets"]}
with tempfile.TemporaryDirectory(prefix="kurumi-official-cores-") as tmp:
    binary = Path(tmp) / "frp"
    for arch, emulator in EMULATORS.items():
        prefix = f"frp_{VERSION}_linux_{arch}"
        name = prefix + ".tar.gz"
        asset = assets[name]
        url = f"https://github.com/fatedier/frp/releases/download/{TAG}/{name}"
        if asset["browser_download_url"] != url or asset["size"] <= 0 or asset["size"] > 160 << 20:
            raise RuntimeError("unexpected official asset: " + name)
        data = get(url, asset["size"])
        if len(data) != asset["size"] or "sha256:" + hashlib.sha256(data).hexdigest() != asset["digest"]:
            raise RuntimeError("official asset digest mismatch: " + name)
        with tarfile.open(fileobj=io.BytesIO(data), mode="r:gz") as tar:
            for kind in ("frpc", "frps"):
                member = tar.getmember(prefix + "/" + kind)
                if not member.isfile() or member.size > 120 << 20:
                    raise RuntimeError("unsafe official executable: " + name)
                binary.write_bytes(tar.extractfile(member).read())
                binary.chmod(0o755)
                actual = subprocess.check_output([emulator, str(binary), "-v"], text=True, timeout=60).strip()
                if actual != VERSION:
                    raise RuntimeError(f"{arch} {kind}: unexpected version {actual!r}")
        print(f"PASS: official {name} digest and both executables under {emulator}", flush=True)
