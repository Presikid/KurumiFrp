"""Real TCP roundtrip through official frps/frpc; uses disposable loopback ports.
Usage: python3 scripts/frp-smoke.py ./kurumi /path/to/frp-dir
This does not download or install software or touch an existing workspace.
"""
import json
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
    http_port, bind_port, remote_port = unique_port(), unique_port(), unique_port()
    base = f"http://127.0.0.1:{http_port}"
    manager = subprocess.Popen([binary, "--data", tmp, "--frp-dir", frp, "--listen", f"127.0.0.1:{http_port}"], stdout=subprocess.DEVNULL)
    try:
        for _ in range(100):
            try:
                urllib.request.urlopen(base, timeout=1).close()
                break
            except OSError:
                if manager.poll() is not None:
                    raise RuntimeError("manager exited")
                time.sleep(.1)
        token = (Path(tmp) / "access.token").read_text()
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
        save("instance", {"name": "smoke-frps", "kind": "frps", "host": "127.0.0.1", "port": bind_port, "token": "smoke-auth-secret-123"})
        save("instance", {"name": "smoke-frpc", "kind": "frpc", "host": "127.0.0.1", "port": bind_port, "token": "smoke-auth-secret-123"})
        state = api("state")
        server_id, client_id = [i["id"] for i in state["state"]["instances"]]
        save("tunnel", {"instanceId": client_id, "name": "echo", "type": "tcp", "localHost": "127.0.0.1", "localPort": echo.server_address[1], "remotePort": remote_port, "enabled": True})
        control(server_id, "start")
        control(client_id, "start")
        message = b"kurumi-real-frp-roundtrip"
        success = False
        for _ in range(120):
            try:
                with socket.create_connection(("127.0.0.1", remote_port), timeout=.5) as connection:
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
            for id in [server_id, client_id]:
                print(json.dumps(api("logs?id=" + id), ensure_ascii=False))
            raise AssertionError("TCP roundtrip failed")
        control(client_id, "stop")
        control(server_id, "stop")
        assert all(v["state"] != "running" for v in api("state")["runtime"].values())
        print("PASS: official frps/frpc verify, start, TCP byte roundtrip and stop.")
    finally:
        manager.terminate()
        manager.wait(timeout=20)
        echo.shutdown()
