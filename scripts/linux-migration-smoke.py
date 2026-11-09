"""Upgrade a real released package; ONLY run on an explicitly disposable host."""
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.request

if os.geteuid() != 0 or os.environ.get('KURUMI_DISPOSABLE_PACKAGE_TEST') != '1':
    raise SystemExit('Requires an explicitly disposable root environment.')
package = Path(sys.argv[1]).resolve()
direct = len(sys.argv) > 2 and sys.argv[2] == '--direct'
deb = package.suffix == '.deb'
old = Path('/var/lib/kurumifrp')
old_meta = Path('/var/lib/kurumifrp-package')
root = Path('/opt/kurumifrp')
exe = Path('/usr/bin/kurumifrp')
if old.exists() or old_meta.exists() or root.exists() or exe.exists() or exe.is_symlink() or subprocess.run(['getent', 'passwd', 'kurumifrp'], stdout=subprocess.DEVNULL).returncode == 0:
    raise SystemExit('Existing installation/account found; nothing changed.')
tag = 'v0.7.1.12'
arch = subprocess.check_output(['dpkg', '--print-architecture'], text=True).strip() if deb else 'x86_64'
name = f'kurumifrp_0.7.1.12_{arch}.deb' if deb else 'kurumifrp-0.7.1.12-1.x86_64.rpm'
request = urllib.request.Request('https://api.github.com/repos/Presikid/KurumiFrp/releases/tags/' + tag, headers={'User-Agent': 'KurumiFRP-migration-test', 'Accept': 'application/vnd.github+json'})
if os.environ.get('GH_TOKEN'):
    request.add_header('Authorization', 'Bearer ' + os.environ['GH_TOKEN'])
with urllib.request.urlopen(request, timeout=60) as r:
    release = json.load(r)
asset = next(a for a in release['assets'] if a['name'] == name)
expected_url = f'https://github.com/Presikid/KurumiFrp/releases/download/{tag}/{name}'
if asset['browser_download_url'] != expected_url or asset['size'] <= 0 or asset['size'] > 256 << 20:
    raise RuntimeError('Unexpected released package metadata')

def run(args, **kw):
    return subprocess.run(args, check=True, timeout=180, **kw)

with tempfile.TemporaryDirectory(prefix='kurumi-old-package-') as tmp:
    old_package = Path(tmp)/name
    req = urllib.request.Request(expected_url, headers={'User-Agent': 'KurumiFRP-migration-test'})
    with urllib.request.urlopen(req, timeout=120) as response, old_package.open('wb') as out:
        shutil.copyfileobj(response, out, length=1024*1024)
    data = old_package.read_bytes()
    if len(data) != asset['size'] or 'sha256:' + hashlib.sha256(data).hexdigest() != asset['digest']:
        raise RuntimeError('Released old package digest mismatch')
    install = (lambda p: ['dpkg', '-i', str(p)]) if deb else (lambda p: ['rpm', '-Uvh', '--replacepkgs', str(p)])
    run(install(old_package))
    if not Path('/run/systemd/system').exists():
        manager = subprocess.Popen(['runuser', '-u', 'kurumifrp', '--', str(exe)], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    else:
        manager = None
    run([str(exe), '--check-running'])
    token = (old/'data/access.token').read_text()
    original = json.loads((old/'data/state.json').read_text())
    # Store representative user configuration through the authenticated API.
    addr = (old/'data/listen.addr').read_text().strip()
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    def api(path, body=None):
        request = urllib.request.Request('http://' + addr + '/api/' + path, data=None if body is None else json.dumps(body).encode(), headers={'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'})
        with opener.open(request, timeout=15) as response: return json.load(response)
    state = api('state')
    api('save', {'revision': state['revision'], 'kind': 'instance', 'value': {'name': 'migration-fixture', 'kind': 'frpc', 'host': '127.0.0.1', 'port': 7000, 'token': 'migration-secret-123', 'httpPort': 0, 'httpsPort': 0}})
    state = api('state')
    instance = state['state']['instances'][0]
    instance['name'] = 'migration-fixture-updated'
    api('save', {'revision': state['revision'], 'kind': 'instance', 'value': instance})
    # Root-owned manually installed binaries are supported by legacy builds.
    core = old/'cores/frpc'
    core.write_text('#!/bin/sh\nif [ "$1" = verify ]; then exit 0; fi\nexec sleep 600\n')
    core_uid = int(subprocess.check_output(['id', '-u', 'kurumifrp'])) if direct else 0
    core_gid = int(subprocess.check_output(['id', '-g', 'kurumifrp'])) if direct else 0
    core.chmod(0o755); os.chown(core, core_uid, core_gid)
    core_digest = hashlib.sha256(core.read_bytes()).hexdigest()
    state = api('state')
    api('control', {'revision': state['revision'], 'id': instance['id'], 'action': 'start'})
    pid = api('state')['runtime'][instance['id']]['pid']
    assert pid > 0
    saved = json.loads((old/'data/state.json').read_text())
    assert saved['key'] == original['key']
    assert saved['history'], 'Fixture did not create a history snapshot'
    if not direct:
        if Path('/run/systemd/system').exists(): run(['systemctl', 'stop', 'kurumifrp.service'])
        else:
            run([str(exe), '--stop']); manager.wait(timeout=60)
        assert not (old/'data/manager.lock').exists()
        for _ in range(50):
            try: os.kill(pid, 0)
            except ProcessLookupError: break
            time.sleep(.1)
        else: raise AssertionError('Legacy managed process survived shutdown')
        # Reproduce a crash after the new copy was published but before the receipt.
        extracted = Path(tmp)/'extracted'; extracted.mkdir()
        if deb: run(['dpkg-deb', '-x', str(package), str(extracted)])
        else:
            archive = subprocess.Popen(['rpm2cpio', str(package)], stdout=subprocess.PIPE)
            run(['cpio', '-id', '--quiet'], cwd=extracted, stdin=archive.stdout)
            archive.stdout.close()
            if archive.wait(timeout=60): raise RuntimeError('rpm2cpio failed')
        binary = extracted/'opt/kurumifrp/kurumifrp'
        unit = (extracted/'usr/lib/systemd/system/kurumifrp.service').read_text()
        assert 'Restart=no' in unit
        assert 'ConditionPathExists=!/opt/kurumifrp/meta/migration-intent' in unit
        assert 'ConditionPathExists=!/opt/kurumifrp/meta/legacy-cleanup' in unit
        root.mkdir(mode=0o755); shutil.copy2(binary, root/'kurumifrp')
        (root/'meta').mkdir(mode=0o700)
        uid = subprocess.check_output(['id', '-u', 'kurumifrp'], text=True).strip()
        intent = root/'meta/migration-intent'; intent.write_text(uid+'\n'); intent.chmod(0o600)
        # Exercise a same-device bind mount where the host permits mount(2).
        bound = old/'bind-mount-fixture'
        bound.mkdir(mode=0o700)
        os.chown(bound, int(uid), (old/'data').stat().st_gid)
        mounted = subprocess.run(['mount', '--bind', str(old/'data'), str(bound)], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0
        if mounted:
            try:
                attempt = subprocess.run([str(root/'kurumifrp'), '--migrate-linux-legacy'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
                if attempt.returncode == 0 or (root/'runtime').exists():
                    raise AssertionError('Migration accepted a same-device bind mount')
                assert (old/'data/state.json').exists(), 'Mount rejection changed source data'
            finally:
                run(['umount', str(bound)])
        else:
            print('SKIP: host does not permit creating the bind-mount fixture')
        bound.rmdir()
        run([str(root/'kurumifrp'), '--migrate-linux-legacy'])
        assert (old/'data/state.json').exists() and (root/'runtime/data/state.json').exists()
        target_state = root/'runtime/data/state.json'
        original_target = target_state.read_bytes()
        target_state.write_bytes(b'conflict')
        if subprocess.run([str(root/'kurumifrp'), '--migrate-linux-legacy'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0:
            raise AssertionError('Mismatched interrupted copy accepted')
        assert (old/'data/state.json').exists(), 'Failed retry deleted legacy data'
        target_state.write_bytes(original_target)
        run([str(root/'kurumifrp'), '--migrate-linux-legacy'])
    if direct:
        assert (old/'data/manager.lock').exists() and not root.exists()
        os.kill(pid, 0)  # The package scripts, not the test, must stop this child.
    run(install(package))
    if direct:
        if manager: manager.wait(timeout=60)
        for _ in range(50):
            try: os.kill(pid, 0)
            except ProcessLookupError: break
            time.sleep(.1)
        else: raise AssertionError('Package upgrade left the old managed child running')
    if not Path('/run/systemd/system').exists():
        manager = subprocess.Popen(['runuser', '-u', 'kurumifrp', '--', str(exe)], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    run([str(exe), '--check-running'])
    if Path('/run/systemd/system').exists():
        run(['systemctl', 'is-active', '--quiet', 'kurumifrp-updater.path'])
    assert not (root/'meta/migration-intent').exists() and not (root/'meta/legacy-cleanup').exists()
    new = root/'runtime/data'
    after = json.loads((new/'state.json').read_text())
    assert (new/'access.token').read_text() == token and after['key'] == saved['key']
    assert after['state']['instances'][0]['name'] == 'migration-fixture-updated'
    assert after['state']['instances'][0]['token'] == saved['state']['instances'][0]['token']
    assert after['history'] == saved['history'], 'Snapshots changed during migration'
    copied_core = root/'runtime/cores/frpc'
    assert copied_core.stat().st_uid == core_uid and copied_core.stat().st_gid == core_gid and copied_core.stat().st_mode & 0o777 == 0o755
    assert hashlib.sha256(copied_core.read_bytes()).hexdigest() == core_digest
    assert not old.exists() and not old_meta.exists()
    run(['dpkg', '--purge', 'kurumifrp'] if deb else ['rpm', '-e', 'kurumifrp'])
    if manager: manager.wait(timeout=60)
    assert not root.exists()
    print('PASS: verified old package upgrade, interrupted-copy recovery, configuration and key preservation.')
