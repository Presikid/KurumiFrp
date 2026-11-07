"""Core page UI test: real local API, synthetic catalog; no binary download.
Use KURUMI_OFFLINE_UI=1 only where browser network policy blocks localhost.
"""
import http.cookiejar
import json
import os
from pathlib import Path
import re
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from playwright.sync_api import sync_playwright, expect

binary=str(Path(sys.argv[1]).resolve())
web=Path(__file__).resolve().parents[1]/'web'
shots=Path(os.environ.get('KURUMI_SCREENSHOTS',tempfile.mkdtemp(prefix='core-shots-')));shots.mkdir(parents=True,exist_ok=True)
with tempfile.TemporaryDirectory(prefix='core-ui-') as tmp:
    root=Path(tmp)
    with socket.socket() as s:s.bind(('127.0.0.1',0));port=s.getsockname()[1]
    base=f'http://127.0.0.1:{port}'
    server=subprocess.Popen([binary,'--data',tmp,'--frp-dir',str(root/'cores'),'--listen',f'127.0.0.1:{port}'],stdout=subprocess.DEVNULL)
    jar=http.cookiejar.CookieJar()
    opener=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar),urllib.request.ProxyHandler({}))
    try:
        for _ in range(100):
            try:opener.open(base,timeout=1).close();break
            except OSError:time.sleep(.1)
        token=(root/'access.token').read_text()
        host='linux_amd64'
        release={'tag_name':'v0.72.0','assets':[{'name':'frp_0.72.0_linux_amd64.tar.gz','platform':host,'compatible':True,'installable':True,'size':14564595,'digest':'sha256:'+'a'*64,'browser_download_url':'https://github.com/fatedier/frp/releases/download/v0.72.0/frp_0.72.0_linux_amd64.tar.gz'},{'name':'frp_0.72.0_windows_amd64.zip','platform':'windows_amd64','compatible':False,'installable':False,'size':10000000,'browser_download_url':'https://github.com/fatedier/frp/releases/download/v0.72.0/frp_0.72.0_windows_amd64.zip'}]}
        def fixture(path):
            if path=='/api/core/releases':return [{'tag_name':'v0.72.0'}]
            if path.startswith('/api/core/release?'):return release
            return None
        def bridge(path,options):
            value=fixture(path)
            if value is not None:return {'status':200,'body':json.dumps(value)}
            if not path.startswith('/api/'):raise ValueError('unexpected URL')
            req=urllib.request.Request(base+path,data=options.get('body','').encode() if options.get('body') else None,headers=options.get('headers',{}),method=options.get('method','GET'))
            try:r=opener.open(req,timeout=10)
            except urllib.error.HTTPError as e:r=e
            return {'status':r.status,'body':r.read().decode()}
        with sync_playwright() as p:
            args={'headless':True,'args':['--no-sandbox']}
            if len(sys.argv)>2:args['executable_path']=sys.argv[2]
            browser=p.chromium.launch(**args);page=browser.new_page(viewport={'width':1440,'height':1000})
            errors=[];page.on('pageerror',lambda e:errors.append(str(e)))
            offline=os.environ.get('KURUMI_OFFLINE_UI')=='1'
            if offline:
                page.expose_function('testFetch',bridge)
                html=re.sub(r'<link[^>]*>|<script[^>]*></script>','',(web/'index.html').read_text())
                page.set_content(html)
                for f in ['style.css','core.css']:page.add_style_tag(content=(web/f).read_text())
                page.add_script_tag(content="window.fetch=async(p,o={})=>{const r=await window.testFetch(p,o);return new Response(r.body,{status:r.status,headers:{'Content-Type':'application/json'}})}")
                for f in ['app.js','core.js']:page.add_script_tag(content=(web/f).read_text())
            else:
                def route(r):
                    value=fixture(r.request.url.removeprefix(base))
                    if value is not None:r.fulfill(status=200,content_type='application/json',body=json.dumps(value))
                    else:r.continue_()
                page.route('**/api/core/**',route);page.goto(base)
            page.locator('#login-form input').fill(token);page.locator('#login-form button').click();page.locator('#app').wait_for(state='visible')
            page.locator('[data-page=core]').click();page.locator('#core-version option[value="v0.72.0"]').wait_for(state='attached')
            expect(page.locator("#core-install")).to_be_enabled()
            assert page.locator('#core-asset').input_value().endswith('linux_amd64.tar.gz')
            page.screenshot(path=str(shots/'core-desktop.png'),full_page=True)
            page.locator('#core-asset').select_option('frp_0.72.0_windows_amd64.zip')
            assert page.locator('#core-install').is_disabled()
            assert 'windows_amd64.zip' in page.locator('#core-official').get_attribute('href')
            page.locator('#theme').click();page.screenshot(path=str(shots/'core-dark.png'),full_page=True)
            page.set_viewport_size({'width':390,'height':844});page.screenshot(path=str(shots/'core-mobile.png'),full_page=True)
            assert page.evaluate('() => document.documentElement.scrollWidth<=window.innerWidth')
            if not offline:
                # A shortcut must log into a NEW browser session without pasting a token.
                req=urllib.request.Request(base+'/api/browser-ticket',data=b'{}',headers={'Authorization':'Bearer '+token,'Content-Type':'application/json'})
                with opener.open(req,timeout=8) as r: launch_url=json.load(r)['url']
                context=browser.new_context();auto=context.new_page()
                auto.route('**/api/core/**',route)
                auto.on('pageerror',lambda e:errors.append(str(e)))
                auto.goto(launch_url)
                auto.locator('#app').wait_for(state='visible')
                auto.locator('#core-screen').wait_for(state='visible')
                assert auto.url==base+'/#core'
                assert token not in auto.url
                assert auto.request.get(base+'/api/state').status==200
                context.close()
                context=browser.new_context();replay=context.new_page();replay.goto(launch_url)
                expect(replay.locator("#login-error")).not_to_have_text("")
                assert replay.locator('#app').is_hidden()
                context.close()
            assert not errors,errors
            browser.close()
        print('PASS: core page, matching default, incompatible install disabled, official link, theme, mobile; catalog is a fixture.')
    finally:
        server.terminate();server.wait(timeout=20)
