'use strict';

// This page observes the updater's real status. It never retries an install.
(() => {
 const K = window.Kurumi;
 const escape = K.esc;
 const storageKey = 'kurumi-update-pending';
 let active = null;
 let posting = false;
 let dismissedStatus = '';
 let storageAvailable = true;

 function readPending() {
  try {
   const value = JSON.parse(localStorage.getItem(storageKey) || 'null');
   if (!value || !['submitting', 'accepted', 'uncertain'].includes(value.phase)) return null;
   if (typeof value.tag !== 'string' || (value.tag && !/^v[0-9]{1,5}\.[0-9]{1,5}\.[0-9]{1,5}(?:\.[0-9]{1,5})?$/.test(value.tag))) return null;
   return {tag:value.tag, phase:value.phase === 'submitting' ? 'uncertain' : value.phase};
  } catch (_) { return null; }
 }
 let pending = readPending();

 function setPending(value) {
  pending = value;
  try {
   if (value) localStorage.setItem(storageKey, JSON.stringify(value));
   else localStorage.removeItem(storageKey);
  } catch (_) { storageAvailable = false; }
 }
 function current(view) {
  return active === view && K.isCurrent('update', view.root) && view.epoch === K.epoch();
 }
 function running(status) { return typeof status === 'string' && status.startsWith('正在'); }
 function submitted(status) { return typeof status === 'string' && status.startsWith('更新请求已提交'); }
 function failed(status) { return typeof status === 'string' && status.startsWith('更新失败：'); }
 function sameVersion(tag, version) {
  return !!tag && typeof version === 'string' && tag.replace(/^v/, '') === version.replace(/^v/, '');
 }
 function eligible(version) { return !!version && version.newer === true && version.available === true; }
 function versionChannel(version) {
  return version.metadataFallback ? '发布状态待确认' : version.prerelease ? '预发布' : '正式版';
 }
 function noticeText() {
  if (!pending) return '';
  const version = pending.tag ? ' ' + pending.tag : '';
  const storageNote = storageAvailable ? '' : ' 浏览器无法保存此提示，关闭页面后仍须手动核对。';
  if (posting && pending.phase === 'submitting') return '正在提交' + version + '的更新请求，请等待。' + storageNote;
  if (pending.phase === 'accepted') return '更新请求' + version + '已受理，最终安装结果仍需核对。请重新打开工作台并确认当前版本。' + storageNote;
  return '更新请求' + version + '的结果尚未确认。请先核对工作台状态和当前版本，再决定是否重新操作。' + storageNote;
 }
 function stopPoll(view) {
  if (view.timer !== null) clearTimeout(view.timer);
  view.timer = null;
 }
 function schedulePoll(view) {
  stopPoll(view);
  if (!current(view) || document.hidden || !view.statusKnown || !running(view.status) || posting) return;
  view.timer = setTimeout(() => { view.timer = null; void loadStatus(view); }, 2000);
 }
 function syncControls(view) {
  if (!current(view)) return;
  const select = view.root.querySelector('[data-update-choice]');
  const install = view.root.querySelector('[data-update-install]');
  const check = view.root.querySelector('[data-update-check]');
  const statusButton = view.root.querySelector('[data-update-status-refresh]');
  const ack = view.root.querySelector('[data-update-ack]');
  const notice = view.root.querySelector('[data-update-notice]');
  const progress = view.root.querySelector('[data-update-progress]');
  const selected = view.versions.find(item => item.tag === select.value);
  check.disabled = view.listLoading;
  statusButton.disabled = view.statusLoading || posting;
  select.disabled = view.listLoading || !view.versions.some(eligible);
  install.disabled = view.listLoading || view.statusLoading || !view.statusKnown || running(view.status) || posting || !!pending || !eligible(selected);
  notice.hidden = !pending;
  view.root.querySelector('[data-update-notice-text]').textContent = noticeText();
  ack.hidden = !pending;
  ack.disabled = posting || view.statusLoading || (view.statusKnown && running(view.status));
  progress.hidden = !posting && !(view.statusKnown && running(view.status));
 }
 function paintVersions(view) {
  if (!current(view)) return;
  const host = view.root.querySelector('[data-update-versions]');
  const listStatus = view.root.querySelector('[data-update-list-status]');
  const select = view.root.querySelector('[data-update-choice]');
  if (view.listLoading) {
   listStatus.textContent = '正在检查发行版本…';
  } else if (view.listError) {
   listStatus.textContent = '无法读取版本列表：' + view.listError;
  } else if (!view.loaded) {
   listStatus.textContent = '尚未检查版本。';
  } else if (!view.versions.length) {
   listStatus.textContent = '没有可显示的发行版本。';
  } else {
   const confirmed = view.versions.find(eligible);
   const unconfirmed = view.versions.some(version => version.newer === true && version.metadataFallback && !version.available);
   listStatus.textContent = confirmed ? '已确认可安装的新版本：' + confirmed.tag : unconfirmed ? '目前没有已确认可安装的新版本。' : '当前列表中没有可安装的新版本。';
   if (unconfirmed) listStatus.textContent += ' 部分新版本的安装包尚未确认。';
  }
  if (view.loaded && !view.listError && view.versions.length) {
   const currentVersion = K.state()?.version;
   const rows = view.versions.map(version => {
    const relation = sameVersion(version.tag, currentVersion) ? '当前版本' : version.newer ? '较当前版本新' : '非更新版本';
    return `<tr><td><strong>${escape(version.tag)}</strong></td><td>${escape(versionChannel(version))}</td><td>${version.available ? '安装包可用' : version.metadataFallback ? '安装包未确认' : '无可用安装包'}</td><td>${relation}</td></tr>`;
   }).join('');
   host.innerHTML = `<div class="table-wrap"><table><thead><tr><th>版本</th><th>发布状态</th><th>安装包</th><th>版本关系</th></tr></thead><tbody>${rows}</tbody></table></div>`;
  } else host.replaceChildren();
  const available = view.versions.filter(eligible);
  if (!view.selectedByUser) view.selected = available.find(version => !version.prerelease && !version.metadataFallback)?.tag || '';
  else if (view.selected && !available.some(version => version.tag === view.selected)) view.selected = '';
  select.innerHTML = '<option value="">请选择可安装版本</option>' + available.map(version =>
   `<option value="${escape(version.tag)}">${escape(version.tag)} · ${escape(versionChannel(version))}</option>`).join('');
  select.value = view.selected;
  syncControls(view);
 }
 function paintStatus(view) {
  if (!current(view)) return;
  const output = view.root.querySelector('[data-update-status]');
  if (posting) output.textContent = '正在提交更新请求…';
  else if (view.statusLoading) output.textContent = '正在读取服务端更新状态…';
  else if (!view.statusKnown) output.textContent = view.statusError || '更新状态尚未确认。';
  else output.textContent = view.status || '当前没有更新任务。';
  syncControls(view);
 }
 async function loadReleases(view) {
  if (!current(view)) return;
  view.listController?.abort();
  const controller = new AbortController();
  view.listController = controller; view.listLoading = true; view.listError = '';
  paintVersions(view);
  let timedOut = false;
  const timeout = setTimeout(() => { timedOut = true; controller.abort(); }, 35000);
  try {
   const result = await K.api('update/releases', undefined, {signal:controller.signal});
   if (!current(view) || view.listController !== controller) return;
   if (!Array.isArray(result.versions)) throw new Error('服务端返回的版本列表无效。');
   if (!result.versions.every(version => version && typeof version.tag === 'string' &&
    typeof version.newer === 'boolean' && typeof version.available === 'boolean' &&
    typeof version.prerelease === 'boolean')) throw new Error('服务端返回的版本数据无效。');
   view.versions = result.versions;
   view.loaded = true;
  } catch (error) {
   if (!current(view) || view.listController !== controller || (error.name === 'AbortError' && !timedOut)) return;
   view.versions = []; view.loaded = false;
   view.listError = timedOut ? '等待版本列表超时，请稍后手动检查。' : error.message;
  } finally {
   clearTimeout(timeout);
   if (view.listController === controller) {
    view.listController = null; view.listLoading = false;
    if (current(view)) paintVersions(view);
   }
  }
 }
 async function loadStatus(view) {
  if (!current(view) || posting) return;
  stopPoll(view);
  view.statusController?.abort();
  const controller = new AbortController();
  view.statusController = controller; view.statusLoading = true; view.statusError = '';
  paintStatus(view);
  let timedOut = false;
  const timeout = setTimeout(() => { timedOut = true; controller.abort(); }, 10000);
  try {
   const result = await K.api('update/status', undefined, {signal:controller.signal});
   if (!current(view) || view.statusController !== controller) return;
   if (typeof result.status !== 'string') throw new Error('服务端返回的更新状态无效。');
   view.status = result.status; view.statusKnown = true;
   if (failed(view.status)) {
    // A status has no operation ID; retain any notice until the user reviews it.
   } else if (!running(view.status) && pending?.tag && sameVersion(pending.tag, K.state()?.version)) {
    setPending(null);
    if (submitted(view.status)) dismissedStatus = view.status;
   } else if (running(view.status)) {
    if (!pending) setPending({tag:'', phase:'accepted'});
    else if (pending.phase !== 'accepted') setPending({tag:pending.tag, phase:'accepted'});
   } else if (submitted(view.status) && !pending && dismissedStatus !== view.status) {
    setPending({tag:'', phase:'accepted'});
   } else if (pending && !view.status) {
    setPending({tag:pending.tag, phase:'uncertain'});
   }
  } catch (error) {
   if (!current(view) || view.statusController !== controller || (error.name === 'AbortError' && !timedOut)) return;
   view.statusKnown = false;
   view.statusError = (timedOut ? '读取更新状态超时。' : '无法读取更新状态：' + error.message) + ' 请手动核对后再操作。';
   if (pending || running(view.status)) setPending({tag:pending?.tag || '', phase:'uncertain'});
  } finally {
   clearTimeout(timeout);
   if (view.statusController === controller) {
    view.statusController = null; view.statusLoading = false;
    if (current(view)) { paintStatus(view); schedulePoll(view); }
   }
  }
 }
 async function install(view) {
  if (!current(view) || posting || pending || view.listLoading || view.statusLoading || !view.statusKnown || running(view.status)) return;
  const tag = view.root.querySelector('[data-update-choice]').value;
  const selected = view.versions.find(version => version.tag === tag && eligible(version));
  if (!selected) {
   K.toast('所选版本不可安装，请重新检查版本。'); return;
  }
  const channelNote = selected.metadataFallback ? '\n此版本的发布状态尚未确认，请先核对发布页面。' : selected.prerelease ? '\n这是预发布版本，请确认需要安装。' : '';
  if (!confirm(`确认更新到 ${tag}（${versionChannel(selected)}）？更新可能中断工作台和托管连接，安装后请重新打开并核对版本。${channelNote}`)) return;
  if (!current(view) || posting || pending || view.statusLoading || !view.statusKnown || running(view.status) || !view.versions.some(version => version.tag === tag && eligible(version))) return;
  stopPoll(view);
  view.statusController?.abort(); view.statusController = null; view.statusLoading = false;
  posting = true;
  setPending({tag, phase:'submitting'});
  paintStatus(view);
  const controller = new AbortController();
  let timedOut = false;
  const timeout = setTimeout(() => { timedOut = true; controller.abort(); }, 30000);
  try {
   const result = await K.api('update/install', {tag}, {signal:controller.signal});
   if (result.ok !== true) throw new Error('服务端返回的数据无法确认更新请求是否受理。');
   setPending({tag, phase:'accepted'});
  } catch (error) {
   if (error.status >= 400 && error.status < 500) {
    setPending(null);
    if (active && current(active)) active.statusError = '更新请求未受理：' + error.message;
   } else {
    setPending({tag, phase:'uncertain'});
    if (active && current(active)) active.statusError = timedOut
     ? '等待更新请求响应超时，结果尚未确认。请先核对版本和更新状态。'
     : '更新请求结果尚未确认：' + error.message + '。请先核对版本和更新状态。';
   }
  } finally {
   clearTimeout(timeout);
   posting = false;
   if (active && current(active)) {
    const page = active;
    page.statusKnown = false;
    paintStatus(page);
    if (pending?.phase === 'accepted') void loadStatus(page);
   }
  }
 }

 K.register('update', {
  render(data) {
   if (data.identity?.role !== 'admin') {
    return '<header class="page-head"><div><h1>软件更新</h1></div></header><section class="panel"><p>当前账户没有管理软件更新的权限。</p></section>';
   }
   return `<header class="page-head"><div><p class="eyebrow">WORKSPACE / UPDATE</p><h1>软件更新</h1><p>检查发行版本，并按当前安装方式选择可用的新版本。</p></div></header>
    <div class="two-column"><section class="panel"><div class="panel-content"><h2>当前版本</h2><p><strong>${escape(data.version || '未知')}</strong></p><p class="small-note">更新可能中断工作台和托管连接。安装请求受理不代表更新已经完成；请重新打开工作台确认版本。</p></div></section>
    <section class="panel"><div class="panel-content"><div class="section-heading"><h2>更新状态</h2><button type="button" data-update-status-refresh>读取状态</button></div><p data-update-status role="status" aria-live="polite">正在读取服务端更新状态…</p><progress data-update-progress aria-label="更新正在进行" hidden></progress><p class="small-note">服务端提供执行阶段，不提供下载百分比。</p></div></section></div>
    <div class="notice" role="status" data-update-notice hidden><p data-update-notice-text></p><button type="button" data-update-ack>我已核对，解除提示</button></div>
    <section class="panel"><div class="panel-content"><div class="section-heading"><div><h2>发行版本</h2><p>仅可安装新于当前版本且有适用安装包的版本。</p></div><button type="button" data-update-check>检查版本</button></div>
    <p class="small-note" data-update-list-status role="status">正在检查发行版本…</p><div data-update-versions></div><label>选择更新版本<select data-update-choice disabled><option value="">请选择可安装版本</option></select></label><div class="actions"><button class="primary" type="button" data-update-install disabled>安装所选版本</button></div><p class="small-note">默认选择可安装的正式版；预发布及发布状态待确认的版本需手动选择。更新可能需要系统授权。</p></div></section>`;
  },
  mount(root, data) {
   if (data.identity?.role !== 'admin') return;
   const view = {root, epoch:K.epoch(), versions:[], selected:'', selectedByUser:false, loaded:false,
    listLoading:false, listError:'', listController:null, status:'', statusKnown:false, statusError:'',
    statusLoading:false, statusController:null, timer:null};
   active = view;
   const choice = root.querySelector('[data-update-choice]');
   choice.addEventListener('change', () => { view.selected = choice.value; view.selectedByUser = true; syncControls(view); });
   root.querySelector('[data-update-check]').addEventListener('click', () => { void loadReleases(view); });
   root.querySelector('[data-update-status-refresh]').addEventListener('click', () => { void loadStatus(view); });
   root.querySelector('[data-update-install]').addEventListener('click', () => { void install(view); });
   root.querySelector('[data-update-ack]').addEventListener('click', () => {
    if (!pending || posting || view.statusLoading || (view.statusKnown && running(view.status))) return;
    if (!confirm('确认已经核对工作台状态和当前版本？解除提示不会取消可能仍在执行的更新。')) return;
    if (submitted(view.status)) dismissedStatus = view.status;
    setPending(null); syncControls(view);
   });
   view.onVisibility = () => {
    if (document.hidden) stopPoll(view);
    else if (current(view) && view.statusKnown && running(view.status) && !posting) void loadStatus(view);
   };
   view.onStorage = event => {
    if (event.key !== storageKey || posting || !current(view)) return;
    pending = readPending(); syncControls(view);
   };
   document.addEventListener('visibilitychange', view.onVisibility);
   window.addEventListener('storage', view.onStorage);
   paintVersions(view); paintStatus(view);
   void loadReleases(view);
   if (!posting) void loadStatus(view);
  },
  dispose() {
   const view = active;
   active = null;
   if (!view) return;
   stopPoll(view);
   view.listController?.abort(); view.statusController?.abort();
   document.removeEventListener('visibilitychange', view.onVisibility);
   window.removeEventListener('storage', view.onStorage);
  }
 });
})();
