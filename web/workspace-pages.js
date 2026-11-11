'use strict';

// The visible pages are composed here; the protocol editor is a shared
// domain-specific form so existing advanced fields remain editable.
(() => {
 const K = window.Kurumi, e = K.esc;
 const states = {starting:'启动中', stopping:'停止中', running:'运行中', stopped:'已停止', exited:'已退出', 'autostart-failed':'自动启动失败'};
 const registrationStates = {stopped:'实例未运行', disabled:'草稿停用', pending:'草稿待应用', 'pending-stop':'旧配置仍在运行', 'not-reported':'不提供注册状态', unavailable:'状态不可用', registering:'注册中', registered:'已注册', failed:'注册失败'};
 const admin = data => data.identity?.role === 'admin';
 const stateLabel = runtime => states[runtime?.state] || runtime?.state || '已停止';
 const status = runtime => `<span class="pill ${runtime?.state === 'running' ? 'good' : runtime?.state === 'exited' || runtime?.state === 'autostart-failed' ? 'warn' : ''}">${e(stateLabel(runtime))}</span>${runtime?.pending ? ' <span class="pill warn">草稿待应用</span>' : ''}`;
 const head = (title, text, actions = '') => `<header class="page-head"><div><h1>${e(title)}</h1><p>${e(text)}</p></div>${actions ? `<div class="actions">${actions}</div>` : ''}</header>`;
 const empty = (title, text) => `<div class="empty"><strong>${e(title)}</strong><p>${e(text)}</p></div>`;
 const can = (data, id, action) => admin(data) || (data.identity?.role === 'operator' && (data.identity.operableInstances || []).includes(id) && (data.identity.allowedActions || []).includes(action));
 const button = (action, label, id = '', classes = '') => `<button type="button" ${classes ? `class="${classes}"` : ''} data-action="${action}" ${id ? `data-id="${e(id)}"` : ''}>${e(label)}</button>`;
 const runtimeFor = (data, id) => data.runtime?.[id] || {state:'stopped'};
 const pickInstance = id => { K.selectedInstance = id; };
 const requireRevision = data => {
  if (!K.state() || K.state().revision !== data.revision) throw new Error('草稿已变化，请刷新后重试。');
 };
 async function mutation(path, payload, message) {
  try { await K.api(path, payload); }
  catch (error) {
   if (!error.status || error.status >= 500) {
    await K.refresh().catch(() => {});
    throw new Error('操作结果未确认；请刷新并核对当前状态后再重试。');
   }
   throw error;
  }
  try { await K.refresh(); }
  catch (_) { if (K.state()) K.toast('操作已受理，但状态刷新失败；请手动刷新后核对。'); return; }
  if (message) K.toast(message);
 }
 function bindButtons(root, handler) {
  root.addEventListener('click', async event => {
   const target = event.target.closest('[data-action]');
   if (!target || !root.contains(target) || target.disabled) return;
   target.disabled = true;
   try { await handler(target.dataset.action, target.dataset.id || '', target); }
   catch (error) { K.toast(error.message); }
   finally { if (target.isConnected) target.disabled = false; }
  });
 }
 function editObject(kind, id, data) {
  requireRevision(data);
  const list = kind === 'instance' ? data.state.instances : data.state.tunnels;
  const old = id ? list.find(item => item.id === id) : null;
  if (id && !old) throw new Error('对象已不存在，请刷新。');
  const clients = data.state.instances.filter(item => item.kind === 'frpc');
  if (kind === 'tunnel' && !clients.length) throw new Error('请先创建 frpc 客户端实例。');
  const editing = {kind, id:old?.id || '', old};
  const update = form => ProtocolEditor.update(form, editing, data.state.tunnels);
  K.openDialog({
   html:kind === 'instance' ? ProtocolEditor.instance(old) : ProtocolEditor.tunnel(old, clients), submitLabel:'保存草稿',
   onReady:update, onChange:(_, form) => update(form), onInput:(event, form) => { if (event.target.name === 'localPort') update(form); },
   onClick:(event, form) => { if (kind === 'tunnel') ProtocolEditor.headerClick(form, event.target); },
   async onSubmit(form) {
    requireRevision(data);
    const value = ProtocolEditor.value(form, editing);
    await mutation('save', {revision:data.revision, kind, value}, '草稿已保存；运行中的实例需要重新应用。');
   }
  });
 }
 function editBatch(data) {
  requireRevision(data);
  const clients = data.state.instances.filter(item => item.kind === 'frpc');
  if (!clients.length) throw new Error('请先创建 frpc 客户端实例。');
  const update = form => ProtocolEditor.update(form,{kind:'tunnel-batch'},data.state.tunnels);
  K.openDialog({html:ProtocolEditor.batch(clients), submitLabel:'保存规则草稿', onReady:update, onChange:(_,form) => update(form), async onSubmit(form) {
   requireRevision(data);
   let result;
   try {
    result = await K.api('tunnels/batch', {revision:data.revision, ...ProtocolEditor.batchValue(form)});
   } catch (error) {
    if (!error.status || error.status >= 500) { await K.refresh().catch(() => {}); throw new Error('请求结果未确认，请先刷新并核对规则，避免重复提交。'); }
    throw error;
   }
   try { await K.refresh(); K.toast(`已创建 ${result.created} 条规则草稿。`); }
   catch (_) { if (K.state()) K.toast(`已创建 ${result.created} 条规则草稿，但刷新失败；请手动刷新核对。`); }
  }});
 }
 function editSchedule(id, data) {
  requireRevision(data);
  const instance = data.state.instances.find(item => item.id === id);
  if (!instance) throw new Error('实例已不存在。');
  const plan = data.schedules?.[id];
  const payload = () => ({revision:data.revision, id, version:plan?.version || ''});
  K.openDialog({
   html:window.KurumiSchedules.editor(instance, plan, e), submitLabel:'保存计划',
   onClick:async (event, form) => {
    if (event.target.closest('[data-schedule-add]')) form.querySelector('[data-schedule-rows]').insertAdjacentHTML('beforeend', window.KurumiSchedules.row());
    if (event.target.closest('[data-schedule-remove]')) event.target.closest('[data-schedule-row]').remove();
    if (event.target.closest('[data-schedule-delete]')) {
     if (!confirm('删除定时计划？原有自动启动设置将在下次启动工作台时生效。')) return;
     requireRevision(data); K.closeDialog();
     try { await mutation('schedules', {...payload(), remove:true}, '计划已删除。'); }
     catch (error) { await K.refresh().catch(() => {}); K.toast(`计划删除未完成或结果未确认：${error.message}。请核对后重试。`); }
    }
   },
   async onSubmit(form) {
    const value = window.KurumiSchedules.value(form);
    if (!value.enabled && plan?.enabled && !confirm('关闭计划后，原有自动启动设置将在下次启动工作台时生效。继续？')) return false;
    requireRevision(data); await mutation('schedules', {...payload(), ...value}, '计划已保存。');
   }
  });
 }
 async function control(action, id, data) {
  requireRevision(data);
  const instance = data.state.instances.find(item => item.id === id);
  if (!instance || !can(data, id, action)) throw new Error('实例不存在或没有操作权限。');
  const schedule = data.schedules?.[id];
  const pause = schedule?.enabled && !schedule.paused ? '此操作可能暂停实例的定时计划。' : '';
  if (action === 'restart' && !confirm(`重启「${instance.name}」会中断现有连接，重新启动失败时可能保持停止。${pause}继续？`)) return;
  if (action === 'stop' && !confirm(`停止「${instance.name}」并中断连接？${pause}`)) return;
  if (action === 'start' && pause && !confirm(`启动「${instance.name}」？${pause}`)) return;
  try {
   await K.api(action === 'validate' ? 'validate' : 'control', {revision:data.revision, id, ...(action === 'validate' ? {} : {action})});
   K.toast(action === 'validate' ? '配置校验通过；这不代表网络连通。' : '实例操作已受理，请核对运行状态。');
  } finally { await K.refresh().catch(() => {}); }
 }

 let overviewCleanup = null;
 K.register('overview', {
  render(data) {
   const instances = data.state.instances || [], tunnels = data.state.tunnels || [];
   const running = instances.filter(item => runtimeFor(data, item.id).state === 'running').length;
   const pending = instances.filter(item => runtimeFor(data, item.id).pending).length;
   const recent = (data.events || []).slice(-5).reverse();
   const trafficAllowed = admin(data) || data.identity?.viewTraffic;
   const configAllowed = admin(data) || data.identity?.viewConfig;
   const selected = instances.some(item => item.id === K.selectedInstance) ? K.selectedInstance : instances[0]?.id || '';
   return `<section class="dashboard-hero"><div><p class="eyebrow">KURUMI FRP / WORKSPACE</p><h1>连接，一目了然。</h1><p>在同一处查看实例与规则的当前草稿和运行状态。</p><div class="actions">${button('go-instances','管理实例','','primary')}${button('go-rules','查看规则')}</div></div></section>
    <div class="stat-grid"><div class="stat"><span>${admin(data) ? '实例' : '授权实例'}</span><strong>${instances.length}</strong></div><div class="stat"><span>运行中</span><strong>${running}</strong></div><div class="stat"><span>${admin(data) ? '规则草稿' : '授权规则草稿'}</span><strong>${configAllowed ? tunnels.length : '—'}</strong></div><div class="stat"><span>配置待应用</span><strong>${configAllowed ? pending : '—'}</strong></div></div>
    <div class="two-column"><section class="panel"><div class="panel-content"><div class="section-heading"><h2>实例状态</h2>${button('go-instances','查看全部')}</div>${instances.length ? `<div class="table-wrap"><table><thead><tr><th>名称</th><th>类型</th><th>状态</th></tr></thead><tbody>${instances.map(item => `<tr><td>${e(item.name)}</td><td>${e(item.kind)}</td><td>${status(runtimeFor(data,item.id))}</td></tr>`).join('')}</tbody></table></div>` : empty('尚无实例','创建客户端或服务端实例后在这里查看。')}</div></section>
    <section class="panel"><div class="panel-content"><div class="section-heading"><h2>最近变更</h2><span class="pill">草稿 r${data.revision}</span></div>${!admin(data) ? empty('无查看权限','变更记录仅供管理员查看。') : recent.length ? `<div>${recent.map(item => `<p>${e(item.message)}<br><small>${e(item.at ? new Date(item.at).toLocaleString('zh-CN') : '')}</small></p>`).join('')}</div>` : empty('暂无记录','配置变更后将显示在这里。')}</div></section></div><p class="small-note">实例运行不等于代理已注册或公网可达。草稿变更需重新应用到实例。</p>
    ${trafficAllowed && instances.length ? `<section class="panel"><div class="panel-content"><div class="section-heading"><div><h2>实例流量</h2><p class="small-note">当前运行代次与近 90 天的已记录采样。</p></div><div class="actions">${button('traffic-refresh','刷新流量')}${button('traffic-export','导出历史 CSV')}</div></div><label>实例<select data-traffic-instance>${instances.map(item => `<option value="${e(item.id)}" ${item.id === selected ? 'selected' : ''}>${e(item.name)} · ${e(item.kind)}</option>`).join('')}</select></label><div data-traffic-output>正在读取…</div><div data-traffic-history></div></div></section>` : ''}`;
  },
  mount(root,data) {
   const picker = root.querySelector('[data-traffic-instance]');
   let serial = 0, history = null;
   const bytes = value => typeof value === 'string' && /^(0|[1-9]\d*)$/.test(value) ? value.replace(/\B(?=(\d{3})+(?!\d))/g, ',').replace(/,/g,',<wbr>') + ' B' : '—';
   const rate = value => typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value.toLocaleString('zh-CN',{maximumFractionDigits:2}) + ' B/s' : '—';
   const table = (items, server) => items.length ? `<div class="table-wrap"><table><thead><tr><th>${server ? '代理' : '规则'}</th><th>类型</th><th>接收</th><th>发送</th></tr></thead><tbody>${items.map(item => `<tr><td>${e(item.name)}</td><td>${e(item.type || item.kind)}</td><td>${bytes(item.receivedBytes)}</td><td>${bytes(item.sentBytes)}</td></tr>`).join('')}</tbody></table></div>` : '<p class="small-note">暂无可显示的明细。</p>';
   function chart(curve) {
    const usable = value => typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value : null;
    const points = (Array.isArray(curve) ? curve : []).filter(point => point && Number.isFinite(Date.parse(point.sampledAt))).map(point => ({at:Date.parse(point.sampledAt),received:usable(point.receivedBytesPerSecond),sent:usable(point.sentBytesPerSecond)})).sort((a,b) => a.at-b.at).slice(-120);
    const values = points.flatMap(point => [point.received,point.sent]).filter(value => value !== null);
    if (!values.length) return '<p class="small-note">当前运行代次没有可显示的速率采样；缺失不代表零流量。</p>';
    const first = points[0].at, last = points[points.length-1].at, max = Math.max(...values);
    const x = at => first === last ? 200 : 4 + (at-first)/(last-first)*392;
    const y = value => max === 0 ? 94 : 94-value/max*88;
    function paths(key,color) {
     const pieces = []; let group = [];
     const flush = () => { if (group.length === 1) pieces.push(`<circle cx="${group[0][0]}" cy="${group[0][1]}" r="2.5" fill="${color}"></circle>`); else if (group.length > 1) pieces.push(`<polyline points="${group.map(pair => pair.join(',')).join(' ')}" fill="none" stroke="${color}" stroke-width="1.5"></polyline>`); group = []; };
     for (const point of points) { if (point[key] === null) flush(); else group.push([x(point.at).toFixed(2),y(point[key]).toFixed(2)]); }
     flush(); return pieces.join('');
    }
    return `<p class="small-note">速率曲线 · 接收（粉） / 发送（金） · 上限 ${rate(max)}</p><svg viewBox="0 0 400 100" preserveAspectRatio="none" width="100%" height="150" role="img" aria-label="本次运行的接收与发送速率；缺失采样不连线">${paths('received','#e99ca8')}${paths('sent','#d9b17c')}</svg><p class="small-note">${e(new Date(first).toLocaleString('zh-CN'))} — ${e(new Date(last).toLocaleString('zh-CN'))}</p>`;
   }
   async function loadTraffic() {
    if (!picker) return;
    const id = picker.value, item = data.state.instances.find(value => value.id === id), sequence = ++serial;
    if (!item) return; K.selectedInstance = id; history = null;
    const output = root.querySelector('[data-traffic-output]'), historyOutput = root.querySelector('[data-traffic-history]');
    output.textContent = '正在读取…'; historyOutput.textContent = '';
    const query = '?id=' + encodeURIComponent(id), runId = runtimeFor(data,id).runId || '';
    const [current, detail, daily] = await Promise.allSettled([
     K.api((item.kind === 'frps' ? 'server-traffic' : 'traffic')+query),
     item.kind === 'frpc' && (admin(data) || data.identity?.viewConfig) ? K.api('client-traffic'+query) : Promise.resolve(null),
     K.api('traffic-history'+query)
    ]);
    if (!K.isCurrent('overview',root) || sequence !== serial || picker.value !== id) return;
    if ((runtimeFor(K.state(),id).runId || '') !== runId) { output.textContent = '实例运行代次已变化，请刷新流量。'; return; }
    if (current.status !== 'fulfilled') output.textContent = '读取当前流量失败：'+current.reason.message;
    else {
     const value = item.kind === 'frps' ? current.value : current.value.instances?.[id];
     if (!value || (value.runId || '') !== runId || (item.kind === 'frps' && value.instanceId !== id)) output.textContent = '实例运行代次已变化，请刷新流量。';
     else if (!value.available) output.innerHTML = `<p class="muted">当前流量采样不可用${value.reason ? '：'+e(value.reason) : '；这不代表流量为零。'}</p>${item.kind === 'frpc' ? chart(value.curve) : ''}`;
     else {
      const details = item.kind === 'frps' ? (value.detailsAllowed === false ? '<p class="small-note">当前账户无代理明细权限。</p>' : table(value.proxies || [],true)) : detail.status === 'fulfilled' && detail.value?.available && detail.value.runId === runId ? table(detail.value.rules || [],false) : '<p class="small-note">本次运行的规则流量明细不可用。</p>';
      output.innerHTML = `<div class="stat-grid"><div class="stat"><span>接收</span><strong>${bytes(value.receivedBytes)}</strong></div><div class="stat"><span>发送</span><strong>${bytes(value.sentBytes)}</strong></div>${item.kind === 'frpc' ? `<div class="stat"><span>接收速率</span><strong>${rate(value.receivedBytesPerSecond)}</strong></div><div class="stat"><span>发送速率</span><strong>${rate(value.sentBytesPerSecond)}</strong></div>` : ''}</div><p class="small-note">采样时间：${e(value.sampledAt ? new Date(value.sampledAt).toLocaleString('zh-CN') : '未知')}。${item.kind === 'frps' ? '服务端统计转发边界，可能包含协议开销，不包含绕过服务端的 P2P 流量。' : '客户端统计转发载荷，不包含协议开销。'}重启后重新计数；不适用于计费。</p>${item.kind === 'frpc' ? chart(value.curve) : ''}${value.proxiesTruncated ? '<p class="small-note">代理明细已截断，总量仍包含未列出的代理。</p>' : ''}${details}`;
     }
    }
    if (daily.status !== 'fulfilled') historyOutput.textContent = '读取流量历史失败：'+daily.reason.message;
    else if (daily.value.instanceId !== id || daily.value.kind !== item.kind) historyOutput.textContent = '实例已变化，请刷新流量历史。';
    else {
     history = daily.value;
     const days = (history.days || []).slice(-90).reverse();
     const months = Array.isArray(history.months) ? history.months.slice().reverse() : null;
     const monthly = months === null ? '<p class="small-note">当前后端未提供月度汇总。</p>' : months.length ? `<div class="table-wrap"><table><thead><tr><th>UTC 月份</th><th>已记录日期</th><th>记录天数</th><th>接收</th><th>发送</th><th>覆盖</th></tr></thead><tbody>${months.map(month => `<tr><td>${e(month.month)}</td><td>${e(month.firstDate)} 至 ${e(month.lastDate)}</td><td>${Number.isSafeInteger(month.recordedDays) && month.recordedDays >= 0 ? month.recordedDays : '—'}</td><td>${bytes(month.receivedBytes)}</td><td>${bytes(month.sentBytes)}</td><td>${month.partial ? '不完整' : '已记录'}</td></tr>`).join('')}</tbody></table></div>` : '<p class="small-note">暂无月度采样汇总。</p>';
     historyOutput.innerHTML = `<details><summary>近 90 天历史 · ${days.length} 天</summary><p class="small-note">仅累计确认的采样增量；缺少日期或数据不完整不代表零流量。${history.storageError ? '历史文件读取或写入失败。' : history.saved ? '数据已写入。' : '存在待写入采样。'}</p>${days.length ? `<div class="table-wrap"><table><thead><tr><th>UTC 日期</th><th>接收</th><th>发送</th><th>覆盖</th></tr></thead><tbody>${days.map(day => `<tr><td>${e(day.date)}</td><td>${bytes(day.receivedBytes)}</td><td>${bytes(day.sentBytes)}</td><td>${day.partial ? '不完整' : '已记录'}</td></tr>`).join('')}</tbody></table></div>` : '<p class="small-note">暂无历史采样。</p>'}<h3>月度汇总</h3><p class="small-note">仅汇总已记录的采样；90 天保留窗口可能截断月初或月末，不适用于计费或配额。</p>${monthly}</details>`;
    }
   }
   let timer = null;
   function schedule() {
    clearTimeout(timer);
    if (!K.isCurrent('overview',root) || !picker) return;
    timer = setTimeout(async () => {
     if (!document.hidden && !root.querySelector('[data-traffic-history] details[open]') && !root.contains(document.activeElement) && !window.getSelection()?.toString()) await loadTraffic();
     schedule();
    },10000);
   }
   if (picker) { picker.addEventListener('change',() => { void loadTraffic(); }); void loadTraffic(); schedule(); }
   overviewCleanup = () => { ++serial; clearTimeout(timer); };
   bindButtons(root, action => {
    if (action === 'go-instances') K.navigate('instances');
    if (action === 'go-rules') K.navigate('tunnels');
    if (action === 'traffic-refresh') return loadTraffic();
    if (action === 'traffic-export') {
     if (!history?.days?.length) throw new Error('当前没有可导出的历史数据。');
     const rows = history.days.filter(day => /^\d{4}-\d{2}-\d{2}$/.test(day.date) && /^\d+$/.test(day.receivedBytes) && /^\d+$/.test(day.sentBytes)).map(day => `${day.date},${day.receivedBytes},${day.sentBytes},${day.partial ? 'true' : 'false'}`);
     if (!rows.length) throw new Error('历史数据格式无效，无法导出。');
     K.download('kurumi-traffic-history.csv','\ufeffdate_utc,received_bytes,sent_bytes,partial\r\n'+rows.join('\r\n')+'\r\n','text/csv');
    }
   });
  },
  dispose() { overviewCleanup?.(); overviewCleanup = null; }
 });

 K.register('instances', {
  render(data) {
   const items = data.state.instances || [], restricted = !admin(data);
   const cards = items.map(item => {
    const runtime = runtimeFor(data,item.id), running = runtime.state === 'running', transitional = ['starting','stopping'].includes(runtime.state);
    const allowed = action => can(data,item.id,action) ? button(action,({start:'启动',stop:'停止',restart:'重启并应用',validate:'校验配置'})[action],item.id,action === 'start' ? 'primary' : '') : '';
    const controls = (running ? allowed('stop') + allowed('restart') : transitional ? allowed('stop') : allowed('start')) + allowed('validate');
    const details = admin(data) || data.identity?.viewConfig;
    const logs = admin(data) || (data.identity?.logInstances || []).includes(item.id);
    const plan = data.schedules?.[item.id];
    return `<article class="card"><div class="section-heading"><div><span class="eyebrow">${e(item.kind)}</span><h2>${e(item.name)}</h2></div><div>${status(runtime)}</div></div>
     ${details ? `<p><span class="muted">地址</span> <code>${e(item.host)}:${e(item.port)}</code></p><p><span class="muted">规则</span> ${data.state.tunnels.filter(rule => rule.instanceId === item.id).length} 条 · <span class="muted">定时计划</span> ${!plan ? '未设置' : !plan.enabled ? '已关闭' : plan.paused ? '已暂停' : '已启用'}</p>` : ''}
     ${runtime.exit ? `<p class="notice">${e(runtime.exit)}</p>` : ''}${plan?.reason ? `<p class="small-note">${e(plan.reason)}</p>` : ''}
     <div class="actions">${controls}${logs ? button('logs','日志',item.id) : ''}${details ? button('config','配置',item.id) : ''}${admin(data) || data.identity?.viewTraffic ? button('traffic','流量',item.id) : ''}${admin(data) ? button('health','诊断',item.id) : ''}</div>
     ${admin(data) ? `<div class="actions">${button('edit-instance','编辑',item.id)}${button('schedule','定时计划',item.id)}${runtime.applied ? button('diff','配置差异',item.id) : ''}${button('duplicate-instance','复制',item.id)}${button('delete-instance','删除',item.id,'danger')}</div>` : ''}</article>`;
   }).join('');
   const warnings = (data.portWarnings || []).map(message => `<p>${e(message)}</p>`).join('');
   return head(restricted ? '授权实例' : '运行实例', restricted ? '仅显示当前账户有权查看的实例。' : '创建、校验和运行本机客户端或服务端。', admin(data) ? button('new-instance','新建实例','','primary') : '') + (warnings ? `<div class="notice">${warnings}</div>` : '') + (cards ? `<div class="cards">${cards}</div>` : empty('暂无实例',restricted ? '请联系管理员分配实例。' : '创建实例后可在此管理运行状态。'));
  },
  mount(root,data) { bindButtons(root, async (action,id) => {
   if (['start','stop','restart','validate'].includes(action)) return control(action,id,data);
   if (action === 'new-instance' || action === 'edit-instance') return editObject('instance',id,data);
   if (action === 'schedule') return editSchedule(id,data);
   if (action === 'traffic') { pickInstance(id); K.navigate('overview'); return; }
   if (['logs','config','health'].includes(action)) { pickInstance(id); K.navigate(action); return; }
   if (action === 'diff') { K.showDiff(await K.api('diff?id='+encodeURIComponent(id))); return; }
   requireRevision(data);
   const item = data.state.instances.find(value => value.id === id);
   if (!item) throw new Error('实例已不存在。');
   if (action === 'delete-instance') {
    if (!confirm(`删除实例「${item.name}」？请先停止实例并删除关联规则。`)) return;
    await mutation('delete',{revision:data.revision,kind:'instance',id},'实例草稿已删除。');
   }
   if (action === 'duplicate-instance') {
    const count = data.state.tunnels.filter(rule => rule.instanceId === id).length;
    const includeTunnels = item.kind === 'frpc' && count > 0 && confirm(`同时复制关联的 ${count} 条规则？副本默认停用。`);
    if (!confirm(`复制「${item.name}」为独立草稿实例？不会启动副本。`)) return;
    let result;
    try { result = await K.api('duplicate',{revision:data.revision,kind:'instance',id,includeTunnels}); }
    catch (error) { if (!error.status || error.status >= 500) { await K.refresh().catch(() => {}); throw new Error('复制结果未确认，请刷新并核对实例后重试。'); } throw error; }
    try { await K.refresh(); K.toast(`实例已复制，附带 ${result.rulesCopied || 0} 条停用规则。`); }
    catch (_) { if (K.state()) K.toast('实例已复制，但刷新失败；请手动刷新核对。'); }
   }
  }); }
 });

 let rulesCleanup = null;
 K.register('tunnels', {
  render(data) {
   const rows = data.state.tunnels || [];
   if (!admin(data) && !data.identity?.viewConfig) return head('连接规则','当前账户没有配置详情查看权限。') + empty('无查看权限','请联系管理员授予配置查看权限。');
   const actions = admin(data) ? button('batch-create','批量创建') + button('new-tunnel','新建规则','','primary') : '';
   const cards = rows.map(rule => {
    const instance = data.state.instances.find(item => item.id === rule.instanceId), unavailable = Protocols.probeUnavailable(rule);
    return `<article class="card" data-rule="${e(rule.id)}" data-search="${e([rule.name,rule.type,rule.localHost,rule.localPort,rule.remotePort,instance?.name,Protocols.remote(rule)].join(' ').toLocaleLowerCase())}" data-type="${e(rule.type)}"><div class="section-heading"><div>${admin(data) ? `<label class="form-check"><input type="checkbox" data-select="${e(rule.id)}">选择</label>` : ''}<h2>${e(rule.name)}</h2><span class="muted">${e(instance?.name || '未知实例')} · ${e(rule.type)}</span></div><span class="pill">${rule.enabled ? '草稿启用' : '草稿停用'}</span></div>
     <p><span class="muted">本地目标 / 监听</span> <code>${e(Protocols.endpoint(rule))}</code> → <span class="muted">远端 / 配对</span> <code>${e(Protocols.remote(rule))}</code></p>
     <p>实例：<span data-rule-runtime="${e(rule.instanceId)}">${status(runtimeFor(data,rule.instanceId))}</span> · 代理注册：<span data-registration="${e(rule.id)}">等待读取</span></p><p class="small-note" data-probe="${e(rule.id)}"></p>
     ${admin(data) ? `<div class="actions">${button('probe',unavailable ? '不支持检测' : '检测目标',rule.id)}${button('edit-tunnel','编辑',rule.id)}${button('duplicate-tunnel','复制',rule.id)}${button('toggle',rule.enabled ? '草稿停用' : '草稿启用',rule.id)}${button('delete-tunnel','删除',rule.id,'danger')}</div>` : ''}</article>`;
   }).join('');
   return head('连接规则','筛选规则并查看草稿状态；修改不会自动应用到运行中的实例。',actions) +
    `<p class="notice" data-rules-stale hidden>草稿已变化，请刷新后重新选择并操作。</p><div class="toolbar"><label>搜索<input type="search" data-filter placeholder="规则、实例或地址"></label><label>协议<select data-protocol><option value="">全部协议</option>${Protocols.types.map(type => `<option value="${e(type)}">${e(type)}</option>`).join('')}</select></label>${admin(data) && rows.length ? button('probe-visible','检测当前筛选') : ''}</div>
    ${admin(data) && rows.length ? `<div class="toolbar"><label class="form-check"><input type="checkbox" data-select-visible>选择当前筛选</label><span data-selection-count>已选 0 条</span><div class="actions">${button('batch-enable','批量启用')}${button('batch-disable','批量停用')}${button('batch-delete','批量删除','','danger')}${button('batch-export','导出选中配置')}</div></div>` : ''}
    <div class="cards" data-rule-list>${cards || empty('暂无规则',admin(data) ? '先创建 frpc 客户端，再添加规则。' : '当前授权实例没有可显示的规则。')}</div><p class="small-note" data-filter-empty hidden>没有匹配的规则。</p>`;
  },
  mount(root,data) {
   if (!admin(data) && !data.identity?.viewConfig) return;
   const selected = new Set(), cards = [...root.querySelectorAll('[data-rule]')];
   const refreshSelection = () => { const label = root.querySelector('[data-selection-count]'); if (label) label.textContent = `已选 ${selected.size} 条`; };
   const applyFilter = () => {
    const query = root.querySelector('[data-filter]').value.toLocaleLowerCase(), type = root.querySelector('[data-protocol]').value;
    selected.clear(); cards.forEach(card => { card.hidden = !card.dataset.search.includes(query) || (!!type && card.dataset.type !== type); const checkbox = card.querySelector('[data-select]'); if (checkbox) checkbox.checked = false; });
    const all = root.querySelector('[data-select-visible]'); if (all) all.checked = false;
    root.querySelector('[data-filter-empty]').hidden = !cards.length || cards.some(card => !card.hidden); refreshSelection();
   };
   root.querySelector('[data-filter]').addEventListener('input',applyFilter);
   root.querySelector('[data-protocol]').addEventListener('change',applyFilter);
   root.addEventListener('change',event => {
    if (event.target.matches('[data-select]')) { if (event.target.checked) selected.add(event.target.dataset.select); else selected.delete(event.target.dataset.select); refreshSelection(); }
    if (event.target.matches('[data-select-visible]')) { cards.filter(card => !card.hidden).forEach(card => { const input = card.querySelector('[data-select]'); if (!input) return; input.checked = event.target.checked; if (input.checked) selected.add(input.dataset.select); else selected.delete(input.dataset.select); }); refreshSelection(); }
   });
   let observationTimer = null, stopped = false;
   async function observeRules() {
    if (stopped || !K.isCurrent('tunnels',root)) return;
    const current = K.state(), stale = current.revision !== data.revision;
    root.querySelector('[data-rules-stale]').hidden = !stale;
    for (const item of root.querySelectorAll('[data-rule-runtime]')) {
     if (stale) item.textContent = '草稿已变化'; else item.innerHTML = status(runtimeFor(current,item.dataset.ruleRuntime));
    }
    const outputs = [...root.querySelectorAll('[data-registration]')];
    if (stale) outputs.forEach(item => { item.textContent = '草稿已变化，请刷新'; });
    else if (!admin(data)) outputs.forEach(item => { item.textContent = '当前账户不可读取'; });
    else {
     try {
      const result = await K.api('registration');
      if (stopped || !K.isCurrent('tunnels',root)) return;
      const latest = K.state();
      if (result.revision !== data.revision || latest.revision !== data.revision) { outputs.forEach(item => { item.textContent = '草稿已变化，请刷新'; }); return; }
      for (const item of outputs) {
       const rule = data.state.tunnels.find(value => value.id === item.dataset.registration), observed = result.tunnels?.[item.dataset.registration];
       if (!rule || (observed?.runId || '') !== (runtimeFor(latest,rule.instanceId).runId || '')) { item.textContent = '运行代次已变化，等待更新'; continue; }
       item.textContent = (registrationStates[observed?.state] || '状态未知') + (observed?.portPending ? ' · 动态端口待确认' : '') + (observed?.remoteAddr ? ' · ' + observed.remoteAddr : '') + (observed?.error ? ' · ' + observed.error : '');
      }
     } catch (_) { if (!stopped && K.isCurrent('tunnels',root)) outputs.forEach(item => { item.textContent = '读取失败'; }); }
    }
   }
   function scheduleObservation() {
    if (stopped || !K.isCurrent('tunnels',root)) return;
    observationTimer = setTimeout(async () => { if (!document.hidden) await observeRules(); scheduleObservation(); },10000);
   }
   void observeRules(); scheduleObservation();
   rulesCleanup = () => { stopped = true; clearTimeout(observationTimer); };
   bindButtons(root, async (action,id,target) => {
    if (action === 'new-tunnel' || action === 'edit-tunnel') return editObject('tunnel',id,data);
    if (action === 'batch-create') return editBatch(data);
    requireRevision(data);
    if (action === 'probe-visible') {
     const visible = cards.filter(card => !card.hidden).map(card => data.state.tunnels.find(rule => rule.id === card.dataset.rule)).filter(Boolean);
     const supported = visible.filter(rule => !Protocols.probeUnavailable(rule));
     if (!supported.length) throw new Error(visible.length ? '当前筛选的规则不支持目标检测。' : '当前筛选没有规则。');
     let complete = 0;
     try {
      for (const rule of supported) {
       if (!K.isCurrent('tunnels',root) || K.state()?.revision !== data.revision) { K.toast('草稿已变化，已停止批量检测；请刷新后核对。'); return; }
       target.textContent = `检测中 ${complete}/${supported.length}`;
       const output = cards.find(card => card.dataset.rule === rule.id)?.querySelector('[data-probe]');
       try { const result = await K.api('probe',{revision:data.revision,id:rule.id}); if (output?.isConnected && K.state()?.revision === data.revision) output.textContent = `${result.status} · ${result.message}`; }
       catch (error) {
        if (error.status === 409) { K.toast('草稿已变化，已停止批量检测；请刷新后核对。'); return; }
        if (output?.isConnected) output.textContent = '检测失败：'+error.message;
       }
       complete++;
      }
      if (K.isCurrent('tunnels',root)) K.toast(`已检测 ${complete} 条规则；${visible.length-supported.length} 条不支持检测。`);
     } finally {
      if (target.isConnected) target.textContent = '检测当前筛选';
     }
     return;
    }
    if (['batch-enable','batch-disable','batch-delete','batch-export'].includes(action)) {
     const ids = [...selected]; if (!ids.length) throw new Error('请先选择规则。');
     if (action === 'batch-export') {
      if (!confirm(`导出 ${ids.length} 条规则？ZIP 包含明文认证令牌、密钥及 HTTP 头值，请妥善保管。`)) return;
      const response = await fetch('/api/tunnels/export',{method:'POST',credentials:'same-origin',cache:'no-store',headers:{'Content-Type':'application/json'},body:JSON.stringify({revision:data.revision,ids,confirmed:true})});
      if (!response.ok) { let message = '导出失败。'; try { message = (await response.json()).error || message; } catch (_) {} if (response.status === 401) void K.refresh().catch(() => {}); throw new Error(message); }
      if (!(response.headers.get('Content-Type') || '').toLowerCase().startsWith('application/zip')) throw new Error('导出响应格式无效。');
      const blob = await response.blob(); if (!blob.size || blob.size > 20*1024*1024) throw new Error('导出文件无效或超过大小限制。');
      if (!K.isCurrent('tunnels',root) || K.state()?.revision !== data.revision) throw new Error('草稿已变化，请刷新后重新导出。');
      K.download('kurumi-selected-rules.zip',blob,'application/zip'); K.toast('导出完成，请妥善保管明文凭据。'); return;
     }
     const deleting = action === 'batch-delete', enabled = action === 'batch-enable';
     if (!confirm(`${deleting ? '删除' : enabled ? '启用' : '停用'}选中的 ${ids.length} 条规则草稿？运行中的实例不会立即改变。`)) return;
     try { await mutation(deleting ? 'tunnels/batch-delete' : 'tunnels/batch-enabled', {revision:data.revision,ids,...(deleting ? {} : {enabled})},'规则草稿已更新。'); }
     catch (error) { await K.refresh().catch(() => {}); throw new Error(`批量操作未完成或结果未确认：${error.message}。请核对草稿。`); }
     return;
    }
    const rule = data.state.tunnels.find(value => value.id === id); if (!rule) throw new Error('规则已不存在。');
    if (action === 'probe') {
     const unavailable = Protocols.probeUnavailable(rule); if (unavailable) throw new Error(unavailable);
     const result = await K.api('probe',{revision:data.revision,id});
     if (!K.isCurrent('tunnels',root) || K.state()?.revision !== data.revision) return;
     const output = target.closest('[data-rule]').querySelector('[data-probe]'); output.textContent = `${result.status} · ${result.message}`; return;
    }
    if (action === 'duplicate-tunnel') {
     const clients = data.state.instances.filter(item => item.kind === 'frpc');
     const current = clients.findIndex(item => item.id === rule.instanceId), input = prompt('复制到哪个客户端？输入编号：\n' + clients.map((item,index) => `${index+1}: ${item.name}`).join('\n'),String(current+1));
     if (input === null) return; const destination = /^\d+$/.test(input.trim()) ? clients[Number(input.trim())-1] : null;
     if (!destination) throw new Error('目标客户端编号无效。');
     await mutation('duplicate',{revision:data.revision,kind:'tunnel',id,targetInstanceId:destination.id},'已创建停用的规则副本，请核对配置。'); return;
    }
    if (action === 'toggle') { await mutation('save',{revision:data.revision,kind:'tunnel',value:{...rule,enabled:!rule.enabled}},'草稿意图已保存，请重新应用实例。'); return; }
    if (action === 'delete-tunnel' && confirm(`删除规则「${rule.name}」的草稿？`)) await mutation('delete',{revision:data.revision,kind:'tunnel',id},'规则草稿已删除。');
   });
  },
  dispose() { rulesCleanup?.(); rulesCleanup = null; }
 });

 K.register('health', {
  render(data) {
   if (!admin(data)) return head('链路诊断','当前账户没有诊断权限。') + empty('无操作权限','请联系管理员运行链路诊断。');
   const instances = data.state.instances || [], selected = instances.some(item => item.id === K.selectedInstance) ? K.selectedInstance : instances[0]?.id || '';
   return head('链路诊断','按需检查控制端口、规则注册状态和本地目标。') + (instances.length ? `<section class="panel"><div class="panel-content"><label>实例<select data-instance>${instances.map(item => `<option value="${e(item.id)}" ${item.id === selected ? 'selected' : ''}>${e(item.name)} · ${e(item.kind)}</option>`).join('')}</select></label><p class="small-note">检测不代表公网可达，报告包含网络地址。</p><div class="actions">${button('health-check','开始诊断','','primary')}${button('health-export','导出报告')}</div><div data-report></div></div></section>` : empty('没有可诊断的实例','先创建实例，再开始诊断。'));
  },
  mount(root,data) {
   let report = null, reportRuntime = '';
   const picker = root.querySelector('[data-instance]');
   const runtimeKey = id => { const value = runtimeFor(K.state(),id); return JSON.stringify([value.state || '',value.pid || 0,value.started || '',value.runId || '']); };
   picker?.addEventListener('change',() => { report = null; reportRuntime = ''; K.selectedInstance = picker.value; root.querySelector('[data-report]').replaceChildren(); });
   bindButtons(root,async action => {
    if (action === 'health-export') {
     if (!report) throw new Error('请先完成诊断。');
     if (K.state()?.revision !== data.revision || runtimeKey(picker.value) !== reportRuntime) throw new Error('实例或草稿已变化，请重新诊断。');
     K.download('kurumi-health.json',JSON.stringify(report,null,2)); return;
    }
    if (action !== 'health-check') return;
    requireRevision(data);
    const id = picker.value, runId = runtimeFor(K.state(),id).runId || '', currentRuntime = runtimeKey(id);
    const output = root.querySelector('[data-report]'); output.textContent = '正在诊断…';
    let value;
    try { value = await K.api('health',{revision:data.revision,id}); }
    catch (error) { if (output.isConnected && picker.value === id) output.textContent = '诊断失败：'+error.message; throw error; }
    if (!K.isCurrent('health',root) || picker.value !== id) return;
    if (K.state()?.revision !== data.revision || runtimeKey(id) !== currentRuntime || (value.runId || '') !== runId || value.runtime !== runtimeFor(K.state(),id).state) { report = null; output.textContent = '实例或草稿已变化，请重新诊断。'; return; }
    report = value; reportRuntime = currentRuntime;
    output.innerHTML = `<h2>${e(value.instance)} · ${e(value.kind)}</h2><p class="small-note">检查时间：${e(value.checkedAt ? new Date(value.checkedAt).toLocaleString('zh-CN') : '未知')}</p><p>运行状态：${e(value.runtime)}${value.pending ? ' · 草稿待应用' : ''}</p><p>控制端口：${e(value.endpoint?.state)} · ${e(value.endpoint?.message)}</p>${value.rules?.length ? `<div class="table-wrap"><table><thead><tr><th>规则</th><th>代理注册</th><th>本地目标</th></tr></thead><tbody>${value.rules.map(rule => `<tr><td>${e(rule.name)} · ${e(rule.type)}</td><td>${e(registrationStates[rule.registration?.state] || rule.registration?.state || '未知')}</td><td>${e(rule.target?.status)} · ${e(rule.target?.message)}</td></tr>`).join('')}</tbody></table></div>` : '<p>没有关联规则。</p>'}<p class="small-note">${e(value.note)}</p>`;
   });
  }
 });

 K.register('core', {
  render(data) {
   const available = data.runtimeAvailable || {};
   return head('运行核心','查看工作台使用的运行后端及可用性。') + `<section class="panel"><div class="panel-content"><p>当前后端：${e(data.runtimeBackend === 'embedded' ? '内置核心' : '自定义后端')}</p><p>frpc：${available.frpc ? '可用' : '不可用'} · frps：${available.frps ? '可用' : '不可用'}</p><p class="small-note">内置核心随工作台更新；自定义后端请在本机管理其文件。</p></div></section>`;
  }
 });
})();
