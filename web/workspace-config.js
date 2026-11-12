'use strict';

(() => {
 const K = window.Kurumi, e = K.esc;
 const admin = data => data.identity?.role === 'admin';
 const head = (title,text) => `<header class="page-head"><div><h1>${e(title)}</h1><p>${e(text)}</p></div></header>`;
 const button = (action,label,extra = '') => `<button type="button" data-action="${action}" ${extra}>${e(label)}</button>`;
 const validRevision = data => { if (!K.state() || K.state().revision !== data.revision) throw new Error('草稿已变化，请刷新后重试。'); };
 const instancePicker = (items,selected) => `<select data-instance ${items.length ? '' : 'disabled'}>${items.length ? items.map(item => `<option value="${e(item.id)}" ${selected === item.id ? 'selected' : ''}>${e(item.name)} · ${e(item.kind)}</option>`).join('') : '<option value="">暂无可用实例</option>'}</select>`;
 function chosen(items) { return items.some(item => item.id === K.selectedInstance) ? K.selectedInstance : items[0]?.id || ''; }
 function selectedScope(root) {
  const scope = root.querySelector('[data-scope]').value;
  const snapshot = Number(root.querySelector('[data-snapshot]').value || 0);
  if ((scope === 'snapshot' && !snapshot) || (scope === 'snapshots' && !root.querySelector('[data-snapshot]').options.length)) throw new Error('没有可导出的快照。');
  return {scope,snapshot};
 }
 function withTimeout(path,body) {
  const controller = new AbortController(), timer = setTimeout(() => controller.abort(),120000);
  return K.api(path,body,{signal:controller.signal}).catch(error => { if (error.name === 'AbortError') throw new Error('等待结果超时，服务端可能仍在处理。'); throw error; }).finally(() => clearTimeout(timer));
 }
 async function readJSON(file,limit) {
  if (!file) throw new Error('请选择文件。');
  if (file.size > limit) throw new Error('文件超过大小限制。');
  try { return JSON.parse(await file.text()); } catch (_) { throw new Error('文件不是有效 JSON。'); }
 }
 function backupDialog(mode,data,root) {
  validRevision(data);
  const blocked = K.transfer.blocked(); if (mode === 'import' && blocked) throw new Error(blocked);
  const scope = mode === 'export' ? selectedScope(root) : null;
  K.openDialog({
   html: mode === 'export'
    ? '<h2>导出加密备份</h2><p class="muted">备份密码须为 12–1024 个 UTF-8 字节，不含控制字符；密码不会保存，请将密码和文件分开保管。</p><div class="form-grid"><label>备份密码<input name="password" type="password" maxlength="1024" required autocomplete="new-password"></label><label>确认密码<input name="confirm" type="password" maxlength="1024" required autocomplete="new-password"></label></div>'
    : '<h2>恢复加密备份</h2><p class="muted">请先停止全部托管实例。恢复会替换配置、定时计划、历史快照和草稿事件记录，并撤销现有非管理员账户的本机实例授权。导入的计划将暂停；节点授权和通知设置保留在本机。</p><div class="form-grid"><label class="span-2">备份文件<input type="file" name="file" accept=".json,application/json" required></label><label class="span-2">备份密码<input name="password" type="password" required maxlength="1024" autocomplete="current-password"></label><label class="form-check span-2"><input type="checkbox" name="confirmRestore" required>确认恢复备份并接受上述影响</label></div>',
   submitLabel:mode === 'export' ? '导出加密备份' : '验证并恢复',
   async onSubmit(form) {
    validRevision(data);
    const password = form.elements.password.value;
    if (mode === 'export') {
     const length = new TextEncoder().encode(password).length;
     if (length < 12 || length > 1024 || /[\x00-\x1f\x7f]/.test(password)) throw new Error('备份密码须为 12–1024 个 UTF-8 字节，且不能含控制字符。');
     if (password !== form.elements.confirm.value) throw new Error('两次输入的密码不一致。');
     const controller = new AbortController(), deadline = setTimeout(() => controller.abort(),120000);
     let result;
     try { result = await K.api('backup/export',{revision:data.revision,password,scope:scope.scope,snapshot:scope.snapshot},{signal:controller.signal}); }
     catch (error) {
      if (controller.signal.aborted) throw new Error('加密备份导出结果尚未确认，文件未下载；请核对后再决定是否重试。');
      throw error;
     } finally { clearTimeout(deadline); }
     if (!K.isCurrent('config',root) || K.state()?.revision !== data.revision) throw new Error('页面或草稿已变化，请重新导出。');
     K.download(`kurumi-${scope.scope}-encrypted.json`,JSON.stringify(result,null,2)); K.toast('加密备份已导出，请妥善保管密码。'); return;
    }
    const backup = await readJSON(form.elements.file.files[0],180*1024*1024);
    if (!K.isCurrent('config',root)) throw new Error('页面已变化，请重新操作。'); validRevision(data);
    const operation = K.transfer.begin('backup');
    try { await withTimeout('backup/import',{revision:data.revision,password,backup}); K.transfer.finish(operation,'complete'); }
    catch (error) {
     if (error.status === 400 || error.status === 409) { K.transfer.finish(operation,'rejected'); throw error; }
     K.transfer.finish(operation,'uncertain',error.message); throw new Error('恢复结果尚未确认，请核对提示和当前配置。');
    }
   }
  });
 }
 function importTOML(data,root) {
  const blocked = K.transfer.blocked(); if (blocked) throw new Error(blocked);
  validRevision(data);
  let preview = null, formVersion = 0;
  const invalidate = form => { preview = null; ++formVersion; form.querySelector('[data-toml-submit]').disabled = true; form.querySelector('[data-toml-result]').textContent = '内容已变化，请重新预览。'; };
  K.openDialog({
   html:'<h2>导入 frp TOML</h2><p class="muted">预览后追加为新实例草稿，不会替换配置或启动实例。</p><div class="form-grid"><label class="span-2">TOML 文件（UTF-8，最多 256 KiB）<input name="file" type="file" accept=".toml,text/plain" required></label><label>类型<select name="kind"><option value="frpc">frpc 客户端</option><option value="frps">frps 服务端</option></select></label><label>实例名称<input name="name" value="导入的 frp 实例" maxlength="80"></label></div><p class="small-note">不受支持或无法保留的设置会阻止导入；预览不会显示认证内容。</p><button type="button" data-toml-preview>预览</button><pre data-toml-result class="code-block" role="status">选择文件后预览。</pre><span data-toml-submit hidden></span>',
   submitLabel:'导入草稿',
   onReady(form) { const submit = form.querySelector('[type="submit"]'); submit.dataset.tomlSubmit = ''; submit.disabled = true; form.querySelector('span[data-toml-submit]').remove(); },
   onInput:(_,form) => invalidate(form), onChange:(_,form) => invalidate(form),
   async onClick(event,form) {
    const trigger = event.target.closest('[data-toml-preview]'); if (!trigger) return;
    const file = form.elements.file.files[0];
    if (!file) throw new Error('请选择 TOML 文件。');
    if (file.size > 256*1024) throw new Error('TOML 文件超过 256 KiB。');
    preview = null; form.querySelector('[data-toml-submit]').disabled = true;
    const version = ++formVersion, output = form.querySelector('[data-toml-result]'); trigger.disabled = true;
    try {
     let content;
     try { content = new TextDecoder('utf-8',{fatal:true}).decode(await file.arrayBuffer()); } catch (_) { throw new Error('TOML 文件必须是 UTF-8。'); }
     if (formVersion !== version || !output.isConnected || !document.querySelector('#dialog').open || !K.isCurrent('config',root)) return;
     validRevision(data);
     const payload = {revision:data.revision,kind:form.elements.kind.value,name:form.elements.name.value.trim(),content};
     const result = await K.api('import/toml/preview',payload);
     if (formVersion !== version || !output.isConnected || !document.querySelector('#dialog').open || !K.isCurrent('config',root)) return;
     validRevision(data);
     const instance = result.instance || {}, lines = [];
     if (instance.kind) {
      lines.push(`实例：${instance.name} · ${instance.kind}`, `地址：${instance.host}:${instance.port}`, `规则：${(result.tunnels || []).length} 条`);
      for (const rule of result.tunnels || []) lines.push(`${rule.name} · ${rule.type} · ${rule.enabled ? '启用' : '停用'} · ${rule.localHost || rule.unixSocketPath || ''}:${rule.localPort || ''}`);
     }
     if (result.issues?.length) lines.push('', '以下问题阻止导入：', ...result.issues.map(issue => `${issue.path}：${issue.message}`));
     if (result.canImport) { preview = {...payload,previewHash:result.previewHash}; form.querySelector('[data-toml-submit]').disabled = false; lines.push('', '可作为新草稿导入。'); }
     output.textContent = lines.join('\n') || '无法预览此文件。';
    } finally { if (trigger.isConnected) trigger.disabled = false; }
   },
   async onSubmit(form) {
    if (!preview) throw new Error('请先重新预览配置。');
    if (!K.isCurrent('config',root)) throw new Error('页面已变化，请重新操作。'); validRevision(data);
    const operation = K.transfer.begin('import');
    try { const result = await withTimeout('import/toml',preview); K.transfer.finish(operation,'complete'); K.toast(`已导入实例和 ${result.rulesImported} 条规则草稿。`); }
    catch (error) { K.transfer.finish(operation,'uncertain',error.message); throw new Error('导入结果尚未确认，请先核对当前草稿。'); }
   }
  });
 }
 function webhookEditor(data,root,model,target,reload) {
  const item = target || {id:'',name:'',enabled:false,format:'',onStatus:true,onConfig:true};
  const snapshot = K.state();
  if (!Number.isSafeInteger(model.revision) || snapshot?.revision !== model.revision || !Array.isArray(snapshot.state?.instances)) throw new Error('配置或通知目标已变化，请刷新工作台并重新读取通知目标。');
  const scope = item.statusScope ?? '';
  if (typeof scope !== 'string' || (scope && !['instances','nodes'].includes(scope) && !/^(?:instance|node):[a-f0-9]{16}$/.test(scope))) throw new Error('状态通知范围无效，请重新读取通知目标。');
  const scopeOptions = [['','全部实例和节点'],['instances','全部本机实例'],['nodes','全部节点'],...snapshot.state.instances.map(instance => ['instance:'+instance.id, instance.name+' · '+instance.kind])];
  if (scope.startsWith('instance:') && !scopeOptions.some(([value]) => value === scope)) scopeOptions.push([scope,'已不存在的实例（ID '+scope.slice(9)+'；不发送实例状态）']);
  if (scope.startsWith('node:')) scopeOptions.push([scope,'已保存的节点（ID '+scope.slice(5)+'；等待读取）']);
  const checked = value => value ? 'checked' : '';
  async function loadNodeScopes(form) {
   const hint = form.querySelector('[data-node-scope-hint]'), select = form.elements.statusScope, epoch = K.epoch();
   const current = () => K.epoch() === epoch && K.isCurrent('config',root) && hint?.isConnected && form.contains(hint) && document.querySelector('#dialog')?.open;
   const unavailable = message => {
    if (scope.startsWith('node:')) {
     const saved = [...select.options].find(option => option.value === scope);
     if (saved) saved.textContent = '已保存的节点（ID '+scope.slice(5)+'；列表未确认）';
    }
    hint.textContent = message;
   };
   let timer;
   try {
    const controller = new AbortController();
    timer = setTimeout(() => controller.abort(),15000);
    const result = await K.api('nodes',undefined,{signal:controller.signal});
    if (!current()) return;
    if (result?.revision !== model.revision || K.state()?.revision !== model.revision) {
     unavailable('节点配置已变化；请关闭窗口并刷新。已选范围保留。');
     return;
    }
    if (!Array.isArray(result.nodes) || result.nodes.length > 32) throw new Error('节点列表无效');
    const ids = new Set();
    for (const node of result.nodes) {
     if (!node || typeof node.id !== 'string' || !/^[a-f0-9]{16}$/.test(node.id) || ids.has(node.id) || typeof node.name !== 'string' || typeof node.monitorEnabled !== 'boolean') throw new Error('节点列表无效');
     ids.add(node.id);
    }
    const selected = select.value;
    for (const node of result.nodes) {
     const value = 'node:'+node.id;
     let option = [...select.options].find(item => item.value === value);
     if (!option) { option = document.createElement('option'); option.value = value; select.append(option); }
     option.textContent = node.name + (node.monitorEnabled ? ' · 节点' : ' · 节点（未启用后台检查）');
    }
    if (scope.startsWith('node:') && !ids.has(scope.slice(5))) {
     const saved = [...select.options].find(option => option.value === scope);
     if (saved) saved.textContent = '已不存在的节点（ID '+scope.slice(5)+'；不发送节点状态）';
    }
    select.value = selected;
    hint.textContent = result.nodes.length ? '只有启用后台检查的节点会产生状态通知。' : '没有远程节点；原有选择仍会保留。';
   } catch (_) {
    if (current()) unavailable('节点列表暂不可读取；已选范围保留，仍可修改其他设置。');
   } finally { if (timer) clearTimeout(timer); }
  }
  K.openDialog({
   html:`<h2>${target ? '编辑' : '新增'}通知目标</h2><p class="muted">仅支持公网 HTTPS。地址、签名密钥和模板保存后不回显。</p><div class="form-grid"><label>名称<input name="targetName" maxlength="80" required value="${e(item.name)}" ${item.id === 'legacy' ? 'readonly' : ''}></label><label>消息格式<select name="format">${[['','通用 JSON'],['discord','Discord'],['slack','Slack'],['ntfy','ntfy'],['telegram','Telegram'],['gotify','Gotify']].map(([value,label]) => `<option value="${e(value)}" ${value === item.format ? 'selected' : ''}>${e(label)}</option>`).join('')}</select></label><label class="form-check span-2"><input type="checkbox" name="enabled" ${checked(item.enabled)}>启用通知</label><label class="span-2">HTTPS 地址${item.hasURL ? '（留空保留）' : ''}<input type="password" name="url" maxlength="1024" autocomplete="new-password"></label><label class="form-check span-2"><input type="checkbox" name="clearURL">清除地址</label><label class="span-2">HMAC 签名密钥${item.hasSigningSecret ? '（留空保留）' : ''}<input type="password" name="signingSecret" maxlength="1024" autocomplete="new-password"></label><label class="form-check span-2"><input type="checkbox" name="clearSigningSecret">清除签名密钥</label><label class="form-check span-2"><input type="checkbox" name="jsonTemplateEnabled" ${checked(item.jsonTemplateEnabled)}>使用自定义 JSON 模板（仅通用 JSON）</label><label class="span-2">JSON 模板${item.hasJSONTemplate ? '（留空保留）' : ''}<textarea name="jsonTemplate" rows="5" maxlength="8192" spellcheck="false"></textarea></label><label class="form-check span-2"><input type="checkbox" name="clearJSONTemplate">清除模板</label><label class="form-check"><input type="checkbox" name="onStatus" ${checked(item.onStatus)}>实例和节点状态</label><label class="span-2">状态通知对象<select name="statusScope">${scopeOptions.map(([value,label]) => `<option value="${e(value)}" ${value === scope ? 'selected' : ''}>${e(label)}</option>`).join('')}</select><small>只影响状态通知；选择单个实例或节点时，不发送其他对象的状态。</small><small data-node-scope-hint role="status">正在读取本机节点列表…</small></label><label class="form-check span-2"><input type="checkbox" name="statusProblemsOnly" ${checked(item.statusProblemsOnly)}>状态通知仅发送：实例退出（包括主动停止）、启动失败、节点不可用</label><p class="small-note span-2">实例退出通知可能早于清理完成；节点不可用不一定代表节点离线。</p><label class="form-check"><input type="checkbox" name="onConfig" ${checked(item.onConfig)}>草稿变更</label><label class="form-check"><input type="checkbox" name="onLoginLimit" ${checked(item.onLoginLimit)}>登录限流</label><label class="form-check"><input type="checkbox" name="onUpdate" ${checked(item.onUpdate)}>软件更新</label></div><p class="small-note">模板可用变量：{{event}}、{{at}}、{{instanceId}}、{{nodeId}}、{{kind}}、{{revision}}、{{sourceIp}}、{{version}}。通知结果仅表示目标接受请求。</p>`,
   submitLabel:'保存通知目标',
   onReady(form) { void loadNodeScopes(form); },
   async onSubmit(form) {
    if (!K.isCurrent('config',root)) throw new Error('页面已变化，请重新读取通知目标。');
    if (K.state()?.revision !== model.revision) throw new Error('草稿已变化，请关闭窗口并刷新后重新读取通知目标。');
    const f = form.elements;
    const payload = {revision:model.revision,action:'save',id:item.id,name:f.targetName.value,enabled:f.enabled.checked,format:f.format.value,url:f.url.value,clearURL:f.clearURL.checked,signingSecret:f.signingSecret.value,clearSigningSecret:f.clearSigningSecret.checked,jsonTemplate:f.jsonTemplate.value,clearJSONTemplate:f.clearJSONTemplate.checked,jsonTemplateEnabled:f.jsonTemplateEnabled.checked,onStatus:f.onStatus.checked,statusProblemsOnly:f.statusProblemsOnly.checked,statusScope:f.statusScope.value,onConfig:f.onConfig.checked,onLoginLimit:f.onLoginLimit.checked,onUpdate:f.onUpdate.checked};
    const generation = K.epoch();
    try { await withTimeout('webhooks',payload); }
    catch (error) {
     if (K.epoch() !== generation) return false;
     await reload().catch(() => {});
     if (K.epoch() !== generation) return false;
     if (!error.status || error.status >= 500) throw new Error('保存结果未确认，请重新读取并核对通知设置后再决定是否重试。');
     throw new Error(`${error.message}。请重新读取并核对通知设置。`);
    }
    if (K.epoch() !== generation) return false;
    try {
     await K.refresh();
     if (K.epoch() !== generation) return false;
     K.toast('通知目标已保存。');
    } catch (_) {
     if (K.epoch() !== generation) return false;
     await reload().catch(() => {});
     if (K.epoch() !== generation) return false;
     if (K.state()) K.toast('通知目标已保存，但刷新失败；请手动刷新并核对。');
    }
   }
  });
 }

 K.register('config', {
  render(data) {
   const items = data.state.instances || [], selected = chosen(items), history = (data.history || []).slice().reverse();
   if (!admin(data) && !data.identity?.viewConfig) return head('配置预览','当前账户没有配置详情查看权限。') + '<section class="panel"><div class="panel-content"><p>请联系管理员授予配置查看权限。</p></div></section>';
   const preview = `<section class="panel"><div class="panel-content"><div class="section-heading"><h2>实例配置预览</h2><span class="pill">脱敏</span></div><label>实例${instancePicker(items,selected)}</label><p class="small-note">&lt;redacted&gt; 表示已隐藏的凭据；预览不能直接部署。</p><div class="actions">${button('config-download','导出脱敏 TOML')}</div><pre class="code-block" data-config-output>${selected ? '正在读取…' : '暂无实例。'}</pre></div></section>`;
   if (!admin(data)) return head('配置预览','查看已授权实例的脱敏草稿。') + preview;
   const histories = history.length ? `<div class="table-wrap"><table><thead><tr><th>版本</th><th>说明</th><th>时间</th><th></th></tr></thead><tbody>${history.map(item => `<tr><td>r${item.revision}</td><td>${e(item.summary)}</td><td>${e(item.at ? new Date(item.at).toLocaleString('zh-CN') : '')}</td><td>${button('restore','恢复',`data-revision="${item.revision}"`)}</td></tr>`).join('')}</tbody></table></div>` : '<p class="muted">暂无历史快照。</p>';
   return head('配置中心','预览草稿、管理快照，以及导入和备份配置。') +
    `<div class="two-column">${preview}<section class="panel"><div class="panel-content"><h2>历史快照</h2><div class="toolbar"><label>保留数量（0–100）<input data-history-limit type="number" min="0" max="100" step="1" value="${Number(data.historyLimit)}"></label>${button('history-limit','保存数量')}</div><p class="small-note">减少数量会立即删除最早的快照。恢复前须停止全部实例，保留的计划会暂停。</p>${histories}</div></section></div>
    <div class="two-column"><section class="panel"><div class="panel-content"><h2>导出与备份</h2><label>导出范围<select data-scope><option value="all">当前配置和全部快照</option><option value="config">仅当前配置</option><option value="snapshots">全部历史快照</option><option value="snapshot">指定快照</option></select></label><label data-snapshot-label hidden>选择快照<select data-snapshot>${history.map(item => `<option value="${item.revision}">r${item.revision} · ${e(item.summary)}</option>`).join('')}</select></label><p class="small-note">普通导出包含数据密钥，请妥善保管；加密备份由单独密码保护。</p><div class="actions">${button('export-config','导出配置')}${button('backup-export','导出加密备份')}</div></div></section>
    <section class="panel"><div class="panel-content"><h2>导入与恢复</h2><p class="small-note">普通配置导入和加密备份恢复会替换当前配置，操作前须停止全部实例。TOML 导入则追加新实例草稿。</p><label>配置文件<input data-import-file type="file" accept=".json,application/json"></label><div class="actions">${button('import-config','导入配置')}${button('backup-import','恢复加密备份')}${button('import-toml','导入 frp TOML')}</div></div></section></div>
    <section class="panel"><div class="panel-content"><div class="section-heading"><div><h2>维护模式：限制受限账户启停</h2><p class="small-note">开启后，非管理员不能通过本工作台启动、停止或重启本机和远程实例，也不能预览远程控制；读取状态、配置校验和退出登录仍可使用。管理员可继续操作并关闭此模式。</p><p class="small-note">已有隧道、已受理请求、定时计划、自动启动及目标的独立远程控制授权不受影响。设置只保存在本机，不包含在导出或备份中。</p></div><div class="actions">${button('maintenance-refresh','刷新状态')}</div></div><div data-maintenance>正在读取…</div></div></section>
    <section class="panel"><div class="panel-content"><div class="section-heading"><div><h2>Webhook 通知</h2><p class="small-note">通知目标只保存在本机，事件不包含凭据或日志。发送测试使用已保存的设置，停用的目标也可发送；自定义模板应包含 {{event}} 以便接收方识别测试。</p></div><div class="actions">${button('webhook-refresh','刷新')}${button('webhook-new','新增目标')}</div></div><div data-webhooks>正在读取…</div></div></section>`;
  },
  mount(root,data) {
   const picker = root.querySelector('[data-instance]'), output = root.querySelector('[data-config-output]');
   if (!picker || !output) return;
   let previewSerial = 0, model = null, webhookSerial = 0;
   let maintenance = null, maintenanceSerial = 0, maintenanceBusy = false;
   async function saveConfig(path,payload,message) {
    try { await K.api(path,payload); }
    catch (error) {
     if (!error.status || error.status >= 500) { await K.refresh().catch(() => {}); throw new Error('操作结果未确认，请刷新并核对当前配置后重试。'); }
     throw error;
    }
    try { await K.refresh(); K.toast(message); }
    catch (_) { if (K.state()) K.toast('操作已受理，但刷新失败；请手动刷新并核对配置。'); }
   }
   async function loadPreview() {
    const id = picker.value, sequence = ++previewSerial; K.selectedInstance = id;
    if (!id) { output.textContent = '暂无实例。'; return; }
    if (K.state()?.revision !== data.revision) { output.textContent = '草稿已变化，请刷新后重新读取。'; return; }
    output.textContent = '正在读取…';
    try {
     const result = await K.api('config?id='+encodeURIComponent(id));
     if (sequence !== previewSerial || !K.isCurrent('config',root) || picker.value !== id) return;
     output.textContent = K.state()?.revision === data.revision ? result.toml : '草稿已变化，请刷新后重新读取。';
    }
    catch (error) { if (sequence === previewSerial && K.isCurrent('config',root)) output.textContent = '读取失败：' + error.message; }
   }
   async function loadWebhooks() {
    const box = root.querySelector('[data-webhooks]'); if (!box) return;
    const sequence = ++webhookSerial, generation = K.epoch();
    const controller = new AbortController(), deadline = setTimeout(() => controller.abort(),15000);
    box.textContent = '正在读取…';
    try {
     const result = await K.api('webhooks',undefined,{signal:controller.signal});
     if (generation !== K.epoch() || sequence !== webhookSerial || !K.isCurrent('config',root)) return;
     model = result;
     box.innerHTML = result.targets?.length ? `<div class="cards">${result.targets.map(target => `<article class="card"><div class="section-heading"><h3>${e(target.name)}</h3><span class="pill">${target.enabled ? '启用' : '停用'} · ${e(target.format || 'JSON')}</span></div><p class="small-note">成功 ${Number(target.status?.sent) || 0} · 失败 ${Number(target.status?.failed) || 0} · 丢弃 ${Number(target.status?.dropped) || 0}${target.status?.lastResult ? ' · 最近：'+e(target.status.lastResult) : ''}</p><div class="actions">${button('webhook-test','发送测试',`data-id="${e(target.id)}" ${target.hasURL ? '' : 'disabled title="请先保存通知地址"'}`)}${button('webhook-edit','编辑',`data-id="${e(target.id)}"`)}${button(target.enabled ? 'webhook-disable' : 'webhook-enable',target.enabled ? '停用' : '启用',`data-id="${e(target.id)}"`)}${button('webhook-delete','删除',`class="danger" data-id="${e(target.id)}"`)}</div></article>`).join('')}</div>` : '<p class="muted">尚未设置通知目标。</p>';
    } catch (error) {
     if (generation === K.epoch() && sequence === webhookSerial && K.isCurrent('config',root)) {
      model = null; box.textContent = controller.signal.aborted ? '读取通知目标超时，请刷新后重试。' : '读取失败：'+error.message;
     }
    } finally { clearTimeout(deadline); }
   }
   async function loadMaintenance() {
    const box = root.querySelector('[data-maintenance]'); if (!box) return;
    const sequence = ++maintenanceSerial; box.textContent = '正在读取…';
    try {
     const result = await K.api('maintenance');
     if (sequence !== maintenanceSerial || !K.isCurrent('config',root)) return;
     if (typeof result?.enabled !== 'boolean' || typeof result?.available !== 'boolean') throw new Error('维护状态格式无效。');
     maintenance = result;
     if (!result.available) {
      box.textContent = '维护设置无法读取，原文件已保留；受限账户的实例操作暂不可用，管理员仍可操作。请检查本机 maintenance.json 后刷新。';
      return;
     }
     box.innerHTML = `<p>当前状态：<strong>${result.enabled ? '已开启' : '已关闭'}</strong></p>${button('maintenance-toggle',result.enabled ? '关闭维护模式' : '开启维护模式')}`;
    } catch (error) {
     if (sequence === maintenanceSerial && K.isCurrent('config',root)) { maintenance = null; box.textContent = '读取失败：'+error.message; }
    }
   }
   picker.addEventListener('change',loadPreview); void loadPreview();
   if (admin(data)) { void loadWebhooks(); void loadMaintenance(); }
   const scope = root.querySelector('[data-scope]'); if (scope) scope.addEventListener('change',() => { root.querySelector('[data-snapshot-label]').hidden = scope.value !== 'snapshot'; });
   root.addEventListener('click',async event => {
    const target = event.target.closest('[data-action]'); if (!target || target.disabled) return;
    target.disabled = true;
    try {
     const action = target.dataset.action;
     if (action === 'config-download') {
      const id = picker.value; if (!id) throw new Error('请先选择实例。');
      const result = await K.api('config?id='+encodeURIComponent(id));
      if (!K.isCurrent('config',root) || picker.value !== id || K.state()?.revision !== data.revision) throw new Error('配置已变化，请刷新后重新导出。');
      K.download('kurumi-redacted.toml',result.toml,'text/plain'); return;
     }
     if (action === 'history-limit') {
      validRevision(data); const input = root.querySelector('[data-history-limit]'), limit = Number(input.value);
      if (input.value === '' || !Number.isInteger(limit) || limit < 0 || limit > 100) throw new Error('保留数量须为 0–100。');
      if (limit < (data.history || []).length && !confirm(`将删除 ${(data.history || []).length-limit} 份最早快照，继续？`)) return;
      await saveConfig('history-settings',{revision:data.revision,historyLimit:limit},'快照保留数量已更新。'); return;
     }
     if (action === 'restore') {
      validRevision(data); if (!confirm('恢复此历史草稿？请先停止全部实例；当前计划会暂停。')) return;
      await saveConfig('restore',{revision:data.revision,target:Number(target.dataset.revision)},'历史草稿已恢复。'); return;
     }
     if (action === 'export-config') {
      validRevision(data); const selected = selectedScope(root), query = new URLSearchParams({scope:selected.scope});
      if (selected.scope === 'snapshot') query.set('revision',String(selected.snapshot));
      const result = await K.api('export?'+query);
      if (!K.isCurrent('config',root) || K.state()?.revision !== data.revision || result.revision !== data.revision) throw new Error('草稿已变化，请刷新后重新导出。');
      K.download(`kurumi-${selected.scope}.json`,JSON.stringify(result,null,2)); K.toast('配置已导出，请妥善保管。'); return;
     }
     if (action === 'backup-export' || action === 'backup-import') { backupDialog(action === 'backup-export' ? 'export' : 'import',data,root); return; }
     if (action === 'import-toml') { importTOML(data,root); return; }
     if (action === 'import-config') {
      validRevision(data); const blocked = K.transfer.blocked(); if (blocked) throw new Error(blocked);
      const file = root.querySelector('[data-import-file]').files[0];
      if (!file) throw new Error('请选择配置文件。');
      if (!confirm('导入会替换当前配置、定时计划、历史快照和草稿事件记录，并撤销现有非管理员账户的本机实例授权。导入的计划将暂停；节点授权和通知设置保留在本机。请先停止全部托管实例。继续？')) return;
      const value = await readJSON(file,128*1024*1024);
      if (!K.isCurrent('config',root)) throw new Error('页面已变化，请重新操作。'); validRevision(data);
      const operation = K.transfer.begin('import');
      try { await withTimeout('import?revision='+data.revision,value); K.transfer.finish(operation,'complete'); }
      catch (error) {
       if (error.status === 400 || error.status === 409) { K.transfer.finish(operation,'rejected'); throw error; }
       K.transfer.finish(operation,'uncertain',error.message); throw new Error('导入结果尚未确认，请先核对当前配置。');
      }
      return;
     }
     if (action === 'maintenance-refresh') {
      if (maintenanceBusy) throw new Error('维护设置正在保存，请稍候。');
      await loadMaintenance(); return;
     }
     if (action === 'maintenance-toggle') {
      if (maintenanceBusy || !maintenance?.available) throw new Error('请先读取并核对维护状态。');
      const expected = maintenance.enabled, enabled = !expected;
      const message = enabled
       ? '开启维护模式？受限账户将不能通过本工作台启停或重启本机和远程实例。已有隧道、计划任务、自动启动和已受理操作不受影响，管理员仍可操作。'
       : '关闭维护模式？受限账户可继续按已有授权操作实例。';
      if (!confirm(message)) return;
      maintenanceBusy = true; ++maintenanceSerial;
      try {
       const result = await K.api('maintenance',{expected,enabled});
       if (typeof result?.enabled !== 'boolean' || result.available !== true || result.enabled !== enabled) throw new Error('服务端响应无法确认维护状态。');
       if (K.isCurrent('config',root)) { await loadMaintenance(); K.toast(enabled ? '维护模式已开启。' : '维护模式已关闭。'); }
      } catch (error) {
       await loadMaintenance();
       throw new Error(error.status === 409 ? '维护状态已变化，请核对后重新操作。' : '操作结果未确认，请刷新并核对维护状态。');
      } finally { maintenanceBusy = false; }
      return;
     }
     if (action === 'webhook-refresh') { await loadWebhooks(); return; }
     if (action === 'webhook-new') { if (!model) throw new Error('请先读取通知目标。'); if (model.targets.length >= model.limit) throw new Error('通知目标已达上限。'); webhookEditor(data,root,model,null,loadWebhooks); return; }
     if (action.startsWith('webhook-')) {
      if (!model) throw new Error('请先读取通知目标。');
      const item = model.targets.find(value => value.id === target.dataset.id); if (!item) throw new Error('目标已变化，请重新读取。');
      if (action === 'webhook-edit') { webhookEditor(data,root,model,item,loadWebhooks); return; }
      const operation = action.slice('webhook-'.length);
      if (operation === 'test') {
       const controller = new AbortController(), timer = setTimeout(() => controller.abort(),15000);
       try {
        const result = await K.api('webhooks',{revision:model.revision,action:'test',id:item.id},{signal:controller.signal});
        if (!K.isCurrent('config',root)) return;
        if (typeof result.accepted !== 'boolean' || typeof result.result !== 'string') throw new Error('无效的测试结果');
        K.toast(`${item.name}：${result.accepted ? '目标已接受测试通知' : '目标未确认接受，消息可能已发送'}（${result.result}）。`);
       } catch (error) {
        if (error.status === 404 || error.status === 409) { await loadWebhooks(); throw error; }
        if (error.status >= 400 && error.status < 500) throw error;
        throw new Error('测试结果未确认，消息可能已经发送，请核对接收端。');
       } finally { clearTimeout(timer); }
       return;
      }
      if (operation === 'delete' && !confirm(`删除通知目标「${item.name}」？`)) return;
      try { await K.api('webhooks',{revision:model.revision,action:operation,id:item.id}); }
      catch (error) { await loadWebhooks(); throw new Error(`${error.message}。请核对当前通知设置。`); }
      try { await K.refresh(); K.toast('通知目标已更新。'); }
      catch (_) { await loadWebhooks(); if (K.state()) K.toast('通知目标已更新，但刷新失败；请手动刷新并核对。'); }
     }
    } catch (error) { K.toast(error.message); }
    finally { if (target.isConnected) target.disabled = false; }
   });
  }
 });

 let logCleanup = null;
 K.register('logs', {
  render(data) {
   const items = (data.state.instances || []).filter(item => admin(data) || (data.identity?.logInstances || []).includes(item.id)), selected = chosen(items);
   const historyAdmin = admin(data) ? `<div data-log-history-admin><p class="small-note" data-history-settings>正在读取日志历史设置…</p><div class="actions"><button type="button" data-action="log-history-toggle" disabled>启用本机历史</button><button type="button" data-action="log-history-clear" disabled>清除全部本机历史</button></div></div>` : '';
   return head('运行日志','按实例查看近期输出；日志可能包含网络地址等信息。') + `<section class="panel"><div class="panel-content"><div class="toolbar"><label>实例${instancePicker(items,selected)}</label><label>范围<select data-log-mode><option value="live">当前运行</option><option value="history">本机历史</option></select></label><label>包含<input data-log-filter type="search" placeholder="搜索日志"></label><label>排除<input data-log-exclude type="search" placeholder="隐藏包含此内容的日志"></label><label class="form-check"><input data-log-case type="checkbox">区分大小写</label><div class="actions">${button('log-refresh','刷新')}${button('log-pause','暂停更新',`aria-pressed="false" ${selected ? '' : 'disabled'}`)}${button('log-bottom','跳到最新')}${button('log-download','导出当前内容')}</div></div><p class="small-note">实时模式保留当前运行最近 200 行。本机历史默认关闭，开启后所有实例共享最多 2 MiB；较早记录会被覆盖，积压、异常退出或写入失败可能丢行。脱敏后的日志仍可能包含敏感信息。</p>${historyAdmin}<p class="small-note" data-log-status>${selected ? '正在读取…' : '没有可查看日志的实例。'}</p><pre class="code-block" data-log-output tabindex="0"></pre></div></section>`;
  },
  mount(root,data) {
   const picker = root.querySelector('[data-instance]'), filter = root.querySelector('[data-log-filter]'), exclude = root.querySelector('[data-log-exclude]'), caseSensitive = root.querySelector('[data-log-case]'), output = root.querySelector('[data-log-output]'), status = root.querySelector('[data-log-status]'), pauseButton = root.querySelector('[data-action="log-pause"]');
   const mode = root.querySelector('[data-log-mode]'), settingsBox = root.querySelector('[data-log-history-admin]'), settingsText = root.querySelector('[data-history-settings]');
   const toggleHistory = root.querySelector('[data-action="log-history-toggle"]'), clearHistory = root.querySelector('[data-action="log-history-clear"]');
   let timer = null, controller = null, version = 0, lines = [], runId = '', painted = '', stopped = false, stream = null, pendingPaint = false, paused = false, pausedStale = false;
   let historyStatus = null, settingsStatus = null, settingsPending = false, settingsVersion = 0;
   const current = () => !stopped && K.isCurrent('logs',root);
   const isHistory = () => mode.value === 'history';
   function stopStream() { if (!stream) return; clearTimeout(stream.watchdog); stream.source.close(); stream = null; }
   function selectingOutput() {
    const selection = window.getSelection();
    return !!selection && !selection.isCollapsed && selection.rangeCount > 0 && selection.getRangeAt(0).intersectsNode(output);
   }
   function clearLogs() { pendingPaint = false; pausedStale = false; historyStatus = null; lines = []; painted = ''; runId = ''; output.textContent = ''; }
   function paint(force = false) {
    if (!current()) return;
    if (!force && selectingOutput()) { pendingPaint = true; return; }
    pendingPaint = false;
    const normalize = value => caseSensitive.checked ? value : value.toLocaleLowerCase();
    const include = normalize(filter.value), omitted = normalize(exclude.value);
    const matched = lines.filter(line => { const value = normalize(line); return (!include || value.includes(include)) && (!omitted || !value.includes(omitted)); });
    const text = matched.join('\n'), follow = output.scrollHeight-output.scrollTop-output.clientHeight < 36;
    if (output.textContent !== text) { output.textContent = text; if (follow) output.scrollTop = output.scrollHeight; }
    painted = text;
    if (isHistory()) status.textContent = !historyStatus ? (picker.value ? '正在读取日志历史…' : '没有可查看日志的实例。') : `本机历史（可能不完整） · ${historyStatus.enabled ? '记录已启用' : '已停用记录，已有历史保留'} · 显示 ${matched.length} / ${lines.length} 行${historyStatus.available === false ? ' · 存储状态异常，请联系管理员' : ''}`;
    else status.textContent = pausedStale ? '实例运行代次已变化，日志已清空；请刷新或继续更新。' : `${paused ? '已暂停更新' : stream?.ready ? '实时更新' : '轮询更新'} · 显示 ${matched.length} / ${lines.length} 行${runId ? '' : ' · 实例尚无运行输出'}`;
   }
   function checkPausedRun() {
    if (isHistory()) return;
    if ((K.state()?.runtime?.[picker.value]?.runId || '') === runId) return;
    clearLogs(); pausedStale = true; paint(true);
   }
   function schedule(delay = 5000) {
    clearTimeout(timer);
    if (!current() || document.hidden || !picker.value || isHistory()) return;
    if (paused) timer = setTimeout(() => { if (current() && !document.hidden && paused) { checkPausedRun(); schedule(); } },delay);
    else if (!stream) timer = setTimeout(() => { if (current() && !document.hidden && !paused) void load(); },delay);
   }
   function openStream(id, expectedRun) {
    const runtime = K.state()?.runtime?.[id];
    if (isHistory() || paused || typeof EventSource !== 'function' || !expectedRun || !['starting','running','stopping'].includes(runtime?.state) || document.hidden) return;
    const generation = K.epoch(), query = new URLSearchParams({id,runId:expectedRun});
    let source;
    try { source = new EventSource('/api/logs/stream?'+query); } catch (_) { return; }
    const connection = {source,ready:false,watchdog:null}; stream = connection;
    const valid = () => current() && stream === connection && generation === K.epoch() && picker.value === id;
    const fail = message => {
     if (!valid()) return;
     stopStream(); status.textContent = message; void K.refresh(true).catch(() => {}); schedule();
    };
    const touch = () => { clearTimeout(connection.watchdog); connection.watchdog = setTimeout(() => fail('实时连接中断，正在改用轮询。'),30000); };
    const receive = (event,reset) => {
     if (!valid()) return;
     if ((K.state()?.runtime?.[id]?.runId || '') !== expectedRun) {
      clearLogs(); fail('实例运行代次已变化，正在重新读取。'); return;
     }
     let payload;
     try { payload = JSON.parse(event.data); } catch (_) { fail('实时日志格式无效，正在改用轮询。'); return; }
     if (payload?.instanceId !== id || payload.runId !== expectedRun || !Array.isArray(payload.lines) || payload.lines.length > 200 || !payload.lines.every(line => typeof line === 'string') || (!reset && !connection.ready)) {
      fail('实时日志状态已变化，正在改用轮询。'); return;
     }
     if (reset) { lines = payload.lines; connection.ready = true; }
     else lines = lines.concat(payload.lines).slice(-200);
     runId = expectedRun; touch(); paint();
    };
    source.addEventListener('snapshot',event => receive(event,true));
    source.addEventListener('lines',event => receive(event,false));
    source.addEventListener('heartbeat',() => {
     if (!valid()) return;
     if ((K.state()?.runtime?.[id]?.runId || '') !== expectedRun) { clearLogs(); fail('实例运行代次已变化，正在重新读取。'); }
     else touch();
    });
    source.addEventListener('end',event => {
     if (!valid()) return;
     let payload; try { payload = JSON.parse(event.data); } catch (_) { fail('实例状态已变化，正在重新读取。'); return; }
     if (payload?.instanceId !== id || payload.runId !== expectedRun) { fail('实例状态已变化，正在重新读取。'); return; }
     stopStream(); status.textContent = '实例已退出，正在更新状态。'; void K.refresh(true).catch(() => {}); schedule(1000);
    });
    source.onerror = () => fail('实时连接不可用，正在改用轮询。');
    touch();
   }
   async function load() {
    clearTimeout(timer); stopStream(); controller?.abort(); const request = new AbortController(); controller = request;
    const id = picker.value, serial = ++version, historical = isHistory(); K.selectedInstance = id;
    if (!id) { clearLogs(); status.textContent = '没有可查看日志的实例。'; return; }
    try {
     const result = await K.api('logs?id='+encodeURIComponent(id)+(historical ? '&history=1' : ''),undefined,{signal:request.signal});
     if (!current() || serial !== version || picker.value !== id || historical !== isHistory()) return;
     if (historical) {
      if (result.history !== true || !Array.isArray(result.lines) || result.lines.length > 20000 || !result.lines.every(line => typeof line === 'string') || typeof result.status?.enabled !== 'boolean' || typeof result.status?.available !== 'boolean') throw new Error('日志历史响应无效。');
      historyStatus = result.status; runId = ''; lines = result.lines; pausedStale = false; paint(); return;
     }
     if (!Array.isArray(result.lines) || result.lines.length > 200 || !result.lines.every(line => typeof line === 'string')) throw new Error('日志响应无效。');
     const observed = K.state()?.runtime?.[id]?.runId || '';
     if ((result.runId || '') !== observed) { clearLogs(); status.textContent = '实例运行状态正在更新，请稍候。'; void K.refresh(true).catch(() => {}); return; }
     runId = result.runId || ''; lines = result.lines; pausedStale = false; paint(); openStream(id,runId);
    } catch (error) {
     if (current() && serial === version && error.name !== 'AbortError') { clearLogs(); status.textContent = '读取失败：'+error.message; }
    } finally { if (controller === request) { controller = null; schedule(); } }
   }
   function setPaused(value) {
    if (isHistory()) return;
    paused = value; pauseButton.textContent = paused ? '继续更新' : '暂停更新'; pauseButton.setAttribute('aria-pressed',String(paused));
    clearTimeout(timer);
    if (paused) {
     ++version; controller?.abort(); controller = null; stopStream();
     checkPausedRun(); if (!pausedStale) paint(true); schedule();
    } else {
     if ((K.state()?.runtime?.[picker.value]?.runId || '') !== runId) clearLogs();
     pausedStale = false; status.textContent = '正在重新读取…';
     if (!document.hidden) void load();
    }
   }
   function validHistorySettings(value) {
    return value && typeof value.enabled === 'boolean' && typeof value.available === 'boolean' && typeof value.settingsError === 'boolean' && typeof value.dropped === 'string' && /^\d{1,20}$/.test(value.dropped);
   }
   function renderHistorySettings() {
    if (!settingsBox) return;
    toggleHistory.disabled = settingsPending || !settingsStatus || settingsStatus.settingsError;
    clearHistory.disabled = settingsPending || !settingsStatus;
    if (!settingsStatus) return;
    toggleHistory.textContent = settingsStatus.enabled ? '停用本机历史' : '启用本机历史';
    settingsText.textContent = settingsStatus.settingsError ? '日志历史设置文件无法读取，采集已停用；原文件已保留，请检查数据目录后重启工作台。' : `本机历史${settingsStatus.enabled ? '记录已启用' : '已停用记录'}；${settingsStatus.available ? '未报告存储错误' : '存储状态异常'}；本次工作台因队列满或写入失败丢弃 ${settingsStatus.dropped} 行。停用保留已有文件，清除才删除。`;
   }
   async function loadHistorySettings() {
    if (!settingsBox || settingsPending || !current()) return;
    const serial = ++settingsVersion;
    try {
     const value = await K.api('logs/history');
     if (!current() || serial !== settingsVersion) return;
     if (value?.ok !== true || !validHistorySettings(value.status)) throw new Error('日志历史设置响应无效。');
     settingsStatus = value.status; renderHistorySettings();
    } catch (_) {
     if (current() && serial === settingsVersion) { settingsStatus = null; renderHistorySettings(); settingsText.textContent = '无法读取日志历史设置，请刷新后重试。'; }
    }
   }
   async function changeHistorySettings(buttonAction) {
    if (!settingsBox || settingsPending || !settingsStatus) return;
    const action = buttonAction === 'log-history-clear' ? 'clear' : settingsStatus.enabled ? 'disable' : 'enable';
    if (action !== 'clear' && settingsStatus.settingsError) return;
    const question = {
     enable:'启用本机历史？新日志会以脱敏后文本保存在本机，仍可能包含敏感信息；所有实例共享最多 2 MiB。',
     disable:'停用本机历史记录？已有文件会保留，仍可查看和手动清除。',
     clear:'清除所有实例的本机日志历史？清除不可撤销，运行中的实例可能立即产生新记录。'
    }[action];
    if (!confirm(question)) return;
    settingsPending = true; ++settingsVersion; renderHistorySettings(); settingsText.textContent = '正在提交；关闭页面不能保证取消操作。';
    try {
     const value = await K.api('logs/history',{action});
     if (!current()) return;
     if (value?.ok !== true || !validHistorySettings(value.status) || (action === 'enable' && !value.status.enabled) || (action === 'disable' && value.status.enabled)) throw new Error('日志历史响应无效。');
     settingsStatus = value.status;
     K.toast({enable:'已启用本机历史。',disable:'已停用记录，已有历史保留。',clear:'已清除本机历史，运行中的实例可能立即产生新记录。'}[action]);
    } catch (_) {
     if (current()) K.toast('操作结果未确认，请刷新设置并核对后再决定是否重试。');
    } finally {
     settingsPending = false;
     if (current()) { renderHistorySettings(); void loadHistorySettings(); if (isHistory()) void load(); }
    }
   }
   const visible = () => { if (document.hidden) { clearTimeout(timer); stopStream(); controller?.abort(); } else if (current()) { if (paused) { checkPausedRun(); schedule(); } else void load(); } };
   const selectionChanged = () => { if (pendingPaint && !selectingOutput()) paint(); };
   picker.addEventListener('change',() => { pauseButton.disabled = isHistory() || !picker.value; clearLogs(); void load(); });
   mode.addEventListener('change',() => {
    paused = false; pauseButton.textContent = '暂停更新'; pauseButton.setAttribute('aria-pressed','false'); pauseButton.disabled = isHistory() || !picker.value;
    clearLogs(); void load();
   });
   filter.addEventListener('input',() => paint(true));
    exclude.addEventListener('input',() => paint(true));
    caseSensitive.addEventListener('change',() => paint(true));
   root.addEventListener('mouseup',() => paint());
   root.addEventListener('click',event => {
    const action = event.target.closest('[data-action]')?.dataset.action;
    if (action === 'log-refresh') { void load(); void loadHistorySettings(); }
    if (action === 'log-pause') setPaused(!paused);
    if (action === 'log-bottom') {
     if (!isHistory() && (K.state()?.runtime?.[picker.value]?.runId || '') !== runId) { clearLogs(); status.textContent = '实例运行代次已变化，正在重新读取。'; void load(); return; }
     if (!lines.length) { K.toast('当前没有可查看的日志。'); return; }
     paint(true); output.scrollTop = output.scrollHeight;
    }
    if (action === 'log-download') { if (!isHistory() && (K.state()?.runtime?.[picker.value]?.runId || '') !== runId) K.toast('实例运行代次已变化，请先刷新日志。'); else if (!painted) K.toast('当前没有可导出的日志。'); else K.download(isHistory() ? 'kurumi-log-history.txt' : 'kurumi-logs.txt',painted,'text/plain'); }
    if (action === 'log-history-toggle' || action === 'log-history-clear') void changeHistorySettings(action);
   });
   document.addEventListener('visibilitychange',visible);
   document.addEventListener('selectionchange',selectionChanged);
   logCleanup = () => { stopped = true; ++version; ++settingsVersion; clearTimeout(timer); stopStream(); controller?.abort(); document.removeEventListener('visibilitychange',visible); document.removeEventListener('selectionchange',selectionChanged); };
   void load(); void loadHistorySettings();
  },
  dispose() { logCleanup?.(); logCleanup = null; }
 });
})();
