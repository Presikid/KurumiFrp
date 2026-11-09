"""Prepare the verified official frp pair for a native or portable package."""
import hashlib
from functools import lru_cache
import io
import json
import os
from pathlib import Path
import re
import tarfile
import urllib.parse
import urllib.request
import zipfile

ROOT = Path(__file__).resolve().parents[1]


class OfficialRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        u = urllib.parse.urlsplit(newurl)
        if u.scheme != 'https' or u.hostname not in {'github.com', 'release-assets.githubusercontent.com', 'objects.githubusercontent.com'} or u.username or u.password or u.port not in (None, 443):
            raise ValueError('untrusted official download redirect')
        return super().redirect_request(req, fp, code, msg, headers, newurl)


@lru_cache(maxsize=1)
def release_metadata():
    supplied = os.environ.get('KURUMI_FRP_RELEASE')
    if supplied is None:
        req = urllib.request.Request('https://api.github.com/repos/fatedier/frp/releases/latest', headers={'User-Agent':'KurumiFRP-package', 'Accept':'application/vnd.github+json'})
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), OfficialRedirect())
        with opener.open(req, timeout=60) as response:
            raw = response.read((4 << 20) + 1)
    else:
        raw = supplied.encode('utf-8')
    if len(raw) > 4 << 20: raise ValueError('official release metadata too large')
    release = json.loads(raw)
    if release.get('draft') is not False or release.get('prerelease') is not False:
        raise ValueError('official latest release is not a confirmed stable release')
    return release


def prepare(platform, target):
    release = release_metadata()
    tag = release['tag_name']
    if not re.fullmatch(r'v[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}', tag):
        raise ValueError('invalid official frp version')
    suffix = '.zip' if platform.startswith('windows_') else '.tar.gz'
    prefix = f'frp_{tag[1:]}_{platform}'
    name = prefix + suffix
    entries = [asset for asset in release['assets'] if asset['name'] == name]
    if len(entries) != 1:
        raise ValueError('official platform asset not unique')
    asset = entries[0]
    url = f'https://github.com/fatedier/frp/releases/download/{tag}/{name}'
    size, digest = asset['size'], asset['digest']
    if asset['browser_download_url'] != url or not isinstance(size, int) or not 0 < size <= 160 << 20 or not re.fullmatch(r'sha256:[a-f0-9]{64}', digest):
        raise ValueError('invalid official asset metadata')
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), OfficialRedirect())
    req = urllib.request.Request(url, headers={'User-Agent': 'KurumiFRP-package'})
    with opener.open(req, timeout=120) as response:
        body = response.read(size + 1)
    if len(body) != size or 'sha256:' + hashlib.sha256(body).hexdigest() != digest:
        raise ValueError('official frp archive size or digest mismatch')
    names = ['frpc.exe', 'frps.exe', 'LICENSE'] if suffix == '.zip' else ['frpc', 'frps', 'LICENSE']
    data = {}
    if suffix == '.zip':
        with zipfile.ZipFile(io.BytesIO(body)) as z:
            if len(z.infolist()) > 1024 or sum(i.file_size for i in z.infolist()) > 300 << 20:
                raise ValueError('frp archive exceeds limits')
            for item in names:
                matches = [x for x in z.infolist() if x.filename == prefix + '/' + item]
                if len(matches) != 1 or matches[0].is_dir() or matches[0].file_size > 120 << 20:
                    raise ValueError('invalid official archive member')
                data[item] = z.read(matches[0])
    else:
        with tarfile.open(fileobj=io.BytesIO(body), mode='r:gz') as tar:
            count = total = 0
            for entry in tar:
                count += 1; total += entry.size
                if count > 1024 or entry.size < 0 or entry.size > 120 << 20 or total > 300 << 20:
                    raise ValueError('frp archive exceeds limits')
                for item in names:
                    if entry.name == prefix + '/' + item:
                        if item in data or not entry.isfile(): raise ValueError('invalid official archive member')
                        data[item] = tar.extractfile(entry).read()
    if set(data) != set(names) or not all(data.values()):
        raise ValueError('official archive is missing binaries or license')
    target = Path(target); target.mkdir(parents=True, exist_ok=True)
    for item, content in data.items():
        (target / item).write_bytes(content)
        (target / item).chmod(0o644 if item == 'LICENSE' else 0o755)
    manifest = {'tag':tag, 'platform':platform, 'archiveSHA256':digest[7:],
                'files':{item:hashlib.sha256(content).hexdigest() for item, content in data.items()}}
    raw = json.dumps(manifest, sort_keys=True, separators=(',', ':')).encode()
    (target / 'manifest.json').write_bytes(raw)
    (target / 'manifest.json').chmod(0o644)
    return hashlib.sha256(raw).hexdigest()
