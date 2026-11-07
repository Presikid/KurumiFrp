"""Live official download through the authenticated API, followed by TCP forwarding.
CI only: downloads a pinned official release and uses disposable workspaces.
"""
import json
from pathlib import Path
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request

binary = str(Path(sys.argv[1]).resolve())
with tempfile.TemporaryDirectory(prefix='kurumi-core-live-') as tmp:
    root=Path(tmp)
    with socket.socket() as s:
        s.bind(('127.0.0.1',0));port=s.getsockname()[1]
    base=f'http://127.0.0.1:{port}'
    manager=subprocess.Popen([binary,'--data',str(root/'data'),'--frp-dir',str(root/'cores'),'--listen',f'127.0.0.1:{port}'],stdout=subprocess.DEVNULL)
    opener=urllib.request.build_opener(urllib.request.ProxyHandler({}))
    def api(path,body=None):
        token=(root/'data/access.token').read_text()
        req=urllib.request.Request(base+'/api/'+path,data=json.dumps(body).encode() if body is not None else None,headers={'Authorization':'Bearer '+token,'Content-Type':'application/json'})
        try:
            with opener.open(req,timeout=35) as r:return json.load(r)
        except urllib.error.HTTPError as e:raise RuntimeError(f'{path}: {e.code} {e.read().decode()}') from None
    try:
        for _ in range(100):
            try:
                status=api('core');break
            except OSError:time.sleep(.1)
        else:raise RuntimeError('manager not ready')
        api('core/install',{'tag':'v0.72.0','platform':status['host']})
        for _ in range(480):
            status=api('core')
            if status['job']['state']=='installed':break
            if status['job']['state'] in ('failed','cancelled'):raise RuntimeError(status['job']['error'])
            time.sleep(1)
        else:raise RuntimeError('download timed out')
        api('core/activate',{'id':status['job']['installedId']})
        assert api('state')['binaries']=={'frpc':True,'frps':True}
        api('quit',{})
        manager.wait(timeout=30)
        subprocess.run([sys.executable,str(Path(__file__).with_name('frp-smoke.py')),binary,str(root/'cores')],check=True)
        print('PASS: official API download, digest, extract, activate, version probes and real TCP roundtrip.')
    finally:
        if manager.poll() is None:manager.terminate();manager.wait(timeout=30)
