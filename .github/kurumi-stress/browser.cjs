'use strict';

// Intended for an ephemeral Ubuntu Actions runner, not a personal machine.
const assert = require('node:assert/strict');
const { spawn } = require('node:child_process');
const crypto = require('node:crypto');
const fs = require('node:fs/promises');
const net = require('node:net');
const os = require('node:os');
const path = require('node:path');
const { chromium } = require('playwright');

if (process.env.GITHUB_ACTIONS !== 'true' || process.env.RUNNER_ENVIRONMENT !== 'github-hosted') throw new Error('This check requires a disposable GitHub-hosted runner');
const binary = process.env.KURUMI_BROWSER_BINARY;
if (!binary || !path.isAbsolute(binary)) throw new Error('KURUMI_BROWSER_BINARY must be an absolute path');
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
function deferred() {
  let resolve, reject;
  const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
async function portServer() {
  const server = net.createServer(socket => socket.end());
  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolve);
  });
  return server;
}
async function freePort() {
  const server = await portServer();
  const port = server.address().port;
  await new Promise((resolve, reject) => server.close(error => error ? reject(error) : resolve()));
  return port;
}
async function waitForServer(origin, child) {
  const until = Date.now() + 30000;
  while (Date.now() < until) {
    if (child.exitCode !== null || child.signalCode !== null) throw new Error('Temporary manager exited during startup');
    try {
      const response = await fetch(origin + '/', { signal: AbortSignal.timeout(1000) });
      if (response.ok) return;
    } catch (_) { /* wait for listener */ }
    await sleep(150);
  }
  throw new Error('Temporary manager did not become ready');
}
async function state(page) {
  return page.evaluate(async () => {
    const response = await fetch('/api/state', { cache: 'no-store' });
    if (!response.ok) throw new Error(`state request returned HTTP ${response.status}`);
    return response.json();
  });
}
async function waitRuntime(page, id, desired, differentRunId = '') {
  await page.waitForFunction(async ({ id, desired, differentRunId }) => {
    try {
      const response = await fetch('/api/state', { cache: 'no-store' });
      if (!response.ok) return false;
      const value = (await response.json()).runtime?.[id];
      return value?.state === desired && (!differentRunId || (value.runId && value.runId !== differentRunId));
    } catch (_) { return false; }
  }, { id, desired, differentRunId }, { timeout: 45000, polling: 300 });
}
const card = (page, id) => page.locator(`[data-local-instance="${id}"]`);
const ruleCard = (page, id) => page.locator(`[data-rule="${id}"]`);
async function navigate(page, name) {
  await page.locator(`#nav [data-page="${name}"]`).click();
}
async function refresh(page) {
  const previous = await page.locator('#content .workspace-page').elementHandle();
  assert.ok(previous, 'workspace page is missing before refresh');
  const response = page.waitForResponse(r => r.url().endsWith('/api/state') && r.request().method() === 'GET');
  await page.locator('#refresh').click();
  assert.equal((await response).status(), 200, 'refresh failed');
  await page.waitForFunction(element => !element.isConnected, previous);
}
async function save(page) {
  const response = page.waitForResponse(r => r.url().endsWith('/api/save') && r.request().method() === 'POST');
  await page.locator('#editor button[type="submit"]').click();
  assert.equal((await response).status(), 200, 'save failed');
  await page.locator('#dialog').waitFor({ state: 'hidden' });
}
async function createInstance(page, name, kind, port, token) {
  await page.locator('[data-action="new-instance"]').click();
  const form = page.locator('#editor');
  await form.locator('[name="name"]').fill(name);
  await form.locator('[name="kind"]').selectOption(kind);
  await form.locator('[name="host"]').fill('127.0.0.1');
  await form.locator('[name="port"]').fill('0');
  await form.locator('[name="token"]').fill(token);
  assert.equal(await form.evaluate(element => element.checkValidity()), false, 'port zero should fail browser validation');
  await form.locator('[name="port"]').fill(String(port));
  await save(page);
  const result = page.locator('[data-local-instance]').filter({ has: page.locator('h2', { hasText: name }) });
  await result.waitFor({ state: 'visible' });
  return result.getAttribute('data-local-instance');
}
async function createRule(page, name, clientId, targetPort, targetKind, unixPath) {
  await page.locator('[data-action="new-tunnel"]').click();
  const form = page.locator('#editor');
  await form.locator('[name="name"]').fill(name);
  await form.locator('[name="instanceId"]').selectOption(clientId);
  await form.locator('[name="targetKind"]').selectOption(targetKind);
  await form.locator('[name="remotePort"]').fill('0');
  if (targetKind === 'network') {
    await form.locator('[name="localHost"]').fill('127.0.0.1');
    await form.locator('[name="localPort"]').fill(String(targetPort));
  } else if (targetKind === 'unix') {
    await form.locator('[name="unixSocketPath"]').fill(unixPath);
    await form.locator('[name="enabled"]').uncheck();
  } else {
    await form.locator('[name="pluginUser"]').fill('synthetic-user');
    await form.locator('[name="pluginPassword"]').fill('synthetic-browser-password');
    await form.locator('[name="enabled"]').uncheck();
  }
  await save(page);
  const result = page.locator('[data-rule]').filter({ has: page.locator('h2', { hasText: name }) });
  await result.waitFor({ state: 'visible' });
  return result.getAttribute('data-rule');
}
async function login(page) {
  await page.locator('#login').waitFor({ state: 'visible' });
  await page.locator('#login-password').fill('');
  await page.locator('#login-form button[type="submit"]').click();
  await page.locator('#app').waitFor({ state: 'visible', timeout: 15000 });
}
async function main() {
  const temp = await fs.mkdtemp(path.join(os.tmpdir(), 'kurumi-browser-'));
  await fs.chmod(temp, 0o700);
  const data = path.join(temp, 'data');
  await fs.mkdir(data, { mode: 0o700 });
  let child, browser, target, lateRelease;
  const failures = [];
  try {
    const port = await freePort();
    const origin = `http://127.0.0.1:${port}`;
    child = spawn(binary, ['--data', data, '--listen', `127.0.0.1:${port}`], {
      stdio: 'ignore',
      env: { PATH: process.env.PATH, LANG: 'C.UTF-8', HOME: temp, TMPDIR: temp,
        HTTP_PROXY: 'http://127.0.0.1:9', HTTPS_PROXY: 'http://127.0.0.1:9',
        http_proxy: 'http://127.0.0.1:9', https_proxy: 'http://127.0.0.1:9',
        NO_PROXY: '127.0.0.1,localhost,::1', no_proxy: '127.0.0.1,localhost,::1' }
    });
    child.on('error', () => failures.push('Temporary manager could not be started'));
    await waitForServer(origin, child);
    target = await portServer();
    const targetPort = target.address().port;
    const frpPort = await freePort();
    const token = crypto.randomBytes(24).toString('hex');
    browser = await chromium.launch({ headless: true, args: ['--disable-background-networking', '--disable-component-update'] });
    const context = await browser.newContext({ viewport: { width: 1440, height: 900 }, serviceWorkers: 'block' });
    await context.route('**/*', route => {
      const url = new URL(route.request().url());
      return url.origin === origin ? route.continue() : route.abort('blockedbyclient');
    });
    const page = await context.newPage();
    const configure = p => {
      p.on('dialog', dialog => void dialog.accept());
      p.on('pageerror', () => failures.push('Uncaught browser page error'));
    };
    configure(page);
    await page.goto(origin, { waitUntil: 'domcontentloaded' });
    await page.locator('#login').waitFor({ state: 'visible' });
    assert.equal(await page.evaluate(async () => (await fetch('/api/state')).status), 401, 'state must require login');
    await page.locator('#login-password').fill('wrong-synthetic-password');
    await page.locator('#login-form button[type="submit"]').click();
    await page.waitForFunction(() => !!document.querySelector('#login-error').textContent.trim());
    await login(page);
    console.log('Login and access checks passed');

    await navigate(page, 'instances');
    const serverId = await createInstance(page, 'browser_server', 'frps', frpPort, token);
    const clientId = await createInstance(page, 'browser_client', 'frpc', frpPort, token);
    await card(page, clientId).locator('[data-action="edit-instance"]').click();
    assert.equal(await page.locator('#editor [name="token"]').inputValue(), '', 'saved token was displayed');
    const editedClientName = 'browser_client_<b>literal</b>';
    await page.locator('#editor [name="name"]').fill(editedClientName);
    await save(page);
    assert.equal((await state(page)).state.instances.find(item => item.id === clientId)?.hasToken, true);
    assert.equal(await card(page, clientId).locator('h2').textContent(), editedClientName);
    assert.equal(await card(page, clientId).locator('h2 b').count(), 0, 'instance name was inserted as HTML');
    console.log('Instance creation and editing passed');

    await navigate(page, 'tunnels');
    const unixPath = path.join(temp, 'browser-target.sock');
    const tcpId = await createRule(page, 'rule_tcp', clientId, targetPort, 'network', unixPath);
    const unixId = await createRule(page, 'rule_unix', clientId, targetPort, 'unix', unixPath);
    const socksId = await createRule(page, 'rule_socks', clientId, targetPort, 'socks5', unixPath);
    await ruleCard(page, tcpId).locator('[data-action="edit-tunnel"]').click();
    await page.locator('#editor [name="name"]').fill('rule_tcp_edit');
    await save(page);
    await page.locator('[data-filter]').fill(unixPath);
    assert.equal(await ruleCard(page, unixId).isVisible(), true, 'Unix target search missed the displayed endpoint');
    assert.equal(await ruleCard(page, tcpId).isVisible(), false);
    await page.locator('[data-filter]').fill('SOCKS5');
    assert.equal(await ruleCard(page, socksId).isVisible(), true, 'SOCKS5 target search missed the displayed endpoint');
    assert.equal(await ruleCard(page, unixId).isVisible(), false);
    await page.locator('[data-filter]').fill('');
    console.log('Rules and endpoint search passed');

    await navigate(page, 'instances');
    // Subscribe before each click: the response can be faster than the click resolves.
    async function doControl(id, action) {
      const response = page.waitForResponse(r => r.url().endsWith('/api/control') && r.request().method() === 'POST');
      await card(page, id).locator(`[data-action="${action}"]`).click();
      assert.equal((await response).status(), 200, `control ${action} failed`);
    }
    await doControl(serverId, 'start');
    await waitRuntime(page, serverId, 'running');
    await refresh(page);
    await doControl(clientId, 'start');
    await waitRuntime(page, clientId, 'running');
    await page.waitForFunction(async id => {
      try {
        const response = await fetch('/api/registration', { cache: 'no-store' });
        return response.ok && (await response.json()).tunnels?.[id]?.state === 'registered';
      } catch (_) { return false; }
    }, tcpId, { timeout: 45000, polling: 500 });
    await refresh(page);
    await navigate(page, 'tunnels');
    await page.waitForFunction(id => document.querySelector(`[data-registration="${id}"]`)?.textContent.includes('已注册'), tcpId, { timeout: 15000 });
    await navigate(page, 'instances');
    const oldRun = (await state(page)).runtime[clientId].runId;
    assert.ok(oldRun, 'running client must have a run ID');
    await doControl(clientId, 'restart');
    await waitRuntime(page, clientId, 'running', oldRun);
    await refresh(page);
    const newRun = (await state(page)).runtime[clientId].runId;
    await page.waitForFunction(({ id, run }) => window.Kurumi.state()?.runtime?.[id]?.runId === run, { id: clientId, run: newRun });
    await doControl(clientId, 'stop');
    await page.waitForFunction(async id => {
      const r = await fetch('/api/state');
      if (!r.ok) return false;
      const s = (await r.json()).runtime?.[id]?.state;
      return s && !['running', 'starting', 'stopping'].includes(s);
    }, clientId, { timeout: 45000, polling: 300 });
    await refresh(page);
    console.log('Local runtime and registration checks passed');

    const other = await context.newPage();
    configure(other);
    await other.goto(origin, { waitUntil: 'domcontentloaded' });
    await other.locator('#app').waitFor({ state: 'visible' });
    await navigate(other, 'instances');
    await card(page, clientId).locator('[data-action="edit-instance"]').click();
    await page.locator('#editor [name="name"]').fill('stale_client_name');
    await card(other, serverId).locator('[data-action="edit-instance"]').click();
    await other.locator('#editor [name="name"]').fill('browser_server_edit');
    await save(other);
    await page.locator('#editor button[type="submit"]').click();
    await page.waitForFunction(() => !!document.querySelector('#form-error').textContent.trim());
    const afterConflict = await state(page);
    assert.equal(afterConflict.state.instances.find(item => item.id === clientId).name, editedClientName, 'stale editor overwrote client');
    assert.equal(afterConflict.state.instances.find(item => item.id === serverId).name, 'browser_server_edit');
    await page.locator('#cancel').click();
    await refresh(page);
    console.log('Stale revision check passed');

    await navigate(page, 'tunnels');
    await ruleCard(page, tcpId).locator('[data-action="edit-tunnel"]').click();
    await page.locator('#editor [name="name"]').fill('rule_tcp_after_500');
    await page.route('**/api/save', async route => {
      const original = await route.fetch();
      if (original.status() !== 200) failures.push(`Injected 500 prerequisite returned HTTP ${original.status()}`);
      await route.fulfill({ status: 500, contentType: 'application/json', body: '{"error":"injected after commit"}' });
    }, { times: 1 });
    const injected = page.waitForResponse(r => r.url().endsWith('/api/save') && r.status() === 500);
    await page.locator('#editor button[type="submit"]').click();
    await injected;
    await page.waitForFunction(() => document.querySelector('#form-error').textContent.includes('未确认'));
    assert.equal((await state(page)).state.tunnels.find(item => item.id === tcpId).name, 'rule_tcp_after_500', 'server-side commit was not observed');
    await page.locator('#cancel').click();
    await refresh(page);
    console.log('Post-commit error handling passed');

    await navigate(page, 'instances');
    await card(page, clientId).locator('[data-action="edit-instance"]').click();
    await page.locator('#editor [name="name"]').fill('browser_client_late');
    await page.evaluate(() => { document.querySelector('#toast').hidden = true; });
    const captured = deferred(); lateRelease = deferred();
    await page.route('**/api/save', async route => {
      try {
        const original = await route.fetch();
        captured.resolve(original.status());
        await lateRelease.promise;
        await route.fulfill({ response: original });
      } catch (error) { captured.reject(error); }
    }, { times: 1 });
    await page.locator('#editor button[type="submit"]').click();
    assert.equal(await Promise.race([captured.promise, sleep(15000).then(() => { throw new Error('Delayed save was not captured'); })]), 200);
    await other.locator('#logout').click();
    await other.locator('#login').waitFor({ state: 'visible' });
    await page.locator('#login').waitFor({ state: 'visible' });
    await login(other);
    await page.locator('#app').waitFor({ state: 'visible', timeout: 15000 });
    await page.evaluate(() => { document.querySelector('#toast').hidden = true; });
    const lateResponse = page.waitForResponse(r => r.url().endsWith('/api/save') && r.request().method() === 'POST');
    let lateRefreshes = 0;
    const countRefresh = request => { if (request.method() === 'GET' && request.url().endsWith('/api/state')) ++lateRefreshes; };
    page.on('request', countRefresh);
    lateRelease.resolve();
    await lateResponse;
    await sleep(300);
    page.off('request', countRefresh);
    const staleToast = await page.locator('#toast').evaluate(element => !element.hidden && element.textContent.includes('草稿已保存'));
    assert.equal(staleToast, false, 'old save displayed success in the new session');
    assert.equal(lateRefreshes, 0, 'old save triggered a refresh in the new session');
    assert.equal((await state(page)).state.instances.find(item => item.id === clientId).name, 'browser_client_late', 'held request did not actually commit');
    for (let round = 0; round < 5; round++) {
      await navigate(page, 'tunnels');
      await page.locator('[data-filter]').fill('SOCKS5');
      assert.equal(await ruleCard(page, socksId).isVisible(), true, `rule search failed on round ${round + 1}`);
      await navigate(page, 'instances');
      await page.locator('[data-instance-search]').fill('browser_client_late');
      assert.equal(await card(page, clientId).isVisible(), true, `instance search failed on round ${round + 1}`);
      await page.locator('[data-instance-search]').fill('');
      await refresh(page);
    }
    assert.deepEqual(failures, []);
    console.log('Cross-session delayed response and repeated navigation checks passed');
  } finally {
    lateRelease?.resolve();
    if (browser) await browser.close().catch(() => {});
    if (target) await new Promise(resolve => target.close(resolve));
    if (child && child.exitCode === null && child.signalCode === null) {
      child.kill('SIGTERM');
      await Promise.race([new Promise(resolve => child.once('exit', resolve)), sleep(5000)]);
      if (child.exitCode === null && child.signalCode === null) child.kill('SIGKILL');
    }
    await fs.rm(temp, { recursive: true, force: true });
  }
}

main().catch(error => { console.error(error.stack || String(error)); process.exitCode = 1; });

