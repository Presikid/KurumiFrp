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
    request_queue_size = 256


class ThreadedUDP(socketserver.ThreadingUDPServer):
    allow_reuse_address = True
    daemon_threads = True


class ThreadedUnix(socketserver.ThreadingUnixStreamServer):
    daemon_threads = True
    request_queue_size = 256


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
        ThreadedUnix(os.path.join(temp.name, "echo.sock"), EchoTCP),
    ]
    for server in servers:
        threading.Thread(target=server.serve_forever, daemon=True).start()
    reserved = {server.server_address[1] for server in servers if isinstance(server.server_address, tuple)}
    ports = []
    while len(ports) < 7:
        port = free_port()
        if port not in reserved:
            ports.append(port)
            reserved.add(port)
    manager_port, server_port, proxy_tcp, proxy_udp, proxy_http, proxy_unix, proxy_socks = ports
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

    def exchange_tcp(seed, port=proxy_tcp):
        with socket.create_connection(("127.0.0.1", port), timeout=10) as sock:
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

    def exchange_socks(seed, password=b"ci-socks-password"):
        def receive(sock, length):
            data = bytearray()
            while len(data) < length:
                chunk = sock.recv(length - len(data))
                if not chunk:
                    raise ConnectionResetError("SOCKS5 response ended early")
                data.extend(chunk)
            return bytes(data)

        user = b"ci-socks-user"
        with socket.create_connection(("127.0.0.1", proxy_socks), timeout=10) as sock:
            sock.settimeout(10)
            sock.sendall(b"\x05\x01\x02")
            if receive(sock, 2) != b"\x05\x02":
                raise AssertionError("SOCKS5 did not require username/password authentication")
            sock.sendall(b"\x01" + bytes([len(user)]) + user + bytes([len(password)]) + password)
            result = receive(sock, 2)
            if password != b"ci-socks-password":
                if result[0] != 1 or result[1] == 0:
                    raise AssertionError("SOCKS5 accepted an incorrect password")
                return True
            if result != b"\x01\x00":
                raise AssertionError("SOCKS5 rejected the configured credentials")
            sock.sendall(b"\x05\x01\x00\x01" + socket.inet_aton("127.0.0.1") + servers[0].server_address[1].to_bytes(2, "big"))
            reply = receive(sock, 4)
            if reply[:2] != b"\x05\x00":
                raise AssertionError(f"SOCKS5 connect failed: {reply!r}")
            length = {1: 4, 4: 16}.get(reply[3])
            if reply[3] == 3:
                length = receive(sock, 1)[0]
            if length is None:
                raise AssertionError("SOCKS5 returned an invalid address type")
            receive(sock, length + 2)
            payload = hashlib.sha256(f"socks:{seed}".encode()).digest() * 32
            sock.sendall(payload)
            if receive(sock, len(payload)) != payload:
                raise AssertionError("SOCKS5 proxy changed the payload")
            return True

    def direct_unix(seed):
        with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as sock:
            sock.settimeout(10)
            sock.connect(servers[3].server_address)
            for iteration in range(8):
                payload = hashlib.sha256(f"unix-{seed}:{iteration}".encode()).digest() * (1 + iteration * 17)
                sock.sendall(payload)
                received = bytearray()
                while len(received) < len(payload):
                    block = sock.recv(len(payload) - len(received))
                    if not block:
                        raise ConnectionResetError("direct Unix target closed early")
                    received.extend(block)
                if received != payload:
                    raise AssertionError("direct Unix target changed the payload")

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
            {"name": "ci-tcp", "type": "tcp", "localHost": "127.0.0.1", "localPort": servers[0].server_address[1], "remotePort": proxy_tcp},
            {"name": "ci-udp", "type": "udp", "localHost": "127.0.0.1", "localPort": servers[1].server_address[1], "remotePort": proxy_udp},
            {"name": "ci-http", "type": "http", "localHost": "127.0.0.1", "localPort": servers[2].server_address[1], "domains": ["kurumi.test"]},
            {"name": "ci-unix", "type": "tcp", "unixSocketPath": servers[3].server_address, "remotePort": proxy_unix},
            {"name": "ci-socks", "type": "tcp", "pluginType": "socks5", "pluginUser": "ci-socks-user", "pluginPassword": "ci-socks-password", "remotePort": proxy_socks},
        ]:
            save("tunnel", {**rule, "instanceId": client["id"], "enabled": True})
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
        wait_until("Unix socket proxy", lambda: (exchange_tcp("unix-ready", proxy_unix), True)[1])
        wait_until("SOCKS5 proxy", lambda: exchange_socks("ready"))
        exchange_socks("wrong-password", b"invalid-password")
        with concurrent.futures.ThreadPoolExecutor(max_workers=32) as pool:
            list(pool.map(direct_unix, range(128)))
            list(pool.map(exchange_tcp, range(320)))
            list(pool.map(exchange_udp, range(128)))
            list(pool.map(exchange_http, range(320)))
            list(pool.map(lambda n: exchange_tcp(f"unix-{n}", proxy_unix), range(128)))
            list(pool.map(exchange_socks, range(128)))
        print("PASS: 128 direct Unix controls, 320 TCP sessions, 128 UDP clients, 320 HTTP requests, 128 Unix and 128 authenticated SOCKS5 sessions")

        # Compare direct and proxied behavior against the same small accept
        # queue. This is diagnostic: an overloaded target can reject either.
        def measure_unix(use_proxy):
            gate = threading.Barrier(32)
            def one(index):
                gate.wait(timeout=20)
                try:
                    if use_proxy:
                        exchange_tcp(f"unix-{index}", proxy_unix)
                    else:
                        direct_unix(index)
                    return None
                except OSError as exc:
                    return f"{type(exc).__name__}: {exc}"
            with concurrent.futures.ThreadPoolExecutor(max_workers=32) as pool:
                return [failure for failure in pool.map(one, range(128)) if failure]

        servers[3].socket.listen(5)
        try:
            direct_failures = measure_unix(False)
            proxy_failures = measure_unix(True)
        finally:
            servers[3].socket.listen(256)
        print(f"Small Unix accept queue: direct failures={len(direct_failures)}, proxied failures={len(proxy_failures)}")
        if proxy_failures:
            logs = request("logs?id=" + client["id"])
            lines = [line for line in logs.get("lines", []) if "dial to uds" in line]
            print("Unix dial diagnostics: " + json.dumps(lines[-20:], ensure_ascii=False))
        if proxy_failures and not direct_failures:
            raise AssertionError(f"Unix proxy failed {len(proxy_failures)} sessions while the same direct load succeeded: {proxy_failures[:3]}")

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
                    wait_until("Unix socket after server restart", lambda: (exchange_tcp("unix-restart", proxy_unix), True)[1], timeout=90)
                    wait_until("SOCKS5 after server restart", lambda: exchange_socks("restart"), timeout=90)
            finally:
                stop_load.set()
                for worker in workers:
                    worker.result()
        if load_counts[0] == 0:
            raise AssertionError("no continuous TCP request succeeded during restart coverage")
        print(f"PASS: 5 client and 3 server restarts with recovered traffic; concurrent successes={load_counts[0]}, interrupted={load_counts[1]}")

        # Keep all protocols active together after restarts, without injecting
        # further failures. Any corruption or unexpected error is reported.
        soak_deadline = time.monotonic() + 60
        operations = {
            "tcp": lambda n: exchange_tcp(n),
            "udp": lambda n: exchange_udp(n),
            "http": lambda n: exchange_http(n),
            "unix": lambda n: exchange_tcp(n, proxy_unix),
            "socks": lambda n: exchange_socks(n),
        }
        totals = {name: 0 for name in operations}
        errors = []
        results_lock = threading.Lock()

        def soak(name, worker):
            count = 0
            while time.monotonic() < soak_deadline:
                try:
                    operations[name](f"soak-{worker}-{count}")
                    count += 1
                except Exception as exc:
                    with results_lock:
                        errors.append(f"{name}: {type(exc).__name__}: {exc}")
                    break
            with results_lock:
                totals[name] += count

        with concurrent.futures.ThreadPoolExecutor(max_workers=15) as pool:
            jobs = [pool.submit(soak, name, index) for name in operations for index in range(3)]
            for job in jobs:
                job.result()
        if errors or any(count == 0 for count in totals.values()):
            raise AssertionError(f"sustained traffic failed: counts={totals}, errors={errors[:10]}")
        print("PASS: 60-second mixed-protocol traffic: " + json.dumps(totals, sort_keys=True))
        control(client, "stop")
        control(server, "stop")
        request("quit", {})
        proc.wait(timeout=60)
        if proc.returncode != 0:
            raise AssertionError(f"manager exited with {proc.returncode}")
        print("PASS: clean shutdown of the manager and embedded frp instances")
    except Exception:
        # These are disposable runner-only instances containing synthetic data.
        for label in ("server", "client"):
            instance = locals().get(label)
            if isinstance(instance, dict) and instance.get("id"):
                try:
                    print(f"--- {label} instance logs ---", file=sys.stderr)
                    print(json.dumps(request("logs?id=" + instance["id"]), ensure_ascii=False)[-16000:], file=sys.stderr)
                except Exception as exc:
                    print(f"Unable to read {label} logs: {exc}", file=sys.stderr)
        raise
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

