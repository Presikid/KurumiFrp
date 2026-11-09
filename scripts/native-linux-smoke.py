"""Native package lifecycle; run as root ONLY on disposable CI/container hosts."""
import hashlib
import fcntl
import json
import os
from pathlib import Path
import shutil
import stat
import subprocess
import sys
import tempfile
import time
import urllib.request

expected_version = (Path(__file__).resolve().parents[1] / 'VERSION').read_text(encoding='utf-8').strip()

if os.geteuid() != 0 or os.environ.get('KURUMI_DISPOSABLE_PACKAGE_TEST') != '1':
    raise SystemExit('This test requires an explicitly disposable root environment.')
package = Path(sys.argv[1]).resolve()
is_deb = package.suffix == '.deb'
root = Path('/opt/kurumifrp')
base = root/'runtime'
meta = root/'meta'
exe = Path('/usr/bin/kurumifrp')
if root.exists() or Path('/var/lib/kurumifrp').exists() or Path('/var/lib/kurumifrp-package').exists() or exe.exists() or subprocess.run(['getent','passwd','kurumifrp'],stdout=subprocess.DEVNULL).returncode == 0:
    raise SystemExit('A pre-existing installation/account was found. Nothing changed.')
opt = Path('/opt')
opt_stat = opt.lstat()
if not stat.S_ISDIR(opt_stat.st_mode) or opt_stat.st_uid != 0:
    raise SystemExit('The disposable host has an untrusted /opt. Nothing changed.')
opt_mode = stat.S_IMODE(opt_stat.st_mode)
install = ['dpkg','-i',str(package)] if is_deb else ['rpm','-Uvh','--replacepkgs',str(package)]
remove = ['dpkg','--remove','kurumifrp'] if is_deb else ['rpm','-e','kurumifrp']
purge = ['dpkg','--purge','kurumifrp'] if is_deb else remove
opener=urllib.request.build_opener(urllib.request.ProxyHandler({}))
processes=[]


def run(args):
    subprocess.run(args, check=True, timeout=120)


def ensure_running():
    if not Path('/run/systemd/system').exists():
        processes.append(subprocess.Popen(['runuser','-u','kurumifrp','--',str(exe)],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL))
    run([str(exe),'--check-running'])


def api(path, value=None):
    address=(base/'data/listen.addr').read_text().strip()
    token=(base/'data/access.token').read_text()
    req=urllib.request.Request('http://'+address+'/api/'+path,
        data=None if value is None else json.dumps(value).encode(),
        headers={'Authorization':'Bearer '+token,'Content-Type':'application/json'})
    with opener.open(req,timeout=10) as r:
        return json.load(r)


def keys():
    return [(base/'data/access.token').read_text(), json.loads((base/'data/state.json').read_text())['key']]


try:
    # GitHub's disposable runners may have a world-writable /opt; the package
    # correctly refuses it. Restore the runner's original mode on every exit.
    opt.chmod(0o755)
    run(install)
    ensure_running()
    before=keys()
    assert api('state')['version']==expected_version
    run(['dpkg','--verify','kurumifrp'] if is_deb else ['rpm','-V','kurumifrp'])
    # The real updater holds this lock while the package manager reads its input.
    scratch=root/'private/update-in-progress'
    scratch.mkdir(mode=0o700)
    staged_package=scratch/package.name
    shutil.copyfile(package, staged_package)
    with (root/'private/.update.lock').open('a') as lock:
        os.chmod(lock.name, 0o600)
        fcntl.flock(lock, fcntl.LOCK_EX)
        run(['dpkg','-i',str(staged_package)] if is_deb else ['rpm','-Uvh','--replacepkgs',str(staged_package)])
        assert staged_package.exists(), 'upgrade deleted the updater input'
    shutil.rmtree(scratch)
    ensure_running()
    assert before==keys(), 'upgrade rotated credentials'
    if is_deb:
        run(remove)
        assert not exe.exists() and before==keys(), 'ordinary remove lost persistent data'
        run(install)
        ensure_running()
        assert before==keys(), 'reinstall lost credentials'
    # Native service account, not root, owns the runtime state.
    uid=int(subprocess.check_output(['id','-u','kurumifrp']))
    assert uid!=0 and (base/'data/state.json').stat().st_uid==uid
    if Path('/run/systemd/system').exists():
        run(['systemctl', 'is-active', '--quiet', 'kurumifrp-updater.path'])
        request_file=base/'update.request'
        staged=base/'.update-test'
        staged.write_text('v' + expected_version)  # Same version is rejected without downloading or installing.
        os.chown(staged, uid, -1)
        os.replace(staged, request_file)
        for _ in range(200):
            if not request_file.exists(): break
            time.sleep(.1)
        assert not request_file.exists(), 'systemd did not process update request'
        assert api('state')['version']==expected_version, 'rejected update changed the running version'
    fixture=base/'cores/frps'
    fixture.write_text('#!/bin/sh\nif [ "$1" = verify ]; then exit 0; fi\nprintf %s "$TMPDIR" > "$TMPDIR/frp-temp-probe"\nexec sleep 600\n')
    fixture.chmod(0o755)
    state=api('state')
    api('save',{'revision':state['revision'],'kind':'instance','value':{'name':'native-uninstall-child','kind':'frps','host':'127.0.0.1','port':17070,'token':'synthetic-test-token','httpPort':0,'httpsPort':0}})
    state=api('state'); iid=state['state']['instances'][0]['id']
    api('control',{'revision':state['revision'],'id':iid,'action':'start'})
    child=api('state')['runtime'][iid]['pid']
    assert child>0
    temproot=base/'tmp'
    for _ in range(50):
        probes=list(temproot.glob('session-*/frp-temp-probe'))
        if probes: break
        time.sleep(.1)
    assert probes and probes[0].read_text()==str(probes[0].parent), 'frp escaped managed temporary path'
    assert not Path('/root/.config/kurumifrp').exists()
    assert not Path('/root/.cache/kurumifrp').exists()
    with tempfile.TemporaryDirectory(prefix='kurumi-external-') as temp:
        outside=Path(temp)/'keep'; outside.write_text('unrelated data')
        (base/'cores/linked').symlink_to(temp,target_is_directory=True)
        run(purge)
        for proc in processes:
            proc.wait(timeout=10)
        assert outside.read_text()=='unrelated data', 'external symlink target changed'
    try:
        os.kill(child,0)
        raise AssertionError('managed child survived package removal')
    except ProcessLookupError:
        pass
    for p in (root,base,meta,exe,Path('/usr/lib/systemd/system/kurumifrp.service'),Path('/usr/share/doc/kurumifrp')):
        assert not p.exists(), 'managed path remains: '+str(p)
    assert subprocess.run(['getent','passwd','kurumifrp'],stdout=subprocess.DEVNULL).returncode!=0
    print('PASS: real native package install, upgrade/reinstall, authenticated readiness, state preservation, complete purge and external-file protection.')

    # Upgrade an actual published package with a running manager and verify
    # that both the active copy and the installation-directory backup survive.
    arch = subprocess.check_output(['dpkg','--print-architecture'], text=True).strip() if is_deb else subprocess.check_output(['rpm','--eval','%{_arch}'], text=True).strip()
    name = f'kurumifrp_0.7.1.12_{arch}.deb' if is_deb else f'kurumifrp-0.7.1.12-1.{arch}.rpm'
    # Digests and sizes are pinned to the official v0.7.1.12 release assets.
    assets = {
        'kurumifrp_0.7.1.12_amd64.deb': (3382600, '08b55a486db0bfc60af49b9fbe618b0f575e32d91a2657f0f27b93f963ba8c7f'),
        'kurumifrp_0.7.1.12_arm64.deb': (3043346, '658d5e39c74d31f41b501d1c96ffa8351e5bfa5f9eb686e47dcf8cb890b592aa'),
        'kurumifrp-0.7.1.12-1.x86_64.rpm': (3927562, '62b701199a61e1fd2bc7ac20645077c4fe22c4a6593fe59fe3461b12cfedb104'),
        'kurumifrp-0.7.1.12-1.aarch64.rpm': (3566679, '23861ef0c5c82cdfab550d3dacc4e4eac2c77a3375572c5bce9d8f861750a9cc'),
    }
    if name not in assets:
        raise RuntimeError('No pinned old-package fixture for ' + name)
    size, digest = assets[name]
    url = f'https://github.com/Presikid/KurumiFrp/releases/download/v0.7.1.12/{name}'
    with tempfile.TemporaryDirectory(prefix='kurumi-old-package-') as temp:
        old_package=Path(temp)/name
        with urllib.request.urlopen(urllib.request.Request(url, headers={'User-Agent':'KurumiFRP-migration-test'}), timeout=120) as response:
            raw=response.read(size+1)
        assert len(raw) == size and hashlib.sha256(raw).hexdigest() == digest
        old_package.write_bytes(raw)
        run(['dpkg','-i',str(old_package)] if is_deb else ['rpm','-Uvh','--replacepkgs',str(old_package)])
        if not Path('/run/systemd/system').exists():
            processes.append(subprocess.Popen(['runuser','-u','kurumifrp','--',str(exe)],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL))
        run([str(exe),'--check-running'])
        old=Path('/var/lib/kurumifrp')
        old_token=(old/'data/access.token').read_text()
        old_address=(old/'data/listen.addr').read_text().strip()
        def old_api(path, value=None):
            request=urllib.request.Request('http://'+old_address+'/api/'+path,
                data=None if value is None else json.dumps(value).encode(),
                headers={'Authorization':'Bearer '+old_token,'Content-Type':'application/json'})
            with opener.open(request, timeout=10) as response: return json.load(response)
        old_revision=old_api('state')['revision']
        old_api('save', {'revision':old_revision,'kind':'instance','value':{'name':'migration-fixture','kind':'frpc','host':'127.0.0.1','port':7000,'token':'migration-secret','httpPort':0,'httpsPort':0}})
        old_state=json.loads((old/'data/state.json').read_text())
        run(install)
        ensure_running()
        backup=root/'backups/legacy/data'
        assert (base/'data/access.token').read_text() == old_token == (backup/'access.token').read_text()
        assert json.loads((base/'data/state.json').read_text())['key'] == old_state['key'] == json.loads((backup/'state.json').read_text())['key']
        assert json.loads((base/'data/state.json').read_text())['state']['instances'][0]['name'] == 'migration-fixture'
        assert json.loads((backup/'state.json').read_text())['state']['instances'][0]['name'] == 'migration-fixture'
        assert not old.exists() and not Path('/var/lib/kurumifrp-package').exists()
        run(purge)
        assert not root.exists(), 'purge left the runtime or legacy backup'
    print('PASS: published old package upgrade retained credentials and an installation-directory backup.')
finally:
    try:
        if exe.exists():
            subprocess.run([str(exe),'--stop'],timeout=55)
        for proc in processes:
            try: proc.wait(timeout=5)
            except subprocess.TimeoutExpired: proc.terminate(); proc.wait(timeout=5)
    finally:
        opt.chmod(opt_mode)
