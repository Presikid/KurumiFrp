'use strict';
// The server enforces every permission; this view only presents available actions.
titles.accounts = '本地账户';
const accountRenderBase = render;
let accountData = null, accountLoadSerial = 0;
window.resetAccountView = () => {
 accountData = null; ++accountLoadSerial;
 for (const d of document.querySelectorAll('#account-password-dialog, #instance-read-dialog, #login-history-dialog')) if (d.open) d.close();
};

function updateAccountNavigation() {
 const admin = data?.identity?.role === 'admin';
 document.querySelectorAll('#nav [data-page]').forEach(el => { el.hidden = !admin && el.dataset.page !== 'instances' && !(el.dataset.page === 'logs' && data?.identity?.allowLogs); });
 $('#quit-app').hidden = !admin;
 $('#change-password').hidden = !data?.identity?.account;
}

render = function () {
 if (!data) return;
 updateAccountNavigation();
 if (data.identity?.role !== 'admin') {
  if (page === 'logs' && data.identity?.allowLogs) { accountRenderBase(); return; }
  stopCorePolling();
  page = 'instances';
  ++serial;
  $('#page-title').textContent = '授权实例';
  document.title = '授权实例 · KurumiFRP';
  document.querySelectorAll('[data-page]').forEach(el => el.classList.toggle('active', el.dataset.page === page));
  $('#content').innerHTML = window.KurumiInstances.render(data,{header,empty,esc,registrationText:instanceRegistrationText});
  renderedSnapshot = stateSnapshot; renderedPage = page; renderedRevision = data.revision;
  return;
 }
 if (page !== 'accounts') { accountRenderBase(); return; }
 stopCorePolling();
 ++serial;
 $('#page-title').textContent = titles.accounts;
 document.title = titles.accounts + ' · KurumiFRP';
 document.querySelectorAll('[data-page]').forEach(el => el.classList.toggle('active', el.dataset.page === page));
 if (!$('#accounts-screen')) {
  $('#content').innerHTML = header('本地账户', '管理员拥有全部权限；操作员可启停指定实例；查看者只能查看指定实例。', '<button type="button" data-login-history>登录记录</button>') +
   `<section id="accounts-screen" class="panel"><div class="panel-content"><p class="small-note">现有本机管理令牌和启动快捷方式仍具有完整管理权限。账户配置单独保存，不包含在配置导出和加密备份中。删除实例会撤销该实例授权；导入配置或恢复历史会撤销全部非管理员实例授权，需要重新分配。授权在配置变更之前撤销，即使变更失败也需检查账户设置。</p><div id="account-list">正在读取账户…</div><hr><div id="account-editor"></div></div></section>`;
  accountData = null;
  loadAccounts();
 }
 renderedSnapshot = stateSnapshot; renderedPage = page; renderedRevision = data.revision;
};

async function loadAccounts() {
 const generation = authGeneration, request = ++accountLoadSerial;
 try {
  const result = await api('accounts');
  if (request !== accountLoadSerial || generation !== authGeneration || page !== 'accounts' || !$('#accounts-screen')) return;
  accountData = result;
  paintAccounts(); showAccountEditor();
 } catch (err) {
  if (request === accountLoadSerial && generation === authGeneration && $('#account-list')) $('#account-list').textContent = '读取失败：' + err.message;
 }
}

function paintAccounts() {
 const list = $('#account-list');
 if (!list || !accountData) return;
 const names = Object.fromEntries(data.state.instances.map(i => [i.id, i.name]));
 list.innerHTML = accountData.accounts.length ? `<div class="table-wrap"><table><thead><tr><th>用户</th><th>角色</th><th>状态</th><th>授权实例</th><th>管理</th></tr></thead><tbody>${accountData.accounts.map(u =>
  `<tr><td>${esc(u.username)}</td><td>${esc(({admin:'管理员',operator:'操作员',viewer:'查看者'})[u.role])}</td><td>${u.enabled?'启用':'停用'}</td><td>${u.role==='admin'?'全部':(u.instances.map(id=>esc(names[id] || ('已不存在：'+id))).join('、') || '无')}${u.role==='operator'?' · 可操作 '+(u.controlInstances || []).length+' 个':''}${u.role!=='admin'&&u.allowLogs?' · 可查看 '+(u.logInstances || []).length+' 个实例日志':''}</td><td><div class="actions"><button type="button" data-account-edit="${esc(u.id)}">编辑</button><button type="button" class="danger" data-account-delete="${esc(u.id)}">删除</button></div></td></tr>`
 ).join('')}</tbody></table></div>` : '<p>尚未创建本地账户。现有管理令牌可继续使用。</p>';
}

function showAccountEditor(id = '') {
 const host = $('#account-editor');
 if (!host || !accountData) return;
 const u = accountData.accounts.find(item => item.id === id);
 const grants = new Set(u?.instances || []);
 const controls = new Set(u?.controlInstances || []);
 const logs = new Set(u?.logInstances || []);
 host.innerHTML = `<form id="account-form"><h3>${u?'编辑账户':'新建账户'}</h3><input type="hidden" name="id" value="${esc(u?.id || '')}"><div class="form-grid">
  <label>用户名<input name="username" required pattern="[A-Za-z0-9][A-Za-z0-9_.-]{2,31}" maxlength="32" value="${esc(u?.username || '')}" ${u?'readonly':''} autocomplete="off"></label>
  <label>角色<select name="role"><option value="viewer" ${!u || u.role==='viewer'?'selected':''}>查看者</option><option value="operator" ${u?.role==='operator'?'selected':''}>操作员</option><option value="admin" ${u?.role==='admin'?'selected':''}>管理员（全部权限）</option></select></label>
  <label class="span-2">${u?'重置密码（留空保留原密码）':'密码（至少 12 字节）'}<input name="password" type="password" autocomplete="new-password" maxlength="1024" ${u?'':'required'}></label>
  <label class="form-check span-2"><input type="checkbox" name="enabled" ${!u || u.enabled?'checked':''}>启用账户</label>
  <fieldset class="span-2" id="account-grants"><legend>可访问的实例</legend>${data.state.instances.length ? data.state.instances.map(i => `<label class="form-check"><input type="checkbox" name="instance" value="${esc(i.id)}" ${grants.has(i.id)?'checked':''}>${esc(i.name)} · ${esc(i.kind)}</label>`).join('') : '<p>当前没有实例。</p>'}<p class="small-note">未选择表示不能访问任何实例；管理员始终可访问全部实例。</p></fieldset>
  <fieldset class="span-2" id="account-controls"><legend>可操作的实例</legend>${data.state.instances.length ? data.state.instances.map(i => `<label class="form-check"><input type="checkbox" name="controlInstance" value="${esc(i.id)}" ${controls.has(i.id)?'checked':''}>${esc(i.name)} · ${esc(i.kind)}</label>`).join('') : '<p>当前没有实例。</p>'}<p class="small-note">可操作实例必须先在上方授权查看；未勾选时仅可查看，不能启停、重启或校验。</p></fieldset>
  <div class="span-2" id="account-log-grant"><label class="form-check"><input type="checkbox" name="allowLogs" ${u?.allowLogs?'checked':''}>允许查看运行日志</label><p class="small-note">默认关闭。日志可能包含地址、用户名和其他敏感内容；请仅授予可信账户。</p></div>
  <fieldset class="span-2" id="account-logs"><legend>可查看日志的实例</legend>${data.state.instances.length ? data.state.instances.map(i => `<label class="form-check"><input type="checkbox" name="logInstance" value="${esc(i.id)}" ${logs.has(i.id)?'checked':''}>${esc(i.name)} · ${esc(i.kind)}</label>`).join('') : '<p>当前没有实例。</p>'}<p class="small-note">只能选择已授权查看的实例；未选择时无法查看任何日志。</p></fieldset>
  </div><div class="actions"><button class="primary" type="submit">保存账户</button><button type="button" data-account-new>新建账户</button></div><p class="error" role="alert"></p></form>`;
 const form = $('#account-form');
 const updateGrants = () => {
  const role = form.elements.role.value, admin = role === 'admin';
  $('#account-grants').hidden = admin; $('#account-log-grant').hidden = admin; $('#account-controls').hidden = role !== 'operator'; $('#account-logs').hidden = admin || !form.elements.allowLogs.checked;
  const visible = new Set([...form.querySelectorAll('[name=instance]:checked')].map(el => el.value));
  form.querySelectorAll('[name=controlInstance], [name=logInstance]').forEach(el => {el.disabled = !visible.has(el.value); if (el.disabled) el.checked = false;});
 };
 form.addEventListener('change', updateGrants); updateGrants();
}

$('#content').addEventListener('click', async e => {
 if (e.target.closest('[data-login-history]')) { openLoginHistory(); return; }
 const instanceRead = e.target.closest('[data-instance-read]');
 if (instanceRead) {
  const mode = instanceRead.dataset.instanceRead;
  const id = instanceRead.dataset.id, runId = data?.runtime?.[id]?.runId || '', revision = data?.revision;
  const instance = data?.state.instances.find(item => item.id === id);
  const d = document.createElement('dialog'), output = document.createElement(mode === 'config' ? 'pre' : 'div'), close = document.createElement('button'), generation = authGeneration;
  d.id = 'instance-read-dialog';
  if (mode !== 'config') d.setAttribute('aria-label', mode === 'traffic-history' ? '流量历史' : '当前流量');
  close.textContent = '关闭'; close.type = 'button'; close.addEventListener('click', () => d.close());
  output.textContent = '正在读取…'; d.append(close, output); document.body.appendChild(d);
  d.addEventListener('close', () => d.remove()); d.showModal();
  try {
   const result = await api(`${mode}?id=${encodeURIComponent(id)}`);
   if (generation === authGeneration && d.open) {
    const current = data?.state.instances.find(item => item.id === id);
    const value = mode === 'traffic' ? result.instances?.[id] : result;
    if (!instance || !current || current.kind !== instance.kind || data.revision !== revision) output.textContent = '实例配置已变化，请关闭后重新查看。';
    else if (mode === 'config') output.textContent = result.toml;
    else if (mode === 'traffic-history' && (value?.instanceId !== id || value.kind !== instance.kind)) output.textContent = '实例状态已变化，请关闭后重新查看。';
    else if (mode !== 'traffic-history' && (!value || (data.runtime?.[id]?.runId || '') !== runId || (value.runId || '') !== runId)) output.textContent = '实例运行状态已变化，请关闭后重新查看。';
    else if (mode === 'traffic-history') output.innerHTML = Overview.renderTrafficHistory(value, esc, true);
    else output.innerHTML = (mode === 'server-traffic' ? Overview.renderServerTraffic(value, esc) : Overview.renderClientTraffic(value, esc)) + '<p class="small-note">再次打开可读取新快照。</p>';
   }
  } catch (err) { if (generation === authGeneration && d.open) output.textContent = err.message; }
  return;
 }
 const edit = e.target.closest('[data-account-edit]'), remove = e.target.closest('[data-account-delete]');
 if (edit) { showAccountEditor(edit.dataset.accountEdit); return; }
 if (e.target.closest('[data-account-new]')) { showAccountEditor(); return; }
 if (!remove || !accountData) return;
 const user = accountData.accounts.find(u => u.id === remove.dataset.accountDelete);
 if (!user || !confirm(`删除账户 ${user.username} 并撤销其会话？`)) return;
 remove.disabled = true;
 try {
  await api('accounts', {action:'delete', revision:accountData.revision, id:user.id});
  toast('账户已删除'); accountData = null; await loadAccounts(); stateETag = ''; await refresh(true);
 } catch (err) { toast(err.message); remove.disabled = false; }
});

function openLoginHistory() {
 if (data?.identity?.role !== 'admin' || document.querySelector('#login-history-dialog')) return;
 const dialog = document.createElement('dialog'), generation = authGeneration;
 dialog.id = 'login-history-dialog';
 dialog.innerHTML = '<h2>登录记录</h2><p class="small-note">连接来源是工作台实际收到连接的对端地址，不使用转发请求头；通过反向代理或 SSH 转发时通常显示代理地址。连续失败会限频记录。</p><div data-history-list>正在读取…</div><p class="error" role="alert"></p><div class="dialog-actions"><button type="button" data-close>关闭</button><button type="button" data-clear class="danger" disabled>清空记录</button></div>';
 document.body.appendChild(dialog);
 dialog.addEventListener('close', () => dialog.remove());
 dialog.querySelector('[data-close]').addEventListener('click', () => dialog.close());
 const list = dialog.querySelector('[data-history-list]'), error = dialog.querySelector('.error'), clear = dialog.querySelector('[data-clear]');
 async function refreshHistory() {
  error.textContent = '';
  try {
   const result = await api('login-history');
   if (generation !== authGeneration || !dialog.open) return;
   const entries = Array.isArray(result.entries) ? result.entries : [];
   list.innerHTML = `${result.saved ? '' : '<p class="error">记录文件不可读取或保存失败；原文件已保留，请检查数据目录。</p>'}${result.suppressed ? `<p class="small-note">本次运行有 ${Number(result.suppressed)} 次重复失败未逐条记录。</p>` : ''}${entries.length ? `<div class="table-wrap"><table><thead><tr><th>时间</th><th>账户</th><th>连接来源</th><th>结果</th></tr></thead><tbody>${entries.map(item => `<tr><td>${esc(new Date(item.at).toLocaleString('zh-CN'))}</td><td>${esc(item.account)}</td><td>${esc(item.peer)}</td><td>${item.success ? '成功' : '未成功'}</td></tr>`).join('')}</tbody></table></div>` : '<p>暂无记录。</p>'}`;
   clear.disabled = !result.saved;
  } catch (err) { if (generation === authGeneration && dialog.open) { error.textContent = err.message; clear.disabled = true; } }
 }
 clear.addEventListener('click', async () => {
  if (!confirm('清空本机保存的登录记录？')) return;
  clear.disabled = true;
  try { await api('login-history', {action:'clear'}); if (generation === authGeneration && dialog.open) await refreshHistory(); }
  catch (err) { if (generation === authGeneration && dialog.open) { error.textContent = err.message; clear.disabled = false; } }
 });
 dialog.showModal();
 void refreshHistory();
}

$('#content').addEventListener('submit', async e => {
 if (e.target.id !== 'account-form' || !accountData) return;
 e.preventDefault();
 const form = e.target, button = form.querySelector('[type=submit]'), error = form.querySelector('.error');
 const role = form.elements.role.value;
 const request = {action:'save', revision:accountData.revision, id:form.elements.id.value, username:form.elements.username.value,
  role, enabled:form.elements.enabled.checked, password:form.elements.password.value,
  instances:role === 'admin' ? [] : [...form.querySelectorAll('[name=instance]:checked')].map(el => el.value),
  controlInstances:role === 'operator' ? [...form.querySelectorAll('[name=controlInstance]:checked')].map(el => el.value) : [], allowLogs:role !== 'admin' && form.elements.allowLogs.checked,
  logInstances:role !== 'admin' && form.elements.allowLogs.checked ? [...form.querySelectorAll('[name=logInstance]:checked')].map(el => el.value) : []};
 button.disabled = true; error.textContent = '';
 try {
  await api('accounts', request); form.elements.password.value = ''; toast('账户已保存');
  accountData = null; await loadAccounts(); stateETag = ''; await refresh(true);
 } catch (err) { error.textContent = err.message; button.disabled = false; }
});

$('#change-password').addEventListener('click', () => {
 const d = document.createElement('dialog');
 d.id = 'account-password-dialog';
 d.innerHTML = `<form id="own-password-form"><h2>修改账户密码</h2><label>原密码<input type="password" name="old" autocomplete="current-password" required maxlength="1024"></label><label>新密码（至少 12 字节）<input type="password" name="new" autocomplete="new-password" required maxlength="1024"></label><div class="dialog-actions"><button type="button" data-close>取消</button><button class="primary" type="submit">修改并重新登录</button></div><p class="error" role="alert"></p></form>`;
 document.body.appendChild(d);
 d.addEventListener('close', () => d.remove());
 d.querySelector('[data-close]').addEventListener('click', () => d.close());
 d.querySelector('form').addEventListener('submit', async e => {
  e.preventDefault(); const form = e.target, button = form.querySelector('[type=submit]'); button.disabled = true;
  try { await api('password', {old:form.elements.old.value, new:form.elements.new.value}); d.close(); showLogin(); announceSessionChange(); $('#login-error').textContent = '密码已修改，请重新登录。'; }
  catch (err) { form.querySelector('.error').textContent = err.message; button.disabled = false; }
 });
 d.showModal();
});

if (location.hash === '#accounts') page = 'accounts';
if (data) render();
