#!/usr/bin/env python3
"""Exercise real loopback frp traffic inside an isolated Actions runner."""
import concurrent.futures
import hashlib
import http.client
import http.server
import json
import os
import socket
import socketserver
import subprocess
import sys
import tempfile
import threading
import time
import urllib.error
import urllib.request


def free_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


class EchoTCP(socketserver.BaseRequestHandler):
    def handle(self):
        try:
            while True:
                data = self.request.recv(65536)
                if not data:
                    return
                self.request.sendall(data)
        except (ConnectionResetError, BrokenPipeError):
            # Restarting an frp instance intentionally interrupts active sockets.
            return


class EchoUDP(socketserver.BaseRequestHandler):
    def handle(self):
        data, sock = self.request
        sock.sendto(data, self.client_address)


class ThreadedTCP(socketserver.ThreadingTCPServer):
    allow_reuse_address = True
    daemon_threads = True


class ThreadedUDP(socketserver.ThreadingUDPServer):
    allow_reuse_address = True
    daemon_threads = True


class HTTPHandler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        body = ("kurumi-loopback:" + self.path).encode()
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *_):
        pass


def wait_until(label, func, timeout=45):
    deadline = time.monotonic() + timeout
    last = None
    while time.monotonic() < deadline:
        try:
            last = func()
            if last:
                return last
        except (AssertionError, OSError, ValueError, KeyError, urllib.error.URLError) as exc:
            last = str(exc)
        time.sleep(0.2)
    raise AssertionError(f"{label} did not become ready; last observation: {last}")


def main():
    if os.environ.get("GITHUB_ACTIONS") != "true" or os.environ.get("RUNNER_ENVIRONMENT") != "github-hosted":
        raise SystemExit("This temporary stress run is restricted to GitHub-hosted runners")
    binary = os.environ["KURUMI_BINARY"]
    temp = tempfile.TemporaryDirectory(prefix="kurumi-traffic-")
    out_path = os.path.join(temp.name, "manager.log")
    servers = [
        ThreadedTCP(("127.0.0.1", 0), EchoTCP),
        ThreadedUDP(("127.0.0.1", 0), EchoUDP),
        http.server.ThreadingHTTPServer(("127.0.0.1", 0), HTTPHandler),
    ]
    for server in servers:
        threading.Thread(target=server.serve_forever, daemon=True).start()
    reserved = {server.server_address[1] for server in servers}
    ports = []
    while len(ports) < 5:
        port = free_port()
        if port not in reserved:
            ports.append(port)
            reserved.add(port)
    manager_port, server_port, proxy_tcp, proxy_udp, proxy_http = ports
    base = f"http://127.0.0.1:{manager_port}"
    cookie = ""
    proc = None
    log = open(out_path, "w+", encoding="utf-8")

    def request(path, body=None, expected=200):
        nonlocal cookie
        payload = None if body is None else json.dumps(body).encode()
        headers = {"Content-Type": "application/json"} if payload is not None else {}
        if cookie:
            headers["Cookie"] = cookie
        req = urllib.request.Request(base + "/api/" + path, data=payload, headers=headers)
        try:
            response = urllib.request.urlopen(req, timeout=45)
        except urllib.error.HTTPError as exc:
            response = exc
        with response:
            raw = response.read()
            if response.status != expected:
                raise AssertionError(f"{path}: HTTP {response.status}, expected {expected}: {raw[:1000]!r}")
            new_cookie = response.headers.get("Set-Cookie")
            if new_cookie:
                cookie = new_cookie.split(";", 1)[0]
            return json.loads(raw) if raw else None

    def state():
        return request("state")

    def save(kind, value):
        current = state()
        request("save", {"revision": current["revision"], "kind": kind, "value": value})
        items = state()["state"]["instances" if kind == "instance" else "tunnels"]
        return next(item for item in items if item["name"] == value["name"])

    def control(item, action):
        snapshot = state()
        body = {"revision": snapshot["revision"], "id": item["id"], "action": action}
        run_id = snapshot["runtime"][item["id"]].get("runId", "")
        if action in ("stop", "restart") and run_id:
            body["expectedRunId"] = run_id
        request("control", body)
        return run_id

    def running(item):
        runtime = state()["runtime"][item["id"]]
        return runtime if runtime["state"] == "running" else None

    def exchange_tcp(seed):
        with socket.create_connection(("127.0.0.1", proxy_tcp), timeout=10) as sock:
            sock.settimeout(10)
            for iteration in range(8):
                payload = hashlib.sha256(f"{seed}:{iteration}".encode()).digest() * (1 + iteration * 17)
                sock.sendall(payload)
                received = bytearray()
                while len(received) < len(payload):
                    block = sock.recv(len(payload) - len(received))
                    if not block:
                        raise ConnectionResetError("TCP proxy closed before returning the payload")
                    received.extend(block)
                if received != payload:
                    raise AssertionError("TCP proxy changed the payload")

    def exchange_udp(seed):
        payload = hashlib.sha256(f"udp:{seed}".encode()).digest() * 4
        with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as sock:
            sock.settimeout(3)
            for _ in range(3):
                sock.sendto(payload, ("127.0.0.1", proxy_udp))
                try:
                    result, _ = sock.recvfrom(4096)
                    if result != payload:
                        raise AssertionError("UDP proxy changed the payload")
                    return True
                except socket.timeout:
                    continue
        raise AssertionError("UDP proxy did not reply after retries")

    def exchange_http(index):
        conn = http.client.HTTPConnection("127.0.0.1", proxy_http, timeout=10)
        path = f"/probe/{index}"
        try:
            conn.request("GET", path, headers={"Host": "kurumi.test"})
            response = conn.getresponse()
            body = response.read()
            if response.status != 200 or body != ("kurumi-loopback:" + path).encode():
                raise AssertionError(f"HTTP proxy returned status={response.status} body={body[:200]!r}")
        finally:
            conn.close()

    try:
        env = dict(os.environ, GORACE="halt_on_error=1 exitcode=66")
        proc = subprocess.Popen([binary, "--data", os.path.join(temp.name, "data"), "--listen", f"127.0.0.1:{manager_port}"], stdout=log, stderr=subprocess.STDOUT, env=env)
        wait_until("manager", lambda: urllib.request.urlopen(base + "/", timeout=2).status == 200)
        request("login", {"password": ""})
        server = save("instance", {"name": "CI server", "kind": "frps", "host": "127.0.0.1", "port": server_port, "httpPort": proxy_http, "proxyBindAddr": "127.0.0.1", "token": "isolated-ci-token"})
        client = save("instance", {"name": "CI client", "kind": "frpc", "host": "127.0.0.1", "port": server_port, "token": "isolated-ci-token"})
        for rule in [
            {"name": "ci-tcp", "type": "tcp", "localPort": servers[0].server_address[1], "remotePort": proxy_tcp},
            {"name": "ci-udp", "type": "udp", "localPort": servers[1].server_address[1], "remotePort": proxy_udp},
            {"name": "ci-http", "type": "http", "localPort": servers[2].server_address[1], "domains": ["kurumi.test"]},
        ]:
            save("tunnel", {**rule, "instanceId": client["id"], "localHost": "127.0.0.1", "enabled": True})
        control(server, "start")
        wait_until("frps", lambda: running(server))
        control(client, "start")
        wait_until("frpc", lambda: running(client))

        def ready_tcp():
            exchange_tcp("ready")
            return True

        wait_until("TCP proxy", ready_tcp)
        wait_until("UDP proxy", lambda: exchange_udp("ready"))
        wait_until("HTTP proxy", lambda: (exchange_http("ready"), True)[1])
        with concurrent.futures.ThreadPoolExecutor(max_workers=32) as pool:
            list(pool.map(exchange_tcp, range(320)))
            list(pool.map(exchange_udp, range(128)))
            list(pool.map(exchange_http, range(320)))
        print("PASS: 320 concurrent TCP sessions, 128 UDP clients, 320 HTTP requests")

        stop_load = threading.Event()
        load_counts = [0, 0]
        load_lock = threading.Lock()

        def continuous_load(worker):
            while not stop_load.is_set():
                try:
                    exchange_tcp(f"continuous-{worker}")
                    outcome = 0
                except OSError:
                    # In-flight connections may be interrupted by an explicit restart.
                    outcome = 1
                    time.sleep(0.01)
                with load_lock:
                    load_counts[outcome] += 1

        with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
            workers = [pool.submit(continuous_load, number) for number in range(8)]
            try:
                for number in range(5):
                    old_run = control(client, "restart")
                    wait_until("frpc restart", lambda: (status if (status := running(client)) and status.get("runId") != old_run else None))
                    wait_until("TCP after client restart", ready_tcp, timeout=90)
                    wait_until("HTTP after client restart", lambda: (exchange_http(f"client-{number}"), True)[1], timeout=90)
                for number in range(3):
                    old_run = control(server, "restart")
                    wait_until("frps restart", lambda: (status if (status := running(server)) and status.get("runId") != old_run else None))
                    wait_until("TCP after server restart", ready_tcp, timeout=90)
                    wait_until("UDP after server restart", lambda: exchange_udp(f"server-{number}"), timeout=90)
                    wait_until("HTTP after server restart", lambda: (exchange_http(f"server-{number}"), True)[1], timeout=90)
            finally:
                stop_load.set()
                for worker in workers:
                    worker.result()
        if load_counts[0] == 0:
            raise AssertionError("no continuous TCP request succeeded during restart coverage")
        print(f"PASS: 5 client and 3 server restarts with recovered traffic; concurrent successes={load_counts[0]}, interrupted={load_counts[1]}")
        control(client, "stop")
        control(server, "stop")
        request("quit", {})
        proc.wait(timeout=60)
        if proc.returncode != 0:
            raise AssertionError(f"manager exited with {proc.returncode}")
        print("PASS: clean shutdown of the manager and embedded frp instances")
    finally:
        failed = sys.exc_info()[0] is not None
        if proc is not None and proc.poll() is None:
            proc.terminate()
            try:
                proc.wait(timeout=15)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait()
        log.flush()
        log.seek(0)
        contents = log.read()
        if proc is not None and (failed or proc.returncode != 0):
            print("--- runner-only manager log ---", file=sys.stderr)
            print(contents[-24000:], file=sys.stderr)
        log.close()
        for server in servers:
            server.shutdown()
            server.server_close()
        temp.cleanup()


if __name__ == "__main__":
    main()

