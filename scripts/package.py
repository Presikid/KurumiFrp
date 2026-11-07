"""Build dependency-free release packages. Run with Python 3 and Go installed."""
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import zipfile

ROOT = Path(__file__).resolve().parents[1]
VERSION = (ROOT / 'VERSION').read_text().strip()
OUT = ROOT / 'dist'
OUT.mkdir(exist_ok=True)
for system, arch in [('linux','amd64'),('linux','arm64'),('windows','amd64'),('windows','arm64')]:
    only = os.environ.get('KURUMI_PACKAGE_TARGET')
    if only and only != system+'_'+arch: continue
    with tempfile.TemporaryDirectory(prefix='kurumi-package-') as tmp:
        stage = Path(tmp) / 'KurumiFRP'
        stage.mkdir()
        name = 'kurumi.exe' if system == 'windows' else 'kurumi'
        env = dict(os.environ, GOOS=system, GOARCH=arch, CGO_ENABLED='0')
        subprocess.run(['go','build','-trimpath','-ldflags=-s -w','-o',str(stage/name),'./cmd/kurumi'], cwd=ROOT, env=env, check=True)
        (stage/'PLATFORM').write_text(f'{system}_{arch}\n', encoding='utf-8')
        (stage/'VERSION').write_text(VERSION+'\n', encoding='utf-8')
        digest = hashlib.sha256((stage/name).read_bytes()).hexdigest()
        (stage/'SHA256SUMS').write_text(f'{digest}  {name}\n', encoding='ascii')
        shutil.copyfile(ROOT/'docs/INSTALL.md',stage/'INSTALL.md')
        for f in (['Install.cmd','install.ps1','Uninstall.cmd','uninstall.ps1'] if system=='windows' else ['install.sh','uninstall.sh']):
            shutil.copyfile(ROOT/'packaging'/f,stage/f)
        manifest = {'product':'io.github.Presikid.KurumiFrp','version':VERSION,'platform':system+'_'+arch,
                    'files':{p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(stage.iterdir())}}
        (stage/'PACKAGE-MANIFEST.json').write_text(json.dumps(manifest,indent=2)+'\n',encoding='utf-8')
        suffix = 'zip'  if system=='windows' else 'tar.gz'
        output = OUT/f'KurumiFRP_{VERSION}_{system}_{arch}.{suffix}'
        if suffix=='zip':
            with zipfile.ZipFile(output,'w',compression=zipfile.ZIP_DEFLATED) as z:
                for f in sorted(stage.iterdir()): z.write(f,'KurumiFRP/'+f.name)
        else:
            with tarfile.open(output,'w:gz') as t:
                for f in sorted(stage.iterdir()):
                    info=t.gettarinfo(f,'KurumiFRP/'+f.name)
                    info.mode=0o755 if f.name in (name,'install.sh') else 0o644
                    info.uid=info.gid=0;info.uname=info.gname=''
                    with f.open('rb') as stream:t.addfile(info,stream)
        print(output.name)
for src,name in [('install-online.sh','install-linux.sh'),('install-online.ps1','install-windows.ps1')]:
    shutil.copyfile(ROOT/'packaging'/src, OUT/name)
archives=sorted(list(OUT.glob(f'KurumiFRP_{VERSION}_*.zip'))+list(OUT.glob(f'KurumiFRP_{VERSION}_*.tar.gz')))
(OUT/'checksums.txt').write_text(''.join(hashlib.sha256(p.read_bytes()).hexdigest()+'  '+p.name+'\n' for p in archives+[OUT/'install-linux.sh',OUT/'install-windows.ps1']),encoding='ascii')
