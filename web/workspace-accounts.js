'use strict';

(() => {
 const K = window.Kurumi, esc = K.esc;
 const roles = {admin:'管理员', operator:'操作员', viewer:'查看者'};
 const actions = [['start','启动'], ['stop','停止'], ['restart','重启'], ['validate','配置校验']];
 const grantKey = grant => `${grant.nodeId}:${grant.instanceId}:${grant.binding}`;
 const stampOK = value => /^[a-f0-9]{64}$/.test(value || '');
 const idOK = value => /^[a-f0-9]{16}$/.test(value || '');
 const textTime = value => {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? String(value || '') : date.toLocaleString('zh-CN');
 };
 const uncertain = error => error.name === 'AbortError' || !error.status || error.status >= 500;
 async function request(path, body, ms = 20000) {
  const controller = new AbortController(), timer = setTimeout(() => controller.abort(), ms);
  try { return await K.api(path, body, {signal:controller.signal}); }
  finally { clearTimeout(timer); }
 }

 K.register('accounts', {
  render(data) {
   if (data.identity?.role !== 'admin') return '<section class="panel"><div class="panel-content">此页面仅供管理员使用。</div></section>';
   return `<header class="page-head"><div><p class="eyebrow">ACCESS / ACCOUNTS</p><h1>账户管理</h1><p>为本地账户分配角色和实例权限。</p></div><div class="actions"><button type="button" data-account="reload">刷新账户</button><button class="primary" type="button" data-account="new" disabled>＋ 新建账户</button></div></header>
    <section class="panel"><div class="panel-content"><p class="small-note">管理员拥有全部权限。删除实例或替换配置会撤销相关授权，请在操作后核对账户。账户配置独立保存，不包含在配置导出或加密备份中。</p><div data-account-list>正在读取账户…</div></div></section>
    <section class="panel"><div class="panel-content"><div class="actions"><h2>登录记录</h2><button type="button" data-account="history">刷新记录</button><button type="button" class="danger" data-account="clear-history" disabled>清空记录</button></div><p class="small-note">来源为工作台收到连接的对端地址；通过 SSH 转发时可能显示本机地址。重复失败会限频记录。</p><div data-login-history>正在读取记录…</div></div></section>`;
  },
  mount(root, data) {
   if (data.identity?.role !== 'admin') return;
   const epoch = K.epoch(), list = root.querySelector('[data-account-list]'), history = root.querySelector('[data-login-history]');
   const createButton = root.querySelector('[data-account="new"]'), clearButton = root.querySelector('[data-account="clear-history"]');
   const instances = Array.isArray(data.state?.instances) ? data.state.instances : [];
   const names = new Map(instances.map(item => [item.id, item.name]));
   let model = null, accountSerial = 0, historySerial = 0, historySaved = false, historyUncertain = false, deleting = false, clearing = false;
   const current = () => K.epoch() === epoch && K.isCurrent('accounts', root) && K.state()?.identity?.role === 'admin';
   const ownAccount = user => !!data.identity?.account && String(data.identity.name || '').toLowerCase() === String(user.username || '').toLowerCase();
   const requireCurrent = () => {
    if (!current()) throw new Error('页面或会话已变化，请重新打开账户管理。');
    if (K.state()?.revision !== data.revision) throw new Error('实例配置已变化，请刷新后重试。');
   };
   const renderList = () => {
    if (!current() || !model) return;
    if (!model.accounts.length) { list.innerHTML = '<p>尚未创建本地账户。管理员仍可使用主密码登录。</p>'; return; }
    list.innerHTML = `<div class="table-wrap"><table><thead><tr><th>用户名</th><th>角色</th><th>状态</th><th>授权</th><th>操作</th></tr></thead><tbody>${model.accounts.map(user => {
     const self = ownAccount(user), local = Array.isArray(user.instances) ? user.instances : [], remote = Array.isArray(user.remoteInstances) ? user.remoteInstances : [];
     const status = !user.enabled ? '已停用' : user.expired ? '已到期' : '启用';
     const grants = user.role === 'admin' ? '全部实例' : `${local.length ? local.map(id => esc(names.get(id) || `实例 ${id}`)).join('、') : '无本机实例'} · 远程 ${remote.length} 项`;
     return `<tr><td>${esc(user.username)}${self ? ' <span class="pill">当前账户</span>' : ''}</td><td>${esc(roles[user.role] || user.role)}</td><td>${status}${user.expiresAt ? `<br><small>到期 ${esc(user.expiresAt)} (UTC)</small>` : ''}</td><td>${grants}</td><td>${self ? '<span class="small-note">请使用“修改登录密码”</span>' : `<div class="actions"><button type="button" data-account="edit" data-id="${esc(user.id)}">编辑</button><button type="button" class="danger" data-account="delete" data-id="${esc(user.id)}">删除</button></div>`}</td></tr>`;
    }).join('')}</tbody></table></div>`;
   };
   const loadAccounts = async () => {
    const serial = ++accountSerial;
    model = null; createButton.disabled = true;
    list.textContent = '正在读取账户…';
    try {
     const value = await request('accounts');
     if (!current() || serial !== accountSerial) return;
     if (!Array.isArray(value.accounts) || !Number.isSafeInteger(value.revision) || value.revision < 1) throw new Error('账户列表格式无效。');
     model = value; createButton.disabled = false; renderList();
    } catch (error) {
     if (current() && serial === accountSerial) list.textContent = '读取账户失败：' + (error.name === 'AbortError' ? '请求超时，请重试。' : error.message);
    }
   };
   const loadHistory = async () => {
    const serial = ++historySerial;
    historySaved = false; clearButton.disabled = true; history.textContent = '正在读取记录…';
    try {
     const value = await request('login-history');
     if (!current() || serial !== historySerial) return;
     if (!Array.isArray(value.entries)) throw new Error('登录记录格式无效。');
     historySaved = value.saved === true;
     clearButton.disabled = !historySaved || historyUncertain;
     const warning = historySaved ? '' : '<p class="error">记录文件不可读取或保存失败，原文件已保留。</p>';
     const skipped = Number.isSafeInteger(value.suppressed) && value.suppressed > 0 ? `<p class="small-note">本次运行有 ${value.suppressed} 次重复失败未逐条记录。</p>` : '';
     const rows = value.entries.length ? `<div class="table-wrap"><table><thead><tr><th>时间</th><th>账户</th><th>连接来源</th><th>结果</th></tr></thead><tbody>${value.entries.map(entry => `<tr><td>${esc(textTime(entry.at))}</td><td>${esc(entry.account)}</td><td>${esc(entry.peer)}</td><td>${entry.success ? '成功' : '未成功'}</td></tr>`).join('')}</tbody></table></div>` : '<p>暂无登录记录。</p>';
     history.innerHTML = warning + skipped + rows;
    } catch (error) {
     if (current() && serial === historySerial) history.textContent = '读取登录记录失败：' + (error.name === 'AbortError' ? '请求超时，请重试。' : error.message);
    }
   };

   const editAccount = user => {
    requireCurrent();
    if (!model) throw new Error('请先读取账户列表。');
    if (user && ownAccount(user)) throw new Error('请使用页面顶部的修改密码功能管理当前账户。');
    const source = model, revision = source.revision;
    const visible = new Set(user?.instances || []), controls = new Set(user?.controlInstances || []);
    const allowed = new Set(user?.allowedActions || []), logs = new Set(user?.logInstances || []);
    const choices = new Map((user?.remoteInstances || []).map(grant => [grantKey(grant), {grant:{...grant}, selected:true, existing:true, label:`节点 ${grant.nodeId} / 实例 ${grant.instanceId}（已有授权）`} ]));
    let saveUncertain = false, reading = false, marker = null;
    const formCurrent = form => current() && marker?.isConnected && form.contains(marker) && document.querySelector('#dialog')?.open;
    const rows = (name, selected) => instances.length ? instances.map(item => `<label class="form-check"><input type="checkbox" name="${name}" value="${esc(item.id)}" ${selected.has(item.id) ? 'checked' : ''}>${esc(item.name)} · ${esc(item.kind)}</label>`).join('') : '<p class="small-note">当前没有本机实例。</p>';
    const remoteHTML = () => choices.size ? [...choices].map(([key, choice]) => `<div class="card"><label class="form-check"><input type="checkbox" data-remote-view="${esc(key)}" ${choice.selected ? 'checked' : ''}>${esc(choice.label)}</label>${choice.grant.control ? '<span class="small-note">已有控制授权</span>' : ''}<label class="form-check" data-remote-control-label><input type="checkbox" data-remote-control="${esc(key)}" ${choice.grant.control ? 'checked' : ''} ${choice.selected ? '' : 'disabled'}>允许启停和重启</label><small>绑定 ${esc(choice.grant.binding)}</small></div>`).join('') : '<p class="small-note">尚无远程实例授权。</p>';
    const updateForm = form => {
     const role = form.elements.role.value, isAdmin = role === 'admin';
     for (const target of form.querySelectorAll('[data-restricted]')) target.hidden = isAdmin;
     for (const target of form.querySelectorAll('[data-operator]')) target.hidden = role !== 'operator';
     const logSection = form.querySelector('[data-account-logs]');
     logSection.hidden = isAdmin || !form.elements.allowLogs.checked;
     const enabled = new Set([...form.querySelectorAll('[name="instance"]:checked')].map(box => box.value));
     form.querySelectorAll('[name="controlInstance"], [name="logInstance"]').forEach(box => { box.disabled = !enabled.has(box.value); });
     form.querySelectorAll('[data-remote-control-label]').forEach(label => { label.hidden = role !== 'operator'; });
    };
    const paintRemote = form => { form.querySelector('[data-remote-choices]').innerHTML = remoteHTML(); updateForm(form); };
    K.openDialog({
     html:`<span data-account-editor-marker hidden></span><h2>${user ? '编辑账户' : '新建账户'}</h2><p class="small-note">用户名由 3–32 位英文字母、数字、点、下划线或连字符组成；开头须为字母或数字。新账户密码须为 12–1024 字节的有效文本，不能含控制字符。</p><div class="form-grid">
      <label>用户名<input name="username" required pattern="[A-Za-z0-9][A-Za-z0-9_.-]{2,31}" maxlength="32" value="${esc(user?.username || '')}" ${user ? 'readonly' : ''} autocomplete="off"></label>
      <label>角色<select name="role"><option value="viewer" ${!user || user.role === 'viewer' ? 'selected' : ''}>查看者</option><option value="operator" ${user?.role === 'operator' ? 'selected' : ''}>操作员</option><option value="admin" ${user?.role === 'admin' ? 'selected' : ''}>管理员（全部权限）</option></select></label>
      <label class="span-2">${user ? '重置密码（留空保留原密码）' : '账户密码（至少 12 字节）'}<input name="password" type="password" autocomplete="new-password" maxlength="1024" ${user ? '' : 'required'}></label>
      <label class="span-2">账户到期时间（UTC，留空不限）<input name="expiresAt" type="datetime-local" step="1" value="${esc(user?.expiresAt ? user.expiresAt.slice(0,19) : '')}"><small>这里填写 UTC 时间。到期后不能登录，现有会话也会失效。</small></label>
      <label class="form-check span-2"><input type="checkbox" name="enabled" ${!user || user.enabled ? 'checked' : ''}>启用账户</label>
      <fieldset class="span-2" data-restricted><legend>可查看的本机实例</legend>${rows('instance',visible)}<p class="small-note">未选择表示不能查看任何实例。</p></fieldset>
      <fieldset class="span-2" data-restricted><legend>实例数据查看权限</legend><label class="form-check"><input type="checkbox" name="viewConfig" ${user?.readPermissions?.config !== false ? 'checked' : ''}>配置详情和规则</label><label class="form-check"><input type="checkbox" name="viewTraffic" ${user?.readPermissions?.traffic !== false ? 'checked' : ''}>流量和流量历史</label></fieldset>
      <fieldset class="span-2" data-operator><legend>可操作的本机实例</legend>${rows('controlInstance',controls)}<p class="small-note">操作授权必须同时拥有该实例的查看授权。</p></fieldset>
      <fieldset class="span-2" data-operator><legend>允许的操作</legend>${actions.map(([id,label]) => `<label class="form-check"><input type="checkbox" name="action" value="${id}" ${allowed.has(id) ? 'checked' : ''}>${label}</label>`).join('')}<p class="small-note">对获授权的本机和远程实例生效；配置校验仅适用于本机。</p></fieldset>
      <div class="span-2" data-restricted><label class="form-check"><input type="checkbox" name="allowLogs" ${user?.allowLogs ? 'checked' : ''}>允许查看日志</label><p class="small-note">日志可能包含敏感信息。</p></div>
      <fieldset class="span-2" data-account-logs><legend>可查看日志的本机实例</legend>${rows('logInstance',logs)}<p class="small-note">日志授权必须属于可查看的实例。</p></fieldset>
      <fieldset class="span-2" data-restricted><legend>远程实例授权</legend><p class="small-note">已有授权会保留。可读取已确认地址的节点，选择实例和身份绑定；目标替换实例后需重新授权。查看者不保留控制权限。转为管理员前请先明确移除已有远程授权。</p><button type="button" data-load-remote>读取远程实例</button><p class="small-note" data-remote-status></p><div data-remote-choices></div></fieldset>
     </div>`,
     submitLabel:'保存账户',
     onReady(form) { marker = form.querySelector('[data-account-editor-marker]'); paintRemote(form); },
     onChange(event, form) {
      const box = event.target;
      if (box.matches('[data-remote-view]')) {
       const choice = choices.get(box.dataset.remoteView);
       if (choice) {
        choice.selected = box.checked;
        if (box.checked) for (const [key, other] of choices) if (key !== box.dataset.remoteView && other.grant.nodeId === choice.grant.nodeId && other.grant.instanceId === choice.grant.instanceId) other.selected = false;
        paintRemote(form);
       }
      } else if (box.matches('[data-remote-control]')) {
       const choice = choices.get(box.dataset.remoteControl);
       if (choice) choice.grant.control = box.checked;
      } else updateForm(form);
     },
     async onClick(event, form) {
      const button = event.target.closest('[data-load-remote]');
      if (!button || reading) return;
      requireCurrent();
      if (!formCurrent(form)) return;
      reading = true; button.disabled = true;
      const output = form.querySelector('[data-remote-status]'); output.textContent = '正在读取节点…';
      try {
       const found = await request('nodes');
       requireCurrent();
       if (!formCurrent(form)) return;
       if (!Array.isArray(found.nodes) || found.revision !== data.revision) throw new Error('节点或配置已变化，请刷新后重试。');
       const nodes = found.nodes.filter(node => idOK(node.id) && stampOK(node.stamp) && Array.isArray(node.addresses) && node.addresses.length);
       let failed = found.nodes.length - nodes.length;
       for (let index = 0; index < nodes.length; index += 4) {
        const batch = nodes.slice(index,index + 4);
        const results = await Promise.allSettled(batch.map(node => request('nodes/check',{id:node.id,stamp:node.stamp},10000)));
        requireCurrent();
        if (!formCurrent(form)) return;
        results.forEach((result,i) => {
         const node = batch[i], value = result.status === 'fulfilled' ? result.value : null;
         if (!value || value.id !== node.id || value.stamp !== node.stamp || !Array.isArray(value.summary?.instances)) { failed++; return; }
         for (const instance of value.summary.instances) {
          if (!idOK(instance.id) || !stampOK(instance.binding)) continue;
          const grant = {nodeId:node.id,instanceId:instance.id,binding:instance.binding,control:false}, key = grantKey(grant);
          const previous = choices.get(key);
          choices.set(key,{grant:previous ? previous.grant : grant, selected:previous?.selected || false, existing:previous?.existing || false, stamp:node.stamp, label:`${node.name} / ${instance.name} · ${instance.kind}`});
         }
        });
       }
       if (formCurrent(form)) { paintRemote(form); output.textContent = failed ? `${failed} 个节点未能读取；已有授权已保留。` : '远程实例已读取；旧版目标可能没有可用身份绑定。'; }
      } catch (error) {
       if (formCurrent(form)) output.textContent = error.name === 'AbortError' ? '读取超时，请重试。' : error.message;
      } finally { reading = false; if (button.isConnected) button.disabled = false; }
     },
     async onSubmit(form) {
      requireCurrent();
      if (!formCurrent(form)) throw new Error('编辑窗口已变化，请重新打开。');
      if (reading) throw new Error('请等待节点读取完成。');
      if (saveUncertain) throw new Error('上次保存结果未确认，请关闭窗口并刷新账户后核对。');
      if (model !== source || model.revision !== revision) throw new Error('账户列表已变化，请重新打开编辑窗口。');
      const role = form.elements.role.value, password = form.elements.password.value;
      if ((!user || password) && (new TextEncoder().encode(password).length < 12 || new TextEncoder().encode(password).length > 1024 || /[\x00-\x1f\x7f]/.test(password))) throw new Error('账户密码须为 12–1024 字节且不能包含控制字符。');
      const chosen = [...choices.values()].filter(choice => choice.selected);
      if (role === 'admin' && chosen.length) throw new Error('请先在远程实例授权中明确取消所有授权，再改为管理员。');
      if (role === 'viewer' && chosen.some(choice => choice.grant.control) && !confirm('改为查看者会撤销这些远程实例的控制权限，保留查看权限。继续？')) return false;
      const newGrants = chosen.filter(choice => !choice.existing);
      if (newGrants.length) {
       const nodes = await request('nodes');
       requireCurrent();
       if (!formCurrent(form)) throw new Error('编辑窗口已变化，请重新打开。');
       if (!Array.isArray(nodes.nodes) || nodes.revision !== data.revision || newGrants.some(choice => !nodes.nodes.some(node => node.id === choice.grant.nodeId && node.stamp === choice.stamp && node.addresses?.length))) throw new Error('远程节点设置已变化，请重新读取实例。');
      }
      const select = name => [...form.querySelectorAll(`[name="${name}"]:checked`)].filter(box => !box.disabled).map(box => box.value);
      const expiry = form.elements.expiresAt.value;
      const payload = {action:'save',revision,id:user?.id || '',username:form.elements.username.value,role,enabled:form.elements.enabled.checked,expiresAt:expiry ? (expiry.length === 16 ? expiry + ':00Z' : expiry + 'Z') : '',password,
       instances:role === 'admin' ? [] : select('instance'),controlInstances:role === 'operator' ? select('controlInstance') : [],allowedActions:role === 'operator' ? select('action') : [],
       readPermissions:role === 'admin' ? null : {config:form.elements.viewConfig.checked,traffic:form.elements.viewTraffic.checked},allowLogs:role !== 'admin' && form.elements.allowLogs.checked,
       logInstances:role !== 'admin' && form.elements.allowLogs.checked ? select('logInstance') : [],remoteInstances:role === 'admin' ? [] : chosen.map(choice => ({...choice.grant,control:role === 'operator' && !!choice.grant.control}))};
      if (model !== source || model.revision !== revision) throw new Error('账户列表已变化，请重新打开编辑窗口。');
      try { await request('accounts',payload,30000); }
      catch (error) { if (uncertain(error)) { saveUncertain = true; throw new Error('保存结果未确认，请关闭窗口并刷新账户后核对，勿重复提交。'); } throw error; }
      if (formCurrent(form)) { form.elements.password.value = ''; K.toast('账户已保存。'); await loadAccounts(); }
     }
    });
   };

   root.addEventListener('click', async event => {
    const button = event.target.closest('[data-account]');
    if (!button || !root.contains(button) || button.disabled) return;
    const action = button.dataset.account;
    try {
     if (action === 'reload') { await loadAccounts(); return; }
     if (action === 'history') { await loadHistory(); return; }
     if (action === 'new') { editAccount(null); return; }
     if (action === 'edit') {
      const user = model?.accounts.find(item => item.id === button.dataset.id);
      if (!user) throw new Error('账户已变化，请刷新。');
      editAccount(user); return;
     }
     if (action === 'delete') {
      requireCurrent();
      const source = model, user = source?.accounts.find(item => item.id === button.dataset.id);
      if (!user || ownAccount(user)) throw new Error('不能删除当前账户，或账户已变化。');
      if (deleting || !confirm(`删除账户「${user.username}」并撤销其会话？`)) return;
      deleting = true; button.disabled = true;
      try { await request('accounts',{action:'delete',revision:source.revision,id:user.id},30000); if (current()) { K.toast('账户已删除。'); await loadAccounts(); } }
      catch (error) { if (current()) { K.toast(uncertain(error) ? '删除结果未确认，请刷新账户后核对。' : error.message); await loadAccounts(); } }
      finally { deleting = false; if (button.isConnected) button.disabled = false; }
      return;
     }
     if (action === 'clear-history') {
      if (!historySaved || clearing || !confirm('清空本机保存的登录记录？')) return;
      clearing = true; clearButton.disabled = true;
      try { await request('login-history',{action:'clear'}); if (current()) { K.toast('登录记录已清空。'); await loadHistory(); } }
      catch (error) { if (current()) { historyUncertain = uncertain(error); K.toast(historyUncertain ? '清空结果未确认，请核对记录后重新打开页面。' : error.message); await loadHistory(); } }
      finally { clearing = false; if (current()) clearButton.disabled = !historySaved || historyUncertain; }
     }
    } catch (error) { if (current()) K.toast(error.message); }
   });
   void loadAccounts(); void loadHistory();
  }
 });
})();
