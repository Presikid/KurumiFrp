"""Test one real installer round trip on disposable GitHub-hosted Windows runners."""
import copy
import hashlib
import json
import os
from pathlib import Path
import struct
import subprocess
import sys
import tempfile
import urllib.request
import winreg

if (os.name != 'nt' or os.environ.get('GITHUB_ACTIONS') != 'true'
        or os.environ.get('RUNNER_ENVIRONMENT') != 'github-hosted'):
    raise SystemExit('Only run on disposable GitHub-hosted Windows runners.')

new_setup = Path(sys.argv[1]).resolve(strict=True)
app = Path(os.environ.get('ProgramW6432', os.environ['ProgramFiles'])) / 'KurumiFRP'
legacy = Path(os.environ['ProgramData']) / 'KurumiFRP'
uninstall_key = r'Software\Microsoft\Windows\CurrentVersion\Uninstall\io.github.Presikid.KurumiFRP_is1'
for hive in (winreg.HKEY_LOCAL_MACHINE, winreg.HKEY_CURRENT_USER):
    for view in (winreg.KEY_WOW64_64KEY, winreg.KEY_WOW64_32KEY):
        try:
            with winreg.OpenKey(hive, uninstall_key, 0, winreg.KEY_READ | view):
                raise SystemExit('Pre-existing installation retained.')
        except FileNotFoundError:
            pass
if app.exists() or legacy.exists():
    raise SystemExit('Pre-existing installation retained.')

url = 'https://github.com/Presikid/KurumiFrp/releases/download/v0.7.1.12/KurumiFRP-0.7.1.12-Setup.exe'
expected = '07fae3eb35c9e4c894d4eefdc98ea1677e4567d4356157d1525d50379c83237b'
opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
architectures = {'X64': 0x8664, 'ARM64': 0xAA64}
runner_arch = os.environ.get('RUNNER_ARCH')
if runner_arch not in architectures:
    raise SystemExit('Unsupported runner architecture: ' + str(runner_arch))


def setup(path):
    subprocess.run([str(path), '/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART', '/TASKS='], check=True, timeout=180)


def profile():
    exe = app / 'kurumi.exe'
    with exe.open('rb') as f:
        f.seek(0x3c)
        pe_offset = struct.unpack('<I', f.read(4))[0]
        f.seek(pe_offset)
        assert f.read(4) == b'PE\0\0', 'invalid executable'
        machine = struct.unpack('<H', f.read(2))[0]
    assert machine == architectures[runner_arch], 'installed executable architecture mismatch'
    subprocess.run([str(exe), '--check-running'], check=True, timeout=30)
    paths = json.loads(subprocess.check_output([str(exe), '--paths'], text=True))
    root = Path(paths['data']).parent
    assert root.is_relative_to(app / 'runtime'), 'wrong runtime location'
    return root


def api(root, path, body=None):
    address = (root / 'data/listen.addr').read_text(encoding='utf-8').strip()
    token = (root / 'data/access.token').read_text(encoding='utf-8')
    req = urllib.request.Request('http://' + address + '/api/' + path,
        data=None if body is None else json.dumps(body).encode(),
        headers={'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'})
    with opener.open(req, timeout=15) as response:
        return json.load(response)


def disk(root):
    return json.loads((root / 'data/state.json').read_text(encoding='utf-8'))


def state_for_comparison(state):
    # State.clone converts a nil Domains slice to an empty slice. No other
    # field, missing key, or value is ignored in the comparison.
    state = copy.deepcopy(state)
    for tunnel in state['tunnels']:
        if 'domains' in tunnel and tunnel['domains'] is None:
            tunnel['domains'] = []
    return state


def changed_paths(left, right, path='state'):
    # Report paths and types only: do not print keys, ciphertext, or tokens.
    if isinstance(left, dict) and isinstance(right, dict):
        return [p for key in sorted(left.keys() | right.keys())
                for p in (changed_paths(left[key], right[key], path + '.' + key)
                          if key in left and key in right else [path + '.' + key + ' (missing)'])]
    if isinstance(left, list) and isinstance(right, list) and len(left) == len(right):
        return [p for index, (a, b) in enumerate(zip(left, right))
                for p in changed_paths(a, b, path + '[' + str(index) + ']')]
    return [] if left == right else [path + ' (' + type(left).__name__ + '/' + type(right).__name__ + ')']


with tempfile.TemporaryDirectory(prefix='kurumi-downgrade-') as tmp:
    old_setup = Path(tmp) / 'old-setup.exe'
    req = urllib.request.Request(url, headers={'User-Agent': 'KurumiFRP-CI'})
    with urllib.request.urlopen(req, timeout=120) as response:
        installer = response.read((16 << 20) + 1)
    if len(installer) > 16 << 20 or hashlib.sha256(installer).hexdigest() != expected:
        raise RuntimeError('Previous installer digest mismatch')
    old_setup.write_bytes(installer)
    try:
        setup(new_setup)
        root = profile()
        initial_root = root
        state = api(root, 'state')
        api(root, 'history-settings', {'revision': state['revision'], 'historyLimit': 12})
        state = api(root, 'state')
        api(root, 'save', {'revision': state['revision'], 'kind': 'instance', 'value': {
            'name': 'downgrade-fixture', 'kind': 'frpc', 'host': '127.0.0.1', 'port': 17070,
            'token': 'downgrade-fixture-token', 'autoStart': True}})
        state = api(root, 'state')
        instance = next(i for i in state['state']['instances'] if i['name'] == 'downgrade-fixture')
        api(root, 'save', {'revision': state['revision'], 'kind': 'tunnel', 'value': {
            'name': 'probe-test', 'instanceId': instance['id'], 'type': 'http',
            'localHost': '127.0.0.1', 'localPort': 18080, 'domains': ['example.com'],
            'probePath': '/healthz', 'probeMethod': 'GET', 'enabled': False}})
        state = api(root, 'state')
        api(root, 'save', {'revision': state['revision'], 'kind': 'tunnel', 'value': {
            'name': 'secret-test', 'instanceId': instance['id'], 'type': 'stcp',
            'localHost': '127.0.0.1', 'localPort': 18081,
            'secret': 'downgrade-fixture-secret', 'enabled': False}})
        before = disk(root)
        assert len(before['history']) >= 3, 'fixture history missing'
        access = (root / 'data/access.token').read_text(encoding='utf-8')
        new_version = (Path(__file__).resolve().parents[1] / 'VERSION').read_text(encoding='utf-8').strip()

        def check(expected_version, expected_db):
            current_root = profile()
            assert current_root == initial_root, 'profile location changed'
            after = disk(current_root)
            assert after['key'] == expected_db['key'], 'data key changed'
            assert after['state'] == expected_db['state'], 'configuration or encrypted credentials changed'
            assert after['history'] == expected_db['history'], 'history snapshots changed'
            assert after['historyLimit'] == expected_db['historyLimit'], 'history limit changed'
            assert (current_root / 'data/access.token').read_text(encoding='utf-8') == access, 'access token changed'
            live = api(current_root, 'state')
            assert live['version'] == expected_version, 'installed version did not change'
            live_instance = next(i for i in live['state']['instances'] if i['name'] == 'downgrade-fixture')
            assert live_instance['autoStart'] and live_instance['hasToken']
            assert next(t for t in live['state']['tunnels'] if t['name'] == 'secret-test')['hasSecret']
            return current_root, live

        setup(old_setup)
        root, state = check('0.7.1.12', before)
        # Force the old program to decrypt and rewrite the new program's data.
        instance = next(i for i in state['state']['instances'] if i['name'] == 'downgrade-fixture')
        instance['port'] = 17071
        api(root, 'save', {'revision': state['revision'], 'kind': 'instance', 'value': instance})
        old_written = disk(root)
        assert old_written['key'] == before['key']
        assert old_written['history'][:-1] == before['history'], 'older version lost previous snapshots'
        actual_snapshot = state_for_comparison(old_written['history'][-1]['state'])
        previous_state = state_for_comparison(before['state'])
        assert actual_snapshot == previous_state, 'snapshot differences: ' + ', '.join(changed_paths(previous_state, actual_snapshot))
        expected_state = copy.deepcopy(previous_state)
        next(i for i in expected_state['instances'] if i['id'] == instance['id'])['port'] = 17071
        actual_state = state_for_comparison(old_written['state'])
        assert actual_state == expected_state, 'configuration differences: ' + ', '.join(changed_paths(expected_state, actual_state))
        setup(new_setup)
        check(new_version, old_written)
        print('PASS:', runner_arch, 'current -> 0.7.1.12 -> current; configuration, secrets and snapshots preserved')
    finally:
        uninstaller = app / 'unins000.exe'
        if uninstaller.exists():
            subprocess.run([str(uninstaller), '/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART'], check=True, timeout=180)

