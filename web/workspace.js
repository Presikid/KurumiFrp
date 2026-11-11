'use strict';

// Shared shell and request lifecycle for the new workspace pages.
// ProtocolEditor uses the same escaping function when it renders form fields.
const esc = (value = '') => String(value ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;', '<':'&lt;', '>':'&gt;', '"':'&quot;', "'":'&#39;'}[c]));

window.Kurumi = (() => {
 const pages = new Map();
 const names = {overview:'总览', tunnels:'规则', instances:'实例', nodes:'节点', 'reported-nodes':'主动上报', firewall:'访问控制', health:'链路诊断', config:'配置', core:'运行核心', logs:'日志', update:'软件更新', accounts:'账户'};
 const $ = selector => document.querySelector(selector);
 let snapshot = null, page = 'overview', session = 0, request = null, root = null, activePage = null;
 let timer = null, toastTimer = null, dialogSpec = null, dialogBusy = false;
 let transfer = null, transferRequest = null;
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
 function updateShell() {
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
  });
  $('#quit-app').hidden = !admin; $('#change-password').hidden = false;
 }
 function render() {
  if (!snapshot) return;
  const component = pages.get(page);
  dispose(); updateShell();
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
 function showDiff(value) {
  $('#diff-title').textContent = `${value.instanceName || '实例'} · 配置差异`;
  $('#diff-summary').textContent = `新增 ${value.addedCount || 0} 行 · 移除 ${value.removedCount || 0} 行${value.secretChanged ? ' · 认证凭据已变化' : ''}${value.orderChanged ? ' · 顺序变化' : ''}`;
  $('#diff-content').textContent = [...(value.removed || []).map(line => '- ' + line), ...(value.added || []).map(line => '+ ' + line)].join('\n') || '没有可显示的行变化';
  if (value.truncated) $('#diff-content').textContent += '\n差异显示已截断';
  $('#diff-dialog').showModal();
 }
 function bind() {
  persistTransfer();
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
 document.addEventListener('DOMContentLoaded', bind, {once:true});
 return {register:(name, component) => pages.set(name, component), api, refresh, navigate, toast, esc, download, openDialog, closeDialog, showDiff, transfer:transfers, state:() => snapshot, epoch:() => session, isCurrent:(target, element) => page === target && !!snapshot && element?.isConnected && $('#content').contains(element)};
})();
