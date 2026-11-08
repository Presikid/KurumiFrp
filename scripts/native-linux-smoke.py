"""Native package lifecycle; run as root ONLY on disposable CI/container hosts."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import urllib.request

if os.geteuid() != 0 or os.environ.get('KURUMI_DISPOSABLE_PACKAGE_TEST') != '1':
    raise SystemExit('This test requires an explicitly disposable root environment.')
package = Path(sys.argv[1]).resolve()
is_deb = package.suffix == '.deb'
base = Path('/var/lib/kurumifrp')
meta = Path('/var/lib/kurumifrp-package')
exe = Path('/usr/bin/kurumifrp')
if base.exists() or meta.exists() or exe.exists() or subprocess.run(['getent','passwd','kurumifrp'],stdout=subprocess.DEVNULL).returncode == 0:
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
    assert api('state')['version']=='0.7.1.3'
    run(['dpkg','--verify','kurumifrp'] if is_deb else ['rpm','-V','kurumifrp'])
    # Reinstall/upgrade path must stop and restart the owned manager, never purge data.
    run(install)
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
        staged.write_text('v0.7.1.3')  # Same version is rejected without downloading or installing.
        os.chown(staged, uid, -1)
        os.replace(staged, request_file)
        for _ in range(200):
            if not request_file.exists(): break
            time.sleep(.1)
        assert not request_file.exists(), 'systemd did not process update request'
        assert api('state')['version']=='0.7.1.3', 'rejected update changed the running version'
    fixture=base/'cores/frps'
    fixture.write_text('#!/bin/sh\nif [ "$1" = verify ]; then exit 0; fi\nprintf %s "$TMPDIR" > "$TMPDIR/frp-temp-probe"\nexec sleep 600\n')
    fixture.chmod(0o755)
    state=api('state')
    api('save',{'revision':state['revision'],'kind':'instance','value':{'name':'native-uninstall-child','kind':'frps','host':'127.0.0.1','port':17070,'token':'synthetic-test-token','httpPort':0,'httpsPort':0}})
    state=api('state'); iid=state['state']['instances'][0]['id']
    api('control',{'revision':state['revision'],'id':iid,'action':'start'})
    child=api('state')['runtime'][iid]['pid']
    assert child>0
    temproot=Path('/run/kurumifrp') if Path('/run/systemd/system').exists() else base/'tmp'
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
    for p in (base,meta,exe,Path('/usr/lib/systemd/system/kurumifrp.service'),Path('/usr/share/doc/kurumifrp')):
        assert not p.exists(), 'managed path remains: '+str(p)
    if Path('/run/systemd/system').exists(): assert not Path('/run/kurumifrp').exists()
    assert subprocess.run(['getent','passwd','kurumifrp'],stdout=subprocess.DEVNULL).returncode!=0
    print('PASS: real native package install, upgrade/reinstall, authenticated readiness, state preservation, complete purge and external-file protection.')
finally:
    if exe.exists():
        subprocess.run([str(exe),'--stop'],timeout=55)
    for proc in processes:
        try: proc.wait(timeout=5)
        except subprocess.TimeoutExpired: proc.terminate(); proc.wait(timeout=5)
