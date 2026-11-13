#!/usr/bin/env python3
"""Exercise one disposable KurumiFRP instance on a GitHub-hosted runner."""

import concurrent.futures
import http.client
import http.cookies
import json
import os
import pathlib
import random
import signal
import socket
import subprocess
import sys
import tempfile
import threading
import time


NO_BODY = object()
TEST_PASSWORD = "kurumi-ci-only-password"
TEST_TOKEN = "kurumi-ci-only-frp-token"
STRESS_SEED = 20261114
HOST = "127.0.0.1"
ECHO_PAYLOAD = b"kurumi-loopback-probe"


def check(condition, message):
    if not condition:
        raise AssertionError(message)


def available_ports(count):
    sockets = []
    try:
        for _ in range(count):
            sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
            sock.bind((HOST, 0))
            sockets.append(sock)
        return [sock.getsockname()[1] for sock in sockets]
    finally:
        for sock in sockets:
            sock.close()


class API:
    def __init__(self, port):
        self.port = port
        self.cookie = ""

    def request(self, path, body=NO_BODY, *, authenticated=True, headers=None, raw=False):
        check(path.startswith("/api/") and not path.startswith("//"), "invalid test path")
        method = "GET" if body is NO_BODY else "POST"
        request_headers = dict(headers or {})
        if authenticated and self.cookie:
            request_headers["Cookie"] = self.cookie
        if body is NO_BODY:
            payload = None
        elif raw:
            payload = body
            request_headers.setdefault("Content-Type", "application/json")
        else:
            payload = json.dumps(body, ensure_ascii=False).encode("utf-8")
            request_headers.setdefault("Content-Type", "application/json")
        conn = http.client.HTTPConnection(HOST, self.port, timeout=15)
        try:
            conn.request(method, path, body=payload, headers=request_headers)
            response = conn.getresponse()
            data = response.read()
            response_headers = dict(response.getheaders())
            value = json.loads(data) if data else None
            return response.status, value, response_headers
        finally:
            conn.close()

    def expect(self, status, path, body=NO_BODY, **kwargs):
        actual, value, headers = self.request(path, body, **kwargs)
        check(actual == status, f"{path}: expected HTTP {status}, got {actual}")
        return value, headers

    def login(self, password):
        _, headers = self.expect(200, "/api/login", {"password": password}, authenticated=False)
        cookie = http.cookies.SimpleCookie()
        cookie.load(headers.get("Set-Cookie", ""))
        check("kurumi_session" in cookie, "successful login omitted session cookie")
        self.cookie = "kurumi_session=" + cookie["kurumi_session"].value

    def state(self):
        value, _ = self.expect(200, "/api/state")
        check(isinstance(value.get("revision"), int), "invalid state revision")
        return value

    def save(self, revision, kind, value):
        return self.request("/api/save", {"revision": revision, "kind": kind, "value": value})


class Workbench:
    def __init__(self, binary, data_dir, port, log_path):
        self.binary = binary
        self.data_dir = data_dir
        self.port = port
        self.log_path = log_path
        self.process = None
        self.log = None

    def start(self):
        self.log = open(self.log_path, "ab")
        # Keep runner credentials, proxy settings and the caller's environment out
        # of the product process. All test targets are explicit loopback IPs.
        env = {"PATH": "/usr/bin:/bin", "LANG": "C.UTF-8", "TMPDIR": str(self.data_dir.parent)}
        self.process = subprocess.Popen(
            [str(self.binary), "--data", str(self.data_dir), "--listen", f"{HOST}:{self.port}"],
            env=env, stdout=self.log, stderr=subprocess.STDOUT,
        )
        deadline = time.monotonic() + 45
        api = API(self.port)
        while time.monotonic() < deadline:
            if self.process.poll() is not None:
                raise AssertionError(f"workbench exited during startup: {self.process.returncode}")
            try:
                status, _, _ = api.request("/api/state", authenticated=False)
                if status == 401:
                    return api
            except (OSError, http.client.HTTPException):
                pass
            time.sleep(0.1)
        raise AssertionError("workbench did not become ready in 45 seconds")

    def stop(self, strict=True):
        process, self.process = self.process, None
        try:
            if process is None:
                return
            if process.poll() is None:
                process.send_signal(signal.SIGTERM)
            try:
                code = process.wait(timeout=30)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=10)
                if strict:
                    raise AssertionError("workbench did not shut down cleanly")
                return
            if strict:
                check(code == 0, f"workbench exited with status {code}")
        finally:
            if self.log is not None:
                self.log.close()
                self.log = None


class Echo:
    def __init__(self, prefix=b""):
        self.prefix = prefix
        self.listener = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        self.listener.bind((HOST, 0))
        self.listener.listen()
        self.listener.settimeout(0.2)
        self.port = self.listener.getsockname()[1]
        self.done = threading.Event()
        self.thread = threading.Thread(target=self.serve, daemon=True)

    def start(self):
        self.thread.start()

    def serve(self):
        while not self.done.is_set():
            try:
                conn, _ = self.listener.accept()
            except socket.timeout:
                continue
            except OSError:
                return
            with conn:
                conn.settimeout(2)
                try:
                    data = b""
                    while len(data) < len(ECHO_PAYLOAD):
                        chunk = conn.recv(len(ECHO_PAYLOAD) - len(data))
                        if not chunk:
                            break
                        data += chunk
                    if data:
                        conn.sendall(self.prefix + data)
                except OSError:
                    pass

    def close(self):
        self.done.set()
        self.listener.close()
        if self.thread.ident is not None:
            self.thread.join(timeout=3)


def item(snapshot, kind, object_id):
    rows = snapshot["state"][kind]
    matches = [row for row in rows if row["id"] == object_id]
    check(len(matches) == 1, f"expected exactly one {kind} item")
    return matches[0]


def create_instance(api, value):
    before = api.state()
    status, result, _ = api.save(before["revision"], "instance", value)
    check(status == 200 and result.get("ok") is True, f"create instance: HTTP {status}")
    after = api.state()
    matches = [row for row in after["state"]["instances"] if row["name"] == value["name"]]
    check(len(matches) == 1, "created instance not found")
    check(after["revision"] == before["revision"] + 1, "create instance revision mismatch")
    check(matches[0].get("hasToken") is True and not matches[0].get("token"), "instance token was not redacted")
    return matches[0]["id"]


def control(api, object_id, action, expected_run=""):
    snapshot = api.state()
    payload = {"revision": snapshot["revision"], "id": object_id, "action": action}
    if expected_run:
        payload["expectedRunId"] = expected_run
    value, _ = api.expect(200, "/api/control", payload)
    check(value.get("ok") is True, f"{action} was not acknowledged")


def wait_running(api, object_id, old_run=""):
    deadline = time.monotonic() + 35
    while time.monotonic() < deadline:
        runtime = api.state().get("runtime", {}).get(object_id, {})
        run_id = runtime.get("runId", "")
        if runtime.get("state") == "running" and run_id and run_id != old_run:
            return run_id
        if runtime.get("state") in ("exited", "autostart-failed"):
            raise AssertionError("instance exited before it became ready")
        time.sleep(0.2)
    raise AssertionError("instance did not become ready in 35 seconds")


def check_echo(port, prefix=b""):
    deadline = time.monotonic() + 35
    payload = ECHO_PAYLOAD
    expected = prefix + payload
    while time.monotonic() < deadline:
        try:
            with socket.create_connection((HOST, port), timeout=2) as conn:
                conn.settimeout(2)
                conn.sendall(payload)
                received = b""
                while len(received) < len(expected):
                    chunk = conn.recv(len(expected) - len(received))
                    if not chunk:
                        break
                    received += chunk
                if received == expected:
                    return
        except OSError:
            pass
        time.sleep(0.2)
    raise AssertionError("local TCP tunnel did not relay the echo payload")


def concurrent_revision_check(api, instance_id, round_index):
    initial = api.state()
    revision = initial["revision"]
    old_name = item(initial, "instances", instance_id)["name"]
    writers, readers = 8, 4
    barrier = threading.Barrier(writers + readers)

    def write(index):
        barrier.wait(timeout=10)
        name = f"stress-client-{round_index}-{index}"
        status, _, _ = api.save(revision, "instance", {"id": instance_id, "name": name})
        return status, name

    def read():
        barrier.wait(timeout=10)
        snapshot = api.state()
        return snapshot["revision"], item(snapshot, "instances", instance_id)["name"]

    with concurrent.futures.ThreadPoolExecutor(max_workers=writers + readers) as pool:
        writes = [pool.submit(write, i) for i in range(writers)]
        reads = [pool.submit(read) for _ in range(readers)]
        writes = [future.result(timeout=30) for future in writes]
        reads = [future.result(timeout=30) for future in reads]
    check(sorted(status for status, _ in writes) == [200] + [409] * (writers - 1), "concurrent saves did not produce one winner")
    winner = next(name for status, name in writes if status == 200)
    final = api.state()
    check(final["revision"] == revision + 1 and item(final, "instances", instance_id)["name"] == winner, "winning save was not persisted")
    for seen_revision, seen_name in reads:
        check((seen_revision, seen_name) in ((revision, old_name), (revision + 1, winner)), "reader observed an inconsistent snapshot")
    return final


def boundary_and_batch_check(api, instance_id):
    rng = random.Random(STRESS_SEED)
    cases = [("tcp", 1, 0, 0), ("udp", 65535, 65535, 10_000_000),
             ("tcp", 65535, 1, 1), ("udp", 1, 65535, 0)]
    for _ in range(12):
        cases.append((rng.choice(("tcp", "udp")), rng.randint(1, 65535),
                      rng.randint(0, 65535), rng.choice((0, 1, 1024))))

    revision = api.state()["revision"]
    expected = {}
    for index, (kind, local_port, remote_port, bandwidth) in enumerate(cases):
        name = "x" * 64 if index == 0 else f"seeded-{index:02d}"
        value = {"instanceId": instance_id, "name": name, "type": kind, "localHost": HOST,
                 "localPort": local_port, "remotePort": remote_port, "bandwidthKB": bandwidth,
                 "enabled": False}
        status, result, _ = api.save(revision, "tunnel", value)
        check(status == 200 and result.get("revision") == revision + 1,
              f"seed {STRESS_SEED} valid case {index}: HTTP {status} or invalid revision")
        revision += 1
        expected[name] = value

    snapshot = api.state()
    rows = {row["name"]: row for row in snapshot["state"]["tunnels"] if row["instanceId"] == instance_id}
    check(snapshot["revision"] == revision, "seeded saves produced an unexpected revision")
    for name, wanted in expected.items():
        row = rows.get(name)
        check(row is not None, f"seeded rule missing: {name}")
        for key in ("type", "localHost", "localPort", "remotePort", "bandwidthKB", "enabled"):
            check(row.get(key) == wanted[key], f"seeded rule changed field {key}: {name}")

    base = {"instanceId": instance_id, "name": "invalid", "type": "tcp", "localHost": HOST,
            "localPort": 8080, "remotePort": 0, "enabled": False}
    invalid = [{"localPort": 0}, {"localPort": 65536}, {"remotePort": -1},
               {"remotePort": 65536}, {"remotePort": None}, {"name": "y" * 65},
               {"type": "unsupported"}, {"bandwidthKB": -1}, {"bandwidthKB": 10_000_001},
               {"name": "x" * 64}]
    for index, changes in enumerate(invalid):
        value = {**base, **changes}
        status, _, _ = api.save(revision, "tunnel", value)
        check(status == 400, f"seed {STRESS_SEED} invalid case {index}: HTTP {status}")
        after = api.state()
        check(after["revision"] == revision and after["state"] == snapshot["state"],
              f"invalid case {index} changed the stored configuration")

    batch = {"revision": revision, "name": "bulk-ci", "instanceId": instance_id,
             "protocol": "tcp+udp", "localHost": HOST, "localPorts": "40000-40003",
             "remotePorts": "41000-41003", "enabled": False}
    result, _ = api.expect(200, "/api/tunnels/batch", batch)
    check(result.get("created") == 8 and result.get("revision") == revision + 1,
          "batch creation count or revision mismatch")
    snapshot = api.state()
    revision = snapshot["revision"]
    bulk = [row for row in snapshot["state"]["tunnels"]
            if row["instanceId"] == instance_id and row["name"].startswith("bulk-ci-")]
    pairs = {(row["type"], row["localPort"], row["remotePort"], row["enabled"]) for row in bulk}
    wanted_pairs = {(kind, local_port, local_port + 1000, False)
                    for kind in ("tcp", "udp") for local_port in range(40000, 40004)}
    check(len(bulk) == 8 and pairs == wanted_pairs, "batch port pairing changed")

    present_ids = {row["id"] for row in snapshot["state"]["tunnels"]}
    missing_id = next(f"{index:016x}" for index in range(600) if f"{index:016x}" not in present_ids)
    status, _, _ = api.request("/api/tunnels/batch-delete", {"revision": revision, "ids": [bulk[0]["id"], missing_id]})
    check(status == 400, "batch delete with a missing rule was not rejected")
    after = api.state()
    check(after["revision"] == revision and after["state"] == snapshot["state"],
          "failed batch delete changed the stored configuration")

    keep = next(row for row in bulk if row["type"] == "udp" and row["localPort"] == 40003)
    deleted = [row["id"] for row in bulk if row["id"] != keep["id"]]
    result, _ = api.expect(200, "/api/tunnels/batch-delete", {"revision": revision, "ids": deleted})
    check(result.get("changed") == 7 and result.get("revision") == revision + 1,
          "batch delete count or revision mismatch")
    snapshot = api.state()
    remaining = [row for row in snapshot["state"]["tunnels"] if row["id"] in {r["id"] for r in bulk}]
    check(len(remaining) == 1 and remaining[0]["id"] == keep["id"], "batch delete removed the wrong rules")

    # Seven valid new rows precede the retained conflicting name; the whole
    # create must still fail without a partial write.
    batch["revision"] = snapshot["revision"]
    status, _, _ = api.request("/api/tunnels/batch", batch)
    check(status == 400, "conflicting batch was not rejected")
    after = api.state()
    check(after["revision"] == snapshot["revision"] and after["state"] == snapshot["state"],
          "conflicting batch left a partial write")


def run(binary, temporary):
    echo = Echo()
    replacement_echo = Echo(b"replacement:")
    management_port, server_port, remote_port = available_ports(3)
    data_dir = temporary / "data"
    app = Workbench(binary, data_dir, management_port, temporary / "workbench.log")
    try:
        echo.start()
        replacement_echo.start()
        api = app.start()
        api.expect(401, "/api/state", authenticated=False)
        api.expect(400, "/api/login", b"{", authenticated=False, raw=True)
        api.expect(403, "/api/login", {"password": ""}, authenticated=False, headers={"Origin": "http://127.0.0.1:1"})
        api.expect(401, "/api/login", {"password": "incorrect-ci-password"}, authenticated=False)
        api.login("")

        server_id = create_instance(api, {"name": "ci-server", "kind": "frps", "host": HOST, "port": server_port,
                                          "token": TEST_TOKEN, "udpPacketSize": 4096, "serverBriefErrors": True})
        client_id = create_instance(api, {"name": "ci-client", "kind": "frpc", "host": HOST, "port": server_port,
                                          "token": TEST_TOKEN, "udpPacketSize": 4096, "natHoleStunServer": "127.0.0.1:9"})
        revision = api.state()["revision"]
        status, _, _ = api.save(revision, "tunnel", {"instanceId": client_id, "name": "ci-echo", "type": "tcp",
                                                       "localHost": HOST, "localPort": echo.port,
                                                       "remotePort": remote_port, "enabled": False})
        check(status == 200, f"create tunnel: HTTP {status}")
        snapshot = api.state()
        tunnel = next(row for row in snapshot["state"]["tunnels"] if row["name"] == "ci-echo")
        tunnel_id = tunnel["id"]
        status, _, _ = api.save(snapshot["revision"], "tunnel", {"id": tunnel_id, "enabled": True})
        check(status == 200, f"update tunnel: HTTP {status}")
        snapshot = api.state()
        tunnel = item(snapshot, "tunnels", tunnel_id)
        check(tunnel["enabled"] is True and tunnel["remotePort"] == remote_port, "tunnel update lost settings")
        config, _ = api.expect(200, "/api/config?id=" + client_id)
        check(f"remotePort = {remote_port}" in config.get("toml", ""), "rendered TCP remote port mismatch")
        check(TEST_TOKEN not in config.get("toml", ""), "configuration preview exposed test token")

        status, _, _ = api.save(snapshot["revision"], "tunnel", {"id": tunnel_id, "remotePort": 70000})
        check(status == 400 and api.state()["revision"] == snapshot["revision"], "invalid tunnel was not rejected atomically")
        status, _, _ = api.save(snapshot["revision"] - 1, "instance", {"id": client_id, "name": "stale-name"})
        check(status == 409 and api.state()["revision"] == snapshot["revision"], "stale save was not rejected atomically")
        for round_index in range(4):
            snapshot = concurrent_revision_check(api, client_id, round_index)
        check(item(snapshot, "instances", client_id).get("hasToken") is True, "editing cleared the saved token")

        copied, _ = api.expect(200, "/api/duplicate", {"revision": snapshot["revision"], "kind": "instance", "id": server_id})
        snapshot = api.state()
        duplicate = item(snapshot, "instances", copied["id"])
        check(duplicate.get("udpPacketSize") == 4096 and duplicate.get("serverBriefErrors") is True, "server copy lost settings")
        check(duplicate["port"] != server_port and not duplicate.get("autoStart"), "server copy did not reset runtime settings")
        copied, _ = api.expect(200, "/api/duplicate", {"revision": snapshot["revision"], "kind": "instance", "id": client_id, "includeTunnels": True})
        snapshot = api.state()
        duplicate = item(snapshot, "instances", copied["id"])
        rules = [row for row in snapshot["state"]["tunnels"] if row["instanceId"] == copied["id"]]
        check(duplicate.get("udpPacketSize") == 4096 and copied.get("rulesCopied") == 1, "client copy lost settings or rule")
        check(len(rules) == 1 and rules[0]["enabled"] is False, "copied rule should be disabled")
        boundary_and_batch_check(api, copied["id"])

        control(api, server_id, "start")
        server_run = wait_running(api, server_id)
        control(api, client_id, "start")
        first_run = wait_running(api, client_id)
        check_echo(remote_port)
        snapshot = api.state()
        status, _, _ = api.save(snapshot["revision"], "tunnel", {"id": tunnel_id, "localPort": replacement_echo.port})
        check(status == 200, f"retarget running rule: HTTP {status}")
        check(api.state()["runtime"][client_id].get("pending") is True, "changed running draft was not marked pending")
        check_echo(remote_port)
        control(api, client_id, "restart", first_run)
        second_run = wait_running(api, client_id, first_run)
        check_echo(remote_port, b"replacement:")
        status, _, _ = api.request("/api/control", {"revision": api.state()["revision"], "id": client_id,
                                                    "action": "stop", "expectedRunId": first_run})
        check(status == 409, "stale run ID did not reject stop")
        control(api, client_id, "stop", second_run)
        control(api, server_id, "stop", server_run)

        before_restart = api.state()
        # A 429 means the request was rejected before the password write; a
        # preceding state request can still be releasing its session gate.
        for _ in range(5):
            status, _, _ = api.request("/api/password", {"old": "", "new": TEST_PASSWORD})
            if status != 429:
                check(status == 200, f"password change: HTTP {status}")
                break
            time.sleep(0.2)
        else:
            raise AssertionError("password change remained busy")
        api.expect(401, "/api/state")
        api.expect(401, "/api/login", {"password": ""}, authenticated=False)
        api.login(TEST_PASSWORD)
        api.expect(200, "/api/logout", {})
        api.expect(401, "/api/state")
        api.login(TEST_PASSWORD)
        app.stop()
        api = app.start()
        api.expect(401, "/api/state", authenticated=False)
        api.login(TEST_PASSWORD)
        after_restart = api.state()
        check(after_restart["revision"] == before_restart["revision"], "revision changed across restart")
        check(after_restart["state"] == before_restart["state"], "configuration changed across restart")
        check(item(after_restart, "instances", client_id).get("hasToken") is True, "token missing after restart")
        for cycle in range(2):
            for index in range(3):
                snapshot = api.state()
                status, _, _ = api.save(snapshot["revision"], "instance",
                                        {"id": client_id, "name": f"persist-{cycle}-{index}"})
                check(status == 200, f"post-restart save {cycle}/{index}: HTTP {status}")
            expected = api.state()
            app.stop()
            api = app.start()
            api.login(TEST_PASSWORD)
            actual = api.state()
            check(actual["revision"] == expected["revision"] and actual["state"] == expected["state"],
                  f"configuration changed after persistence cycle {cycle}")
            check(item(actual, "instances", client_id).get("hasToken") is True,
                  f"token missing after persistence cycle {cycle}")
        print(f"PASS: auth, seed {STRESS_SEED}, boundaries, batch atomicity, concurrent revisions, TCP and persistence")
        app.stop()
    finally:
        app.stop(strict=False)
        echo.close()
        replacement_echo.close()


def main():
    check(os.environ.get("GITHUB_ACTIONS") == "true" and os.environ.get("RUNNER_ENVIRONMENT") == "github-hosted",
          "this script is restricted to a GitHub-hosted runner")
    check(len(sys.argv) == 2, "usage: runner_api_check.py /absolute/path/to/kurumi")
    binary = pathlib.Path(sys.argv[1])
    check(binary.is_absolute() and binary.is_file(), "expected an absolute path to the runner-built binary")
    runner_temp = os.environ.get("RUNNER_TEMP", "")
    check(runner_temp and pathlib.Path(runner_temp).is_absolute(), "RUNNER_TEMP must be an absolute path")
    with tempfile.TemporaryDirectory(prefix="kurumi-api-check-", dir=runner_temp) as path:
        run(binary, pathlib.Path(path))


if __name__ == "__main__":
    main()

