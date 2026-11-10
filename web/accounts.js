'use strict';
// The server enforces every permission; this view only presents available actions.
titles.accounts = '本地账户';
const accountRenderBase = render;
let accountData = null, accountLoadSerial = 0;
window.resetAccountView = () => {
 accountData = null; ++accountLoadSerial;
 for (const d of document.querySelectorAll('#account-password-dialog, #instance-read-dialog, #login-history-dialog, #oidc-settings-dialog')) if (d.open) d.close();
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
  page = 'instances';
  ++serial;
  $('#page-title').textContent = '授权实例';
  document.title = '授权实例 · KurumiFRP';
  document.querySelectorAll('[data-page]').forEach(el => el.classList.toggle('active', el.dataset.page === page));
  $('#content').innerHTML = window.KurumiInstances.render(data,{header,empty,esc,registrationText:instanceRegistrationText});
  $('#content').insertAdjacentHTML('beforeend', '<section class="panel" id="shared-nodes"><div class="panel-title"><h3>授权远程实例</h3></div><div class="panel-content"><div class="notice" data-shared-notices hidden></div><div data-shared-list>正在读取…</div></div></section>');
  paintSharedNotices();
  loadSharedNodes();
  renderedSnapshot = stateSnapshot; renderedPage = page; renderedRevision = data.revision;
  return;
 }
 if (page !== 'accounts') { accountRenderBase(); return; }
 ++serial;
 $('#page-title').textContent = titles.accounts;
 document.title = titles.accounts + ' · KurumiFRP';
 document.querySelectorAll('[data-page]').forEach(el => el.classList.toggle('active', el.dataset.page === page));
 if (!$('#accounts-screen')) {
  $('#content').innerHTML = header('本地账户', '管理员拥有全部权限；操作员可启停指定实例；查看者只能查看指定实例。', '<button type="button" data-login-history>登录记录</button>') +
   `<section id="accounts-screen" class="panel"><div class="panel-content"><p class="small-note">现有本机管理令牌和启动快捷方式仍具有完整管理权限。账户配置单独保存，不包含在配置导出和加密备份中。删除实例会撤销该实例授权；导入配置或恢复历史会撤销全部非管理员实例授权，需要重新分配。授权在配置变更之前撤销，即使变更失败也需检查账户设置。</p><div class="actions"><button type="button" data-oidc-settings>单点登录设置</button></div><div id="account-list">正在读取账户…</div><hr><div id="account-editor"></div></div></section>`;
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

function paintSharedNotices() {
 const host = document.querySelector('#shared-nodes [data-shared-notices]'), notices = window.KurumiRemoteNotices?.list();
 if (!host || !notices) return;
 host.hidden = notices.length === 0;
 host.innerHTML = notices.map(notice => notice.phase === 'unavailable' ? '<p>无法读取远程操作核对记录，暂不能提交操作。</p>' : notice.phase === 'pending' ? '<p>远程操作正在进行；关闭页面不能保证取消。</p>' : `<p>有远程操作结果未确认，请独立核对目标后再解除。 <button type="button" data-shared-ack="${esc(notice.key)}">核对并解除</button></p>`).join('');
 host.querySelectorAll('[data-shared-ack]').forEach(button => button.addEventListener('click', () => {
  if (!confirm('请先独立核对目标状态及先前请求是否仍会到达；解除记录不代表操作已完成。确认已核对？')) return;
  if (!window.KurumiRemoteNotices.remove(button.dataset.sharedAck)) toast('无法清除核对记录，请检查浏览器本地存储。');
  paintSharedNotices();
 }));
}
window.KurumiPaintSharedNotices = paintSharedNotices;

async function loadSharedNodes() {
 const host = document.querySelector('#shared-nodes [data-shared-list]');
 if (!host) return;
 const generation = authGeneration, navigation = navigationGeneration;
 const current = () => host.isConnected && generation === authGeneration && navigation === navigationGeneration && page === 'instances';
 try {
  const result = await api('nodes');
  if (!current()) return;
  const nodes = Array.isArray(result.nodes) ? result.nodes : [];
  host.innerHTML = nodes.length ? nodes.map(node => `<div class="check-row"><strong>${esc(node.name)}</strong><button type="button" data-shared-check="${esc(node.id)}" ${node.ready?'':'disabled'}>${node.ready?'检查状态':'等待管理员确认地址'}</button></div><div data-shared-result="${esc(node.id)}"></div>`).join('') : '<p class="small-note">当前没有远程实例授权。</p>';
  host.querySelectorAll('[data-shared-check]').forEach(button => button.addEventListener('click', async () => {
   const node = nodes.find(item => item.id === button.dataset.sharedCheck);
   const output = host.querySelector(`[data-shared-result="${node?.id || ''}"]`);
   if (!node || !output || !current()) return;
   const controller = new AbortController(), timeout = setTimeout(() => controller.abort(), 10000);
   button.disabled = true; output.textContent = '正在读取…';
   try {
    const checked = await api('nodes/check', {id:node.id, stamp:node.stamp}, {signal:controller.signal});
    if (!current() || !output.isConnected) return;
    if (checked.id !== node.id || checked.stamp !== node.stamp) throw new Error('节点已变化，请重新打开页面。');
    const instances = Array.isArray(checked.summary?.instances) ? checked.summary.instances : [];
    const states = {running:'运行中', stopped:'已停止', exited:'已退出', 'autostart-failed':'自动启动失败'};
    const allowed = new Set(data.identity?.allowedActions || []);
    const actionButton = (instance, action, label) => allowed.has(action) ? `<button type="button" data-shared-action="${action}" data-instance-id="${esc(instance.id)}">${label}</button>` : '';
    output.innerHTML = instances.length ? `<ul>${instances.map(instance => `<li>${esc(instance.name)} · ${esc(instance.kind)} · ${esc(states[instance.state] || '状态未知')}${esc(window.KurumiRemoteTraffic?.(instance) || '')}${instance.canControl ? ` <span class="actions">${instance.state === 'running' ? actionButton(instance,'stop','停止') + actionButton(instance,'restart','重启') : actionButton(instance,'start','启动')}</span>` : ''}</li>`).join('')}</ul>` : '<p class="small-note">没有可显示的授权实例；实例可能已被替换，请联系管理员重新授权。</p>';
    output.querySelectorAll('[data-shared-action]').forEach(action => action.addEventListener('click', () => {
     const instance = instances.find(item => item.id === action.dataset.instanceId);
     if (instance) void controlSharedNode(node, instance, action.dataset.sharedAction, output, current);
    }));
   } catch (error) {if (current() && output.isConnected) output.textContent = error.name === 'AbortError' ? '检查超时，请重试。' : error.message;}
   finally {clearTimeout(timeout); if (button.isConnected) button.disabled = false;}
  }));
 } catch (error) {if (current()) host.textContent = '读取远程授权失败：' + error.message;}
}

async function controlSharedNode(node, instance, action, output, current) {
 const notices = window.KurumiRemoteNotices;
 if (!current() || !notices || notices.list().length) {paintSharedNotices(); return;}
 output.querySelectorAll('button').forEach(button => {button.disabled = true;});
 let pending = null;
 try {
  const controller = new AbortController(), timeout = setTimeout(() => controller.abort(), 10000);
  let checked;
  try {checked = await api('nodes/control', {id:node.id, stamp:node.stamp, instanceId:instance.id, action:'preview', version:''}, {signal:controller.signal});}
  finally {clearTimeout(timeout);}
  if (!current() || !output.isConnected) return;
  const preview = checked.preview, running = preview?.state === 'running';
  if (checked.id !== node.id || checked.stamp !== node.stamp || preview?.id !== instance.id || preview.binding !== instance.binding || !/^[a-f0-9]{64}$/.test(preview.version || '') || !/^[a-f0-9]{64}$/.test(checked.capability || '') || !Number.isSafeInteger(checked.capabilityExpiry) || (action === 'start' && running) || (action !== 'start' && !running)) throw new Error('目标或授权已变化，请重新检查。');
  const label = ({start:'启动', stop:'停止', restart:'重启'})[action];
  if (!label || !confirm(`在「${node.name}」${label}实例「${preview.name}」？\n重启会应用目标草稿，失败后实例可能保持停止。操作被受理后会暂停目标的定时计划；断线不保证取消。`)) return;
  if (!current() || notices.list().length) {paintSharedNotices(); return;}
  pending = notices.begin(node, preview, action);
  if (!pending) throw new Error('无法保存操作核对记录，请检查浏览器本地存储。');
  paintSharedNotices();
  const sendController = new AbortController(), sendTimeout = setTimeout(() => sendController.abort(), 45000);
  let result;
  try {result = await api('nodes/control', {id:node.id, stamp:node.stamp, instanceId:instance.id, action, version:preview.version, capability:checked.capability, capabilityExpiry:checked.capabilityExpiry, confirmed:true}, {signal:sendController.signal});}
  finally {clearTimeout(sendTimeout);}
  if (!result?.ok || result.id !== node.id || result.stamp !== node.stamp || result.instanceId !== instance.id) throw new Error('目标结果未确认。');
  notices.finish(pending, true); pending = null;
  if (current() && output.isConnected) output.textContent = '目标已受理操作，请重新检查状态。';
 } catch (error) {
  if (pending) notices.finish(pending, false);
  if (current() && output.isConnected) output.textContent = pending ? '远程操作结果未确认，请先独立核对目标。' : (error.name === 'AbortError' ? '读取目标超时，请重试。' : error.message);
 } finally {
  paintSharedNotices();
  if (output.isConnected) output.querySelectorAll('button').forEach(button => {button.disabled = false;});
 }
}

function paintAccounts() {
 const list = $('#account-list');
 if (!list || !accountData) return;
 const names = Object.fromEntries(data.state.instances.map(i => [i.id, i.name]));
 list.innerHTML = accountData.accounts.length ? `<div class="table-wrap"><table><thead><tr><th>用户</th><th>角色</th><th>状态</th><th>授权实例</th><th>管理</th></tr></thead><tbody>${accountData.accounts.map(u =>
  `<tr><td>${esc(u.username)}</td><td>${esc(({admin:'管理员',operator:'操作员',viewer:'查看者'})[u.role])}</td><td>${!u.enabled?'停用':u.expired?'已到期':'启用'}${u.expiresAt?'<br><small>到期：'+esc(u.expiresAt)+'</small>':''}</td><td>${u.role==='admin'?'全部':(u.instances.map(id=>esc(names[id] || ('已不存在：'+id))).join('、') || '无')}${u.role==='operator'?' · 可操作 '+(u.controlInstances || []).length+' 个':''}${u.role!=='admin'&&u.allowLogs?' · 可查看 '+(u.logInstances || []).length+' 个实例日志':''}${u.role!=='admin'?' · 远程可查看 '+(u.remoteInstances || []).length+' 个':''}${u.role==='operator'?'、可操作 '+(u.remoteInstances || []).filter(grant=>grant.control).length+' 个':''}</td><td><div class="actions"><button type="button" data-account-edit="${esc(u.id)}">编辑</button><button type="button" class="danger" data-account-delete="${esc(u.id)}">删除</button></div></td></tr>`
 ).join('')}</tbody></table></div>` : '<p>尚未创建本地账户。现有管理令牌可继续使用。</p>';
}

function showAccountEditor(id = '') {
 const host = $('#account-editor');
 if (!host || !accountData) return;
 const u = accountData.accounts.find(item => item.id === id);
 const grants = new Set(u?.instances || []);
 const controls = new Set(u?.controlInstances || []);
 const allowedActions = new Set(u?.role === 'operator' ? (u.allowedActions || []) : ['start','stop','restart','validate']);
 const logs = new Set(u?.logInstances || []);
 const remoteKey = grant => `${grant.nodeId}:${grant.instanceId}:${grant.binding}`;
 const remoteChoices = new Map((u?.remoteInstances || []).map(grant => [remoteKey(grant), {grant, label:`节点 ${grant.nodeId} / 实例 ${grant.instanceId}（待核对）`, selected:true}]));
 host.innerHTML = `<form id="account-form"><h3>${u?'编辑账户':'新建账户'}</h3><input type="hidden" name="id" value="${esc(u?.id || '')}"><div class="form-grid">
  <label>用户名<input name="username" required pattern="[A-Za-z0-9][A-Za-z0-9_.-]{2,31}" maxlength="32" value="${esc(u?.username || '')}" ${u?'readonly':''} autocomplete="off"></label>
  <label>角色<select name="role"><option value="viewer" ${!u || u.role==='viewer'?'selected':''}>查看者</option><option value="operator" ${u?.role==='operator'?'selected':''}>操作员</option><option value="admin" ${u?.role==='admin'?'selected':''}>管理员（全部权限）</option></select></label>
  <label class="span-2">${u?'重置密码（留空保留原密码）':'密码（至少 12 字节）'}<input name="password" type="password" autocomplete="new-password" maxlength="1024" ${u?'':'required'}></label>
  <label class="span-2">账户到期时间（UTC，留空不限）<input name="expiresAt" type="datetime-local" step="1" value="${esc(u?.expiresAt ? u.expiresAt.slice(0,19) : '')}"><small>到期后不能登录，现有会话在下一次请求或日志帧时失效；此处填写 UTC 时间。</small></label>
  <label class="form-check span-2"><input type="checkbox" name="enabled" ${!u || u.enabled?'checked':''}>启用账户</label>
  <fieldset class="span-2" id="account-grants"><legend>可访问的实例</legend>${data.state.instances.length ? data.state.instances.map(i => `<label class="form-check"><input type="checkbox" name="instance" value="${esc(i.id)}" ${grants.has(i.id)?'checked':''}>${esc(i.name)} · ${esc(i.kind)}</label>`).join('') : '<p>当前没有实例。</p>'}<p class="small-note">未选择表示不能访问任何实例；管理员始终可访问全部实例。</p></fieldset>
  <fieldset class="span-2" id="account-read-permissions"><legend>实例数据查看范围</legend><label class="form-check"><input type="checkbox" name="viewConfig" ${u?.readPermissions?.config !== false?'checked':''}>配置详情和规则</label><label class="form-check"><input type="checkbox" name="viewTraffic" ${u?.readPermissions?.traffic !== false?'checked':''}>当前流量和流量历史</label><p class="small-note">仅适用于已授权查看的实例；远程流量还需要目标开启共享。日志单独授权，日志本身可能包含地址等信息。</p></fieldset>
  <fieldset class="span-2" id="account-controls"><legend>可操作的实例</legend>${data.state.instances.length ? data.state.instances.map(i => `<label class="form-check"><input type="checkbox" name="controlInstance" value="${esc(i.id)}" ${controls.has(i.id)?'checked':''}>${esc(i.name)} · ${esc(i.kind)}</label>`).join('') : '<p>当前没有实例。</p>'}<p class="small-note">可操作实例必须先在上方授权查看；未勾选时仅可查看，不能启停、重启或校验。</p></fieldset>
  <fieldset class="span-2" id="account-actions"><legend>允许的实例操作</legend>${[['start','启动'],['stop','停止'],['restart','重启'],['validate','配置校验']].map(([value,label])=>`<label class="form-check"><input type="checkbox" name="allowedAction" value="${value}" ${allowedActions.has(value)?'checked':''}>${label}</label>`).join('')}<p class="small-note">对有操作授权的本机及远程实例生效；配置校验仅适用于本机。重启会中断实例，失败时实例可能保持停止。</p></fieldset>
  <div class="span-2" id="account-log-grant"><label class="form-check"><input type="checkbox" name="allowLogs" ${u?.allowLogs?'checked':''}>允许查看运行日志</label><p class="small-note">默认关闭。日志可能包含地址、用户名和其他敏感内容；请仅授予可信账户。</p></div>
  <fieldset class="span-2" id="account-logs"><legend>可查看日志的实例</legend>${data.state.instances.length ? data.state.instances.map(i => `<label class="form-check"><input type="checkbox" name="logInstance" value="${esc(i.id)}" ${logs.has(i.id)?'checked':''}>${esc(i.name)} · ${esc(i.kind)}</label>`).join('') : '<p>当前没有实例。</p>'}<p class="small-note">只能选择已授权查看的实例；未选择时无法查看任何日志。</p></fieldset>
  <fieldset class="span-2" id="account-remote"><legend>远程实例授权</legend><p class="small-note">读取已添加且已确认地址的节点，选择可查看状态的实例；操作员可另行获得启停权限。目标替换实例后需要重新授权。</p><button type="button" data-load-remote-grants>读取远程实例</button><p class="small-note" data-remote-status></p><div data-remote-choices></div></fieldset>
  </div><div class="actions"><button class="primary" type="submit">保存账户</button><button type="button" data-account-new>新建账户</button></div><p class="error" role="alert"></p></form>`;
 const form = $('#account-form');
 const updateGrants = () => {
  const role = form.elements.role.value, admin = role === 'admin';
  $('#account-grants').hidden = admin; $('#account-read-permissions').hidden = admin; $('#account-log-grant').hidden = admin; $('#account-controls').hidden = role !== 'operator'; $('#account-actions').hidden = role !== 'operator'; $('#account-logs').hidden = admin || !form.elements.allowLogs.checked; $('#account-remote').hidden = admin;
  const visible = new Set([...form.querySelectorAll('[name=instance]:checked')].map(el => el.value));
  form.querySelectorAll('[name=controlInstance], [name=logInstance]').forEach(el => {el.disabled = !visible.has(el.value); if (el.disabled) el.checked = false;});
 };
 const paintRemoteChoices = () => {
  const host = form.querySelector('[data-remote-choices]');
  const operator = form.elements.role.value === 'operator';
  host.innerHTML = remoteChoices.size ? [...remoteChoices].map(([key, choice]) => `<div><label class="form-check"><input type="checkbox" name="remoteInstance" value="${esc(key)}" ${choice.selected?'checked':''}>${esc(choice.label)}</label>${operator ? `<label class="form-check"><input type="checkbox" name="remoteControl" value="${esc(key)}" ${choice.grant.control?'checked':''} ${choice.selected?'':'disabled'}>允许启停和重启</label>` : ''}</div>`).join('') : '<p class="small-note">尚无远程实例授权。</p>';
 };
 form.addEventListener('change', event => {
  const box = event.target;
  if (box.name !== 'remoteInstance' && box.name !== 'remoteControl') return;
  const choice = remoteChoices.get(box.value);
  if (!choice) return;
  if (box.name === 'remoteControl') {choice.grant.control = box.checked; return;}
  choice.selected = box.checked;
  if (box.checked) for (const [key, other] of remoteChoices) {
   if (key !== box.value && other.grant.nodeId === choice.grant.nodeId && other.grant.instanceId === choice.grant.instanceId) other.selected = false;
  }
  paintRemoteChoices();
 });
 form.querySelector('[data-load-remote-grants]').addEventListener('click', async event => {
  const button = event.currentTarget, status = form.querySelector('[data-remote-status]'), generation = authGeneration;
  button.disabled = true; status.textContent = '正在读取节点…';
  const controller = new AbortController(), timeout = setTimeout(() => controller.abort(), 60000);
  try {
   const list = await api('nodes', undefined, {signal:controller.signal});
   const nodes = (list.nodes || []).filter(node => node.addresses?.length);
   let failed = (list.nodes || []).length - nodes.length;
   for (let offset = 0; offset < nodes.length; offset += 4) {
    const batch = nodes.slice(offset, offset + 4);
    const results = await Promise.allSettled(batch.map(node => api('nodes/check', {id:node.id, stamp:node.stamp}, {signal:controller.signal})));
    if (!form.isConnected || generation !== authGeneration) return;
    results.forEach((result, index) => {
     const node = batch[index];
     if (result.status !== 'fulfilled' || result.value.id !== node.id || result.value.stamp !== node.stamp) {failed++; return;}
     for (const instance of result.value.summary?.instances || []) {
      if (!/^[a-f0-9]{64}$/.test(instance.binding || '')) continue;
      const grant = {nodeId:node.id, instanceId:instance.id, binding:instance.binding}, key = remoteKey(grant);
      const previous = remoteChoices.get(key);
      if (previous?.grant.control) grant.control = true;
      remoteChoices.set(key, {grant, label:`${node.name} / ${instance.name} · ${instance.kind}`, selected:previous?.selected || false});
     }
    });
   }
   if (form.isConnected && generation === authGeneration) {paintRemoteChoices(); status.textContent = failed ? `${failed} 个节点未能读取，请先检查地址或授权。` : '远程实例已读取；旧版目标可能无法提供实例身份。';}
  } catch (error) { if (form.isConnected && generation === authGeneration) status.textContent = error.name === 'AbortError' ? '读取超时，请重试。' : error.message; }
  finally {clearTimeout(timeout); if (form.isConnected) button.disabled = false;}
 });
 form.remoteGrantChoices = remoteChoices;
 paintRemoteChoices();
 form.elements.role.addEventListener('change', paintRemoteChoices);
 form.addEventListener('change', updateGrants); updateGrants();
}

$('#content').addEventListener('click', async e => {
 if (e.target.closest('[data-login-history]')) { openLoginHistory(); return; }
 if (e.target.closest('[data-oidc-settings]')) { openOIDCSettings(); return; }
 const instanceRead = e.target.closest('[data-instance-read]');
 if (instanceRead) {
  const mode = instanceRead.dataset.instanceRead;
  const id = instanceRead.dataset.id, runId = data?.runtime?.[id]?.runId || '', revision = data?.revision;
  const instance = data?.state.instances.find(item => item.id === id);
  const d = document.createElement('dialog'), output = document.createElement(mode === 'config' ? 'pre' : 'div'), close = document.createElement('button'), generation = authGeneration;
  d.id = 'instance-read-dialog';
  if (mode !== 'config') d.setAttribute('aria-label', mode === 'traffic-history' ? '流量历史' : mode === 'client-traffic' ? '规则流量' : '当前流量');
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
    else output.innerHTML = (mode === 'server-traffic' ? Overview.renderServerTraffic(value, esc) : mode === 'client-traffic' ? Overview.renderClientRuleTraffic(value, esc, true) : Overview.renderClientTraffic(value, esc)) + '<p class="small-note">再次打开可读取新快照。</p>';
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

function openOIDCSettings() {
 if (data?.identity?.role !== 'admin' || document.querySelector('#oidc-settings-dialog')) return;
 if (!accountData) {toast('请先等待读取本地账户。'); return;}
 const accounts = [...accountData.accounts], generation = authGeneration;
 const dialog = document.createElement('dialog');
 dialog.id = 'oidc-settings-dialog';
 dialog.innerHTML = '<h2>单点登录设置</h2><div data-oidc-content>正在读取…</div><p class="error" role="alert"></p><div class="dialog-actions"><button type="button" data-close>关闭</button></div>';
 document.body.appendChild(dialog);
 let submitting = false;
 const current = () => dialog.open && generation === authGeneration && data?.identity?.role === 'admin';
 const close = dialog.querySelector('[data-close]'), output = dialog.querySelector('[data-oidc-content]'), error = dialog.querySelector('.error');
 dialog.addEventListener('close', () => dialog.remove());
 dialog.addEventListener('cancel', e => {if (submitting) e.preventDefault();});
 close.addEventListener('click', () => {if (!submitting) dialog.close();});
 dialog.showModal();
 (async () => {
  try {
   const settings = await api('oidc/config');
   if (!current()) return;
   output.innerHTML = `<form id="oidc-settings-form"><p class="small-note">使用支持授权码和 PKCE 的公开客户端。发行者与提供方端点须为公网 HTTPS；不保存客户端密钥。只允许明确绑定的本地账户登录。</p><p class="small-note">在提供方登记此工作台当前使用的回调地址：<code data-callback></code>。使用不同端口或主机名时须分别登记。</p><div class="form-grid"><label class="form-check span-2"><input type="checkbox" name="enabled">启用单点登录</label><label class="span-2">发行者地址<input name="issuer" type="url" maxlength="1024" autocomplete="off"></label><label class="span-2">客户端 ID<input name="clientId" maxlength="512" autocomplete="off"></label><fieldset class="span-2"><legend>提供方账户绑定</legend><p class="small-note">填写提供方用户的准确 sub，并选择已有本地账户；不存在的账户需明确移除或更换。</p><div data-bindings></div><button type="button" data-add-binding>添加绑定</button></fieldset></div><p class="small-note">修改设置会使已登录的单点登录会话在下一次请求时失效。本地账户和管理令牌仍可使用。此配置不包含在备份和导出中。</p><button type="submit" class="primary">保存设置</button></form>`;
   const form = output.querySelector('form'), rows = form.querySelector('[data-bindings]'), save = form.querySelector('[type=submit]');
   form.querySelector('[data-callback]').textContent = location.origin + '/api/oidc/callback';
   form.elements.enabled.checked = settings.enabled === true;
   form.elements.issuer.value = settings.issuer || '';
   form.elements.clientId.value = settings.clientId || '';
   const addRow = binding => {
    if (rows.children.length >= 100) {error.textContent = '最多添加 100 个绑定。'; return;}
    const row = document.createElement('div'), id = binding.accountId || '';
    const missing = id && !accounts.some(account => account.id === id);
    row.className = 'form-grid';
    row.innerHTML = `<label>提供方用户 sub<input data-subject maxlength="255" value="${esc(binding.subject || '')}" autocomplete="off" required></label><label>本地账户<select data-account required><option value="">请选择账户</option>${missing ? `<option value="${esc(id)}" selected>不存在的账户 ${esc(id)}（需移除或更换）</option>` : ''}${accounts.map(account => `<option value="${esc(account.id)}" ${account.id === id ? 'selected' : ''}>${esc(account.username)}${account.enabled && !account.expired ? '' : '（不可登录）'}</option>`).join('')}</select></label><div class="span-2"><button type="button" data-remove-binding>移除此绑定</button></div>`;
    row.querySelector('[data-remove-binding]').addEventListener('click', () => row.remove());
    rows.appendChild(row);
   };
   (Array.isArray(settings.bindings) ? settings.bindings : []).forEach(addRow);
   form.querySelector('[data-add-binding]').addEventListener('click', () => addRow({}));
   form.addEventListener('submit', async e => {
    e.preventDefault(); if (submitting || !current()) return;
    const bindings = [...rows.children].map(row => ({subject:row.querySelector('[data-subject]').value,accountId:row.querySelector('[data-account]').value}));
    const request = {revision:settings.revision, enabled:form.elements.enabled.checked, issuer:form.elements.issuer.value.trim(), clientId:form.elements.clientId.value.trim(), bindings};
    const controller = new AbortController(), timeout = setTimeout(() => controller.abort(), 20000);
    submitting = true; save.disabled = true; close.disabled = true; error.textContent = '';
    try {
     await api('oidc/config', request, {signal:controller.signal});
     if (!current()) return;
     dialog.close(); toast('单点登录设置已保存；现有单点登录会话需重新登录。');
     stateETag = ''; void refresh(true);
    } catch (err) {
     if (!current()) return;
     const uncertain = err.name === 'AbortError' || !err.status || err.status >= 500;
     error.textContent = uncertain ? '保存结果尚未确认，请关闭窗口后重新打开并核对配置。' : err.message;
     if (!uncertain) save.disabled = false;
    } finally {clearTimeout(timeout); submitting = false; close.disabled = false;}
   });
  } catch (err) {if (current()) error.textContent = '读取失败：' + err.message;}
 })();
}

$('#content').addEventListener('submit', async e => {
 if (e.target.id !== 'account-form' || !accountData) return;
 e.preventDefault();
 const form = e.target, button = form.querySelector('[type=submit]'), error = form.querySelector('.error');
 const role = form.elements.role.value;
 const localExpiry = form.elements.expiresAt.value;
 const expiresAt = localExpiry ? (localExpiry.length === 16 ? localExpiry + ':00Z' : localExpiry + 'Z') : '';
 const request = {action:'save', revision:accountData.revision, id:form.elements.id.value, username:form.elements.username.value,
  role, enabled:form.elements.enabled.checked, expiresAt, password:form.elements.password.value,
  instances:role === 'admin' ? [] : [...form.querySelectorAll('[name=instance]:checked')].map(el => el.value),
  controlInstances:role === 'operator' ? [...form.querySelectorAll('[name=controlInstance]:checked')].map(el => el.value) : [], allowLogs:role !== 'admin' && form.elements.allowLogs.checked,
  allowedActions:role === 'operator' ? [...form.querySelectorAll('[name=allowedAction]:checked')].map(el => el.value) : [],
  readPermissions:role === 'admin' ? null : {config:form.elements.viewConfig.checked, traffic:form.elements.viewTraffic.checked},
  logInstances:role !== 'admin' && form.elements.allowLogs.checked ? [...form.querySelectorAll('[name=logInstance]:checked')].map(el => el.value) : [],
  remoteInstances:role === 'admin' ? [] : [...form.remoteGrantChoices.values()].filter(choice => choice.selected).map(choice => ({...choice.grant, control:role === 'operator' && !!choice.grant.control}))};
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
