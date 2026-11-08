"""Optional real-browser smoke test. Requires playwright and Chromium.
Run: python scripts/browser-smoke.py /path/to/kurumi [chromium-path]
No real frp process is launched. All data is disposable.
"""
import json
import http.cookiejar
import re
import urllib.error
import os
from pathlib import Path
import socket
import subprocess
import sys
import tempfile
import time
import urllib.request
from playwright.sync_api import sync_playwright, expect

binary = str(Path(sys.argv[1]).resolve())
shots = Path(os.environ.get("KURUMI_SCREENSHOTS", tempfile.mkdtemp(prefix="kurumi-shots-")))
shots.mkdir(parents=True, exist_ok=True)
with tempfile.TemporaryDirectory(prefix="kurumi-browser-") as directory:
    root = Path(directory)
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    base = f"http://127.0.0.1:{port}"
    server = subprocess.Popen([binary, "--data", str(root), "--frp-dir", str(root / "frp"), "--listen", f"127.0.0.1:{port}"], stdout=subprocess.DEVNULL)
    try:
        for _ in range(100):
            if server.poll() is not None:
                raise RuntimeError("server exited")
            try:
                urllib.request.urlopen(base, timeout=1).close()
                break
            except OSError:
                time.sleep(.1)
        token = (root / "access.token").read_text()
        with urllib.request.urlopen(base + "/kurumi.webp", timeout=5) as response:
            header = response.read(12)
            assert header[:4] == b"RIFF" and header[8:12] == b"WEBP", "theme image was not embedded and served"
        with urllib.request.urlopen(base + "/app.js", timeout=5) as response:
            asset_etag = response.headers.get("ETag")
            assert response.headers.get("Cache-Control") == "private, no-cache"
            assert asset_etag, "embedded script omitted ETag"
        conditional_asset = urllib.request.Request(base + "/app.js", headers={"If-None-Match": asset_etag})
        try:
            urllib.request.urlopen(conditional_asset, timeout=5)
            raise AssertionError("unchanged embedded asset did not return 304")
        except urllib.error.HTTPError as error:
            assert error.code == 304, f"conditional asset request returned {error.code}"
        with sync_playwright() as p:
            engine = os.environ.get("KURUMI_BROWSER", "chromium")
            if engine not in ("chromium", "firefox", "webkit"):
                raise ValueError("unsupported browser engine: " + engine)
            launch = {"headless": True}
            if engine == "chromium":
                launch["args"] = ["--no-sandbox"]
            if len(sys.argv) > 2:
                launch["executable_path"] = sys.argv[2]
            browser = getattr(p, engine).launch(**launch)
            page = browser.new_page(viewport={"width": 1440, "height": 1000}, device_scale_factor=1)
            errors = []
            page.on("pageerror", lambda e: errors.append(str(e)))
            offline = os.environ.get("KURUMI_OFFLINE_UI") == "1"
            jar = http.cookiejar.CookieJar()
            opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar), urllib.request.ProxyHandler({}))
            def bridge(path, options):
                if not path.startswith("/api/"):
                    raise ValueError("test bridge only supports local API")
                request = urllib.request.Request(base + path, data=options.get("body", "").encode() if options.get("body") else None, headers=options.get("headers", {}), method=options.get("method", "GET"))
                try:
                    response = opener.open(request, timeout=20)
                except urllib.error.HTTPError as error:
                    response = error
                return {"status": response.status, "body": response.read().decode(), "headers": dict(response.headers)}
            if offline:
                page.expose_function("testFetch", bridge)
            def load_page():
                if not offline:
                    page.goto(base)
                    return
                # Offline DOM integration harness. No browser network-policy changes.
                web = Path(__file__).resolve().parents[1] / "web"
                html = re.sub(r'<link[^>]*>|<script[^>]*></script>', "", (web / "index.html").read_text())
                page.set_content(html)
                page.add_style_tag(content=(web / "style.css").read_text())
                page.add_style_tag(content=(web / "core.css").read_text())
                page.add_script_tag(content="window.fetch = async (p, o={}) => { const r = await window.testFetch(p, o); return new Response(r.status === 304 ? null : r.body, {status:r.status,headers:r.headers}); };")
                page.add_script_tag(content=(web / "app.js").read_text())
                page.add_script_tag(content=(web / "core.js").read_text())
            load_page()
            page.screenshot(path=str(shots / "login.png"), full_page=True)
            page.set_viewport_size({"width": 390, "height": 844})
            assert page.locator('.login-art').is_visible(), 'mobile login lost its theme artwork'
            assert page.evaluate("() => document.documentElement.scrollWidth <= window.innerWidth")
            page.set_viewport_size({"width": 1440, "height": 1000})
            page.locator('#login-form input').fill(token)
            page.locator('#login-form button').click()
            page.locator('#app').wait_for(state="visible")
            page.locator('[data-action="new-instance"]').first.click()
            # Modal input should not trigger a full-viewport backdrop filter repaint.
            backdrop_filter = page.locator('#dialog').evaluate(
                "(dialog) => getComputedStyle(dialog, '::backdrop').getPropertyValue('backdrop-filter').trim()"
            )
            assert backdrop_filter in ("", "none"), f"modal backdrop unexpectedly applies a filter: {backdrop_filter}"
            name_input = page.locator('#editor input[name="name"]')
            name_input.press_sequentially("Tokyo entry", delay=5)
            assert name_input.input_value() == "Tokyo entry"
            page.locator('#editor input[name="host"]').fill("127.0.0.1")
            page.locator('#editor input[name="token"]').fill("test-passphrase-123")
            page.locator('#editor button[type="submit"]').click()
            page.locator('#dialog').wait_for(state="hidden")
            page.locator('[data-page="tunnels"]').click()
            page.locator('[data-action="new-tunnel"]').first.click()
            page.locator('#editor input[name="name"]').fill("desktop-rdp")
            page.locator('#editor input[name="localPort"]').fill("3389")
            page.locator('#editor input[name="remotePort"]').fill("13389")
            page.locator('#editor button[type="submit"]').click()
            page.locator('#dialog').wait_for(state="hidden")
            page.locator('[data-action="new-tunnel"]').first.click()
            page.locator('#editor input[name="name"]').fill("conflict-test")
            page.locator('#editor input[name="remotePort"]').fill("13389")
            page.locator('#editor button[type="submit"]').click()
            expect(page.locator("#form-error")).not_to_have_text("")
            page.locator('#cancel').click()
            page.locator('[data-action="edit-tunnel"]').first.click()
            page.locator('#editor input[name="localPort"]').fill("3390")
            page.locator('#editor button[type="submit"]').click()
            page.locator('#dialog').wait_for(state="hidden")
            polling_checks = page.evaluate((Path(__file__).with_name("polling-regression.js")).read_text(encoding="utf-8"), {"token": token})
            print("Polling regression passed: " + polling_checks)
            page.locator('[data-page="overview"]').click()
            page.screenshot(path=str(shots / "overview.png"), full_page=True)
            page.locator('[data-page="config"]').click()
            expect(page.locator("#config-output")).to_contain_text("redacted")
            assert "test-passphrase-123" not in page.locator('#config-output').text_content()
            expect(page.locator('#history-limit')).to_have_value('5')
            page.locator('#export-scope').select_option('config')
            with page.expect_download() as exported:
                page.locator('[data-action="export-data"]').click()
            config_file = str(exported.value.path())
            exported_json = json.loads(Path(exported.value.path()).read_text(encoding='utf-8'))
            assert 'state' in exported_json and exported_json['history'] == [] and exported_json['key']
            assert 'test-passphrase-123' not in json.dumps(exported_json)
            page.locator('#export-scope').select_option('snapshot')
            with page.expect_download() as exported:
                page.locator('[data-action="export-data"]').click()
            exported_json = json.loads(Path(exported.value.path()).read_text(encoding='utf-8'))
            assert 'state' in exported_json and exported_json['history'] == [] and exported_json['key']
            page.on('dialog', lambda dialog: dialog.accept())
            page.locator('#history-limit').fill('2')
            page.locator('[data-action="history-settings"]').click()
            expect(page.locator('#history-limit')).to_have_value('2')
            expect(page.locator('[data-action="restore"]')).to_have_count(2)
            page.locator('#import-file').set_input_files(config_file)
            page.locator('[data-action="import-data"]').click()
            expect(page.locator('#toast')).to_contain_text('导入成功')
            expect(page.locator('[data-action="restore"]')).to_have_count(0)
            page.locator('[data-page="instances"]').click()
            page.locator('[data-action="start"]').first.click()
            page.locator('#toast').wait_for(state="visible")
            page.screenshot(path=str(shots / "instances.png"), full_page=True)
            page.locator('#theme').click()
            page.screenshot(path=str(shots / "dark.png"), full_page=True)
            page.locator('#theme').click()
            page.set_viewport_size({"width": 390, "height": 844})
            page.locator('[data-page="overview"]').click()
            page.screenshot(path=str(shots / "mobile.png"), full_page=True)
            assert page.evaluate("() => document.documentElement.scrollWidth <= window.innerWidth")
            if not offline:
                page.reload()
            page.locator('#app').wait_for(state="visible")
            page.locator('[data-page="tunnels"]').click()
            assert page.locator('tbody tr').count() == 1
            assert not errors, errors
            browser.close()
            print(("Offline UI harness" if offline else "Browser smoke") + " passed: login, CRUD, conflicts, redaction, saved-state reads, responsive layout; no page errors.")
    finally:
        server.terminate()
        server.wait(timeout=15)
