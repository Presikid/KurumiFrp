'use strict';

// This script shares the app's authenticated API and dialog lifecycle.
window.KurumiWebhooks = (() => {
 let model = null, pending = null;
 const formats = {'':'通用 JSON', discord:'Discord', slack:'Slack', ntfy:'ntfy', telegram:'Telegram', gotify:'Gotify'};

 function current(generation, session) {
  return generation === webhookDialogGeneration && session === authGeneration && webhookDialogEl?.open;
 }
 function controlsBusy(value) {
  webhookDialogEl.querySelectorAll('button, input, select, textarea').forEach(control => {
   if (value) { control.dataset.webhookWasDisabled = control.disabled ? '1' : '0'; control.disabled = true; }
   else { control.disabled = control.dataset.webhookWasDisabled === '1'; delete control.dataset.webhookWasDisabled; }
  });
 }
 function notice(message) {
  model = null;
  webhookDialogEl.innerHTML = '<h2>Webhook 通知</h2><p class="error" role="alert"></p><div class="dialog-actions"><button type="button" data-webhook-action="close">关闭</button><button type="button" data-webhook-action="reload">重新读取</button></div>';
  webhookDialogEl.querySelector('.error').textContent = message;
 }
 async function read(generation, session, failureMessage = '读取通知目标失败，请重试。') {
  webhookDialogEl.innerHTML = '<h2>Webhook 通知</h2><p>正在读取设置…</p>';
  const controller = new AbortController(), timer = setTimeout(() => controller.abort(), 15000);
  try {
   const result = await api('webhooks', undefined, {signal:controller.signal});
   if (!current(generation, session)) return false;
   if (!Array.isArray(result.targets) || !Number.isSafeInteger(result.revision) || !Number.isSafeInteger(result.limit)) throw new Error('无效的通知数据');
   model = result;
   list();
   return true;
  } catch (_) {
   if (current(generation, session)) notice(failureMessage);
   return false;
  } finally { clearTimeout(timer); }
 }
 function list() {
  const items = model.targets.map(target => {
   const status = target.status || {}, id = esc(target.id);
   return `<section class="panel"><div class="panel-title"><h3>${esc(target.name)}</h3><small>${target.enabled ? '已启用' : '已停用'} · ${esc(formats[target.format] || '未知格式')}</small></div><div class="panel-content"><p class="small-note">本次运行：成功 ${Number(status.sent) || 0} · 失败 ${Number(status.failed) || 0} · 丢弃 ${Number(status.dropped) || 0}${status.lastResult ? ' · 最近结果 ' + esc(status.lastResult) : ''}${status.lastAt ? ' · ' + esc(status.lastAt) : ''}${target.enabled && !status.ready ? ' · 配置不可用，请检查并重新设置' : ''}</p><div class="actions"><button type="button" data-webhook-action="edit" data-id="${id}">编辑</button><button type="button" data-webhook-action="${target.enabled ? 'disable' : 'enable'}" data-id="${id}">${target.enabled ? '停用' : '启用'}</button><button type="button" class="danger" data-webhook-action="delete" data-id="${id}">删除</button></div></div></section>`;
  }).join('');
  webhookDialogEl.innerHTML = `<h2>Webhook 通知目标</h2><p class="muted">每个目标分别配置地址、格式、事件和可选签名。事件字段可能包含实例 ID / 类型、配置版本、可用软件版本或登录连接对端 IP，不包含账户名、凭据或日志；自定义模板会发送其中填写的内容。</p>${items || '<p class="small-note">尚未设置通知目标。</p>'}<p class="small-note">共 ${model.targets.length} / ${model.limit} 个目标。新增目标默认停用；结果仅表示目标接受请求。队列有限，发送失败不会阻止其他目标，单个慢目标会延迟后续目标。</p><p class="error" role="alert"></p><div class="dialog-actions"><button type="button" data-webhook-action="close">关闭</button><button type="button" data-webhook-action="reload">刷新结果</button><button type="button" class="primary" data-webhook-action="new" ${model.targets.length >= model.limit ? 'disabled' : ''}>新增目标</button></div>`;
 }
 function edit(target) {
  const isNew = !target, v = target || {id:'', name:'', enabled:false, format:'', onStatus:true, onConfig:true, onLoginLimit:false, onUpdate:false};
  const options = Object.entries(formats).map(([value, label]) => `<option value="${value}" ${v.format === value ? 'selected' : ''}>${label}</option>`).join('');
  webhookDialogEl.innerHTML = `<form id="webhook-target-form" data-id="${esc(v.id)}" data-revision="${model.revision}"><h2>${isNew ? '新增通知目标' : '编辑通知目标'}</h2><label>名称<input name="targetName" maxlength="80" required value="${esc(v.name)}" ${v.id === 'legacy' ? 'readonly' : ''}></label><label class="form-check"><input type="checkbox" name="enabled" ${v.enabled ? 'checked' : ''}>启用通知</label><label>消息格式<select name="format">${options}</select></label><label>HTTPS 地址${v.hasURL ? '（已保存，留空保留）' : ''}<input type="password" name="url" maxlength="1024" autocomplete="new-password" placeholder="https://…"></label><label class="form-check"><input type="checkbox" name="clearURL">清除已保存地址</label><p class="small-note">Discord / Slack 填写传入 Webhook 地址，ntfy 填写完整主题地址。Telegram 填写 https://api.telegram.org/botTOKEN/sendMessage?chat_id=ID（只填一个 chat_id）；Gotify 填写完整消息地址，例如 https://push.example.com/message?token=TOKEN。更换格式时请核对地址。</p><label>可选 HMAC-SHA256 签名密钥${v.hasSigningSecret ? '（已保存，留空保留）' : ''}<input type="password" name="signingSecret" maxlength="1024" autocomplete="new-password"></label><label class="form-check"><input type="checkbox" name="clearSigningSecret">清除已保存签名密钥</label><p class="small-note">密钥须为 8–1024 字节的 UTF-8 文本且不含 ASCII 控制字符。保存后不再显示，接收方需自行验证签名。</p><label class="form-check"><input type="checkbox" name="jsonTemplateEnabled" ${v.jsonTemplateEnabled ? 'checked' : ''}>启用自定义 JSON 模板（仅通用 JSON）</label><label>JSON 模板${v.hasJSONTemplate ? '（已保存，留空保留）' : ''}<textarea name="jsonTemplate" maxlength="8192" rows="5" autocomplete="off" spellcheck="false" placeholder='{"event":"{{event}}","time":"{{at}}"}'></textarea></label><label class="form-check"><input type="checkbox" name="clearJSONTemplate">清除已保存模板</label><p class="small-note">模板仅替换字符串值中的 {{event}}、{{at}}、{{instanceId}}、{{kind}}、{{revision}}、{{sourceIp}} 和 {{version}}；不存在的字段为空字符串。最多 8192 字节、16 层。模板可能含敏感内容，保存在本机且不回显；其内容会发送给目标。切换其他格式前请先取消启用模板。</p><label class="form-check"><input type="checkbox" name="onStatus" ${v.onStatus ? 'checked' : ''}>实例启动、退出及启动失败</label><label class="form-check"><input type="checkbox" name="onConfig" ${v.onConfig ? 'checked' : ''}>草稿保存、删除、复制、批量修改、导入和恢复</label><label class="form-check"><input type="checkbox" name="onLoginLimit" ${v.onLoginLimit ? 'checked' : ''}>登录尝试被限流</label><label class="form-check"><input type="checkbox" name="onUpdate" ${v.onUpdate ? 'checked' : ''}>软件更新可用</label><p class="small-note">登录限流通知可能包含工作台连接对端 IP，反向代理或 SSH 转发时可能是代理或本机地址；不会读取转发请求头。更新通知仅在现有版本检查发现可安装的新版本时发送。</p><p class="small-note">仅接受公网 HTTPS，不跟随重定向，不使用系统代理。更改此目标或关闭工作台时，该目标尚未发送的消息可能丢失。</p><p class="error" role="alert"></p><div class="dialog-actions"><button type="button" data-webhook-action="back">返回</button><button class="primary" type="submit">保存</button></div></form>`;
 }
 async function mutate(payload) {
  if (pending) return;
  const generation = webhookDialogGeneration, session = authGeneration, request = {};
  pending = request;
  controlsBusy(true);
  const controller = new AbortController(), timer = setTimeout(() => controller.abort(), 15000);
  try {
   await api('webhooks', payload, {signal:controller.signal});
   if (!current(generation, session)) return;
   pending = null;
   const loaded = await read(generation, session, '设置已保存，但刷新失败；请重新读取后核对。');
   if (loaded) toast('通知设置已保存');
  } catch (error) {
   if (!current(generation, session)) return;
   if (error.status === 409) {
    notice('配置已变化，操作未执行；请重新读取后核对。');
   } else if (error.status >= 400 && error.status < 500) {
    const message = webhookDialogEl.querySelector('.error');
    if (message) message.textContent = error.message;
    controlsBusy(false);
   } else {
    notice('操作结果未确认，可能已经生效；请重新读取并核对后再操作。');
   }
  } finally {
   clearTimeout(timer);
   if (pending === request) pending = null;
  }
 }
 function initialize() {
  if (webhookDialogEl) return;
  webhookDialogEl = document.createElement('dialog');
  webhookDialogEl.id = 'webhook-dialog';
  document.body.appendChild(webhookDialogEl);
  webhookDialogEl.addEventListener('close', () => {
   if (!webhookDialogEl.open) { ++webhookDialogGeneration; model = null; pending = null; webhookDialogEl.innerHTML = ''; scheduleRefresh(0); }
  });
  webhookDialogEl.addEventListener('cancel', event => { if (pending) event.preventDefault(); });
  webhookDialogEl.addEventListener('click', event => {
   const button = event.target.closest('[data-webhook-action]');
   if (!button || pending) return;
   const action = button.dataset.webhookAction;
   if (action === 'close') { webhookDialogEl.close(); return; }
   if (action === 'reload') { read(webhookDialogGeneration, authGeneration); return; }
   if (!model) return;
   if (action === 'back') { list(); return; }
   if (action === 'new') { if (model.targets.length < model.limit) edit(null); return; }
   const target = model.targets.find(item => item.id === button.dataset.id);
   if (!target) return;
   if (action === 'edit') { edit(target); return; }
   if (action === 'delete' && !confirm(`删除通知目标「${target.name}」？`)) return;
   if (['enable', 'disable', 'delete'].includes(action)) mutate({revision:model.revision, action, id:target.id});
  });
  webhookDialogEl.addEventListener('submit', event => {
   if (!event.target.matches('#webhook-target-form')) return;
   event.preventDefault();
   if (pending || !model) return;
   const form = event.target, fields = form.elements;
   mutate({revision:Number(form.dataset.revision), action:'save', id:form.dataset.id, name:fields.targetName.value, enabled:fields.enabled.checked, format:fields.format.value, url:fields.url.value, clearURL:fields.clearURL.checked, signingSecret:fields.signingSecret.value, clearSigningSecret:fields.clearSigningSecret.checked, jsonTemplate:fields.jsonTemplate.value, clearJSONTemplate:fields.clearJSONTemplate.checked, jsonTemplateEnabled:fields.jsonTemplateEnabled.checked, onStatus:fields.onStatus.checked, onConfig:fields.onConfig.checked, onLoginLimit:fields.onLoginLimit.checked, onUpdate:fields.onUpdate.checked});
  });
 }
 async function open() {
  if (!data || data.identity?.role !== 'admin') return;
  initialize();
  if (webhookDialogEl.open) return;
  const generation = ++webhookDialogGeneration, session = authGeneration;
  stopRefreshPolling();
  webhookDialogEl.showModal();
  await read(generation, session);
 }
 return {open};
})();
