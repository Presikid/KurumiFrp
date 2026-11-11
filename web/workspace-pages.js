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
 const empty = (title, text, artwork = '') => `<div class="empty">${artwork ? `<img src="/assets/${artwork}.webp" alt="" aria-hidden="true">` : ''}<strong>${e(title)}</strong><p>${e(text)}</p></div>`;
 const can = (data, id, action) => admin(data) || (data.identity?.role === 'operator' && (data.identity.operableInstances || []).includes(id) && (data.identity.allowedActions || []).includes(action));
 const button = (action, label, id = '', classes = '') => `<button type="button" ${classes ? `class="${classes}"` : ''} data-action="${action}" ${id ? `data-id="${e(id)}"` : ''}>${e(label)}</button>`;
 const runtimeFor = (data, id) => data.runtime?.[id] || {state:'stopped'};
 const pickInstance = id => { K.selectedInstance = id; };
 const localControlPrefix = 'kurumi-local-control-v1:';
 const localControlInFlight = new Set(), localControlReviewing = new Set();
 const localControlMemory = new Map(), localControlDismissed = new Map();
 const controlLabels = {start:'启动', stop:'停止', restart:'重启'};
 const notifyLocalControl = () => window.dispatchEvent(new Event('kurumi-local-control-change'));
 function localControlNotice(id) {
  const memory = localControlMemory.get(id);
  let stored;
  try { stored = localStorage.getItem(localControlPrefix + id); } catch (_) {}
  const raw = memory?.raw || (stored !== localControlDismissed.get(id) ? stored : null);
  if (raw == null) return null;
  let record;
  try { record = JSON.parse(raw); } catch (_) { record = null; }
  return {raw, phase:record?.phase === 'pending' ? 'pending' : 'uncertain', action:Object.hasOwn(controlLabels,record?.action) ? controlLabels[record.action] : '启停',
   volatile:!!memory && stored !== memory.raw, error:memory?.errorEpoch === K.epoch() ? memory.error : ''};
 }
 function beginLocalControl(id,action) {
  if (localControlInFlight.has(id) || localControlNotice(id)) { notifyLocalControl(); throw new Error('此实例有操作正在处理或结果未确认，请先核对并解除提示。'); }
  const raw = JSON.stringify({phase:'pending',action,nonce:`${Date.now()}:${Math.random()}`});
  localControlMemory.set(id,{raw});
  try {
   const key = localControlPrefix + id;
   localStorage.setItem(key,raw);
  } catch (_) { /* Keep the page-local record when storage is unavailable. */ }
  localControlInFlight.add(id); notifyLocalControl(); return raw;
 }
 function clearLocalControl(id,expected) {
  const memory = localControlMemory.get(id), key = localControlPrefix + id;
  if (memory && memory.raw !== expected) return false;
  let current, readable = false;
  try { current = localStorage.getItem(key); readable = true; } catch (_) {}
  if (!memory && readable && current !== null && current !== expected) return false;
  if (readable && current === expected) {
   try {
    localStorage.removeItem(key);
    if (localStorage.getItem(key) === expected) localControlDismissed.set(id,expected);
    else if (localControlDismissed.get(id) === expected) localControlDismissed.delete(id);
   } catch (_) { localControlDismissed.set(id,expected); }
  } else if (!readable) localControlDismissed.set(id,expected);
  if (memory) localControlMemory.delete(id);
  return true;
 }
 function markLocalControlUncertain(id,expected,error) {
  const memory = localControlMemory.get(id);
  if (memory?.raw === expected && error.status) { memory.error = error.message; memory.errorEpoch = K.epoch(); }
  // A pending record without an active request is shown as uncertain, including after reload.
 }
 function paintLocalControls(root) {
  for (const card of root.querySelectorAll('[data-local-instance]')) {
   const id = card.dataset.localInstance, notice = localControlNotice(id), inFlight = localControlInFlight.has(id);
   const box = card.querySelector('[data-local-control-notice]'), text = card.querySelector('[data-local-control-text]'), acknowledge = card.querySelector('[data-action="ack-local-control"]');
   box.hidden = !notice && !inFlight;
   const volatile = notice?.volatile ? '提示仅保存在本页面，其他标签页不共享；关闭或重新加载页面后会丢失。' : '';
   if (inFlight) text.textContent = `实例操作正在等待结果；请勿重复提交。${volatile}`;
   else if (notice) text.textContent = `${notice.error ? `服务端返回：${notice.error}。` : ''}上次${notice.action}结果尚未确认。请刷新并独立核对实际状态及先前请求是否仍在处理。${volatile}${acknowledge ? '' : '请联系有操作权限的账户核对并解除。'}`;
   if (acknowledge) { acknowledge.hidden = !notice || inFlight; acknowledge.disabled = localControlReviewing.has(id); }
   card.querySelectorAll('[data-action="start"], [data-action="stop"], [data-action="restart"]').forEach(element => { element.disabled = !!notice || inFlight; });
  }
 }
 async function acknowledgeLocalControl(id) {
  if (localControlReviewing.has(id) || localControlInFlight.has(id)) throw new Error('操作仍在处理，请等待结果后再核对。');
  const before = localControlNotice(id);
  if (!before) return;
  const generation = K.epoch(); localControlReviewing.add(id); notifyLocalControl();
  try {
   const refreshed = await K.refresh();
   const state = K.state(), instance = state?.state?.instances?.find(item => item.id === id);
   if (!refreshed || generation !== K.epoch() || !instance) throw new Error('无法确认实例当前状态，请重新登录或刷新后核对。');
   if (!['start','stop','restart'].some(action => can(state,id,action))) throw new Error('当前账户没有此实例的操作权限。');
   const after = localControlNotice(id);
   if (!after || after.raw !== before.raw || localControlInFlight.has(id)) throw new Error('操作记录已变化，请重新核对。');
   const runtime = runtimeFor(state,id);
   if (!confirm(`「${instance.name}」当前状态：${stateLabel(runtime)}。\n刷新只能显示当前状态，不能保证先前请求已经结束。${after.volatile ? '\n提示仅保存在本页面，其他标签页不共享；关闭或重新加载页面后会丢失。' : ''}\n请先独立核对目标和请求状态；确认已核对并解除提示？`)) return;
   if (!clearLocalControl(id,before.raw)) throw new Error('操作记录已变化或无法清除，请重新核对。');
   notifyLocalControl(); K.toast('已解除核对提示。');
  } finally { localControlReviewing.delete(id); notifyLocalControl(); }
 }
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
   finally {
    if (target.isConnected) target.disabled = ['start','stop','restart'].includes(target.dataset.action) && (localControlInFlight.has(target.dataset.id) || !!localControlNotice(target.dataset.id));
   }
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
  let saveUncertain = false;
  const update = form => {
   ProtocolEditor.update(form, editing, data.state.tunnels);
   if (kind === 'tunnel') {
    const moved = !!old && form.elements.instanceId.value !== old.instanceId;
    const apply = form.elements.applyAfterSave;
    apply.disabled = moved;
    if (moved) apply.checked = false;
    form.querySelector('[data-apply-moved]').hidden = !moved;
   }
  };
  const applyOption = kind === 'tunnel' ? `<p><label class="form-check"><input type="checkbox" name="applyAfterSave">保存后重启所属客户端</label></p>
   <p class="small-note">仅在客户端仍是原来的运行代次时重启；会应用该实例的全部草稿。重启会中断连接，可能暂停定时计划。</p>
   <p class="small-note" data-apply-moved hidden>移动规则时只保存草稿；源客户端仍可能运行旧规则，请分别核对并重新应用源和目标客户端。</p>` : '';
  K.openDialog({
   html:kind === 'instance' ? ProtocolEditor.instance(old) : ProtocolEditor.tunnel(old, clients) + applyOption, submitLabel:'保存草稿',
   onReady:update, onChange:(_, form) => update(form), onInput:(event, form) => { if (event.target.name === 'localPort') update(form); },
   onClick:(event, form) => { if (kind === 'tunnel') ProtocolEditor.actionClick(form, event.target); },
   async onSubmit(form) {
    if (saveUncertain) throw new Error('上次保存结果尚未确认，请关闭表单并核对草稿后再操作。');
    requireRevision(data);
    const value = ProtocolEditor.value(form, editing);
    if (kind !== 'tunnel' || !form.elements.applyAfterSave.checked) {
     await mutation('save', {revision:data.revision, kind, value}, '草稿已保存；运行中的实例需要重新应用。');
     return;
    }
    if (old && old.instanceId !== value.instanceId) throw new Error('移动规则时请分别核对并重新应用源和目标客户端。');
    const current = K.state(), instance = current.state.instances.find(item => item.id === value.instanceId);
    const runtime = runtimeFor(current, value.instanceId), epoch = K.epoch();
    if (!instance || !can(current, value.instanceId, 'restart') || runtime.state !== 'running' || !runtime.runId) {
     throw new Error('客户端未运行、状态不确定或无重启权限；可取消勾选，仅保存草稿。');
    }
    if (localControlInFlight.has(value.instanceId) || localControlNotice(value.instanceId)) {
     throw new Error('此客户端有操作正在处理或结果未确认，请先到实例页面核对。');
    }
    if (!confirm(`保存后重启「${instance.name}」并应用该实例全部草稿？\n重启会中断现有连接，失败时可能保持停止，也可能暂停定时计划。`)) return false;
    let result;
    const controller = new AbortController(), deadline = setTimeout(() => controller.abort(), 15000);
    try { result = await K.api('save', {revision:data.revision, kind, value}, {signal:controller.signal}); }
    catch (error) {
     if (!error.status || error.status >= 500) {
      saveUncertain = true; await K.refresh().catch(() => {});
      throw new Error('保存结果尚未确认；没有提交重启。请关闭表单并核对草稿后再操作。');
     }
     throw error;
    } finally { clearTimeout(deadline); }
    if (result?.ok !== true) {
     saveUncertain = true; await K.refresh().catch(() => {});
     throw new Error('保存响应无法确认；没有提交重启。请关闭表单并核对草稿后再操作。');
    }
    const savedRevision = result.revision;
    if (!Number.isSafeInteger(savedRevision) || savedRevision !== data.revision + 1) {
     await K.refresh().catch(() => {});
     if (K.epoch() === epoch && K.state()) K.toast('草稿已保存，但返回的修订号无法确认；未提交重启，请刷新核对。');
     return;
    }
    try {
     if (!await K.refresh() || K.epoch() !== epoch) return;
    } catch (error) {
     if (K.epoch() === epoch && K.state()) K.toast(`草稿已保存（r${savedRevision}）；状态刷新失败，未提交重启：${error.message}`);
     return;
    }
    const fresh = K.state(), updated = runtimeFor(fresh, value.instanceId);
    if (fresh.revision !== savedRevision || updated.state !== 'running' || updated.runId !== runtime.runId) {
     K.toast(`草稿已保存（r${savedRevision}）；配置或实例运行状态已变化，未提交重启。`);
     return;
    }
    try {
     const outcome = await control('restart', value.instanceId, fresh, {confirmed:true, expectedRunId:runtime.runId});
     if (K.epoch() === epoch && K.state()) K.toast(`草稿已保存（r${savedRevision}），重启已受理；${outcome?.needsReview ? '核对记录未能清除，请到实例页面核对并解除提示。' : '请核对实例运行状态。'}`);
    } catch (error) {
     if (K.epoch() === epoch && K.state()) K.toast(`草稿已保存（r${savedRevision}）；重启未完成或结果未确认：${error.message}`);
    }
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
 async function control(action, id, data, options = {}) {
  requireRevision(data);
  const instance = data.state.instances.find(item => item.id === id);
  if (!instance || !can(data, id, action)) throw new Error('实例不存在或没有操作权限。');
  if (action !== 'validate') {
   const notice = localControlNotice(id);
   if (localControlInFlight.has(id) || notice) {
    notifyLocalControl();
    throw new Error('此实例有操作正在处理或结果未确认，请先核对并解除提示。');
   }
  }
  const schedule = data.schedules?.[id];
  const pause = schedule?.enabled && !schedule.paused ? '此操作可能暂停实例的定时计划。' : '';
  if (action === 'restart' && !options.confirmed && !confirm(`重启「${instance.name}」会中断现有连接，重新启动失败时可能保持停止。${pause}继续？`)) return;
  if (action === 'stop' && !confirm(`停止「${instance.name}」并中断连接？${pause}`)) return;
  if (action === 'start' && pause && !confirm(`启动「${instance.name}」？${pause}`)) return;
  if (action === 'validate') {
   try { await K.api('validate',{revision:data.revision,id}); K.toast('配置校验通过；这不代表网络连通。'); }
   finally { await K.refresh().catch(() => {}); }
   return;
  }
  requireRevision(data);
  const controller = new AbortController(), record = beginLocalControl(id,action);
  const deadline = setTimeout(() => controller.abort(),60000);
  try {
   const result = await K.api('control',{revision:data.revision,id,action,...(options.expectedRunId ? {expectedRunId:options.expectedRunId} : {})},{signal:controller.signal});
   if (result?.ok !== true) throw new Error('服务端响应无法确认操作结果。');
   const needsReview = !clearLocalControl(id,record);
   if (needsReview) K.toast('实例操作已受理，但核对记录未能清除；请刷新并核对后解除提示。');
   else K.toast('实例操作已受理，请核对运行状态。');
   return {needsReview};
  } catch (error) {
   if ([401,403,404,405].includes(error.status)) {
    if (!clearLocalControl(id,record)) throw new Error(`${error.message}；操作记录已变化或无法清除，请先核对。`);
    throw error;
   }
   markLocalControlUncertain(id,record,error);
   const volatile = localControlNotice(id)?.volatile ? '提示仅保存在本页面，其他标签页不共享；关闭或重新加载页面后会丢失。' : '';
   throw new Error(`${error.status ? `服务端返回：${error.message}。` : ''}实例操作结果尚未确认，请刷新并独立核对实际状态及请求是否仍在处理，再解除提示。${volatile}`);
  } finally {
   clearTimeout(deadline); localControlInFlight.delete(id); notifyLocalControl();
   await K.refresh().catch(() => {});
  }
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
    <div class="two-column"><section class="panel"><div class="panel-content"><div class="section-heading"><h2>实例状态</h2>${button('go-instances','查看全部')}</div>${instances.length ? `<div class="table-wrap"><table><thead><tr><th>名称</th><th>类型</th><th>状态</th></tr></thead><tbody>${instances.map(item => `<tr><td>${e(item.name)}</td><td>${e(item.kind)}</td><td>${status(runtimeFor(data,item.id))}</td></tr>`).join('')}</tbody></table></div>` : empty('尚无实例','创建客户端或服务端实例后在这里查看。','empty-welcome')}</div></section>
    <section class="panel"><div class="panel-content"><div class="section-heading"><h2>最近变更</h2><span class="pill">草稿 r${data.revision}</span></div>${!admin(data) ? empty('无查看权限','变更记录仅供管理员查看。') : recent.length ? `<div>${recent.map(item => `<p>${e(item.message)}<br><small>${e(item.at ? new Date(item.at).toLocaleString('zh-CN') : '')}</small></p>`).join('')}</div>` : empty('暂无记录','配置变更后将显示在这里。')}</div></section></div><p class="small-note">实例运行不等于代理已注册或公网可达。草稿变更需重新应用到实例。</p>
    ${admin(data) ? `<section class="panel"><div class="panel-content"><div class="section-heading"><h2>远程节点后台观测</h2><div class="actions">${button('node-observation-refresh','刷新观测')}${button('go-nodes','管理节点')}</div></div><p class="small-note">显示本次工作台进程的后台拉取结果；刷新只读取本机观测，不会主动检查远端。</p><div data-node-observations role="status">正在读取…</div></div></section>` : ''}
    ${trafficAllowed && instances.length ? `<section class="panel"><div class="panel-content"><div class="section-heading"><div><h2>实例流量</h2><p class="small-note">当前运行代次与近 90 天的已记录采样。</p></div><div class="actions">${button('traffic-refresh','刷新流量')}${button('traffic-export','导出历史 CSV')}</div></div><label>实例<select data-traffic-instance>${instances.map(item => `<option value="${e(item.id)}" ${item.id === selected ? 'selected' : ''}>${e(item.name)} · ${e(item.kind)}</option>`).join('')}</select></label><div data-traffic-output>正在读取…</div><div data-traffic-budget role="status">正在读取月度提醒…</div><div data-traffic-history></div></div></section>` : ''}`;
  },
  mount(root,data) {
   const picker = root.querySelector('[data-traffic-instance]');
   let serial = 0, history = null, budget = null;
   let nodeSerial = 0;
   const nodeEpoch = K.epoch();
   async function loadNodeObservations() {
    const output = root.querySelector('[data-node-observations]');
    if (!output || !admin(data)) return;
    const sequence = ++nodeSerial;
    const current = () => sequence === nodeSerial && nodeEpoch === K.epoch() && K.isCurrent('overview',root) && K.state()?.identity?.role === 'admin';
    output.textContent = '正在读取…';
    try {
     const result = await K.api('nodes');
     if (!current()) return;
     if (!result || !Array.isArray(result.nodes)) throw new Error('节点观测格式无效。');
     const nodes = result.nodes.filter(node => node?.monitorEnabled === true);
     if (!nodes.length) { output.textContent = '暂无启用后台检查的节点。'; return; }
     const labels = {available:'可用',pending:'待复查',unavailable:'未确认可用'};
     const time = value => typeof value === 'string' && Number.isFinite(Date.parse(value)) ? new Date(value).toLocaleString('zh-CN') : '—';
     output.innerHTML = `<div class="table-wrap"><table><thead><tr><th>节点</th><th>最近后台检查</th><th>最近成功</th></tr></thead><tbody>${nodes.map(node => {
      const status = node.monitorStatus;
      const state = status ? (labels[status.state] || '状态未知') : '等待首次检查';
      return `<tr><td>${e(node.name)}</td><td>${e(state)}${status?.checkedAt ? ' · '+e(time(status.checkedAt)) : ''}</td><td>${status?.lastSuccessAt ? e(time(status.lastSuccessAt)) : '本次运行尚无成功观测'}</td></tr>`;
     }).join('')}</tbody></table></div>`;
    } catch (error) {
     if (current()) output.textContent = '读取后台观测失败：'+error.message;
    }
   }
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
    if (!item) return; K.selectedInstance = id; history = null; budget = null;
    const output = root.querySelector('[data-traffic-output]'), historyOutput = root.querySelector('[data-traffic-history]'), budgetOutput = root.querySelector('[data-traffic-budget]');
    output.textContent = '正在读取…'; historyOutput.textContent = ''; budgetOutput.textContent = '正在读取月度提醒…';
    const query = '?id=' + encodeURIComponent(id), runId = runtimeFor(data,id).runId || '';
    const [current, detail, daily, reminder] = await Promise.allSettled([
     K.api((item.kind === 'frps' ? 'server-traffic' : 'traffic')+query),
     item.kind === 'frpc' && (admin(data) || data.identity?.viewConfig) ? K.api('client-traffic'+query) : Promise.resolve(null),
     K.api('traffic-history'+query),
     K.api('traffic-budget'+query)
    ]);
    if (!K.isCurrent('overview',root) || sequence !== serial || picker.value !== id) return;
    if ((runtimeFor(K.state(),id).runId || '') !== runId) { output.textContent = '实例运行代次已变化，请刷新流量。'; budgetOutput.textContent = '实例状态已变化，请刷新月度提醒。'; return; }
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
    if (reminder.status !== 'fulfilled') budgetOutput.textContent = '读取月度提醒失败：'+reminder.reason.message;
    else {
     const value = reminder.value;
     const decimal = value => typeof value === 'string' && /^(0|[1-9]\d*)$/.test(value);
     const valid = value && value.instanceId === id && value.kind === item.kind && Number.isSafeInteger(value.revision) &&
      typeof value.version === 'string' && /^\d{4}-\d{2}$/.test(value.month) && decimal(value.limitBytes) &&
      (value.recordedBytes === null || decimal(value.recordedBytes)) && ['disabled','unknown','not_reached','reached'].includes(value.status) &&
      typeof value.blockStart === 'boolean' && typeof value.historySaved === 'boolean' && typeof value.historyStorageError === 'boolean';
     if (!valid) budgetOutput.textContent = '月度提醒数据无效，请刷新后重试。';
     else {
      budget = value;
      const labels = {disabled:'未设置提醒值',unknown:'本月没有可用的已记录采样，提醒状态未知',not_reached:'已记录采样尚未达到提醒值',reached:'已记录采样已达到提醒值'};
      const storage = value.historyStorageError ? (value.blockStart ? '历史文件读取或写入失败，已启用的启动检查会阻止此实例启动；请检查存储或关闭检查。' : '历史文件读取或写入失败；若启用启动检查，将阻止此实例启动。') : value.historySaved ? '' : '部分采样尚未写入磁盘，异常退出可能丢失。';
      const guard = value.blockStart ? (value.historyStorageError ? '已启用启动检查，历史存储异常。' : value.status === 'reached' ? '已启用启动检查，当前已记录采样达到提醒值，会阻止新启动或重启。' : value.status === 'unknown' ? '已启用启动检查；本月没有样本时允许启动，但无法证明实际流量未超额。' : '已启用启动检查，已记录采样尚未达到提醒值。') : '启动检查已关闭。';
      budgetOutput.innerHTML = `<div class="${value.status === 'reached' || value.historyStorageError ? 'notice' : ''}"><p><strong>${e(value.month)} UTC 月度提醒 · ${e(labels[value.status])}</strong></p><p>已记录接收与发送合计：${value.recordedBytes === null ? '未知' : bytes(value.recordedBytes)}${value.limitBytes === '0' ? '' : ` / 提醒值 ${bytes(value.limitBytes)}`}</p><p>${e(guard)}</p><p class="small-note">只比较本实例已记录的采样，可能遗漏流量；未达到提醒值不代表实际用量低于该值。检查不会主动停止运行中的实例或限流，不用于实时配额或计费。${e(storage)}</p>${admin(data) ? button('traffic-budget-edit','设置提醒和启动检查') : ''}</div>`;
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
   if (admin(data)) void loadNodeObservations();
   overviewCleanup = () => { ++serial; ++nodeSerial; clearTimeout(timer); };
   bindButtons(root, action => {
    if (action === 'go-instances') K.navigate('instances');
    if (action === 'go-rules') K.navigate('tunnels');
    if (action === 'go-nodes') K.navigate('nodes');
    if (action === 'node-observation-refresh') return loadNodeObservations();
    if (action === 'traffic-refresh') return loadTraffic();
    if (action === 'traffic-budget-edit') {
     const current = budget, id = picker?.value, instance = data.state.instances.find(item => item.id === id);
     if (!admin(data) || !current || !instance || current.instanceId !== id) throw new Error('提醒数据已变化，请刷新后重试。');
     const factors = {B:1n, MiB:1048576n, GiB:1073741824n}, stored = BigInt(current.limitBytes);
     const unit = stored > 0n && stored % factors.GiB === 0n ? 'GiB' : stored > 0n && stored % factors.MiB === 0n ? 'MiB' : 'B';
     const amount = (stored / factors[unit]).toString();
     K.openDialog({
      html:`<h2>设置月度流量提醒</h2><p class="muted">${e(instance.name)} · 仅比较 UTC 月度已记录的接收与发送采样。采样可能不完整，不能证明实际流量低于提醒值。</p><div class="form-grid"><label>提醒值（0 为关闭）<input name="amount" inputmode="numeric" pattern="[0-9]+" maxlength="20" required value="${e(amount)}"></label><label>单位<select name="unit"><option value="B" ${unit === 'B' ? 'selected' : ''}>字节</option><option value="MiB" ${unit === 'MiB' ? 'selected' : ''}>MiB</option><option value="GiB" ${unit === 'GiB' ? 'selected' : ''}>GiB</option></select><small>切换单位后请重新输入数值。</small></label><label class="form-check span-2"><input type="checkbox" name="blockStart" ${current.blockStart ? 'checked' : ''}>已记录采样达到提醒值后，阻止此实例新启动或重启</label></div><p class="small-note">默认关闭；不会主动停止运行中的实例。重启停旧期间若最后采样达到提醒值，替代实例可能不会启动。没有当月样本时允许启动；历史存储出错会阻止已启用检查的实例启动。提醒设置文件不可用时无法判断哪些实例启用，启动检查将跳过。</p>`,
      submitLabel:'保存提醒',
      async onSubmit(form) {
       const amount = form.elements.amount.value, unit = form.elements.unit.value;
       if (!/^[0-9]+$/.test(amount)) throw new Error('请输入非负整数。');
       if (!Object.hasOwn(factors,unit)) throw new Error('请选择有效的单位。');
       const limit = BigInt(amount) * factors[unit];
       if (limit > 18446744073709551615n) throw new Error('提醒值不能超过 18446744073709551615 字节。');
       const limitBytes = limit.toString();
       const blockStart = form.elements.blockStart.checked;
       if (limitBytes === '0' && blockStart) throw new Error('启用启动检查前，请设置大于 0 的提醒值。');
       if (!K.isCurrent('overview',root) || picker.value !== id || K.state()?.revision !== current.revision) throw new Error('实例或配置已变化，请关闭窗口并刷新。');
       try { await K.api('traffic-budget',{id,revision:current.revision,version:current.version,limitBytes,blockStart}); }
       catch (error) {
        if (!error.status || error.status >= 500) throw new Error('保存结果尚未确认，请关闭窗口并刷新核对；提醒设置文件不可用时启动检查会跳过。');
        throw error;
       }
       K.toast('月度提醒已保存。');
       if (K.isCurrent('overview',root) && picker.value === id) void loadTraffic();
      }
     });
    }
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

 let instancesCleanup = null;
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
    const mayAcknowledge = ['start','stop','restart'].some(action => can(data,item.id,action));
    return `<article class="card" data-local-instance="${e(item.id)}"><div class="section-heading"><div><span class="eyebrow">${e(item.kind)}</span><h2>${e(item.name)}</h2></div><div>${status(runtime)}</div></div>
     ${details ? `<p><span class="muted">地址</span> <code>${e(item.host)}:${e(item.port)}</code></p><p><span class="muted">规则</span> ${data.state.tunnels.filter(rule => rule.instanceId === item.id).length} 条 · <span class="muted">定时计划</span> ${!plan ? '未设置' : !plan.enabled ? '已关闭' : plan.paused ? '已暂停' : '已启用'}</p>` : ''}
     ${runtime.exit ? `<p class="notice">${e(runtime.exit)}</p>` : ''}${plan?.reason ? `<p class="small-note">${e(plan.reason)}</p>` : ''}
     <div class="notice" role="status" data-local-control-notice hidden><span data-local-control-text></span>${mayAcknowledge ? button('ack-local-control','刷新核对并解除',item.id) : ''}</div>
     <div class="actions">${controls}${logs ? button('logs','日志',item.id) : ''}${details ? button('config','配置',item.id) : ''}${admin(data) || data.identity?.viewTraffic ? button('traffic','流量',item.id) : ''}${admin(data) ? button('health','诊断',item.id) : ''}</div>
     ${admin(data) ? `<div class="actions">${button('edit-instance','编辑',item.id)}${button('schedule','定时计划',item.id)}${runtime.applied ? button('diff','配置差异',item.id) : ''}${button('duplicate-instance','复制',item.id)}${button('delete-instance','删除',item.id,'danger')}</div>` : ''}</article>`;
   }).join('');
   const warnings = (data.portWarnings || []).map(message => `<p>${e(message)}</p>`).join('');
   return head(restricted ? '授权实例' : '运行实例', restricted ? '仅显示当前账户有权查看的实例。' : '创建、校验和运行本机客户端或服务端。', admin(data) ? button('new-instance','新建实例','','primary') : '') + (warnings ? `<div class="notice">${warnings}</div>` : '') + (cards ? `<div class="cards">${cards}</div>` : empty('暂无实例',restricted ? '请联系管理员分配实例。' : '创建实例后可在此管理运行状态。','empty-welcome'));
  },
  mount(root,data) {
   const paint = () => { if (K.isCurrent('instances',root)) paintLocalControls(root); };
   const storage = event => { if (event.key === null || event.key.startsWith(localControlPrefix)) paint(); };
   window.addEventListener('storage',storage); window.addEventListener('kurumi-local-control-change',paint);
   instancesCleanup = () => { window.removeEventListener('storage',storage); window.removeEventListener('kurumi-local-control-change',paint); };
   paint();
   bindButtons(root, async (action,id) => {
   if (action === 'ack-local-control') return acknowledgeLocalControl(id);
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
   });
  },
  dispose() { instancesCleanup?.(); instancesCleanup = null; }
 });

 let rulesCleanup = null;
 K.register('tunnels', {
  render(data) {
   const rows = data.state.tunnels || [];
   if (!admin(data) && !data.identity?.viewConfig) return head('连接规则','当前账户没有配置详情查看权限。') + empty('无查看权限','请联系管理员授予配置查看权限。');
   const clients = (data.state.instances || []).filter(item => item.kind === 'frpc');
   const actions = admin(data) ? button('batch-create','批量创建') + button('new-tunnel','新建规则','','primary') : '';
   const cards = rows.map(rule => {
    const instance = data.state.instances.find(item => item.id === rule.instanceId), unavailable = Protocols.probeUnavailable(rule);
    return `<article class="card" data-rule="${e(rule.id)}" data-instance-id="${e(rule.instanceId)}" data-search="${e([rule.name,rule.type,rule.localHost,rule.localPort,rule.remotePort,instance?.name,Protocols.remote(rule)].join(' ').toLocaleLowerCase())}" data-type="${e(rule.type)}"><div class="section-heading"><div>${admin(data) ? `<label class="form-check"><input type="checkbox" data-select="${e(rule.id)}">选择</label>` : ''}<h2>${e(rule.name)}</h2><span class="muted">${e(instance?.name || '未知实例')} · ${e(rule.type)}</span></div><span class="pill">${rule.enabled ? '草稿启用' : '草稿停用'}</span></div>
     <p><span class="muted">本地目标 / 监听</span> <code>${e(Protocols.endpoint(rule))}</code> → <span class="muted">远端 / 配对</span> <code>${e(Protocols.remote(rule))}</code></p>
     <p>实例：<span data-rule-runtime="${e(rule.instanceId)}">${status(runtimeFor(data,rule.instanceId))}</span> · 代理注册：<span data-registration="${e(rule.id)}">等待读取</span></p><p class="small-note" data-probe="${e(rule.id)}"></p>
     ${admin(data) ? `<div class="actions">${button('probe',unavailable ? '不支持检测' : '检测目标',rule.id)}${button('edit-tunnel','编辑',rule.id)}${button('duplicate-tunnel','复制',rule.id)}${button('toggle',rule.enabled ? '草稿停用' : '草稿启用',rule.id)}${button('delete-tunnel','删除',rule.id,'danger')}</div>` : ''}</article>`;
   }).join('');
   return head('连接规则','筛选规则并查看草稿状态；修改不会自动应用到运行中的实例。',actions) +
    `<p class="notice" data-rules-stale hidden>草稿已变化，请刷新后重新选择并操作。</p><div class="toolbar"><label>搜索<input type="search" data-filter placeholder="规则、实例或地址"></label><label>协议<select data-protocol><option value="">全部协议</option>${Protocols.types.map(type => `<option value="${e(type)}">${e(type)}</option>`).join('')}</select></label><label>客户端<select data-rule-instance><option value="">全部客户端</option>${clients.map(item => `<option value="${e(item.id)}">${e(item.name)}</option>`).join('')}</select></label>${admin(data) && rows.length ? button('probe-visible','检测当前筛选') : ''}</div>
    ${admin(data) && rows.length ? `<div class="toolbar"><label class="form-check"><input type="checkbox" data-select-visible>选择当前筛选</label><span data-selection-count>已选 0 条（筛选外 0 条）</span><button type="button" data-selection-action="visible" disabled>仅保留当前筛选</button><button type="button" data-selection-action="clear" disabled>清空全部选择</button><div class="actions">${button('batch-copy','批量复制')}${button('batch-enable','批量启用')}${button('batch-disable','批量停用')}${button('batch-delete','批量删除','','danger')}${button('batch-export','导出 ZIP')}${button('batch-export-toml','导出 TOML')}</div></div>` : ''}
    <div class="cards" data-rule-list>${cards || empty('暂无规则',admin(data) ? '先创建 frpc 客户端，再添加规则。' : '当前授权实例没有可显示的规则。','empty-search')}</div><p class="small-note" data-filter-empty hidden>没有匹配的规则。</p>`;
  },
  mount(root,data) {
   if (!admin(data) && !data.identity?.viewConfig) return;
   const selected = new Set(), cards = [...root.querySelectorAll('[data-rule]')];
   const hiddenSelectionCount = () => cards.filter(card => card.hidden && selected.has(card.dataset.rule)).length;
   const refreshSelection = () => {
    const visible = cards.filter(card => !card.hidden), visibleSelected = visible.filter(card => selected.has(card.dataset.rule)).length;
    const hidden = hiddenSelectionCount(), label = root.querySelector('[data-selection-count]'), all = root.querySelector('[data-select-visible]');
    if (label) label.textContent = `已选 ${selected.size} 条（筛选外 ${hidden} 条）`;
    if (all) { all.disabled = !visible.length; all.checked = visible.length > 0 && visibleSelected === visible.length; all.indeterminate = visibleSelected > 0 && visibleSelected < visible.length; }
    const keep = root.querySelector('[data-selection-action="visible"]'), clear = root.querySelector('[data-selection-action="clear"]');
    if (keep) keep.disabled = hidden === 0;
    if (clear) clear.disabled = selected.size === 0;
   };
   const applyFilter = () => {
    const query = root.querySelector('[data-filter]').value.toLocaleLowerCase(), type = root.querySelector('[data-protocol]').value, instance = root.querySelector('[data-rule-instance]').value;
    cards.forEach(card => { card.hidden = !card.dataset.search.includes(query) || (!!type && card.dataset.type !== type) || (!!instance && card.dataset.instanceId !== instance); });
    root.querySelector('[data-filter-empty]').hidden = !cards.length || cards.some(card => !card.hidden); refreshSelection();
   };
   root.querySelector('[data-filter]').addEventListener('input',applyFilter);
   root.querySelector('[data-protocol]').addEventListener('change',applyFilter);
   root.querySelector('[data-rule-instance]').addEventListener('change',applyFilter);
   root.addEventListener('change',event => {
    if (event.target.matches('[data-select]')) { if (event.target.checked) selected.add(event.target.dataset.select); else selected.delete(event.target.dataset.select); refreshSelection(); }
    if (event.target.matches('[data-select-visible]')) { cards.filter(card => !card.hidden).forEach(card => { const input = card.querySelector('[data-select]'); if (!input) return; input.checked = event.target.checked; if (input.checked) selected.add(input.dataset.select); else selected.delete(input.dataset.select); }); refreshSelection(); }
   });
   root.addEventListener('click',event => {
    const control = event.target.closest('[data-selection-action]'); if (!control || control.disabled) return;
    if (control.dataset.selectionAction === 'clear') selected.clear();
    else if (control.dataset.selectionAction === 'visible') cards.filter(card => card.hidden).forEach(card => selected.delete(card.dataset.rule));
    cards.forEach(card => { const input = card.querySelector('[data-select]'); if (input) input.checked = selected.has(card.dataset.rule); });
    refreshSelection();
   });
   refreshSelection();
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
    if (action === 'batch-copy') { requireRevision(data); return window.KurumiBatchCopy.open(root,target,data.revision); }
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
    if (['batch-enable','batch-disable','batch-delete','batch-export','batch-export-toml'].includes(action)) {
     const ids = [...selected]; if (!ids.length) throw new Error('请先选择规则。');
     const hiddenCount = hiddenSelectionCount();
     const selectionNote = `\n总计 ${ids.length} 条，筛选外 ${hiddenCount} 条。${hiddenCount ? '筛选外规则也会一并处理。' : ''}`;
     if (action === 'batch-export' || action === 'batch-export-toml') {
      const toml = action === 'batch-export-toml', rules = ids.map(id => data.state.tunnels.find(rule => rule.id === id));
      if (rules.some(rule => !rule)) throw new Error('部分规则已不存在，请刷新后重新选择。');
      let clientId = '';
      if (toml) {
       const clients = new Set(rules.map(rule => rule.instanceId));
       if (clients.size !== 1) throw new Error('TOML 导出只能选择同一个 frpc 客户端的规则；跨客户端请使用 ZIP。');
       clientId = rules[0].instanceId;
       if (!/^[a-f0-9]{16}$/.test(clientId) || !data.state.instances.some(instance => instance.id === clientId && instance.kind === 'frpc')) throw new Error('所选规则的 frpc 客户端不可用，请刷新后重试。');
      }
      const disabledNote = rules.some(rule => !rule.enabled) ? '\n选中包含停用规则：导出文件使用 enabled = false，运行前请确认目标 frpc 支持该字段并核对规则状态。' : '';
      if (!confirm(`导出选中规则为 ${toml ? 'TOML' : 'ZIP'}？${selectionNote}\n文件不加密，包含明文认证令牌、配对密钥、HTTP 密码、SOCKS5 插件密码及自定义 HTTP 头值，请妥善保管。${disabledNote}`)) return;
      const payload = {revision:data.revision,ids,confirmed:true,...(toml ? {format:'toml'} : {})};
      const response = await fetch('/api/tunnels/export',{method:'POST',credentials:'same-origin',cache:'no-store',headers:{'Content-Type':'application/json'},body:JSON.stringify(payload)});
      if (!response.ok) { let message = '导出失败。'; try { message = (await response.json()).error || message; } catch (_) {} if (response.status === 401) void K.refresh().catch(() => {}); throw new Error(message); }
      const type = (response.headers.get('Content-Type') || '').split(';')[0].trim().toLowerCase();
      if (type !== (toml ? 'application/toml' : 'application/zip')) throw new Error('导出响应格式无效。');
      const blob = await response.blob(); if (!blob.size || blob.size > 20*1024*1024) throw new Error('导出文件无效或超过大小限制。');
      if (!K.isCurrent('tunnels',root) || K.state()?.revision !== data.revision) throw new Error('草稿已变化，请刷新后重新导出。');
      K.download(toml ? `frpc-${clientId}.toml` : 'kurumi-selected-rules.zip',blob,type); K.toast('导出完成，请妥善保管明文凭据。'); return;
     }
     const deleting = action === 'batch-delete', enabled = action === 'batch-enable';
     if (!confirm(`${deleting ? '删除' : enabled ? '启用' : '停用'}选中的规则草稿？${selectionNote}\n运行中的实例不会立即改变。`)) return;
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
