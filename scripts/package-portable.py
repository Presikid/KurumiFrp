"""Build portable multi-arch Linux tar.gz archives bundling official frpc + frps."""
import io
import os
import re
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile

sys.path.insert(0, str(Path(__file__).resolve().parent))
import bundled_frp  # noqa: E402

ROOT = Path(__file__).resolve().parents[1]
VERSION = (ROOT / 'VERSION').read_text(encoding='utf-8').strip()
if not re.fullmatch(r'[0-9]{1,5}\.[0-9]{1,5}\.[0-9]{1,5}\.[0-9]{1,5}', VERSION):
    raise ValueError(f'invalid version: {VERSION}')
OUT = ROOT / 'dist-native'

TARGETS = {
    'arm': ('arm', '5', 'softfloat', 'linux_arm'),
    'arm_hf': ('arm', '7', 'softfloat', 'linux_arm_hf'),
    'riscv64': ('riscv64', '7', 'softfloat', 'linux_riscv64'),
    'loong64': ('loong64', '7', 'softfloat', 'linux_loong64'),
    'mips': ('mips', '7', 'softfloat', 'linux_mips'),
    'mipsle': ('mipsle', '7', 'softfloat', 'linux_mipsle'),
    'mips64': ('mips64', '7', 'softfloat', 'linux_mips64'),
    'mips64le': ('mips64le', '7', 'softfloat', 'linux_mips64le'),
}


def add_bytes(tar, name, data, mode):
    info = tarfile.TarInfo(name)
    info.size = len(data)
    info.mode = mode
    info.mtime = 0
    tar.addfile(info, io.BytesIO(data))


def main():
    OUT.mkdir(exist_ok=True)
    readme_bytes = (ROOT / 'README.md').read_bytes()
    with tempfile.TemporaryDirectory(prefix='kurumi-portable-') as tmp:
        tmp_path = Path(tmp)
        binary = tmp_path / 'kurumifrp'
        for name, (goarch, goarm, gomips, frp_plat) in TARGETS.items():
            env = dict(
                os.environ,
                GOOS='linux',
                GOARCH=goarch,
                CGO_ENABLED='0',
                GOARM=goarm,
                GOMIPS=gomips,
                GOMIPS64=gomips,
                GOFLAGS='',
            )
            subprocess.run(
                ['go', 'build', '-trimpath', '-ldflags=-s -w', '-o', str(binary), './cmd/kurumi'],
                cwd=ROOT,
                env=env,
                check=True,
            )
            bdir = tmp_path / f'bundled-{name}'
            bundled_frp.prepare(frp_plat, bdir)

            out_archive = OUT / f'KurumiFRP-{VERSION}-linux-{name}.tar.gz'
            with tarfile.open(out_archive, 'w:gz') as tar:
                add_bytes(tar, 'kurumifrp', binary.read_bytes(), 0o755)
                add_bytes(tar, 'README.md', readme_bytes, 0o644)
                for item in sorted(bdir.iterdir()):
                    mode = 0o644 if item.name in ('LICENSE', 'manifest.json') else 0o755
                    add_bytes(tar, f'bundled/{item.name}', item.read_bytes(), mode)
            print(f'Built {out_archive.name}', flush=True)


if __name__ == '__main__':
    main()
