'use strict';

// Rendering stays independent of the runtime implementation. app.js owns the
// API calls and delegates the existing data-action buttons to its handlers.
window.KurumiInstances = (() => {
 const operations = new Map();
 const errors = new Map();
 const busyLabels = {start:'启动中…', stop:'停止中…', restart:'重启中…', validate:'校验中…'};
 const stateLabels = {starting:'启动中', running:'运行中', stopping:'停止中', stopped:'已停止', exited:'已退出', 'autostart-failed':'自动启动失败'};

 function paint(id) {
  const operation = operations.get(id);
  document.querySelectorAll('.instance-card[data-instance-id]').forEach(card => {
   if (card.dataset.instanceId !== id) return;
   card.setAttribute('aria-busy', operation ? 'true' : 'false');
   card.querySelectorAll('[data-instance-control]').forEach(button => {
    button.disabled = Boolean(operation);
    button.textContent = operation && operation.action === button.dataset.action
     ? busyLabels[operation.action] : button.dataset.idleLabel;
   });
   const output = card.querySelector('[data-instance-error]');
   const message = errors.get(id);
   if (output) {
    output.hidden = !message;
    output.textContent = message ? '操作失败：' + message : '';
   }
  });
 }

 function begin(id, action) {
  if (operations.has(id)) return null;
  const operation = {action};
  operations.set(id, operation);
  errors.delete(id);
  paint(id);
  return operation;
 }

 function end(id, operation) {
  // An old request must not clear a newer session's operation for the same ID.
  if (operations.get(id) !== operation) return;
  operations.delete(id);
  paint(id);
 }

 function setError(id, message) {
  errors.set(id, String(message));
  paint(id);
 }

 function clearError(id) {
  errors.delete(id);
  paint(id);
 }

 function reset() {
  operations.clear();
  errors.clear();
 }

 function formatTime(value) {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString('zh-CN', {hour12:false});
 }

 function renderCard(instance, data, registrationText, esc) {
  const id = esc(instance.id);
  const name = esc(instance.name);
  const runtime = data.runtime?.[instance.id] || {state:'stopped'};
  const restricted = data.identity?.role !== 'admin';
  const showConfig = !restricted || !!data.identity?.viewConfig;
  const showTraffic = !restricted || !!data.identity?.viewTraffic;
  const operator = data.identity?.role === 'operator' && (data.identity?.operableInstances || []).includes(instance.id);
  const can = action => !restricted || (operator && (data.identity?.allowedActions || []).includes(action));
  const state = runtime.state || 'stopped';
  const running = state === 'running';
  const starting = state === 'starting';
  const stopping = state === 'stopping';
  const role = instance.kind === 'frpc' ? 'client' : instance.kind === 'frps' ? 'server' : 'other';
  const roleName = role === 'client' ? '客户端' : role === 'server' ? '服务端' : '实例';
  const roleDescription = role === 'client' ? '连接上游服务端' : role === 'server' ? '在本机监听连接' : '本机运行实例';
  const roleCode = esc(String(instance.kind || '').toUpperCase());
  const statusClass = running ? 'running' : (state === 'exited' || state === 'autostart-failed') ? 'attention' : 'stopped';
  const stateText = esc(stateLabels[state] || state);
  const host = String(instance.host || '');
  const endpoint = host.includes(':') && !host.startsWith('[') ? '[' + host + ']:' + instance.port : host + ':' + instance.port;
  const ruleCount = data.state.tunnels.filter(tunnel => tunnel.instanceId === instance.id).length;
  const operation = operations.get(instance.id);
  const error = errors.get(instance.id);
  const plan = data.schedules?.[instance.id];
  const planStatus = !plan ? '未设置' : !plan.enabled ? '已关闭' : plan.paused ? '已暂停' : plan.lastStatus === 'pending' ? '执行中' : '已启用';
  let configText;
  if (starting) configText = '实例正在启动，配置是否生效仍待确认。';
  else if (stopping) configText = '实例正在停止，资源清理完成后可再次启动。';
  else if (runtime.pending) configText = running ? '当前仍使用上次应用的配置；请检查草稿后重启应用。' : '草稿尚未应用；请检查配置后再启动。';
  else if (runtime.applied) configText = running ? '当前运行配置与草稿一致。' : '草稿与上次应用的配置一致；启动时会重新检查。';
  else configText = running ? '尚未提供运行配置与草稿的比较结果。' : '启动时将检查并应用当前草稿。';

  const button = (action, label, primary = false, control = false) => {
   const display = control && operation?.action === action ? busyLabels[action] : label;
   return `<button type="button" ${primary?'class="primary" ':''}data-action="${action}" data-id="${id}" aria-label="${esc(label + ' · ' + instance.name)}"${control?` data-instance-control data-idle-label="${esc(label)}"${operation?' disabled':''}`:''}>${display}</button>`;
  };
  const registration = !restricted && role === 'client' ? `<div class="instance-card__registration"><span>代理注册</span><strong data-registration-count="${id}">${esc(registrationText(instance))}</strong></div>` : '';
  const vhosts = role === 'server' ? `<div class="instance-card__fact instance-card__fact--wide"><dt>草稿监听端口</dt><dd>HTTP ${esc(instance.httpPort || '关闭')} · HTTPS ${esc(instance.httpsPort || '关闭')} · KCP ${esc(instance.kcpBindPort || '关闭')} · QUIC ${esc(instance.quicBindPort || '关闭')}</dd></div>` : '';
  const started = runtime.started ? `<div class="instance-card__fact instance-card__fact--wide"><dt>${running?'启动时间':'最近启动'}</dt><dd>${esc(formatTime(runtime.started))}</dd></div>` : '';
  const process = runtime.pid ? `<div class="instance-card__fact"><dt>系统进程 PID</dt><dd>${esc(runtime.pid)}</dd></div>` : '';
  const runtimeActions = starting || stopping ? (can('stop') ? button('stop','停止',false,true) : '')
   : running ? (can('stop') ? button('stop','停止',false,true) : '') + (can('restart') ? button('restart','重启应用',Boolean(runtime.pending),true) : '')
   : (can('start') ? button('start','启动',true,true) : '');
  const readButton = (mode, label) => `<button type="button" data-instance-read="${mode}" data-id="${id}">${label}</button>`;
  const readActions = restricted ? (showConfig ? readButton('config','配置预览') : '') +
   (showTraffic ? readButton(role === 'server' ? 'server-traffic' : 'traffic','当前流量') + readButton('traffic-history','流量历史') : '') +
   (showTraffic && showConfig && role === 'client' ? readButton('client-traffic','规则流量') : '') +
   ((data.identity?.logInstances || []).includes(instance.id) ? button('logs','日志') : '')
   : button('logs','日志') + button('health','链路检测');

  return `<article class="instance-card instance-card--${role}" data-instance-id="${id}" aria-busy="${operation?'true':'false'}">
   <div class="instance-card__head"><div class="instance-card__identity"><span class="instance-card__symbol" aria-hidden="true">${role === 'client'?'↗':role === 'server'?'▣':'◇'}</span><div><p class="instance-card__kind">${roleName} <span>${roleCode}</span></p><h3>${name}</h3><p class="instance-card__description">${roleDescription}</p></div></div><div class="instance-card__badges"><span class="instance-state instance-state--${statusClass}"><i aria-hidden="true"></i>${stateText}</span>${runtime.pending?'<span class="pill warn">配置待应用</span>':''}</div></div>
   ${showConfig?`<div class="instance-card__endpoint"><span>${role === 'client'?'上游服务器地址':role === 'server'?'本机监听地址':'实例地址'} · 草稿</span><code>${esc(endpoint)}</code></div>`:''}
   ${showConfig?`<dl class="instance-card__facts"><div class="instance-card__fact"><dt>关联规则</dt><dd>${ruleCount} 条</dd></div><div class="instance-card__fact"><dt>认证令牌</dt><dd>${instance.hasToken?'已保存':'缺失'}</dd></div><div class="instance-card__fact"><dt>工作台启动时自动恢复</dt><dd>${!instance.autoStart?'未启用':plan?.enabled?'已启用（计划优先，暂不生效）':'已启用'}</dd></div><div class="instance-card__fact"><dt>定时计划</dt><dd>${esc(planStatus)}</dd></div>${process}${vhosts}${started}</dl>`:''}
   ${showConfig&&plan?.reason?`<div class="instance-card__runtime-message"><strong>计划状态</strong><p>${esc(plan.reason)}</p></div>`:''}
   ${registration}
   ${showConfig?`<div class="instance-card__config ${runtime.pending?'instance-card__config--pending':''}"><div><strong>${runtime.pending?'配置待应用':'配置状态'}</strong><p>${configText}</p></div>${runtime.applied?button('diff','查看差异'):''}</div>`:''}
   ${runtime.exit?`<div class="instance-card__runtime-message"><strong>运行信息</strong><p>${esc(runtime.exit)}</p></div>`:''}
   <p class="instance-card__error" data-instance-error role="alert" ${error?'':'hidden'}>${error?'操作失败：'+esc(error):''}</p>
   <div class="instance-card__controls">${runtimeActions}${readActions}</div>
   <div class="instance-card__actions">${can('validate') ? button('validate','校验配置',false,true) : ''}${restricted ? '' : button('edit-schedule','定时计划')+button('edit-instance','编辑')+button('duplicate-instance','复制实例')+button('delete-instance','删除')}</div>
  </article>`;
 }

 function render(data, helpers) {
  const instances = data.state.instances;
  const restricted = data.identity?.role !== 'admin';
  const current = instances.filter(i => data.runtime?.[i.id]?.state === 'running').length;
  const pending = instances.filter(i => data.runtime?.[i.id]?.pending).length;
  const groups = [
   {kind:'frpc', name:'客户端', description:'连接上游服务端，并承载代理规则。'},
   {kind:'frps', name:'服务端', description:'在本机监听，为客户端提供服务。'},
   {kind:'other', name:'其他实例', description:'其他类型的本机实例。'}
  ];
  const sections = groups.map(group => {
   const members = instances.filter(instance => group.kind === 'other' ? !['frpc','frps'].includes(instance.kind) : instance.kind === group.kind);
   if (!members.length) return '';
   return `<section class="instance-group"><div class="instance-group__heading"><div><h2>${group.name} <span>${members.length}</span></h2><p>${group.description}</p></div></div><div class="instance-grid">${members.map(instance => renderCard(instance, data, helpers.registrationText, helpers.esc)).join('')}</div></section>`;
  }).join('');

  const title = restricted ? '授权实例' : '运行实例';
  const subtitle = restricted ? `${helpers.esc(data.identity?.name || '账户')} · ${data.identity?.role === 'operator' ? '操作员' : '查看者'} · 仅显示已授权实例${data.identity?.allowLogs ? '；日志权限按实例分配' : '；日志权限未授予'}` : '只管理本机实例；上游服务器地址不代表远程管理权限。';
  const action = restricted ? '' : '<button class="primary" data-action="new-instance">＋ 新建实例</button>';
  const empty = restricted ? helpers.empty('暂无授权实例','请联系管理员分配实例。') : helpers.empty('添加第一个运行实例','先创建客户端或服务端，再检查配置并启动。','<button data-action="new-instance" class="primary">新建实例</button>','welcome');
  return helpers.header(title,subtitle,action) +
   `<div class="instances-page"><div class="instances-summary" aria-label="实例状态概览"><div><span>实例总数</span><strong>${instances.length}</strong></div><div><span>运行中</span><strong>${current}</strong></div><div><span>配置待应用</span><strong>${pending}</strong></div><p>启动前会检查配置。运行状态不代表代理已注册或链路可达。</p></div>${sections || empty}</div>`;
 }

 return {render, begin, end, setError, clearError, reset, sync:paint};
})();
