"""Destructive lifecycle test in an isolated user installation directory.
Windows integration entries are real; run Windows tests only on disposable CI.
The frp child is a synthetic Go process, not a claimed protocol test.
"""
import ctypes
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.request
import zipfile

ROOT = Path(__file__).resolve().parents[1]
VERSION = (ROOT / 'VERSION').read_text().strip()
package = Path(sys.argv[1]).resolve()
if os.name == 'nt' and os.environ.get('CI') != 'true':
    raise SystemExit('Windows install/uninstall smoke is restricted to disposable CI.')

def alive(pid):
    if os.name == 'nt':
        kernel = ctypes.windll.kernel32
        kernel.OpenProcess.restype = ctypes.c_void_p
        handle = kernel.OpenProcess(0x100000, False, pid)
        if not handle:
            return False
        try:
            return kernel.WaitForSingleObject(ctypes.c_void_p(handle), 0) == 258
        finally:
            kernel.CloseHandle(ctypes.c_void_p(handle))
    try:
        os.kill(pid, 0)
        return True
    except ProcessLookupError:
        return False

def batch(script, *args):
    # Explicit cmd quoting; paths may contain spaces. Arguments here are fixed test flags.
    return 'cmd.exe /d /s /c ""' + str(script) + '" ' + ' '.join(args) + '"'

def ps(script, *args):
    return ['powershell.exe', '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', str(script), *args]

with tempfile.TemporaryDirectory(prefix='kurumi-lifecycle-') as temp:
    root = Path(temp)
    env = dict(os.environ)
    if os.name == 'nt':
        env['LOCALAPPDATA'] = str(root / 'local')
        Path(env['LOCALAPPDATA']).mkdir()
        target = Path(env['LOCALAPPDATA']) / 'KurumiFRP'
        exe = target / 'kurumi.exe'
        uninstall = ps(target / 'uninstall.ps1', '-Yes')
    else:
        env['HOME'] = str(root / 'home')
        Path(env['HOME']).mkdir()
        stubs = root / 'stubs'
        stubs.mkdir()
        for name, code in [('systemctl', 'exit 1'), ('xdg-open', 'exit 0')]:
            p = stubs / name
            p.write_text('#!/bin/sh\n' + code + '\n')
            p.chmod(0o755)
        env['PATH'] = str(stubs) + os.pathsep + env['PATH']
        target = Path(env['HOME']) / '.local/share/kurumifrp'
        exe = target / 'bin/kurumi'
        uninstall = ['sh', str(target / 'uninstall.sh'), '--yes']
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    def api(path, body=None):
        addr = (target / 'data/listen.addr').read_text().strip()
        token = (target / 'data/access.token').read_text()
        req = urllib.request.Request('http://' + addr + '/api/' + path,
            data=json.dumps(body).encode() if body is not None else None,
            headers={'Content-Type': 'application/json', 'Authorization': 'Bearer ' + token})
        with opener.open(req, timeout=8) as r:
            return json.load(r)
    hashes = None
    receipt = None
    try:
        for iteration in range(2):
            payload = root / ('payload-' + str(iteration))
            payload.mkdir()
            archive = payload / package.name
            shutil.copyfile(package, archive)
            if archive.suffix == '.zip':
                with zipfile.ZipFile(archive) as z:
                    z.extractall(payload)
            else:
                with tarfile.open(archive) as t:
                    t.extractall(payload, filter='data')
            source = payload / 'KurumiFRP'
            manifest = json.loads((source / 'PACKAGE-MANIFEST.json').read_text())
            if iteration == 0:
                (source / 'my-document.txt').write_text('must survive')
            install = ps(source / 'install.ps1', '-NoOpen', '-NoAutostart') if os.name == 'nt' else ['sh', str(source / 'install.sh')]
            if os.name == 'nt' and iteration == 1:
                install = batch(source / 'Install.cmd', '-NoOpen', '-NoAutostart')
            subprocess.run(install, env=env, cwd=source, check=True, timeout=90)
            assert api('state')['version'] == VERSION
            assert not archive.exists(), 'verified original installer archive remains'
            for name in list(manifest['files']) + ['PACKAGE-MANIFEST.json']:
                assert not (source / name).exists(), 'installer payload remains: ' + name
            if iteration == 0:
                assert (source / 'my-document.txt').read_text() == 'must survive'
            else:
                assert not source.exists(), 'empty extraction directory remains'
            current = [hashlib.sha256((target / 'data' / n).read_bytes()).hexdigest() for n in ('access.token', 'master.key')]
            if hashes is not None:
                assert current == hashes, 'reinstall rotated secrets'
            hashes = current
        print('PASS: installed startup, repeated install, credential preservation, payload+archive cleanup, unrelated source files preserved.')

        # A failed shutdown must not delete the application or its data.
        token_path = target / 'data/access.token'
        original_token = token_path.read_bytes()
        token_path.write_text('b' * 64)
        try:
            failed = subprocess.run(uninstall, env=env, timeout=30)
            assert failed.returncode != 0, 'uninstall ignored failed shutdown'
            assert exe.exists() and (target / 'data/master.key').exists()
            assert not (target / '.uninstalling').exists()
        finally:
            token_path.write_bytes(original_token)
        assert api('state')['version'] == VERSION
        print('PASS: shutdown authentication failure retains installation and data.')

        # Demonstrate that a live, owned subprocess is stopped by uninstall.
        fixture = root / 'fake-frps.go'
        fixture.write_text('package main\nimport("os";"time")\nfunc main(){if len(os.Args)>1 && os.Args[1]=="verify"{return};for{time.Sleep(time.Second)}}\n')
        child_exe = target / 'cores' / ('frps.exe' if os.name == 'nt' else 'frps')
        build_env = dict(env, CGO_ENABLED='0')
        build_env['GOCACHE'] = subprocess.check_output(['go', 'env', 'GOCACHE'], text=True).strip()
        subprocess.run(['go', 'build', '-o', str(child_exe), str(fixture)], env=build_env, check=True, timeout=90)
        state = api('state')
        api('save', {'revision': state['revision'], 'kind': 'instance', 'value': {'name': 'uninstall-child', 'kind': 'frps', 'host': '127.0.0.1', 'port': 17070, 'token': 'synthetic-test-token', 'httpPort': 0, 'httpsPort': 0}})
        state = api('state')
        iid = state['state']['instances'][0]['id']
        api('control', {'revision': state['revision'], 'id': iid, 'action': 'start'})
        child_pid = api('state')['runtime'][iid]['pid']
        manager_pid = int((target / 'data/manager.lock').read_text().strip())
        assert alive(child_pid) and alive(manager_pid)
        outside = root / 'unrelated'
        outside.mkdir()
        (outside / 'keep.txt').write_text('user data')
        if os.name == 'nt':
            subprocess.run(['cmd.exe', '/c', 'mklink', '/J', str(target / 'cores/linked'), str(outside)], check=True)
            receipt = json.loads((target / 'install-receipt.json').read_text(encoding='utf-8-sig'))
        else:
            (target / 'cores/linked').symlink_to(outside, target_is_directory=True)
        final_uninstall = batch(target / 'Uninstall.cmd', '-Yes') if os.name == 'nt' else uninstall
        subprocess.run(final_uninstall, env=env, cwd=target, check=True, timeout=90)
        for _ in range(100):
            if not alive(manager_pid) and not alive(child_pid):
                break
            time.sleep(.1)
        assert not alive(manager_pid) and not alive(child_pid), 'owned process survived removal'
        assert not target.exists(), 'application directory remains'
        assert (outside / 'keep.txt').read_text() == 'user data', 'external symlink/junction target damaged'
        if os.name == 'nt':
            for link in receipt['links']:
                assert not Path(link['path']).exists(), 'shortcut remains'
            subprocess.run(['powershell.exe', '-NoProfile', '-Command', "if (Test-Path 'HKCU:\\Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\KurumiFRP') { exit 1 }"], env=env, check=True)
        else:
            for name in ['.local/bin/kurumifrp', '.local/bin/kurumifrp-uninstall', '.config/systemd/user/kurumifrp.service', '.config/systemd/user/default.target.wants/kurumifrp.service']:
                p = Path(env['HOME']) / name
                assert not p.exists() and not p.is_symlink(), 'integration remains: ' + name
        print('PASS: full removal stops manager+owned child, removes data/cores/integrations, preserves outside symlink/junction target.')

        if os.name != 'nt':
            # Transport fixture only: exercise the published one-command
            # bootstrap without depending on a release that is not published yet.
            fixtures = root / 'downloads'
            fixtures.mkdir()
            shutil.copyfile(package, fixtures / package.name)
            (fixtures / 'checksums.txt').write_text(hashlib.sha256(package.read_bytes()).hexdigest() + '  ' + package.name + '\n')
            mock = stubs / 'curl'
            mock.write_text('''#!/bin/sh
set -eu
out=''; url=''
while [ "$#" -gt 0 ]; do
 case "$1" in -o) out=$2; shift 2;; https://github.com/Presikid/KurumiFrp/releases/download/*) url=$1; shift;; *) shift;; esac
done
[ -n "$out" ] && [ -n "$url" ]
cp -- "$TEST_FIXTURES/${url##*/}" "$out"
''')
            mock.chmod(0o755)
            tmp = root / 'download-temp'
            tmp.mkdir()
            online_env = dict(env, TMPDIR=str(tmp), TEST_FIXTURES=str(fixtures))
            subprocess.run(['sh', str(ROOT / 'packaging/install-online.sh')], env=online_env, check=True, timeout=90)
            assert api('state')['version'] == VERSION
            assert not list(tmp.iterdir()), 'bootstrap temporary archive or extraction remains'
            subprocess.run(uninstall, env=env, check=True, timeout=90)
            assert not target.exists()
            print('PASS: public bootstrap (fixture transport) cleans its entire temporary download directory after success.')
    finally:
        try:
            api('quit', {})
        except (OSError, ValueError):
            pass
