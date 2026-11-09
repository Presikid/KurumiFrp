"""Native package lifecycle; run as root ONLY on disposable CI/container hosts."""
import hashlib
import fcntl
import json
import os
from pathlib import Path
import shutil
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
    run(install)
    ensure_running()
    before=keys()
    assert api('state')['version']==expected_version
    run(['dpkg','--verify','kurumifrp'] if is_deb else ['rpm','-V','kurumifrp'])
    # The real root updater invokes the package manager on a package in its
    # private scratch directory. The upgrade must not delete its input.
    stage = root/'root-tmp/update-in-progress'
    stage.mkdir(mode=0o700)
    staged_package = stage/package.name
    shutil.copyfile(package, staged_package)
    staged_digest = hashlib.sha256(staged_package.read_bytes()).hexdigest()
    staged_install = ['dpkg','-i',str(staged_package)] if is_deb else ['rpm','-Uvh','--replacepkgs',str(staged_package)]
    updater_lock = root/'root-tmp/.updater.lock'
    with updater_lock.open('a') as lock:
        updater_lock.chmod(0o600)
        fcntl.flock(lock, fcntl.LOCK_EX)
        run(staged_install)  # Upgrade scripts must not deadlock on the updater's lock.
    assert staged_package.exists() and hashlib.sha256(staged_package.read_bytes()).hexdigest() == staged_digest, 'upgrade removed its active input'
    shutil.rmtree(stage)  # Same cleanup performed by the updater after package manager exit.
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
        crashed = root/'root-tmp/update-crashed'
        crashed.mkdir(mode=0o700)
        (crashed/'old-package').write_text('simulated interrupted updater')
        with updater_lock.open('a') as lock:
            fcntl.flock(lock, fcntl.LOCK_EX)
            subprocess.run(purge, timeout=120, check=False)
            assert crashed.exists() and base.exists() and (meta/'uid').exists(), 'concurrent purge damaged an active updater or user data'
        unknown = root/'root-tmp/unrecognized'
        unknown.write_text('do not remove unknown files')
        lifecycle = Path(__file__).resolve().parents[1]/'packaging/linux/lifecycle.sh'
        retry = purge if is_deb or subprocess.run(['rpm', '-q', 'kurumifrp'], stdout=subprocess.DEVNULL).returncode == 0 else ['sh', '-c', '. "$1"; purge_state', 'sh', str(lifecycle)]
        subprocess.run(retry, timeout=120, check=False)
        assert unknown.exists() and base.exists() and (meta/'uid').exists(), 'failed purge deleted user data or its ownership receipt'
        assert subprocess.run(['getent', 'passwd', 'kurumifrp'], stdout=subprocess.DEVNULL).returncode == 0, 'failed purge deleted the account'
        unknown.unlink()
        if is_deb or subprocess.run(['rpm', '-q', 'kurumifrp'], stdout=subprocess.DEVNULL).returncode == 0:
            run(purge)
        else:
            # RPM may already have removed the package despite a postun error.
            run(['sh', '-c', '. "$1"; purge_state', 'sh', str(lifecycle)])
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
    # Reinstall, then simulate an interrupted purge after the service account
    # and runtime disappeared but before root-owned metadata was removed.
    run(install)
    ensure_running()
    run([str(exe), '--stop'])
    if processes: processes[-1].wait(timeout=60)
    uid = int(subprocess.check_output(['id', '-u', 'kurumifrp']))
    gid = int(subprocess.check_output(['id', '-g', 'kurumifrp']))
    intent = meta/'purge-intent'
    intent.write_text(f'{uid}:{gid}\n'); intent.chmod(0o600)
    shutil.rmtree(base)
    shutil.rmtree(root/'root-tmp')
    run(['userdel', 'kurumifrp'])
    run(purge)
    assert not root.exists(), 'interrupted purge did not resume'
    print('PASS: real native package install, upgrade/reinstall, authenticated readiness, state preservation, complete purge and external-file protection.')
finally:
    if exe.exists():
        subprocess.run([str(exe),'--stop'],timeout=55)
    for proc in processes:
        try: proc.wait(timeout=5)
        except subprocess.TimeoutExpired: proc.terminate(); proc.wait(timeout=5)
