'use strict';

// The host supplies session and state operations; this dialog owns only its
// transient file, preview and in-flight request.
window.KurumiTOMLImport = (() => {
 let dialog = null, host = null, pending = null, version = 0, importing = false;

 function clear() {
  pending = null;
  ++version;
  const button = dialog?.querySelector('[data-toml-import]');
  if (button) button.disabled = true;
  const output = dialog?.querySelector('[data-toml-result]');
  if (output) output.textContent = '更改后请重新预览。';
 }

 function setup() {
  dialog = document.createElement('dialog');
  document.body.appendChild(dialog);
  dialog.addEventListener('close', () => {
   clear();
   importing = false;
   dialog.innerHTML = '';
   host?.schedule();
  });
  dialog.addEventListener('cancel', event => { if (importing) event.preventDefault(); });
  dialog.addEventListener('input', clear);
  dialog.addEventListener('change', clear);
  dialog.addEventListener('click', async event => {
   if (event.target.closest('[data-toml-close]')) {
    if (!importing) dialog.close();
    return;
   }
   const previewButton = event.target.closest('[data-toml-preview]');
   const importButton = event.target.closest('[data-toml-import]');
   if (!previewButton && !importButton) return;
   const button = previewButton || importButton;
   const output = dialog.querySelector('[data-toml-result]');
   const generation = host.generation(), formVersion = version;
   let operation = null;
   button.disabled = true;
   try {
    if (previewButton) {
     pending = null;
     dialog.querySelector('[data-toml-import]').disabled = true;
     const file = dialog.querySelector('[name="file"]').files[0];
     if (!file) throw new Error('请选择 TOML 文件');
     if (file.size > 256 * 1024) throw new Error('TOML 文件超过 256 KiB');
     let content;
     try { content = new TextDecoder('utf-8', {fatal:true}).decode(await file.arrayBuffer()); }
     catch (_) { throw new Error('TOML 文件必须是 UTF-8'); }
     if (generation !== host.generation() || formVersion !== version || !dialog.open) return;
     const state = host.state();
     if (!state) throw new Error('工作台状态已变化，请重新登录');
     const request = {revision:state.revision, kind:dialog.querySelector('[name="kind"]').value,
      name:dialog.querySelector('[name="name"]').value.trim(), content};
     const result = await host.api('import/toml/preview', request);
     if (generation !== host.generation() || formVersion !== version || !dialog.open) return;
     const instance = result.instance || {}, lines = [];
     if (instance.kind) {
      lines.push(`实例：${instance.name} · ${instance.kind}`, `地址：${instance.host}:${instance.port}`,
       `规则：${(result.tunnels || []).length} 条`);
      for (const rule of result.tunnels || []) {
       let detail = rule.unixSocketPath ? `Unix: ${rule.unixSocketPath}` : `${rule.localHost}:${rule.localPort}`;
       if (['tcp', 'udp'].includes(rule.type)) detail += ` → ${rule.remotePort}`;
       if (rule.type.endsWith('-visitor')) detail += ` → ${rule.serverName}`;
       lines.push(`  ${rule.name} · ${rule.type} · ${rule.enabled ? '启用' : '停用'} · ${detail}`);
      }
     }
     if (result.issues?.length) lines.push('', '以下问题阻止导入：', ...result.issues.map(issue => `• ${issue.path}：${issue.message}`));
     if (result.canImport) {
      if (!host.state() || host.state().revision !== request.revision) lines.push('', '配置已更新，请重新预览。');
      else {
       pending = {...request, previewHash:result.previewHash};
       dialog.querySelector('[data-toml-import]').disabled = false;
       lines.push('', '可以作为新草稿导入。');
      }
     }
     output.textContent = lines.join('\n');
    } else {
     if (!pending) throw new Error('请重新预览配置');
     operation = {kind:'import', generation};
     host.begin(operation);
     importing = true;
     dialog.querySelectorAll('input,select,button').forEach(input => { input.disabled = true; });
     const result = await host.transfer('import/toml', pending);
     host.finish(operation, 'complete');
     operation = null;
     if (generation !== host.generation() || !dialog.open) return;
     importing = false;
     dialog.close();
     host.toast(`已导入实例和 ${result.rulesImported} 条规则；草稿尚未应用`);
    }
   } catch (err) {
    if (operation) host.finish(operation, 'uncertain', err.message || '导入请求未完成');
    if (generation === host.generation() && dialog.open) {
     output.textContent = err.message || '导入失败';
     pending = null;
     dialog.querySelector('[data-toml-import]').disabled = true;
    }
   } finally {
    if (generation === host.generation() && dialog.open) {
     if (previewButton) previewButton.disabled = false;
     if (importButton) {
      importing = false;
      dialog.querySelectorAll('input,select,button').forEach(input => { input.disabled = false; });
      importButton.disabled = !pending;
     }
    }
   }
  });
 }

 function open(deps) {
  if (dialog?.open) return;
  const blocked = deps.blocked();
  if (blocked) { deps.toast(blocked); return; }
  host = deps;
  if (!dialog) setup();
  pending = null;
  ++version;
  dialog.innerHTML = '<h2>导入 frp TOML</h2><p class="muted">添加一个客户端或服务端草稿，不会启动实例或替换现有配置。</p><div class="form-grid"><label class="span-2">TOML 文件（UTF-8，最多 256 KiB）<input type="file" name="file" accept=".toml,text/plain"></label><label>配置类型<select name="kind"><option value="frpc">frpc · 客户端</option><option value="frps">frps · 服务端</option></select></label><label>新实例名称<input name="name" maxlength="80" value="导入的 frp 实例"></label></div><p class="small-note">模板、includes、插件及无法保留的设置会阻止导入；预览不会显示认证内容。</p><pre data-toml-result role="status">选择文件并预览。</pre><div class="dialog-actions"><button type="button" data-toml-close>取消</button><button type="button" data-toml-preview>预览</button><button type="button" class="primary" data-toml-import disabled>导入草稿</button></div>';
  host.stop();
  dialog.showModal();
 }

 return Object.freeze({open, isOpen:() => !!dialog?.open, close:() => { if (dialog?.open) dialog.close(); }});
})();
