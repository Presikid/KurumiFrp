"""Real TCP/STCP roundtrips through official frps/frpc; uses disposable loopback ports.
Usage: python3 scripts/frp-smoke.py ./kurumi /path/to/frp-dir
This does not download or install software. Native package CI may pass
--existing ADDRESS DATA_DIR --disposable-package-test to test its disposable installation.
"""
import json
import os
from pathlib import Path
import socket
import socketserver
import subprocess
import sys
import tempfile
import threading
import time
import urllib.error
import urllib.request


def port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


class Echo(socketserver.BaseRequestHandler):
    def handle(self):
        self.request.settimeout(5)
        value = self.request.recv(1000)
        self.request.sendall(value)


binary = str(Path(sys.argv[1]).resolve())
frp = str(Path(sys.argv[2]).resolve())
existing = '--existing' in sys.argv[3:]
if existing and (len(sys.argv) != 7 or sys.argv[3] != '--existing' or sys.argv[6] != '--disposable-package-test' or
                 (os.environ.get('KURUMI_DISPOSABLE_PACKAGE_TEST') != '1' and not (os.name == 'nt' and os.environ.get('CI') == 'true'))):
    raise SystemExit('Existing-workspace tests require an explicitly disposable package CI environment.')
with tempfile.TemporaryDirectory(prefix="kurumi-frp-") as tmp, socketserver.ThreadingTCPServer(("127.0.0.1", 0), Echo) as echo:
    echo.daemon_threads = True
    threading.Thread(target=echo.serve_forever, daemon=True).start()
    used = {echo.server_address[1]}
    def unique_port():
        while True:
            p = port()
            if p not in used:
                used.add(p)
                return p
    http_port, bind_port, remote_port, visitor_port = unique_port(), unique_port(), unique_port(), unique_port()
    base = 'http://' + sys.argv[4] if existing else f"http://127.0.0.1:{http_port}"
    data_dir = Path(sys.argv[5]) if existing else Path(tmp)
    # frpc reads lowercase http_proxy directly and does not apply no_proxy.
    # This disposable integration fixture connects only to our loopback frps;
    # keep the runner's outbound HTTP proxy out of that local test connection.
    local_env = os.environ.copy()
    local_env.pop("http_proxy", None)
    manager = None if existing else subprocess.Popen([binary, "--data", tmp, "--frp-dir", frp, "--listen", f"127.0.0.1:{http_port}"], stdout=subprocess.DEVNULL, env=local_env)
    try:
        for _ in range(100):
            try:
                urllib.request.urlopen(base, timeout=1).close()
                break
            except OSError:
                if manager is not None and manager.poll() is not None:
                    raise RuntimeError("manager exited")
                time.sleep(.1)
        token = (data_dir / "access.token").read_text()
        def api(path, body=None):
            request = urllib.request.Request(base + "/api/" + path,
                data=json.dumps(body).encode() if body is not None else None,
                headers={"Authorization": "Bearer " + token, "Content-Type": "application/json"})
            try:
                with urllib.request.urlopen(request, timeout=30) as response:
                    return json.load(response)
            except urllib.error.HTTPError as e:
                raise RuntimeError(f"API {path}: {e.code} {e.read().decode()}") from None
        def save(kind, value):
            return api("save", {"revision": api("state")["revision"], "kind": kind, "value": value})
        def control(id, action):
            return api("control", {"revision": api("state")["revision"], "id": id, "action": action})
        if existing:
            status = api('core')
            assert status.get('bundledInUse'), 'packaged core is not selected'
            initial = api('state')['state']
            assert not initial['instances'] and not initial['tunnels'], 'existing test workspace must be empty'
            packaged = Path(binary).parent / 'bundled' / (Path(__file__).resolve().parents[1] / 'VERSION').read_text().strip()
            suffix = '.exe' if os.name == 'nt' else ''
            for kind in ('frpc', 'frps'):
                version = subprocess.check_output([str(packaged / (kind + suffix)), '-v'], text=True, timeout=10).strip()
                assert version.lstrip('v') == status['bundled']['tag'].lstrip('v'), (kind, version, status['bundled']['tag'])
        if not api("state")["binaries"]["frpc"]:
            installed = api("core")["installed"]
            if len(installed) != 1:
                raise RuntimeError("select a single installed core for the disposable workspace")
            api("core/activate", {"id": installed[0]["id"]})
        # Exercise real Go-template + TOML parsing, including quotes, slashes,
        # Unicode, equals signs, and template-looking data expanded only once.
        auth_secret = 'smoke-"auth"\\secret=猫<&>\'{{ .Envs.UNEXPECTED }}'
        pair_secret = 'pair-"secret"\\key=私密<&>\'{{ .Envs.UNEXPECTED }}'
        save("instance", {"name": "smoke-frps", "kind": "frps", "host": "127.0.0.1", "port": bind_port, "token": auth_secret})
        save("instance", {"name": "smoke-frpc", "kind": "frpc", "host": "127.0.0.1", "port": bind_port, "token": auth_secret})
        state = api("state")
        server_id, client_id = [i["id"] for i in state["state"]["instances"]]
        save("tunnel", {"instanceId": client_id, "name": "echo", "type": "tcp", "localHost": "127.0.0.1", "localPort": echo.server_address[1], "remotePort": remote_port, "enabled": True})
        save("tunnel", {"instanceId": client_id, "name": "private-echo", "type": "stcp", "localHost": "127.0.0.1", "localPort": echo.server_address[1], "secret": pair_secret, "enabled": True})
        save("instance", {"name": "smoke-visitor", "kind": "frpc", "host": "127.0.0.1", "port": bind_port, "token": auth_secret})
        visitor_id = api("state")["state"]["instances"][-1]["id"]
        save("tunnel", {"instanceId": visitor_id, "name": "visitor", "type": "stcp-visitor", "serverName": "private-echo", "localHost": "127.0.0.1", "localPort": visitor_port, "secret": pair_secret, "enabled": True})
        control(server_id, "start")
        control(client_id, "start")
        control(visitor_id, "start")
        current = api('state')['state']
        tcp_rule = next(t for t in current['tunnels'] if t['name'] == 'echo')
        visitor_rule = next(t for t in current['tunnels'] if t['name'] == 'visitor')
        for _ in range(100):
            report = api('registration')['tunnels']
            if report[tcp_rule['id']]['state'] == 'registered':
                break
            time.sleep(.1)
        else:
            raise AssertionError(f"registration not detected: {report}")
        assert report[visitor_rule['id']]['state'] == 'not-reported'
        health = api('health', {'revision': api('state')['revision'], 'id': client_id})
        row = next(r for r in health['rules'] if r['id'] == tcp_rule['id'])
        assert health['endpoint']['state'] == 'reachable' and row['registration']['state'] == 'registered' and row['target']['status'] == 'reachable'
        assert auth_secret not in json.dumps(health, ensure_ascii=False) and pair_secret not in json.dumps(health, ensure_ascii=False)
        tcp_rule['localPort'] = echo.server_address[1] + 1 if echo.server_address[1] < 65535 else echo.server_address[1] - 1
        save('tunnel', tcp_rule)
        assert api('registration')['tunnels'][tcp_rule['id']]['state'] == 'pending'
        health = api('health', {'revision': api('state')['revision'], 'id': client_id})
        assert next(r for r in health['rules'] if r['id'] == tcp_rule['id'])['registration']['state'] == 'pending'
        message = b"kurumi-real-frp-roundtrip"
        for target_port in [remote_port, visitor_port]:
            success = False
            for _ in range(120):
                try:
                    with socket.create_connection(("127.0.0.1", target_port), timeout=.5) as connection:
                        connection.sendall(message)
                        received = b""
                        while len(received) < len(message):
                            chunk = connection.recv(1000)
                            if not chunk:
                                break
                            received += chunk
                        if received == message:
                            success = True
                            break
                except OSError:
                    pass
                time.sleep(.1)
            if not success:
                for id in [server_id, client_id, visitor_id]:
                    print(json.dumps(api("logs?id=" + id), ensure_ascii=False))
                raise AssertionError(f"TCP/STCP roundtrip failed on {target_port}")
        for config in (data_dir / "runtime").glob("*.toml"):
            text = config.read_text()
            assert auth_secret not in text and pair_secret not in text
        control(visitor_id, "stop")
        control(client_id, "stop")
        control(server_id, "stop")
        assert all(v["state"] != "running" for v in api("state")["runtime"].values())
        if existing:
            state = api('state')['state']
            for kind, values in [('tunnel', state['tunnels']), ('instance', state['instances'])]:
                for value in values:
                    api('delete', {'revision': api('state')['revision'], 'kind': kind, 'id': value['id']})
        print("PASS: official frps/frpc verify, complex credentials, TCP/STCP byte roundtrips and stop.")
    finally:
        if manager is not None:
            manager.terminate()
            manager.wait(timeout=20)
        echo.shutdown()
