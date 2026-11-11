'use strict';

// Administration screens for the rebuilt workspace. All mutations use the
// revisions returned by the owning API; network failures are never retried.
(() => {
 const K = window.Kurumi;
 const {api, esc, toast, openDialog} = K;
 const noticePrefix = 'kurumi-node-control-';
 const timeout = async (path, body, ms = 15000, options = {}) => {
  const controller = new AbortController(), timer = setTimeout(() => controller.abort(), ms);
  try { return await api(path, body, {...options, signal:controller.signal}); }
  finally { clearTimeout(timer); }
 };
 const errorText = error => error.name === 'AbortError' ? '请求超时，请先刷新核对结果。' : error.message;
 const nodeNotices = () => {
  try {
   const result = [];
   for (let i = 0; i < localStorage.length; i++) {
    const key = localStorage.key(i);
    if (!key?.startsWith(noticePrefix)) continue;
    let item = null;
    try { item = JSON.parse(localStorage.getItem(key)); } catch (_) {}
    result.push({key, item});
   }
   return result;
  } catch (_) { return null; }
 };
 const beginNodeNotice = (node, instance, action) => {
  const key = noticePrefix + Array.from(crypto.getRandomValues(new Uint8Array(16)), n => n.toString(16).padStart(2, '0')).join('');
  const value = JSON.stringify({nodeId:node.id, instanceId:instance.id, action});
  try {
   localStorage.setItem(key, value);
   if (localStorage.getItem(key) !== value) throw new Error('storage unavailable');
   return key;
  } catch (_) { throw new Error('无法保存远程操作核对记录，请检查浏览器存储。'); }
 };

 K.register('nodes', {
  render(data) {
   const admin = data.identity?.role === 'admin';
   return `<div class="page-head"><div><p class="eyebrow">NETWORK / NODES</p><h1>节点管理</h1><p>连接到其他工作台，查看和管理授权实例。</p></div>${admin ? '<button class="primary" data-node="new">＋ 添加节点</button>' : ''}</div>
    <div data-node-notices></div><section class="panel"><div class="panel-content"><div class="actions"><button data-node="reload">刷新节点</button><button data-node="auto" aria-pressed="false">开启自动检查</button></div><p class="small-note">自动检查只在此页面打开时运行；节点后台检查需单独启用。</p><div data-node-list>正在读取节点…</div></div></section>
    ${admin ? `<section class="panel"><div class="panel-content"><h2>共享本机</h2><p class="small-note">只读与控制授权独立。新授权只显示一次；远程访问需自行配置可信 HTTPS 入口。</p><div data-node-share>正在读取…</div></div></section>` : ''}`;
  },
  mount(root, data) {
   const admin = data.identity?.role === 'admin', list = root.querySelector('[data-node-list]'), share = root.querySelector('[data-node-share]');
   let model = null, busy = false, auto = false, autoTimer = null, autoGeneration = 0;
   const results = new Map();
   const current = () => K.isCurrent('nodes', root);
   const paintNotices = () => {
    const host = root.querySelector('[data-node-notices]'), notices = nodeNotices();
    if (!host) return;
    if (!notices) { host.innerHTML = '<p class="notice">无法读取远程操作核对记录，暂不能提交远程操作。</p>'; return; }
    host.innerHTML = notices.map(n => `<div class="notice">远程操作结果未确认：${esc(n.item?.nodeId || '未知节点')} / ${esc(n.item?.instanceId || '未知实例')}。请先核对目标状态。 <button data-node="ack" data-key="${esc(n.key)}">核对后解除</button></div>`).join('');
   };
   const paint = () => {
    if (!current() || !model) return;
    paintNotices();
    list.innerHTML = model.nodes.length ? `<div class="cards">${model.nodes.map(node => {
     const addresses = Array.isArray(node.addresses) ? node.addresses : [], ready = admin ? addresses.length > 0 : node.ready;
     const observation = node.monitorStatus, state = results.get(node.id);
     const monitor = node.monitorEnabled ? `后台检查：${({available:'可用',pending:'等待复查',unavailable:'不可用'})[observation?.state] || '等待结果'}` : '后台检查未启用';
     const instances = Array.isArray(state?.summary?.instances) ? state.summary.instances : [];
     const detail = state?.error ? `<p class="error">${esc(state.error)}</p>` : state?.summary ? `<p class="small-note">工作台 ${esc(state.summary.version)} · ${instances.length} 个实例</p>${instances.map(instance => {
      const running = instance.state === 'running', canControl = admin ? node.hasControl : instance.canControl;
      const permitted = action => canControl && (admin || (data.identity?.allowedActions || []).includes(action));
      const operation = (action, label) => permitted(action) ? `<button data-node="control" data-node-id="${esc(node.id)}" data-instance="${esc(instance.id)}" data-operation="${action}">${label}</button>` : '';
      const controls = running ? operation('stop', '停止') + operation('restart', '重启') : operation('start', '启动');
      const traffic = instance.traffic ? `<p class="small-note">接收 ${esc(instance.traffic.receivedBytes ?? '暂无数据')} B · 发送 ${esc(instance.traffic.sentBytes ?? '暂无数据')} B</p>` : '';
      return `<div class="card"><strong>${esc(instance.name)}</strong> <span class="pill">${esc(instance.state)}</span> <small>${esc(instance.kind)}</small>${traffic}${controls ? `<div class="actions">${controls}</div>` : ''}</div>`;
     }).join('')}` : '<p class="small-note">尚未读取实例状态。</p>';
     return `<article class="card"><h3>${esc(node.name)}</h3>${admin ? `<p><code>${esc(node.url)}</code></p><p class="small-note">${addresses.length ? '已确认：' + esc(addresses.join('、')) : '尚未确认地址'} · ${esc(monitor)}</p>` : `<p class="small-note">${ready ? '地址已确认' : '等待管理员确认地址'}</p>`}<div class="actions">${admin ? `<button data-node="resolve" data-id="${esc(node.id)}">核对地址</button><button data-node="monitor" data-id="${esc(node.id)}">${node.monitorEnabled ? '关闭后台检查' : '开启后台检查'}</button><button data-node="edit" data-id="${esc(node.id)}">编辑</button><button class="danger" data-node="delete" data-id="${esc(node.id)}">删除</button>` : ''}<button data-node="check" data-id="${esc(node.id)}" ${ready ? '' : 'disabled'}>读取状态</button></div>${detail}</article>`;
    }).join('')}</div>` : '<p class="empty">暂无可访问的远程节点。</p>';
    if (share) share.innerHTML = `<p>只读共享：${model.sharing ? '已启用' : '未启用'} · 流量共享：${model.shareTraffic ? '已启用' : '未启用'} · 远程控制：${model.controlSharing ? '已启用' : '未启用'}</p><div class="actions"><button data-node="issue">生成只读授权</button>${model.sharing ? '<button data-node="revoke">撤销只读授权</button><button data-node="traffic">切换流量共享</button>' : ''}<button data-node="issue-control">生成控制授权</button>${model.controlSharing ? '<button data-node="revoke-control">撤销控制授权</button>' : ''}</div>`;
   };
   const load = async () => {
    try {
     const value = await timeout('nodes', undefined);
     if (!current()) return;
     if (!value || !Array.isArray(value.nodes)) throw new Error('节点列表格式无效');
     model = value;
     for (const [id, result] of results) {
      if (!model.nodes.some(node => node.id === id && node.stamp === result.stamp)) results.delete(id);
     }
     paint(); return true;
    } catch (error) { if (current()) list.textContent = '读取失败：' + errorText(error); return false; }
   };
   const pollNodes = async generation => {
    if (!auto || generation !== autoGeneration || !current()) return;
    if (await load()) {
     const nodes = [...model.nodes];
     for (const node of nodes) {
      if (!auto || generation !== autoGeneration || !current()) return;
      if (admin ? !node.addresses?.length : !node.ready) continue;
      try {
       const value = await timeout('nodes/check', {id:node.id, stamp:node.stamp}, 10000);
       if (auto && generation === autoGeneration && current() && model.nodes.some(item => item.id === node.id && item.stamp === node.stamp) && value.id === node.id && value.stamp === node.stamp && Array.isArray(value.summary?.instances)) { results.set(node.id, value); paint(); }
      } catch (error) {
       if (auto && generation === autoGeneration && current() && model.nodes.some(item => item.id === node.id && item.stamp === node.stamp)) { results.set(node.id, {stamp:node.stamp, error:errorText(error)}); paint(); }
      }
     }
    }
    if (auto && generation === autoGeneration && current()) autoTimer = setTimeout(() => { void pollNodes(generation); }, 15000);
   };
   const syncRevision = async revision => {
    try {
     await K.refresh(true);
     if (Number.isSafeInteger(revision) && K.state()?.revision < revision) await K.refresh(true);
    } catch (_) { toast('节点设置已保存，但工作台状态刷新失败，请手动刷新。'); }
   };
   const mutate = async (payload, options = {}) => {
    if (!model || !Number.isSafeInteger(model.revision)) throw new Error('节点列表已变化，请刷新。');
    const result = await timeout('nodes', {revision:model.revision, ...payload}, 20000, options);
    if (options.acceptLateSuccess) { if (K.state()) void syncRevision(result.revision); }
    else await syncRevision(result.revision);
    return result;
   };
   const edit = node => {
    const v = node || {};
    openDialog({html:`<h2>${node ? '编辑节点' : '添加节点'}</h2><p class="small-note">只连接可信 HTTPS 地址；修改地址需要重新输入授权并核对解析结果。</p><label>名称<input name="name" maxlength="64" required value="${esc(v.name || '')}"></label><label>HTTPS 地址<input name="url" type="url" maxlength="512" required value="${esc(v.url || '')}"></label><label>只读授权${node ? '（留空保留）' : ''}<input name="credential" type="password" maxlength="64" autocomplete="new-password" ${node ? '' : 'required'}></label><label>控制授权（可选）<input name="controlCredential" type="password" maxlength="64" autocomplete="new-password"></label>${v.hasControl ? '<label class="form-check"><input name="clearControl" type="checkbox">清除已保存的控制授权</label>' : ''}`,
     submitLabel:'保存节点', onSubmit:async form => {
      await mutate({action:'save', id:v.id || '', name:form.elements.name.value.trim(), url:form.elements.url.value.trim(), credential:form.elements.credential.value.trim(), controlCredential:form.elements.controlCredential.value.trim(), clearControl:!!form.elements.clearControl?.checked});
      toast('节点已保存，请核对地址。'); await load();
     }});
   };
   const issue = async action => {
    if (!confirm('生成新授权会使同类旧授权失效；新授权只显示一次。继续？')) return;
    const dialog = document.createElement('dialog');
    dialog.innerHTML = '<h2>生成授权</h2><p data-message>正在生成，请等待结果。</p><textarea data-value rows="3" readonly hidden></textarea><div class="dialog-actions"><button type="button" data-close disabled>关闭</button></div>';
    document.body.appendChild(dialog);
    let pending = true;
    dialog.addEventListener('cancel', e => {if (pending) e.preventDefault();});
    dialog.addEventListener('close', () => dialog.remove());
    dialog.querySelector('[data-close]').addEventListener('click', () => dialog.close());
    dialog.showModal();
    try {
     const value = await mutate({action}, {acceptLateSuccess:true});
     if (!/^[a-f0-9]{64}$/.test(value.credential || '')) throw new Error('授权响应无效，请重新生成。');
     dialog.querySelector('[data-message]').textContent = '请复制此授权并通过安全渠道交给目标；关闭后不能再次显示。';
     const output = dialog.querySelector('[data-value]'); output.value = value.credential; output.hidden = false; output.focus(); output.select();
     if (current()) await load();
    } catch (error) {
     dialog.querySelector('[data-message]').textContent = error.status ? error.message : '结果未确认，旧授权可能已失效；请刷新后重新生成。';
    } finally {pending = false; dialog.querySelector('[data-close]').disabled = false;}
   };
   const control = async (node, instance, action) => {
    const notices = nodeNotices();
    if (!notices || notices.length) throw new Error('请先核对并解除已有远程操作记录。');
    if (!/^[a-f0-9]{64}$/.test(instance.binding || '')) throw new Error('目标未提供实例身份绑定，不能安全执行远程操作。');
    const preview = await timeout('nodes/control', {id:node.id, stamp:node.stamp, instanceId:instance.id, action:'preview', version:''}, 10000);
    if (!current() || preview.id !== node.id || preview.stamp !== node.stamp || preview.preview?.id !== instance.id || preview.preview?.binding !== instance.binding || preview.preview?.name !== instance.name || preview.preview?.kind !== instance.kind || preview.preview?.state !== instance.state || !/^[a-f0-9]{64}$/.test(preview.preview?.version || '')) {
     results.delete(node.id); if (current()) paint();
     throw new Error('远程实例身份或状态已变化，请重新读取。');
    }
    if (!confirm(`对「${node.name} / ${preview.preview.name}」执行${({start:'启动',stop:'停止',restart:'重启'})[action]}？断线后操作仍可能完成。`)) return;
    const key = beginNodeNotice(node, instance, action); paintNotices();
    try {
     await timeout('nodes/control', {id:node.id, stamp:node.stamp, instanceId:instance.id, action, version:preview.preview.version, capability:preview.capability || '', capabilityExpiry:preview.capabilityExpiry || 0, confirmed:true}, 45000);
     localStorage.removeItem(key); toast('远程操作已受理，请重新读取状态。');
    } catch (error) { throw new Error('操作结果未确认，请先核对目标状态。' + (error.status === 409 ? ' ' + error.message : '')); }
    finally {if (current()) paintNotices();}
   };
   root.addEventListener('click', async event => {
    const button = event.target.closest('[data-node]');
    if (!button || busy) return;
    const action = button.dataset.node;
    if (action === 'ack') {
     if (confirm('已独立核对目标及之前的请求状态，解除此记录？')) {try {localStorage.removeItem(button.dataset.key);} catch (_) {} paintNotices();}
     return;
    }
    if (action === 'new') {edit(); return;}
    if (action === 'reload') {await load(); return;}
    if (action === 'auto') {
     auto = !auto; const generation = ++autoGeneration; clearTimeout(autoTimer);
     button.textContent = auto ? '关闭自动检查' : '开启自动检查'; button.setAttribute('aria-pressed', String(auto));
     if (auto) void pollNodes(generation);
     return;
    }
    if (!model) return;
    const id = button.dataset.id || button.dataset.nodeId, node = model.nodes.find(n => n.id === id);
    if (action === 'edit' && node) {edit(node); return;}
    busy = true; button.disabled = true;
    try {
     if (action === 'issue' || action === 'issue-control') {await issue(action); return;}
     if (action === 'check' && node) {
      const value = await timeout('nodes/check', {id:node.id, stamp:node.stamp}, 10000);
      if (value.id !== node.id || value.stamp !== node.stamp || !Array.isArray(value.summary?.instances)) throw new Error('节点状态无效或已变化。');
      results.set(node.id, value); paint(); return;
     }
     if (action === 'resolve' && node) {
      const value = await timeout('nodes/resolve', {id:node.id, stamp:node.stamp}, 10000);
      if (value.id !== node.id || value.stamp !== node.stamp || !Array.isArray(value.addresses) || !value.addresses.length) throw new Error('解析结果无效。');
      if (!confirm(`确认下列地址属于 ${node.name}？\n${value.addresses.join('\n')}`)) return;
      const saved = await timeout('nodes/confirm', {id:node.id, stamp:node.stamp, revision:value.revision, addresses:value.addresses}, 20000);
      await syncRevision(saved.revision);
      toast('地址已确认。'); await load(); return;
     }
     if (action === 'control' && node) {
      const instance = results.get(node.id)?.summary?.instances?.find(v => v.id === button.dataset.instance);
      if (instance) await control(node, instance, button.dataset.operation); return;
     }
     if (action === 'delete' && node && !confirm(`从本机删除节点「${node.name}」？目标端授权不会被撤销。`)) return;
     if (action === 'revoke' || action === 'revoke-control') {if (!confirm('撤销授权？已受理的操作可能继续完成。')) return;}
     if (action === 'delete' || action === 'monitor' || action === 'revoke' || action === 'revoke-control' || action === 'traffic') {
      await mutate({action:action === 'traffic' ? 'share-traffic' : action, id:node?.id || '', ...(action === 'monitor' ? {enabled:!node.monitorEnabled} : action === 'traffic' ? {enabled:!model.shareTraffic} : {})});
      toast('节点设置已更新。'); await load();
     }
    } catch (error) { if (current()) toast(errorText(error)); }
    finally {busy = false; if (button.isConnected) button.disabled = false;}
   });
   paintNotices(); void load();
  }
 });

 K.register('firewall', {
  render() {
   return `<div class="page-head"><div><p class="eyebrow">SECURITY / ACCESS</p><h1>端口访问控制</h1><p>为本机端口设置来源规则，提交后立即应用到 Linux 防火墙。</p></div></div><div data-fw-root>正在读取配置…</div>`;
  },
  hasUnsaved(root) { return root.dataset.dirty === 'true'; },
  mount(root) {
   const host = root.querySelector('[data-fw-root]');
   const current = () => K.isCurrent('firewall', root);
   let model = null, busy = false;
   const readRules = () => [...host.querySelectorAll('[data-fw-rule]')].map(section => {
    const get = name => section.querySelector(`[name="${name}"]`);
    const kind = get('refreshKind').value;
    return {name:get('name').value.trim(), enabled:get('enabled').checked, mode:get('mode').value, ports:get('ports').value.trim(), sources:get('sources').value.trim(), ...(kind ? {refresh:{kind, value:get('refreshValue').value.trim(), intervalMinutes:Number(get('refreshMinutes').value)}} : {})};
   });
   const ruleHTML = (rule, index) => `<section class="card" data-fw-rule><div class="actions"><h3>规则 ${index + 1}</h3><button type="button" data-fw="remove-rule">删除规则</button></div><div class="form-grid"><label>名称<input name="name" maxlength="64" value="${esc(rule.name || '')}"></label><label>处理方式<select name="mode"><option value="block" ${rule.mode !== 'allow' ? 'selected' : ''}>阻止列出的来源</option><option value="allow" ${rule.mode === 'allow' ? 'selected' : ''}>只允许列出的来源</option></select></label><label class="span-2">TCP / UDP 端口<input name="ports" value="${esc(rule.ports || '')}" placeholder="例如 7000, 8000-8010"></label><label class="span-2">IP 地址或网段<textarea name="sources" rows="5" placeholder="每行一个 IP 或 CIDR">${esc(rule.sources || '')}</textarea></label><label class="form-check"><input type="checkbox" name="enabled" ${rule.enabled !== false ? 'checked' : ''}>启用此规则</label><label>自动刷新<select name="refreshKind"><option value="">关闭</option><option value="asn" ${rule.refresh?.kind === 'asn' ? 'selected' : ''}>ASN</option><option value="https" ${rule.refresh?.kind === 'https' ? 'selected' : ''}>HTTPS 清单</option></select></label><label>刷新来源<input name="refreshValue" value="${esc(rule.refresh?.value || '')}" placeholder="AS 号或 HTTPS 地址"></label><label>刷新间隔（分钟）<input name="refreshMinutes" type="number" min="60" max="10080" value="${esc(rule.refresh?.intervalMinutes || 1440)}"></label><label class="span-2">导入本地来源文件<input name="sourceFile" type="file" accept=".txt,text/plain"></label></div><p class="small-note">自动刷新会替换整条规则的来源快照；首次应用前请先核对来源。</p><div class="actions"><button type="button" data-fw="import-file">导入文件</button><button type="button" data-fw="asn">查询 ASN 网段</button><button type="button" data-fw="list">获取 HTTPS 清单</button><button type="button" data-fw="normalize">整理当前来源</button></div></section>`;
   const paint = (rules, settings) => {
    if (!current() || !model) return;
    const c = model.config || {}, activeRules = rules || (Array.isArray(c.rules) && c.rules.length ? c.rules : c.mode ? [{name:'', enabled:true, mode:c.mode, ports:c.ports, sources:c.sources}] : []);
    const selected = settings || c;
    host.innerHTML = `<section class="panel"><div class="panel-content"><p class="notice">${model.supported ? '规则同时作用于所选 TCP 和 UDP 端口。应用失败时请先核对内核状态。' : '当前系统不支持 Linux nftables；可查看已保存的配置。'}</p><p class="small-note">上次应用：${esc(model.lastResult || 'unknown')} ${esc(model.refreshStatus || '')} ${esc(model.restoreStatus || '')}</p><div class="actions"><button type="button" data-fw="reload">重新读取</button><button type="button" data-fw="seen" ${model.supported ? '' : 'disabled'}>查看拦截记录</button></div><div data-fw-seen></div><form data-fw-form><div class="form-grid"><label class="form-check"><input type="checkbox" name="filterEnabled" ${selected.enabled ? 'checked' : ''}>启用端口来源过滤</label><label class="form-check"><input type="checkbox" name="autoRestore" ${selected.autoRestore ? 'checked' : ''}>启动时恢复已保存规则</label><label class="form-check"><input type="checkbox" name="recordBlocked" ${selected.recordBlocked ? 'checked' : ''}>记录被拦截的来源</label></div><div data-fw-rules>${activeRules.map(ruleHTML).join('')}</div><div class="actions"><button type="button" data-fw="add-rule">＋ 添加规则</button><button class="primary" type="submit" ${model.supported ? '' : 'disabled'}>保存并应用</button></div></form></div></section>`;
   };
   const load = async (keepFormOnError = false) => {
    try { const value = await timeout('firewall', undefined); if (!current()) return; model = value; root.dataset.dirty = 'false'; paint(); }
    catch (error) { if (current()) {
     if (keepFormOnError && host.querySelector('[data-fw-form]')) toast('重新读取失败，已保留当前表单：' + errorText(error));
     else host.textContent = '读取失败：' + errorText(error);
    } }
   };
   const settings = () => ({enabled:host.querySelector('[name="filterEnabled"]').checked, autoRestore:host.querySelector('[name="autoRestore"]').checked, recordBlocked:host.querySelector('[name="recordBlocked"]').checked});
   for (const type of ['input', 'change']) host.addEventListener(type, event => { if (event.target.closest('[data-fw-form]')) root.dataset.dirty = 'true'; });
   host.addEventListener('submit', async event => {
    if (!event.target.matches('[data-fw-form]')) return;
    event.preventDefault(); if (busy || !model?.supported) return;
    const values = settings(), rules = readRules();
    if (!confirm(values.enabled ? '保存并立即应用这些端口来源规则？来源匹配错误可能影响连接。' : '关闭端口来源过滤？尚未应用的规则编辑不会保存。')) return;
    busy = true; const form = event.target, submit = form.querySelector('[type="submit"]'); submit.disabled = true; form.inert = true;
    try {
     const saved = await timeout('firewall', {revision:model.config.revision, ...values, rules}, 45000);
     if (!current()) return;
     if (!saved?.config || !Number.isSafeInteger(saved.config.revision)) throw new Error('保存响应无效，请重新读取并核对。');
     model = {...model, ...saved}; toast('规则已保存并应用。'); await load(true);
    }
    catch (error) { toast(error.status === 400 || error.status === 409 ? error.message : '应用结果可能未确认，请重新读取并核对内核状态：' + errorText(error)); }
    finally {busy = false; form.inert = false; submit.disabled = false;}
   });
   host.addEventListener('click', async event => {
    const button = event.target.closest('[data-fw]'); if (!button || busy) return;
    const action = button.dataset.fw;
    if (action === 'reload') { if (confirm('重新读取将丢弃尚未保存的编辑，继续？')) await load(); return; }
    if (action === 'add-rule' || action === 'remove-rule') {
     const rulesHost = host.querySelector('[data-fw-rules]');
     if (!rulesHost) return;
     if (action === 'add-rule') {
      const count = rulesHost.querySelectorAll('[data-fw-rule]').length;
      if (count >= 16) {toast('最多支持 16 条规则。'); return;}
      rulesHost.insertAdjacentHTML('beforeend', ruleHTML({mode:'block', enabled:true, ports:'', sources:''}, count));
     } else {
      const section = button.closest('[data-fw-rule]');
      if (!section || section.parentElement !== rulesHost) return;
      section.remove();
      rulesHost.querySelectorAll('[data-fw-rule]').forEach((rule, index) => {
       const title = rule.querySelector('h3');
       if (title) title.textContent = `规则 ${index + 1}`;
      });
     }
     root.dataset.dirty = 'true'; return;
    }
    busy = true; button.disabled = true;
    try {
     if (action === 'seen') {
      const result = await timeout('firewall/seen', undefined, 20000), area = host.querySelector('[data-fw-seen]');
      if (current() && area) area.innerHTML = result.enabled ? `<p class="small-note">${result.partial ? '记录可能不完整。' : '以下为当前读取到的记录。'}</p><div class="table-wrap"><table><thead><tr><th>来源</th><th>协议</th><th>端口</th><th></th></tr></thead><tbody>${(result.records || []).map(row => `<tr><td>${esc(row.source)}</td><td>${esc(row.protocol)}</td><td>${esc(row.port)}</td><td><button type="button" data-fw="ip-asn" data-ip="${esc(row.source)}">查询 ASN</button></td></tr>`).join('')}</tbody></table></div>` : '<p class="small-note">当前未启用拦截记录。</p>';
      return;
     }
     if (action === 'ip-asn') {
      if (!confirm(`将 ${button.dataset.ip} 发送到 RIPEstat 查询所属网络？`)) return;
      const result = await timeout('firewall/ip-asn', {ip:button.dataset.ip}, 25000);
      toast(`网段：${result.prefix || '未知'}；ASN：${(result.asns || []).join('、') || '未知'}`); return;
     }
     const section = button.closest('[data-fw-rule]'); if (!section) return;
     const sources = section.querySelector('[name="sources"]'); let result;
     if (action === 'normalize') result = await timeout('firewall/import', {text:sources.value}, 15000);
     if (action === 'import-file') {
      const file = section.querySelector('[name="sourceFile"]').files[0];
      if (!file || file.size > 256 * 1024) throw new Error('请选择不超过 256 KiB 的 UTF-8 来源文件。');
      let text; try { text = new TextDecoder('utf-8', {fatal:true}).decode(await file.arrayBuffer()); } catch (_) { throw new Error('来源文件必须是 UTF-8。'); }
      result = await timeout('firewall/import', {text}, 15000);
     }
     if (action === 'asn') { const value = prompt('输入要查询的 ASN，例如 AS4134：', section.querySelector('[name="refreshValue"]').value); if (value === null) return; result = await timeout('firewall/asn', {asn:value.trim()}, 40000); }
     if (action === 'list') { const value = prompt('输入公网 HTTPS 清单地址：', section.querySelector('[name="refreshValue"]').value); if (value === null) return; result = await timeout('firewall/list', {url:value.trim()}, 25000); }
     if (result) {
      if (!Array.isArray(result.prefixes)) throw new Error('来源清单格式无效');
      let append = false;
      if (sources.value.trim() && action !== 'normalize' && !confirm('用此次结果替换当前来源？')) { if (!confirm('将此次结果追加到当前来源？')) return; append = true; }
      sources.value = (append ? sources.value.trim() + '\n' : '') + result.prefixes.join('\n');
      root.dataset.dirty = 'true';
      toast('来源已填入编辑器，尚未应用。');
     }
    } catch (error) { if (current()) toast(errorText(error)); }
    finally {busy = false; if (button.isConnected) button.disabled = false;}
   });
   void load();
  }
 });
})();
