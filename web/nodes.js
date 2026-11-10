'use strict';
// A node credential is shown once on the target and never included in a list.
titles.nodes = '节点管理';
const nodesBaseRender = render;
const nodesBaseResetSession = resetSession;
let nodesModel = null, nodeResults = Object.create(null), nodeLoading = false;
let nodePageGeneration = 0, nodeDialogGeneration = 0, nodeRequest = null, nodeBusy = false;
let nodesDialog = null, nodeIssued = '', nodeReview = null, nodeIssuing = false, nodeLogoutPending = false;
const nodeCheckRequests = new Set();
// Each submitted action has its own key so different tabs cannot overwrite or
// clear one another's pending results. No names or credentials are persisted.
const nodeControlNoticePrefix = 'kurumi-node-control-';
let nodeControlNotices = [], localNodeControl = null;
function syncNodeControlNotices() {
 const notices = [];
 try {
  for (let i=0; i<localStorage.length; i++) {
   const key = localStorage.key(i);
   if (!key || !key.startsWith(nodeControlNoticePrefix)) continue;
   let saved = null;
   try { saved = JSON.parse(localStorage.getItem(key)); } catch (_) { /* Keep a damaged record visible. */ }
   const valid = saved && /^[a-f0-9]{16}$/.test(saved.nodeId) && /^[a-f0-9]{16}$/.test(saved.instanceId) && ['start','stop','restart'].includes(saved.action);
   notices.push(valid ? {key,nodeId:saved.nodeId,instanceId:saved.instanceId,action:saved.action,phase:'uncertain'} : {key,phase:'uncertain'});
  }
 } catch (_) { notices.push({phase:'unavailable'}); }
 if (localNodeControl) {
  const index = notices.findIndex(n => n.key === localNodeControl.key);
  if (index < 0) notices.push(localNodeControl); else notices[index] = localNodeControl;
 }
 nodeControlNotices = notices;
}
function beginNodeControlNotice(node, instance, action) {
 let key = '', created = false;
 try {
  const id = Array.from(crypto.getRandomValues(new Uint8Array(16)), b => b.toString(16).padStart(2,'0')).join('');
  key = nodeControlNoticePrefix + id;
  if (localStorage.getItem(key) !== null) throw new Error('duplicate notice');
  const record = {nodeId:node.id,instanceId:instance.id,action};
  const encoded = JSON.stringify(record);
  localStorage.setItem(key,encoded);
  created = true;
  if (localStorage.getItem(key) !== encoded) throw new Error('storage unavailable');
  localNodeControl = {key,...record,nodeName:node.name,instanceName:instance.name,phase:'pending'};
  syncNodeControlNotices();
  return localNodeControl;
 } catch (_) {
  if (created) { try { localStorage.removeItem(key); } catch (_) {} }
  syncNodeControlNotices();
  return null;
 }
}
function finishNodeControlNotice(pending, succeeded) {
 if (localNodeControl?.key !== pending.key) return;
 if (succeeded) {
  try {
   localStorage.removeItem(pending.key);
   if (localStorage.getItem(pending.key) === null) localNodeControl = null;
  } catch (_) { /* Leave a conservative notice if the record cannot be cleared. */ }
 }
 if (localNodeControl) {
  localNodeControl.phase = 'uncertain';
  try { localStorage.setItem(pending.key,JSON.stringify({nodeId:pending.nodeId,instanceId:pending.instanceId,action:pending.action})); } catch (_) {}
 }
 syncNodeControlNotices();
}
function paintNodeControlNotice() {
 const host = $('#node-control-notice'); if (!host) return;
 if (!nodeControlNotices.length) { host.hidden = true; host.replaceChildren(); return; }
 host.hidden = false;
 host.innerHTML = nodeControlNotices.map(notice => {
  if (notice.phase === 'unavailable') return '<p>无法读取本地核对记录，暂不能提交远程操作。请检查浏览器本地存储。</p>';
  const node = nodesModel?.nodes.find(n => n.id === notice.nodeId);
  const instance = nodeResults[notice.nodeId]?.summary?.instances.find(i => i.id === notice.instanceId);
  const target = esc((notice.nodeName || node?.name || (notice.nodeId ? '节点 ' + notice.nodeId : '未知节点')) + ' / ' + (notice.instanceName || instance?.name || (notice.instanceId ? '实例 ' + notice.instanceId : '未知实例')));
  const action = ({start:'启动',stop:'停止',restart:'重启'})[notice.action] || '操作';
  return notice.phase === 'pending'
   ? `<p>正在等待 ${target} 的${action}结果。关闭页面或断线不能保证取消。</p>`
   : `<p>${target} 的${action}结果未确认或核对记录尚未清除。请核对目标状态，再决定是否重试。 <button type="button" data-node-action="ack-control" data-notice-key="${esc(notice.key)}">核对并解除</button></p>`;
 }).join('');
}
syncNodeControlNotices();
window.addEventListener('storage', event => {
 if (event.key === null || event.key.startsWith(nodeControlNoticePrefix)) { syncNodeControlNotices(); paintNodes(); }
});

window.KurumiNodeNavigationBlocked = () => Boolean(nodesDialog?.open && (nodeIssuing || nodeIssued));
window.KurumiDeferNodeLogout = () => {
 if (!nodesDialog?.open || (!nodeIssuing && !nodeIssued)) return false;
 nodeLogoutPending = true;
 return true;
};

render = function () {
 nodesBaseRender();
 if (data && page === 'nodes') mountNodes();
 else leaveNodes();
};
resetSession = function () {
 nodeIssuing = false; nodeLogoutPending = false;
 leaveNodes(); nodesModel = null; nodeResults = Object.create(null); nodeIssued = ''; nodeReview = null;
 if (nodesDialog?.open) nodesDialog.close();
 nodesBaseResetSession();
};

function leaveNodes() {
 if (!nodesModel && !nodeRequest && !nodesDialog?.open) return;
 ++nodePageGeneration;
 nodeRequest?.abort(); nodeRequest = null; nodeLoading = false;
 for (const controller of nodeCheckRequests) controller.abort();
 nodeCheckRequests.clear();
 nodesModel = null; nodeResults = Object.create(null); nodeIssued = ''; nodeReview = null;
 if (nodesDialog?.open) nodesDialog.close();
}
function mountNodes() {
 if ($('#nodes-screen')) return;
 $('#content').innerHTML = header('节点管理','按需查看或控制已授权远程工作台的实例。') + `
 <div id="nodes-screen">
  <div id="node-control-notice" class="notice" role="alert" hidden></div>
  <section class="panel"><div class="panel-title"><div><h3>远程节点</h3><p>实例状态不代表代理已注册或服务可访问。</p></div><div class="actions"><button data-node-action="reload">刷新列表</button><button class="primary" data-node-action="new">＋ 添加节点</button></div></div>
   <div class="panel-content"><p class="small-note">先在目标工作台生成只读授权，添加后核对解析地址。检查只连接已确认地址，并验证 HTTPS 证书。</p><div id="nodes-list" role="status">正在读取…</div></div></section>
  <section class="panel"><div class="panel-title"><div><h3>允许其他工作台查看本机</h3><p>独立只读授权仅用于查看本机所有实例的名称、类型和运行状态。</p></div><span id="node-sharing" class="pill">读取中</span></div>
   <div class="panel-content"><p class="small-note">工作台仍只监听本机。需由管理员配置受信任的 HTTPS 反向代理；只读与控制使用不同授权。</p><div class="actions"><button data-node-action="issue">生成 / 更换只读授权</button><button class="danger" data-node-action="revoke">撤销只读授权</button></div><p id="node-page-error" class="error" role="alert"></p></div></section>
  <section class="panel"><div class="panel-title"><div><h3>允许远程启停和重启</h3><p>单独授权，默认关闭。持有者可操作本机全部实例。</p></div><span id="node-control-sharing" class="pill">读取中</span></div>
   <div class="panel-content"><p class="small-note">仅向可信工作台提供此授权；生成新授权会撤销旧授权。已受理的操作可能在撤销后完成。</p><div class="actions"><button data-node-action="issue-control">生成 / 更换控制授权</button><button class="danger" data-node-action="revoke-control">撤销控制授权</button></div></div></section>
 </div>`;
 paintNodeControlNotice();
 loadNodes();
}
function ensureNodesDialog() {
 if (nodesDialog) return;
 nodesDialog = document.createElement('dialog');
 nodesDialog.id = 'nodes-dialog';
 window.KurumiNodeDialog = nodesDialog;
 document.body.appendChild(nodesDialog);
 nodesDialog.addEventListener('cancel', e => { if (nodeBusy) e.preventDefault(); });
 nodesDialog.addEventListener('close', () => {
  if (!nodesDialog.open) {
   ++nodeDialogGeneration; nodeIssued = ''; nodeReview = null; nodesDialog.innerHTML = '';
   if (nodeLogoutPending) { nodeLogoutPending = false; showLogin(); }
   else scheduleRefresh(0);
  }
 });
 nodesDialog.addEventListener('click', e => {
  const button = e.target.closest('button[data-node-dialog]');
  if (!button) return;
  if (button.dataset.nodeDialog === 'close' && !nodeBusy) nodesDialog.close();
  if (button.dataset.nodeDialog === 'select') {
   const input = nodesDialog.querySelector('textarea');
   if (input) { input.focus(); input.select(); }
  }
 });
 nodesDialog.addEventListener('submit', saveNodeForm);
 nodesDialog.addEventListener('submit', confirmNodeAddresses);
}
function paintNodes() {
 if (page !== 'nodes' || !$('#nodes-screen')) return;
 syncNodeControlNotices();
 paintNodeControlNotice();
 const list = $('#nodes-list'), sharing = $('#node-sharing'), controlSharing = $('#node-control-sharing');
 const controls = [...$('#nodes-screen').querySelectorAll('button[data-node-action]')];
 if (!nodesModel) {
  list.textContent = nodeLoading ? '正在读取节点列表…' : '节点列表已过期或未知，请刷新。';
  sharing.textContent = '状态未知'; sharing.className = 'pill';
  controlSharing.textContent = '状态未知'; controlSharing.className = 'pill';
  controls.forEach(button => {const allowed=button.dataset.nodeAction==='reload' || button.dataset.nodeAction==='ack-control';button.disabled = !allowed || nodeBusy || nodeLoading;});
  return;
 }
 sharing.textContent = nodesModel.sharing ? '已允许查看' : '未启用';
 sharing.className = 'pill' + (nodesModel.sharing ? ' good' : '');
 controlSharing.textContent = nodesModel.controlSharing ? '允许远程控制' : '未启用';
 controlSharing.className = 'pill' + (nodesModel.controlSharing ? ' warn' : '');
 controls.forEach(button => { button.disabled = nodeBusy || nodeLoading || (button.dataset.nodeAction === 'revoke' && !nodesModel.sharing) || (button.dataset.nodeAction === 'revoke-control' && !nodesModel.controlSharing); });
 if (!nodesModel.nodes.length) { list.innerHTML = '<p class="muted">尚未添加远程节点。</p>'; return; }
 list.innerHTML = nodesModel.nodes.map(node => {
  const state = nodeResults[node.id];
  let detail = '<p class="small-note">尚未检查。状态只在点击检查时更新。</p>';
  if (state?.loading) detail = '<p class="small-note">正在检查…</p>';
  else if (state?.error) detail = `<p class="error">${esc(state.error)}</p>`;
  else if (state?.summary) {
   const labels = {running:'运行中',stopped:'已停止',exited:'已退出','autostart-failed':'自动启动失败'};
   const instances = state.summary.instances;
   detail = `<p class="small-note">检查于 ${esc(new Date(state.checkedAt).toLocaleString('zh-CN'))} · 工作台 ${esc(state.summary.version)} · ${instances.length} 个实例</p>` +
    (instances.length ? `<ul class="node-instances">${instances.map(i=>{
     const running = i.state === 'running', action = running ? 'stop' : 'start';
     const disabled = nodeBusy||nodeLoading||nodeControlNotices.length?'disabled':'';
     const buttons = node.hasControl ? `<div class="actions"><button type="button" data-node-action="control-${action}" data-node-id="${esc(node.id)}" data-instance-id="${esc(i.id)}" ${disabled}>${running?'停止':'启动'}</button>${running?`<button type="button" data-node-action="control-restart" data-node-id="${esc(node.id)}" data-instance-id="${esc(i.id)}" ${disabled}>重启</button>`:''}</div>` : '';
     return `<li><strong>${esc(i.name)}</strong><span>${esc(i.kind)} · ${esc(labels[i.state] || '状态未知')}</span>${buttons}</li>`;
    }).join('')}</ul>` : '<p class="small-note">目标没有实例。</p>');
  }
  const addresses = node.addresses || [];
  const pin = addresses.length ? '已确认：' + addresses.join('、') : '尚未确认解析地址';
  return `<article class="node-card"><div><h3>${esc(node.name)}</h3><code>${esc(node.url)}</code><p class="small-note">${esc(pin)}</p></div><div class="actions"><button data-node-action="resolve" data-node-id="${esc(node.id)}" ${nodeBusy||nodeLoading?'disabled':''}>核对地址</button><button data-node-action="check" data-node-id="${esc(node.id)}" ${nodeBusy||nodeLoading||state?.loading||!addresses.length?'disabled':''}>检查状态</button><button data-node-action="edit" data-node-id="${esc(node.id)}" ${nodeBusy||nodeLoading?'disabled':''}>编辑</button><button class="danger" data-node-action="delete" data-node-id="${esc(node.id)}" ${nodeBusy||nodeLoading?'disabled':''}>删除</button></div><div class="node-detail">${detail}</div></article>`;
 }).join('');
}
async function loadNodes() {
 if (nodeLoading || page !== 'nodes' || !data) return;
 const generation = nodePageGeneration, session = authGeneration, controller = new AbortController();
 nodeRequest = controller; nodeLoading = true; paintNodes();
 const timeout = setTimeout(() => controller.abort(), 10000);
 try {
  const result = await api('nodes', undefined, {signal:controller.signal});
  if (!result || !Array.isArray(result.nodes) || !Number.isSafeInteger(result.revision)) throw new Error('节点列表格式无效');
  if (nodeRequest !== controller || generation !== nodePageGeneration || session !== authGeneration || page !== 'nodes') return;
  const previous = nodesModel;
  nodesModel = result;
  if (previous && previous.revision !== result.revision) nodeResults = Object.create(null);
  else if (previous) for (const id of Object.keys(nodeResults)) {
   if (!result.nodes.some(n => n.id === id && previous.nodes.some(old => old.id === id && old.stamp === n.stamp))) delete nodeResults[id];
  }
  const error = $('#node-page-error'); if (error) error.textContent = '';
 } catch (error) {
  if (nodeRequest === controller && generation === nodePageGeneration && session === authGeneration && page === 'nodes') {
   nodesModel = null; nodeResults = Object.create(null);
   const status = $('#node-page-error'); if (status) status.textContent = error.name === 'AbortError' ? '读取超时，请刷新列表。' : error.message;
  }
 } finally {
  clearTimeout(timeout);
  if (nodeRequest === controller) {
   nodeRequest = null;
   if (generation === nodePageGeneration) { nodeLoading = false; paintNodes(); }
  }
 }
}
function markNodesUnknown() {
 nodesModel = null; nodeResults = Object.create(null); paintNodes();
}
function refreshNodesAfterMutation() {
 const generation = nodePageGeneration, session = authGeneration;
 nodeRequest?.abort(); nodeRequest = null; nodeLoading = false;
 markNodesUnknown();
 // Do not discard the once-only credential if an immediate refresh loses authentication.
 if (!(nodeIssued && nodesDialog?.open)) void refresh(true).catch(() => {});
 void loadNodes().catch(() => {
  if (generation !== nodePageGeneration || session !== authGeneration || page !== 'nodes') return;
  markNodesUnknown();
  const status = $('#node-page-error'); if (status) status.textContent = '节点列表刷新失败，请重新刷新。';
 });
}
function openNodeForm(node) {
 if (!nodesModel || nodeBusy || nodeLoading) return;
 ensureNodesDialog();
 if (nodesDialog.open) return;
 const generation = ++nodeDialogGeneration;
 const value = node || {id:'', name:'', url:''};
 nodesDialog.innerHTML = `<form id="node-form" data-id="${esc(value.id)}" data-revision="${Number(nodesModel.revision)}" data-generation="${generation}">
 <h2>${node?'编辑节点':'添加节点'}</h2><p class="muted">只能使用可信 HTTPS 地址；授权由目标工作台生成，与本机登录令牌不同。</p>
 <label>名称（最多 64 个字符）<input name="name" maxlength="128" required value="${esc(value.name)}"></label>
 <label>HTTPS 地址<input name="url" type="url" maxlength="512" required value="${esc(value.url)}" placeholder="https://node.example.com"></label>
 <label>目标的只读授权${node?'（留空保留；更换地址时须重新输入）':''}<input name="credential" type="password" maxlength="64" minlength="64" autocomplete="new-password" ${node?'':'required'}></label>
 <label>目标的控制授权（可选${node?.hasControl?'，留空保留':''}）<input name="controlCredential" type="password" maxlength="64" minlength="64" autocomplete="new-password"></label>
 ${node?.hasControl?'<label class="form-check"><input name="clearControl" type="checkbox">清除本机保存的控制授权</label>':''}
 <p class="small-note">更换 HTTPS 地址时会清除原控制授权，需重新输入并核对解析地址。控制授权与只读授权不同；验证系统信任的 TLS 证书，不使用系统代理和重定向。</p>
 <p class="error" role="alert"></p><div class="dialog-actions"><button type="button" data-node-dialog="close">取消</button><button type="submit" class="primary">保存</button></div></form>`;
 stopRefreshPolling(); nodesDialog.showModal();
}
async function nodeMutation(payload, path='nodes') {
 const controller = new AbortController(), timeout = setTimeout(() => controller.abort(), 20000);
 try { return await api(path,payload,{signal:controller.signal}); }
 finally { clearTimeout(timeout); }
}
async function saveNodeForm(event) {
 if (!event.target.matches('#node-form')) return;
 event.preventDefault();
 if (nodeBusy || !nodesModel) return;
 const form = event.target, generation = nodeDialogGeneration, session = authGeneration;
 const payload = {revision:Number(form.dataset.revision), action:'save', id:form.dataset.id, name:form.elements.name.value.trim(), url:form.elements.url.value.trim(), credential:form.elements.credential.value.trim(), controlCredential:form.elements.controlCredential.value.trim(), clearControl:!!form.elements.clearControl?.checked};
 if (Array.from(payload.name).length > 64) { form.querySelector('.error').textContent = '名称最多 64 个字符。'; return; }
 nodeBusy = true; form.querySelector('button[type="submit"]').disabled = true;
 form.querySelector('.error').textContent = '';
 try {
  await nodeMutation(payload);
  if (generation !== nodeDialogGeneration || session !== authGeneration) return;
  nodesDialog.close();
  refreshNodesAfterMutation();
  if (session === authGeneration) toast('节点已保存，请核对地址后检查状态。');
 } catch (error) {
  if (generation !== nodeDialogGeneration || session !== authGeneration || !nodesDialog.open) return;
  form.querySelector('.error').textContent = error.status === 409 ? '配置已变化，请关闭并重新打开表单。' : error.status ? error.message : '保存结果未确认，请刷新列表并核对后重试。';
  if (error.status === 409 || !error.status) await loadNodes();
 } finally {
  nodeBusy = false;
  if (generation === nodeDialogGeneration && nodesDialog.open) form.querySelector('button[type="submit"]').disabled = false;
  paintNodes();
 }
}
function showIssuedNodeCredential(value, control=false) {
 ensureNodesDialog();
 ++nodeDialogGeneration; nodeIssued = value;
 nodesDialog.innerHTML = `<h2>${control?'控制授权':'只读授权'}已生成</h2><p>旧授权已失效。${control?'持有者可远程启动、停止和重启本机全部实例。':''}此授权只显示一次；请复制并通过安全渠道交给获准的工作台。丢失后可重新生成。</p><label>授权<textarea readonly rows="3" aria-label="${control?'控制授权':'只读授权'}"></textarea></label><div class="dialog-actions"><button type="button" data-node-dialog="select">选择授权</button><button class="primary" type="button" data-node-dialog="close">已保存，关闭</button></div>`;
 nodesDialog.querySelector('textarea').value = nodeIssued;
 stopRefreshPolling(); if (!nodesDialog.open) nodesDialog.showModal();
}
async function mutateNodes(action, node) {
 if (!nodesModel || nodeBusy || nodeLoading) return;
 const issuing = action === 'issue' || action === 'issue-control';
 const prompt = action === 'issue' ? '生成新的只读授权？旧授权会立即失效。新授权只显示一次。' : action === 'issue-control' ? '生成新的控制授权？持有者可远程启动、停止和重启本机全部实例。旧控制授权会立即失效。' : action === 'revoke' ? '撤销本机只读授权？所有使用它的控制端将无法再查看状态。' : action === 'revoke-control' ? '撤销本机控制授权？已经受理的操作可能仍会完成。' : `从本机删除节点「${node.name}」？这不会撤销目标节点上的授权。`;
 if (!confirm(prompt)) return;
 const generation = nodePageGeneration, session = authGeneration;
 let issuedVisible = false;
 nodeBusy = true; paintNodes();
 if (issuing) {
  nodeIssuing = true;
  ensureNodesDialog(); ++nodeDialogGeneration;
  nodesDialog.innerHTML = '<h2>正在生成授权</h2><p>请等待结果。关闭页面可能导致新授权丢失，旧授权仍可能已被撤销。</p>';
  stopRefreshPolling(); stateRequest?.controller.abort(); nodesDialog.showModal();
 }
 try {
  const result = await nodeMutation({revision:nodesModel.revision,action,id:node?.id || ''});
  if (generation !== nodePageGeneration || session !== authGeneration || page !== 'nodes') return;
  if (action === 'delete') delete nodeResults[node.id];
  if (issuing) {
   if (!/^[a-f0-9]{64}$/.test(result.credential || '')) throw new Error('授权响应无效');
   showIssuedNodeCredential(result.credential, action === 'issue-control'); issuedVisible = true;
  }
  refreshNodesAfterMutation();
  if (!issuing) toast(action === 'revoke' ? '已撤销本机只读授权。' : action === 'revoke-control' ? '已撤销本机控制授权。' : '已从本机删除节点。');
 } catch (error) {
  if (generation !== nodePageGeneration || session !== authGeneration || page !== 'nodes') return;
  if (issuedVisible) { markNodesUnknown(); const status=$('#node-page-error'); if(status)status.textContent='授权已生成，但列表刷新失败，请刷新列表。'; return; }
  const message = !error.status && issuing ? '生成结果未确认，旧授权可能已失效。请刷新状态并重新生成。' : error.status === 409 ? '配置已变化，请刷新后重试。' : !error.status ? '结果未确认，请刷新列表核对后再操作。' : error.message;
  $('#node-page-error').textContent = message;
  if (issuing && nodesDialog?.open) nodesDialog.innerHTML = `<h2>无法确认授权</h2><p class="error">${esc(message)}</p><div class="dialog-actions"><button type="button" data-node-dialog="close">关闭</button></div>`;
  if (error.status === 409 || !error.status) { await loadNodes(); if ($('#node-page-error')) $('#node-page-error').textContent = message; }
 } finally { nodeIssuing = false; nodeBusy = false; paintNodes(); }
}
async function reviewNodeAddresses(node) {
 if (nodeBusy || nodeLoading || !nodesModel) return;
 ensureNodesDialog(); if (nodesDialog.open) return;
 const generation = ++nodeDialogGeneration, pageGeneration = nodePageGeneration, session = authGeneration;
 const controller = new AbortController(), timeout = setTimeout(() => controller.abort(), 9000);
 nodeCheckRequests.add(controller); nodeBusy = true; nodeReview = null;
 nodesDialog.innerHTML = '<h2>正在解析节点地址</h2><p>请稍候；此步骤不会发送节点授权。</p>';
 stopRefreshPolling(); nodesDialog.showModal(); paintNodes();
 try {
  const result = await api('nodes/resolve',{id:node.id,stamp:node.stamp},{signal:controller.signal});
  if (generation !== nodeDialogGeneration || pageGeneration !== nodePageGeneration || session !== authGeneration || !nodesDialog.open) return;
  if (result.id !== node.id || result.stamp !== node.stamp || !Array.isArray(result.addresses) || !result.addresses.length || !result.addresses.every(a=>typeof a === 'string') || !Number.isSafeInteger(result.revision)) throw new Error('解析结果无效，请重试。');
  nodeReview = {id:node.id,stamp:node.stamp,revision:result.revision,addresses:result.addresses.slice()};
  const previous = (node.addresses || []).length ? node.addresses.join('、') : '尚未确认';
  const addresses = result.addresses.map(address=>`<li><code>${esc(address)}</code></li>`).join('');
  nodesDialog.innerHTML = `<form id="node-address-form"><h2>核对节点地址</h2><p>请确认这些地址属于「${esc(node.name)}」（${esc(node.url)}）。只应信任你认识的目标；内网地址也可使用。</p><p class="small-note">之前确认：${esc(previous)}</p><p>当前解析：</p><ul class="node-addresses">${addresses}</ul><p class="small-note">地址发生变化时会暂停发送授权，需要再次核对。服务商轮换地址可能需要重新确认；连接时仍会验证 HTTPS 证书。</p><p class="error" role="alert"></p><div class="dialog-actions"><button type="button" data-node-dialog="close">取消</button><button class="primary" type="submit">确认这些地址</button></div></form>`;
 } catch (error) {
  if (generation !== nodeDialogGeneration || pageGeneration !== nodePageGeneration || session !== authGeneration || !nodesDialog.open) return;
  const message = error.name === 'AbortError' ? '解析超时，请检查网络后重试。' : error.message;
  nodesDialog.innerHTML = `<h2>无法核对地址</h2><p class="error">${esc(message)}</p><div class="dialog-actions"><button type="button" data-node-dialog="close">关闭</button></div>`;
  if (error.status === 409) refreshNodesAfterMutation();
 } finally {
  clearTimeout(timeout); nodeCheckRequests.delete(controller); nodeBusy = false; paintNodes();
 }
}
async function confirmNodeAddresses(event) {
 if (!event.target.matches('#node-address-form')) return;
 event.preventDefault();
 if (nodeBusy || !nodeReview) return;
 const review = nodeReview, form = event.target, generation = nodeDialogGeneration, session = authGeneration;
 const submit = form.querySelector('button[type="submit"]');
 nodeBusy = true; submit.disabled = true; form.querySelector('.error').textContent = '';
 try {
  await nodeMutation(review,'nodes/confirm');
  if (generation !== nodeDialogGeneration || session !== authGeneration || !nodesDialog.open) return;
  nodeReview = null; nodesDialog.close(); refreshNodesAfterMutation();
  toast('地址已确认，可检查节点状态。');
 } catch (error) {
  if (generation !== nodeDialogGeneration || session !== authGeneration || !nodesDialog.open) return;
  const uncertain = !error.status;
  form.querySelector('.error').textContent = uncertain ? '确认结果未明，请关闭后刷新并重新核对。' : error.status === 409 ? '地址或节点已变化，请关闭后重新核对。' : error.message;
  if (uncertain || error.status === 409) { nodeReview = null; refreshNodesAfterMutation(); }
 } finally {
  nodeBusy = false;
  if (generation === nodeDialogGeneration && nodesDialog.open && nodeReview) submit.disabled = false;
  paintNodes();
 }
}
async function controlNode(node, instance, action) {
 syncNodeControlNotices();
 if (nodeBusy || nodeLoading || nodeControlNotices.length || !node.hasControl || !node.addresses?.length) { paintNodes(); return; }
 const generation = nodePageGeneration, session = authGeneration;
 nodeBusy = true; paintNodes();
 let pending = null;
 try {
  const controller = new AbortController(), timeout = setTimeout(()=>controller.abort(),9000);
  let result;
  try { result = await api('nodes/control',{id:node.id,stamp:node.stamp,instanceId:instance.id,action:'preview',version:''},{signal:controller.signal}); }
  finally { clearTimeout(timeout); }
  if (generation !== nodePageGeneration || session !== authGeneration || page !== 'nodes' || !nodesModel?.nodes.some(n=>n.id===node.id && n.stamp===node.stamp)) return;
  const preview = result.preview;
  if (result.id!==node.id || result.stamp!==node.stamp || !preview || preview.id!==instance.id || !/^[a-f0-9]{64}$/.test(preview.version || '')) throw new Error('目标状态无效，请重新检查。');
  const running = preview.state === 'running';
  if (preview.name !== instance.name || preview.kind !== instance.kind || (action === 'start' && running) || (action !== 'start' && !running)) { delete nodeResults[node.id]; toast('目标实例信息或状态已变化，请重新检查。'); return; }
  const label = ({start:'启动',stop:'停止',restart:'重启'})[action];
  const stateLabel = ({running:'运行中',stopped:'已停止',exited:'已退出','autostart-failed':'自动启动失败'})[preview.state] || '状态未知';
  if (!confirm(`在「${node.name}」${label}实例「${preview.name}」（${preview.kind}）？\n当前状态：${stateLabel}。${action === 'restart' ? '重启会应用目标上的当前草稿；停止后如果启动失败，实例可能保持停止。' : ''}目标实例如有启用中的定时计划，此操作受理后会暂停计划，须在目标工作台重新启用。提交后连接中断不能保证取消；结果不明时请先到目标核对。`)) return;
  syncNodeControlNotices();
  if (nodeControlNotices.length) { toast('有尚未核对的远程操作，请先核对。'); return; }
  pending = beginNodeControlNotice(node, preview, action);
  if (!pending) throw new Error('无法保存远程操作核对记录，请启用浏览器本地存储后重试。');
  paintNodes();
  const sendController = new AbortController(), sendTimeout = setTimeout(()=>sendController.abort(),45000);
  let applied;
  try { applied = await api('nodes/control',{id:node.id,stamp:node.stamp,instanceId:instance.id,action,version:preview.version,confirmed:true},{signal:sendController.signal}); }
  finally { clearTimeout(sendTimeout); }
  if (!applied?.ok || applied.id!==node.id || applied.stamp!==node.stamp || applied.instanceId!==instance.id) throw new Error('目标结果无效');
  finishNodeControlNotice(pending, true);
  if (session === authGeneration && generation === nodePageGeneration && page === 'nodes') {
   delete nodeResults[node.id]; toast(`目标已受理${label}请求，请重新检查状态。`);
  }
 } catch (error) {
  if (pending) finishNodeControlNotice(pending, false);
  if (session === authGeneration && generation === nodePageGeneration && page === 'nodes') {
   if (pending) toast('远程操作结果未确认，请先到目标工作台核对。');
   else toast(error.name === 'AbortError' ? '读取目标状态超时，请稍后重试。' : error.message);
  }
 } finally { nodeBusy = false; paintNodes(); }
}
async function reviewNodeControlNotice(key) {
 syncNodeControlNotices();
 const notice = nodeControlNotices.find(n => n.key === key);
 if (!notice || notice.phase !== 'uncertain' || nodeBusy || nodeLoading) return;
 const generation = nodePageGeneration, session = authGeneration;
 const node = nodesModel?.nodes.find(n => n.id === notice.nodeId);
 let message = '无法在线核对目标。';
 nodeBusy = true; paintNodes();
 try {
  try {
   if (node?.hasControl && node.addresses?.length && notice.instanceId) {
    const controller = new AbortController(), timeout = setTimeout(()=>controller.abort(),9000);
    try {
     const result = await api('nodes/control',{id:node.id,stamp:node.stamp,instanceId:notice.instanceId,action:'preview',version:''},{signal:controller.signal});
     if (generation !== nodePageGeneration || session !== authGeneration || page !== 'nodes') return;
     const preview = result.preview, state = ({running:'运行中',stopped:'已停止',exited:'已退出','autostart-failed':'自动启动失败'})[preview?.state];
     if (result.id !== node.id || result.stamp !== node.stamp || preview?.id !== notice.instanceId || !state || !nodesModel?.nodes.some(n => n.id === node.id && n.stamp === node.stamp)) throw new Error('目标状态无效');
     message = `目标「${node.name}」的实例「${preview.name}」当前${state}；这次读取时没有已预约操作。`;
    } finally { clearTimeout(timeout); }
   }
  } catch (_) {
   if (generation !== nodePageGeneration || session !== authGeneration || page !== 'nodes') return;
   message = '无法在线核对目标或目标正在处理操作。';
  }
  if (generation !== nodePageGeneration || session !== authGeneration || page !== 'nodes') return;
  syncNodeControlNotices();
  if (!nodeControlNotices.some(n => n.key === key && n.phase === 'uncertain')) return;
  if (!confirm(`${message}\n先前请求仍可能在网络中，稍后才到达。请独立核对目标及请求是否仍会送达，再解除记录。确认已核对？`)) return;
  try {
   localStorage.removeItem(key);
   if (localStorage.getItem(key) !== null) throw new Error('storage unavailable');
   if (localNodeControl?.key === key) localNodeControl = null;
   syncNodeControlNotices();
  } catch (_) { toast('无法清除核对记录，请检查浏览器本地存储。'); }
 }
 finally { nodeBusy = false; paintNodes(); }
}
async function checkNode(node) {
 if (nodeBusy || nodeLoading || nodeResults[node.id]?.loading) return;
 const generation = nodePageGeneration, session = authGeneration, controller = new AbortController();
 const pending = {loading:true}; nodeResults[node.id] = pending; nodeCheckRequests.add(controller); paintNodes();
 const timeout = setTimeout(() => controller.abort(), 9000);
 try {
  const result = await api('nodes/check',{id:node.id,stamp:node.stamp},{signal:controller.signal});
  if (generation !== nodePageGeneration || session !== authGeneration || page !== 'nodes' || nodeResults[node.id] !== pending || !nodesModel?.nodes.some(n=>n.id===node.id && n.stamp===node.stamp)) return;
  if (result.id !== node.id || result.stamp !== node.stamp) { nodeResults[node.id] = {error:'节点信息已变化，请刷新列表并重试。'}; return; }
  nodeResults[node.id] = result;
 } catch (error) {
  if (generation !== nodePageGeneration || session !== authGeneration || page !== 'nodes' || nodeResults[node.id] !== pending) return;
  nodeResults[node.id] = {error:error.name === 'AbortError' ? '检查超时，请检查网络后重试。' : error.message};
 } finally { clearTimeout(timeout); nodeCheckRequests.delete(controller); if (generation === nodePageGeneration) paintNodes(); }
}
$('#content').addEventListener('click', event => {
 const button = event.target.closest('[data-node-action]');
 if (!button || page !== 'nodes') return;
 const action = button.dataset.nodeAction, node = nodesModel?.nodes.find(n => n.id === button.dataset.nodeId);
 if (action === 'ack-control') {
  void reviewNodeControlNotice(button.dataset.noticeKey);
 }
 else if (action === 'reload') loadNodes();
 else if (action === 'new') openNodeForm(null);
 else if (action === 'edit' && node) openNodeForm(node);
 else if (action === 'check' && node) checkNode(node);
 else if (['control-start','control-stop','control-restart'].includes(action) && node) {
  const instance = nodeResults[node.id]?.summary?.instances.find(i=>i.id===button.dataset.instanceId);
  if (instance) controlNode(node,instance,action.slice('control-'.length));
 }
 else if (action === 'resolve' && node) reviewNodeAddresses(node);
 else if (['issue','revoke','issue-control','revoke-control'].includes(action)) mutateNodes(action, null);
 else if (action === 'delete' && node) mutateNodes(action, node);
});
if (location.hash === '#nodes') { page = 'nodes'; render(); }
