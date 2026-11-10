'use strict';

titles.firewall = '端口访问控制';
const firewallBaseRender = render;

render = function () {
 firewallBaseRender();
 if (!data || data.identity?.role !== 'admin' || page !== 'firewall' || $('#firewall-screen')) return;
 $('#content').innerHTML = header('端口访问控制', '为明确指定的 Linux TCP/UDP 端口手动应用来源地址过滤。') + `
  <section id="firewall-screen" class="panel"><div class="panel-content">
   <p class="small-note">规则作用于本机指定端口的新入站连接，也可能影响使用相同端口的其他服务。回环和已有连接不受此规则处理。允许列表没有列出的 IP 地址族也会被阻止。</p>
   <p class="small-note">此操作需要 Linux、nftables 和网络管理权限。工作台或系统重启后不会自动重新应用；其他防火墙工具也可能更改规则。请预留独立的服务器管理通道。</p>
   <p id="firewall-status" role="status">正在读取配置…</p>
   <p id="firewall-table" class="small-note"></p>
   <button type="button" id="firewall-reload">刷新配置</button>
   <form id="firewall-form"><fieldset id="firewall-controls" disabled>
    <label><input type="checkbox" name="enabled"> 启用端口来源过滤</label>
    <div id="firewall-settings">
     <label>过滤方式<select name="mode"><option value="allow">仅允许列表中的来源</option><option value="block">阻止列表中的来源</option></select></label>
     <label>TCP/UDP 目标端口<input name="ports" maxlength="1024" placeholder="7000, 6000-6010"></label>
     <label>来源 IP 或 CIDR 网段<textarea name="sources" maxlength="8192" rows="5" placeholder="192.0.2.1&#10;2001:db8::/32"></textarea></label>
    </div>
    <p class="small-note">地址按逗号或换行分隔。端口适用于 TCP 和 UDP；动态分配的端口需要明确列入范围。</p>
    <button type="submit" class="primary">保存并应用</button>
   </fieldset></form>
  </div></section>`;
 mountFirewall();
};

function mountFirewall() {
 const host = $('#firewall-screen'), form = $('#firewall-form'), controls = $('#firewall-controls');
 const status = $('#firewall-status'), table = $('#firewall-table'), reload = $('#firewall-reload');
 const generation = authGeneration, navigation = navigationGeneration;
 let model = null, busy = false;
 const current = () => host.isConnected && generation === authGeneration && navigation === navigationGeneration && page === 'firewall';
 const syncEnabled = () => {
  for (const input of $('#firewall-settings').querySelectorAll('input,select,textarea')) input.disabled = !form.elements.enabled.checked;
 };
 const load = async () => {
  if (busy) return;
  busy = true; reload.disabled = true; controls.disabled = true;
  status.textContent = '正在读取配置…';
  const controller = new AbortController(), timeout = setTimeout(() => controller.abort(), 10000);
  try {
   const result = await api('firewall', undefined, {signal:controller.signal});
   if (!current()) return;
   model = result.config;
   form.elements.enabled.checked = !!model.enabled;
   form.elements.mode.value = model.mode || 'allow';
   form.elements.ports.value = model.ports || '';
   form.elements.sources.value = model.sources || '';
   table.textContent = model.tableId ? '本工作台管理的表：kurumi_' + model.tableId : '尚未创建本工作台的规则表。';
   status.textContent = !result.supported ? '当前系统不支持此功能；已有内核规则状态未知。'
    : result.lastResult === 'applied' ? '本进程最近一次应用命令返回成功；当前规则仍可能被系统或其他工具更改。'
    : '内核规则状态未知；保存的配置不代表当前已经生效。';
   controls.disabled = !result.supported;
   syncEnabled();
  } catch (err) {
   if (!current()) return;
   model = null;
   status.textContent = '读取失败：' + (err.name === 'AbortError' ? '请求超时' : err.message);
  } finally {
   clearTimeout(timeout); busy = false;
   if (current()) reload.disabled = false;
  }
 };
 form.elements.enabled.addEventListener('change', syncEnabled);
 reload.addEventListener('click', load);
 form.addEventListener('submit', async event => {
  event.preventDefault();
  if (busy || !model) return;
  const enabled = form.elements.enabled.checked;
  if (!confirm(enabled ? '将修改本机指定端口的新入站连接过滤，可能影响其他服务。确定保存并应用？' : '将移除本工作台管理的规则表。确定保存并应用？')) return;
  const payload = {revision:model.revision, enabled, mode:form.elements.mode.value, ports:form.elements.ports.value, sources:form.elements.sources.value};
  busy = true; controls.disabled = true; reload.disabled = true;
  status.textContent = '正在保存并应用；断线或关闭页面不能保证取消。';
  const controller = new AbortController(), timeout = setTimeout(() => controller.abort(), 30000);
  try {
   const result = await api('firewall', payload, {signal:controller.signal});
   if (!current()) return;
   model = result.config;
   table.textContent = '本工作台管理的表：kurumi_' + model.tableId;
   status.textContent = '应用命令返回成功；当前规则仍可能被系统或其他工具更改。';
   controls.disabled = false;
   syncEnabled();
  } catch (err) {
   if (!current()) return;
   if (err.status === 400) {
    status.textContent = '未应用：' + err.message;
    controls.disabled = false;
    syncEnabled();
   } else {
    model = null;
    status.textContent = '配置或规则结果未确认：' + (err.name === 'AbortError' ? '等待超时' : err.message) + '。请刷新配置并独立核对内核规则后，再决定是否重新应用。';
   }
  } finally {
   clearTimeout(timeout); busy = false;
   if (current()) reload.disabled = false;
  }
 });
 load();
}
