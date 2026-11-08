"""Run the real Setup EXE and native uninstaller on disposable Windows CI."""
import ctypes
from ctypes import wintypes
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
    with winreg.OpenKey(winreg.HKEY_LOCAL_MACHINE,arp,0,access):
        raise SystemExit('A real pre-existing installation was found.')
except FileNotFoundError:
    pass
opener=urllib.request.build_opener(urllib.request.ProxyHandler({}))


def wait_until(test):
    for _ in range(150):
        if test(): return
        time.sleep(.2)
    raise AssertionError('condition not satisfied')


class STARTUPINFO(ctypes.Structure):
    _fields_=[('cb',wintypes.DWORD),('lpReserved',wintypes.LPWSTR),('lpDesktop',wintypes.LPWSTR),('lpTitle',wintypes.LPWSTR),
              ('dwX',wintypes.DWORD),('dwY',wintypes.DWORD),('dwXSize',wintypes.DWORD),('dwYSize',wintypes.DWORD),
              ('dwXCountChars',wintypes.DWORD),('dwYCountChars',wintypes.DWORD),('dwFillAttribute',wintypes.DWORD),
              ('dwFlags',wintypes.DWORD),('wShowWindow',wintypes.WORD),('cbReserved2',wintypes.WORD),
              ('lpReserved2',ctypes.POINTER(ctypes.c_byte)),('hStdInput',wintypes.HANDLE),('hStdOutput',wintypes.HANDLE),('hStdError',wintypes.HANDLE)]
class PROCESSINFO(ctypes.Structure):
    _fields_=[('hProcess',wintypes.HANDLE),('hThread',wintypes.HANDLE),('dwProcessId',wintypes.DWORD),('dwThreadId',wintypes.DWORD)]
adv=ctypes.WinDLL('advapi32',use_last_error=True)
kernel=ctypes.WinDLL('kernel32',use_last_error=True)
kernel.CloseHandle.argtypes=[wintypes.HANDLE]
kernel.WaitForSingleObject.argtypes=[wintypes.HANDLE,wintypes.DWORD]
kernel.GetExitCodeProcess.argtypes=[wintypes.HANDLE,ctypes.POINTER(wintypes.DWORD)]
create=adv.CreateProcessWithLogonW
create.argtypes=[wintypes.LPCWSTR,wintypes.LPCWSTR,wintypes.LPCWSTR,wintypes.DWORD,wintypes.LPCWSTR,wintypes.LPWSTR,
                 wintypes.DWORD,wintypes.LPVOID,wintypes.LPCWSTR,ctypes.POINTER(STARTUPINFO),ctypes.POINTER(PROCESSINFO)]
create.restype=wintypes.BOOL

def as_standard(username,password,program,args=(),wait=False):
    si=STARTUPINFO();si.cb=ctypes.sizeof(si);pi=PROCESSINFO()
    line=ctypes.create_unicode_buffer(subprocess.list2cmdline([str(program),*map(str,args)]))
    if not create(username,'.',password,1,str(program),line,0,None,str(program.parent),ctypes.byref(si),ctypes.byref(pi)):
        raise ctypes.WinError(ctypes.get_last_error())
    kernel.CloseHandle(pi.hThread)
    if wait:
        try:
            assert kernel.WaitForSingleObject(pi.hProcess,30000)==0, 'standard-user command timed out'
            code=wintypes.DWORD();assert kernel.GetExitCodeProcess(pi.hProcess,ctypes.byref(code))
            assert code.value==0, 'standard-user command failed: '+str(code.value)
        finally: kernel.CloseHandle(pi.hProcess)
    else:
        kernel.CloseHandle(pi.hProcess)
    return pi.dwProcessId


with tempfile.TemporaryDirectory(prefix='kurumi-native-') as temp:
    root=Path(temp)
    app=Path(os.environ.get('ProgramW6432',os.environ['ProgramFiles']))/'KurumiFRP'; exe=app/'kurumi.exe'
    common=app/'runtime'
    legacy=Path(os.environ['ProgramData'])/'KurumiFRP'
    if app.exists() or common.exists() or legacy.exists(): raise SystemExit('Pre-existing machine paths retained.')
    profile=None
    account=None
    def api(path,body=None):
        address=(profile/'data/listen.addr').read_text().strip()
        req=urllib.request.Request('http://'+address+'/api/'+path,
            data=None if body is None else json.dumps(body).encode(),
            headers={'Authorization':'Bearer '+(profile/'data/access.token').read_text(),'Content-Type':'application/json'})
        with opener.open(req,timeout=10) as r: return json.load(r)
    try:
        before=None
        for i in range(2):
            source=root/('Setup-'+str(i)+'.exe'); shutil.copyfile(setup,source)
            log=root/('install-'+str(i)+'.log')
            result=subprocess.run([str(source),'/VERYSILENT','/SUPPRESSMSGBOXES','/NORESTART',
                                  '/TASKS=cleanup','/LOG='+str(log)],timeout=100)
            if result.returncode:
                print(log.read_text(encoding='utf-8',errors='replace')); result.check_returncode()
            paths=json.loads(subprocess.check_output([str(exe),'--paths'],text=True))
            profile=Path(paths['data']).parent
            assert profile.parent==common/'profiles', paths
            assert profile.is_relative_to(app), 'runtime data escaped the installation directory'
            assert not legacy.exists(), 'legacy ProgramData data was not migrated'
            assert Path(paths['temporary'])==profile/'tmp'
            subprocess.run([str(exe),'--check-running'],check=True,timeout=25)
            assert api('state')['version']=='0.7.1.4'
            wait_until(lambda:not source.exists())
            current=[(profile/'data/access.token').read_text(), json.loads((profile/'data/state.json').read_text())['key']]
            if before is not None: assert before==current, 'reinstall rotated secrets'
            before=current
            with winreg.OpenKey(winreg.HKEY_LOCAL_MACHINE,arp,0,access) as key:
                assert winreg.QueryValueEx(key,'DisplayName')[0]=='KurumiFRP'
                command=winreg.QueryValueEx(key,'UninstallString')[0]
                assert 'unins' in command and '.exe' in command
            assert not list(app.rglob('*.ps1')) and not list(app.rglob('*.cmd')), 'script launch dependency remains'
        # Test an actual non-administrator account, not only an elevated runner.
        import secrets
        username='kfrp'+secrets.token_hex(5)
        # net.exe prompts interactively for passwords longer than 14 chars.
        # Use a complex 14-character ephemeral CI password, never log argv.
        password='Km1!'+secrets.token_hex(5)
        created=subprocess.run(['net.exe','user',username,password,'/add'],capture_output=True,text=True)
        if created.returncode:
            raise RuntimeError('Temporary test user creation failed: '+(created.stdout+created.stderr).replace(password,'[redacted]'))
        account=username
        original_profiles=set((common/'profiles').iterdir())
        as_standard(username,password,exe)
        wait_until(lambda:len(set((common/'profiles').iterdir())-original_profiles)==1)
        other=(set((common/'profiles').iterdir())-original_profiles).pop()
        wait_until(lambda:(other/'data/access.token').exists() and (other/'data/listen.addr').exists())
        as_standard(username,password,exe,['--check-running'],wait=True)
        assert json.loads((other/'data/state.json').read_text())['key']!=json.loads((profile/'data/state.json').read_text())['key']
        assert (other/'data/listen.addr').read_text()!=(profile/'data/listen.addr').read_text()
        probe_source=root/'acl.go'
        probe_source.write_text('package main\nimport("os")\nfunc main(){if f,e:=os.Open(os.Args[1]);e==nil{f.Close();os.Exit(71)};if e:=os.WriteFile(os.Args[2],[]byte("bad"),0600);e==nil{os.Remove(os.Args[2]);os.Exit(72)}}\n')
        probe_exe=app/'acl-probe.exe'
        subprocess.run(['go','build','-o',str(probe_exe),str(probe_source)],check=True)
        try: as_standard(username,password,probe_exe,[profile/'data/access.token',app/'forbidden-write'],wait=True)
        finally: probe_exe.unlink()
        # Failed authenticated shutdown must abort before the engine deletes files.
        token=profile/'data/access.token'; saved=token.read_bytes()
        token.write_text('b'*64)
        failed=subprocess.run([str(app/'unins000.exe'),'/VERYSILENT','/SUPPRESSMSGBOXES','/NORESTART'],timeout=35)
        assert failed.returncode!=0 and exe.exists() and (profile/'data/state.json').exists()
        token.write_bytes(saved)
        assert api('state')['version']=='0.7.1.4'
        fixture=root/'fixture.go'
        fixture.write_text('package main\nimport("os";"time";"path/filepath")\nfunc main(){if len(os.Args)>1 && os.Args[1]=="verify"{return};os.WriteFile(filepath.Join(os.TempDir(),"frp-temp-probe"),[]byte(os.Getenv("TEMP")),0600);for{time.Sleep(time.Second)}}\n')
        subprocess.run(['go','build','-o',str(profile/'cores/frps.exe'),str(fixture)],check=True,timeout=90)
        state=api('state')
        api('save',{'revision':state['revision'],'kind':'instance','value':{'name':'native-child','kind':'frps','host':'127.0.0.1','port':17070,'token':'synthetic-test-token','httpPort':0,'httpsPort':0}})
        state=api('state'); iid=state['state']['instances'][0]['id']
        api('control',{'revision':state['revision'],'id':iid,'action':'start'})
        assert api('state')['runtime'][iid]['pid']>0
        wait_until(lambda:bool(list((profile/'tmp').glob('session-*/frp-temp-probe'))))
        probe=next((profile/'tmp').glob('session-*/frp-temp-probe'))
        assert probe.read_text()==str(probe.parent), 'frp inherited system TEMP'
        outside=root/'outside'; outside.mkdir(); (outside/'keep').write_text('user document')
        subprocess.run(['cmd.exe','/d','/c','mklink','/J',str(profile/'cores/linked'),str(outside)],check=True)
        uninstaller=app/'unins000.exe'
        log=root/'uninstall.log'
        result=subprocess.run([str(uninstaller),'/VERYSILENT','/SUPPRESSMSGBOXES','/NORESTART','/LOG='+str(log)],timeout=100)
        if result.returncode:
            print(log.read_text(encoding='utf-8',errors='replace'));result.check_returncode()
        wait_until(lambda:not app.exists())
        assert not common.exists(), 'installation runtime residue'
        assert not legacy.exists(), 'legacy ProgramData residue'
        assert (outside/'keep').read_text()=='user document'
        try:
            with winreg.OpenKey(winreg.HKEY_LOCAL_MACHINE,arp,0,access): raise AssertionError('ARP residue')
        except FileNotFoundError: pass
        print('PASS: default Program Files install/reinstall, installation-directory isolation with a real standard user, private credentials, read-only Program Files, temporary paths, HKLM registration, verified Setup removal and full machine uninstall.')
    finally:
        if exe.exists(): subprocess.run([str(exe),'--native-maintenance','stop'],timeout=100)
        if account: subprocess.run(['net.exe','user',account,'/delete'],check=True,capture_output=True)
