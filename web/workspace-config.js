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
    ? '<h2>导出加密备份</h2><p class="muted">备份密码不会保存；请将密码和文件分开保管。</p><div class="form-grid"><label>备份密码<input name="password" type="password" minlength="12" maxlength="1024" required autocomplete="new-password"></label><label>确认密码<input name="confirm" type="password" minlength="12" maxlength="1024" required autocomplete="new-password"></label></div>'
    : '<h2>恢复加密备份</h2><p class="muted">请先停止全部托管实例。恢复会替换配置、定时计划和快照；导入的计划将暂停。节点授权和通知设置保留在本机。</p><div class="form-grid"><label class="span-2">备份文件<input type="file" name="file" accept=".json,application/json" required></label><label class="span-2">备份密码<input name="password" type="password" required maxlength="1024" autocomplete="current-password"></label><label class="form-check span-2"><input type="checkbox" name="confirmRestore" required>确认替换当前配置、计划和历史快照</label></div>',
   submitLabel:mode === 'export' ? '导出加密备份' : '验证并恢复',
   async onSubmit(form) {
    validRevision(data);
    const password = form.elements.password.value;
    if (mode === 'export') {
     if (password !== form.elements.confirm.value) throw new Error('两次输入的密码不一致。');
     const result = await K.api('backup/export',{revision:data.revision,password,scope:scope.scope,snapshot:scope.snapshot});
     if (!K.isCurrent('config',root) || K.state()?.revision !== data.revision) throw new Error('页面或草稿已变化，请重新导出。');
     K.download(`kurumi-${scope.scope}-encrypted.json`,JSON.stringify(result,null,2)); K.toast('加密备份已导出，请妥善保管密码。'); return;
    }
    const backup = await readJSON(form.elements.file.files[0],180*1024*1024);
    if (!K.isCurrent('config',root)) throw new Error('页面已变化，请重新操作。'); validRevision(data);
    const operation = K.transfer.begin('backup');
    try { await withTimeout('backup/import',{revision:data.revision,password,backup}); K.transfer.finish(operation,'complete'); }
    catch (error) { K.transfer.finish(operation,'uncertain',error.message); throw new Error('恢复结果尚未确认，请核对提示和当前配置。'); }
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
  const checked = value => value ? 'checked' : '';
  K.openDialog({
   html:`<h2>${target ? '编辑' : '新增'}通知目标</h2><p class="muted">仅支持公网 HTTPS。地址、签名密钥和模板保存后不回显。</p><div class="form-grid"><label>名称<input name="targetName" maxlength="80" required value="${e(item.name)}" ${item.id === 'legacy' ? 'readonly' : ''}></label><label>消息格式<select name="format">${[['','通用 JSON'],['discord','Discord'],['slack','Slack'],['ntfy','ntfy'],['telegram','Telegram'],['gotify','Gotify']].map(([value,label]) => `<option value="${e(value)}" ${value === item.format ? 'selected' : ''}>${e(label)}</option>`).join('')}</select></label><label class="form-check span-2"><input type="checkbox" name="enabled" ${checked(item.enabled)}>启用通知</label><label class="span-2">HTTPS 地址${item.hasURL ? '（留空保留）' : ''}<input type="password" name="url" maxlength="1024" autocomplete="new-password"></label><label class="form-check span-2"><input type="checkbox" name="clearURL">清除地址</label><label class="span-2">HMAC 签名密钥${item.hasSigningSecret ? '（留空保留）' : ''}<input type="password" name="signingSecret" maxlength="1024" autocomplete="new-password"></label><label class="form-check span-2"><input type="checkbox" name="clearSigningSecret">清除签名密钥</label><label class="form-check span-2"><input type="checkbox" name="jsonTemplateEnabled" ${checked(item.jsonTemplateEnabled)}>使用自定义 JSON 模板（仅通用 JSON）</label><label class="span-2">JSON 模板${item.hasJSONTemplate ? '（留空保留）' : ''}<textarea name="jsonTemplate" rows="5" maxlength="8192" spellcheck="false"></textarea></label><label class="form-check span-2"><input type="checkbox" name="clearJSONTemplate">清除模板</label><label class="form-check"><input type="checkbox" name="onStatus" ${checked(item.onStatus)}>实例和节点状态</label><label class="form-check"><input type="checkbox" name="onConfig" ${checked(item.onConfig)}>草稿变更</label><label class="form-check"><input type="checkbox" name="onLoginLimit" ${checked(item.onLoginLimit)}>登录限流</label><label class="form-check"><input type="checkbox" name="onUpdate" ${checked(item.onUpdate)}>软件更新</label></div><p class="small-note">模板可用变量：{{event}}、{{at}}、{{instanceId}}、{{nodeId}}、{{kind}}、{{revision}}、{{sourceIp}}、{{version}}。通知结果仅表示目标接受请求。</p>`,
   submitLabel:'保存通知目标',
   async onSubmit(form) {
    if (!K.isCurrent('config',root)) throw new Error('页面已变化，请重新读取通知目标。');
    const f = form.elements;
    const payload = {revision:model.revision,action:'save',id:item.id,name:f.targetName.value,enabled:f.enabled.checked,format:f.format.value,url:f.url.value,clearURL:f.clearURL.checked,signingSecret:f.signingSecret.value,clearSigningSecret:f.clearSigningSecret.checked,jsonTemplate:f.jsonTemplate.value,clearJSONTemplate:f.clearJSONTemplate.checked,jsonTemplateEnabled:f.jsonTemplateEnabled.checked,onStatus:f.onStatus.checked,onConfig:f.onConfig.checked,onLoginLimit:f.onLoginLimit.checked,onUpdate:f.onUpdate.checked};
    try { await K.api('webhooks',payload); }
    catch (error) { await reload().catch(() => {}); throw new Error(`${error.message}。请重新读取并核对通知设置。`); }
    try { await K.refresh(); K.toast('通知目标已保存。'); }
    catch (_) { await reload().catch(() => {}); if (K.state()) K.toast('通知目标已保存，但刷新失败；请手动刷新并核对。'); }
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
    <section class="panel"><div class="panel-content"><div class="section-heading"><div><h2>Webhook 通知</h2><p class="small-note">通知目标只保存在本机，事件不包含凭据或日志。</p></div><div class="actions">${button('webhook-refresh','刷新')}${button('webhook-new','新增目标')}</div></div><div data-webhooks>正在读取…</div></div></section>`;
  },
  mount(root,data) {
   const picker = root.querySelector('[data-instance]'), output = root.querySelector('[data-config-output]');
   if (!picker || !output) return;
   let previewSerial = 0, model = null, webhookSerial = 0;
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
    const sequence = ++webhookSerial; box.textContent = '正在读取…';
    try {
     const result = await K.api('webhooks');
     if (sequence !== webhookSerial || !K.isCurrent('config',root)) return;
     model = result;
     box.innerHTML = result.targets?.length ? `<div class="cards">${result.targets.map(target => `<article class="card"><div class="section-heading"><h3>${e(target.name)}</h3><span class="pill">${target.enabled ? '启用' : '停用'} · ${e(target.format || 'JSON')}</span></div><p class="small-note">成功 ${Number(target.status?.sent) || 0} · 失败 ${Number(target.status?.failed) || 0} · 丢弃 ${Number(target.status?.dropped) || 0}${target.status?.lastResult ? ' · 最近：'+e(target.status.lastResult) : ''}</p><div class="actions">${button('webhook-edit','编辑',`data-id="${e(target.id)}"`)}${button(target.enabled ? 'webhook-disable' : 'webhook-enable',target.enabled ? '停用' : '启用',`data-id="${e(target.id)}"`)}${button('webhook-delete','删除',`class="danger" data-id="${e(target.id)}"`)}</div></article>`).join('')}</div>` : '<p class="muted">尚未设置通知目标。</p>';
    } catch (error) { if (sequence === webhookSerial && K.isCurrent('config',root)) { model = null; box.textContent = '读取失败：'+error.message; } }
   }
   picker.addEventListener('change',loadPreview); void loadPreview(); if (admin(data)) void loadWebhooks();
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
      if (!confirm('导入会替换当前配置、定时计划和快照；请先停止全部实例。继续？')) return;
      const value = await readJSON(file,128*1024*1024);
      if (!K.isCurrent('config',root)) throw new Error('页面已变化，请重新操作。'); validRevision(data);
      const operation = K.transfer.begin('import');
      try { await withTimeout('import?revision='+data.revision,value); K.transfer.finish(operation,'complete'); }
      catch (error) { K.transfer.finish(operation,'uncertain',error.message); throw new Error('导入结果尚未确认，请先核对当前配置。'); }
      return;
     }
     if (action === 'webhook-refresh') { await loadWebhooks(); return; }
     if (action === 'webhook-new') { if (!model) throw new Error('请先读取通知目标。'); if (model.targets.length >= model.limit) throw new Error('通知目标已达上限。'); webhookEditor(data,root,model,null,loadWebhooks); return; }
     if (action.startsWith('webhook-')) {
      if (!model) throw new Error('请先读取通知目标。');
      const item = model.targets.find(value => value.id === target.dataset.id); if (!item) throw new Error('目标已变化，请重新读取。');
      if (action === 'webhook-edit') { webhookEditor(data,root,model,item,loadWebhooks); return; }
      const operation = action.slice('webhook-'.length);
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
   return head('运行日志','按实例查看近期输出；日志可能包含网络地址等信息。') + `<section class="panel"><div class="panel-content"><div class="toolbar"><label>实例${instancePicker(items,selected)}</label><label>筛选<input data-log-filter type="search" placeholder="搜索日志"></label><div class="actions">${button('log-refresh','刷新')}${button('log-download','导出当前内容')}</div></div><p class="small-note" data-log-status>${selected ? '正在读取…' : '没有可查看日志的实例。'}</p><pre class="code-block" data-log-output tabindex="0"></pre></div></section>`;
  },
  mount(root,data) {
   const picker = root.querySelector('[data-instance]'), filter = root.querySelector('[data-log-filter]'), output = root.querySelector('[data-log-output]'), status = root.querySelector('[data-log-status]');
   let timer = null, controller = null, version = 0, lines = [], runId = '', painted = '', stopped = false, stream = null;
   const current = () => !stopped && K.isCurrent('logs',root);
   function stopStream() { if (!stream) return; clearTimeout(stream.watchdog); stream.source.close(); stream = null; }
   function paint() {
    if (!current() || window.getSelection()?.toString()) return;
    const query = filter.value.toLocaleLowerCase(), matched = query ? lines.filter(line => line.toLocaleLowerCase().includes(query)) : lines;
    const text = matched.join('\n'), follow = output.scrollHeight-output.scrollTop-output.clientHeight < 36;
    if (output.textContent !== text) { output.textContent = text; if (follow) output.scrollTop = output.scrollHeight; }
    painted = text; status.textContent = `${stream?.ready ? '实时更新' : '轮询更新'} · 显示 ${matched.length} / ${lines.length} 行${runId ? '' : ' · 实例尚无运行输出'}`;
   }
   function schedule(delay = 5000) { clearTimeout(timer); if (current() && !document.hidden && picker.value && !stream) timer = setTimeout(() => { void load(); },delay); }
   function openStream(id, expectedRun) {
    const runtime = K.state()?.runtime?.[id];
    if (typeof EventSource !== 'function' || !expectedRun || !['starting','running','stopping'].includes(runtime?.state) || document.hidden) return;
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
      lines = []; painted = ''; output.textContent = ''; fail('实例运行代次已变化，正在重新读取。'); return;
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
     if ((K.state()?.runtime?.[id]?.runId || '') !== expectedRun) { lines = []; painted = ''; runId = ''; output.textContent = ''; fail('实例运行代次已变化，正在重新读取。'); }
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
    const id = picker.value, serial = ++version; K.selectedInstance = id;
    if (!id) { lines = []; painted = ''; output.textContent = ''; status.textContent = '没有可查看日志的实例。'; return; }
    try {
     const result = await K.api('logs?id='+encodeURIComponent(id),undefined,{signal:request.signal});
     if (!current() || serial !== version || picker.value !== id) return;
     if (!Array.isArray(result.lines) || result.lines.length > 200 || !result.lines.every(line => typeof line === 'string')) throw new Error('日志响应无效。');
     const observed = K.state()?.runtime?.[id]?.runId || '';
     if ((result.runId || '') !== observed) { lines = []; painted = ''; runId = ''; output.textContent = ''; status.textContent = '实例运行状态正在更新，请稍候。'; void K.refresh(true).catch(() => {}); return; }
     runId = result.runId || ''; lines = result.lines; paint(); openStream(id,runId);
    } catch (error) {
     if (current() && serial === version && error.name !== 'AbortError') { status.textContent = '读取失败：'+error.message; lines = []; painted = ''; output.textContent = ''; }
    } finally { if (controller === request) controller = null; schedule(); }
   }
   const visible = () => { if (document.hidden) { clearTimeout(timer); stopStream(); controller?.abort(); } else if (current()) void load(); };
   picker.addEventListener('change',() => { lines = []; painted = ''; runId = ''; output.textContent = ''; void load(); });
   filter.addEventListener('input',paint);
   root.addEventListener('mouseup',paint);
   root.addEventListener('click',event => {
    const action = event.target.closest('[data-action]')?.dataset.action;
    if (action === 'log-refresh') void load();
    if (action === 'log-download') { if ((K.state()?.runtime?.[picker.value]?.runId || '') !== runId) K.toast('实例运行代次已变化，请先刷新日志。'); else if (!painted) K.toast('当前没有可导出的日志。'); else K.download('kurumi-logs.txt',painted,'text/plain'); }
   });
   document.addEventListener('visibilitychange',visible);
   logCleanup = () => { stopped = true; ++version; clearTimeout(timer); stopStream(); controller?.abort(); document.removeEventListener('visibilitychange',visible); };
   void load();
  },
  dispose() { logCleanup?.(); logCleanup = null; }
 });
})();
