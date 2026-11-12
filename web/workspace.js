'use strict';

const skinStylesheet = document.createElement('link');
skinStylesheet.rel = 'stylesheet';
skinStylesheet.href = '/theme-skins.css';
document.head.appendChild(skinStylesheet);

// Shared shell and request lifecycle for the new workspace pages.
// ProtocolEditor uses the same escaping function when it renders form fields.
const esc = (value = '') => String(value ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;', '<':'&lt;', '>':'&gt;', '"':'&quot;', "'":'&#39;'}[c]));

window.Kurumi = (() => {
 const pages = new Map();
 const names = {overview:'总览', tunnels:'规则', instances:'实例', nodes:'节点', 'reported-nodes':'主动上报', firewall:'访问控制', health:'链路诊断', config:'配置', core:'运行核心', logs:'日志', update:'软件更新', accounts:'账户', 'own-proxies':'我的代理'};
 const $ = selector => document.querySelector(selector);
 let snapshot = null, page = 'overview', session = 0, request = null, root = null, activePage = null;
 let timer = null, toastTimer = null, dialogSpec = null, dialogBusy = false;
 let transfer = null, transferRequest = null;
 let themeCatalogSession = -1, themeCatalogRequest = null;
 let channel = null, signal = '';
 const signalKey = 'kurumi-workspace-session';
 const transferKey = 'kurumi-workspace-transfer';

 try { if (typeof BroadcastChannel === 'function') channel = new BroadcastChannel(signalKey); } catch (_) {}
 try {
  const saved = JSON.parse(sessionStorage.getItem(transferKey) || 'null');
  if (saved && ['import','backup'].includes(saved.kind) && ['pending','uncertain','complete'].includes(saved.phase)) {
   transfer = {kind:saved.kind, phase:saved.phase === 'pending' ? 'uncertain' : saved.phase};
  }
 } catch (_) {}

 function persistTransfer() {
  try { if (transfer) sessionStorage.setItem(transferKey, JSON.stringify({kind:transfer.kind, phase:transfer.phase})); else sessionStorage.removeItem(transferKey); } catch (_) {}
  const notice = $('#transfer-notice');
  notice.hidden = !transfer;
  document.body.classList.toggle('has-transfer-notice', !!transfer);
  if (!transfer) return;
  const label = transfer.kind === 'backup' ? '备份恢复' : '配置导入';
  $('#transfer-message').textContent = transfer.phase === 'pending' ? `${label}已提交，正在等待结果。关闭页面不能保证取消服务端操作。` : transfer.phase === 'uncertain' ? `${label}的结果尚未确认；服务端可能仍在处理或已经更改。请刷新并核对配置后再决定是否重试。` : `${label}已完成，请核对当前配置。`;
  if (transfer.detail) $('#transfer-message').textContent += ` ${transfer.detail}`;
  $('#transfer-dismiss').hidden = transfer.phase === 'pending';
 }
 const transfers = {
  blocked() { return transferRequest ? '已有导入或恢复正在处理。' : transfer?.phase === 'uncertain' ? '请先核对上一项操作的结果并关闭提示。' : ''; },
  begin(kind) {
   const blocked = this.blocked();
   if (blocked) throw new Error(blocked);
   const operation = {kind, session};
   transferRequest = operation; transfer = {kind, phase:'pending'}; persistTransfer(); return operation;
  },
  finish(operation, phase, detail = '') {
   if (operation !== transferRequest) return;
   transferRequest = null;
   if (phase === 'rejected') { transfer = null; persistTransfer(); return; }
   transfer = {kind:operation.kind, phase, detail:operation.session === session ? detail : ''};
   persistTransfer();
   if (snapshot) void refresh().catch(() => {});
  }
 };

 function toast(message) {
  const element = $('#toast');
  element.textContent = String(message); element.hidden = false;
  clearTimeout(toastTimer); toastTimer = setTimeout(() => { element.hidden = true; }, 7000);
 }
 function dispose() {
  if (activePage?.dispose) { try { activePage.dispose(); } catch (_) {} }
  activePage = null; root = null;
 }
 function confirmDiscard() {
  return !root || !activePage?.hasUnsaved?.(root) || confirm('当前页面有尚未保存的修改，离开或刷新会丢弃这些修改。继续？');
 }
 function stopTimer() { clearTimeout(timer); timer = null; }
 function closeMenu(focus = false) {
  const opened = $('#app').classList.contains('menu-open');
  $('#app').classList.remove('menu-open'); document.body.classList.remove('navigation-open');
  $('#menu-toggle').setAttribute('aria-expanded', 'false');
  if (focus && opened) $('#menu-toggle').focus();
 }
 function showLogin() {
  ++session; stopTimer(); dispose(); snapshot = null; request = null;
  closeMenu();
  if ($('#dialog').open) $('#dialog').close();
  if ($('#diff-dialog').open) $('#diff-dialog').close();
  $('#content').replaceChildren(); $('#app').hidden = true; $('#login').hidden = false;
  const picker = $('#skin-picker'); if (picker) picker.hidden = true;
  $('#login-form').elements.token.value = '';
 }
 function announce() {
  signal = `${Date.now()}:${Math.random()}`;
  try { channel?.postMessage(signal); } catch (_) {}
  try { localStorage.setItem(signalKey, signal); } catch (_) {}
 }
 function receive(value) {
  if (typeof value !== 'string' || !value || value === signal) return;
  signal = value; showLogin(); void refresh().catch(() => {});
 }
 if (channel) channel.onmessage = event => receive(event.data);
 window.addEventListener('storage', event => { if (event.key === signalKey) receive(event.newValue); });

 async function api(path, body, options = {}) {
  const generation = session;
  const response = await fetch('/api/' + path, {
   method:body === undefined ? 'GET' : 'POST', credentials:'same-origin', cache:'no-store',
   headers:{...(body === undefined ? {} : {'Content-Type':'application/json'}), ...(options.headers || {})},
   body:body === undefined ? undefined : JSON.stringify(body), signal:options.signal
  });
  let result;
  try { result = await response.json(); }
  catch (_) {
   if (response.ok) throw new Error('响应格式无效，操作结果可能未确认，请刷新后核对。');
   result = {};
  }
  if (generation !== session && !(options.acceptLateSuccess && response.ok)) throw new DOMException('会话已变化', 'AbortError');
  if (!response.ok) {
   if (response.status === 401 && path !== 'login' && generation === session) showLogin();
   const error = new Error(result.error || `请求失败（HTTP ${response.status}）`); error.status = response.status; throw error;
  }
  return result;
 }
 function canViewOwnProxies() {
  const identity = snapshot?.identity;
  return Boolean(identity?.account && identity?.passwordAccount && (identity.role === 'admin' || identity.viewConfig));
 }
 function updateShell() {
  if (page === 'own-proxies' && !canViewOwnProxies()) {
   page = 'overview';
   if (location.hash === '#own-proxies') history.replaceState(null, '', location.pathname + location.search + '#overview');
  }
  $('#revision').textContent = `草稿 r${snapshot.revision}`;
  $('#sync-time').textContent = `更新于 ${new Date().toLocaleTimeString('zh-CN')}`;
  $('#page-title').textContent = names[page] || page;
  document.title = `${names[page] || page} · KurumiFRP`;
  const admin = snapshot.identity?.role === 'admin';
  document.querySelectorAll('#nav [data-page]').forEach(button => {
   const selected = button.dataset.page === page;
   button.classList.toggle('active', selected);
   if (selected) button.setAttribute('aria-current','page'); else button.removeAttribute('aria-current');
   button.hidden = !admin && ['reported-nodes','firewall','health','update','accounts'].includes(button.dataset.page);
   if (button.dataset.page === 'own-proxies') button.hidden = !canViewOwnProxies();
  });
  $('#quit-app').hidden = !admin; $('#change-password').hidden = false;
 }
 function render() {
  if (!snapshot) return;
  dispose(); updateShell();
  const component = pages.get(page);
  const content = $('#content');
  root = document.createElement('div'); root.className = 'workspace-page';
  content.replaceChildren(root);
  if (!component) { root.innerHTML = '<section class="panel"><div class="panel-content">页面不可用。</div></section>'; return; }
  activePage = component;
  root.innerHTML = component.render(snapshot);
  if (component.mount) component.mount(root, snapshot);
 }
 function schedule() {
  stopTimer();
  if (!snapshot || document.hidden) return;
  timer = setTimeout(() => { timer = null; void refresh(true).catch(() => {}); }, 10000);
 }
 async function refresh(silent = false, discardEdits = false) {
  const generation = session;
  if (request) {
   if (silent) return request;
   try { await request; } catch (_) {}
   if (generation !== session) return false;
   return refresh(false, discardEdits);
  }
  const previous = snapshot && JSON.stringify(snapshot);
  const scope = value => JSON.stringify([value?.identity || null, (value?.state?.instances || []).map(item => item.id).sort()]);
  const previousScope = snapshot && scope(snapshot);
  const previousIdentity = snapshot && JSON.stringify(snapshot.identity || null);
  const pending = (async () => {
   const controller = new AbortController(), deadline = setTimeout(() => controller.abort(),15000);
   let next;
   try { next = await api('state',undefined,{signal:controller.signal}); }
   catch (error) { if (controller.signal.aborted) throw new Error('读取状态超时，请重试。'); throw error; }
   finally { clearTimeout(deadline); }
   if (generation !== session) return false;
   if (!next || !Number.isSafeInteger(next.revision) || !Array.isArray(next.state?.instances) || !Array.isArray(next.state?.tunnels) || !next.runtime || typeof next.runtime !== 'object' || !next.identity || typeof next.identity.role !== 'string') throw new Error('工作台状态格式无效，请刷新后重试。');
   snapshot = next;
   $('#login').hidden = true; $('#app').hidden = false;
   updateShell();
   void updateThemePicker().catch(() => {});
   const changed = previous !== JSON.stringify(snapshot);
   const accessChanged = previousScope !== null && previousScope !== scope(snapshot);
   const identityChanged = previousIdentity !== null && previousIdentity !== JSON.stringify(snapshot.identity || null);
   const keepEdits = !discardEdits && !identityChanged && root && activePage?.hasUnsaved?.(root);
   if (accessChanged && !keepEdits) {
    if ($('#dialog').open) $('#dialog').close();
    if ($('#diff-dialog').open) $('#diff-dialog').close();
   }
   const active = document.activeElement;
   const busy = $('#dialog').open || $('#diff-dialog').open || (active && $('#content').contains(active)) || Boolean(window.getSelection()?.toString());
   if (!keepEdits && (accessChanged || !silent || (!busy && changed && ['overview','instances'].includes(page)))) render();
   return true;
  })();
  request = pending;
  try { return await pending; }
  finally { if (request === pending) request = null; if (generation === session) schedule(); }
 }
 function navigate(target) {
  if (!pages.has(target)) return;
  if (target === page && root?.isConnected) { closeMenu(); return; }
  if (!confirmDiscard()) {
   if (location.hash !== '#' + page) history.replaceState(null, '', location.pathname + location.search + '#' + page);
   return;
  }
  const menuWasOpen = $('#app').classList.contains('menu-open');
  page = target; closeMenu();
  if (location.hash !== '#' + target) history.replaceState(null, '', location.pathname + location.search + '#' + target);
  render();
  if (menuWasOpen) $('#content').focus();
 }
 function download(name, text, type = 'application/json') {
  const url = URL.createObjectURL(new Blob([text], {type}));
  const link = document.createElement('a'); link.href = url; link.download = name; document.body.appendChild(link); link.click(); link.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
 }
 function closeDialog() { if (!dialogBusy && $('#dialog').open) $('#dialog').close(); }
 function openDialog(spec) {
  if ($('#dialog').open || dialogBusy) return;
  dialogSpec = spec;
  $('#dialog-body').innerHTML = spec.html;
  $('#form-error').textContent = '';
  $('#editor button[type="submit"]').textContent = spec.submitLabel || '保存';
  $('#editor button[type="submit"]').disabled = false;
  $('#dialog').showModal();
  try { spec.onReady?.($('#editor')); }
  catch (error) { $('#dialog').close(); dialogSpec = null; throw error; }
 }
 function savedSkin() {
  try {
   const value = localStorage.getItem('kurumi-skin') || '';
   return value.length <= 32 && /^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(value) ? value : '';
  } catch (_) { return ''; }
 }
 function applySkin(id) {
  if (id && id !== 'default') document.body.dataset.kurumiSkin = id;
  else delete document.body.dataset.kurumiSkin;
  try { if (id && id !== 'default') localStorage.setItem('kurumi-skin', id); else localStorage.removeItem('kurumi-skin'); } catch (_) {}
 }
 async function updateThemePicker() {
  const active = document.body.dataset.kurumiSkin || savedSkin();
  if ((themeCatalogSession === session && !active) || themeCatalogRequest === session || !snapshot) return;
  const generation = session;
  themeCatalogRequest = generation;
  const controller = new AbortController(), deadline = setTimeout(() => controller.abort(), 15000);
  try {
   const result = await api('themes', undefined, {signal:controller.signal});
   if (generation !== session || !snapshot || !Array.isArray(result.themes)) return;
   themeCatalogSession = generation;
   $('#skin-picker').hidden = result.themes.length === 0;
   if (active && !result.themes.some(item => item.id === active && item.installed)) {
    applySkin('default');
    toast('当前主题不可用或素材缺失，已恢复默认主题。');
   }
  } finally {
   clearTimeout(deadline);
   if (themeCatalogRequest === generation) themeCatalogRequest = null;
  }
 }
 async function chooseSkin() {
  const generation = session, result = await api('themes');
  if (generation !== session || !snapshot || !Array.isArray(result.themes) || !result.themes.length) return;
  const admin = snapshot.identity?.role === 'admin';
  const themes = result.themes.filter(item => item && typeof item.id === 'string' && item.id.length <= 32 && /^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(item.id));
  const active = document.body.dataset.kurumiSkin || 'default';
  const current = themes.some(item => item.id === active && item.installed) ? active : 'default';
  const options = [`<option value="default"${current === 'default' ? ' selected' : ''}>默认主题（随程序提供）</option>`];
  for (const item of themes) {
   const disabled = !admin && !item.installed;
   const status = item.installed ? '已安装' : admin ? '选择后下载' : '需要管理员安装';
   options.push(`<option value="${esc(item.id)}"${current === item.id ? ' selected' : ''}${disabled ? ' disabled' : ''}>${esc(item.name)} · ${status}</option>`);
  }
  openDialog({
   html:`<h2>狂三主题</h2><p class="muted">主题只在选择时从 GitHub 下载；已安装的主题可离线使用，选择保存在当前浏览器。</p><div class="form-grid"><label>选择主题<select name="skin">${options.join('')}</select></label></div>`,
   submitLabel:'应用主题',
   async onSubmit(form) {
    const id = form.elements.skin.value;
    if (id !== 'default') {
     const selected = themes.find(item => item.id === id);
     if (!selected || (!selected.installed && !admin)) throw new Error('此主题尚不可用。');
     if (!selected.installed) {
      const submit = form.querySelector('[type="submit"]'), label = submit.textContent;
      submit.textContent = '正在下载主题…';
      try { await api('themes/install', {id}); }
      catch (error) {
       if (!error.status || error.status >= 500) throw new Error('主题安装结果尚未确认，请重新打开主题选择并检查。当前主题未更改。');
       throw error;
      } finally { submit.textContent = label; }
     }
    }
    applySkin(id); toast('主题已切换。');
   }
  });
 }
 function showDiff(value) {
  $('#diff-title').textContent = `${value.instanceName || '实例'} · 配置差异`;
  $('#diff-summary').textContent = `新增 ${value.addedCount || 0} 行 · 移除 ${value.removedCount || 0} 行${value.secretChanged ? ' · 认证凭据已变化' : ''}${value.orderChanged ? ' · 顺序变化' : ''}`;
  $('#diff-content').textContent = [...(value.removed || []).map(line => '- ' + line), ...(value.added || []).map(line => '+ ' + line)].join('\n') || '没有可显示的行变化';
  if (value.truncated) $('#diff-content').textContent += '\n差异显示已截断';
  $('#diff-dialog').showModal();
 }
 function bind() {
  persistTransfer();
  const ownProxiesButton = document.createElement('button');
  ownProxiesButton.type = 'button'; ownProxiesButton.dataset.page = 'own-proxies';
  ownProxiesButton.textContent = '我的代理'; ownProxiesButton.hidden = true;
  $('#nav').appendChild(ownProxiesButton);
  $('#transfer-dismiss').addEventListener('click', () => { if (!transferRequest) { transfer = null; persistTransfer(); } });
  $('#login-form').elements.token.required = false;
  $('#login-form').addEventListener('submit', async event => {
   event.preventDefault(); const form = event.currentTarget, button = form.querySelector('[type="submit"]');
   button.disabled = true; $('#login-error').textContent = '';
   // Invalidate an initial state request that may still be returning 401.
   ++session; request = null;
   try {
    const username = form.elements.username.value.trim(), password = form.elements.token.value;
    await api('login', username ? {username, password} : {password});
    form.elements.token.value = ''; ++session; announce(); await refresh();
   } catch (error) { $('#login-error').textContent = error.message; }
   finally { button.disabled = false; }
  });
  $('#logout').addEventListener('click', async () => { try { await api('logout', {}); showLogin(); announce(); } catch (error) { toast(error.message); } });
  $('#quit-app').addEventListener('click', async () => {
   if (!confirm('退出 KurumiFRP 并停止全部托管实例？现有连接会中断。')) return;
   try { await api('quit', {}); showLogin(); $('#login-error').textContent = '程序已退出。'; } catch (error) { toast(error.message); }
  });
  $('#change-password').addEventListener('click', () => openDialog({
   html:'<h2>修改登录密码</h2><p class="muted">修改后需要重新登录。主密码可以为空；本地账户密码须符合账户要求。</p><div class="form-grid"><label>当前密码<input name="old" type="password" autocomplete="current-password"></label><label>新密码<input name="new" type="password" autocomplete="new-password"></label><label>再次输入新密码<input name="confirm" type="password" autocomplete="new-password"></label></div>',
   submitLabel:'更新密码',
   async onSubmit(form) {
    if (form.elements.new.value !== form.elements.confirm.value) throw new Error('两次输入的新密码不一致。');
    try { await api('password', {old:form.elements.old.value, new:form.elements.new.value}); }
    catch (error) {
     if (error.status === 409 || !error.status || error.status >= 500) {
      showLogin(); announce(); $('#login-error').textContent = '密码状态或保存结果尚未确认，请重新登录后核对。'; return;
     }
     throw error;
    }
    showLogin(); announce(); $('#login-error').textContent = '密码已更新，请重新登录。';
   }
  }));
  $('#refresh').addEventListener('click', () => { if (confirmDiscard()) void refresh(false, true).catch(error => toast(error.message)); });
  applySkin(savedSkin());
  const picker = document.createElement('button');
  picker.id = 'skin-picker'; picker.type = 'button'; picker.className = 'icon-button'; picker.hidden = true;
  picker.title = '选择狂三主题'; picker.setAttribute('aria-label', '选择狂三主题'); picker.textContent = '✦';
  picker.addEventListener('click', () => { void chooseSkin().catch(error => toast(error.message)); });
  $('#theme').before(picker);
  try { document.body.classList.toggle('light', localStorage.getItem('kurumi-theme') === 'light'); } catch (_) {}
  $('#theme').addEventListener('click', () => {
   document.body.classList.toggle('light');
   try { localStorage.setItem('kurumi-theme', document.body.classList.contains('light') ? 'light' : 'dark'); } catch (_) {}
  });
  $('#nav').addEventListener('click', event => { const button = event.target.closest('[data-page]'); if (button) navigate(button.dataset.page); });
  $('#sidebar').addEventListener('click', event => { if (event.target.closest('a[href="#overview"]')) { event.preventDefault(); navigate('overview'); } });
  $('#menu-toggle').addEventListener('click', () => {
   const open = !$('#app').classList.contains('menu-open');
   $('#app').classList.toggle('menu-open', open); document.body.classList.toggle('navigation-open', open);
   $('#menu-toggle').setAttribute('aria-expanded', String(open));
   if (open) $('#menu-close').focus();
  });
  $('#menu-close').addEventListener('click', () => closeMenu(true));
  $('#nav-scrim').addEventListener('click', () => closeMenu(true));
  window.addEventListener('keydown', event => {
   if (!$('#app').classList.contains('menu-open')) return;
   if (event.key === 'Escape') { closeMenu(true); return; }
   if (event.key !== 'Tab') return;
   const focusable = [...$('#sidebar').querySelectorAll('a[href], button:not([disabled])')].filter(element => !element.hidden && element.getClientRects().length);
   if (!focusable.length) { event.preventDefault(); return; }
   const first = focusable[0], last = focusable[focusable.length-1], active = document.activeElement;
   if (event.shiftKey && (active === first || !$('#sidebar').contains(active))) { event.preventDefault(); last.focus(); }
   else if (!event.shiftKey && (active === last || !$('#sidebar').contains(active))) { event.preventDefault(); first.focus(); }
  });
  window.addEventListener('resize', () => { if (window.innerWidth > 760) closeMenu(); });
  window.addEventListener('hashchange', () => { const target = location.hash.slice(1); if (pages.has(target)) navigate(target); });
  document.addEventListener('visibilitychange', () => { if (document.hidden) stopTimer(); else if (snapshot) void refresh(true).catch(() => {}); });
  $('#cancel').addEventListener('click', closeDialog);
  $('#dialog').addEventListener('cancel', event => { if (dialogBusy) event.preventDefault(); });
  $('#dialog').addEventListener('close', () => { if (!$('#dialog').open) dialogSpec = null; });
  $('#diff-close').addEventListener('click', () => $('#diff-dialog').close());
  for (const type of ['click','change','input']) $('#editor').addEventListener(type, event => {
   const spec = dialogSpec, callback = spec?.['on' + type[0].toUpperCase() + type.slice(1)];
   if (!callback || dialogBusy) return;
   const showError = error => { if ($('#dialog').open && dialogSpec === spec) $('#form-error').textContent = error.message; else if (snapshot) toast(error.message); };
   try { Promise.resolve(callback(event, $('#editor'))).catch(showError); }
   catch (error) { showError(error); }
  });
  $('#editor').addEventListener('submit', async event => {
   event.preventDefault();
   if (!dialogSpec?.onSubmit || dialogBusy) return;
   const spec = dialogSpec, button = event.currentTarget.querySelector('[type="submit"]');
   dialogBusy = true; button.disabled = true; $('#cancel').disabled = true; $('#dialog-body').inert = true; $('#form-error').textContent = '';
   try {
    const result = await spec.onSubmit(event.currentTarget);
    dialogBusy = false;
    if (result !== false && $('#dialog').open && dialogSpec === spec) $('#dialog').close();
   } catch (error) { if ($('#dialog').open && dialogSpec === spec) $('#form-error').textContent = error.message; }
   finally { dialogBusy = false; button.disabled = false; $('#cancel').disabled = false; $('#dialog-body').inert = false; }
  });
  const hash = location.hash.slice(1);
  if (pages.has(hash)) page = hash;
  else if (hash.startsWith('launch=')) history.replaceState(null, '', location.pathname + location.search + '#overview');
  const initialSession = session;
  void refresh().catch(error => { if (session === initialSession && error.status !== 401) $('#login-error').textContent = error.message; });
 }
 let ownProxiesCleanup = null;
 pages.set('own-proxies', {
  render(value) {
   const instances = (value.state?.instances || []).filter(item => item?.kind === 'frps' && /^[a-f0-9]{16}$/.test(item.id));
   const options = instances.map(item => `<option value="${esc(item.id)}">${esc(item.name || 'frps')}</option>`).join('');
   return `<section class="panel"><div class="panel-content"><h2>我的代理</h2><p class="muted">显示采样窗口内在当前受管控制连接中观察到的名称。采样可能不完整，最多显示 256 项；返回时可能已不再登记，也不代表可以访问。</p><label>服务端实例 <select data-own-proxy-instance>${options}</select></label> <button type="button" data-own-proxy-refresh>重新采样</button><p data-own-proxy-status>采样不可用。</p><ul data-own-proxy-list></ul></div></section>`;
  },
  mount(element) {
   const select = element.querySelector('[data-own-proxy-instance]');
   const button = element.querySelector('[data-own-proxy-refresh]');
   const status = element.querySelector('[data-own-proxy-status]');
   const list = element.querySelector('[data-own-proxy-list]');
   let pending = null, serial = 0, disposed = false;
   const clear = message => { list.replaceChildren(); status.textContent = message; };
   async function sample() {
    pending?.abort();
    const requestSerial = ++serial, generation = session, id = select.value;
    clear(id ? '正在采样…' : '采样不可用。');
    if (!id || document.hidden) return;
    const controller = new AbortController(); pending = controller;
    const deadline = setTimeout(() => controller.abort(), 5000);
    try {
     const data = await api(`me/managed-proxies?id=${encodeURIComponent(id)}`, undefined, {signal:controller.signal});
     if (disposed || generation !== session || requestSerial !== serial || !element.isConnected || select.value !== id) return;
     const start = new Date(data?.startedAt), end = new Date(data?.sampledAt);
     if (!Array.isArray(data?.names) || data.names.length > 256 || data.mayBeIncomplete !== true ||
         data.names.some(name => typeof name !== 'string' || !name || name.length > 1024) ||
         !Number.isFinite(start.getTime()) || !Number.isFinite(end.getTime()) || start > end) throw new Error('采样不可用');
     status.textContent = `采样窗口：${start.toLocaleString('zh-CN')} 至 ${end.toLocaleString('zh-CN')}。结果可能不完整。`;
     if (!data.names.length) {
      const item = document.createElement('li'); item.textContent = '本次未采样到条目。'; list.appendChild(item);
     } else {
      for (const name of data.names) {
       const item = document.createElement('li'); item.textContent = `${name} · 采样时已登记`; list.appendChild(item);
      }
     }
    } catch (_) {
     if (!disposed && generation === session && requestSerial === serial && element.isConnected) clear('采样不可用。');
    } finally {
     clearTimeout(deadline);
     if (pending === controller) pending = null;
    }
   }
   const onChange = () => { void sample(); };
   const onVisibility = () => {
    if (document.hidden) { ++serial; pending?.abort(); pending = null; clear('采样不可用。'); }
    else void sample();
   };
   select.addEventListener('change', onChange); button.addEventListener('click', onChange);
   document.addEventListener('visibilitychange', onVisibility);
   ownProxiesCleanup = () => {
    disposed = true; ++serial; pending?.abort(); pending = null;
    select.removeEventListener('change', onChange); button.removeEventListener('click', onChange);
    document.removeEventListener('visibilitychange', onVisibility); clear('采样不可用。');
   };
   void sample();
  },
  dispose() { if (ownProxiesCleanup) ownProxiesCleanup(); ownProxiesCleanup = null; }
 });
 document.addEventListener('DOMContentLoaded', bind, {once:true});
 return {register:(name, component) => pages.set(name, component), api, refresh, navigate, toast, esc, download, openDialog, closeDialog, showDiff, endCurrentSession:() => { showLogin(); announce(); }, transfer:transfers, state:() => snapshot, epoch:() => session, isCurrent:(target, element) => page === target && !!snapshot && element?.isConnected && $('#content').contains(element)};
})();
