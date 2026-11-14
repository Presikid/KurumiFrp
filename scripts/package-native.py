"""Build native Inno Setup Windows EXE and Linux DEB/RPM packages bundling official frpc + frps."""
import argparse
import gzip
import hashlib
import os
import re
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

sys.path.insert(0, str(Path(__file__).resolve().parent))
import bundled_frp  # noqa: E402

ROOT = Path(__file__).resolve().parents[1]
VERSION = (ROOT / 'VERSION').read_text(encoding='utf-8').strip()
if not re.fullmatch(r'[0-9]{1,5}\.[0-9]{1,5}\.[0-9]{1,5}\.[0-9]{1,5}', VERSION):
    raise ValueError(f'invalid four-segment version: {VERSION}')
OUT = ROOT / 'dist-native'


def run(args, **kwargs):
    print('+', ' '.join(map(str, args)), flush=True)
    subprocess.run(list(map(str, args)), cwd=ROOT, check=True, **kwargs)


def build_kurumi(system, arch, dest):
    dest.parent.mkdir(parents=True, exist_ok=True)
    mode = 'windows-installed' if system == 'windows' else 'linux-system'
    goarch = {'armhf': 'arm'}.get(arch, arch)
    flags = f'-s -w -X main.distribution={mode}'
    if system == 'windows':
        flags += ' -H=windowsgui'
    env = dict(os.environ, GOOS=system, GOARCH=goarch, GOARM='7', CGO_ENABLED='0', GOFLAGS='')
    run(['go', 'build', '-trimpath', f'-ldflags={flags}', '-o', dest, './cmd/kurumi'], env=env)


def write_text(path, text, mode=0o644):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text, encoding='utf-8')
    path.chmod(mode)


def windows():
    iscc = os.environ.get('ISCC') or shutil.which('ISCC.exe')
    if not iscc:
        candidate = Path(os.environ.get('ProgramFiles(x86)', r'C:\Program Files (x86)')) / 'Inno Setup 6/ISCC.exe'
        if candidate.is_file():
            iscc = str(candidate)
    if not iscc:
        raise SystemExit('Inno Setup 6 (ISCC.exe) is required on Windows build host.')

    with tempfile.TemporaryDirectory(prefix='kurumi-win-') as temp:
        stage = Path(temp)
        for arch in ('amd64', 'arm64'):
            build_kurumi('windows', arch, stage / arch / 'kurumi.exe')
            bundled_frp.prepare(f'windows_{arch}', stage / arch / 'bundled')

        readme = (ROOT / 'README.md').read_text(encoding='utf-8')
        isl_notice = (ROOT / 'packaging/windows/ChineseSimplified-LICENSE.txt').read_text(encoding='utf-8')
        frp_lic = (stage / 'amd64/bundled/LICENSE').read_text(encoding='utf-8')
        combined_doc = (
            readme
            + '\n\n## 第三方组件许可声明\n\n'
            + '安装包内随附的上游官方 `frpc` / `frps` 二进制来自 `fatedier/frp`（Apache License 2.0）；'
            + '安装向导简体中文语言文件许可如下（上述许可均不授予 KurumiFrp 项目本身任何开源权利）：\n\n'
            + isl_notice
            + '\n\n### Apache License 2.0 (fatedier/frp)\n\n'
            + frp_lic
        )
        write_text(stage / 'README.md', combined_doc)
        run([
            iscc,
            f'/DAppVersion={VERSION}',
            f'/DPayloadDir={stage}',
            f'/DOutputPath={OUT}',
            ROOT / 'packaging/windows/kurumifrp.iss',
        ])


def populate_linux_tree(stage, binary, bundled_dir):
    app_root = stage / 'opt/kurumifrp'
    (app_root / 'bundled').mkdir(parents=True, exist_ok=True)
    shutil.copyfile(binary, app_root / 'kurumifrp')
    (app_root / 'kurumifrp').chmod(0o755)

    for item in bundled_dir.iterdir():
        dest = app_root / 'bundled' / item.name
        shutil.copyfile(item, dest)
        dest.chmod(0o644 if item.name in ('LICENSE', 'manifest.json') else 0o755)

    link = stage / 'usr/bin/kurumifrp'
    link.parent.mkdir(parents=True, exist_ok=True)
    link.symlink_to('../../opt/kurumifrp/kurumifrp')

    write_text(
        stage / 'usr/lib/systemd/system/kurumifrp.service',
        (ROOT / 'packaging/linux/kurumifrp.service').read_text(encoding='utf-8'),
    )

    docs = stage / 'usr/share/doc/kurumifrp'
    write_text(docs / 'README.md', (ROOT / 'README.md').read_text(encoding='utf-8'))
    frp_lic = (bundled_dir / 'LICENSE').read_text(encoding='utf-8')
    write_text(
        docs / 'copyright',
        'Upstream: https://github.com/Presikid/KurumiFrp\n'
        'Copyright (c) Presikid. All Rights Reserved. No open-source license grant is provided for KurumiFrp.\n\n'
        'Bundled official fatedier/frp binaries (frpc, frps) are distributed under Apache License 2.0:\n\n'
        + frp_lic,
    )
    changelog_bytes = f'KurumiFrp ({VERSION}) native Linux package with bundled official frpc/frps.\n'.encode('utf-8')
    (docs / 'changelog.gz').write_bytes(gzip.compress(changelog_bytes, mtime=0))
    (docs / 'changelog.gz').chmod(0o644)

    man_src = (ROOT / 'packaging/linux/kurumifrp.1').read_bytes()
    man_dst = stage / 'usr/share/man/man1/kurumifrp.1.gz'
    man_dst.parent.mkdir(parents=True, exist_ok=True)
    man_dst.write_bytes(gzip.compress(man_src, mtime=0))
    man_dst.chmod(0o644)

    for p in stage.rglob('*'):
        if p.is_dir():
            p.chmod(0o755)


def maintainer_script(action):
    body = (ROOT / 'packaging/linux/lifecycle.sh').read_text(encoding='utf-8').replace('@PACKAGE_VERSION@', VERSION)
    return '#!/bin/sh\nset -e\n' + body + '\n' + action + '\n'


def linux(arch, deb_only=False):
    rpm_arch = {'amd64': 'x86_64', 'arm64': 'aarch64'}.get(arch)
    frp_plat = {
        'amd64': 'linux_amd64',
        'arm64': 'linux_arm64',
        'armhf': 'linux_arm_hf',
        'riscv64': 'linux_riscv64',
    }[arch]

    with tempfile.TemporaryDirectory(prefix=f'kurumi-linux-{arch}-') as temp:
        work = Path(temp)
        binary = work / 'kurumifrp'
        bundled_dir = work / 'bundled'
        build_kurumi('linux', arch, binary)
        bundled_frp.prepare(frp_plat, bundled_dir)

        deb_stage = work / 'debian'
        populate_linux_tree(deb_stage, binary, bundled_dir)
        installed_kb = sum(p.stat().st_size for p in deb_stage.rglob('*') if p.is_file()) // 1024 + 1

        write_text(deb_stage / 'DEBIAN/control', f'''Package: kurumifrp
Version: {VERSION}
Section: net
Priority: optional
Architecture: {arch}
Maintainer: Presikid <67297065+Presikid@users.noreply.github.com>
Installed-Size: {installed_kb}
Depends: ca-certificates, passwd, procps
Recommends: systemd
Homepage: https://github.com/Presikid/KurumiFrp
Description: Tokisaki Kurumi themed WebUI manager with bundled official frpc and frps
 Self-contained FRP server and client WebUI controller bundling official frpc/frps.
''')
        scripts = {
            'postinst': 'case "${1:-}" in configure) setup_service;; esac',
            'prerm': 'case "${1:-}" in remove|upgrade|deconfigure) stop_service;; esac',
            'postrm': 'case "${1:-}" in remove) cleanup_unit;; purge) cleanup_unit; purge_all;; esac',
        }
        for name, act in scripts.items():
            write_text(deb_stage / 'DEBIAN' / name, maintainer_script(act), 0o755)

        md5s = ''.join(
            hashlib.md5(p.read_bytes()).hexdigest() + '  ' + p.relative_to(deb_stage).as_posix() + '\n'
            for top in ('usr', 'opt')
            for p in sorted((deb_stage / top).rglob('*'))
            if p.is_file() and not p.is_symlink()
        )
        write_text(deb_stage / 'DEBIAN/md5sums', md5s)
        run(['dpkg-deb', '--root-owner-group', '--build', deb_stage, OUT / f'kurumifrp_{VERSION}_{arch}.deb'])

        if deb_only or rpm_arch is None:
            return

        rpm_top = work / 'rpm'
        for sub in ('BUILD', 'BUILDROOT', 'RPMS', 'SOURCES', 'SPECS', 'SRPMS'):
            (rpm_top / sub).mkdir(parents=True)
        payload = rpm_top / 'SOURCES/payload'
        populate_linux_tree(payload, binary, bundled_dir)
        common = (ROOT / 'packaging/linux/lifecycle.sh').read_text(encoding='utf-8').replace('@PACKAGE_VERSION@', VERSION).replace('%', '%%')

        spec = f'''Name: kurumifrp
Version: {VERSION}
Release: 1
Summary: Tokisaki Kurumi themed WebUI manager with bundled official frpc and frps
License: LicenseRef-KurumiFRP-Proprietary
URL: https://github.com/Presikid/KurumiFrp
ExclusiveArch: x86_64 aarch64
AutoReqProv: no
Requires: ca-certificates, systemd, shadow-utils, procps-ng
%global debug_package %{{nil}}
%global __os_install_post %{{nil}}
%description
Self-contained FRP server and client WebUI controller bundling official frpc and frps.
%install
mkdir -p %{{buildroot}}
cp -a %{{_sourcedir}}/payload/. %{{buildroot}}/
%posttrans
{common}
setup_service
%preun
{common}
if [ "$1" = 0 ]; then stop_service; fi
%postun
{common}
if [ "$1" = 0 ]; then cleanup_unit; purge_all; fi
%files
/usr/bin/kurumifrp
%dir /opt/kurumifrp
/opt/kurumifrp/kurumifrp
%dir /opt/kurumifrp/bundled
/opt/kurumifrp/bundled/*
/usr/lib/systemd/system/kurumifrp.service
%dir /usr/share/doc/kurumifrp
%doc /usr/share/doc/kurumifrp/*
/usr/share/man/man1/kurumifrp.1.gz
'''
        spec_path = rpm_top / 'SPECS/kurumifrp.spec'
        write_text(spec_path, spec)
        run(['rpmbuild', '-bb', '--define', f'_topdir {rpm_top}', '--target', rpm_arch, spec_path])
        rpms = list((rpm_top / 'RPMS').rglob('*.rpm'))
        if len(rpms) != 1:
            raise SystemExit(f'Expected 1 RPM, found {len(rpms)}')
        shutil.copyfile(rpms[0], OUT / rpms[0].name)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('target', choices=['windows', 'linux'])
    parser.add_argument('--arch', choices=['amd64', 'arm64', 'armhf', 'riscv64', 'all'], default='all')
    parser.add_argument('--deb-only', action='store_true')
    args = parser.parse_args()
    OUT.mkdir(exist_ok=True)
    if args.target == 'windows':
        windows()
    else:
        arches = ('amd64', 'arm64', 'armhf', 'riscv64') if args.arch == 'all' else (args.arch,)
        for a in arches:
            linux(a, args.deb_only or a in ('armhf', 'riscv64'))


if __name__ == '__main__':
    main()
