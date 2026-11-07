"""Isolated user-install smoke test. Never use with a real user data directory."""
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

package=Path(sys.argv[1]).resolve()
with tempfile.TemporaryDirectory(prefix='kurumi-install-test-') as temp:
    root=Path(temp);payload=root/'package';payload.mkdir()
    # This is a package produced by this checkout, not an arbitrary external input.
    if package.suffix=='.zip':
        with zipfile.ZipFile(package) as z:z.extractall(payload)
    else:
        with tarfile.open(package) as t:t.extractall(payload,filter='data')
    source=payload/'KurumiFRP'
    env=dict(os.environ)
    if os.name=='nt':
        env['LOCALAPPDATA']=str(root/'local');Path(env['LOCALAPPDATA']).mkdir()
        install=['powershell.exe','-NoProfile','-ExecutionPolicy','Bypass','-File',str(source/'install.ps1'),'-NoOpen','-NoAutostart']
        target=Path(env['LOCALAPPDATA'])/'KurumiFRP'
        exe=target/'kurumi.exe'
    else:
        env['HOME']=str(root/'home');Path(env['HOME']).mkdir()
        stubs=root/'stubs';stubs.mkdir()
        for name,code in [('systemctl','exit 1'),('xdg-open','exit 0')]:
            p=stubs/name;p.write_text('#!/bin/sh\n'+code+'\n');p.chmod(0o755)
        env['PATH']=str(stubs)+os.pathsep+env['PATH']
        install=['sh',str(source/'install.sh')]
        target=Path(env['HOME'])/'.local/share/kurumifrp';exe=target/'bin/kurumi'
    opener=urllib.request.build_opener(urllib.request.ProxyHandler({}))
    child=None
    def api(path,body=None):
        addr=(target/'data/listen.addr').read_text().strip()
        token=(target/'data/access.token').read_text()
        req=urllib.request.Request('http://'+addr+'/api/'+path,data=json.dumps(body).encode() if body is not None else None,headers={'Content-Type':'application/json','Authorization':'Bearer '+token})
        with opener.open(req,timeout=8) as r:return json.load(r)
    hashes=None
    try:
        for iteration in range(2):
            subprocess.run(install,env=env,check=True,timeout=90)
            if os.name=='nt':
                subprocess.run(['powershell.exe','-NoProfile','-ExecutionPolicy','Bypass','-File',str(target/'Launch.ps1'),'-Background'],env=env,check=True,timeout=20)
            for _ in range(100):
                try:assert api('state')['version']=='0.2.0';break
                except OSError:time.sleep(.1)
            else:raise AssertionError('installed manager did not start')
            current=[hashlib.sha256((target/'data'/n).read_bytes()).hexdigest() for n in ('access.token','master.key')]
            if hashes is not None:assert current==hashes,'installer rotated secrets'
            hashes=current
            ticket=api('browser-ticket',{})['url'].split('#launch=')[1]
            req=urllib.request.Request('http://'+(target/'data/listen.addr').read_text()+'/api/launch-login',data=json.dumps({'ticket':ticket}).encode(),headers={'Content-Type':'application/json'})
            with opener.open(req,timeout=8) as r:assert r.status==200
            api('quit',{})
            for _ in range(100):
                if not (target/'data/manager.lock').exists():break
                time.sleep(.1)
            assert not (target/'data/manager.lock').exists()
            if child:child.wait(timeout=20);child=None
        print('PASS: install, start, browser ticket, quit, repeat install and credential preservation.')
    finally:
        try:api('quit',{})
        except OSError:pass
        if child and child.poll() is None:child.terminate();child.wait(timeout=20)
