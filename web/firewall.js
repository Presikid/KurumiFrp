'use strict';

titles.firewall = '端口访问控制';
const firewallBaseRender = render;

render = function () {
 firewallBaseRender();
 if (!data || data.identity?.role !== 'admin' || page !== 'firewall' || $('#firewall-screen')) return;
 $('#content').innerHTML = header('端口访问控制', '为明确指定的 Linux TCP/UDP 端口手动应用来源地址过滤。') + `
  <section id="firewall-screen" class="panel"><div class="panel-content">
   <p class="small-note">规则作用于本机指定端口的新入站连接，也可能影响使用相同端口的其他服务。回环和已有连接不受此规则处理。重叠端口必须通过所有相关规则；允许列表没有列出的 IP 地址族也会被阻止。</p>
   <p class="small-note">此操作需要 Linux、nftables 和网络管理权限。工作台或系统重启后不会自动重新应用；其他防火墙工具也可能更改规则。请预留独立的服务器管理通道。</p>
   <p id="firewall-status" role="status">正在读取配置…</p>
   <p id="firewall-table" class="small-note"></p>
   <button type="button" id="firewall-reload">刷新配置</button>
   <form id="firewall-form"><fieldset id="firewall-controls" disabled>
    <label><input type="checkbox" name="enabled"> 启用端口来源过滤</label>
    <fieldset id="firewall-settings"><legend>规则</legend>
     <div id="firewall-rules"></div><button type="button" data-firewall-add>添加规则</button>
     <label class="form-check"><input type="checkbox" name="recordBlocked">记录近期被这些规则阻止的来源</label>
     <p class="small-note">记录包含来源地址、协议和目标端口，约一小时无新尝试后过期且数量有限；关闭或重新应用规则会清空记录。</p>
    </fieldset>
    <p class="small-note">地址按逗号或换行分隔。端口适用于 TCP 和 UDP；动态分配的端口需要明确列入范围。</p>
    <button type="submit" class="primary">保存并应用</button>
   </fieldset></form>
   <section id="firewall-records"><h3>近期拦截记录</h3><p class="small-note">记录并非完整审计日志，只反映此功能捕获的连接尝试；列表按地址排列，不代表时间顺序。</p><div id="firewall-seen" role="status">尚未读取。</div></section>
  </div></section>`;
 mountFirewall();
};

function mountFirewall() {
 const host = $('#firewall-screen'), form = $('#firewall-form'), controls = $('#firewall-controls');
 const status = $('#firewall-status'), table = $('#firewall-table'), reload = $('#firewall-reload');
 const rulesHost = $('#firewall-rules'), settings = $('#firewall-settings');
 const generation = authGeneration, navigation = navigationGeneration;
 let model = null, supported = false, busy = false, seenTimer = null, seenRequest = null;
 const current = () => host.isConnected && generation === authGeneration && navigation === navigationGeneration && page === 'firewall';
 const syncEnabled = () => {
  settings.disabled = !form.elements.enabled.checked;
 };
 const addRule = (rule = {}) => {
  if (rulesHost.children.length >= 16) { status.textContent = '最多添加 16 条规则。'; return; }
  const row = document.createElement('fieldset');
  row.className = 'firewall-rule';
  row.innerHTML = `<legend>来源规则</legend><label class="form-check"><input type="checkbox" data-rule-enabled>启用此规则</label><label>名称（可选）<input data-rule-name maxlength="64"></label>
   <label>过滤方式<select data-rule-mode><option value="allow">仅允许列表中的来源</option><option value="block">阻止列表中的来源</option></select></label>
   <label>TCP/UDP 目标端口<input data-rule-ports maxlength="1024" placeholder="7000, 6000-6010"></label>
   <label>来源 IP 或 CIDR 网段<textarea data-rule-sources maxlength="262144" rows="5" placeholder="192.0.2.1&#10;2001:db8::/32"></textarea></label>
   <label>按 AS 号添加网段<input data-rule-asn maxlength="12" placeholder="例如 AS3333"></label><button type="button" data-firewall-asn>查询并加入此规则</button>
   <p data-asn-status class="small-note" role="status">向 RIPEstat 查询近两周观察到的网段；结果可能不完整或过时，不会自动更新或应用。</p>
   <button type="button" data-firewall-remove>删除此规则</button>`;
  row.querySelector('[data-rule-name]').value = rule.name || '';
  row.querySelector('[data-rule-enabled]').checked = rule.enabled !== false;
  row.querySelector('[data-rule-mode]').value = rule.mode || 'allow';
  row.querySelector('[data-rule-ports]').value = rule.ports || '';
  row.querySelector('[data-rule-sources]').value = rule.sources || '';
  rulesHost.appendChild(row);
 };
 const readRules = () => [...rulesHost.querySelectorAll('.firewall-rule')].map(row=>({
  name:row.querySelector('[data-rule-name]').value, enabled:row.querySelector('[data-rule-enabled]').checked, mode:row.querySelector('[data-rule-mode]').value,
  ports:row.querySelector('[data-rule-ports]').value, sources:row.querySelector('[data-rule-sources]').value
 }));
 const refreshSeen = async () => {
  clearTimeout(seenTimer); seenTimer = null;
  if (!current() || !supported || !model?.enabled || !model.recordBlocked || seenRequest) return;
  if (document.hidden) { seenTimer = setTimeout(refreshSeen, 5000); return; }
  const output = $('#firewall-seen'), controller = new AbortController();
  seenRequest = controller;
  const timeout = setTimeout(() => controller.abort(), 10000);
  try {
   const result = await api('firewall/seen', undefined, {signal:controller.signal});
   if (!current() || seenRequest !== controller) return;
   if (!result.enabled) output.textContent = '保存的配置未启用记录；实际规则仍需独立核对。';
   else {
    const records = Array.isArray(result.records) ? result.records : [];
    output.innerHTML = (result.partial ? '<p class="small-note">部分记录无法解析或超过显示上限。</p>' : '') + (records.length ? `<div class="table-wrap"><table><thead><tr><th>来源地址</th><th>协议</th><th>目标端口</th></tr></thead><tbody>${records.map(record=>`<tr><td>${esc(record.source)}</td><td>${esc(record.protocol)}</td><td>${esc(record.port)}</td></tr>`).join('')}</tbody></table></div>` : '<p>当前没有可显示的记录。</p>');
   }
  } catch (_) {
   if (current() && seenRequest === controller) output.textContent = '记录读取失败，实际规则状态未知。';
  } finally {
   clearTimeout(timeout);
   if (seenRequest === controller) {
    seenRequest = null;
    if (current() && model?.enabled && model.recordBlocked) seenTimer = setTimeout(refreshSeen, 5000);
   }
  }
 };
 const syncSeen = () => {
  clearTimeout(seenTimer); seenTimer = null;
  seenRequest?.abort(); seenRequest = null;
  if (supported && model?.enabled && model.recordBlocked) void refreshSeen();
  else $('#firewall-seen').textContent = supported ? '保存的配置未启用记录；实际规则仍需独立核对。' : '当前系统无法读取内核记录。';
 };
 const load = async () => {
  if (busy) return;
  clearTimeout(seenTimer); seenRequest?.abort(); seenRequest = null;
  busy = true; reload.disabled = true; controls.disabled = true;
  status.textContent = '正在读取配置…';
  const controller = new AbortController(), timeout = setTimeout(() => controller.abort(), 10000);
  try {
   const result = await api('firewall', undefined, {signal:controller.signal});
   if (!current()) return;
   model = result.config;
   supported = !!result.supported;
   form.elements.enabled.checked = !!model.enabled;
   form.elements.recordBlocked.checked = !!model.recordBlocked;
   rulesHost.replaceChildren();
   const rules = Array.isArray(model.rules) && model.rules.length ? model.rules : model.enabled ? [{mode:model.mode, ports:model.ports, sources:model.sources}] : [{}];
   rules.forEach(addRule);
   table.textContent = model.tableId ? '本工作台管理的表：kurumi_' + model.tableId : '尚未创建本工作台的规则表。';
   status.textContent = !result.supported ? '当前系统不支持此功能；已有内核规则状态未知。'
    : result.lastResult === 'applied' ? '本进程最近一次应用命令返回成功；当前规则仍可能被系统或其他工具更改。'
    : '内核规则状态未知；保存的配置不代表当前已经生效。';
   controls.disabled = !result.supported;
   syncEnabled();
   syncSeen();
  } catch (err) {
   if (!current()) return;
   model = null;
   status.textContent = '读取失败：' + (err.name === 'AbortError' ? '请求超时' : err.message);
   $('#firewall-seen').textContent = '记录状态未知。';
  } finally {
   clearTimeout(timeout); busy = false;
   if (current()) reload.disabled = false;
  }
 };
 form.elements.enabled.addEventListener('change', syncEnabled);
 reload.addEventListener('click', load);
 form.addEventListener('click', async event => {
  if (event.target.closest('[data-firewall-add]')) { addRule(); return; }
  const row = event.target.closest('.firewall-rule');
  if (!row) return;
  if (event.target.closest('[data-firewall-remove]')) {
   if (rulesHost.children.length > 1) row.remove();
   else row.querySelector('[data-asn-status]').textContent = '至少保留一条规则；如需停用全部规则，请取消启用。';
   return;
  }
  if (!event.target.closest('[data-firewall-asn]') || busy || !model || !supported || !form.elements.enabled.checked) return;
  const notice = row.querySelector('[data-asn-status]'), sources = row.querySelector('[data-rule-sources]');
  busy = true; controls.disabled = true; reload.disabled = true;
  notice.textContent = '正在向 RIPEstat 查询…';
  const controller = new AbortController(), timeout = setTimeout(() => controller.abort(), 25000);
  try {
   const result = await api('firewall/asn', {asn:row.querySelector('[data-rule-asn]').value}, {signal:controller.signal});
   if (!current()) return;
   if (!Array.isArray(result.prefixes) || !result.prefixes.length) throw new Error('未获得网段');
   const existing = sources.value.split(/[,\r\n]+/).map(value=>value.trim()).filter(Boolean);
   const merged = [...new Set([...existing, ...result.prefixes])];
   const text = merged.join('\n');
   const others = [...rulesHost.querySelectorAll('[data-rule-sources]')].filter(input=>input !== sources).map(input=>input.value);
   const totalCount = merged.length + others.reduce((count,value)=>count + value.split(/[,\r\n]+/).filter(part=>part.trim()).length,0);
   const totalBytes = new TextEncoder().encode(text + others.join('')).length;
   if (totalCount > 4096 || totalBytes > 262144) throw new Error('所有规则合并后的来源列表超过上限');
   sources.value = text;
   notice.textContent = `${result.asn} 返回 ${result.prefixes.length} 个网段，已加入草稿；请核对列表，再决定是否保存并应用。数据未自动更新。`;
  } catch (err) {
   if (current()) notice.textContent = '查询失败，来源列表未更改：' + (err.name === 'AbortError' ? '请求超时' : err.message);
  } finally {
   clearTimeout(timeout); busy = false;
   if (current()) { controls.disabled = false; reload.disabled = false; syncEnabled(); }
  }
 });
 form.addEventListener('submit', async event => {
  event.preventDefault();
  if (busy || !model) return;
  const enabled = form.elements.enabled.checked;
  if (!confirm(enabled ? '将修改本机指定端口的新入站连接过滤，可能影响其他服务。确定保存并应用？' : '将移除本工作台管理的规则表。确定保存并应用？')) return;
  const payload = {revision:model.revision, enabled, recordBlocked:form.elements.recordBlocked.checked, rules:enabled ? readRules() : []};
  clearTimeout(seenTimer); seenRequest?.abort(); seenRequest = null;
  $('#firewall-seen').textContent = '应用期间暂停读取记录。';
  busy = true; controls.disabled = true; reload.disabled = true;
  status.textContent = '正在保存并应用；断线或关闭页面不能保证取消。';
  const controller = new AbortController(), timeout = setTimeout(() => controller.abort(), 30000);
  try {
   const result = await api('firewall', payload, {signal:controller.signal});
   if (!current()) return;
   model = result.config;
   table.textContent = '本工作台管理的表：kurumi_' + model.tableId;
   if (!enabled) {
    rulesHost.replaceChildren();
    (model.rules?.length ? model.rules : [{}]).forEach(addRule);
    form.elements.recordBlocked.checked = !!model.recordBlocked;
   }
   status.textContent = enabled ? '应用命令返回成功；当前规则仍可能被系统或其他工具更改。' : '移除命令返回成功；上次保存的规则已保留，未应用的编辑未保存。';
   controls.disabled = false;
   syncEnabled();
   syncSeen();
  } catch (err) {
   if (!current()) return;
   if (err.status === 400) {
    status.textContent = '未应用：' + err.message;
    controls.disabled = false;
    syncEnabled();
    syncSeen();
   } else {
    model = null;
    clearTimeout(seenTimer); seenRequest?.abort(); seenRequest = null;
    $('#firewall-seen').textContent = '规则或记录结果未确认。';
    status.textContent = '配置或规则结果未确认：' + (err.name === 'AbortError' ? '等待超时' : err.message) + '。请刷新配置并独立核对内核规则后，再决定是否重新应用。';
   }
  } finally {
   clearTimeout(timeout); busy = false;
   if (current()) reload.disabled = false;
  }
 });
 load();
}
