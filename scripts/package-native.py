"""Build real Inno Setup EXE and dpkg/RPM packages. Build-time Python only."""
import argparse
import gzip
import hashlib
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]
VERSION = (ROOT / 'VERSION').read_text().strip()
OUT = ROOT / 'dist-native'


def run(args, **kwargs):
    print('+', ' '.join(map(str, args)), flush=True)
    subprocess.run(list(map(str, args)), cwd=ROOT, check=True, **kwargs)


def build(system, arch, dest):
    dest.parent.mkdir(parents=True, exist_ok=True)
    mode = 'windows-installed' if system == 'windows' else 'linux-system'
    goarch = {'armhf': 'arm'}.get(arch, arch)
    flags = '-s -w -X main.distribution=' + mode
    if system == 'windows':
        flags += ' -H=windowsgui'
    if system == 'linux' and arch == 'armhf':
        flags += ' -X github.com/Presikid/KurumiFrp/internal/core.armVariant=arm_hf'
    run(['go', 'build', '-trimpath', '-ldflags=' + flags, '-o', dest, './cmd/kurumi'],
        env=dict(os.environ, GOOS=system, GOARCH=goarch, GOARM='7', CGO_ENABLED='0'))


def windows():
    iscc = os.environ.get('ISCC') or shutil.which('ISCC.exe')
    if not iscc:
        p = Path(os.environ.get('ProgramFiles(x86)', r'C:\Program Files (x86)')) / 'Inno Setup 6/ISCC.exe'
        if p.is_file():
            iscc = str(p)
    if not iscc:
        raise SystemExit('Install Inno Setup 6.3+ on the build host, not end-user devices.')
    with tempfile.TemporaryDirectory(prefix='kurumi-setup-') as temp:
        stage = Path(temp)
        for arch in ('amd64', 'arm64'):
            build('windows', arch, stage / arch / 'kurumi.exe')
        # The bundled language translation has its own attribution; include it
        # in the existing installation guide rather than creating a license folder.
        guide = (ROOT / 'docs/INSTALL.md').read_text(encoding='utf-8')
        notice = (ROOT / 'packaging/windows/ChineseSimplified-LICENSE.txt').read_text(encoding='utf-8')
        (stage / 'INSTALL.md').write_text(guide + '\n## 第三方组件声明\n\n简体中文安装界面翻译使用以下第三方许可；这不是 KurumiFRP 的项目许可。\n\n' + notice, encoding='utf-8')
        run([iscc, '/DAppVersion=' + VERSION, '/DPayloadDir=' + str(stage),
             '/DOutputPath=' + str(OUT), ROOT / 'packaging/windows/kurumifrp.iss'])


def write(path, text, mode=0o644):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text, encoding='utf-8')
    path.chmod(mode)


def linux_tree(stage, binary):
    target = stage / 'opt/kurumifrp/kurumifrp'
    target.parent.mkdir(parents=True)
    shutil.copyfile(binary, target)
    target.chmod(0o755)
    link = stage / 'usr/bin/kurumifrp'
    link.parent.mkdir(parents=True, exist_ok=True)
    link.symlink_to('../../opt/kurumifrp/kurumifrp')
    write(stage / 'usr/lib/systemd/system/kurumifrp.service',
          (ROOT / 'packaging/linux/kurumifrp.service').read_text())
    for unit in ('kurumifrp-updater.service', 'kurumifrp-updater.path'):
        write(stage / 'usr/lib/systemd/system' / unit, (ROOT / 'packaging/linux' / unit).read_text())
    docs = stage / 'usr/share/doc/kurumifrp'
    write(docs / 'README.md', (ROOT / 'docs/INSTALL.md').read_text())
    write(docs / 'copyright', 'Upstream: https://github.com/Presikid/KurumiFrp\n'
          'No project license grant has been specified. Packaging does not grant additional rights.\n')
    write(docs / 'changelog', 'KurumiFRP ' + VERSION + ': native installers and package-managed lifecycle.\n')
    for src, dest in [(ROOT / 'packaging/linux/kurumifrp.1', stage / 'usr/share/man/man1/kurumifrp.1.gz'),
                      (docs / 'changelog', docs / 'changelog.gz')]:
        dest.parent.mkdir(parents=True, exist_ok=True)
        dest.write_bytes(gzip.compress(src.read_bytes(), mtime=0))
        dest.chmod(0o644)
    (docs / 'changelog').unlink()
    for p in stage.rglob('*'):
        if p.is_dir():
            p.chmod(0o755)


def maintainer(action):
    return '#!/bin/sh\n' + (ROOT / 'packaging/linux/lifecycle.sh').read_text() + '\n' + action + '\n'


def linux(arch, deb_only=False):
    rpm_arch = {'amd64': 'x86_64', 'arm64': 'aarch64'}.get(arch)
    if not deb_only and rpm_arch is None:
        raise ValueError('RPM is not supported for this architecture')
    with tempfile.TemporaryDirectory(prefix='kurumi-native-') as temp:
        work = Path(temp)
        binary = work / 'kurumifrp'
        build('linux', arch, binary)
        stage = work / 'debian'
        linux_tree(stage, binary)
        size = sum(p.stat().st_size for p in stage.rglob('*') if p.is_file()) // 1024 + 1
        write(stage / 'DEBIAN/control', f'''Package: kurumifrp
Version: {VERSION}
Section: net
Priority: optional
Architecture: {arch}
Maintainer: Presikid <67297065+Presikid@users.noreply.github.com>
Installed-Size: {size}
Depends: ca-certificates, passwd, procps, util-linux, init-system-helpers (>= 1.54)
Recommends: systemd
Homepage: https://github.com/Presikid/KurumiFrp
Description: Local frpc and frps management workbench
 Embedded WebUI with official core downloads, configuration history and
 managed child processes. Runs as a dedicated unprivileged systemd service.
 Management listens only on loopback; no firewall rules are changed.
''')
        scripts = {
            'preinst': 'case "${1:-}" in install|upgrade) pre_upgrade;; esac',
            'postinst': 'case "${1:-}" in configure) setup_state; start_workbench;; esac',
            'prerm': 'case "${1:-}" in remove) lock_updater_for_purge; stop_workbench;; upgrade|deconfigure) stop_workbench;; esac',
            'postrm': 'case "${1:-}" in remove) remove_unit_state;; purge) remove_unit_state; purge_state;; esac',
        }
        for name, action in scripts.items():
            write(stage / 'DEBIAN' / name, maintainer(action), 0o755)
        md5s = ''.join(hashlib.md5(p.read_bytes()).hexdigest() + '  ' + p.relative_to(stage).as_posix() + '\n'
                       for top in ('usr', 'opt') for p in sorted((stage / top).rglob('*')) if p.is_file() and not p.is_symlink())
        write(stage / 'DEBIAN/md5sums', md5s)
        run(['dpkg-deb', '--root-owner-group', '--build', stage, OUT / f'kurumifrp_{VERSION}_{arch}.deb'])
        if deb_only:
            return
        top = work / 'rpm'
        for d in ('BUILD', 'BUILDROOT', 'RPMS', 'SOURCES', 'SPECS', 'SRPMS'):
            (top / d).mkdir(parents=True)
        payload = top / 'SOURCES/payload'
        linux_tree(payload, binary)
        common = (ROOT / 'packaging/linux/lifecycle.sh').read_text().replace('%', '%%')
        spec = f'''Name: kurumifrp
Version: {VERSION}
Release: 1
Summary: Local frpc and frps management workbench
License: LicenseRef-KurumiFRP-Unspecified
URL: https://github.com/Presikid/KurumiFrp
ExclusiveArch: x86_64 aarch64
AutoReqProv: no
Requires: ca-certificates, systemd, shadow-utils, procps-ng, coreutils, util-linux, findutils, gawk
Requires(pre): shadow-utils, coreutils, procps-ng, findutils
Requires(preun): systemd, coreutils
Requires(postun): shadow-utils, procps-ng, coreutils
%global debug_package %{{nil}}
%global __os_install_post %{{nil}}
%description
Native system package with embedded WebUI, verified frp downloads and a
least-privilege systemd service. Management is loopback-only.
%install
mkdir -p %{{buildroot}}
cp -a %{{_sourcedir}}/payload/. %{{buildroot}}/
%pre
{common}
pre_upgrade
%posttrans
{common}
setup_state
start_workbench
%preun
{common}
if [ "$1" = 0 ]; then lock_updater_for_purge; fi
stop_workbench
%postun
{common}
if [ "$1" = 0 ]; then remove_unit_state; purge_state; elif systemd_active; then systemctl daemon-reload; fi
%files
/usr/bin/kurumifrp
%dir /opt/kurumifrp
/opt/kurumifrp/kurumifrp
/usr/lib/systemd/system/kurumifrp.service
/usr/lib/systemd/system/kurumifrp-updater.service
/usr/lib/systemd/system/kurumifrp-updater.path
%dir /usr/share/doc/kurumifrp
%doc /usr/share/doc/kurumifrp/*
/usr/share/man/man1/kurumifrp.1.gz
'''
        path = top / 'SPECS/kurumifrp.spec'
        write(path, spec)
        run(['rpmbuild', '-bb', '--define', '_topdir ' + str(top), '--target', rpm_arch, path])
        # --target determines the binary RPM architecture. A fixed BuildArch
        # re-enters spec parsing and rejects cross-targets on RPM 4.18 hosts.
        packages = list((top / 'RPMS').rglob('*.rpm'))
        if len(packages) != 1:
            raise SystemExit('Expected exactly one target RPM.')
        for p in packages:
            actual = subprocess.check_output(
                ['rpm', '-qp', '--queryformat', '%{ARCH}', str(p)], text=True).strip()
            if actual != rpm_arch:
                raise SystemExit('RPM architecture mismatch: ' + actual)
            shutil.copyfile(p, OUT / p.name)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('target', choices=['windows', 'linux'])
    parser.add_argument('--arch', choices=['amd64', 'arm64', 'armhf', 'riscv64', 'all'], default='all')
    parser.add_argument('--deb-only', action='store_true', help='local DEB build without rpmbuild')
    args = parser.parse_args()
    OUT.mkdir(exist_ok=True)
    if args.target == 'windows':
        windows()
    else:
        for arch in (('amd64', 'arm64', 'armhf', 'riscv64') if args.arch == 'all' else (args.arch,)):
            linux(arch, args.deb_only or arch in ('armhf', 'riscv64'))


if __name__ == '__main__':
    main()
