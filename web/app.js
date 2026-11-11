'use strict';
const $ = (s) => document.querySelector(s);
const esc = (v = '') => String(v ?? '').replace(/[&<>"']/g, (c) => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const titles = {overview:'网络总览',tunnels:'规则管理',instances:'运行实例',health:'链路监测',config:'配置中心',logs:'运行记录',update:'软件更新'};
let updateChecked = false, releases = [], updateStatus = '', updateListError = '', updateSelection = '', updateSelectionLost = false, updateRequestSerial = 0;
let updateOptions = '', updateInstalling = false, updateTaskRunning = false, updateStatusKnown = false, updatePollTimer = null, updateStatusRequest = null;
let probeResults = Object.create(null), probing = Object.create(null), probeRevision = null, probingAll = false, probeBatchProgress = null;
let data, page = 'overview', selected = '', filter = '', protocol = '', editing, editRev, serial = 0, toastTimer, refreshTimer = null;
let authGeneration = 0, stateETag = '', stateSnapshot = '', renderedSnapshot = '', renderedPage = '', renderedRevision = null;
let stateRequest = null, logRequest = null, configRequest = null, deferredConfig = null;
let logFilter = '', logSnapshot = null, logExportView = null;
let logStream = null, logPaintFrame = null, logAccessDenied = null;
let registrationRequest = null, registrations = Object.create(null), registrationRevision = null, registrationProcesses = Object.create(null), registrationEpoch = 0, registrationExpiryTimer = null;
let registrationPaintPending = false;
let navigationGeneration = 0;
let oidcStatusRequest = null, oidcStartRequest = null;
let healthDialog = null, healthChecking = false, healthSelected = '';
const dialog = $('#dialog');
const diffDialog = $('#diff-dialog');
let backupDialogEl = null, backupRequest = null, importFileVersion = 0;
let webhookDialogEl = null, webhookDialogGeneration = 0;
let transferRequest = null, transferNotice = null;
const authSignalKey = 'kurumi-auth-session-change';
let lastAuthSignal = '';
let authChannel = null;
try {if (typeof BroadcastChannel === 'function') authChannel = new BroadcastChannel('kurumi-auth-session');} catch (_) { /* Storage events are a fallback. */ }
const transferStorageKey = 'kurumi-transfer-status';
try {
 const saved = /^(import|backup):(pending|uncertain|complete)$/.exec(sessionStorage.getItem(transferStorageKey) || '');
 if (saved) transferNotice = {kind:saved[1], phase:saved[2] === 'pending' ? 'uncertain' : saved[2]};
} catch (_) { /* Session storage is optional. */ }
function paintTransferNotice() {
 const notice = $('#transfer-notice'), dismiss = $('#transfer-dismiss');
 notice.hidden = !transferNotice;
 document.body.classList.toggle('has-transfer-notice', !!transferNotice);
 if (!transferNotice) return;
 const name = transferNotice.kind === 'backup' ? '加密备份恢复' : '配置导入';
 $('#transfer-message').textContent = transferNotice.phase === 'pending'
  ? `${name}已提交，正在等待结果。关闭页面或断线不能保证取消服务端操作。`
  : transferNotice.phase === 'uncertain'
   ? `${name}结果尚未确认，服务端可能仍在处理或已发生更改。请稍后刷新并核对配置，再决定是否重试。`
   : `${name}已完成，请核对当前配置。`;
 if (transferNotice.detail) $('#transfer-message').textContent += ` 提示：${transferNotice.detail}`;
 dismiss.hidden = transferNotice.phase === 'pending';
}
function setTransferNotice(kind, phase, detail='') {
 transferNotice = {kind,phase,detail};
 try {sessionStorage.setItem(transferStorageKey, `${kind}:${phase}`);} catch (_) { /* Session storage is optional. */ }
 paintTransferNotice();
}
function transferBlocked() {
 if (transferRequest) return '已有导入或恢复正在等待结果，请稍候。';
 if (transferNotice?.phase === 'uncertain') return '请先核对并确认上一项导入或恢复的结果提示。';
 return '';
}
function beginTransfer(request) {
 const blocked = transferBlocked();
 if (blocked) throw new Error(blocked);
 request.submitted = true; transferRequest = request;
 setTransferNotice(request.kind, 'pending');
}
function finishTransfer(request, phase, detail='') {
 if (transferRequest !== request) return;
 transferRequest = null;
 setTransferNotice(request.kind, phase, request.generation===authGeneration ? detail : '');
 // An earlier state request may have completed before the server applied the import.
 if (data) refresh();
}
$('#transfer-dismiss').addEventListener('click',()=>{
 if (transferRequest) return;
 transferNotice = null;
 try {sessionStorage.removeItem(transferStorageKey);} catch (_) { /* Session storage is optional. */ }
 paintTransferNotice();
});
paintTransferNotice();
function toast(text) { $('#toast').textContent = text; $('#toast').hidden = false; clearTimeout(toastTimer); toastTimer = setTimeout(() => $('#toast').hidden = true, 8000); }
function showDiff(v) {
 $('#diff-title').textContent = v.instanceName + ' · 配置差异';
 $('#diff-summary').textContent = '新增 '+v.addedCount+' 行 · 移除 '+v.removedCount+' 行'+(v.secretChanged?' · 认证凭据已变化':'')+(v.orderChanged?' · 仅顺序变化':'');
 const lines=[...(v.removed||[]).map(x=>'- '+x),...(v.added||[]).map(x=>'+ '+x)];
 $('#diff-content').textContent=(lines.join('\n')||'没有可显示的行变化')+(v.truncated?'\n差异显示已截断':'');
 stopRefreshPolling();
 diffDialog.showModal();
}
async function api(path, body, options = {}) {
 const generation = authGeneration;
 const response = await fetch('/api/' + path, {method:body === undefined ? 'GET':'POST',credentials:'same-origin',headers:{...(body === undefined ? {}:{'Content-Type':'application/json'}),...options.headers},body:body === undefined ? undefined:JSON.stringify(body),signal:options.signal});
 if (options.conditional && response.status === 304) return {value:null,etag:response.headers.get('ETag') || ''};
 const json = await response.json();
 if (!response.ok) {
  if (response.status === 401 && path !== 'login' && generation === authGeneration) {
   const immediate = path === 'logout' || (path === 'nodes' && ['issue','issue-control'].includes(body?.action));
   if (immediate || !window.KurumiDeferNodeLogout?.()) showLogin();
  }
  const error = new Error(json.error || `HTTP ${response.status}`); error.status = response.status; throw error;
 }
 if (path === 'login' || path === 'launch-login') {resetSession(); announceSessionChange();}
 return options.conditional ? {value:json,etag:response.headers.get('ETag') || ''} : json;
}
async function transferAPI(path, body) {
 const controller = new AbortController();
 const timeout = setTimeout(() => controller.abort(), 120000);
 try {return await api(path,body,{signal:controller.signal});}
 catch (error) {
  if (controller.signal.aborted) throw new Error('等待结果超时，服务端可能仍在处理');
  throw error;
 } finally {clearTimeout(timeout);}
}
function stopRefreshPolling() {
 if (refreshTimer !== null) {
  clearTimeout(refreshTimer);
  refreshTimer = null;
 }
}
function scheduleRefresh(delay) {
 stopRefreshPolling();
 if (!data || document.hidden || dialog.open || diffDialog.open || backupDialogEl?.open || webhookDialogEl?.open || healthDialog?.open || window.KurumiTOMLImport.isOpen() || window.KurumiNodeDialog?.open) return;
 const wait = delay ?? (Object.values(data.runtime || {}).some(v => v.state === 'running' || v.state === 'starting' || v.state === 'stopping') ? 5000 : 10000);
 refreshTimer = setTimeout(() => {
  refreshTimer = null;
  refresh(true);
 }, wait);
}
function cancelLogRequest() {
 const request = logRequest; logRequest = null; request?.controller.abort();
}
function cancelConfigRequest() {
 const request = configRequest; configRequest = null; deferredConfig = null; request?.controller.abort();
}
function cancelPageRequests() {cancelLogRequest(); cancelConfigRequest(); stopLogStream(); Overview.pause(); logAccessDenied = null; logSnapshot = null; logExportView = null;}
function resetSession() {
 stopRefreshPolling();
 oidcStatusRequest?.abort(); oidcStatusRequest = null;
 oidcStartRequest?.abort(); oidcStartRequest = null;
 window.resetAccountView?.();
 Overview.reset();
 window.KurumiInstances.reset();
 ++authGeneration; ++serial;
 stateRequest?.controller.abort(); cancelPageRequests();
 if (logPaintFrame !== null) cancelAnimationFrame(logPaintFrame);
 logPaintFrame = null;
 if (backupRequest && !backupRequest.submitted) backupRequest.controller.abort();
 backupRequest = null;
 if (transferNotice?.detail) {transferNotice.detail='';paintTransferNotice();}
 stateRequest = null; logFilter = ''; logSnapshot = null;
 $('#content').replaceChildren();
 updateChecked = false; releases = []; updateStatus = ''; updateListError = ''; updateSelection = ''; updateSelectionLost = false; ++updateRequestSerial;
 if (updatePollTimer !== null) clearTimeout(updatePollTimer);
 updateStatusRequest?.controller.abort();
 updatePollTimer = null; updateStatusRequest = null; updateInstalling = false; updateTaskRunning = false; updateStatusKnown = false; updateOptions = '';
 registrationRequest = null; registrations = Object.create(null); registrationRevision = null; registrationProcesses = Object.create(null); ++registrationEpoch; clearTimeout(registrationExpiryTimer); registrationExpiryTimer = null; registrationPaintPending = false; healthChecking = false; healthSelected = ''; ++navigationGeneration;
 stateETag = ''; stateSnapshot = ''; renderedSnapshot = ''; renderedPage = ''; renderedRevision = null; data = null;
 probeResults = Object.create(null); probing = Object.create(null); probeRevision = null; probingAll = false; probeBatchProgress = null;
 RulesPage.reset(); filter = ''; protocol = '';
 editing = null; $('#dialog-body').inert = false; $('#cancel').disabled = false; $('#editor button[type="submit"]').disabled = false;
}
function showLogin() {
 resetSession(); updateChecked=false; $('#login').hidden = false; $('#app').hidden = true;
 if (dialog.open) dialog.close(); if (diffDialog.open) diffDialog.close(); if (backupDialogEl?.open) backupDialogEl.close(); if (webhookDialogEl?.open) webhookDialogEl.close(); if (healthDialog?.open) healthDialog.close(); window.KurumiTOMLImport.close();
 void refreshOIDCLogin();
}
async function refreshOIDCLogin() {
 const button = $('#oidc-login'), generation = authGeneration, controller = new AbortController();
 button.hidden = true; oidcStatusRequest?.abort(); oidcStatusRequest = controller;
 const timeout = setTimeout(() => controller.abort(), 10000);
 try {
  const status = await api('oidc/status', undefined, {signal:controller.signal});
  if (oidcStatusRequest === controller && generation === authGeneration && !$('#login').hidden) button.hidden = status.enabled !== true;
 } catch (_) { /* Local login remains available if the status cannot be read. */ }
 finally {clearTimeout(timeout); if (oidcStatusRequest === controller) oidcStatusRequest = null;}
}
function announceSessionChange() {
 const signal = `${Date.now()}:${Math.random()}`;
 lastAuthSignal = signal;
 try {authChannel?.postMessage(signal);} catch (_) { /* Storage events are a fallback. */ }
 try {localStorage.setItem(authSignalKey, signal);} catch (_) { /* BroadcastChannel is a fallback. */ }
}
function receiveSessionChange(signal) {
 if (typeof signal !== 'string' || !signal || signal === lastAuthSignal) return;
 lastAuthSignal = signal;
 showLogin();
 refresh(true);
}
if (authChannel) authChannel.onmessage = event => receiveSessionChange(event.data);
window.addEventListener('storage', event => {if (event.key === authSignalKey) receiveSessionChange(event.newValue);});
function hasSelection() {return Boolean(window.getSelection()?.toString());}
function canSilentRender() {const content=$('#content'), active=document.activeElement;return !dialog.open && !diffDialog.open && !backupDialogEl?.open && !webhookDialogEl?.open && !healthDialog?.open && !window.KurumiTOMLImport.isOpen() && !window.KurumiNodeDialog?.open && !hasSelection() && (active===content || !content.contains(active));}
function renderPreservingScroll() {
 const x = window.scrollX, y = window.scrollY, previousSelected = selected, content = $('#content');
 const top = content.scrollTop, left = content.scrollLeft;
 const positions = ['#log-output','#config-output','.rc-history-list','.table-wrap'].flatMap(selector => [...content.querySelectorAll(selector)].map((el,index) => ({selector,index,top:el.scrollTop,left:el.scrollLeft,text:el.tagName === 'PRE' ? el.textContent : null})));
 render();
 for (const position of positions) {
  const el = content.querySelectorAll(position.selector)[position.index];
  if (!el || (position.text !== null && previousSelected !== selected)) continue;
  // Configuration output is restored while its asynchronous replacement loads.
  if (position.text !== null && position.selector !== '#log-output') el.textContent = position.text;
  el.scrollTop = position.top; el.scrollLeft = position.left;
 }
 content.scrollTop = top; content.scrollLeft = left; window.scrollTo(x,y);
}
async function refresh(silent = false) {
 const generation = authGeneration;
 if (stateRequest) {
  if (silent) return stateRequest.promise;
  // An explicit refresh after a save must not reuse a request sent before it.
  await stateRequest.promise;
  if (generation !== authGeneration) return;
  return refresh(false);
 }
 const request = {generation,controller:new AbortController()};
 stateRequest = request;
 request.promise = (async () => {
  try {
   const fresh = await api('state',undefined,{conditional:true,headers:stateETag ? {'If-None-Match':stateETag} : {},signal:request.controller.signal});
   if (generation !== authGeneration) return false;
   let accessChanged = false;
   if (fresh.value !== null) {
    const scope = value => JSON.stringify([value?.identity || null, (value?.state?.instances || []).map(i => i.id).sort()]);
    const identityChanged = !!data && JSON.stringify(data.identity || null) !== JSON.stringify(fresh.value.identity || null);
    accessChanged = !!data && scope(data) !== scope(fresh.value);
    if (accessChanged) {
     ++navigationGeneration; ++registrationEpoch;
     cancelPageRequests(); window.resetAccountView?.();
     if (dialog.open) dialog.close(); if (diffDialog.open) diffDialog.close();
     if (backupDialogEl?.open) backupDialogEl.close(); if (webhookDialogEl?.open) webhookDialogEl.close(); if (healthDialog?.open) healthDialog.close();
     window.KurumiTOMLImport.close();
     if (identityChanged && window.KurumiNodeDialog?.open) window.KurumiNodeDialog.close();
     registrationRequest = null; registrations = Object.create(null); registrationRevision = null; registrationProcesses = Object.create(null);
     clearTimeout(registrationExpiryTimer); registrationExpiryTimer = null;
     probeResults = Object.create(null); Overview.reset(); window.KurumiInstances.reset(); $('#content').replaceChildren();
    }
    const revisionChanged = data && data.revision !== fresh.value.revision;
    if (probeRevision !== fresh.value.revision) {probeResults = Object.create(null); probeRevision = fresh.value.revision;}
    const runtimeChanged = invalidateRegistrationForRuntime(data, fresh.value); invalidateLogsForRuntime(data, fresh.value); data = fresh.value;
    if (RulesPage.syncRevision(data.revision)) toast('草稿版本已变化，规则选择已清空，请重新选择。');
    if (page === 'tunnels' && revisionChanged) paintRulesList();
    if (page === 'tunnels' && !hasSelection()) RulesPage.paintRuntime(rulesContext());
    paintRegistrations(Boolean(revisionChanged || runtimeChanged)); stateSnapshot = JSON.stringify(data); stateETag = fresh.etag;
   }
   if (!data) return false;
   $('#login').hidden = true; $('#app').hidden = false;
   if (!updateChecked && data.identity?.role === 'admin') {updateChecked = true; checkUpdates(true);}
   if (!hasSelection()) {
    const revision = `DRAFT / r${data.revision}`;
    if ($('#revision').textContent !== revision) $('#revision').textContent = revision;
    $('#sync-time').textContent = `更新于 ${new Date().toLocaleTimeString('zh-CN')}`;
   }
   if (accessChanged || (!silent && !dialog.open && !diffDialog.open && !backupDialogEl?.open && !webhookDialogEl?.open && !healthDialog?.open && !window.KurumiTOMLImport.isOpen() && !window.KurumiNodeDialog?.open)) render();
   else if (canSilentRender() && (renderedSnapshot !== stateSnapshot || renderedPage !== page)) renderPreservingScroll();
   applyDeferredConfig();
   refreshRegistration();
   if (page === 'overview') Overview.refresh();
   syncLogStream();
   // Logs have a separate endpoint and can change even when state returns 304.
   // A delayed log response must not block polling the process generation.
   refreshLogs(silent);
   return true;
  } catch(e) {if (generation === authGeneration && e.name !== 'AbortError' && !silent && data) toast(e.message);return false;}
 })().finally(() => {
  if (stateRequest === request) stateRequest = null;
  scheduleRefresh();
 });
 return request.promise;
}
function logRunId(state, id) {return state?.runtime?.[id]?.runId || '';}
function invalidateLogsForRuntime(previous, next) {
 if (!previous || !next) return;
 const visible = id => next.state.instances.some(instance => instance.id === id);
 const stale = entry => !visible(entry.id) || entry.runId !== logRunId(next, entry.id);
 if (logRequest && stale(logRequest)) cancelLogRequest();
 if (logStream && stale(logStream)) stopLogStream();
 if (logSnapshot && stale(logSnapshot)) logSnapshot = null;
 if (logAccessDenied && stale(logAccessDenied)) logAccessDenied = null;
 if (page === 'logs' && selected && (!visible(selected) || logRunId(previous, selected) !== logRunId(next, selected))) {
  showLogMessage(visible(selected) ? '正在读取日志' : '实例不可用', visible(selected) ? '实例运行状态已变化，正在获取新的运行输出。' : '实例已移除或当前会话无权查看。', visible(selected) ? '正在读取…' : '实例不可用');
 }
}
function refreshLogs(silent = true) {
 if (!data || page !== 'logs' || !selected || document.hidden || !data.state.instances.some(i=>i.id===selected)) return Promise.resolve();
 const generation = authGeneration, navigation = navigationGeneration, id = selected, runId = logRunId(data, selected);
 if (logStream?.ready && logStream.id === id && logStream.runId === runId && logStream.generation === generation && logStream.navigation === navigation) return Promise.resolve();
 if (logRequest && logRequest.generation === generation && logRequest.navigation === navigation && logRequest.id === id && logRequest.runId === runId) return logRequest.promise;
 logRequest?.controller.abort();
 const request = {generation,navigation,id,runId,controller:new AbortController(),timedOut:false};
 const timeout = setTimeout(()=>{request.timedOut=true;request.controller.abort();},10000);
 logRequest = request;
 request.promise = (async () => {
  try {
   const result = await api('logs?id='+encodeURIComponent(id),undefined,{signal:request.controller.signal});
   if (logRequest !== request || generation !== authGeneration || navigation !== navigationGeneration || page !== 'logs' || selected !== id || runId !== logRunId(data, id)) return;
   if ((result.runId || '') !== runId) {
    stopLogStream(); logSnapshot = null;
    showLogMessage('正在读取日志', '实例运行状态正在更新，请稍候。', '正在读取…');
    refresh(true);
    return;
   }
   if (!Array.isArray(result.lines) || result.lines.length > 200 || !result.lines.every(line=>typeof line === 'string')) throw new Error('日志响应无效');
   logAccessDenied = null;
   logSnapshot = {id, runId, lines: result.lines};
   // Updating only the output keeps controls focused and preserves selected text.
   scheduleLogPaint();
   syncLogStream();
  } catch(e) {
   if (logRequest === request && generation === authGeneration && navigation === navigationGeneration && page === 'logs' && selected === id && runId === logRunId(data, id) && (e.name !== 'AbortError' || request.timedOut) && data) {
    if (e.status === 403) {
     logAccessDenied = {id,runId}; logSnapshot = null; stopLogStream();
     showLogMessage('无法查看日志', '当前会话没有查看此实例日志的权限。', '无查看权限');
     paintLogStatus();
     return;
    }
    const message = request.timedOut ? '读取日志超时，请稍后重试。' : '读取日志失败，请稍后重试。';
    if (!logSnapshot || logSnapshot.id !== id || logSnapshot.runId !== runId) showLogMessage('暂时无法读取日志', message, '读取失败');
    else if ($('#log-count')) $('#log-count').textContent = '刷新失败 · 显示上次结果';
    if (!silent) toast(request.timedOut ? '读取日志超时，请稍后重试' : e.message);
   }
  }
 })().finally(() => {clearTimeout(timeout);if (logRequest === request) logRequest = null;});
 return request.promise;
}
function paintLogStatus() {
 const status = $('#log-mode');
 if (!status) return;
 status.textContent = logAccessDenied?.id === selected && logAccessDenied.runId === logRunId(data, selected) ? '无查看权限' : logStream?.status || (selected ? '轮询更新' : '等待实例');
}
function stopLogStream() {
 if (logStream) {
  logStream.source?.close();
  clearTimeout(logStream.retry);
  clearTimeout(logStream.watchdog);
  logStream = null;
 }
 paintLogStatus();
}
function syncLogStream() {
 const instance = data?.state?.instances?.find(i=>i.id===selected);
 const runtime = data?.runtime?.[selected];
 if (page !== 'logs' || document.hidden || !instance || !['starting','running','stopping'].includes(runtime?.state) || !runtime.runId || (logAccessDenied?.id === selected && logAccessDenied.runId === runtime.runId) || typeof EventSource !== 'function') {
  stopLogStream();
  return;
 }
 const id = selected, runId = runtime.runId;
 if (logStream && logStream.id === id && logStream.runId === runId && logStream.generation === authGeneration && logStream.navigation === navigationGeneration) {
  paintLogStatus();
  return;
 }
 stopLogStream();
 const stream = {id,runId,generation:authGeneration,navigation:navigationGeneration,source:null,retry:null,watchdog:null,delay:1000,ready:false,ended:false,status:'连接实时日志…'};
 logStream = stream;
 connectLogStream(stream);
}
function connectLogStream(stream) {
 if (logStream !== stream || stream.ended) return;
 stream.status = '连接实时日志…';
 paintLogStatus();
 const query = new URLSearchParams({id:stream.id,runId:stream.runId});
 let source;
 try {source = new EventSource('/api/logs/stream?'+query.toString());}
 catch (_) {retryLogStream(stream);return;}
 stream.source = source;
 const current = () => logStream === stream && stream.source === source && stream.generation === authGeneration && stream.navigation === navigationGeneration && page === 'logs' && selected === stream.id && data?.state?.instances?.some(i=>i.id===stream.id) && logRunId(data, stream.id) === stream.runId;
 const touch = () => {
  clearTimeout(stream.watchdog);
  stream.watchdog = setTimeout(()=>{if(current())retryLogStream(stream);},30000);
 };
 const receive = (event, snapshot) => {
  if (!current()) return;
  let payload;
  try {payload=JSON.parse(event.data);} catch (_) {retryLogStream(stream);return;}
  if (!payload || payload.instanceId !== stream.id || payload.runId !== stream.runId || !Array.isArray(payload.lines) || payload.lines.length > 200 || !payload.lines.every(line=>typeof line === 'string')) {retryLogStream(stream);return;}
  if (snapshot) {
   cancelLogRequest();
   logSnapshot = {id:stream.id,runId:stream.runId,lines:payload.lines};
   stream.ready = true; stream.delay = 1000; stream.status = '实时更新';
   paintLogStatus();
  } else {
   if (!stream.ready || !logSnapshot || logSnapshot.id !== stream.id || logSnapshot.runId !== stream.runId) {retryLogStream(stream);return;}
   logSnapshot.lines = logSnapshot.lines.concat(payload.lines).slice(-200);
  }
  touch();
  scheduleLogPaint();
 };
 source.addEventListener('snapshot', event=>receive(event,true));
 source.addEventListener('lines', event=>receive(event,false));
 source.addEventListener('heartbeat', ()=>{if(current())touch();});
 source.addEventListener('end', event=>{
  if (!current()) return;
  let payload;
  try {payload=JSON.parse(event.data);} catch (_) {retryLogStream(stream);return;}
  if (!payload || payload.instanceId !== stream.id || payload.runId !== stream.runId) {retryLogStream(stream);return;}
  source.close(); clearTimeout(stream.watchdog); stream.source=null; stream.ready=false; stream.ended=true; stream.status='实例已退出';
  paintLogStatus(); refresh(true);
 });
 source.onerror = ()=>{if(current())retryLogStream(stream);};
 touch();
}
function retryLogStream(stream) {
 if (logStream !== stream || stream.ended || stream.retry !== null) return;
 stream.source?.close(); stream.source=null; stream.ready=false;
 clearTimeout(stream.watchdog); stream.watchdog=null;
 stream.status='轮询更新 · 等待重连'; paintLogStatus();
 refreshLogs(true);
 refresh(true);
 const delay=stream.delay; stream.delay=Math.min(stream.delay*2,30000);
 stream.retry=setTimeout(()=>{stream.retry=null;connectLogStream(stream);},delay);
}
function scheduleLogPaint() {
 if (hasSelection() || logPaintFrame !== null) return;
 logPaintFrame = requestAnimationFrame(()=>{logPaintFrame=null;if (!hasSelection()) paintLogs();});
}
function showLogMessage(title, detail, count) {
 const output = $('#log-output'), empty = $('#log-empty');
 logExportView = null;
 const exportButton = $('#content [data-action="export-logs"]');
 if (exportButton) exportButton.disabled = true;
 if (!output || !empty) return;
 output.replaceChildren(); output.hidden = true; empty.hidden = false;
 $('#log-empty-title').textContent = title; $('#log-empty-detail').textContent = detail;
 $('#log-empty-clear').hidden = true; $('#log-count').textContent = count;
}
function paintLogs() {
 const output = $('#log-output');
 const clear = $('#content [data-action="clear-log-filter"]');
 const exportButton = $('#content [data-action="export-logs"]');
 if (clear) clear.hidden = !logFilter;
 if (!output || !logSnapshot || logSnapshot.id !== selected || logSnapshot.runId !== logRunId(data, selected)) {
  logExportView = null;
  if (exportButton) exportButton.disabled = true;
  return;
 }
 const lines = logSnapshot.lines;
 const query = logFilter.toLocaleLowerCase();
 const matches = query ? lines.filter(line => line.toLocaleLowerCase().includes(query)) : lines;
 const noMatch = !!lines.length && !!query && !matches.length;
 $('#log-count').textContent = query ? `显示 ${matches.length} / ${lines.length} 行` : `共 ${lines.length} 行`;
 output.hidden = !matches.length; $('#log-empty').hidden = !!matches.length;
 $('#log-empty-title').textContent = noMatch ? '没有找到匹配的日志' : '暂无运行日志';
 $('#log-empty-detail').textContent = noMatch ? '试试其他关键词，或清除筛选查看全部日志。' : '实例有运行输出后，日志会显示在这里。';
 $('#log-empty-clear').hidden = !noMatch;
 const text = matches.join('\n');
 // Keep the export tied to what was actually painted, even when a text
 // selection temporarily pauses painting new stream events.
 logExportView = matches.length ? {id:selected,runId:logSnapshot.runId,filter:logFilter,generation:authGeneration,navigation:navigationGeneration,text} : null;
 if (exportButton) exportButton.disabled = !logExportView;
 if (output.textContent === text) return;
 const top = output.scrollTop, left = output.scrollLeft, follow = output.scrollHeight - top - output.clientHeight < 36;
 output.textContent = text; output.scrollTop = follow ? output.scrollHeight : top; output.scrollLeft = left;
}
function applyDeferredConfig() {
 if (!deferredConfig) return;
 if (deferredConfig.serial !== serial || deferredConfig.generation !== authGeneration || deferredConfig.navigation !== navigationGeneration || deferredConfig.id !== selected || page !== 'config' || !data || deferredConfig.revision !== data.revision) {deferredConfig = null; return;}
 if (!canSilentRender()) return;
 const output = $('#config-output');
 if (!output) return;
 const top = output.scrollTop, left = output.scrollLeft;
 output.textContent = deferredConfig.text; output.scrollTop = top; output.scrollLeft = left;
 $('#config-preview-status').textContent = '当前草稿 · 已脱敏';
 deferredConfig = null;
}
function loadConfigPreview(renderSerial) {
 const request = {serial:renderSerial, generation:authGeneration, navigation:navigationGeneration, revision:data.revision, id:selected, controller:new AbortController(), timedOut:false};
 configRequest = request;
 const timeout = setTimeout(()=>{request.timedOut=true;request.controller.abort();},10000);
 const current = () => configRequest === request && request.serial === serial && request.generation === authGeneration && request.navigation === navigationGeneration && page === 'config' && request.id === selected && data && request.revision === data.revision;
 api('config?id='+encodeURIComponent(request.id),undefined,{signal:request.controller.signal}).then(v=>{
  if (!current()) return;
  deferredConfig = {...request, text:v.toml}; applyDeferredConfig();
 }).catch(e=>{
  if (!current() || (e.name === 'AbortError' && !request.timedOut)) return;
  $('#config-output').textContent = '无法读取配置预览，请稍后刷新。';
  $('#config-preview-status').textContent = '读取失败';
  toast(request.timedOut ? '读取配置超时，请稍后重试' : e.message);
 }).finally(()=>{clearTimeout(timeout);if(configRequest === request)configRequest = null;});
}
const stateOf = (id) => data.runtime[id] || {state:'stopped'};
function badge(id) {const v=stateOf(id);return `<span class="pill ${v.state==='running'?'good':(v.state==='exited'||v.state==='autostart-failed')?'warn':''}">${({starting:'实例启动中',stopping:'实例停止中',running:'实例运行',stopped:'未启动',exited:'实例已退出','autostart-failed':'自动启动失败'})[v.state] || esc(v.state)}</span>${v.pending?'<span class="pill warn">待应用</span>':''}`;}
function header(title,subtitle,action='') {return `<div class="page-head"><div><p class="eyebrow">KURUMI / ${esc(page.toUpperCase())}</p><h1>${title}</h1><p>${subtitle}</p></div>${action}</div>`;}
function empty(title,text,action='',art='') {
 const src=art==='welcome'?'/assets/empty-welcome.webp':art==='search'?'/assets/empty-search.webp':'';
 const visual=src?`<img class="empty-illustration" src="${src}" width="384" height="384" loading="lazy" decoding="async" alt="">`:'<span class="empty-symbol" aria-hidden="true">◈</span>';
 return `<div class="empty">${visual}<strong>${title}</strong><p>${text}</p>${action}</div>`;
}
function time(v) {return new Date(v).toLocaleString('zh-CN',{hour12:false});}
function recent() {const events=Array.isArray(data.events)?data.events:[];return events.length?`<div class="timeline">${events.slice(-6).reverse().map(e=>`<div class="event"><p>${esc(e.message)}</p><small>${time(e.at)}</small></div>`).join('')}</div>`:empty('还没有操作记录','保存第一份配置后，变更记录会出现在这里。');}
function captureConfigControls() {
 if (page !== 'config' || renderedPage !== 'config') return null;
 const limit = $('#history-limit'), file = $('#import-file');
 return {scope:$('#export-scope')?.value, snapshot:$('#export-snapshot')?.value, limit:limit && limit.value !== limit.dataset.savedValue ? limit.value : null, file:file?.files.length ? file : null};
}
function syncExportScope() {
 const scope = $('#export-scope'), field = $('#export-snapshot-field'), snapshot = $('#export-snapshot');
 if (!scope || !field || !snapshot) return;
 field.hidden = scope.value !== 'snapshot';
 snapshot.disabled = field.hidden || !data.history.length;
}
function restoreConfigControls(saved) {
 if (saved) {
  if (saved.limit !== null) $('#history-limit').value = saved.limit;
  if (saved.scope) $('#export-scope').value = saved.scope;
  const snapshot = $('#export-snapshot');
  if (saved.snapshot && [...snapshot.options].some(option=>option.value === saved.snapshot)) snapshot.value = saved.snapshot;
  else if (saved.scope === 'snapshot' && data.history.length) {
   const option = document.createElement('option'); option.value = ''; option.textContent = '所选快照已不可用，请重新选择'; option.disabled = true; option.selected = true; snapshot.prepend(option);
  }
  if (saved.file) $('#import-file').replaceWith(saved.file);
 }
 syncExportScope();
}
function probeCell(t) {
 const unavailable=Protocols.probeUnavailable(t);
 if (unavailable) return `<span class="pill">不支持</span><small class="probe-message">${esc(unavailable)}</small>`;
 if (probing[t.id]) return '<span class="pill warn">检测中</span>';
 const result = probeResults[t.id];
 if (!result || result.revision !== data.revision) return '<span class="muted">尚未检测</span>';
 const labels = {
  healthy: ['HTTP 正常', 'good'],
  responding: ['服务已响应', 'warn'],
  redirect: ['返回重定向', 'warn'],
  error: ['检测失败', 'bad'],
  reachable: ['TCP 可连接', 'good'],
  unreachable: ['不可连接', 'bad'],
  unsupported: ['不支持', ''],
  invalid: ['配置无效', 'bad']
 };
 const meta = labels[result.status] || labels.unreachable;
 const detail = result.httpStatus ? 'HTTP ' + result.httpStatus : result.latencyMs + ' ms';
 const checked = result.checkedAt ? new Date(result.checkedAt).toLocaleTimeString('zh-CN') : '';
 return '<span class="pill ' + meta[1] + '" title="' + esc(result.message) + '">' + meta[0] + '</span><small class="probe-meta">' + esc(detail + ' · ' + checked) + '</small><small class="probe-message">' + esc(result.message) + '</small>';
}
function render() {
 if (!data) return;
 const configControls = captureConfigControls();
 cancelConfigRequest();
 const n = ++serial; $('#page-title').textContent=titles[page]; document.title=titles[page]+' · KurumiFRP';
 document.querySelectorAll('[data-page]').forEach(el=>el.classList.toggle('active',el.dataset.page===page));
 const content=$('#content');
 const portWarnings = Array.isArray(data.portWarnings) && data.portWarnings.length ? `<div class="notice" role="alert">${data.portWarnings.map(message => `<p class="small-note">${esc(message)}</p>`).join('')}</div>` : '';
 if (page==='update' && renderedPage==='update' && $('#update-version')) {
  paintUpdate(); renderedSnapshot = stateSnapshot; renderedRevision = data.revision; return;
 }
 if(page==='overview') {
  content.innerHTML=Overview.render(data,{escape:esc,header,badge,instanceRegistrationText,tunnelTable,loadServerTraffic:(id,signal)=>api('server-traffic?id='+encodeURIComponent(id),undefined,{signal}),loadTraffic:(id,signal)=>api('traffic?id='+encodeURIComponent(id),undefined,{signal}),loadClientRuleTraffic:(id,signal)=>api('client-traffic?id='+encodeURIComponent(id),undefined,{signal}),loadHistory:(id,signal)=>api('traffic-history?id='+encodeURIComponent(id),undefined,{signal})});
  Overview.refresh();
 } else if(page==='instances') {
  content.innerHTML=window.KurumiInstances.render(data,{header,empty,esc,registrationText:instanceRegistrationText});
  if(portWarnings) content.insertAdjacentHTML('afterbegin',portWarnings);
 } else if(page==='health') {
  if (!data.state.instances.some(i=>i.id===healthSelected)) healthSelected=data.state.instances[0]?.id||'';
  content.innerHTML=header('链路监测中心','选择实例，按需检查实例、控制端口、注册状态和本地目标。')+
   (data.state.instances.length?`<section class="panel"><div class="panel-content"><label>选择实例<select id="health-instance">${data.state.instances.map(i=>`<option value="${esc(i.id)}" ${i.id===healthSelected?'selected':''}>${esc(i.name)} · ${esc(i.kind)}</option>`).join('')}</select></label><p class="small-note">诊断不会修改配置或连接；导出报告包含网络地址。</p><button class="primary" data-action="health">开始诊断</button></div></section>`:empty('还没有可监测的实例','请先创建一个实例。','<button data-go="instances">运行实例</button>'));
 } else if(page==='tunnels') {
  content.innerHTML=header('规则管理','管理连接规则、观察实例状态并检查本地目标。',`<div class="actions"><button data-action="new-tunnel-batch" ${RulesPage.isUnavailable()?'disabled':''}>批量 / TCP+UDP</button><button class="primary" data-action="new-tunnel" ${RulesPage.isUnavailable()?'disabled':''}>＋ 新建规则</button></div>`)+RulesPage.render(rulesContext());
  RulesPage.paint(rulesContext());
  if(portWarnings) content.insertAdjacentHTML('afterbegin',portWarnings);
 } else if(page==='config') {
  ensureSelected();
  content.innerHTML=window.KurumiRecordsConfig.config({data,selected,header,picker,time,esc});
  restoreConfigControls(configControls);
  if(selected) loadConfigPreview(n);
 } else if(page==='update') {
  reconcileUpdateSelection();
  updateOptions = updateOptionsMarkup();
  content.innerHTML=header('软件更新','从 GitHub 获取官方版本，选择可用的新版本进行更新。')+`<section class="panel"><div class="panel-content"><p>当前版本 <span id="update-current">${esc(data.version)}</span>。更新期间工作台和托管连接可能中断；Windows 需要管理员授权。</p><label>选择版本<select id="update-version">${updateOptions}</select></label><p id="update-selection-note" class="small-note" role="status" ${updateSelectionLost?'':'hidden'}>已选版本不再可用，请重新选择。</p><div class="actions"><button data-action="check-update">检查版本</button><button class="primary" data-action="install-update" ${updateCanInstall()&&!updateBusy()?'':'disabled'}>更新所选版本</button></div><p id="update-list-error" class="small-note" role="alert" ${updateListError?'':'hidden'}>${esc(updateListError)}</p><p id="update-status" class="small-note">${esc(updateStatus||'选择版本后将下载并校验官方安装包。')}</p></div></section>`;
  $('#update-version').value=updateSelection;
 } else if(page==='logs') {
  ensureSelected(); content.innerHTML=window.KurumiRecordsConfig.logs({selected,logFilter,header,picker,recent,esc});
  if(selected) {paintLogs(); syncLogStream(); refreshLogs(false);} else stopLogStream();
 }
 renderedSnapshot = stateSnapshot; renderedPage = page; renderedRevision = data.revision;
}
function updateCanInstall(tag=updateSelection) {return !!tag && releases.some(v=>v.tag===tag&&v.newer&&v.available);}
function updateBusy() {return updateInstalling || updateTaskRunning || !updateStatusKnown;}
function reconcileUpdateSelection() {
 if (updateSelection && !updateCanInstall()) {updateSelection=''; updateSelectionLost=true;}
 if (!updateSelection && !updateSelectionLost) updateSelection=releases.find(v=>v.newer&&v.available)?.tag || '';
}
function updateOptionsMarkup() {
 return `<option value="" disabled>${updateSelectionLost?'已选版本不再可用，请重新选择':'请选择可用版本'}</option>`+
  releases.map(v=>`<option value="${esc(v.tag)}" ${v.newer&&v.available?'':'disabled'}>${v.newer&&v.available?'':'✕ '}${esc(v.tag)}${v.metadataFallback?' · 发布状态待确认':v.prerelease?' · 预览版':''}${v.newer?(v.available?'':' · 无可用安装包'):' · 暂不支持切换'}</option>`).join('');
}
function paintUpdate() {
 if (page!=='update' || !data) return;
 const select=$('#update-version'); if (!select) return;
 reconcileUpdateSelection();
 const options=updateOptionsMarkup();
 if (options!==updateOptions) {select.innerHTML=options; updateOptions=options; select.value=updateSelection;}
 else if (document.activeElement!==select && select.value!==updateSelection) select.value=updateSelection;
 $('#update-current').textContent=data.version;
 $('#update-selection-note').hidden=!updateSelectionLost;
 $('#content [data-action="install-update"]').disabled=updateBusy() || !updateCanInstall() || select.value!==updateSelection;
 $('#update-list-error').textContent=updateListError;
 $('#update-list-error').hidden=!updateListError;
 $('#update-status').textContent=updateStatus || '选择版本后将下载并校验官方安装包。';
}
async function checkUpdates(promptUser=false) {
 const generation=authGeneration, requestSerial=++updateRequestSerial;
 refreshUpdateStatus(generation);
 try {
  const v=await api('update/releases');
  if(generation!==authGeneration||requestSerial!==updateRequestSerial)return;
  releases=v.versions; updateListError='';
  paintUpdate();
  const latest=releases.find(v=>v.newer&&v.available);
  if(promptUser&&latest&&page!=='update'&&confirm(`发现新版本 ${latest.tag}。现在查看更新吗？`))navigate('update');
 } catch(e) {
  if(generation!==authGeneration||requestSerial!==updateRequestSerial)return;
  updateListError='无法获取版本列表：'+e.message;
  paintUpdate();
 }
}
function scheduleUpdatePoll(generation, delay) {
 if (updatePollTimer!==null) clearTimeout(updatePollTimer);
 const timer=setTimeout(()=>{
  if (updatePollTimer===timer) updatePollTimer=null;
  if (generation===authGeneration) refreshUpdateStatus(generation);
 },delay);
 updatePollTimer=timer;
}
function refreshUpdateStatus(generation) {
 if (!data || generation!==authGeneration || updateInstalling) return;
 if (updateStatusRequest) return updateStatusRequest.promise;
 if (updatePollTimer!==null) clearTimeout(updatePollTimer);
 updatePollTimer=null;
 const request={generation,controller:new AbortController(),timedOut:false};
 const timeout=setTimeout(()=>{request.timedOut=true;request.controller.abort();},10000);
 updateStatusRequest=request;
 request.promise=(async()=>{
  try {
   const v=await api('update/status',undefined,{signal:request.controller.signal});
   if(generation!==authGeneration||updateStatusRequest!==request)return;
   updateStatus=v.status||''; updateStatusKnown=true; updateTaskRunning=updateStatus.startsWith('正在');
  } catch(err) {
   if(generation!==authGeneration||updateStatusRequest!==request||(err.name==='AbortError'&&!request.timedOut))return;
   updateStatus='工作台暂时不可用，请稍后刷新。'; updateStatusKnown=false;
  } finally {
   clearTimeout(timeout);
   if(updateStatusRequest===request) {
    updateStatusRequest=null; paintUpdate();
    if(generation===authGeneration&&(!updateStatusKnown||updateTaskRunning)) scheduleUpdatePoll(generation,updateStatusKnown?2000:5000);
   }
  }
 })();
 return request.promise;
}
function pickerInstances(){return page==='logs'&&data.identity?.role!=='admin'?data.state.instances.filter(i=>(data.identity?.logInstances||[]).includes(i.id)):data.state.instances;}
function ensureSelected(){const instances=pickerInstances();if(!instances.some(i=>i.id===selected)){cancelLogRequest();stopLogStream();logSnapshot=null;logExportView=null;logAccessDenied=null;selected=instances[0]?.id||'';}}
function picker(){const instances=pickerInstances();return `<select class="config-picker" id="instance-picker" aria-label="选择实例" ${instances.length?'':'disabled'}>${instances.length?instances.map(i=>`<option value="${esc(i.id)}" ${selected===i.id?'selected':''}>${esc(i.name)} · ${esc(i.kind)}</option>`).join(''):'<option value="">尚无可用实例</option>'}</select>`;}
function rulesContext(){return {data,filter,protocol,probingAll,probing,probeBatchProgress,esc,empty,stateOf,registrationFor,registrationText,probeCell};}
function paintRulesList(){
 const list=$('#tunnel-list');
 if (!list || !data) return;
 const context=rulesContext(); list.dataset.ruleRevision=String(data.revision); list.innerHTML=RulesPage.list(context);
 RulesPage.paint(context); RulesPage.paintRuntime(context);
}
const registrationLabels = {stopped:'实例未运行',disabled:'未应用此规则',pending:'草稿未应用', 'pending-stop':'旧配置含此规则，待应用停用', 'not-reported':'不提供注册状态',unavailable:'状态不可用',registering:'注册中',registered:'已注册',failed:'注册失败'};
function registrationText(item) {
 if (!item) return '等待状态';
 return (registrationLabels[item.state] || '状态未知') + (item.portPending ? ' · 动态端口待确认' : '') + (item.error ? ' · '+item.error : '') + (item.remoteAddr ? ' · 最近观察到 '+item.remoteAddr : '') + (item.pending && item.state==='registered' ? ' · 实例配置待应用' : '');
}
function runtimeIdentity(runtime) {return [runtime?.state || 'stopped', runtime?.pid || 0, runtime?.started || '', runtime?.runId || ''].join('|');}
function processSnapshot(state) {
 const result=Object.create(null);
 for (const instance of state?.state?.instances || []) result[instance.id]=runtimeIdentity(state.runtime?.[instance.id]);
 return result;
}
function invalidateRegistrationForRuntime(previous, next) {
 if (!previous || !next) return false;
 const before=processSnapshot(previous), after=processSnapshot(next);
 let changed=false;
 for (const tunnel of previous.state.tunnels || []) {
  if (before[tunnel.instanceId]===after[tunnel.instanceId]) continue;
  changed=true;
  delete registrations[tunnel.id];
 }
 return changed;
}
function registrationFor(tunnel) {
 if (!data || registrationRevision!==data.revision || registrationProcesses[tunnel.instanceId]!==runtimeIdentity(data.runtime?.[tunnel.instanceId])) return null;
 return registrations[tunnel.id];
}
function instanceRegistrationText(instance) {
 if (stateOf(instance.id).state === 'starting') return '实例启动中';
 if (stateOf(instance.id).state === 'stopping') return '实例停止中';
 if (stateOf(instance.id).state !== 'running') return '代理未运行';
 const eligible=data.state.tunnels.filter(t=>t.instanceId===instance.id && t.enabled && !Protocols.isVisitor(t.type));
 const items=eligible.map(registrationFor);
 const count=items.filter(item=>item?.state==='registered'&&!item.portPending).length;
 const unresolved=items.some(item=>!item || item.portPending || !['registered','failed'].includes(item.state));
 if (unresolved) return count ? `观察到已注册 ${count} · 其余待确认` : '注册状态待确认';
 return `观察到已注册 ${count}`+(stateOf(instance.id).pending?' · 实例待应用':'');
}
function healthSummary(rules) {
 const summary={registered:0,failed:0,unknown:0,notApplicable:0,targetResponded:0,targetUnreachable:0,targetOther:0};
 for (const rule of rules) {
  const state=rule.registration?.state;
  if (state==='registered' && !rule.registration?.portPending) summary.registered++;
  else if (state==='failed') summary.failed++;
  else if (['disabled','not-reported'].includes(state)) summary.notApplicable++;
  else summary.unknown++;
  if (['healthy','reachable','responding','redirect'].includes(rule.target?.status)) summary.targetResponded++;
  else if (rule.target?.status==='unreachable') summary.targetUnreachable++;
  else summary.targetOther++;
 }
 return summary;
}
function showHealth(report) {
 if (!healthDialog) {
  healthDialog=document.createElement('dialog');
  healthDialog.innerHTML='<h2>链路检测</h2><p class="muted">分层结果不代表公网可达；导出报告包含网络地址。</p><pre></pre><div class="dialog-actions"><button type="button" data-health-close>关闭</button><button type="button" data-health-export>导出报告</button></div>';
  document.body.appendChild(healthDialog);
  healthDialog.querySelector('[data-health-close]').addEventListener('click',()=>healthDialog.close());
  healthDialog.addEventListener('close',()=>scheduleRefresh(0));
 }
 const summary=healthSummary(report.rules);
 report.summary=summary;
 const rows=[report.instance+' · '+report.kind+' · 检测时间 '+time(report.checkedAt), '检测时注册：已注册 '+summary.registered+' · 失败 '+summary.failed+' · 待确认 '+summary.unknown+' · 不适用 '+summary.notApplicable, '草稿本地目标：可响应 '+summary.targetResponded+' · 不可达 '+summary.targetUnreachable+' · 未检测或不适用 '+summary.targetOther, '实例运行状态：'+report.runtime+(report.pending?' · 配置待应用':''), '草稿控制端口：'+report.endpoint.state+' '+report.endpoint.message];
 for (const rule of report.rules) rows.push(rule.name+' · '+rule.type+'\n  已应用代理注册：'+registrationText(rule.registration)+'\n  草稿本地目标：'+rule.target.status+' · '+rule.target.message);
 rows.push(report.note);
 healthDialog.querySelector('pre').textContent=rows.join('\n\n');
 healthDialog.querySelector('[data-health-export]').onclick=()=>download('kurumi-health.json',JSON.stringify(report,null,2));
 stopRefreshPolling(); healthDialog.showModal();
}
function paintRegistrations(force=false) {
 if (!force && hasSelection()) {registrationPaintPending=true;return;}
 registrationPaintPending=false;
 document.querySelectorAll('[data-registration]').forEach(el=>{
  const tunnel=data?.state?.tunnels?.find(t=>t.id===el.dataset.registration);
  const next=registrationText(tunnel ? registrationFor(tunnel) : null);
  if (el.textContent!==next) el.textContent=next;
 });
 document.querySelectorAll('[data-registration-count]').forEach(el=>{
  const instance=data?.state?.instances?.find(i=>i.id===el.dataset.registrationCount);
  if (instance) {const next=instanceRegistrationText(instance);if(el.textContent!==next)el.textContent=next;}
 });
}
document.addEventListener('selectionchange',()=>{if(hasSelection())return;if(registrationPaintPending)paintRegistrations();if(page==='tunnels'&&data)RulesPage.paintRuntime(rulesContext());if(page==='logs')paintLogs();if(page==='config')applyDeferredConfig();});
function refreshRegistration() {
 if (data?.identity?.role !== 'admin') return;
 if (!data || !['overview','tunnels','instances'].includes(page) || document.hidden || registrationRequest || healthChecking) return;
 const generation=authGeneration, revision=data.revision, epoch=registrationEpoch, processes=processSnapshot(data), started=performance.now();
 const controller=new AbortController(), timeout=setTimeout(()=>controller.abort(),15000);
 const request=api('registration',undefined,{signal:controller.signal}).then(result=>{
  if (generation!==authGeneration || epoch!==registrationEpoch || document.hidden || !data || revision!==data.revision || result.revision!==revision) return;
  const remaining=15000-(performance.now()-started);
  if (remaining<=0) return;
  const current=processSnapshot(data), accepted=Object.create(null), incoming=result.tunnels || {};
  for (const tunnel of data.state.tunnels || []) {
   if (processes[tunnel.instanceId]===current[tunnel.instanceId] && Object.hasOwn(incoming,tunnel.id)) accepted[tunnel.id]=incoming[tunnel.id];
  }
  registrations=accepted;registrationRevision=revision;registrationProcesses=processes;paintRegistrations(true);
  clearTimeout(registrationExpiryTimer);
  registrationExpiryTimer=setTimeout(()=>{
   if(generation!==authGeneration || epoch!==registrationEpoch || registrationRevision!==revision)return;
   registrations=Object.create(null);registrationRevision=null;registrationProcesses=Object.create(null);registrationExpiryTimer=null;paintRegistrations(true);
  },remaining);
 }).catch(()=>{if(generation===authGeneration && epoch===registrationEpoch && data && revision===data.revision) {clearTimeout(registrationExpiryTimer);registrationExpiryTimer=null;registrations=Object.create(null);registrationRevision=null;registrationProcesses=Object.create(null);paintRegistrations(true);}}).finally(()=>{clearTimeout(timeout);if(registrationRequest===request){registrationRequest=null;if(epoch!==registrationEpoch && !document.hidden)refreshRegistration();}});
 registrationRequest=request;
}
function tunnelTable(rows) {
 if(!rows.length)return empty('这里还静悄悄的 ♡','先添加 frpc 实例，再创建连接规则。','<button data-action="new-tunnel">＋ 添加规则</button>');
 return `<div class="table-wrap"><table><thead><tr><th>服务 / 归属</th><th>协议</th><th>本地目标 / 监听</th><th>远端 / 配对</th><th>配置意图</th><th>代理注册</th><th>检测结果</th><th>操作</th></tr></thead><tbody>${rows.map(t=>{const unavailable=Protocols.probeUnavailable(t);return `<tr><td><strong>${esc(t.name)}</strong><small>${esc(data.state.instances.find(i=>i.id===t.instanceId)?.name)}</small></td><td><span class="type-icon">${esc(t.type.toUpperCase())}</span></td><td><code>${esc(Protocols.endpoint(t))}</code></td><td><code>${esc(Protocols.remote(t))}</code></td><td><span class="pill ${t.enabled?'good':''}">${t.enabled?'期望启用':'期望停用'}</span></td><td data-registration="${esc(t.id)}">${esc(registrationText(registrationFor(t)))}</td><td class="probe-cell">${probeCell(t)}</td><td><div class="actions"><button data-action="probe" data-id="${esc(t.id)}" ${unavailable||probing[t.id]||probingAll?'disabled':''} ${unavailable?`title="${esc(unavailable)}"`:''}>${unavailable?'不支持检测':probing[t.id]?'检测中…':Protocols.isVisitor(t.type)?'检测监听':'检测目标'}</button><button data-action="edit-tunnel" data-id="${esc(t.id)}">编辑</button><button data-action="duplicate-tunnel" data-id="${esc(t.id)}">复制规则</button><button data-action="toggle" data-id="${esc(t.id)}">${t.enabled?'停用':'启用'}</button><button class="danger" data-action="delete-tunnel" data-id="${esc(t.id)}">删除</button></div></td></tr>`;}).join('')}</tbody></table></div>`;
}
function openEditor(kind,id) {
 const old=(kind==='instance'?data.state.instances:data.state.tunnels).find(v=>v.id===id);
 if(id&&!old){toast('对象已不存在，请刷新后重试');return;}
 const clients=data.state.instances.filter(i=>i.kind==='frpc');
 if(kind==='tunnel'&&!clients.length){toast('请先添加一个 frpc 客户端实例');return;}
 editing={kind,id:old?.id||'',old};editRev=data.revision;
 $('#editor button[type="submit"]').textContent='保存草稿';
 $('#form-error').textContent='';
 $('#dialog-body').innerHTML=kind==='instance'?ProtocolEditor.instance(old):ProtocolEditor.tunnel(old,clients);
 stopRefreshPolling();updateFields();dialog.showModal();
}
function openScheduleEditor(id) {
 const instance=data.state.instances.find(i=>i.id===id);
 if(!instance || data.identity?.role!=='admin'){toast('实例不存在或没有管理权限');return;}
 const plan=data.schedules?.[id];
 editing={kind:'schedule',id,version:plan?.version||'',old:plan,submitting:false};editRev=data.revision;
 $('#form-error').textContent='';
 $('#editor button[type="submit"]').textContent='保存计划';
 $('#dialog-body').innerHTML=window.KurumiSchedules.editor(instance,plan,esc);
 stopRefreshPolling();dialog.showModal();
}
function openTunnelBatchEditor() {
 const clients=data.state.instances.filter(i=>i.kind==='frpc');
 if(!clients.length){toast('请先添加一个 frpc 客户端实例');return;}
 editing={kind:'tunnel-batch',submitting:false};editRev=data.revision;
 $('#editor button[type="submit"]').textContent='保存草稿';
 $('#form-error').textContent='';
 $('#dialog-body').innerHTML=ProtocolEditor.batch(clients);
 stopRefreshPolling();dialog.showModal();
}
function updateFields(){
 if(editing && editing.kind!=='schedule') ProtocolEditor.update($('#editor'),editing,data.state.tunnels);
}
async function saveSchedule(remove) {
 const current=editing;
 if(!current || current.kind!=='schedule' || current.submitting)return;
 let value;
 try { value=remove?{remove:true}:window.KurumiSchedules.value($('#editor')); }
 catch(err) { $('#form-error').textContent=err.message; return; }
 if(remove && !confirm('删除此定时计划？原有的自动恢复设置将在下次启动工作台时生效。'))return;
 if(!remove && !value.enabled && current.old?.enabled && !confirm('关闭计划后，原有的自动恢复设置将在下次启动工作台时生效。继续？'))return;
 const generation=authGeneration, button=$('#editor button[type="submit"]');
 current.submitting=true;button.disabled=true;$('#cancel').disabled=true;$('#dialog-body').inert=true;
 const release=()=>{if(editing===current){current.submitting=false;button.disabled=false;$('#cancel').disabled=false;$('#dialog-body').inert=false;}};
 try {
  await api('schedules',{revision:editRev,id:current.id,version:current.version,...value});
  if(generation!==authGeneration)return;
  release();dialog.close();toast(remove?'计划已删除':'计划已保存');await refresh();
 } catch(err) { if(generation===authGeneration)$('#form-error').textContent=err.message; }
 finally { release(); }
}
function navigate(to){if(!titles[to] || (to!=='nodes' && window.KurumiNodeNavigationBlocked?.()))return;cancelPageRequests();++navigationGeneration;page=to;location.hash=to;render();refreshRegistration();}
async function openWebhookDialog() { await window.KurumiWebhooks.open(); }
function closeBackupDialog() {
 const request=backupRequest; backupRequest=null;
 if(request && !request.submitted)request.controller.abort();
 if(backupDialogEl?.open)backupDialogEl.close();
}
function openBackupDialog(mode, scope='all', snapshot=0) {
 if (!data || !['export','import'].includes(mode) || backupDialogEl?.open) return;
 if (mode==='import' && transferBlocked()) {toast(transferBlocked());return;}
 if (!backupDialogEl) {
  backupDialogEl = document.createElement('dialog');
  backupDialogEl.id = 'backup-dialog';
  document.body.appendChild(backupDialogEl);
  backupDialogEl.addEventListener('close',()=>{if(!backupDialogEl.open)backupDialogEl.innerHTML='';scheduleRefresh(0);});
  backupDialogEl.addEventListener('cancel',e=>{e.preventDefault();closeBackupDialog();});
  backupDialogEl.addEventListener('click',e=>{if(e.target.closest('[data-backup-cancel]'))closeBackupDialog();});
  backupDialogEl.addEventListener('submit',async e=>{
   if(!e.target.matches('#backup-form'))return;
   e.preventDefault();
   const f=e.target,button=f.querySelector('button[type="submit"]'),error=f.querySelector('.error');
   if (backupRequest || !data) return;
   const request={generation:authGeneration,navigation:navigationGeneration,revision:Number(backupDialogEl.dataset.revision),mode:backupDialogEl.dataset.mode,scope:backupDialogEl.dataset.scope,snapshot:Number(backupDialogEl.dataset.snapshot),kind:'backup',controller:new AbortController(),submitted:false}; backupRequest=request;
   const currentRequest=()=>backupRequest===request && request.generation===authGeneration && backupDialogEl.open;
   const checkRestoreContext=()=>{
    if(request.mode==='import' && (request.navigation!==navigationGeneration || page!=='config' || renderedPage!=='config' || renderedRevision!==request.revision || data?.revision!==request.revision))throw new Error('页面或配置已变化，请关闭窗口并刷新后重新恢复');
   };
   const password=f.elements.password.value, file=request.mode==='import'?f.elements.file.files[0]:null;
   const confirmed=request.mode==='import' && f.elements.confirmRestore.checked;
   error.textContent='';button.disabled=true;
   try{
    if(request.mode==='export' && password!==f.elements.confirm.value)throw new Error('两次输入的密码不一致');
    if(request.mode==='import'){
     if(transferBlocked())throw new Error(transferBlocked());
     checkRestoreContext();
     if(!file)throw new Error('请选择备份文件');
     if(file.size>180*1024*1024)throw new Error('备份文件超过大小限制');
     if(!confirmed)throw new Error('请确认恢复操作');
    }
    const current=await api('state',undefined,{signal:request.controller.signal});
    if(!currentRequest())return;
    checkRestoreContext();
    if(request.mode==='export'){
     if(request.navigation!==navigationGeneration || page!=='config' || current.revision!==request.revision || data.revision!==request.revision)throw new Error('页面或配置已变化，请关闭窗口并刷新后重新导出');
     const result=await api('backup/export',{revision:request.revision,password,scope:request.scope,snapshot:request.snapshot},{signal:request.controller.signal});
     if(!currentRequest())return;
     if(request.navigation!==navigationGeneration || page!=='config')return;
     download('kurumi-'+request.scope+'-encrypted-'+new Date().toISOString().slice(0,10)+'.json',JSON.stringify(result,null,2));
     closeBackupDialog();
     toast('加密备份已导出，请妥善保管密码');
    }else{
     if(current.revision!==request.revision || data.revision!==request.revision)throw new Error('配置已变化，请关闭窗口并刷新后重新恢复');
     let backup;
     try{backup=JSON.parse(await file.text());}catch(_){throw new Error('备份文件不是有效 JSON');}
     if(!currentRequest())return;
     checkRestoreContext();
     beginTransfer(request);
     f.querySelector('[data-backup-cancel]').textContent='关闭';
     f.querySelector('#backup-status').textContent='恢复已提交。关闭窗口不会取消服务端操作，收到结果后会在页面提示。';
     await transferAPI('backup/import',{revision:request.revision,password,backup});
     if(currentRequest())closeBackupDialog();
     finishTransfer(request,'complete');
    }
   }catch(e){
    if(request.submitted){
     finishTransfer(request,'uncertain',e.message||'恢复请求未完成');
     if(currentRequest()){
      f.querySelector('#backup-status').textContent='恢复结果尚未确认，请关闭窗口并查看提示。';
      error.textContent=(e.message||'恢复请求未完成')+'。结果尚未确认；服务端可能仍在处理或已发生更改，请关闭窗口后核对提示。';
     }
    }else if(currentRequest() && e.name!=='AbortError')error.textContent=e.message||'备份操作失败';
   }
   finally{if(backupRequest===request)backupRequest=null;button.disabled=false;}
  });
 }
 backupDialogEl.dataset.mode=mode;
 backupDialogEl.dataset.scope=scope;
 backupDialogEl.dataset.snapshot=String(snapshot);
 backupDialogEl.dataset.revision=String(data.revision);
 backupDialogEl.innerHTML=mode==='export'
  ? '<form id="backup-form"><h2>导出加密备份</h2><p class="muted">密码不会保存在工作台；遗失密码后无法恢复，请将密码和文件分开保管。</p><div class="form-grid"><label>备份密码<input name="password" type="password" minlength="12" maxlength="1024" autocomplete="new-password" required></label><label>确认密码<input name="confirm" type="password" minlength="12" maxlength="1024" autocomplete="new-password" required></label></div><div class="dialog-actions"><button type="button" data-backup-cancel>取消</button><button type="submit" class="primary">导出加密文件</button></div><p class="error" role="alert"></p></form>'
  : '<form id="backup-form"><h2>恢复加密备份</h2><p class="muted">恢复前须停止全部托管实例并等待操作结束。恢复将替换当前配置、定时计划与历史快照，导入的定时计划暂停并须重新启用，不会立即启动实例；不会恢复 frp 核心程序、本机管理令牌、Webhook 和节点设置。</p><div class="form-grid"><label class="span-2">备份文件<input name="file" type="file" accept=".json,application/json" required></label><label class="span-2">备份密码<input name="password" type="password" minlength="12" maxlength="1024" autocomplete="current-password" required></label><label class="form-check span-2"><input name="confirmRestore" type="checkbox" required>确认替换当前配置、定时计划和历史快照</label></div><p id="backup-status" class="small-note" role="status"></p><div class="dialog-actions"><button type="button" data-backup-cancel>取消</button><button type="submit" class="primary">验证并恢复</button></div><p class="error" role="alert"></p></form>';
 stopRefreshPolling();
 backupDialogEl.showModal();
}


function download(name,text,type='application/json'){const u=URL.createObjectURL(new Blob([text],{type}));const a=document.createElement('a');try{a.href=u;a.download=name;document.body.appendChild(a);a.click();}finally{a.remove();setTimeout(()=>URL.revokeObjectURL(u),1000);}}
$('#login-form').addEventListener('submit',async e=>{e.preventDefault();const b=e.target.querySelector('button[type=submit]'),oidc=$('#oidc-login'),username=e.target.elements.username.value.trim(),value=e.target.elements.token.value;b.disabled=true;oidc.disabled=true;try{await api('login',username?{username,password:value}:{token:value.trim()});e.target.reset();$('#login-error').textContent='';await refresh();}catch(err){$('#login-error').textContent=err.message;}finally{b.disabled=false;oidc.disabled=false;}});
$('#oidc-login').addEventListener('click', async () => {
 if (oidcStartRequest) return;
 const button = $('#oidc-login'), submit = $('#login-form button[type=submit]'), generation = authGeneration, controller = new AbortController();
 oidcStartRequest = controller; button.disabled = true; submit.disabled = true; $('#login-error').textContent = '';
 const timeout = setTimeout(() => controller.abort(), 15000);
 try {
  const result = await api('oidc/start', {}, {signal:controller.signal});
  if (oidcStartRequest !== controller || generation !== authGeneration || $('#login').hidden) return;
  const target = new URL(result.authorizationURL);
  if (target.protocol !== 'https:' || target.username || target.password) throw new Error('身份提供方授权地址无效');
  window.location.assign(target.href);
 } catch (err) {
  if (generation === authGeneration && oidcStartRequest === controller) $('#login-error').textContent = err.name === 'AbortError' ? '连接身份提供方超时，请重试。' : err.message;
 } finally {
  clearTimeout(timeout);
  if (oidcStartRequest === controller) oidcStartRequest = null;
  button.disabled = false; submit.disabled = false;
 }
});
const loginQuery = new URLSearchParams(location.search);
if (loginQuery.get('oidc') === 'failed') {
 loginQuery.delete('oidc');
 const remainingQuery = loginQuery.toString();
 history.replaceState(history.state, '', location.pathname + (remainingQuery ? '?' + remainingQuery : '') + location.hash);
 $('#login-error').textContent = '单点登录未完成，请检查账户绑定或联系管理员。';
}
$('#logout').addEventListener('click',async()=>{try{await api('logout',{});showLogin();announceSessionChange();}catch(e){toast(e.message);}});
$('#refresh').addEventListener('click',()=>RulesPage.needsSync()?synchronizeRules():refresh());
$('#theme').addEventListener('click',()=>{document.body.classList.toggle('dark');try{localStorage.setItem('kurumi-theme',document.body.classList.contains('dark')?'dark':'light');}catch(_){/* private browsing may deny storage */}});
try{document.body.classList.toggle('dark',localStorage.getItem('kurumi-theme')==='dark');}catch(_){/* theme storage is optional */}
$('#nav').addEventListener('click',e=>{const b=e.target.closest('[data-page]');if(b)navigate(b.dataset.page);});
$('#cancel').addEventListener('click',()=>{if(!editing?.submitting)dialog.close();});
dialog.addEventListener('cancel',e=>{if(editing?.submitting)e.preventDefault();});
dialog.addEventListener('close',()=>scheduleRefresh(0));
$('#diff-close').addEventListener('click',()=>diffDialog.close());
diffDialog.addEventListener('close',()=>scheduleRefresh(0));
$('#editor').addEventListener('click',e=>{if(editing?.kind==='tunnel'){ProtocolEditor.actionClick(e.currentTarget,e.target);}});
$('#editor').addEventListener('change',updateFields);
$('#editor').addEventListener('click',e=>{
 if(editing?.kind!=='schedule'||editing.submitting)return;
 if(e.target.closest('[data-schedule-add]')){
  const rows=$('#editor [data-schedule-rows]');
  if(rows.querySelectorAll('[data-schedule-row]').length>=64){$('#form-error').textContent='最多添加 64 个事件';return;}
  rows.insertAdjacentHTML('beforeend',window.KurumiSchedules.row());
 }
 const remove=e.target.closest('[data-schedule-remove]');if(remove)remove.closest('[data-schedule-row]').remove();
 if(e.target.closest('[data-schedule-delete]'))saveSchedule(true);
});
$('#editor').addEventListener('input',e=>{if(editing?.kind==='tunnel'&&e.target.name==='localPort')updateFields();});
$('#editor').addEventListener('submit',async e=>{
 e.preventDefault();if(editing?.submitting)return;const f=e.target,b=f.querySelector('button[type="submit"]');b.disabled=true;
 $('#form-error').textContent='';
 if(editing?.kind==='schedule'){b.disabled=false;await saveSchedule(false);return;}
 if(editing?.kind==='tunnel-batch'){
  const current=editing, generation=authGeneration, controller=new AbortController();
  const value=ProtocolEditor.batchValue(f), revision=editRev;
  current.submitting=true;$('#dialog-body').inert=true;$('#cancel').disabled=true;
  const timeout=setTimeout(()=>controller.abort(),20000);
  const release=()=>{if(editing===current){current.submitting=false;$('#dialog-body').inert=false;$('#cancel').disabled=false;b.disabled=false;}};
  try {
   const result=await api('tunnels/batch',{revision,...value},{signal:controller.signal});
   if(generation!==authGeneration)return;
   RulesPage.clear();RulesPage.setNeedsSync(true);release();dialog.close();
   const refreshed=await refresh();
   if(generation!==authGeneration)return;
   if(refreshed && data?.revision>=result.revision)RulesPage.setNeedsSync(false);
   if(data && page==='tunnels')render();
   toast(`已保存 ${result.created} 条规则草稿；${RulesPage.needsSync()?'请先刷新并核对草稿':'运行中的实例需重新应用'}。`);
  } catch(err) {
   if(generation!==authGeneration)return;
   if(err.status===400){$('#form-error').textContent=err.message;return;}
   RulesPage.clear();RulesPage.setNeedsSync(true);release();dialog.close();
   if(data && page==='tunnels')render();
   if(err.status===409)toast('草稿版本已变化；请刷新并核对后重新填写。');
   else toast('批量创建结果尚未确认，服务端可能仍在处理。请稍后刷新并核对草稿，避免重复提交。');
  } finally {
   clearTimeout(timeout);
   release();
  }
  return;
 }
 try{const value=ProtocolEditor.value(f,editing);await api('save',{revision:editRev,kind:editing.kind,value});dialog.close();toast('草稿已保存；运行中的实例未改变');await refresh();}catch(err){$('#form-error').textContent=err.message;}finally{b.disabled=false;}
});
$('#content').addEventListener('input',e=>{if(e.target.id==='search'){filter=e.target.value;RulesPage.clear();paintRulesList();}if(e.target.id==='log-filter'){logFilter=e.target.value;paintLogs();}});
$('#content').addEventListener('change',e=>{if(e.target.id==='import-file')++importFileVersion;if(e.target.id==='health-instance')healthSelected=e.target.value;if(e.target.id==='protocol'){protocol=e.target.value;RulesPage.clear();paintRulesList();}if(page==='tunnels'&&data)RulesPage.change(e.target,rulesContext());if(e.target.id==='instance-picker'){cancelPageRequests();++navigationGeneration;selected=e.target.value;render();}if(e.target.id==='export-scope')syncExportScope();if(e.target.id==='update-version'){updateSelection=e.target.value;updateSelectionLost=!updateCanInstall();if(updateSelectionLost)updateSelection='';paintUpdate();}});
$('#content').addEventListener('focusout',()=>queueMicrotask(()=>{if(page!=='config'||!data)return;applyDeferredConfig();if(renderedSnapshot!==stateSnapshot&&canSilentRender())renderPreservingScroll();}));
async function synchronizeRules() {
 const generation=authGeneration, refreshed=await refresh();
 if(generation!==authGeneration)return;
 if(refreshed){RulesPage.setNeedsSync(false);if(data&&page==='tunnels')render();toast('规则草稿已刷新。');}
 else toast('无法刷新规则草稿，请稍后重试。');
}
async function exportSelectedRules() {
 if(data?.identity?.role!=='admin')return;
 const plan=RulesPage.plan(rulesContext(),null);
 if(!plan){RulesPage.clear();paintRulesList();toast('规则选择已失效，请重新选择。');return;}
 if(!confirm(`导出选中的 ${plan.ids.length} 条规则？\nZIP 内每个客户端一份 TOML，包含明文客户端认证令牌、配对密钥、HTTP 密码、SOCKS5 插件密码和自定义 HTTP 头值，文件不加密。请妥善保管。`))return;
 if(!data || data.revision!==plan.revision){toast('草稿已变化，请刷新后重新选择。');return;}
 const generation=authGeneration,navigation=navigationGeneration,controller=new AbortController();
 const timeout=setTimeout(()=>controller.abort(),60000);
 RulesPage.setBusy(true);render();
 try {
  const response=await fetch('/api/tunnels/export',{method:'POST',credentials:'same-origin',cache:'no-store',headers:{'Content-Type':'application/json'},body:JSON.stringify({revision:plan.revision,ids:plan.ids,confirmed:true}),signal:controller.signal});
  if(!response.ok){
   if(response.status===401&&generation===authGeneration){if(!window.KurumiDeferNodeLogout?.())showLogin();}
   let message='无法导出所选规则。';
   try{const result=await response.json();if(typeof result.error==='string')message=result.error;}catch(_){}
   const error=new Error(message);error.status=response.status;throw error;
  }
  if((response.headers.get('Content-Type')||'').split(';')[0].trim().toLowerCase()!=='application/zip')throw new Error('导出响应格式无效。');
  const declared=Number(response.headers.get('Content-Length')||0);
  if(declared>20*1024*1024)throw new Error('导出文件超过大小限制。');
  const blob=await response.blob();
  if(!blob.size||blob.size>20*1024*1024)throw new Error('导出文件无效或超过大小限制。');
  if(generation!==authGeneration||navigation!==navigationGeneration||page!=='tunnels'||!data||data.identity?.role!=='admin')return;
  if(data.revision!==plan.revision)throw new Error('草稿已变化，请刷新后重新导出。');
  download('kurumi-selected-rules.zip',blob,'application/zip');
  toast('所选配置已导出，文件包含明文认证信息，请妥善保管。');
 } catch(err) {
  if(generation!==authGeneration||navigation!==navigationGeneration||page!=='tunnels')return;
  if(err.status===409){RulesPage.clear();RulesPage.setNeedsSync(true);}
  toast(err.name==='AbortError'?'导出超时，请稍后重试。':err.message);
 } finally {
  clearTimeout(timeout);
  if(generation===authGeneration){RulesPage.setBusy(false);if(data&&page==='tunnels')render();}
 }
}
async function batchRuleOperation(operation) {
 const deleting=operation==='delete',enabled=operation==='enable';
 const plan=RulesPage.plan(rulesContext(),deleting?null:enabled);
 if(!plan){RulesPage.clear();paintRulesList();toast('规则选择已失效，请重新选择。');return;}
 if(!deleting&&!plan.changed){toast('所选规则的草稿意图已经一致。');return;}
 const action=deleting?'删除':enabled?'启用':'停用';
 const question=deleting?`删除选中的 ${plan.ids.length} 条规则草稿？如需恢复，请先确认已有可用快照。`:`将选中的 ${plan.ids.length} 条规则设为草稿${action}（其中 ${plan.changed} 条会改变）？`;
 if(!confirm(question+'\n运行中的实例不会立即改变；请在实例页重启应用。'))return;
 const generation=authGeneration;
 RulesPage.setBusy(true);render();
 try {
  const result=await api(deleting?'tunnels/batch-delete':'tunnels/batch-enabled',deleting?{revision:plan.revision,ids:plan.ids}:{revision:plan.revision,ids:plan.ids,enabled});
  if(generation!==authGeneration)return;
  RulesPage.clear(); RulesPage.setNeedsSync(true);
  const refreshed=await refresh();
  if(generation===authGeneration){if(refreshed)RulesPage.setNeedsSync(false);toast(`已${deleting?'删除':'保存'} ${result.changed} 条规则草稿${deleting?'':action+'意图'}；${refreshed?'实例需重新应用':'请先刷新草稿后继续操作'}。`);}
 } catch(err) {
  if(generation!==authGeneration)return;
  RulesPage.clear(); RulesPage.setNeedsSync(true);
  const refreshed=await refresh();
  if(generation===authGeneration){if(refreshed)RulesPage.setNeedsSync(false);toast('批量请求未完成或结果未确认：'+err.message+(refreshed?'。请核对当前草稿后重新选择。':'。请先刷新草稿后继续操作。'));}
 } finally {
  if(generation===authGeneration){RulesPage.setBusy(false);if(data&&page==='tunnels')render();}
 }
}
$('#content').addEventListener('click',async e=>{
 const b=e.target.closest('button');if(!b||!data)return;if(b.dataset.go){navigate(b.dataset.go);return;}
 const action=b.dataset.action,id=b.dataset.id || (action==='health'?$('#health-instance')?.value:undefined);if(!action)return;
  if(action==='clear-log-filter'){logFilter='';const input=$('#log-filter');if(input){input.value='';input.focus();}paintLogs();return;}
 if(action==='export-logs'){
  const view=logExportView, output=$('#log-output');
  if(page!=='logs'||!view||view.id!==selected||view.runId!==logRunId(data,selected)||view.filter!==logFilter||view.generation!==authGeneration||view.navigation!==navigationGeneration||!data.state.instances.some(i=>i.id===view.id)||(logAccessDenied?.id===view.id&&logAccessDenied.runId===view.runId)||!output||output.hidden||output.textContent!==view.text){
   logExportView=null;b.disabled=true;toast('日志已变化，请刷新后重新导出');return;
  }
  try {download(`kurumi-log-${view.id}-${new Date().toISOString().replace(/[:.]/g,'-')}.txt`,view.text,'text/plain;charset=utf-8');toast('已导出显示的日志，请妥善保管');}
  catch(err) {toast(err.message || '日志导出失败');}
  return;
 }
 const originPage=page, requestGeneration=authGeneration;
 let instanceOperation=null;
 if(action==='refresh-rules'){await synchronizeRules();return;}
 if(['new-tunnel','new-tunnel-batch','edit-tunnel','duplicate-tunnel','delete-tunnel','toggle','probe'].includes(action)&&RulesPage.isUnavailable()){toast('批量操作正在处理或草稿需要刷新，请稍后再操作。');return;}
 if(action==='clear-rule-selection'){RulesPage.clear();RulesPage.paint(rulesContext());return;}
 if(action==='batch-export'){await exportSelectedRules();return;}
 if(action==='batch-enable'||action==='batch-disable'||action==='batch-delete'){await batchRuleOperation(action.slice(6));return;}
 if(action==='new-instance'||action==='edit-instance'){openEditor('instance',id);return;}
 if(action==='edit-schedule'){openScheduleEditor(id);return;}
 if(action==='new-tunnel-batch'){openTunnelBatchEditor();return;}
 if(action==='new-tunnel'||action==='edit-tunnel'){openEditor('tunnel',id);return;}
 if(action==='import-toml'){
  window.KurumiTOMLImport.open({api, transfer:transferAPI, state:()=>data, generation:()=>authGeneration,
   blocked:transferBlocked, begin:beginTransfer, finish:finishTransfer, toast,
   stop:stopRefreshPolling, schedule:()=>scheduleRefresh(0)});
  return;
 }
 if(action==='logs'){selected=id;navigate('logs');return;}
 if(originPage==='instances' && id)window.KurumiInstances.clearError(id);
 b.disabled=true;
 try{
   if(action==='refresh-logs'){cancelLogRequest();stopLogStream();logAccessDenied=null;syncLogStream();await refreshLogs(false);return;}
  if(action==='webhook-settings'){await openWebhookDialog();return;}
  if(action==='health'){
   if (healthChecking || !id) return;
   healthChecking=true;
   const generation=authGeneration, navigation=navigationGeneration, origin=page, revision=data.revision, runtime=runtimeIdentity(data.runtime?.[id]);
   try {
    if (registrationRequest) await registrationRequest;
    if (generation!==authGeneration || navigation!==navigationGeneration || !data || revision!==data.revision) return;
    const report=await api('health',{revision,id});
    if(generation===authGeneration&&navigation===navigationGeneration&&data&&report.revision===revision&&data.revision===revision&&runtime===runtimeIdentity(data.runtime?.[id])&&data.state.instances.some(i=>i.id===id)&&page===origin&&['instances','health'].includes(origin)&&(origin!=='health'||$('#health-instance')?.value===id)&&!dialog.open&&!diffDialog.open&&!backupDialogEl?.open&&!webhookDialogEl?.open&&!healthDialog?.open&&!window.KurumiTOMLImport.isOpen())showHealth(report);
   } finally {if (generation===authGeneration) healthChecking=false;}
   return;
  }
  if(action==='duplicate-instance'){
   const instance=data.state.instances.find(i=>i.id===id);
   if(!instance)throw new Error('源实例已不存在');
   const rules=data.state.tunnels.filter(t=>t.instanceId===id).length;
   const includeTunnels=instance.kind==='frpc'&&rules>0&&confirm('是否同时复制 '+rules+' 条关联规则？\n复制的规则默认停用，以避免端口和域名冲突。');
   if(!confirm('复制「'+instance.name+'」作为独立草稿实例？\n不会启动实例；若为服务端将另选监听端口并关闭 HTTP(S) 监听。'))return;
   const copied=await api('duplicate',{revision:data.revision,kind:'instance',id,includeTunnels});
   toast('实例复制成功，附带 '+copied.rulesCopied+' 条停用规则；请检查配置后再启动');
   await refresh();return;
  }
  if(action==='duplicate-tunnel'){
   const tunnel=data.state.tunnels.find(t=>t.id===id);
   if(!tunnel)throw new Error('源规则已不存在');
   const clients=data.state.instances.filter(i=>i.kind==='frpc');
   const options=clients.map((i,n)=>(n+1)+': '+i.name).join('\n');
   const current=clients.findIndex(i=>i.id===tunnel.instanceId);
   const input=prompt('复制到哪个 frpc 客户端？输入编号：\n'+options,current+1);
   if(input===null)return;
   if(!/^[0-9]+$/.test(input.trim()))throw new Error('请输入有效的目标编号');
   const target=clients[Number(input.trim())-1];
   if(!target)throw new Error('目标客户端编号无效');
   if(target.id!==tunnel.instanceId && !confirm(`复制到「${target.name}」时将检查服务目标和回退关系；副本默认停用，请在启用前核对配置。`))return;
   const copied=await api('duplicate',{revision:data.revision,kind:'tunnel',id,targetInstanceId:target.id});
   toast('已创建停用的规则副本。请检查本地目标及远端端口后再启用');
   await refresh();return;
  }
  if(action==='check-update'){await checkUpdates();return;}
  if(action==='install-update'){
   const generation=authGeneration, tag=updateSelection;
   if(updateBusy())return;
   if(!updateCanInstall(tag)||$('#update-version').value!==tag){updateSelection='';updateSelectionLost=true;paintUpdate();throw new Error('所选版本不可用，请重新选择');}
   if(!confirm(`更新到 ${tag}？更新将中断工作台和托管连接。更新完成后请重新打开工作台并确认版本。`))return;
   updateInstalling=true;
   if(updatePollTimer!==null)clearTimeout(updatePollTimer);
   updatePollTimer=null; updateStatusRequest?.controller.abort(); updateStatusRequest=null;
   try {
    await api('update/install',{tag});
    if(generation!==authGeneration||!data)return;
    updateTaskRunning=true; updateStatusKnown=true; updateStatus='正在检查并准备更新';
   } catch(err) {
    if(generation!==authGeneration)return;
    updateStatusKnown=false;
    throw err;
   } finally {
    if(generation===authGeneration){updateInstalling=false;paintUpdate();scheduleUpdatePoll(generation,1000);}
   }
   return;
  }
   if(action==='export-data'){
    const generation=authGeneration, navigation=navigationGeneration, revision=data.revision, scope=$('#export-scope').value, snapshot=$('#export-snapshot').value;
    if(scope==='snapshot'&&!snapshot)throw new Error('没有可导出的快照');
    const query=new URLSearchParams({scope});if(scope==='snapshot')query.set('revision',snapshot);
    const result=await api('export?'+query);
    if(generation!==authGeneration||navigation!==navigationGeneration||page!=='config'||!data)return;
    if(data.revision!==revision||result.revision!==revision)throw new Error('配置已变化，请刷新后重新导出');
    download(`kurumi-${scope}.json`,JSON.stringify(result,null,2));toast('配置文件已导出，请妥善保管');return;
   }
   if(action==='import-data'){
    const generation=authGeneration, navigation=navigationGeneration, revision=data.revision, input=$('#import-file'), file=input.files[0], fileVersion=importFileVersion;
    const current=()=>generation===authGeneration&&navigation===navigationGeneration&&page==='config'&&!!data;
    if(transferBlocked())throw new Error(transferBlocked());
    if(renderedPage!=='config'||renderedRevision!==revision)throw new Error('配置已变化，请刷新后重新导入');
    if(!file)throw new Error('请选择配置文件');if(file.size>128*1024*1024)throw new Error('配置文件超过 128 MiB');
    if(!confirm('导入前须停止全部托管实例并等待操作结束。导入将替换当前配置、定时计划和快照；导入的计划会暂停，须检查后重新启用。继续？'))return;
    let parsed;try{parsed=JSON.parse(await file.text());}catch(_){if(!current())return;throw new Error('配置文件不是有效 JSON');}
    if(!current())return;
    if(revision!==data.revision||renderedRevision!==revision)throw new Error('配置已变化，请刷新后重新导入');
    const request={generation,kind:'import',submitted:false};
    beginTransfer(request);
    try{await transferAPI('import?revision='+revision,parsed);}
    catch(err){finishTransfer(request,'uncertain',err.message||'导入请求未完成');return;}
    if(generation===authGeneration && input.isConnected && input===$('#import-file') && importFileVersion===fileVersion && input.files[0]===file)input.value='';
    finishTransfer(request,'complete');return;
   }
  if(action==='history-settings'){const input=$('#history-limit'), limit=Number(input.value);if(input.value===''||!Number.isInteger(limit)||limit<0||limit>100)throw new Error('快照数量须为 0–100');if(limit<data.history.length&&!confirm(`将删除 ${data.history.length-limit} 份最早快照，继续？`))return;await api('history-settings',{revision:data.revision,historyLimit:limit});toast('快照保留数量已更新');await refresh();return;}
   if(action==='export-toml'){
    if(!selected)throw new Error('先选择一个实例');
    const generation=authGeneration, navigation=navigationGeneration, revision=data.revision, instance=selected;
    const v=await api('config?id='+encodeURIComponent(instance));
    if(generation!==authGeneration||navigation!==navigationGeneration||page!=='config'||selected!==instance||!data)return;
    if(data.revision!==revision)throw new Error('配置已变化，请刷新后重新导出');
    download('kurumi-redacted.toml',v.toml,'text/plain');return;
   }
  if(action==='probe-visible'){
   const revision=data.revision, generation=authGeneration;
   const targets=RulesPage.filtered(rulesContext());
   const probeable=targets.filter(t=>!Protocols.probeUnavailable(t));
   if(!probeable.length){toast(targets.length?'当前列表中的规则不支持本地检测':'当前列表没有规则');return;}
   probingAll=true;
   probeBatchProgress={done:0,total:probeable.length};
   const counts={healthy:0,redirect:0,responding:0,reachable:0,unreachable:0,invalid:0,error:0};
   let stale=false;
   render();
   for(const tunnel of probeable){
    if(generation!==authGeneration||!data||data.revision!==revision){stale=true;break;}
    probing[tunnel.id]=true;
    render();
    try{
     const result=await api('probe',{revision,id:tunnel.id});
     if(generation!==authGeneration||!data||data.revision!==revision){stale=true;break;}
     probeResults[tunnel.id]={...result,revision};
     counts[result.status]=(counts[result.status]||0)+1;
    }catch(error){
     if(generation!==authGeneration||!data||data.revision!==revision||error.message.includes('配置已在其他窗口更新')){stale=true;break;}
     probeResults[tunnel.id]={status:'error',message:error.message,revision,checkedAt:new Date().toISOString(),latencyMs:0};
     counts.error++;
    }finally{
     if(generation===authGeneration){delete probing[tunnel.id];if(probeBatchProgress)probeBatchProgress.done++;if(data)render();}
    }
    if(stale)break;
   }
   if(generation!==authGeneration)return;
   probingAll=false;
   probeBatchProgress=null;
   if(stale){await refresh(true);toast('配置发生变化，已停止批量检测；请刷新后重新检测。');}
   else{
    const good=counts.healthy+counts.reachable;
    const responding=counts.redirect+counts.responding;
    toast(`检测完成：${probeable.length} 条，正常/可连接 ${good} 条，已响应但需检查 ${responding} 条，不可连接/失败 ${counts.unreachable+counts.invalid+counts.error} 条；${targets.length-probeable.length} 条不支持本地检测。`);
   }
   if(data)render();
   return;
  }
  if(action==='diff'){const generation=authGeneration, result=await api('diff?id='+encodeURIComponent(id));if(generation===authGeneration&&data)showDiff(result);return;}
  if(action==='probe'){
   const revision=data.revision, generation=authGeneration;
   probing[id]=true;
   render();
   try {
    const v=await api('probe',{revision,id});
    if(generation!==authGeneration)return;
    if(data && data.revision===revision) {
     probeResults[id]={...v,revision};
     render();
     toast(v.message);
    } else {
     await refresh(true);
     toast('配置已变化，本次检测结果已丢弃，请重新检测。');
    }
   } finally {
    if(generation===authGeneration){delete probing[id];if(data) render();}
   }
   return;
  }
  if(action==='validate'){
   if(originPage==='instances'){instanceOperation=window.KurumiInstances.begin(id,action);if(!instanceOperation)return;}
   await api('validate',{revision:data.revision,id});toast('配置校验通过；尚未执行网络探测');return;
  }
  if(['start','restart','stop'].includes(action)){
   const plan=data.schedules?.[id], pause=plan?.enabled&&!plan?.paused?'\n此操作通过初步检查后会暂停定时计划，即使操作失败也需管理员重新启用。':'';
   if(action==='restart'&&!confirm('重启应用将中断该实例的现有连接。配置检查失败时不会停止当前实例；重新启动失败时不会自动回退。'+pause+'\n继续？'))return;
   if(action==='stop'&&!confirm('停止该实例及其正在转发的连接？'+pause))return;
   if(action==='start'&&pause&&!confirm('启动该实例？'+pause))return;
   if(originPage==='instances'){instanceOperation=window.KurumiInstances.begin(id,action);if(!instanceOperation)return;}
   await api('control',{revision:data.revision,id,action});toast(action==='stop'?'实例已停止':'启动请求完成，正在刷新实例状态');
  }else if(action.startsWith('delete-')){if(!confirm('删除这项草稿配置？历史版本仍保留。'))return;await api('delete',{revision:data.revision,id,kind:action.slice(7)});toast('草稿配置已删除');}
  else if(action==='toggle'){const t=data.state.tunnels.find(t=>t.id===id);await api('save',{revision:data.revision,kind:'tunnel',value:{...t,enabled:!t.enabled}});toast('启停意图已保存，请在实例页应用');}
  else if(action==='restore'){if(!confirm('恢复历史草稿？全部托管实例必须已停止，当前草稿也会保留为历史版本。当前定时计划将暂停，不存在的实例对应的计划将删除。'))return;await api('restore',{revision:data.revision,target:+b.dataset.target});toast('历史草稿已恢复，定时计划已暂停');}
  await refresh();
 }catch(err){
  toast(err.message);
  if(originPage==='instances' && id && requestGeneration===authGeneration && data?.state.instances.some(i=>i.id===id))window.KurumiInstances.setError(id,err.message);
  // A restart can fail after the old runtime has stopped; refresh its state.
  if(instanceOperation && requestGeneration===authGeneration && data)await refresh();
 }finally{
  if(instanceOperation)window.KurumiInstances.end(id,instanceOperation);
  if(action==='install-update')paintUpdate();else b.disabled=false;
  if(originPage==='instances' && id)window.KurumiInstances.sync(id);
 }
});
$('#content').addEventListener('click',e=>{const b=e.target.closest('[data-backup-action]');if(b)openBackupDialog(b.dataset.backupAction);});
window.addEventListener('hashchange',()=>{const p=location.hash.slice(1);if(p!=='nodes'&&window.KurumiNodeNavigationBlocked?.()){location.hash='nodes';return;}if(titles[p]&&page!==p){cancelPageRequests();++navigationGeneration;page=p;render();refreshRegistration();}});
page=titles[location.hash.slice(1)]?location.hash.slice(1):'overview';
// core.js consumes a launch ticket before the first authenticated state request.
if(!/^#launch=([a-f0-9]{64})$/.test(location.hash))refresh(true);
document.addEventListener('visibilitychange', () => {
 if (document.hidden) {
  stopRefreshPolling();stopLogStream();cancelLogRequest();Overview.pause();++registrationEpoch;clearTimeout(registrationExpiryTimer);registrationExpiryTimer=null;registrations=Object.create(null);registrationRevision=null;registrationProcesses=Object.create(null);paintRegistrations(true);
 }
 else if (data && !dialog.open && !window.KurumiTOMLImport.isOpen() && !window.KurumiNodeDialog?.open) refresh(true);
});
