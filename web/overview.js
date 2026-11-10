'use strict';

// The overview consumes the same public state and actions as the rest of the
// workspace. Server forwarding totals have a separate scope from client payload.
const Overview = (() => {
 let trafficInstance = '';
 let trafficContext = null;
 let trafficResult = null;
 let trafficRequest = null;
 let historyResult = null;
 let historyRequest = null;

 function rate(value) {
  if (value === null) return '—';
  const units = ['B/s', 'KiB/s', 'MiB/s', 'GiB/s', 'TiB/s'];
  let scaled = value, unit = 0;
  while (scaled >= 1024 && unit < units.length - 1) {scaled /= 1024; unit++;}
  if (scaled > 0 && scaled < 0.01) return '＜0.01 ' + units[unit];
  return scaled.toLocaleString('zh-CN', {maximumFractionDigits: 2}) + ' ' + units[unit];
 }

 function sampleTime(value) {
  if (value === null || value === undefined || value === '') return '时间未知';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? '时间未知' : date.toLocaleString('zh-CN', {hour12:false});
 }

 // Future adapters may pass { [instanceId]: { samples: [{ at: ISO timestamp,
 // receivedBytesPerSecond: number|null, sentBytesPerSecond: number|null }] } }.
 // Missing or invalid measurements stay missing; they are never treated as 0.
 // An adapter must include null-valued samples for any known collection gaps.
 function trafficChart(series, escape) {
  const validRate = value => typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value : null;
  const samples = (Array.isArray(series?.samples) ? series.samples : [])
   .filter(sample => sample && typeof sample.at === 'string' && Number.isFinite(Date.parse(sample.at)))
   .map(sample => ({at:Date.parse(sample.at), received:validRate(sample.receivedBytesPerSecond), sent:validRate(sample.sentBytesPerSecond)}))
   .sort((a, b) => a.at - b.at).slice(-120);
  const values = samples.flatMap(sample => [sample.received, sample.sent]).filter(value => value !== null);
  if (!values.length) return `<div class="overview-chart-empty" role="status"><span aria-hidden="true">⌁</span><strong>流量数据不可用</strong><p>暂时无法获取该实例的接收与发送速率；这不代表流量为零。</p></div>`;

  const first = samples[0], last = samples[samples.length - 1];
  const max = Math.max(...values);
  const x = at => first.at === last.at ? 200 : 4 + (at - first.at) / (last.at - first.at) * 392;
  const y = value => max === 0 ? 94 : 94 - value / max * 88;
  function pathFor(key) {
   const paths = [];
   let points = [];
   function flush() {
    if (points.length === 1) paths.push(`<circle class="overview-series-${key}" cx="${points[0][0]}" cy="${points[0][1]}" r="2.5"></circle>`);
    else if (points.length > 1) paths.push(`<polyline class="overview-series-${key}" points="${points.map(point => point.join(',')).join(' ')}"></polyline>`);
    points = [];
   }
   for (const sample of samples) {
    if (sample[key] === null) flush();
    else points.push([x(sample.at).toFixed(2), y(sample[key]).toFixed(2)]);
   }
   flush();
   return paths.join('');
  }
  const times = first.at === last.at ? escape(sampleTime(first.at)) : escape(sampleTime(first.at)) + ' — ' + escape(sampleTime(last.at));
  return `<div class="overview-traffic-values"><div><span><i class="overview-key-received"></i>接收</span><strong>${rate(last.received)}</strong></div><div><span><i class="overview-key-sent"></i>发送</span><strong>${rate(last.sent)}</strong></div><small>最后采样：${escape(sampleTime(last.at))}</small></div>
   <div class="overview-plot" role="img" aria-label="流量速率图，横轴为采样时间，纵轴为每秒字节数；空值采样处不连线。"><span class="overview-scale">${rate(max)}</span><svg viewBox="0 0 400 100" preserveAspectRatio="none" aria-hidden="true">${pathFor('received')}${pathFor('sent')}</svg><span class="overview-scale overview-scale-zero">0 B/s</span></div><p class="overview-chart-times">${times}</p>`;
 }

 function selectedInstance() {
  return trafficContext?.instances.find(instance => instance.id === trafficInstance);
 }

 // Server totals arrive as decimal strings; do not round them through Number.
 function bytes(value) {
  return typeof value === 'string' && /^(0|[1-9]\d*)$/.test(value)
   ? value.replace(/\B(?=(\d{3})+(?!\d))/g, ',') + ' B' : '—';
 }

 const serverExplanation = '<p class="small-note">内置服务端记录转发边界的字节：接收为用户 → 代理，发送为代理 → 用户；部分流量含协议帧或心跳。UDP 按已记录的数据报内容计数；HTTP 发送量按代理工作连接读取，未必送达用户。绕过本服务端的 P2P 流量不计入。</p><p class="small-note">数据仅属于本次运行，停止后仍可能增加，重新启动后重新计数。</p>';

 function renderServerTraffic(value, escape) {
  if (!value?.available) return `<div class="overview-server-empty" role="status">当前运行代次没有可用的内置服务端流量数据。</div>${serverExplanation}`;
  const proxies = Array.isArray(value.proxies) ? value.proxies : [];
  const rows = proxies.slice(0, 100).map(proxy => `<tr><td>${escape(proxy.name)}</td><td>${escape(proxy.type)}</td><td>${bytes(proxy.receivedBytes)}</td><td>${bytes(proxy.sentBytes)}</td></tr>`).join('');
  const detail = rows ? `<div class="table-wrap"><table><thead><tr><th>代理</th><th>类型</th><th>接收</th><th>发送</th></tr></thead><tbody>${rows}</tbody></table></div>${proxies.length > 100 ? `<p class="small-note">仅显示前 100 条，已保留 ${proxies.length} 条。</p>` : ''}` : '<p class="small-note">尚无可显示的代理明细。</p>';
  const truncated = value.proxiesTruncated ? '<p class="small-note" role="status">代理明细不完整：已达到记录上限或名称、类型过长；未列出的代理仍计入总量。</p>' : '';
  const proxyDetails = value.detailsAllowed === false ? '<p class="small-note">此账户没有配置详情权限，代理名称与明细未显示。</p>' : `${truncated}<details class="overview-server-details"><summary>已保留的代理明细 · ${proxies.length} 条</summary>${detail}</details>`;
  const state = ({running:'运行中',starting:'启动中',stopping:'停止中',exited:'已退出',stopped:'已停止','autostart-failed':'自动启动失败'})[value.state] || '状态未知';
  return `<div class="overview-traffic-values"><div><span><i class="overview-key-received"></i>接收 · 用户 → 代理</span><strong>${bytes(value.receivedBytes)}</strong></div><div><span><i class="overview-key-sent"></i>发送 · 代理 → 用户</span><strong>${bytes(value.sentBytes)}</strong></div><small>快照：${escape(sampleTime(value.sampledAt))} · ${state}</small></div>${proxyDetails}${serverExplanation}`;
 }

 function serverTrafficView() {
  const instance = selectedInstance();
  const runId = trafficContext.runtime[instance.id]?.runId || '';
  const result = trafficResult?.id === instance.id && trafficResult.runId === runId && trafficResult.revision === trafficContext.revision ? trafficResult : null;
  if (!result) return `<div class="overview-server-empty" role="status">正在读取服务端流量…</div>${serverExplanation}`;
  if (result.error) return `<div class="overview-server-empty" role="status">${result.changed ? '实例运行状态已变化，等待刷新。' : '无法读取服务端流量，请稍后重试。'}</div>${serverExplanation}`;
  return renderServerTraffic(result.value, trafficContext.escape);
 }

 function clientTrafficView() {
  const instance = selectedInstance();
  if (!instance) return trafficChart(null, trafficContext.escape);
  const runId = trafficContext.runtime[instance.id]?.runId || '';
  const result = trafficResult?.id === instance.id && trafficResult.runId === runId && trafficResult.revision === trafficContext.revision ? trafficResult : null;
  if (!result) return '<div class="overview-chart-empty" role="status">正在读取客户端流量…</div>';
  if (result.error) return `<div class="overview-chart-empty" role="status">${result.changed ? '实例运行状态已变化，等待刷新。' : '无法读取客户端流量，请稍后重试。'}</div>`;
  return renderClientTraffic(result.value, trafficContext.escape);
 }

 function renderClientTraffic(value, escape) {
  const samples = (Array.isArray(value.curve) ? value.curve : []).map(point => ({
   at:point.sampledAt, receivedBytesPerSecond:point.receivedBytesPerSecond, sentBytesPerSecond:point.sentBytesPerSecond
  }));
  const note = value.available ? '' : '<p class="small-note">当前采样不可用；图表仅显示此运行代次的历史采样。</p>';
  const details = Object.hasOwn(value, 'ruleTraffic') ? renderClientRuleTraffic(value.ruleTraffic, escape) : '';
  return note + trafficChart({samples}, escape) + '<p class="small-note">客户端统计转发载荷，不包含协议开销；不同于服务端统计口径。</p>' + details;
 }

 function renderClientRuleTraffic(value, escape, open = false) {
  if (!value?.available) return '<p class="small-note">当前运行代次的规则流量明细不可用，不代表流量为零。</p>';
  const rules = Array.isArray(value.rules) ? value.rules : [];
  const rows = rules.map(rule => `<tr><td>${escape(rule.name)}</td><td>${rule.kind === 'visitor' ? 'Visitor' : '代理'}</td><td>${bytes(rule.receivedBytes)}</td><td>${bytes(rule.sentBytes)}</td></tr>`).join('');
  const body = rows ? `<div class="table-wrap"><table><thead><tr><th>已应用名称</th><th>类型</th><th>接收</th><th>发送</th></tr></thead><tbody>${rows}</tbody></table></div>` : '<p class="small-note">此运行代次没有可显示的规则。</p>';
  return `<details class="overview-server-details" ${open ? 'open' : ''}><summary>本次运行的规则流量 · ${rules.length} 条</summary><p class="small-note">快照：${escape(sampleTime(value.sampledAt))}。名称为启动时应用的规则，草稿可能已经变化；数据在重启后重置，不能用于计费或配额。</p>${body}</details>`;
 }

 function paintTraffic() {
  const chart = document.querySelector('#overview-traffic-chart');
  if (!chart || !trafficContext) return;
  const open = chart.querySelector('details')?.open;
  chart.innerHTML = selectedInstance()?.kind === 'frps' ? serverTrafficView() : clientTrafficView();
  const details = chart.querySelector('details');
  if (details && open) details.open = true;
 }

 function pauseTraffic() {
  if (!trafficRequest) return;
  clearTimeout(trafficRequest.timeout);
  trafficRequest.controller.abort();
  trafficRequest = null;
 }

 function pauseHistory() {
  if (!historyRequest) return;
  clearTimeout(historyRequest.timeout);
  historyRequest.controller.abort();
  historyRequest = null;
 }

 function pause() {pauseTraffic(); pauseHistory();}

 function refreshTraffic() {
  const instance = selectedInstance();
  if (!instance || document.hidden || !document.querySelector('#overview-traffic-chart')) return;
  const id = instance.id, kind = instance.kind, runId = trafficContext.runtime[id]?.runId || '', revision = trafficContext.revision;
  const load = kind === 'frps' ? trafficContext.loadServerTraffic : trafficContext.loadTraffic;
  if (!load) return;
  if (trafficRequest && trafficRequest.id === id && trafficRequest.runId === runId && trafficRequest.kind === kind && trafficRequest.revision === revision) return;
  pauseTraffic();
  const request = {id, runId, kind, revision, controller:new AbortController(), timedOut:false};
  trafficRequest = request;
  request.timeout = setTimeout(() => {request.timedOut = true; request.controller.abort();}, 10000);
  const current = () => trafficRequest === request && trafficInstance === id && selectedInstance()?.kind === kind && trafficContext.revision === revision && (trafficContext.runtime[id]?.runId || '') === runId;
  const ruleTraffic = kind === 'frpc' && trafficContext.loadClientRuleTraffic
   ? trafficContext.loadClientRuleTraffic(id, request.controller.signal).catch(() => null) : Promise.resolve(null);
  Promise.all([load(id, request.controller.signal), ruleTraffic]).then(([response, detail]) => {
   if (!current()) return;
   let value = kind === 'frps' ? response : response?.instances?.[id];
   if (kind === 'frpc' && value) value = {...value, ruleTraffic:detail?.instanceId === id && detail.runId === runId ? detail : null};
   const matches = kind === 'frps' ? value?.instanceId === id : !!value;
   trafficResult = matches && (value.runId || '') === runId
    ? {id, runId, revision, value} : {id, runId, revision, error:true, changed:true};
   paintTraffic();
  }).catch(error => {
   if (!current() || (error.name === 'AbortError' && !request.timedOut)) return;
   trafficResult = {id, runId, revision, error:true};
   paintTraffic();
  }).finally(() => {
   clearTimeout(request.timeout);
   if (trafficRequest === request) trafficRequest = null;
  });
 }

 function historyView() {
  const instance = selectedInstance();
  if (!instance) return '';
  const result = historyResult?.id === instance.id && historyResult.kind === instance.kind && historyResult.revision === trafficContext.revision ? historyResult : null;
  if (!result) return '<p class="small-note" role="status">正在读取流量历史…</p>';
  if (result.error) return '<p class="small-note" role="status">无法读取流量历史，请稍后重试。</p>';
  return renderTrafficHistory(result.value, trafficContext.escape);
 }

 function renderTrafficHistory(value, escape, open = false) {
  const days = Array.isArray(value.days) ? value.days.slice(-90).reverse() : [];
  const status = value.storageError ? '历史文件读取或写入失败；内存数据可能未保存。' : value.saved ? (value.savedAt ? '最近写入：' + sampleTime(value.savedAt) : '暂无已写入的数据。') : '存在待保存采样，约每 30 秒写入。';
  const rows = days.map(day => `<tr><td>${escape(day.date)}</td><td>${bytes(day.receivedBytes)}</td><td>${bytes(day.sentBytes)}</td><td>${day.partial ? '不完整' : '已记录'}</td></tr>`).join('');
  const body = rows ? `<div class="table-wrap"><table><thead><tr><th>UTC 日期</th><th>接收</th><th>发送</th><th>覆盖</th></tr></thead><tbody>${rows}</tbody></table></div>` : '<p class="small-note">尚无可显示的历史采样。</p>';
  return `<details class="overview-server-details" ${open ? 'open' : ''}><summary>近 90 天流量历史</summary><p class="small-note">只累计确认的采样增量；跨日、采样中断、重置和退出时可能遗漏，缺少日期不代表零流量。该记录不适用于计费或强制配额。</p><p class="small-note">${escape(status)}</p>${body}</details>`;
 }

 function paintHistory() {
  const element = document.querySelector('#overview-traffic-history');
  if (!element || !trafficContext) return;
  const open = element.querySelector('details')?.open;
  element.innerHTML = historyView();
  if (open && element.querySelector('details')) element.querySelector('details').open = true;
 }

 function refreshHistory() {
  const instance = selectedInstance();
  if (!instance || !trafficContext.loadHistory || document.hidden || !document.querySelector('#overview-traffic-history')) return;
  const id = instance.id, kind = instance.kind, revision = trafficContext.revision;
  if (historyRequest && historyRequest.id === id && historyRequest.kind === kind && historyRequest.revision === revision) return;
  pauseHistory();
  const request = {id, kind, revision, controller:new AbortController(), timedOut:false};
  historyRequest = request;
  request.timeout = setTimeout(() => {request.timedOut = true; request.controller.abort();}, 10000);
  const current = () => historyRequest === request && trafficInstance === id && selectedInstance()?.kind === kind && trafficContext.revision === revision;
  trafficContext.loadHistory(id, request.controller.signal).then(value => {
   if (!current()) return;
   historyResult = value?.instanceId === id && value.kind === kind ? {id, kind, revision, value} : {id, kind, revision, error:true};
   paintHistory();
  }).catch(error => {
   if (!current() || (error.name === 'AbortError' && !request.timedOut)) return;
   historyResult = {id, kind, revision, error:true};
   paintHistory();
  }).finally(() => {
   clearTimeout(request.timeout);
   if (historyRequest === request) historyRequest = null;
  });
 }

 function refresh() {refreshTraffic(); refreshHistory();}

 function render(snapshot, helpers) {
  const instances = snapshot.state.instances;
  const tunnels = snapshot.state.tunnels;
  const events = snapshot.events || [];
  const binaries = snapshot.binaries || {};
  const embedded = snapshot.runtimeBackend === 'embedded';
  const embeddedReady = snapshot.runtimeAvailable?.frpc && snapshot.runtimeAvailable?.frps;
  const coreRows = embedded
   ? `<div class="check-row"><span>内置核心</span><span class="pill ${embeddedReady?'good':'warn'}">${embeddedReady?'可用':'暂不可用'}</span></div>`
   : `<div class="check-row"><span>frpc 二进制</span><span class="pill ${binaries.frpc?'good':'warn'}">${binaries.frpc?'已找到':'未安装'}</span></div><div class="check-row"><span>frps 二进制</span><span class="pill ${binaries.frps?'good':'warn'}">${binaries.frps?'已找到':'未安装'}</span></div>`;
  const coreNote = embedded ? '内置核心仍在开发，部分协议和功能暂不可用。' : '指定 frp-dir 中的二进制属于受信任程序；请仅放入官方或自行审计的版本。';
  const {escape, header, badge, instanceRegistrationText, tunnelTable} = helpers;
  if (!instances.some(instance => instance.id === trafficInstance)) trafficInstance = instances[0]?.id || '';
  trafficContext = {escape, instances, revision:snapshot.revision, runtime:snapshot.runtime || {}, loadServerTraffic:helpers.loadServerTraffic, loadTraffic:helpers.loadTraffic, loadClientRuleTraffic:helpers.loadClientRuleTraffic, loadHistory:helpers.loadHistory};
  const running = instances.filter(instance => snapshot.runtime[instance.id]?.state === 'running').length;
  const pending = instances.filter(instance => snapshot.runtime[instance.id]?.pending).length;
  const enabled = tunnels.filter(tunnel => tunnel.enabled).length;
  const metrics = [
   [instances.length, '本机实例', `${running} 个实例运行中`],
   [running, '实例运行中', '不代表链路已经连通'],
   [tunnels.length, '草稿规则', `${enabled} 条期望启用，不等于已注册`],
   [pending, '实例配置待应用', '保存草稿不会改变当前运行配置']
  ];
  const instanceRows = instances.slice(0, 5).map(instance => {
   const runtime = snapshot.runtime[instance.id] || {state:'stopped'};
   const rules = tunnels.filter(tunnel => tunnel.instanceId === instance.id);
   const id = escape(instance.id);
   return `<article class="overview-instance"><div class="overview-instance-main"><div><strong>${escape(instance.name)}</strong><span class="type-icon">${escape(instance.kind.toUpperCase())}</span></div><small>草稿地址 ${escape(instance.host)}:${escape(instance.port)} · ${rules.length} 条草稿规则</small>${instance.kind === 'frpc' ? `<small data-registration-count="${id}">${escape(instanceRegistrationText(instance))}</small>` : ''}${runtime.exit ? `<small class="overview-exit">${escape(runtime.exit)}</small>` : ''}</div><div class="overview-instance-side"><div class="overview-badges">${badge(instance.id)}</div><div class="actions"><button data-action="logs" data-id="${id}">日志</button>${['starting','running','stopping'].includes(runtime.state) ? `<button data-action="stop" data-id="${id}">停止</button>` : `<button data-action="start" data-id="${id}">启动</button>`}</div></div></article>`;
  }).join('');
  const eventRows = events.slice(-6).reverse().map(event => `<li><span class="overview-event-dot" aria-hidden="true"></span><div><p>${escape(event.message)}</p><time>${escape(sampleTime(event.at))}</time></div></li>`).join('');
  const trafficOptions = instances.map(instance => `<option value="${escape(instance.id)}" ${instance.id === trafficInstance ? 'selected' : ''}>${escape(instance.name)} · ${escape(instance.kind)}</option>`).join('');

  return header('网络总览', '查看本机实例、草稿规则和最近配置事件。', '<div class="overview-head-actions"><button data-action="new-tunnel">＋ 新建规则</button><button class="primary" data-action="new-instance">＋ 新建实例</button></div>') +
   `<section class="hero overview-hero"><div><p class="eyebrow">KURUMI / CONNECTION WORKSPACE</p><h2>每一条连接，都有迹可循。</h2><p>查看实例运行情况，确认规则状态，再决定何时应用配置。</p></div><div class="hero-graphic" aria-hidden="true"><span class="hero-stamp">KURUMI<br>FRP</span></div></section>` +
   `<section class="metrics overview-metrics" aria-label="状态摘要">${metrics.map(([value, label, note]) => `<div class="metric"><span class="label">${label}</span><strong>${value}</strong><small>${note}</small></div>`).join('')}</section>` +
   `<div class="overview-grid"><section class="panel overview-instances"><div class="panel-title"><div><h3>运行实例</h3><small>实例状态 · 草稿地址与规则</small></div><button class="quiet" data-go="instances">全部实例 →</button></div>${instances.length ? `<div class="overview-instance-list">${instanceRows}</div>${instances.length > 5 ? `<p class="overview-more">显示前 5 个，共 ${instances.length} 个实例。</p>` : ''}` : '<div class="overview-empty"><strong>还没有运行实例</strong><p>先创建一个客户端或服务端实例。</p><button data-action="new-instance">＋ 新建实例</button></div>'}</section>
   <section class="panel overview-traffic"><div class="panel-title"><div><h3>实例流量</h3><small>按实例查看</small></div><span class="pill">每实例</span></div><div class="overview-traffic-body"><label for="overview-traffic-instance">选择实例</label><select id="overview-traffic-instance" ${instances.length ? '' : 'disabled'}>${trafficOptions || '<option value="">暂无实例</option>'}</select><div id="overview-traffic-chart">${selectedInstance()?.kind === 'frps' ? serverTrafficView() : clientTrafficView()}</div><div id="overview-traffic-history">${historyView()}</div></div></section></div>` +
   `<section class="panel overview-rules"><div class="panel-title"><div><h3>连接规则</h3><small>草稿配置 · ${tunnels.length > 5 ? `显示前 5 条，共 ${tunnels.length} 条` : `共 ${tunnels.length} 条`}</small></div><button class="quiet" data-go="tunnels">全部规则 →</button></div>${tunnelTable(tunnels.slice(0, 5))}</section>` +
   `<section class="panel"><div class="panel-title"><h3>环境与边界</h3><span class="pill accent">LOCAL</span></div><div class="panel-content">${coreRows}<div class="check-row"><span>配置保存</span><span class="pill good">本地持久化</span></div><div class="check-row"><span>远端节点</span><span class="pill">授权后查看与启停</span></div><p class="small-note blank">配置文件包含密钥，请妥善保管。${coreNote}</p></div></section>` +
   `<section class="panel overview-events"><div class="panel-title"><div><h3>最近事件</h3><small>仅显示配置变更记录，进程输出请查看日志</small></div><button class="quiet" data-go="logs">运行记录 →</button></div>${eventRows ? `<ol class="overview-event-list">${eventRows}</ol>` : '<div class="overview-empty"><strong>暂无配置事件</strong><p>保存配置后，最近的变更会显示在这里。</p></div>'}</section>`;
 }

 document.querySelector('#content').addEventListener('change', event => {
  if (event.target.id !== 'overview-traffic-instance' || !trafficContext) return;
  pause();
  trafficInstance = event.target.value;
  trafficResult = null;
  historyResult = null;
  paintTraffic();
  paintHistory();
  refresh();
 });

 function reset() {pause(); trafficInstance = ''; trafficContext = null; trafficResult = null; historyResult = null;}
 return {render, reset, pause, refresh, renderServerTraffic, renderClientTraffic, renderClientRuleTraffic, renderTrafficHistory};
})();
