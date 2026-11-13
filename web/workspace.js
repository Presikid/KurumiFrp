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
 const accountWriteFreezeHint = error => error?.status >= 500 && typeof error.message === 'string' && error.message.startsWith('账户配置保存状态未确认')
  ? '账户文件写入结果未确认，后续账户写入已冻结；已有受管 FRP 转发可能仍在运行。请联系管理员确认工作台完全停止后再启动，并核对账户配置。'
  : '';
 let snapshot = null, page = 'overview', session = 0, request = null, root = null, activePage = null;
 let timer = null, toastTimer = null, dialogSpec = null, dialogBusy = false, dialogOperation = 0;
 let transfer = null, transferRequest = null;
 let themeCatalogSession = -1, themeCatalogRequest = null, themeVersion = 0, themeChoiceSequence = 0;
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
 function invalidateDialog() {
  const wasBusy = dialogBusy;
  ++dialogOperation; dialogBusy = false; dialogSpec = null;
  if ($('#dialog').open) $('#dialog').close();
  $('#cancel').disabled = false; $('#dialog-body').inert = false;
  $('#editor button[type="submit"]').disabled = false;
  return wasBusy;
 }
 function showLogin(message = '') {
  ++session; stopTimer(); dispose(); snapshot = null; request = null;
  closeMenu(); const wasBusy = invalidateDialog();
  if ($('#diff-dialog').open) $('#diff-dialog').close();
  $('#content').replaceChildren(); $('#app').hidden = true; $('#login').hidden = false;
  const picker = $('#skin-picker'); if (picker) picker.hidden = true;
  $('#login-form').elements.token.value = '';
  if (message || wasBusy) $('#login-error').textContent = [message, wasBusy ? '此前提交的操作可能仍在处理，重新登录后请先核对。' : ''].filter(Boolean).join(' ');
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
  return identity?.account === true && identity?.passwordAccount === true;
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
  const otherSessions = $('#revoke-other-sessions');
  if (otherSessions) otherSessions.hidden = !snapshot.identity?.account || !snapshot.identity?.passwordAccount;
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
    const wasBusy = invalidateDialog();
    if ($('#diff-dialog').open) $('#diff-dialog').close();
    if (wasBusy) toast('已提交的操作可能仍在处理，请刷新并核对。');
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
  ++dialogOperation; dialogSpec = spec;
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
  ++themeVersion;
  if (id && id !== 'default') document.body.dataset.kurumiSkin = id;
  else delete document.body.dataset.kurumiSkin;
  try { if (id && id !== 'default') localStorage.setItem('kurumi-skin', id); else localStorage.removeItem('kurumi-skin'); } catch (_) {}
 }
 async function updateThemePicker() {
  const active = document.body.dataset.kurumiSkin || savedSkin();
  if ((themeCatalogSession === session && !active) || themeCatalogRequest === session || !snapshot) return;
  const generation = session, version = themeVersion;
  themeCatalogRequest = generation;
  const controller = new AbortController(), deadline = setTimeout(() => controller.abort(), 15000);
  try {
   const result = await api('themes', undefined, {signal:controller.signal});
   if (generation !== session || !snapshot || !Array.isArray(result.themes)) return;
   themeCatalogSession = generation;
   $('#skin-picker').hidden = result.themes.length === 0;
   if (version === themeVersion && active && !result.themes.some(item => item.id === active && item.installed)) {
    applySkin('default');
    toast('当前主题不可用或素材缺失，已恢复默认主题。');
   }
  } finally {
   clearTimeout(deadline);
   if (themeCatalogRequest === generation) themeCatalogRequest = null;
  }
 }
 function currentThemeChoice(choice) {
  return choice.generation === session && choice.operation === dialogOperation && choice.sequence === themeChoiceSequence && !!snapshot && !$('#dialog').open && !$('#diff-dialog').open && !dialogBusy;
 }
 async function chooseSkin(choice) {
  const generation = choice.generation, controller = new AbortController(), deadline = setTimeout(() => controller.abort(), 15000);
  let result;
  try { result = await api('themes', undefined, {signal:controller.signal}); }
  catch (error) {
   if (controller.signal.aborted) throw new Error('读取主题目录超时，请重试。');
   throw error;
  } finally { clearTimeout(deadline); }
  if (!currentThemeChoice(choice) || !Array.isArray(result.themes) || !result.themes.length) return;
  const admin = snapshot.identity?.role === 'admin';
  const themes = result.themes.filter(item => item && typeof item.id === 'string' && item.id.length <= 32 && /^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(item.id));
  const active = document.body.dataset.kurumiSkin || 'default';
  const current = themes.some(item => item.id === active && item.installed) ? active : 'default';
  const cards = [{id:'default', name:'经典灵装', description:'随程序提供，可离线使用。', installed:true}, ...themes].map(item => {
   const isDefault = item.id === 'default', installed = isDefault || item.installed === true;
   const disabled = !admin && !installed;
   const status = isDefault ? '已内置' : installed ? '已安装' : admin ? '选择后下载' : '需要管理员安装';
   const preview = isDefault ? '/assets/overview.webp' : installed ? `/theme-assets/${item.id}/overview.png` : '';
   const artwork = preview ? `<img src="${esc(preview)}" alt="" loading="lazy" decoding="async">` : '';
   return `<label class="theme-picker-choice">
    <input class="theme-picker-input" type="radio" name="skin" value="${esc(item.id)}"${current === item.id ? ' checked' : ''}${disabled ? ' disabled' : ''}>
    <span class="theme-picker-card">
     <span class="theme-picker-art" data-theme="${esc(item.id)}" aria-hidden="true">${artwork}</span>
     <span class="theme-picker-copy"><strong>${esc(item.name)}</strong><span>${esc(item.description || '')}</span><small>${esc(status)}</small></span>
    </span>
   </label>`;
  }).join('');
  openDialog({
   html:`<h2>狂三主题</h2><p class="muted">选择你喜欢的场景；可选主题只在选择后从 GitHub 下载，安装后可离线使用。</p><fieldset class="theme-picker-grid"><legend>选择主题</legend>${cards}</fieldset>`,
   submitLabel:'应用主题',
   async onSubmit(form) {
    const operation = dialogOperation;
    const id = form.elements.skin.value;
    if (id !== 'default') {
     const selected = themes.find(item => item.id === id);
     if (!selected || (!selected.installed && !admin)) throw new Error('此主题尚不可用。');
     if (!selected.installed) {
      const submit = form.querySelector('[type="submit"]'), label = submit.textContent;
      const controller = new AbortController(), deadline = setTimeout(() => controller.abort(), 140000);
      submit.textContent = '正在下载主题…';
      try { await api('themes/install', {id}, {signal:controller.signal}); }
      catch (error) {
       if (!error.status || error.status >= 500) throw new Error('主题安装结果尚未确认，请重新打开主题选择并检查。当前主题未更改。');
       throw error;
      } finally {
       clearTimeout(deadline);
       if (generation === session && operation === dialogOperation) submit.textContent = label;
      }
     }
    }
    if (generation !== session || operation !== dialogOperation) return false;
    applySkin(id); toast('主题已切换。');
   }
  });
 }
 function showDiff(value) {
  ++themeChoiceSequence;
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
   try { await api('quit', {}); showLogin('程序已退出。'); } catch (error) { toast(error.message); }
  });
  $('#change-password').addEventListener('click', () => openDialog({
   html:'<h2>修改登录密码</h2><p class="muted">修改后需要重新登录。主密码可以为空；本地账户密码须符合账户要求。</p><div class="form-grid"><label>当前密码<input name="old" type="password" autocomplete="current-password"></label><label>新密码<input name="new" type="password" autocomplete="new-password"></label><label>再次输入新密码<input name="confirm" type="password" autocomplete="new-password"></label></div>',
   submitLabel:'更新密码',
   async onSubmit(form) {
    if (form.elements.new.value !== form.elements.confirm.value) throw new Error('两次输入的新密码不一致。');
    const generation = session, operation = dialogOperation;
    try { await api('password', {old:form.elements.old.value, new:form.elements.new.value}); }
    catch (error) {
     if (generation !== session || operation !== dialogOperation) return false;
     if (error.status === 409 || !error.status || error.status >= 500) {
      showLogin(accountWriteFreezeHint(error) || '密码状态或保存结果尚未确认，请重新登录后核对。'); announce(); return;
     }
     throw error;
    }
    if (generation !== session || operation !== dialogOperation) return false;
    // The password change has completed successfully; this is not an interrupted submission.
    dialogBusy = false;
    showLogin('密码已更新，请重新登录。'); announce();
   }
  }));
  const otherSessions = document.createElement('button');
  otherSessions.type = 'button'; otherSessions.id = 'revoke-other-sessions';
  otherSessions.textContent = '结束其他会话'; otherSessions.hidden = true;
  $('#change-password').after(otherSessions);
  otherSessions.addEventListener('click', async () => {
   if (!confirm('结束此账户当时存在的其他工作台会话？当前会话及共享该会话的标签页会保留。正在进行或之后的登录仍可能建立新会话，frp 连接不受影响。')) return;
   const generation = session, controller = new AbortController();
   const deadline = setTimeout(() => controller.abort(), 15000);
   otherSessions.disabled = true;
   try {
    const result = await api('me/sessions/revoke-others', {}, {signal:controller.signal});
    if (generation !== session) return;
    if (result?.ok !== true) throw new Error('会话操作结果尚未确认');
    toast('已结束当时存在的其他会话；之后的登录仍可建立新会话。');
   } catch (error) {
    if (generation !== session) return;
    if ([400, 403, 409].includes(error.status)) toast(error.message);
    else toast('会话操作结果尚未确认；旧请求可能仍在处理，请先核对再决定是否重试。');
   } finally {
    clearTimeout(deadline); otherSessions.disabled = false;
   }
  });
  $('#refresh').addEventListener('click', () => { if (confirmDiscard()) void refresh(false, true).catch(error => toast(error.message)); });
  applySkin(savedSkin());
  const picker = document.createElement('button');
  picker.id = 'skin-picker'; picker.type = 'button'; picker.className = 'icon-button'; picker.hidden = true;
  picker.title = '选择狂三主题'; picker.setAttribute('aria-label', '选择狂三主题'); picker.textContent = '✦';
  picker.addEventListener('click', () => {
   if ($('#dialog').open || $('#diff-dialog').open || dialogBusy || !snapshot) return;
   const choice = {generation:session, operation:dialogOperation, sequence:++themeChoiceSequence};
   void chooseSkin(choice).catch(error => { if (currentThemeChoice(choice)) toast(error.message); });
  });
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
   const spec = dialogSpec, generation = session, operation = dialogOperation, callback = spec?.['on' + type[0].toUpperCase() + type.slice(1)];
   if (!callback || dialogBusy) return;
   const showError = error => { if (generation === session && operation === dialogOperation && $('#dialog').open && dialogSpec === spec) $('#form-error').textContent = error.message; };
   try { Promise.resolve(callback(event, $('#editor'))).catch(showError); }
   catch (error) { showError(error); }
  });
  $('#editor').addEventListener('submit', async event => {
   event.preventDefault();
   if (!dialogSpec?.onSubmit || dialogBusy) return;
   const spec = dialogSpec, button = event.currentTarget.querySelector('[type="submit"]');
   const operation = ++dialogOperation, generation = session;
   dialogBusy = true; button.disabled = true; $('#cancel').disabled = true; $('#dialog-body').inert = true; $('#form-error').textContent = '';
   try {
    const result = await spec.onSubmit(event.currentTarget);
    if (operation !== dialogOperation || generation !== session) return;
    dialogBusy = false;
    if (result !== false && $('#dialog').open && dialogSpec === spec) $('#dialog').close();
   } catch (error) { if (operation === dialogOperation && generation === session && $('#dialog').open && dialogSpec === spec) $('#form-error').textContent = error.message; }
   finally {
    if (operation === dialogOperation && generation === session) {
     dialogBusy = false; button.disabled = false; $('#cancel').disabled = false; $('#dialog-body').inert = false;
    }
   }
  });
  const hash = location.hash.slice(1);
  if (pages.has(hash)) page = hash;
  else if (hash.startsWith('launch=')) history.replaceState(null, '', location.pathname + location.search + '#overview');
  const initialSession = session;
  void refresh().catch(error => { if (session === initialSession && error.status !== 401) $('#login-error').textContent = error.message; });
 }
 function mountOwnProxyQuota(element) {
  const status = element.querySelector('[data-own-quota-status]');
  const button = element.querySelector('[data-own-quota-refresh]');
  let pending = null, serial = 0, disposed = false, active = Promise.resolve();
  async function refreshQuota() {
   pending?.abort();
   const requestSerial = ++serial, generation = session;
   status.textContent = document.hidden ? '配额暂不可用。' : '正在读取配额…';
   if (document.hidden) return;
   const controller = new AbortController(); pending = controller;
   const deadline = setTimeout(() => controller.abort(), 5000);
   try {
    const data = await api('me/managed-proxy-quota', undefined, {signal:controller.signal});
    if (disposed || generation !== session || requestSerial !== serial || !element.isConnected) return;
    const sampled = new Date(data?.sampledAt);
    if (!Number.isSafeInteger(data?.used) || data.used < 0 ||
        !(data.limit === null || (Number.isSafeInteger(data.limit) && data.limit >= 0 && data.limit <= 1000000)) ||
        typeof data.sampledAt !== 'string' || !Number.isFinite(sampled.getTime())) throw new Error('配额暂不可用');
    const limit = data.limit === null ? '不限' : data.limit === 0 ? '0（阻止新的代理占位）' : String(data.limit);
    status.textContent = `截至 ${sampled.toLocaleString('zh-CN')}：占位 ${data.used}，上限 ${limit}。`;
   } catch (_) {
    if (!disposed && generation === session && requestSerial === serial && element.isConnected) status.textContent = '配额暂不可用。';
   } finally {
    clearTimeout(deadline);
    if (pending === controller) pending = null;
   }
  }
  const requestQuota = () => { active = refreshQuota(); return active; };
  const onRefresh = () => { void requestQuota(); };
  const onVisibility = () => {
   if (document.hidden) { ++serial; pending?.abort(); pending = null; status.textContent = '配额暂不可用。'; }
   else void requestQuota();
  };
  button.addEventListener('click', onRefresh);
  document.addEventListener('visibilitychange', onVisibility);
  void requestQuota();
  return {
   whenSettled: () => active,
   dispose() {
    disposed = true; ++serial; pending?.abort(); pending = null;
    button.removeEventListener('click', onRefresh);
    document.removeEventListener('visibilitychange', onVisibility); status.textContent = '配额暂不可用。';
   }
  };
 }
 let ownProxiesCleanup = null;
 pages.set('own-proxies', {
  render(value) {
   const instances = (value.state?.instances || []).filter(item => item?.kind === 'frps' && /^[a-f0-9]{16}$/.test(item.id));
   const options = instances.map(item => `<option value="${esc(item.id)}">${esc(item.name || 'frps')}</option>`).join('');
   const canReadNames = value.identity?.role === 'admin' || value.identity?.viewConfig === true;
   const namesPanel = canReadNames ? `<section class="panel"><div class="panel-content"><h2>我的代理</h2><p class="muted">显示采样窗口内在当前受管控制连接中观察到的名称。采样可能不完整，最多显示 256 项；返回时可能已不再登记，也不代表可以访问。</p><label>服务端实例 <select data-own-proxy-instance>${options}</select></label> <button type="button" data-own-proxy-refresh>重新采样</button><p data-own-proxy-status>采样不可用。</p><ul data-own-proxy-list></ul></div></section>` : '<p class="muted">当前账户未获配置详情授权，不能采样代理名称。</p>';
   return `<section class="panel"><div class="panel-content"><h2>代理占位与上限</h2><p class="muted">这是本工作台所有内嵌受管 frps 的账户占位总数，包含创建或清理中的代理；它不是在线数、流量或账单。</p><button type="button" data-own-quota-refresh>刷新配额</button><p data-own-quota-status>配额暂不可用。</p></div></section>${namesPanel}`;
  },
  mount(element) {
   const quota = mountOwnProxyQuota(element);
   const select = element.querySelector('[data-own-proxy-instance]');
   const button = element.querySelector('[data-own-proxy-refresh]');
   const status = element.querySelector('[data-own-proxy-status]');
   const list = element.querySelector('[data-own-proxy-list]');
   if (!select || !button || !status || !list) { ownProxiesCleanup = () => quota.dispose(); return; }
   let pending = null, serial = 0, disposed = false;
   const clear = message => { list.replaceChildren(); status.textContent = message; };
   async function sample() {
    if (disposed) return;
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
    else void quota.whenSettled().then(() => { if (!disposed) void sample(); });
   };
   select.addEventListener('change', onChange); button.addEventListener('click', onChange);
   document.addEventListener('visibilitychange', onVisibility);
   ownProxiesCleanup = () => {
    quota.dispose();
    disposed = true; ++serial; pending?.abort(); pending = null;
    select.removeEventListener('change', onChange); button.removeEventListener('click', onChange);
    document.removeEventListener('visibilitychange', onVisibility); clear('采样不可用。');
   };
   void quota.whenSettled().then(() => { if (!disposed) void sample(); });
  },
  dispose() { if (ownProxiesCleanup) ownProxiesCleanup(); ownProxiesCleanup = null; }
 });
 document.addEventListener('DOMContentLoaded', bind, {once:true});
 return {register:(name, component) => pages.set(name, component), api, refresh, navigate, toast, esc, download, openDialog, closeDialog, showDiff, endCurrentSession:() => { showLogin(); announce(); }, transfer:transfers, accountWriteFreezeHint, state:() => snapshot, epoch:() => session, isCurrent:(target, element) => page === target && !!snapshot && element?.isConnected && $('#content').contains(element)};
})();
