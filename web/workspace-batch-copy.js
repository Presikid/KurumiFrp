'use strict';

(() => {
 const K = window.Kurumi, esc = K.esc;
 async function timed(path, body, ms) {
  const controller = new AbortController(), timer = setTimeout(() => controller.abort(), ms);
  try { return await K.api(path, body, {signal:controller.signal}); }
  finally { clearTimeout(timer); }
 }
 const unknown = error => error.name === 'AbortError' || !error.status || error.status >= 500;

 async function openBatchCopy(root, button) {
  const epoch = K.epoch();
  const current = () => K.epoch() === epoch && K.isCurrent('tunnels',root) && K.state()?.identity?.role === 'admin';
  const preselected = new Set([...root.querySelectorAll('[data-select]:checked')].map(box => box.dataset.select));
  button.disabled = true;
  try {
   const data = await timed('state', undefined, 15000);
   if (!current()) return;
   if (data.identity?.role !== 'admin' || !Number.isSafeInteger(data.revision) || !Array.isArray(data.state?.instances) || !Array.isArray(data.state?.tunnels)) throw new Error('无法读取当前草稿，请刷新后重试。');
   const clients = data.state.instances.filter(item => item.kind === 'frpc');
   const names = new Map(clients.map(item => [item.id,item.name]));
   const rules = data.state.tunnels.filter(rule => names.has(rule.instanceId));
   if (!clients.length || !rules.length) throw new Error('需要至少一个客户端及一条规则。');
   let pendingUnknown = false, marker = null;
   const formCurrent = form => current() && marker?.isConnected && form.contains(marker) && document.querySelector('#dialog')?.open;
   const rows = rules.map(rule => {
    const search = [rule.name,rule.type,names.get(rule.instanceId)].join(' ').toLocaleLowerCase();
    return `<label class="form-check" data-copy-row data-search="${esc(search)}"><input type="checkbox" name="copyRule" value="${esc(rule.id)}" ${preselected.has(rule.id) ? 'checked' : ''}>${esc(rule.name)} · ${esc(rule.type)} · ${esc(names.get(rule.instanceId))}</label>`;
   }).join('');
   K.openDialog({
    html:`<span data-copy-marker hidden></span><h2>批量复制规则</h2><p class="small-note">根据最新草稿选择规则和目标客户端。副本自动重命名并默认停用；不会启动或重启实例。含 XTCP 回退的规则须同时选择完整回退链，复制后回退指向副本。</p><label>目标客户端<select name="target" required>${clients.map(item => `<option value="${esc(item.id)}">${esc(item.name)}</option>`).join('')}</select></label><label>筛选规则<input type="search" data-copy-search placeholder="规则、类型或客户端"></label><div class="actions"><button type="button" data-copy-visible="select">选择可见</button><button type="button" data-copy-visible="clear">取消可见</button></div><fieldset><legend>选择要复制的规则</legend>${rows}</fieldset>`,
    submitLabel:'复制所选规则',
    onReady(form) { marker = form.querySelector('[data-copy-marker]'); },
    onInput(event,form) {
     if (!event.target.matches('[data-copy-search]')) return;
     const query = event.target.value.toLocaleLowerCase();
     form.querySelectorAll('[data-copy-row]').forEach(row => { row.hidden = !row.dataset.search.includes(query); });
    },
    onClick(event,form) {
     const control = event.target.closest('[data-copy-visible]');
     if (!control) return;
     form.querySelectorAll('[data-copy-row]').forEach(row => { if (!row.hidden) row.querySelector('[name="copyRule"]').checked = control.dataset.copyVisible === 'select'; });
    },
    async onSubmit(form) {
     if (!formCurrent(form)) throw new Error('页面或会话已变化，请重新打开批量复制。');
     if (pendingUnknown) throw new Error('上次复制结果未确认，请关闭窗口并刷新草稿后核对。');
     const ids = [...form.querySelectorAll('[name="copyRule"]:checked')].map(box => box.value);
     const target = form.elements.target.value;
     if (!ids.length) throw new Error('请至少选择一条规则。');
     const destination = clients.find(item => item.id === target);
     if (!destination) throw new Error('目标客户端无效，请重新选择。');
     if (!confirm(`复制 ${ids.length} 条规则到「${destination.name}」？所有副本默认停用，正在运行的配置不会改变。`)) return false;
     let result;
     try { result = await timed('duplicate',{revision:data.revision,kind:'tunnels',ids,targetInstanceId:target},30000); }
     catch (error) {
      if (unknown(error)) { pendingUnknown = true; throw new Error('复制结果未确认，请关闭窗口并刷新草稿后核对，勿重复提交。'); }
      throw error;
     }
     if (!result?.ok || result.rulesCopied !== ids.length || !Array.isArray(result.ids) || result.ids.length !== ids.length) {
      pendingUnknown = true; throw new Error('复制结果无法确认，请关闭窗口并刷新草稿后核对。');
     }
     if (formCurrent(form)) {
      try { await K.refresh(); K.toast(`已复制 ${ids.length} 条停用规则，请核对配置。`); }
      catch (_) { if (K.state()) K.toast('复制已完成，但刷新失败；请手动刷新核对。'); }
     }
    }
   });
  } catch (error) {
   if (current()) K.toast(error.name === 'AbortError' ? '读取草稿超时，请重试。' : error.message);
  } finally { if (button.isConnected) button.disabled = false; }
 }

 window.KurumiBatchCopy = Object.freeze({open:openBatchCopy});
})();
