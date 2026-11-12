'use strict';

(() => {
 const K = window.Kurumi, esc = K.esc;
 const roles = {admin:'管理员', operator:'操作员', viewer:'查看者'};
 const actions = [['start','启动'], ['stop','停止'], ['restart','重启'], ['validate','配置校验']];
 const grantKey = grant => `${grant.nodeId}:${grant.instanceId}:${grant.binding}`;
 const stampOK = value => /^[a-f0-9]{64}$/.test(value || '');
 const idOK = value => /^[a-f0-9]{16}$/.test(value || '');
 const accountIDOK = value => /^[a-f0-9]{32}$/.test(value || '');
 // Transient request state is not evidence of what the server saved to disk.
 let frpPending = false, frpNeedsReview = false, frpReviewNotice = false, frpReviewGeneration = 0;
 let frpReviewTarget = null, frpReviewSnapshot = null;
 let revocationNotices = [];
 let disposeFRP = null, activeFRPWarning = null, activeRevocationWarning = null, refreshActiveAccounts = null;
 const requireFRPReview = (accountId, instanceId, action, tokenLost = false) => {
  frpNeedsReview = true; frpReviewNotice = true; frpReviewGeneration++;
  frpReviewTarget = {accountId, instanceId, action, tokenLost}; frpReviewSnapshot = null;
 };
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
    <section class="panel"><div class="panel-content"><p class="small-note">管理员拥有全部权限。删除实例或替换配置会撤销相关授权，请在操作后核对账户。账户配置独立保存，不包含在配置导出或加密备份中。</p><p class="error" data-account-write-warning role="alert" hidden></p><p class="error" data-account-revocation-warning role="alert" hidden></p><button type="button" data-account="revocation-ack" hidden>已知晓连接提示</button><div class="section-heading"><h2>账户分组</h2><button type="button" data-account="group-new" disabled>新建分组</button></div><p class="small-note">分组用于整理账户；可保存手动应用的权限模板。更改分组或模板不会自动修改账户权限，不提供套餐或流量配额。</p><div data-account-groups>正在读取分组…</div><div class="toolbar"><label>搜索用户名<input type="search" data-account-search placeholder="输入用户名" maxlength="32" disabled></label><label>账户状态<select data-account-filter disabled><option value="all">全部状态</option><option value="active">启用（含即将到期）</option><option value="expiring">7 天内到期</option><option value="expired">已到期</option><option value="disabled">已停用</option></select></label><label>所属分组<select data-account-group-filter disabled><option value="*">全部分组</option><option value="">未分组</option></select></label></div><p class="small-note" data-account-summary role="status"></p><p class="error" data-account-frp-warning role="status" hidden></p><button type="button" data-account="frp-reviewed" hidden>已核对，关闭提示</button><div data-account-list>正在读取账户…</div></div></section>
    <section class="panel"><div class="panel-content"><div class="actions"><h2>登录记录</h2><button type="button" data-account="history">刷新记录</button><button type="button" class="danger" data-account="clear-history" disabled>清空记录</button></div><p class="small-note">来源为工作台收到连接的对端地址；通过 SSH 转发时可能显示本机地址。重复失败会限频记录。</p><div data-login-history>正在读取记录…</div></div></section>`;
  },
  mount(root, data) {
   if (data.identity?.role !== 'admin') return;
   const epoch = K.epoch(), list = root.querySelector('[data-account-list]'), history = root.querySelector('[data-login-history]');
   const createButton = root.querySelector('[data-account="new"]'), clearButton = root.querySelector('[data-account="clear-history"]');
   const search = root.querySelector('[data-account-search]'), filter = root.querySelector('[data-account-filter]'), summary = root.querySelector('[data-account-summary]'), frpWarning = root.querySelector('[data-account-frp-warning]'), frpReviewed = root.querySelector('[data-account="frp-reviewed"]'), writeWarning = root.querySelector('[data-account-write-warning]');
   const revocationWarning = root.querySelector('[data-account-revocation-warning]'), revocationAck = root.querySelector('[data-account="revocation-ack"]');
   const groupNew = root.querySelector('[data-account="group-new"]'), groupList = root.querySelector('[data-account-groups]'), groupFilter = root.querySelector('[data-account-group-filter]');
   const instances = Array.isArray(data.state?.instances) ? data.state.instances : [];
   const names = new Map(instances.map(item => [item.id, item.name]));
   let model = null, accountSerial = 0, historySerial = 0, historySaved = false, historyUncertain = false, deleting = false, clearing = false, groupBusy = false;
   const current = () => K.epoch() === epoch && K.isCurrent('accounts', root) && K.state()?.identity?.role === 'admin';
   const ownAccount = user => !!data.identity?.account && String(data.identity.name || '').toLowerCase() === String(user.username || '').toLowerCase();
   const requireCurrent = () => {
    if (!current()) throw new Error('页面或会话已变化，请重新打开账户管理。');
    if (K.state()?.revision !== data.revision) throw new Error('实例配置已变化，请刷新后重试。');
   };
   const writesBlocked = () => model?.accountWritesUncertain !== false;
   const writeBlockReason = () => model?.accountWritesUncertain === true ? '账户文件写入结果未确认，账户文件的所有写入已冻结；整个账户列表可能是旧的内存状态，已有工作台登录和 frp 转发可能继续。请安全停止并重启工作台，然后刷新并核对恢复后的账户状态。' : '无法确认账户文件的写入状态，账户修改已禁用；请刷新账户或更新工作台。';
   const requireWritable = () => { if (writesBlocked()) throw new Error(writeBlockReason()); };
   const updateWriteWarning = () => {
    if (!model) return;
    writeWarning.hidden = !writesBlocked();
    writeWarning.textContent = model.accountWritesUncertain === true ? '账户文件写入发生错误，本进程已冻结全部账户文件写入。整个账户列表可能是旧的内存状态；已有工作台登录和 frp 转发可能继续。请安全停止并重启工作台，再刷新并核对恢复后的账户状态；若仍显示冻结，请检查账户文件和数据目录。' : writesBlocked() ? '无法确认账户文件的写入状态，账户修改已禁用。请刷新账户或更新工作台。' : '';
   };
   const updateRevocationWarning = () => {
    revocationWarning.hidden = revocationNotices.length === 0;
    revocationAck.hidden = revocationNotices.length === 0;
    revocationWarning.textContent = revocationNotices.join(' ');
   };
   activeRevocationWarning = updateRevocationWarning;
   const recordRevocationWarning = (result, operation) => {
    if (result?.ok !== true || result.revocation !== 'unconfirmed') return '';
    const detail = typeof result.warning === 'string' ? result.warning.trim() : '';
    const message = `${operation}已保存。服务端报告旧 frp 连接关闭未确认，已请求停止实例；请勿重复提交，并核对实例状态。${detail ? ` ${detail}` : ''}`;
    if (!revocationNotices.includes(message)) revocationNotices.push(message);
    activeRevocationWarning?.();
    if (K.epoch() === epoch) K.toast('操作已保存；旧 frp 连接关闭未确认，请查看账户页提示。');
    return message;
   };
   const updateFRPWarning = () => {
    frpWarning.hidden = !frpPending && !frpNeedsReview && !frpReviewNotice;
    const target = frpReviewTarget, action = target && ({'frp-issue':'发放','frp-rotate':'轮换','frp-disable':'停用'}[target.action] || '登记');
    frpWarning.textContent = frpPending ? 'frp 凭据操作正在处理，离开页面不会取消服务端操作；若离开，可能无法取得一次性令牌。' : frpNeedsReview ? `账户 ${target.accountId}、实例 ${target.instanceId} 的凭据${action}${target.tokenLost ? '已完成，但令牌未显示' : '结果未确认，旧请求可能仍在处理'}。刷新只是当时的快照，未收到的明文无法取回；请核对后点击“已核对”。新操作若发生版本冲突，请关闭窗口并刷新复核。账户文件写入冻结时须先安全重启。` : frpReviewNotice ? '请核对 frp 凭据登记：未收到或未保存的一次性令牌无法再次显示；已有记录时请主动轮换，无记录时可发放。账户状态不能判断是否收到过明文。' : '';
    frpReviewed.hidden = !frpNeedsReview && !frpReviewNotice;
    frpReviewed.disabled = frpPending || (frpNeedsReview && (writesBlocked() || frpReviewSnapshot !== model));
   };
   activeFRPWarning = updateFRPWarning;
   const renderList = () => {
    if (!current() || !model) return;
    updateFRPWarning();
    const sampledAt = Date.parse(model.sampledAt), hasTime = Number.isFinite(sampledAt), query = search.value.trim().toLowerCase();
    const state = user => {
     if (!user.enabled) return 'disabled';
     if (user.expired) return 'expired';
     const expiry = user.expiresAt ? Date.parse(user.expiresAt) : NaN;
     return hasTime && Number.isFinite(expiry) && expiry > sampledAt && expiry - sampledAt <= 7 * 24 * 60 * 60 * 1000 ? 'expiring' : 'active';
    };
    const visible = model.accounts.filter(user => {
     const accountState = state(user), selected = filter.value;
     return String(user.username || '').toLowerCase().includes(query) && (selected === 'all' || selected === accountState || (selected === 'active' && accountState === 'expiring')) && (groupFilter.value === '*' || (user.groupId || '') === groupFilter.value);
    });
    const expiring = model.accounts.filter(user => state(user) === 'expiring').length;
    summary.textContent = `显示 ${visible.length} / ${model.accounts.length} 个账户。` + (hasTime ? ` 全部账户中有 ${expiring} 个已启用账户在 7 天内到期；状态截至 ${model.sampledAt}，请刷新查看最新结果。` : ' 服务端时间不可用，无法判断是否临近到期；请刷新账户。');
    if (!model.accounts.length) { list.innerHTML = '<p>尚未创建本地账户。管理员仍可使用主密码登录。</p>'; return; }
    if (!visible.length) { list.innerHTML = '<p>没有符合筛选条件的账户。</p>'; return; }
    list.innerHTML = `<div class="table-wrap"><table><thead><tr><th>用户名</th><th>角色</th><th>状态</th><th>分组</th><th>授权</th><th>操作</th></tr></thead><tbody>${visible.map(user => {
     const self = ownAccount(user), local = Array.isArray(user.instances) ? user.instances : [], remote = Array.isArray(user.remoteInstances) ? user.remoteInstances : [];
     const accountState = state(user), status = {disabled:'已停用',expired:'已到期',expiring:'7 天内到期',active:'启用'}[accountState];
     const grants = user.role === 'admin' ? '全部实例' : `${local.length ? local.map(id => esc(names.get(id) || `实例 ${id}`)).join('、') : '无本机实例'} · 远程 ${remote.length} 项`;
     const groupOptions = `<option value="">未分组</option>` + model.groups.map(group => `<option value="${esc(group.id)}" ${group.id === user.groupId ? 'selected' : ''}>${esc(group.name)}</option>`).join('');
     const revoke = `<button type="button" data-account="revoke-sessions" data-id="${esc(user.id)}">撤销工作台会话</button>`;
     const frp = `<button type="button" data-account="frp-tokens" data-id="${esc(user.id)}">frp 凭据登记</button>`;
     return `<tr><td>${esc(user.username)}${self ? ' <span class="pill">当前账户</span>' : ''}</td><td>${esc(roles[user.role] || user.role)}</td><td><span class="pill ${accountState === 'expiring' || accountState === 'expired' ? 'warn' : ''}">${status}</span>${user.expiresAt ? `<br><small>到期 ${esc(user.expiresAt)} (UTC)</small>` : ''}</td><td><select data-account-group="${esc(user.id)}" aria-label="${esc(user.username)} 的分组" ${groupBusy || writesBlocked() ? 'disabled' : ''}>${groupOptions}</select></td><td>${grants}</td><td>${self ? `<div class="actions">${frp}${revoke}<span class="small-note">${writesBlocked() ? '暂不可修改账户' : '修改密码请使用“修改登录密码”'}</span></div>` : `<div class="actions"><button type="button" data-account="edit" data-id="${esc(user.id)}" ${writesBlocked() ? 'disabled' : ''}>编辑</button>${frp}${revoke}<button type="button" class="danger" data-account="delete" data-id="${esc(user.id)}" ${writesBlocked() ? 'disabled' : ''}>删除</button></div>`}</td></tr>`;
    }).join('')}</tbody></table></div>`;
   };
   const renderGroups = () => {
    if (!current() || !model) return;
    const previous = groupFilter.value;
    groupFilter.innerHTML = '<option value="*">全部分组</option><option value="">未分组</option>' + model.groups.map(group => `<option value="${esc(group.id)}">${esc(group.name)}</option>`).join('');
    groupFilter.value = previous === '*' || previous === '' || model.groups.some(group => group.id === previous) ? previous : '*';
    groupNew.disabled = groupBusy || writesBlocked();
    groupList.innerHTML = model.groups.length ? `<div class="table-wrap"><table><thead><tr><th>分组</th><th>账户数</th><th>权限模板</th><th>操作</th></tr></thead><tbody>${model.groups.map(group => `<tr><td>${esc(group.name)}</td><td>${model.accounts.filter(user => user.groupId === group.id).length}</td><td>${group.template ? `已保存 · ${esc(roles[group.template.role] || group.template.role)}` : '未设置'}</td><td><div class="actions"><button type="button" data-account="group-edit" data-id="${esc(group.id)}" ${groupBusy || writesBlocked() ? 'disabled' : ''}>编辑</button><button type="button" class="danger" data-account="group-delete" data-id="${esc(group.id)}" ${groupBusy || writesBlocked() ? 'disabled' : ''}>删除</button></div></td></tr>`).join('')}</tbody></table></div>` : '<p class="small-note">尚未创建分组；账户暂为未分组。</p>';
   };
   const loadAccounts = async () => {
    const serial = ++accountSerial, reviewGeneration = frpReviewGeneration;
    frpReviewSnapshot = null;
    model = null; createButton.disabled = true; groupNew.disabled = true; search.disabled = true; filter.disabled = true; groupFilter.disabled = true; summary.textContent = '';
    updateFRPWarning();
    list.textContent = '正在读取账户…'; groupList.textContent = '正在读取分组…';
    try {
     const value = await request('accounts');
     if (!current() || serial !== accountSerial) return;
     if (!Array.isArray(value.accounts) || !Array.isArray(value.groups) || !Number.isSafeInteger(value.revision) || value.revision < 1) throw new Error('账户列表格式无效。');
     const ids = new Set();
     for (const group of value.groups) {
      if (!group || !/^[a-f0-9]{32}$/.test(group.id || '') || typeof group.name !== 'string' || ids.has(group.id)) throw new Error('账户分组格式无效。');
      const template = group.template;
      if (template && (!Object.hasOwn(roles,template.role) || !Array.isArray(template.instances) || !Array.isArray(template.controlInstances) || !Array.isArray(template.allowedActions) || !Array.isArray(template.logInstances) || typeof template.allowLogs !== 'boolean' || typeof template.readPermissions?.config !== 'boolean' || typeof template.readPermissions?.traffic !== 'boolean')) throw new Error('账户权限模板格式无效。');
      ids.add(group.id);
     }
     if (value.accounts.some(user => !user || typeof user.groupId !== 'string' || (user.groupId && !ids.has(user.groupId)))) throw new Error('账户分组信息无效。');
     model = value;
     if (value.accountWritesUncertain === false && frpNeedsReview && !frpPending && reviewGeneration === frpReviewGeneration) frpReviewSnapshot = value;
     createButton.disabled = writesBlocked(); search.disabled = false; filter.disabled = false; groupFilter.disabled = false; updateWriteWarning(); renderGroups(); renderList();
    } catch (error) {
     if (current() && serial === accountSerial) { list.textContent = '读取账户失败：' + (error.name === 'AbortError' ? '请求超时，请重试。' : error.message); groupList.textContent = '账户分组不可用，请刷新重试。'; }
    }
   };
   refreshActiveAccounts = () => { if (current()) void loadAccounts(); };
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

   const manageFRP = user => {
    requireCurrent();
    if (frpPending) throw new Error('另一个 frp 凭据操作仍在处理，请等待结果。');
    if (!model || groupBusy || deleting) throw new Error('请等待账户操作完成并刷新列表。');
    const source = model, revision = source.revision;
    const entries = user.frpTokens === undefined ? [] : user.frpTokens;
    const dataOK = accountIDOK(user.id) && Array.isArray(entries) && entries.every(item => item && typeof item === 'object');
    const configOK = Number.isSafeInteger(source.configRevision) && source.configRevision >= 0;
    const instancesOK = Array.isArray(source.frpInstances) && source.frpInstances.every(item => item && idOK(item.id) && typeof item.name === 'string') &&
     new Set(source.frpInstances.map(item => item.id)).size === source.frpInstances.length;
    const instancesByID = new Map((instancesOK ? source.frpInstances : []).map(item => [item.id,item]));
    const records = dataOK ? entries : [], grouped = new Map();
    for (const entry of records) if (idOK(entry.instanceId)) grouped.set(entry.instanceId,[...(grouped.get(entry.instanceId) || []),entry]);
    const targetIDs = [...instancesByID.keys(), ...[...grouped.keys()].filter(id => !instancesByID.has(id))];
    const statusLabel = entry => entry.status === 'registered' ? '已登记，可按当前实例身份和数据密钥读取' : entry.status === 'needs-reissue' ? '需重新签发' : '状态无法识别';
    const table = records.length ? `<div class="table-wrap"><table><thead><tr><th>本机 frps 实例</th><th>凭据 ID / 版本</th><th>登记状态</th></tr></thead><tbody>${records.map(entry => {
     const instance = instancesByID.get(entry.instanceId), name = instance ? instance.name : !idOK(entry.instanceId) ? '实例标识无效，无法通过此页面停用' : instancesOK ? '实例已不存在或不再是 frps' : '当前实例列表不可用';
     const count = grouped.get(entry.instanceId)?.length || 0;
     return `<tr><td>${esc(name)}<br><small>${esc(entry.instanceId || '未知 ID')}</small></td><td>${esc(entry.credentialId || '未知 ID')}<br><small>版本 ${Number.isSafeInteger(entry.version) && entry.version > 0 ? entry.version : '未知'}</small></td><td>${esc(statusLabel(entry))}${count > 1 ? '<br><small>同一实例有多条记录，只能先全部停用。</small>' : ''}</td></tr>`;
    }).join('')}</tbody></table></div>` : '<p class="small-note">该账户没有已登记的 frp 凭据。</p>';
    const options = targetIDs.map(id => {
     const instance = instancesByID.get(id), count = grouped.get(id)?.length || 0;
     return `<option value="${esc(id)}">${esc(instance ? instance.name : instancesOK ? '已移除或非 frps 实例' : '当前实例列表不可用')} · ${esc(id)} · ${count ? `${count} 条记录` : '未登记'}</option>`;
    }).join('');
    let marker = null, displayed = false, invalidated = false;
    const formCurrent = form => current() && marker?.isConnected && form.contains(marker) && document.querySelector('#dialog')?.open;
    const syncForm = form => {
     if (!formCurrent(form)) return;
     const controls = form.querySelector('[data-frp-controls]'), reason = form.querySelector('[data-frp-reason]');
     const blocked = !dataOK || writesBlocked() || frpNeedsReview || frpPending || invalidated;
     controls.hidden = blocked || !targetIDs.length;
     if (blocked || !targetIDs.length) {
      reason.textContent = !dataOK ? '凭据信息格式不可用，请刷新账户并核对服务端版本。' : writesBlocked() ? writeBlockReason() : frpNeedsReview ? '请关闭窗口，刷新账户并核对提示中的账户与实例，再点击“已核对”。刷新只是当时快照，旧请求可能仍在处理；未收到的明文无法取回。' : frpPending ? '另一个 frp 凭据操作仍在处理，请等待结果。' : invalidated ? '账户或实例状态已变化，请关闭窗口并刷新后核对。' : !instancesOK ? '当前实例列表不可用，也没有可停用的有效实例记录，请刷新后核对。' : '当前没有可发放的本机 frps 实例，也没有可停用的有效实例记录。';
      form.querySelector('[type="submit"]').textContent = '关闭';
      return;
     }
     reason.textContent = '';
      const id = form.elements.frpInstance.value, instance = instancesByID.get(id), existing = grouped.get(id) || [], actions = [];
      const bindingOK = instance && configOK && stampOK(instance.binding);
      if (bindingOK && !existing.length && records.length < 64) actions.push(['frp-issue','发放新凭据']);
      if (bindingOK && existing.length === 1 && records.length <= 64) actions.push(['frp-rotate','轮换已有凭据']);
     if (existing.length) actions.push(['frp-disable',existing.length > 1 ? '停用该实例的全部记录' : '停用登记']);
     form.elements.frpAction.innerHTML = actions.map(([value,label]) => `<option value="${value}">${label}</option>`).join('');
      form.querySelector('[data-frp-target]').textContent = instance ? `当前实例 ID：${instance.id}；身份绑定：${stampOK(instance.binding) ? instance.binding : '不可用'}。请核对目标；绑定变化时须重新选择。` : `实例 ID：${id}；当前实例信息不可用或该实例不再是 frps，可以停用旧记录。`;
      if (!instancesOK) reason.textContent = '当前实例列表不可用；仍可停用已登记的记录。';
      else if (!bindingOK && instance) reason.textContent = '当前实例身份绑定或配置版本不可用，不能发放或轮换；可停用已有记录。';
      else if (!actions.length) reason.textContent = '此账户已达到 64 条凭据记录上限，请先停用不再使用的记录。';
     form.querySelector('[type="submit"]').textContent = actions.length ? '确认登记操作' : '关闭';
    };
    K.openDialog({
     html:`<div data-frp-marker><h2>frp 凭据登记 · ${esc(user.username)}</h2><p class="error">目前仅登记数据，frp 账户认证尚未接入，当前不可使用；这些操作不能阻止新连接，也不会断开已有连接。请勿将登记状态当作当前访问控制。</p><p class="small-note">账户 ID：<code>${esc(user.id)}</code>。以下是后续客户端配置要求，目前仍不可用于认证：<code>user=${esc(user.id)}</code>、<code>auth.additionalScopes=["NewWorkConns"]</code>、<code>transport.tcpMux=false</code>，并须验证服务端证书。</p><p class="small-note">“已登记”只表示记录可按当前实例身份和数据密钥读取，不表示认证已启用。替换实例、导入或恢复配置后可能需要重新签发；每个账户最多登记 64 个实例。</p><p class="small-note">如果未收到或未保存一次性令牌，它将无法再次显示；请刷新并核对登记，已有记录时主动轮换，无记录时可发放。服务端登记状态不能判断明文是否收到。刷新只是当时的快照，旧请求可能仍在处理；新操作若发生版本冲突，请关闭窗口并刷新复核。</p><div data-frp-records>${dataOK ? table : ''}</div><p class="small-note" data-frp-reason role="status"></p><div data-frp-controls><div class="form-grid"><label>目标实例<select name="frpInstance">${options}</select></label><label>登记操作<select name="frpAction"></select></label></div><p class="small-note" data-frp-target></p><p class="small-note">发放或轮换后的明文只显示一次；关闭或离开页面后无法再次读取。停用会删除记录。</p></div><div data-frp-result hidden><h3>一次性令牌</h3><p class="small-note">请安全保存；窗口打开期间可以重复复制。关闭或离开页面会清除显示，但不会清除剪贴板；关闭后无法再次显示，若未保存请核对登记后主动轮换。这仍不启用 frp 账户认证。</p><p data-frp-result-info></p><label>令牌<input name="frpToken" type="text" readonly autocomplete="off" spellcheck="false"></label><button type="button" data-frp-copy>复制令牌</button><p class="small-note" data-frp-copy-status role="status"></p></div></div>`,
     submitLabel:'确认登记操作',
     onReady(form) {
      marker = form.querySelector('[data-frp-marker]');
      const dialog = document.querySelector('#dialog');
      const cleanup = () => {
       const input = marker?.querySelector('[name="frpToken"]');
       if (input) input.value = '';
       if (displayed) {
        frpReviewNotice = true;
        activeFRPWarning?.();
        if (K.epoch() === epoch) K.toast('令牌已关闭，无法再次显示；如未保存，请核对登记后主动轮换。');
       }
       dialog.removeEventListener('close',cleanup);
       window.removeEventListener('pagehide',dispose);
       if (disposeFRP === dispose) disposeFRP = null;
       marker = null;
      };
      const dispose = () => {
       const owned = marker?.isConnected && dialog.contains(marker);
       cleanup();
       if (owned && dialog.open) dialog.close();
      };
      disposeFRP = dispose;
      dialog.addEventListener('close',cleanup);
      window.addEventListener('pagehide',dispose);
      syncForm(form);
     },
     onChange(event, form) { if (event.target.matches('[name="frpInstance"]')) syncForm(form); },
     async onClick(event, form) {
      if (!event.target.closest('[data-frp-copy]') || !displayed || !formCurrent(form)) return;
      const input = form.elements.frpToken, status = form.querySelector('[data-frp-copy-status]');
      try {
       if (!navigator.clipboard?.writeText) throw new Error('clipboard unavailable');
       await navigator.clipboard.writeText(input.value);
       if (formCurrent(form)) status.textContent = '已复制，请安全保存；关闭窗口不会清除剪贴板。';
      } catch (_) {
       if (formCurrent(form)) { input.focus(); input.select(); status.textContent = '自动复制不可用；已选中令牌，请手动复制。'; }
      }
     },
     async onSubmit(form) {
      if (frpPending) return false;
      if (displayed || invalidated || !dataOK || writesBlocked() || frpNeedsReview || !targetIDs.length || !form.elements.frpAction.value) return;
      requireCurrent();
      requireWritable();
      if (!formCurrent(form) || model !== source || model.revision !== revision) throw new Error('账户列表已变化，请关闭窗口并刷新后核对。');
      const instanceId = form.elements.frpInstance.value, action = form.elements.frpAction.value, instance = instancesByID.get(instanceId), existing = grouped.get(instanceId) || [];
      if (!idOK(instanceId) || !targetIDs.includes(instanceId) || !['frp-issue','frp-rotate','frp-disable'].includes(action) || (action !== 'frp-disable' && (!instance || !configOK || !stampOK(instance.binding))) || (action === 'frp-issue' && (existing.length || records.length >= 64)) || (action === 'frp-rotate' && (existing.length !== 1 || records.length > 64)) || (action === 'frp-disable' && !existing.length)) throw new Error('凭据状态已变化，请关闭窗口并刷新后核对。');
      const label = {'frp-issue':'发放','frp-rotate':'轮换','frp-disable':'停用'}[action];
      const target = instance ? `${instance.name}（${instanceId}）` : instancesOK ? `已移除或非 frps 的实例 ${instanceId}` : `实例 ${instanceId}（当前实例列表不可用）`;
      const details = instance && action !== 'frp-disable' ? `\n实例身份绑定：${instance.binding}` : '';
      if (!confirm(`为账户「${user.username}」${label} ${target} 的 frp 凭据登记？${details}${existing.length > 1 ? '\n该实例的全部重复记录都将被停用。' : ''}${action === 'frp-rotate' ? '\n轮换将替换旧记录，旧明文无法再次读取。' : ''}\n目前仅登记数据，认证尚未接入，不能阻止新连接，也不会断开已有连接。${action !== 'frp-disable' ? '\n明文只显示一次，请准备好安全保存。' : ''}`)) return false;
      const payload = {action,revision,id:user.id,instanceId};
      if (action !== 'frp-disable') { payload.configRevision = source.configRevision; payload.instanceBinding = instance.binding; }
      frpPending = true; updateFRPWarning();
      let result = null, reloadAfterCompletion = false;
      try {
       result = await request('accounts',payload,30000);
       const credential = result?.credential;
       const success = result?.ok === true && result.revision === revision + 1 && Number.isSafeInteger(result.configRevision) && result.configRevision >= 0 &&
        (action === 'frp-disable' || (result.configRevision === source.configRevision && stampOK(result.token) && credential?.instanceId === instanceId && accountIDOK(credential.credentialId) && Number.isSafeInteger(credential.version) && credential.version > 0 && ['registered','needs-reissue'].includes(credential.status)));
       if (!success) throw new Error('响应无法确认凭据登记结果。');
       const revocation = recordRevocationWarning(result,`账户 ${user.id}、实例 ${instanceId} 的凭据${label}`);
       if (action !== 'frp-disable') {
        if (!formCurrent(form)) {
         requireFRPReview(user.id,instanceId,action,true); reloadAfterCompletion = true;
         if (K.epoch() === epoch) K.toast(`${revocation ? '连接关闭未确认；' : ''}登记已完成，但页面已离开，令牌无法再次显示；请核对记录后主动轮换。`);
         return;
        }
        displayed = true;
        form.querySelector('[data-frp-records]').hidden = true;
        form.querySelector('[data-frp-controls]').hidden = true;
        form.querySelector('[data-frp-result]').hidden = false;
        form.querySelector('[data-frp-result-info]').textContent = `${target} · 凭据 ID ${credential.credentialId} · 版本 ${credential.version} · ${statusLabel(credential)}。`;
        if (revocation) {
         const warning = document.createElement('p'); warning.className = 'error'; warning.setAttribute('role','alert'); warning.textContent = revocation;
         form.querySelector('[data-frp-result-info]').after(warning);
        }
        form.elements.frpToken.value = result.token;
        form.querySelector('[type="submit"]').textContent = '关闭';
       } else if (current()) { if (!revocation) K.toast('frp 凭据登记已停用；已有连接不会断开。'); }
       else if (K.epoch() === epoch) { if (!revocation) K.toast('frp 凭据登记已停用；请刷新并核对状态。'); refreshActiveAccounts?.(); }
       if (current()) void loadAccounts();
       return action === 'frp-disable' ? undefined : false;
      } catch (error) {
       if (error.status === 408 || uncertain(error)) {
        requireFRPReview(user.id,instanceId,action); invalidated = true; reloadAfterCompletion = true;
        if (formCurrent(form)) syncForm(form);
        else if (K.epoch() === epoch) K.toast('frp 凭据保存结果未确认，请勿自动重试；请刷新账户并核对。');
        throw new Error('保存结果未确认，请勿自动重试。旧请求可能仍在处理，刷新只是当时的快照；请关闭窗口并核对账户和实例，再点击“已核对”。若写入已冻结，请安全重启工作台。未收到的明文无法取回；新操作若发生版本冲突，请关闭窗口并刷新复核。');
       }
       if (error.status === 409 || error.status === 404) {
        invalidated = true;
        if (formCurrent(form)) syncForm(form);
        const message = error.status === 409 ? '账户或实例版本冲突，请关闭窗口并刷新后重新核对。' : '账户或实例状态已变化，请关闭窗口并刷新后核对。';
        if (!formCurrent(form) && K.epoch() === epoch) { K.toast(message); refreshActiveAccounts?.(); }
        throw new Error(message);
       }
       if (!formCurrent(form) && K.epoch() === epoch) K.toast(error.message);
       throw error;
      } finally {
       if (result && typeof result === 'object' && 'token' in result) result.token = '';
       frpPending = false;
       activeFRPWarning?.();
       if ((frpNeedsReview || invalidated) && formCurrent(form)) syncForm(form);
       if (reloadAfterCompletion && K.epoch() === epoch) refreshActiveAccounts?.();
      }
     }
    });
   };

   const editGroup = group => {
    requireCurrent();
    requireWritable();
    if (!model || groupBusy || deleting) throw new Error('请等待账户操作完成并刷新列表。');
    const source = model, revision = source.revision;
    let saveUncertain = false;
    K.openDialog({
     html:`<h2>${group ? '编辑账户分组' : '新建账户分组'}</h2><p class="small-note">分组本身不会改变账户权限。可从现有账户复制角色和本机授权作为模板；模板不会自动应用，也不包含远程授权、密码或到期时间。</p><div class="form-grid"><label>分组名称<input name="groupName" maxlength="128" required value="${esc(group?.name || '')}"></label><label>权限模板<select name="templateFrom"><option value="keep">${group?.template ? '保留现有模板' : '不设置模板'}</option>${group?.template ? '<option value="clear">删除现有模板</option>' : ''}${source.accounts.map(user => `<option value="${esc(user.id)}">复制 ${esc(user.username)} · ${esc(roles[user.role] || user.role)}</option>`).join('')}</select></label></div>`,
     submitLabel:'保存分组',
     async onSubmit(form) {
      requireCurrent();
      requireWritable();
      if (saveUncertain) throw new Error('上次保存结果未确认，请关闭窗口并刷新账户后核对。');
      if (model !== source || model.revision !== revision) throw new Error('账户或分组已变化，请刷新后重试。');
      const groupName = form.elements.groupName.value.trim();
      if (!groupName || [...groupName].length > 40 || new TextEncoder().encode(groupName).length > 128) throw new Error('分组名称须为 1 至 40 个字符，且不超过 128 字节。');
      const templateFrom = form.elements.templateFrom.value;
      const sourceUser = source.accounts.find(user => user.id === templateFrom);
      if (sourceUser?.role === 'admin' && !confirm('该账户是管理员，复制的模板可授予全部管理权限。继续保存模板？')) return false;
      const payload = {action:'group-save',revision,id:group?.id || '',groupName};
      if (templateFrom !== 'keep') payload.templateFrom = templateFrom === 'clear' ? '' : templateFrom;
      groupBusy = true; renderGroups(); renderList();
      try {
       await request('accounts',payload,30000);
       if (current()) { K.toast('分组已保存。'); await loadAccounts(); }
      } catch (error) {
       if (uncertain(error)) { saveUncertain = true; if (current()) void loadAccounts(); throw new Error('保存结果未确认，请关闭窗口并刷新账户后核对，勿重复提交；若账户文件写入已冻结，请安全重启工作台。'); }
       throw error;
      } finally {
       groupBusy = false;
       if (current() && model) { renderGroups(); renderList(); }
      }
     }
    });
   };

   const editAccount = user => {
    requireCurrent();
    requireWritable();
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
     form.querySelector('[data-account-remote]').hidden = isAdmin && ![...choices.values()].some(choice => choice.selected);
     form.querySelector('[data-load-remote]').hidden = isAdmin;
     form.querySelectorAll('[data-remote-view]').forEach(box => { box.disabled = isAdmin && !box.checked; });
     form.querySelectorAll('[data-remote-control-label]').forEach(label => { label.hidden = role !== 'operator'; });
    };
    const paintRemote = form => { form.querySelector('[data-remote-choices]').innerHTML = remoteHTML(); updateForm(form); };
    K.openDialog({
     html:`<span data-account-editor-marker hidden></span><h2>${user ? '编辑账户' : '新建账户'}</h2><p class="small-note">用户名由 3–32 位英文字母、数字、点、下划线或连字符组成；开头须为字母或数字。新账户密码须为 12–1024 字节的有效文本，不能含控制字符。</p><div class="form-grid">
      <label>用户名<input name="username" required pattern="[A-Za-z0-9][A-Za-z0-9_.-]{2,31}" maxlength="32" value="${esc(user?.username || '')}" ${user ? 'readonly' : ''} autocomplete="off"></label>
      <label>角色<select name="role"><option value="viewer" ${!user || user.role === 'viewer' ? 'selected' : ''}>查看者</option><option value="operator" ${user?.role === 'operator' ? 'selected' : ''}>操作员</option><option value="admin" ${user?.role === 'admin' ? 'selected' : ''}>管理员（全部权限）</option></select></label>
      <label>所属分组<select name="groupId"><option value="">未分组</option>${source.groups.map(group => `<option value="${esc(group.id)}" ${group.id === user?.groupId ? 'selected' : ''}>${esc(group.name)}</option>`).join('')}</select></label><div><button type="button" data-apply-template>应用所选分组的权限模板</button><p class="small-note">选择分组不会自动应用模板；应用只填入草稿，保存账户后才生效。远程授权、密码和到期时间不从模板复制。</p></div>
      <label class="span-2">${user ? '重置密码（留空保留原密码）' : '账户密码（至少 12 字节）'}<input name="password" type="password" autocomplete="new-password" maxlength="1024" ${user ? '' : 'required'}></label>
      <label class="span-2">账户到期时间（UTC，留空不限）<input name="expiresAt" type="datetime-local" step="1" value="${esc(user?.expiresAt ? user.expiresAt.slice(0,19) : '')}"><small>这里填写 UTC 时间。到期后不能登录工作台，现有工作台会话在下次请求或实时日志帧时失效。</small></label>
      <label class="form-check span-2"><input type="checkbox" name="enabled" ${!user || user.enabled ? 'checked' : ''}>启用账户</label>
      <fieldset class="span-2" data-restricted><legend>可查看的本机实例</legend>${rows('instance',visible)}<p class="small-note">未选择表示不能查看任何实例。</p></fieldset>
      <fieldset class="span-2" data-restricted><legend>实例数据查看权限</legend><label class="form-check"><input type="checkbox" name="viewConfig" ${user?.readPermissions?.config !== false ? 'checked' : ''}>配置详情和规则</label><label class="form-check"><input type="checkbox" name="viewTraffic" ${user?.readPermissions?.traffic !== false ? 'checked' : ''}>流量和流量历史</label></fieldset>
      <fieldset class="span-2" data-operator><legend>可操作的本机实例</legend>${rows('controlInstance',controls)}<p class="small-note">操作授权必须同时拥有该实例的查看授权。</p></fieldset>
      <fieldset class="span-2" data-operator><legend>允许的操作</legend>${actions.map(([id,label]) => `<label class="form-check"><input type="checkbox" name="action" value="${id}" ${allowed.has(id) ? 'checked' : ''}>${label}</label>`).join('')}<p class="small-note">对获授权的本机和远程实例生效；配置校验仅适用于本机。</p></fieldset>
      <div class="span-2" data-restricted><label class="form-check"><input type="checkbox" name="allowLogs" ${user?.allowLogs ? 'checked' : ''}>允许查看日志</label><p class="small-note">日志可能包含敏感信息。</p></div>
      <fieldset class="span-2" data-account-logs><legend>可查看日志的本机实例</legend>${rows('logInstance',logs)}<p class="small-note">日志授权必须属于可查看的实例。</p></fieldset>
      <fieldset class="span-2" data-account-remote><legend>远程实例授权</legend><p class="small-note">已有授权会保留。可读取已确认地址的节点，选择实例和身份绑定；目标替换实例后需重新授权。查看者不保留控制权限。转为管理员时请在此逐项取消全部已选远程授权，保存后才能生效。</p><button type="button" data-load-remote>读取远程实例</button><p class="small-note" data-remote-status></p><div data-remote-choices></div></fieldset>
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
      if (event.target.closest('[data-apply-template]')) {
       requireCurrent();
       if (!formCurrent(form) || model !== source || model.revision !== revision) throw new Error('账户或分组已变化，请重新打开编辑窗口。');
       const group = source.groups.find(item => item.id === form.elements.groupId.value), template = group?.template;
       if (!template) throw new Error('所选分组没有权限模板。');
       if (template.instances.some(id => !instances.some(item => item.id === id))) throw new Error('模板包含已不存在的实例，请先编辑分组模板。');
       const remote = [...choices.values()].filter(choice => choice.selected), currentActions = [...form.querySelectorAll('[name="action"]:checked')].map(box => box.value);
       const sameActions = currentActions.length === template.allowedActions.length && currentActions.every(action => template.allowedActions.includes(action));
       if (remote.length && (template.role !== form.elements.role.value || template.readPermissions.config !== form.elements.viewConfig.checked || template.readPermissions.traffic !== form.elements.viewTraffic.checked || (remote.some(choice => choice.grant.control) && !sameActions) || template.role === 'admin' || (template.role === 'viewer' && remote.some(choice => choice.grant.control)))) throw new Error('该模板会影响现有远程权限；请先手动调整远程授权，或选择不改变其角色和操作权限的模板。');
       const role = roles[template.role] || template.role;
       const actionNames = template.allowedActions.map(id => actions.find(([key]) => key === id)?.[1] || id).join('、') || '无';
       const details = template.role === 'admin' ? '管理员拥有全部实例和管理权限。' : `本机查看 ${template.instances.length} 个实例、操作 ${template.controlInstances.length} 个实例、日志 ${template.logInstances.length} 个实例。\n操作项：${actionNames}；配置查看：${template.readPermissions.config ? '允许' : '不允许'}；流量查看：${template.readPermissions.traffic ? '允许' : '不允许'}。`;
       if (!confirm(`应用「${group.name}」模板？\n角色：${role}。${details}\n将覆盖当前草稿中的角色、本机授权、操作项和配置/流量/日志查看设置；远程授权不会复制。保存账户后才生效。`)) return;
       const setChecks = (name, values) => { const selected = new Set(values); form.querySelectorAll(`[name="${name}"]`).forEach(box => { box.checked = selected.has(box.value); }); };
       form.elements.role.value = template.role;
       setChecks('instance',template.instances); setChecks('controlInstance',template.controlInstances); setChecks('action',template.allowedActions); setChecks('logInstance',template.logInstances);
       form.elements.viewConfig.checked = template.readPermissions.config; form.elements.viewTraffic.checked = template.readPermissions.traffic; form.elements.allowLogs.checked = template.allowLogs;
       updateForm(form);
       return;
      }
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
      requireWritable();
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
      const payload = {action:'save',revision,id:user?.id || '',username:form.elements.username.value,role,groupId:form.elements.groupId.value,enabled:form.elements.enabled.checked,expiresAt:expiry ? (expiry.length === 16 ? expiry + ':00Z' : expiry + 'Z') : '',password,
       instances:role === 'admin' ? [] : select('instance'),controlInstances:role === 'operator' ? select('controlInstance') : [],allowedActions:role === 'operator' ? select('action') : [],
       readPermissions:role === 'admin' ? null : {config:form.elements.viewConfig.checked,traffic:form.elements.viewTraffic.checked},allowLogs:role !== 'admin' && form.elements.allowLogs.checked,
       logInstances:role !== 'admin' && form.elements.allowLogs.checked ? select('logInstance') : [],remoteInstances:role === 'admin' ? [] : chosen.map(choice => ({...choice.grant,control:role === 'operator' && !!choice.grant.control}))};
      requireWritable();
      if (model !== source || model.revision !== revision) throw new Error('账户列表已变化，请重新打开编辑窗口。');
      let result;
      try { result = await request('accounts',payload,30000); }
      catch (error) { if (uncertain(error)) { saveUncertain = true; if (current()) void loadAccounts(); throw new Error('保存结果未确认，请关闭窗口并刷新账户后核对，勿重复提交；若账户文件写入已冻结，请安全重启工作台。'); } throw error; }
      const revocation = recordRevocationWarning(result,`账户「${user?.username || payload.username}」的变更`);
      if (formCurrent(form)) { form.elements.password.value = ''; if (!revocation) K.toast('账户已保存。'); await loadAccounts(); }
     }
    });
   };

   search.addEventListener('input',renderList);
   filter.addEventListener('change',renderList);
   groupFilter.addEventListener('change',renderList);
   root.addEventListener('change', async event => {
    const select = event.target.closest('[data-account-group]');
    if (!select || !root.contains(select)) return;
    try {
     requireCurrent();
     requireWritable();
     const source = model, user = source?.accounts.find(item => item.id === select.dataset.accountGroup), groupId = select.value;
     if (!user || !source || groupBusy || deleting) throw new Error('账户列表已变化或操作尚未结束，请刷新后重试。');
     if (groupId && !source.groups.some(group => group.id === groupId)) throw new Error('分组已变化，请刷新后重试。');
     if (groupId === (user.groupId || '')) return;
     groupBusy = true; renderGroups(); renderList();
     try {
      await request('accounts',{action:'group-assign',revision:source.revision,id:user.id,groupId},30000);
      if (current()) K.toast('账户分组已更新。');
     } catch (error) {
      if (current()) K.toast(uncertain(error) ? '分组变更结果未确认，请刷新后核对。' : error.message);
     } finally {
      groupBusy = false;
      if (current()) await loadAccounts();
     }
    } catch (error) {
     if (current()) { K.toast(error.message); renderList(); }
    }
   });
   root.addEventListener('click', async event => {
    const button = event.target.closest('[data-account]');
    if (!button || !root.contains(button) || button.disabled) return;
    const action = button.dataset.account;
    try {
     if (['new','edit','delete','group-new','group-edit','group-delete'].includes(action)) requireWritable();
     if (groupBusy && ['reload','new','edit','delete','frp-tokens','revoke-sessions','group-new','group-edit','group-delete'].includes(action)) throw new Error('账户分组操作尚未结束，请稍后再试。');
     if (action === 'reload') { await loadAccounts(); return; }
     if (action === 'history') { await loadHistory(); return; }
     if (action === 'revocation-ack') { revocationNotices = []; updateRevocationWarning(); return; }
     if (action === 'frp-reviewed') {
      if (frpPending) return;
      if (frpNeedsReview) {
       requireCurrent();
       if (!frpReviewTarget || writesBlocked() || frpReviewSnapshot !== model) throw new Error('请先刷新账户；若账户写入已冻结，请安全重启后再核对。');
       const target = frpReviewTarget, user = model.accounts.find(item => item.id === target.accountId);
       if (user && user.frpTokens !== undefined && !Array.isArray(user.frpTokens)) throw new Error('该账户的凭据状态无法读取，请刷新后核对。');
       const entries = user ? (user.frpTokens || []).filter(item => item?.instanceId === target.instanceId) : [];
       const observed = !user ? '快照中没有该账户' : entries.length ? `${entries.length} 条凭据记录：${entries.map(item => `${item.status || '状态未知'} / ${item.credentialId || '凭据 ID 未知'}`).join('、')}` : '快照中没有该实例的凭据记录';
       const note = target.tokenLost ? '此前的操作已成功，但一次性令牌未显示。' : '此前请求结果未确认，旧请求可能仍在处理。';
       if (!confirm(`账户 ID：${target.accountId}\n实例 ID：${target.instanceId}\n账户修订号：${model.revision}\n登记快照：${observed}\n${note}刷新只是当时的快照，未收到的明文无法取回。若新操作发生版本冲突，请关闭窗口并刷新复核。\n已核对并理解上述状态？`)) return;
      }
      frpNeedsReview = false; frpReviewNotice = false; frpReviewTarget = null; frpReviewSnapshot = null; updateFRPWarning(); return;
     }
     if (action === 'group-new') { editGroup(null); return; }
     if (action === 'group-edit') {
      const group = model?.groups.find(item => item.id === button.dataset.id);
      if (!group) throw new Error('分组已变化，请刷新。');
      editGroup(group); return;
     }
     if (action === 'group-delete') {
      requireCurrent();
      const source = model, group = source?.groups.find(item => item.id === button.dataset.id);
      if (!group || deleting) throw new Error('分组已变化或账户操作尚未结束，请刷新。');
      const count = source.accounts.filter(user => user.groupId === group.id).length;
      if (!confirm(`删除分组「${group.name}」？${count} 个账户将变为未分组，角色和权限不变。`)) return;
      groupBusy = true; renderGroups(); renderList();
      try {
       await request('accounts',{action:'group-delete',revision:source.revision,id:group.id},30000);
       if (current()) K.toast('分组已删除。');
      } catch (error) {
       if (current()) K.toast(uncertain(error) ? '删除结果未确认，请刷新后核对。' : error.message);
      } finally {
       groupBusy = false;
       if (current()) await loadAccounts();
      }
      return;
     }
     if (action === 'new') { editAccount(null); return; }
     if (action === 'edit') {
      const user = model?.accounts.find(item => item.id === button.dataset.id);
      if (!user) throw new Error('账户已变化，请刷新。');
      editAccount(user); return;
     }
     if (action === 'frp-tokens') {
      const user = model?.accounts.find(item => item.id === button.dataset.id);
      if (!user) throw new Error('账户已变化，请刷新。');
      manageFRP(user); return;
     }
     if (action === 'revoke-sessions') {
      requireCurrent();
      const source = model, user = source?.accounts.find(item => item.id === button.dataset.id);
      if (!source || !user || deleting) throw new Error('账户已变化或操作尚未结束，请刷新后重试。');
      const self = ownAccount(user);
      if (!confirm(`撤销「${user.username}」现有的本机工作台会话？${self ? '\n当前浏览器也将退出登录。' : ''}\n不会禁用账户或更改密码，也不撤销 frp 连接及节点授权。已受理的操作可能继续，正在进行或之后的登录仍可能建立新会话。`)) return;
      deleting = true; button.disabled = true;
      try {
       const result = await request('accounts',{action:'revoke-sessions',revision:source.revision,id:user.id},30000);
       if (result?.ok !== true || result.revision !== source.revision) throw new Error('响应无法确认会话撤销结果。');
       if (self && K.epoch() === epoch) {
        K.endCurrentSession(); document.querySelector('#login-error').textContent = '工作台会话已撤销，请重新登录。';
       } else if (current()) K.toast('现有工作台会话已撤销；有效凭据仍可重新登录。');
      } catch (error) {
       if (uncertain(error)) {
        if (self && K.epoch() === epoch) {
         K.endCurrentSession(); document.querySelector('#login-error').textContent = '撤销结果未确认，请重新登录；本页面无法核实旧会话是否已撤销。';
        } else if (current()) K.toast('撤销结果未确认；本页面无法核实旧会话是否已撤销，请按需停用账户或重置密码。');
       } else if (current()) {
        K.toast(error.message);
        if (error.status === 409) await loadAccounts();
       }
      } finally { deleting = false; if (button.isConnected) button.disabled = false; }
      return;
     }
     if (action === 'delete') {
      requireCurrent();
      const source = model, user = source?.accounts.find(item => item.id === button.dataset.id);
      if (!user || ownAccount(user)) throw new Error('不能删除当前账户，或账户已变化。');
      if (deleting || !confirm(`删除账户「${user.username}」并撤销其工作台会话？\n当前不会断开已有 frp 连接。`)) return;
      deleting = true; button.disabled = true;
      try { const result = await request('accounts',{action:'delete',revision:source.revision,id:user.id},30000); const revocation = recordRevocationWarning(result,`账户「${user.username}」的删除`); if (current()) { if (!revocation) K.toast('账户已删除。'); await loadAccounts(); } }
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
   updateFRPWarning(); updateRevocationWarning();
   void loadAccounts(); void loadHistory();
  },
  dispose() { disposeFRP?.(); activeFRPWarning = null; activeRevocationWarning = null; refreshActiveAccounts = null; }
 });
})();
