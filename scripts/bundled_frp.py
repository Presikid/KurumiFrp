"""Download and SHA-256 verify official fatedier/frp (frpc + frps) binaries for packaging."""
from functools import lru_cache
import hashlib
import io
import json
import os
from pathlib import Path
import re
import tarfile
import urllib.parse
import urllib.request
import zipfile


class OfficialRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        u = urllib.parse.urlsplit(newurl)
        allowed = {'github.com', 'release-assets.githubusercontent.com', 'objects.githubusercontent.com'}
        if u.scheme != 'https' or u.hostname not in allowed or u.username or u.password or u.port not in (None, 443):
            raise ValueError(f'untrusted redirect target: {newurl}')
        return super().redirect_request(req, fp, code, msg, headers, newurl)


@lru_cache(maxsize=1)
def release_metadata():
    supplied = os.environ.get('KURUMI_FRP_RELEASE')
    if supplied is None:
        headers = {'User-Agent': 'KurumiFRP-Packager', 'Accept': 'application/vnd.github+json'}
        gh_tok = os.environ.get('GITHUB_TOKEN') or os.environ.get('GH_TOKEN')
        if gh_tok:
            headers['Authorization'] = f'Bearer {gh_tok}'
        req = urllib.request.Request('https://api.github.com/repos/fatedier/frp/releases/latest', headers=headers)
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), OfficialRedirect())
        with opener.open(req, timeout=60) as resp:
            raw = resp.read((4 << 20) + 1)
    else:
        raw = supplied.encode('utf-8')
    if len(raw) > 4 << 20:
        raise ValueError('official frp release metadata exceeds 4 MiB')
    rel = json.loads(raw)
    if rel.get('draft') is not False or rel.get('prerelease') is not False:
        raise ValueError('latest fatedier/frp release is not a stable release')
    return rel


def resolve_asset(release, tag, platform):
    candidates = [platform]
    if platform == 'linux_arm_hf':
        candidates.append('linux_arm')
    for plat in candidates:
        suffix = '.zip' if plat.startswith('windows_') else '.tar.gz'
        prefix = f'frp_{tag[1:]}_{plat}'
        name = prefix + suffix
        matches = [a for a in release.get('assets', []) if a.get('name') == name]
        if len(matches) == 1:
            return plat, prefix, suffix, matches[0]
    raise ValueError(f'official frp asset not found for platform {platform}')


def prepare(platform, target):
    release = release_metadata()
    tag = release['tag_name']
    if not re.fullmatch(r'v[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}', tag):
        raise ValueError(f'invalid official frp tag: {tag}')

    resolved_plat, prefix, suffix, asset = resolve_asset(release, tag, platform)
    url = f"https://github.com/fatedier/frp/releases/download/{tag}/{asset['name']}"
    size = asset.get('size')
    digest = asset.get('digest')
    if asset.get('browser_download_url') != url or not isinstance(size, int) or not (0 < size <= (160 << 20)):
        raise ValueError('invalid official asset metadata')

    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), OfficialRedirect())
    req = urllib.request.Request(url, headers={'User-Agent': 'KurumiFRP-Packager'})
    with opener.open(req, timeout=120) as resp:
        body = resp.read(size + 1)

    if len(body) != size:
        raise ValueError('official frp archive size mismatch')
    actual_sha256 = hashlib.sha256(body).hexdigest()
    if digest:
        if not re.fullmatch(r'sha256:[a-f0-9]{64}', digest) or digest != f'sha256:{actual_sha256}':
            raise ValueError('official frp archive SHA-256 digest mismatch')

    names = ['frpc.exe', 'frps.exe', 'LICENSE'] if suffix == '.zip' else ['frpc', 'frps', 'LICENSE']
    extracted = {}
    if suffix == '.zip':
        with zipfile.ZipFile(io.BytesIO(body)) as zf:
            for item in names:
                matches = [x for x in zf.infolist() if x.filename == f'{prefix}/{item}']
                if len(matches) != 1 or matches[0].is_dir():
                    raise ValueError(f'missing {item} in official zip archive')
                extracted[item] = zf.read(matches[0])
    else:
        with tarfile.open(fileobj=io.BytesIO(body), mode='r:gz') as tf:
            for entry in tf:
                for item in names:
                    if entry.name == f'{prefix}/{item}' and entry.isfile():
                        extracted[item] = tf.extractfile(entry).read()

    if set(extracted) != set(names) or not all(extracted.values()):
        raise ValueError('official frp archive missing required binaries or LICENSE')

    target_dir = Path(target)
    target_dir.mkdir(parents=True, exist_ok=True)
    for item, content in extracted.items():
        out_file = target_dir / item
        out_file.write_bytes(content)
        out_file.chmod(0o644 if item == 'LICENSE' else 0o755)

    manifest = {
        'tag': tag,
        'platform': resolved_plat,
        'archiveSHA256': actual_sha256,
        'files': {k: hashlib.sha256(v).hexdigest() for k, v in extracted.items()},
    }
    raw_manifest = json.dumps(manifest, indent=2, sort_keys=True).encode('utf-8') + b'\n'
    (target_dir / 'manifest.json').write_bytes(raw_manifest)
    (target_dir / 'manifest.json').chmod(0o644)
    return tag
