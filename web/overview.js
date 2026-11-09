'use strict';

// The overview consumes the same public state and actions as the rest of the
// workspace. Traffic is optional until the per-instance API is available.
const Overview = (() => {
 let trafficInstance = '';
 let trafficContext = null;

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

 function selectedTraffic() {
  return trafficContext?.series?.[trafficInstance];
 }

 function render(snapshot, helpers, trafficByInstance = null) {
  const instances = snapshot.state.instances;
  const tunnels = snapshot.state.tunnels;
  const events = snapshot.events || [];
  const binaries = snapshot.binaries || {};
  const {escape, header, badge, instanceRegistrationText, tunnelTable} = helpers;
  if (!instances.some(instance => instance.id === trafficInstance)) trafficInstance = instances[0]?.id || '';
  trafficContext = {series:trafficByInstance, escape};
  const running = instances.filter(instance => snapshot.runtime[instance.id]?.state === 'running').length;
  const pending = instances.filter(instance => snapshot.runtime[instance.id]?.pending).length;
  const enabled = tunnels.filter(tunnel => tunnel.enabled).length;
  const metrics = [
   [instances.length, '本机实例', `${running} 个进程运行中`],
   [running, '进程运行中', '不代表链路已经连通'],
   [tunnels.length, '草稿规则', `${enabled} 条期望启用，不等于已注册`],
   [pending, '实例配置待应用', '保存草稿不会改变当前运行配置']
  ];
  const instanceRows = instances.slice(0, 5).map(instance => {
   const runtime = snapshot.runtime[instance.id] || {state:'stopped'};
   const rules = tunnels.filter(tunnel => tunnel.instanceId === instance.id);
   const id = escape(instance.id);
   return `<article class="overview-instance"><div class="overview-instance-main"><div><strong>${escape(instance.name)}</strong><span class="type-icon">${escape(instance.kind.toUpperCase())}</span></div><small>草稿地址 ${escape(instance.host)}:${escape(instance.port)} · ${rules.length} 条草稿规则</small>${instance.kind === 'frpc' ? `<small data-registration-count="${id}">${escape(instanceRegistrationText(instance))}</small>` : ''}${runtime.exit ? `<small class="overview-exit">${escape(runtime.exit)}</small>` : ''}</div><div class="overview-instance-side"><div class="overview-badges">${badge(instance.id)}</div><div class="actions"><button data-action="logs" data-id="${id}">日志</button>${runtime.state === 'running' ? `<button data-action="stop" data-id="${id}">停止</button>` : `<button data-action="start" data-id="${id}">启动</button>`}</div></div></article>`;
  }).join('');
  const eventRows = events.slice(-6).reverse().map(event => `<li><span class="overview-event-dot" aria-hidden="true"></span><div><p>${escape(event.message)}</p><time>${escape(sampleTime(event.at))}</time></div></li>`).join('');
  const trafficOptions = instances.map(instance => `<option value="${escape(instance.id)}" ${instance.id === trafficInstance ? 'selected' : ''}>${escape(instance.name)} · ${escape(instance.kind)}</option>`).join('');

  return header('网络总览', '查看本机实例、草稿规则和最近配置事件。', '<div class="overview-head-actions"><button data-action="new-tunnel">＋ 新建规则</button><button class="primary" data-action="new-instance">＋ 新建实例</button></div>') +
   `<section class="hero overview-hero"><div><p class="eyebrow">KURUMI / CONNECTION WORKSPACE</p><h2>每一条连接，都有迹可循。</h2><p>查看实例运行情况，确认规则状态，再决定何时应用配置。</p></div><div class="hero-graphic" aria-hidden="true"><span class="hero-stamp">KURUMI<br>FRP</span></div></section>` +
   `<section class="metrics overview-metrics" aria-label="状态摘要">${metrics.map(([value, label, note]) => `<div class="metric"><span class="label">${label}</span><strong>${value}</strong><small>${note}</small></div>`).join('')}</section>` +
   `<div class="overview-grid"><section class="panel overview-instances"><div class="panel-title"><div><h3>运行实例</h3><small>进程状态 · 草稿地址与规则</small></div><button class="quiet" data-go="instances">全部实例 →</button></div>${instances.length ? `<div class="overview-instance-list">${instanceRows}</div>${instances.length > 5 ? `<p class="overview-more">显示前 5 个，共 ${instances.length} 个实例。</p>` : ''}` : '<div class="overview-empty"><strong>还没有运行实例</strong><p>先创建一个客户端或服务端实例。</p><button data-action="new-instance">＋ 新建实例</button></div>'}</section>
   <section class="panel overview-traffic"><div class="panel-title"><div><h3>实例流量</h3><small>接收与发送速率</small></div><span class="pill">每实例</span></div><div class="overview-traffic-body"><label for="overview-traffic-instance">选择实例</label><select id="overview-traffic-instance" ${instances.length ? '' : 'disabled'}>${trafficOptions || '<option value="">暂无实例</option>'}</select><div id="overview-traffic-chart">${trafficChart(selectedTraffic(), escape)}</div></div></section></div>` +
   `<section class="panel overview-rules"><div class="panel-title"><div><h3>连接规则</h3><small>草稿配置 · ${tunnels.length > 5 ? `显示前 5 条，共 ${tunnels.length} 条` : `共 ${tunnels.length} 条`}</small></div><button class="quiet" data-go="tunnels">全部规则 →</button></div>${tunnelTable(tunnels.slice(0, 5))}</section>` +
   `<section class="panel"><div class="panel-title"><h3>环境与边界</h3><span class="pill accent">LOCAL</span></div><div class="panel-content"><div class="check-row"><span>frpc 二进制</span><span class="pill ${binaries.frpc?'good':'warn'}">${binaries.frpc?'已找到':'未安装'}</span></div><div class="check-row"><span>frps 二进制</span><span class="pill ${binaries.frps?'good':'warn'}">${binaries.frps?'已找到':'未安装'}</span></div><div class="check-row"><span>配置保存</span><span class="pill good">本地持久化</span></div><div class="check-row"><span>远端设备管理</span><span class="pill">尚未实现</span></div><p class="small-note blank">配置文件包含密钥，请妥善保管。指定 frp-dir 中的二进制属于受信任程序；请仅放入官方或自行审计的版本。</p></div></section>` +
   `<section class="panel overview-events"><div class="panel-title"><div><h3>最近事件</h3><small>仅显示配置变更记录，进程输出请查看日志</small></div><button class="quiet" data-go="logs">运行记录 →</button></div>${eventRows ? `<ol class="overview-event-list">${eventRows}</ol>` : '<div class="overview-empty"><strong>暂无配置事件</strong><p>保存配置后，最近的变更会显示在这里。</p></div>'}</section>`;
 }

 document.querySelector('#content').addEventListener('change', event => {
  if (event.target.id !== 'overview-traffic-instance' || !trafficContext) return;
  trafficInstance = event.target.value;
  const chart = document.querySelector('#overview-traffic-chart');
  if (chart) chart.innerHTML = trafficChart(selectedTraffic(), trafficContext.escape);
 });

 function reset() {trafficInstance = ''; trafficContext = null;}
 return {render, reset};
})();
