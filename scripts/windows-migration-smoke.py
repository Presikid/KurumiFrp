"""Upgrade a disposable real Windows installation from the previous data layout."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import urllib.request

if os.name != "nt" or os.environ.get("CI") != "true":
    raise SystemExit("Only run on disposable Windows CI.")
new_setup = Path(sys.argv[1]).resolve()
app = Path(os.environ.get("ProgramW6432", os.environ["ProgramFiles"])) / "KurumiFRP"
old_root = Path(os.environ["ProgramData"]) / "KurumiFRP"
if app.exists() or old_root.exists():
    raise SystemExit("Pre-existing installation retained.")
url = "https://github.com/Presikid/KurumiFrp/releases/download/v0.7.1.5/KurumiFRP-0.7.1.5-Setup.exe"
expected = "b01f3342688e299853667697345279c0938a7aa21af842efc9125218b5d5b41c"
opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

def api(profile, path, body=None):
    address = (profile / "data/listen.addr").read_text(encoding="utf-8").strip()
    token = (profile / "data/access.token").read_text(encoding="utf-8")
    req = urllib.request.Request("http://" + address + "/api/" + path,
        data=None if body is None else json.dumps(body).encode(),
        headers={"Authorization": "Bearer " + token, "Content-Type": "application/json"})
    with opener.open(req, timeout=10) as response:
        return json.load(response)

def setup(path):
    subprocess.run([str(path), "/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART", "/TASKS="],
                   check=True, timeout=180)

with tempfile.TemporaryDirectory(prefix="kurumi-upgrade-") as tmp:
    old_setup = Path(tmp) / "old-setup.exe"
    req = urllib.request.Request(url, headers={"User-Agent": "KurumiFRP-CI"})
    with urllib.request.urlopen(req, timeout=120) as response:
        data = response.read((16 << 20) + 1)
    if len(data) > 16 << 20 or hashlib.sha256(data).hexdigest() != expected:
        raise RuntimeError("Previous installer digest mismatch")
    old_setup.write_bytes(data)
    exe = app / "kurumi.exe"
    try:
        setup(old_setup)
        paths = json.loads(subprocess.check_output([str(exe), "--paths"], text=True))
        old_profile = Path(paths["data"]).parent
        subprocess.run([str(exe), "--check-running"], check=True, timeout=30)
        token = (old_profile / "data/access.token").read_text(encoding="utf-8")
        before = json.loads((old_profile / "data/state.json").read_text(encoding="utf-8"))
        state = api(old_profile, "state")
        api(old_profile, "save", {"revision": state["revision"], "kind": "instance", "value": {
            "name": "migration-fixture", "kind": "frps", "host": "127.0.0.1", "port": 17070,
            "token": "migration-fixture-token", "httpPort": 0, "httpsPort": 0}})
        setup(new_setup)
        paths = json.loads(subprocess.check_output([str(exe), "--paths"], text=True))
        profile = Path(paths["data"]).parent
        subprocess.run([str(exe), "--check-running"], check=True, timeout=30)
        assert profile.is_relative_to(app / "runtime"), "profile was not moved into install directory"
        assert not old_root.exists(), "legacy root remains after migration"
        assert (profile / "data/access.token").read_text(encoding="utf-8") == token, "access token changed"
        assert json.loads((profile / "data/state.json").read_text(encoding="utf-8"))["key"] == before["key"], "state key changed"
        assert any(i["name"] == "migration-fixture" for i in api(profile, "state")["state"]["instances"])
        assert not (app / "licenses/ChineseSimplified-LICENSE.txt").exists()
        print("PASS: genuine 0.7.1.5 installation upgrades with configuration and credentials preserved")
    finally:
        uninstaller = app / "unins000.exe"
        if uninstaller.exists():
            subprocess.run([str(uninstaller), "/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART"],
                           check=True, timeout=180)
