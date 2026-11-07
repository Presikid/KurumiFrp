"""Run the real Setup EXE and native uninstaller on disposable Windows CI."""
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
import winreg

if os.name!='nt' or os.environ.get('CI')!='true':
    raise SystemExit('Only run native Windows install/uninstall tests on disposable CI.')
setup=Path(sys.argv[1]).resolve()
arp=r'Software\Microsoft\Windows\CurrentVersion\Uninstall\io.github.Presikid.KurumiFRP_is1'
access=winreg.KEY_READ|winreg.KEY_WOW64_64KEY
try:
    with winreg.OpenKey(winreg.HKEY_CURRENT_USER,arp,0,access):
        raise SystemExit('A real pre-existing installation was found.')
except FileNotFoundError:
    pass
opener=urllib.request.build_opener(urllib.request.ProxyHandler({}))


def wait_until(test):
    for _ in range(150):
        if test(): return
        time.sleep(.2)
    raise AssertionError('condition not satisfied')


with tempfile.TemporaryDirectory(prefix='kurumi-native-') as temp:
    root=Path(temp); app=root/'Installed App'; exe=app/'kurumi.exe'
    def api(path,body=None):
        address=(app/'data/listen.addr').read_text().strip()
        req=urllib.request.Request('http://'+address+'/api/'+path,
            data=None if body is None else json.dumps(body).encode(),
            headers={'Authorization':'Bearer '+(app/'data/access.token').read_text(),'Content-Type':'application/json'})
        with opener.open(req,timeout=10) as r: return json.load(r)
    try:
        before=None
        for i in range(2):
            source=root/('Setup-'+str(i)+'.exe'); shutil.copyfile(setup,source)
            log=root/('install-'+str(i)+'.log')
            result=subprocess.run([str(source),'/VERYSILENT','/SUPPRESSMSGBOXES','/NORESTART',
                                  '/DIR='+str(app),'/TASKS=cleanup','/LOG='+str(log)],timeout=100)
            if result.returncode:
                print(log.read_text(encoding='utf-8',errors='replace')); result.check_returncode()
            subprocess.run([str(exe),'--check-running'],check=True,timeout=25)
            assert api('state')['version']=='0.4.0'
            wait_until(lambda:not source.exists())
            current=[hashlib.sha256((app/'data'/n).read_bytes()).hexdigest() for n in ('access.token','master.key')]
            if before is not None: assert before==current, 'reinstall rotated secrets'
            before=current
            with winreg.OpenKey(winreg.HKEY_CURRENT_USER,arp,0,access) as key:
                assert winreg.QueryValueEx(key,'DisplayName')[0]=='KurumiFRP'
                command=winreg.QueryValueEx(key,'UninstallString')[0]
                assert 'unins' in command and '.exe' in command
            assert not list(app.rglob('*.ps1')) and not list(app.rglob('*.cmd')), 'script launch dependency remains'
        # Failed authenticated shutdown must abort before the engine deletes files.
        token=app/'data/access.token'; saved=token.read_bytes()
        token.write_text('b'*64)
        failed=subprocess.run([str(app/'unins000.exe'),'/VERYSILENT','/SUPPRESSMSGBOXES','/NORESTART'],timeout=35)
        assert failed.returncode!=0 and exe.exists() and (app/'data/master.key').exists()
        token.write_bytes(saved)
        assert api('state')['version']=='0.4.0'
        fixture=root/'fixture.go'
        fixture.write_text('package main\nimport("os";"time")\nfunc main(){if len(os.Args)>1 && os.Args[1]=="verify"{return};for{time.Sleep(time.Second)}}\n')
        subprocess.run(['go','build','-o',str(app/'cores/frps.exe'),str(fixture)],check=True,timeout=90)
        state=api('state')
        api('save',{'revision':state['revision'],'kind':'instance','value':{'name':'native-child','kind':'frps','host':'127.0.0.1','port':17070,'token':'synthetic-test-token','httpPort':0,'httpsPort':0}})
        state=api('state'); iid=state['state']['instances'][0]['id']
        api('control',{'revision':state['revision'],'id':iid,'action':'start'})
        assert api('state')['runtime'][iid]['pid']>0
        outside=root/'outside'; outside.mkdir(); (outside/'keep').write_text('user document')
        subprocess.run(['cmd.exe','/d','/c','mklink','/J',str(app/'cores/linked'),str(outside)],check=True)
        uninstaller=app/'unins000.exe'
        log=root/'uninstall.log'
        result=subprocess.run([str(uninstaller),'/VERYSILENT','/SUPPRESSMSGBOXES','/NORESTART','/LOG='+str(log)],timeout=100)
        if result.returncode:
            print(log.read_text(encoding='utf-8',errors='replace'));result.check_returncode()
        wait_until(lambda:not app.exists())
        assert (outside/'keep').read_text()=='user document'
        try:
            with winreg.OpenKey(winreg.HKEY_CURRENT_USER,arp,0,access): raise AssertionError('ARP residue')
        except FileNotFoundError: pass
        print('PASS: actual Setup.exe install/reinstall, automatic Setup deletion, ARP registration, no script dependency, native uninstall and junction target protection.')
    finally:
        if exe.exists(): subprocess.run([str(exe),'--stop'],timeout=55)
