'use strict';

(() => {
 const K = window.Kurumi, esc = K.esc;
 const idOK = value => /^[a-f0-9]{16}$/.test(value || '');
 const credentialOK = value => /^[a-f0-9]{64}$/.test(value || '');
 const states = {pending:'尚无报告', online:'近期收到报告', offline:'近期无报告', revoked:'已撤销'};
 const instanceStates = {running:'运行中', stopped:'已停止', exited:'已退出', 'autostart-failed':'自动启动失败', starting:'启动中', stopping:'停止中'};
 let cleanup = null, needsAttention = () => false;

 K.register('reported-nodes', {
  render(data) {
   if (data.identity?.role !== 'admin') return '<section class="panel"><div class="panel-content">此页面仅供管理员使用。</div></section>';
   return `<header class="page-head"><div><p class="eyebrow">NODES / REPORTING</p><h1>主动上报</h1><p>接收其他设备的节点摘要，或将本机摘要上报给控制端。</p></div></header>
    <section class="notice">此列表与“节点管理”中的拉取节点相互独立，不支持远程控制。状态是收到报告的观察，不能证明业务可达。报告可能延迟，最近接收时间不是采集时间。</section>
    <section class="panel" data-issued hidden><div class="panel-content"><h2>请保存本次凭据</h2><p class="small-note" data-issued-label></p><p class="small-note">凭据只在本次显示，不会再次返回。请通过安全渠道交给对应设备；离开页面或丢失响应后需重新轮换。关闭页面不能撤销已提交的操作。</p><label>节点凭据<textarea data-credential readonly rows="2" spellcheck="false" autocomplete="off"></textarea></label><div class="actions"><button type="button" data-action="clear-credential">已保存，清除显示</button></div></div></section>
    <section class="panel"><div class="panel-content"><div class="section-heading"><div><h2>接收其他节点</h2><p>为每台设备单独登记和签发凭据，最多 32 个。</p></div><button type="button" data-action="reload">刷新列表</button></div>
     <p class="small-note">页面可见且没有操作时，约每 30 秒读取本机接收列表，也可手动刷新。展开摘要、选择文字或列表项保持焦点时会暂缓自动读取。近期收到报告表示接收端约 90 秒内收到过新报告；没有收到新报告后标记为近期无报告。工作台重启、轮换或撤销后，本进程中的报告摘要会清除。</p>
     <p class="small-note" data-freshness role="status">尚未读取节点快照。</p>
     <form data-issue-form><div class="form-grid"><label>节点名称<input name="name" required maxlength="128" autocomplete="off" placeholder="用于区分设备的名称"><small>1–64 个字符；允许同名，节点 ID 才是身份。</small></label><div class="actions"><button class="primary" type="submit" data-issue>登记并签发凭据</button></div></div></form>
     <p class="small-note" data-message role="status"></p><div data-list>正在读取节点…</div></div></section>
    <section class="panel" data-out><div class="panel-content"><div class="section-heading"><div><h2>本机向控制端上报</h2><p>本机可向一个独立的控制端发送实例摘要。</p></div><button type="button" data-out-action="reload">刷新设置</button></div>
     <p class="small-note">在控制端登记本机并安全取得节点 ID 和一次性凭据。这里填写控制端的 HTTPS 来源地址；先保存，再逐个核对并确认解析地址，最后启用。保存设置会停用上报。对方收到的报告不提供远程控制权限。</p>
     <p class="small-note" data-out-message role="status"></p><div data-out-list>正在读取上报设置…</div><div data-out-candidate></div>
     <form data-out-form hidden><div class="form-grid"><label>控制端 HTTPS 来源地址<input name="url" type="url" required autocomplete="off" placeholder="https://controller.example.com"><small>不含路径、查询或片段；控制端须配置可信 HTTPS 代理。</small></label><label>控制端签发的节点 ID<input name="nodeId" required pattern="[a-f0-9]{16}" maxlength="16" autocomplete="off"></label>
      <label class="span-2">节点凭据<input name="credential" type="password" autocomplete="new-password" spellcheck="false"><small>新目标或更换目标时必填；地址和 ID 均未改变时留空保留原凭据。输入不会再次显示。</small></label>
      <label class="form-check span-2"><input name="shareTraffic" type="checkbox">允许发送本次运行的流量样本</label></div><p class="small-note">流量为可选样本，不是计费或配额数据。保存时如有报告已发出，不能撤回。</p><div class="actions"><button class="primary" type="submit" data-out-save>保存并停用</button><button type="button" data-out-action="cancel">取消</button></div></form>
     </div></section>`;
  },
  mount(root, data) {
   if (data.identity?.role !== 'admin') return;
   const epoch = K.epoch(), requests = new Set();
   const list = root.querySelector('[data-list]'), message = root.querySelector('[data-message]'), freshness = root.querySelector('[data-freshness]');
   const form = root.querySelector('[data-issue-form]'), issue = root.querySelector('[data-issue]');
   const secretPanel = root.querySelector('[data-issued]'), secret = root.querySelector('[data-credential]');
   const current = () => K.epoch() === epoch && K.isCurrent('reported-nodes', root) && K.state()?.identity?.role === 'admin';
   let model = null, pending = false, loading = false, issued = false;
   let automatic = null, automaticTimer = null, lastRead = null, readFailed = false;
   const uncertain = error => error.name === 'AbortError' || !error.status || error.status >= 500;
   function cancelAutomatic() {
    if (automatic) { automatic.cancelled = true; automatic.controller.abort(); }
   }
   async function request(path, body, automaticController) {
    if (!automaticController && automatic) {
     const previous = automatic;
     cancelAutomatic();
     await previous.done;
    }
    if (!current()) throw new DOMException('页面已离开', 'AbortError');
    const controller = automaticController || new AbortController(), timer = setTimeout(() => controller.abort(), 20000);
    requests.add(controller);
    try { return await K.api(path, body, {signal:controller.signal}); }
    finally { clearTimeout(timer); requests.delete(controller); }
   }
   function controls() {
    issue.disabled = pending || loading || !model || issued;
    form.elements.name.disabled = pending;
    root.querySelector('[data-action="reload"]').disabled = pending || loading;
    list.querySelectorAll('button').forEach(button => { button.disabled = pending || loading || issued || !model; });
   }
   function showFreshness() {
    // Do not replace text while the user may be selecting it.
    if (window.getSelection()?.toString()) return;
    let text;
    if (!lastRead) {
     text = readFailed ? '读取失败，尚无可显示的节点快照。请刷新重试。' : '尚未读取节点快照。';
    } else {
     const age = Date.now() - lastRead, stale = age < 0 || age >= 90000;
     text = `本机列表最近读取：${new Date(lastRead).toLocaleString('zh-CN')}。` +
      (readFailed ? '最近读取失败，当前列表可能过期，操作已停用。' : stale ? '列表已过期，请刷新核对。' : '这是读取时的快照。');
    }
    if (freshness.textContent !== text) freshness.textContent = text;
   }
   function validList(value) {
    return Number.isSafeInteger(value?.revision) && value.revision >= 1 && Array.isArray(value.nodes) && !value.nodes.some(node => !node || !idOK(node.id) || typeof node.name !== 'string' || typeof node.state !== 'string');
   }
   function acceptList(value) {
    if (!validList(value)) throw new Error('节点列表格式无效。');
    model = value; lastRead = Date.now(); readFailed = false;
    renderList(); showFreshness();
   }
   const time = value => value ? esc(value) : '尚无';
   function summary(node) {
    if (!node.summary || !Array.isArray(node.summary.instances)) return '<p class="small-note">暂无报告摘要。</p>';
    const instances = node.summary.instances;
    if (!instances.length) return '<p class="small-note">最近报告中没有实例。</p>';
    const bytes = value => typeof value === 'string' && /^\d+$/.test(value) ? `${esc(value)} 字节` : '未知';
    return `<details><summary>最近报告摘要（${instances.length} 个实例）</summary><p class="small-note">工作台版本：${esc(node.summary.version || '未知')}。流量为对方可选提供的本次运行样本，不能用于计费或配额。</p><div class="table-wrap"><table><thead><tr><th>实例</th><th>类型</th><th>报告中的状态</th><th>可选流量</th></tr></thead><tbody>${instances.map(item => {
     const traffic = item?.traffic;
     return `<tr><td>${esc(item?.name || '')}<br><span class="small-note">${esc(item?.id || '')}</span></td><td>${esc(item?.kind || '未知')}</td><td>${esc(instanceStates[item?.state] || item?.state || '未知')}</td><td>${traffic ? `收 ${bytes(traffic.receivedBytes)} / 发 ${bytes(traffic.sentBytes)}<br><span class="small-note">对方自报流量采样时间 ${time(traffic.sampledAt)}</span>` : '未提供'}</td></tr>`;
    }).join('')}</tbody></table></div></details>`;
   }
   function renderList() {
    if (!model.nodes.length) { list.innerHTML = '<p>尚未登记主动上报节点。</p>'; controls(); return; }
    list.innerHTML = `<div class="cards">${model.nodes.map(node => `<article class="card"><h3>${esc(node.name)}</h3><p class="small-note">节点 ID：${esc(node.id)}</p><p><span class="pill">${esc(states[node.state] || '状态未知')}</span> <span class="small-note">最近接收时间：${time(node.lastSeen)}</span></p>${summary(node)}<div class="actions"><button type="button" data-action="rename" data-id="${esc(node.id)}">重命名</button><button type="button" data-action="rotate" data-id="${esc(node.id)}">轮换凭据</button>${node.state === 'revoked' ? '' : `<button type="button" data-action="revoke" data-id="${esc(node.id)}">撤销凭据</button>`}<button class="danger" type="button" data-action="delete" data-id="${esc(node.id)}">删除节点</button></div></article>`).join('')}</div>`;
    controls();
   }
   async function load() {
    if (!current() || pending || loading) return;
    loading = true; model = null; lastRead = null; readFailed = false;
    list.textContent = '正在读取节点…'; controls(); showFreshness();
    try {
     const value = await request('node-agents');
     if (!current()) return;
     acceptList(value);
    } catch (error) {
     if (current()) {
      readFailed = true; showFreshness();
      list.textContent = '读取节点失败：' + (error.name === 'AbortError' ? '请求超时，请刷新重试。' : error.message);
     }
    } finally { loading = false; if (current()) controls(); }
   }
   function showCredential(value) {
    issued = true;
    root.querySelector('[data-issued-label]').textContent = `${value.node.name} · 节点 ID：${value.node.id}`;
    secret.value = value.credential; secretPanel.hidden = false;
    secretPanel.scrollIntoView({block:'nearest'});
   }
   async function change(action, node, name) {
    if (!current() || pending || loading || issued || !model) return;
    const revision = model.revision;
    pending = true; message.textContent = '正在提交；关闭页面不能保证取消操作。'; controls();
    try {
     const body = {action, revision};
     if (name !== undefined) body.name = name;
     if (node) body.id = node.id;
     const value = await request('node-agents', body);
     if (!current()) return;
     if (value?.ok !== true || !Number.isSafeInteger(value.revision) || value.revision <= revision) throw new Error('操作响应无效。');
     if (action === 'issue' || action === 'rotate') {
      if (!idOK(value.node?.id) || typeof value.node?.name !== 'string' || !credentialOK(value.credential) || (node && value.node.id !== node.id)) throw new Error('凭据响应无效。');
      showCredential(value);
     }
     if (action === 'rename' && (value.node?.id !== node.id || value.node?.name !== name)) throw new Error('重命名响应无效。');
     if (action === 'issue') form.elements.name.value = '';
     message.textContent = {issue:'节点已登记。', rename:'节点名称已更新。', rotate:'凭据已轮换，旧凭据和会话已失效。', revoke:'凭据已撤销。', delete:'节点已删除，原 ID 不再使用。'}[action];
    } catch (error) {
     if (current()) message.textContent = uncertain(error) ? (action === 'issue' || action === 'rotate' ? '操作结果未确认。请刷新列表并核对；签发或轮换可能已经生效，若凭据丢失请对正确的节点重新轮换。' : '操作结果未确认，请刷新列表并核对。') : `${error.message} 请刷新列表后核对。`;
    } finally {
     pending = false;
     if (current()) { controls(); void load(); }
    }
   }
   const validName = name => !!name && name === name.trim() && [...name].length <= 64 && new TextEncoder().encode(name).length <= 256 && !/[\x00-\x1f\x7f-\x9f]/.test(name);
   form.addEventListener('submit', event => {
    event.preventDefault();
    const name = form.elements.name.value;
    if (!validName(name)) {
     message.textContent = '名称须为 1–64 个字符，首尾不能有空格或包含控制字符。'; return;
    }
    void change('issue', null, name);
   });
   root.addEventListener('click', event => {
    const button = event.target.closest('[data-action]');
    if (!button || !root.contains(button) || button.disabled) return;
    const action = button.dataset.action;
    if (action === 'clear-credential') { secret.value = ''; issued = false; secretPanel.hidden = true; controls(); return; }
    if (action === 'reload') { void load(); return; }
    const node = model?.nodes.find(item => item.id === button.dataset.id);
    if (!node || !['rename','rotate','revoke','delete'].includes(action)) return;
    if (action === 'rename') {
     const name = prompt('输入新的节点名称：', node.name);
     if (name === null) return;
     if (!validName(name)) { message.textContent = '名称须为 1–64 个字符，首尾不能有空格或包含控制字符。'; return; }
     if (name === node.name) { message.textContent = '节点名称未变化。'; return; }
     void change(action, node, name); return;
    }
    const question = {rotate:`轮换「${node.name}」的凭据？旧凭据和会话立即失效，设备需要配置新凭据。`, revoke:`撤销「${node.name}」的凭据？该设备将不能继续上报。`, delete:`删除「${node.name}」？其凭据和会话将失效，节点 ID 不再使用。`}[action];
    if (confirm(question)) void change(action, node);
   });
   const outRoot = root.querySelector('[data-out]'), outList = root.querySelector('[data-out-list]');
   const outForm = root.querySelector('[data-out-form]'), outMessage = root.querySelector('[data-out-message]');
   const outCandidate = root.querySelector('[data-out-candidate]');
   const outStates = {disabled:'已停用', connecting:'正在建立会话', sending:'正在发送', waiting:'等待下次上报', retrying:'尚未确认，正在重试', blocked:'上报受阻'};
   let outModel = null, outPending = false, outLoading = false, editing = false, candidate = null;
   function outControls() {
    outRoot.querySelectorAll('[data-out-action], [data-out-save]').forEach(button => {
     const needsAddresses = button.dataset.outAction === 'enable' && !outModel?.reporter?.addresses?.length;
     button.disabled = outPending || outLoading || needsAddresses || (editing && button.dataset.outAction !== 'cancel' && !button.hasAttribute('data-out-save'));
    });
    outForm.querySelectorAll('input').forEach(input => { input.disabled = outPending; });
   }
   function renderCandidate() {
    outCandidate.innerHTML = candidate ? `<div class="notice"><p>请独立核对下列全部 IP 是否属于控制端，再确认。确认时会重新解析，结果变化则拒绝。</p><ul>${candidate.addresses.map(address => `<li>${esc(address)}</li>`).join('')}</ul><button type="button" data-out-action="confirm">确认这些地址</button></div>` : '';
    outControls();
   }
   function renderOut() {
    const item = outModel.reporter;
    if (!item.configured) {
     outList.innerHTML = '<p>尚未配置本机主动上报。</p><button type="button" data-out-action="edit">配置上报</button>';
    } else {
     const status = item.status || {}, addresses = item.addresses.length ? item.addresses.map(esc).join('、') : '尚未确认';
     outList.innerHTML = `<p>控制端：${esc(item.url)}<br>节点 ID：${esc(item.nodeId)}<br>确认地址：${addresses}<br>流量共享：${item.shareTraffic ? '开启' : '关闭'}</p><p><span class="pill">${esc(outStates[status.state] || '状态未知')}</span> ${esc(status.message || '')}</p><p class="small-note">本机最近收到确认：${time(status.lastSuccessAt)}。确认只表示当时对端已接收，不表示对端现在在线或业务可达。</p><div class="actions"><button type="button" data-out-action="edit">修改设置</button>${item.enabled ? '<button type="button" data-out-action="disable">停用上报</button>' : '<button type="button" data-out-action="resolve">核对解析地址</button><button type="button" data-out-action="enable">启用上报</button>'}<button type="button" class="danger" data-out-action="delete">删除设置</button></div>`;
    }
    renderCandidate();
   }
   async function loadOut() {
    if (!current() || outPending || outLoading || editing) return;
    outLoading = true; outModel = null; candidate = null; outCandidate.replaceChildren(); outList.textContent = '正在读取上报设置…'; outControls();
    try {
     const value = await request('nodes');
     if (!current()) return;
     const item = value?.reporter;
     if (!Number.isSafeInteger(value?.revision) || value.revision < 1 || !item || typeof item.configured !== 'boolean' || !Array.isArray(item.addresses) || item.addresses.some(address => typeof address !== 'string') || typeof item.enabled !== 'boolean' || typeof item.shareTraffic !== 'boolean' || (item.configured && (typeof item.url !== 'string' || !idOK(item.nodeId) || !credentialOK(item.stamp)))) throw new Error('上报设置格式无效。');
     outModel = {revision:value.revision, reporter:item}; renderOut();
    } catch (error) {
     if (current()) outList.textContent = '读取上报设置失败：' + (error.name === 'AbortError' ? '请求超时，请刷新重试。' : error.message);
    } finally { outLoading = false; if (current()) outControls(); }
   }
   function editOut() {
    if (!outModel || outPending || outLoading) return;
    const item = outModel.reporter;
    editing = true; candidate = null; outCandidate.replaceChildren();
    outForm.elements.url.value = item.url || ''; outForm.elements.nodeId.value = item.nodeId || '';
    outForm.elements.credential.value = ''; outForm.elements.shareTraffic.checked = item.shareTraffic;
    outForm.hidden = false; outMessage.textContent = ''; outControls(); outForm.elements.url.focus();
   }
   function cancelOut() {
    outForm.elements.credential.value = ''; outForm.reset(); outForm.hidden = true; editing = false; outControls();
   }
   async function changeOut(action, extra = {}) {
    if (!current() || outPending || outLoading || !outModel) return;
    const source = outModel, revision = source.revision, stamp = source.reporter.stamp || '';
    outPending = true; outMessage.textContent = '正在提交；关闭页面不能保证取消操作。'; outControls();
    try {
     const value = await request('nodes', {action, revision, stamp, ...extra});
     if (!current()) return;
     if (value?.ok !== true || !Number.isSafeInteger(value.revision) || value.revision <= revision) throw new Error('操作响应无效。');
     if (editing) cancelOut();
     outMessage.textContent = { 'reporter-save':'设置已保存，上报已停用；请核对地址后启用。', 'reporter-confirm':'解析地址已确认，可启用上报。', 'reporter-enable':extra.enabled ? '已启用上报。' : '已停用上报。', 'reporter-delete':'上报设置已删除。' }[action];
     void K.refresh(true).catch(() => {});
    } catch (error) {
     if (current()) {
      if (uncertain(error) || error.status === 409) {
       if (editing) cancelOut();
       outMessage.textContent = uncertain(error) ? '操作结果未确认，请刷新设置并核对后再决定是否重试。' : `${error.message} 请刷新后重试。`;
      } else outMessage.textContent = error.message + (action === 'reporter-save' ? ' 凭据输入已清除；如需更换请重新填写。' : '');
     }
    } finally {
     outPending = false; candidate = null;
     if (current()) { outControls(); if (!editing) void loadOut(); }
    }
   }
   async function resolveOut() {
    if (!current() || outPending || outLoading || !outModel?.reporter.configured || outModel.reporter.enabled) return;
    const source = outModel, stamp = source.reporter.stamp;
    outPending = true; candidate = null; outMessage.textContent = '正在解析目标地址，不会发送凭据。'; renderCandidate();
    try {
     const value = await request('nodes', {action:'reporter-resolve', revision:source.revision, stamp});
     if (!current()) return;
     if (value?.revision !== source.revision || value?.stamp !== stamp || !Array.isArray(value.addresses) || !value.addresses.length || value.addresses.length > 16 || value.addresses.some(address => typeof address !== 'string')) throw new Error('解析结果无效，请刷新设置后重试。');
     candidate = value; outMessage.textContent = '请独立核对全部解析地址。';
    } catch (error) { if (current()) outMessage.textContent = error.name === 'AbortError' ? '解析超时，请重试。' : error.message; }
    finally { outPending = false; if (current()) renderCandidate(); }
   }
   outForm.addEventListener('submit', event => {
    event.preventDefault();
    if (!editing || !outModel || outPending) return;
    const url = outForm.elements.url.value, nodeId = outForm.elements.nodeId.value, credential = outForm.elements.credential.value;
    const previous = outModel.reporter;
    if (!idOK(nodeId) || (credential && !credentialOK(credential)) || (!credential && (!previous.configured || previous.url !== url || previous.nodeId !== nodeId))) {
     outMessage.textContent = '请填写有效的节点 ID；新目标须填写 64 位小写十六进制节点凭据。'; return;
    }
    outForm.elements.credential.value = '';
    void changeOut('reporter-save', {url, nodeId, credential, shareTraffic:outForm.elements.shareTraffic.checked});
   });
   outRoot.addEventListener('click', event => {
    const button = event.target.closest('[data-out-action]');
    if (!button || !outRoot.contains(button) || button.disabled) return;
    const action = button.dataset.outAction, item = outModel?.reporter;
    if (action === 'reload') { void loadOut(); return; }
    if (action === 'cancel') { cancelOut(); return; }
    if (action === 'edit') { editOut(); return; }
    if (action === 'resolve') { void resolveOut(); return; }
    if (action === 'confirm' && candidate && confirm('已独立核对列表中的全部地址属于控制端？')) { void changeOut('reporter-confirm', {addresses:candidate.addresses}); return; }
    if (action === 'enable' && item?.configured && confirm('启用本机主动上报？摘要会发送到已确认地址。')) { void changeOut('reporter-enable', {enabled:true}); return; }
    if (action === 'disable' && item?.configured && confirm('停用本机主动上报？已经发出的报告无法撤回。')) { void changeOut('reporter-enable', {enabled:false}); return; }
    if (action === 'delete' && item?.configured && confirm('删除本机上报设置并停用上报？已经发出的报告无法撤回。')) void changeOut('reporter-delete');
   });
   function autoPaused() {
    const active = document.activeElement;
    const editingField = active && (form.contains(active) || outForm.contains(active)) && active.matches('input, textarea, select, [contenteditable="true"]');
    return !current() || document.hidden || pending || loading || issued || outPending || outLoading || editing || !!candidate ||
     !!form.elements.name.value || requests.size !== 0 || list.contains(active) || !!editingField ||
     !!list.querySelector('details[open]') || !!document.querySelector('dialog[open]') || !!window.getSelection()?.toString();
   }
   function scheduleAutomatic(delay = 30000) {
    clearTimeout(automaticTimer); automaticTimer = null;
    if (!current() || document.hidden) return;
    automaticTimer = setTimeout(() => {
     automaticTimer = null; showFreshness();
     if (automatic || autoPaused()) { scheduleAutomatic(); return; }
     const task = {controller:new AbortController(), cancelled:false, done:null};
     automatic = task;
     task.done = (async () => {
      try {
       const value = await request('node-agents', undefined, task.controller);
       if (!task.cancelled && !task.controller.signal.aborted && !autoPaused()) acceptList(value);
      } catch (_) {
       if (!task.cancelled && !autoPaused()) {
        readFailed = true; model = null; controls(); showFreshness();
       }
      } finally {
       if (automatic === task) automatic = null;
       if (current()) { showFreshness(); scheduleAutomatic(); }
      }
     })();
    }, delay);
   }
   const visibilityChanged = () => {
    if (document.hidden) { clearTimeout(automaticTimer); automaticTimer = null; cancelAutomatic(); }
    else if (current()) { showFreshness(); scheduleAutomatic(0); }
   };
   const selectionChanged = () => {
    if (window.getSelection()?.toString()) cancelAutomatic();
    else if (current()) showFreshness();
   };
   root.addEventListener('focusin', cancelAutomatic);
   root.addEventListener('pointerdown', cancelAutomatic);
   root.addEventListener('input', cancelAutomatic);
   root.addEventListener('toggle', cancelAutomatic, true);
   document.addEventListener('visibilitychange', visibilityChanged);
   document.addEventListener('selectionchange', selectionChanged);
   needsAttention = () => pending || issued || !!form.elements.name.value || outPending || editing || !!candidate;
   const beforeUnload = event => { if (needsAttention()) { event.preventDefault(); event.returnValue = ''; } };
   window.addEventListener('beforeunload', beforeUnload);
   cleanup = () => {
    clearTimeout(automaticTimer); cancelAutomatic();
    document.removeEventListener('visibilitychange', visibilityChanged);
    document.removeEventListener('selectionchange', selectionChanged);
    requests.forEach(controller => controller.abort());
    window.removeEventListener('beforeunload', beforeUnload);
    secret.value = ''; form.elements.name.value = ''; outForm.elements.credential.value = '';
    needsAttention = () => false;
   };
   void load(); void loadOut(); scheduleAutomatic();
  },
  hasUnsaved() { return needsAttention(); },
  dispose() { const fn = cleanup; cleanup = null; if (fn) fn(); }
 });
})();
