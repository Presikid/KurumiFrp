'use strict';

// The rules screen owns only its transient selection. The application remains
// responsible for fetching state, executing actions, and observing registration.
const RulesPage = (() => {
 let selected = new Set(), revision = null, busy = false, needsSync = false;

 function clear() { selected.clear(); revision = null; }
 function reset() { clear(); busy = false; needsSync = false; }
 function syncRevision(next) {
  if (revision !== null && revision !== next) { clear(); return true; }
  return false;
 }
 function blocked(ctx) {
  return busy || needsSync || ctx.probingAll || Object.values(ctx.probing).some(Boolean);
 }
 function filtered(ctx) {
  return ctx.data.state.tunnels.filter(t => {
   const instance = ctx.data.state.instances.find(i => i.id === t.instanceId);
   return Protocols.matches(t, instance, ctx.filter, ctx.protocol);
  });
 }
 function runtimeText(runtime) {
  return ({running:'进程运行中',stopped:'实例未启动',exited:'进程已退出','autostart-failed':'自动启动失败'})[runtime.state] || '进程状态未知';
 }
 function card(t, ctx) {
  const {esc, data} = ctx;
  const instance = data.state.instances.find(i => i.id === t.instanceId);
  const runtime = ctx.stateOf(t.instanceId);
  const disabled = blocked(ctx) ? 'disabled' : '';
  const unavailable = Protocols.probeUnavailable(t);
  const visitor = Protocols.isVisitor(t.type);
  const probeDisabled = unavailable || ctx.probing[t.id] || ctx.probingAll || busy;
  return `<article class="rule-card${selected.has(t.id) ? ' is-selected' : ''}">
   <div class="rule-card-heading"><div class="rule-identity">
    <input class="rule-checkbox" type="checkbox" data-rule-select="${esc(t.id)}" aria-label="选择规则 ${esc(t.name)}，${esc(instance?.name || '未知实例')}" ${selected.has(t.id) ? 'checked' : ''} ${disabled}>
    <div><h3>${esc(t.name)}</h3><p>${esc(instance?.name || '未知实例')}</p></div>
   </div><span class="type-icon">${esc(t.type.toUpperCase())}</span></div>
   <div class="rule-route"><div><span>${visitor ? '本地监听' : '草稿本地目标'}</span><code>${esc(Protocols.endpoint(t))}</code></div><span class="rule-route-arrow" aria-hidden="true">→</span><div><span>${visitor ? '配对服务' : '远端 / 配对'}</span><code>${esc(Protocols.remote(t))}</code></div></div>
   <div class="rule-observations"><div><span>草稿意图</span><strong class="pill ${t.enabled ? 'accent' : ''}">${t.enabled ? '期望启用' : '期望停用'}</strong></div>
    <div><span>实例进程</span><strong data-rule-runtime="${esc(t.instanceId)}">${esc(runtimeText(runtime))}</strong><small data-rule-pending="${esc(t.instanceId)}" ${runtime.pending ? '' : 'hidden'}>实例配置待应用</small></div>
    <div><span>已应用代理状态</span><strong class="rule-registration" data-registration="${esc(t.id)}">${esc(ctx.registrationText(ctx.registrationFor(t)))}</strong></div>
    <div class="rule-probe"><span>${visitor ? '草稿监听检测' : '草稿目标检测'}</span>${ctx.probeCell(t)}</div></div>
   <div class="actions rule-actions"><button data-action="probe" data-id="${esc(t.id)}" ${probeDisabled || needsSync ? 'disabled' : ''} ${unavailable ? `title="${esc(unavailable)}"` : ''}>${unavailable ? '不支持检测' : ctx.probing[t.id] ? '检测中…' : visitor ? '检测监听' : '检测目标'}</button><button data-action="edit-tunnel" data-id="${esc(t.id)}" ${disabled}>编辑</button><button data-action="duplicate-tunnel" data-id="${esc(t.id)}" ${disabled}>复制</button><button data-action="toggle" data-id="${esc(t.id)}" ${disabled}>${t.enabled ? '草稿停用' : '草稿启用'}</button><button class="danger" data-action="delete-tunnel" data-id="${esc(t.id)}" ${disabled}>删除</button></div>
  </article>`;
 }
 function list(ctx) {
  const rows = filtered(ctx), all = ctx.data.state.tunnels.length;
  const disabled = blocked(ctx) ? 'disabled' : '';
  const checked = rows.length > 0 && rows.every(t => selected.has(t.id));
  const message = all ? '没有符合筛选条件的规则。' : '先添加 frpc 实例，再创建连接规则。';
  return `<div class="rules-list-head"><div class="rules-list-title"><div><h2>规则列表</h2><p>显示 ${rows.length} / ${all} 条</p></div><span class="rules-revision">草稿 r${ctx.data.revision}</span></div>
   <div class="rules-batch"><label class="rules-select-all"><input class="rule-checkbox" type="checkbox" data-rule-select-all ${checked ? 'checked' : ''} ${!rows.length || disabled ? 'disabled' : ''}>选择当前筛选</label><span class="rules-selection-count" role="status">已选 ${selected.size} 条</span><div class="rules-batch-actions"><button data-action="batch-enable" ${!selected.size || disabled ? 'disabled' : ''}>批量草稿启用</button><button data-action="batch-disable" ${!selected.size || disabled ? 'disabled' : ''}>批量草稿停用</button><button class="quiet" data-action="clear-rule-selection" ${!selected.size || disabled ? 'disabled' : ''}>清空选择</button></div></div>
   <p class="rules-selection-note">只选择当前筛选中的规则；筛选或草稿版本变化时会清空选择。</p></div>
   ${rows.length ? `<div class="rules-cards">${rows.map(t => card(t, ctx)).join('')}</div>` : ctx.empty(all ? '没有匹配的规则' : '还没有连接规则', message, '', all ? 'search' : '')}`;
 }
 function render(ctx) {
  const {data, esc} = ctx;
  const stats = summary(ctx);
  const disabled = blocked(ctx) ? 'disabled' : '';
  return `${needsSync ? `<div class="notice" role="alert">尚未确认最新草稿，请先刷新后再操作规则。 <button data-action="refresh-rules" ${busy ? 'disabled' : ''}>刷新草稿</button></div>` : ''}<section class="rules-intro"><div><p class="eyebrow">CONNECTION RULES / 规则工作台</p><h2>让每条连接各就其位。</h2><p>启停只保存草稿；运行中的实例需在实例页重启应用。代理注册、进程状态与本地目标检测分别显示。</p></div><div class="rules-intro-art" aria-hidden="true"></div></section>
   <section class="rules-summary" aria-label="规则摘要"><div><strong data-rules-stat="total">${stats.total}</strong><span>草稿规则</span></div><div><strong data-rules-stat="enabled">${stats.enabled}</strong><span>草稿期望启用</span></div><div><strong data-rules-stat="pending">${stats.pending}</strong><span>实例配置待应用</span></div></section>
   <div class="rules-filters"><label><span>搜索</span><input id="search" type="search" placeholder="规则、地址或实例" aria-label="搜索规则" value="${esc(ctx.filter)}" ${disabled}></label><label><span>协议</span><select id="protocol" aria-label="按协议筛选" ${disabled}><option value="">所有协议</option>${Protocols.types.map(t => `<option value="${esc(t)}" ${ctx.protocol === t ? 'selected' : ''}>${esc(t)}</option>`).join('')}</select></label><button data-action="probe-visible" ${disabled}>${ctx.probingAll ? '批量检测 ' + (ctx.probeBatchProgress?.done || 0) + '/' + (ctx.probeBatchProgress?.total || 0) : '检测当前筛选'}</button></div>
   <section class="panel rules-panel" id="tunnel-list" data-rule-revision="${data.revision}">${list(ctx)}</section>`;
 }
 function summary(ctx) {
  const tunnels = ctx.data.state.tunnels;
  return {total:tunnels.length, enabled:tunnels.filter(t => t.enabled).length, pending:new Set(tunnels.filter(t => ctx.stateOf(t.instanceId).pending).map(t => t.instanceId)).size};
 }
 function paintSummary(ctx) {
  for (const [key, value] of Object.entries(summary(ctx))) {
   const element = document.querySelector(`[data-rules-stat="${key}"]`);
   if (element && element.textContent !== String(value)) element.textContent = value;
  }
 }
 function paintRuntime(ctx) {
  document.querySelectorAll('[data-rule-runtime]').forEach(element => {
   const next = runtimeText(ctx.stateOf(element.dataset.ruleRuntime));
   if (element.textContent !== next) element.textContent = next;
  });
  document.querySelectorAll('[data-rule-pending]').forEach(element => {
   const hidden = !ctx.stateOf(element.dataset.rulePending).pending;
   if (element.hidden !== hidden) element.hidden = hidden;
  });
  paintSummary(ctx);
 }
 function paint(ctx) {
  const root = document.querySelector('#tunnel-list');
  if (!root) return;
  const rows = filtered(ctx), count = rows.filter(t => selected.has(t.id)).length;
  root.querySelectorAll('[data-rule-select]').forEach(input => {
   input.checked = selected.has(input.dataset.ruleSelect);
   input.closest('.rule-card')?.classList.toggle('is-selected', input.checked);
  });
  const all = root.querySelector('[data-rule-select-all]');
  if (all) { all.checked = !!rows.length && count === rows.length; all.indeterminate = count > 0 && count < rows.length; }
  const label = root.querySelector('.rules-selection-count');
  if (label) label.textContent = `已选 ${selected.size} 条`;
  root.querySelectorAll('[data-action="batch-enable"], [data-action="batch-disable"], [data-action="clear-rule-selection"]').forEach(button => { button.disabled = !selected.size || blocked(ctx); });
 }
 function change(target, ctx) {
  if (blocked(ctx)) return false;
  const rows = filtered(ctx);
  if (target.matches('[data-rule-select-all]')) {
   if (target.checked) rows.forEach(t => selected.add(t.id));
   else rows.forEach(t => selected.delete(t.id));
  } else if (target.matches('[data-rule-select]')) {
   const id = target.dataset.ruleSelect;
   if (!rows.some(t => t.id === id)) return false;
   if (target.checked) selected.add(id); else selected.delete(id);
  } else return false;
  revision = selected.size ? ctx.data.revision : null;
  paint(ctx);
  return true;
 }
 function plan(ctx, enabled) {
  if (blocked(ctx) || revision !== ctx.data.revision) return null;
  const rows = filtered(ctx).filter(t => selected.has(t.id));
  if (!rows.length || rows.length !== selected.size) return null;
  return {revision, ids:rows.map(t => t.id), enabled, changed:rows.filter(t => t.enabled !== enabled).length};
 }
 return {clear, reset, syncRevision, filtered, render, list, paint, paintRuntime, change, plan, setBusy:value => { busy = value; }, setNeedsSync:value => { needsSync = value; }, needsSync:() => needsSync, isUnavailable:() => busy || needsSync};
})();
