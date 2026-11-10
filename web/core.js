'use strict';
// This file extends the existing workspace without coupling core downloads to
// configuration revision writes or the five-second instance refresh.
titles.core = '核心管理';
const coreRenderBase = render;
let coreStatus, coreRelease, coreReleases = [], coreError = '', coreLoading = false;
let coreStatusETag = '', coreStatusRequest = null, corePollTimer = null;
let coreReleasesLoaded = false, coreReleaseCache = new Map();
render = function () {
 coreRenderBase();
 if (data && page === 'core') mountCore();
 else stopCorePolling();
};
const corePhase = {idle:'等待选择版本', metadata:'读取官方发布信息', downloading:'下载中', extracting:'校验通过，正在安全解压', installed:'下载完成，等待启用', failed:'下载失败，原核心未改变', cancelled:'下载已取消，原核心未改变'};
const coreBusy = () => ['metadata','downloading','extracting'].includes(coreStatus?.job.state);
const coreSize = n => (Number(n||0)/1048576).toFixed(1)+' MiB';
function mountCore() {
 if (data?.runtimeBackend === 'embedded') {
  stopCorePolling();
  $('#content').innerHTML = header('内置核心', '当前版本直接运行内置核心，无需下载外部程序。') +
   '<section class="panel"><div class="panel-content"><p>内置 frp 随工作台更新；当前为开发候选，部分协议、并发运行及异常恢复仍待验收。</p></div></section>';
  return;
 }
 if ($('#core-screen')) {paintCoreStatus();scheduleCorePoll(0);return;}
 $('#content').innerHTML = header('frp 核心管理','从官方发布中选择版本。识别的是运行本程序的主机，不是当前浏览器设备。') + `
 <div id="core-screen">
 <section class="hero core-hero"><div><p class="eyebrow">OFFICIAL RELEASES / VERIFIED DOWNLOADS</p><h2>选好版本，剩下的交给工作台。</h2><p>自动匹配平台 · SHA-256 校验 · 独立版本目录 · 明确切换，不覆盖运行中的程序</p></div><span class="core-download-icon" aria-hidden="true">↓</span></section>
 <div class="grid-two"><section class="panel"><div class="panel-title"><h3>下载安装</h3><button id="core-reload">刷新版本</button></div><div class="panel-content core-form">
 <div class="check-row"><span>运行主机</span><strong id="core-host">正在识别…</strong></div>
 <label>frp 稳定版本<select id="core-version" aria-label="frp 稳定版本"><option>读取官方版本…</option></select></label>
 <label>系统与架构<select id="core-asset" aria-label="核心平台"><option>请先选择版本</option></select></label>
 <p id="core-asset-note" class="small-note"></p><div class="actions"><button id="core-install" class="primary" disabled>下载到本机</button><a id="core-official" class="core-link" target="_blank" rel="noopener noreferrer" hidden>官方安装包 ↗</a></div>
 <p class="small-note">其他平台可取官方安装包，但不能在本机安装。缺少 SHA-256 信息的版本不会自动安装。</p>
 <p id="core-error" class="error" role="alert"></p>
 </div></section><section class="panel"><div class="panel-title"><h3>下载任务</h3><span class="pill accent">SHA-256</span></div><div class="panel-content">
 <strong id="core-job-title">暂无任务</strong><progress id="core-progress" max="100" value="0" aria-label="核心下载进度"></progress><p id="core-job-detail" class="small-note" role="status"></p><button id="core-cancel" hidden>取消下载</button>
 <div class="notice">下载完成后点击「启用此版本」。切换前须停止全部实例；切换不会自动启动实例。旧版本保留，可随时重新选择。</div>
 </div></section></div>
 <section class="panel"><div class="panel-title"><h3>已下载核心</h3><span class="muted">frpc + frps 成对管理</span></div><div class="panel-content"><p id="core-bundled" class="small-note" hidden></p><div id="core-installed"></div></div></section>
 <p class="small-note">下载仅连接 GitHub 官方发布与其文件存储。网络受限时可为 KurumiFRP 启动进程配置 HTTPS_PROXY；不会自动使用第三方镜像或关闭证书校验。</p></div>`;
 $('#core-reload').onclick = () => loadCoreReleases(true);
 $('#core-version').onchange = () => loadCoreRelease($('#core-version').value);
 $('#core-asset').onchange = paintCoreAsset;
 $('#core-install').onclick = async () => {
  const asset = coreRelease?.assets.find(a=>a.name===$('#core-asset').value);
  if (!asset?.installable) return;
  $('#core-install').disabled = true;
  try {await api('core/install',{tag:coreRelease.tag_name,platform:asset.platform});coreError='';await pollCore();}
  catch(e){coreError=e.message;paintCoreStatus();}
 };
 $('#core-cancel').onclick=async()=>{try{await api('core/cancel',{});await pollCore();}catch(e){toast(e.message);}};
 $('#core-installed').onclick=async e=>{
  const b=e.target.closest('[data-core-activate]');if(!b)return;b.disabled=true;
  try {await api('core/activate',{id:b.dataset.coreActivate});toast('核心已启用；在运行实例页校验配置后启动');await pollCore();await refresh(true);}
  catch(e){toast(e.message);}finally{b.disabled=false;}
 };
 scheduleCorePoll(0);loadCoreReleases();
}
function stopCorePolling() {
 if (corePollTimer !== null) {
  clearTimeout(corePollTimer);
  corePollTimer = null;
 }
}
function scheduleCorePoll(delay) {
 stopCorePolling();
 if (!data || data.runtimeBackend === 'embedded' || page !== 'core' || document.hidden) return;
 const wait = delay ?? (coreBusy() ? 1500 : 10000);
 corePollTimer = setTimeout(() => {
  corePollTimer = null;
  pollCore();
 }, wait);
}
async function pollCore() {
 if (!data || data.runtimeBackend === 'embedded' || page !== 'core' || document.hidden) return;
 if (coreStatusRequest) return coreStatusRequest;
 const request = (async () => {
  try {
   const fresh = await api('core', undefined, {
    conditional: true,
    headers: coreStatusETag ? {'If-None-Match': coreStatusETag} : {}
   });
   if (fresh.value !== null) {
    coreStatus = fresh.value;
    coreStatusETag = fresh.etag;
    paintCoreStatus();
   }
  } catch (e) {
   coreError = e.message;
   paintCoreStatus();
  }
 })();
 coreStatusRequest = request;
 try {
  await request;
 } finally {
  if (coreStatusRequest === request) coreStatusRequest = null;
  scheduleCorePoll();
 }
}
function paintCoreStatus() {
 if (!$('#core-screen'))return;
 $('#core-error').textContent=coreError;
 if (!coreStatus)return;
 const bundledNote=$('#core-bundled'); bundledNote.hidden=!coreStatus.bundled && !coreStatus.bundledError;
 if (coreStatus.bundled) bundledNote.textContent=`安装包预置 ${coreStatus.bundled.tag} · ${coreStatus.bundled.platform} · ${coreStatus.bundledInUse?'正在使用':'备用'}；预置版本可能随软件升级变化。`;
 else if (coreStatus.bundledError) bundledNote.textContent=coreStatus.bundledError;
 $('#core-host').textContent=coreStatus.host+(coreStatus.supported?' · 已支持':' · 暂不支持自动安装');
 const j=coreStatus.job;
 $('#core-job-title').textContent=corePhase[j.state]||j.state;
 $('#core-progress').value=j.total ? Math.min(100,100*j.bytes/j.total) : 0;
 $('#core-job-detail').textContent=j.error || (j.tag ? `${j.tag} · ${coreSize(j.bytes)} / ${coreSize(j.total)}`:'选择平台和版本开始。');
 $('#core-cancel').hidden=!coreBusy();
 const running=Object.values(data.runtime).some(v=>v.state==='running');
 $('#core-installed').innerHTML=coreStatus.installed.length ? coreStatus.installed.map(v=>`<div class="core-installed-row"><div><b>${esc(v.tag)}</b><span class="pill ${v.id===coreStatus.active?'good':''}">${v.id===coreStatus.active?'当前启用':esc(v.platform)}</span><p class="small-note">${esc(v.installedAt)}<br>SHA-256 ${esc(v.archiveSHA256.slice(0,20))}…</p></div><button data-core-activate="${esc(v.id)}" ${v.id===coreStatus.active||running?'disabled':''}>${v.id===coreStatus.active?'已启用':running?'先停止实例':'启用此版本'}</button></div>`).join('') : empty('尚未下载核心','选择与你的主机匹配的版本。旧版手动放入 frp-dir 的文件仍然可用。');
 paintCoreAsset();
}
async function loadCoreReleases(force=false) {
 if(coreLoading)return;
 if(!force && coreReleasesLoaded) {
  try {await showCoreReleaseOptions(false);}
  catch(e) {coreError=e.message;paintCoreStatus();}
  return;
 }
 if(force)coreReleasesLoaded=false;
 coreLoading=true;coreError='';
 try {
  coreReleases=await api('core/releases');
  if(!coreReleases.length)throw new Error('没有可用的稳定版本');
  coreReleasesLoaded=true;
  await showCoreReleaseOptions(force);
 }catch(e){coreError=e.message;}finally{coreLoading=false;paintCoreStatus();}
}
async function showCoreReleaseOptions(force=false) {
 if(!$('#core-screen'))return;
 const previous=coreRelease?.tag_name;
 $('#core-version').innerHTML=coreReleases.map((r,i)=>`<option value="${esc(r.tag_name)}">${esc(r.tag_name)}${r.pinnedFallback?' · 临时固定版本，列表受限':i===0?' · 最新稳定版':''}</option>`).join('');
 if(!coreReleases.length)throw new Error('没有可用的稳定版本');
 if(coreReleases.some(r=>r.tag_name===previous))$('#core-version').value=previous;
 if(force)coreRelease=null;
 const tag=$('#core-version').value;
 if(coreRelease?.tag_name===tag) {
  showCoreAssetOptions(coreRelease);
  return;
 }
 await loadCoreRelease(tag,force);
}
let coreSelection=0;
function showCoreAssetOptions(release) {
 const previous=$('#core-asset')?.value;
 const options=release.assets.slice().sort((a,b)=>Number(b.compatible)-Number(a.compatible));
 $('#core-asset').innerHTML=options.map(a=>`<option value="${esc(a.name)}">${esc(a.platform)}${a.compatible?' · 匹配本机':''} · ${coreSize(a.size)}</option>`).join('');
 if(options.some(a=>a.name===previous))$('#core-asset').value=previous;
 paintCoreAsset();
}
async function loadCoreRelease(tag,force=false) {
 if(!force && coreRelease?.tag_name===tag) {coreSelection++;showCoreAssetOptions(coreRelease);return;}
 if(!force && coreReleaseCache.has(tag)) {
  coreSelection++;
  coreRelease=coreReleaseCache.get(tag);
  coreError='';
  showCoreAssetOptions(coreRelease);
  return;
 }
 const generation=++coreSelection;coreRelease=null;paintCoreAsset();
 try {
  const r=await api('core/release?tag='+encodeURIComponent(tag));
  if(generation!==coreSelection||!$('#core-screen'))return;
  coreRelease=r;coreReleaseCache.set(tag,r);coreError='';
  showCoreAssetOptions(r);
 }catch(e){coreError=e.message;}paintCoreStatus();
}
function paintCoreAsset() {
 if(!$('#core-screen'))return;
 const asset=coreRelease?.assets.find(a=>a.name===$('#core-asset').value);
 $('#core-install').disabled=!asset?.installable || coreBusy();
 $('#core-official').hidden=!asset;
 if(asset)$('#core-official').href=asset.browser_download_url;
 $('#core-asset-note').textContent=asset ? asset.installable ? `${asset.name} · 下载后进行 SHA-256 校验` : '此安装包不匹配本机或缺少校验信息，只提供官方下载入口。' : '等待版本信息；失败时可点击刷新重试。';
}
document.addEventListener('visibilitychange', () => {
 if (document.hidden) stopCorePolling();
 else if (page === 'core') scheduleCorePoll(0);
});
if(location.hash==='#core'){page='core';render();}
(async()=>{
 const match=/^#launch=([a-f0-9]{64})$/.exec(location.hash);
 if(!match)return;
 history.replaceState(null,'',location.pathname+location.search+'#core');
 try{
  for(let attempt=0;;attempt++) {
   try {await api('launch-login',{ticket:match[1]});break;}
   catch(error) {
    if(error.status!==429 || attempt>=9)throw error;
    await new Promise(resolve=>setTimeout(resolve,500));
   }
  }
  page='core';await refresh();
 }
 catch(e){$('#login-error').textContent=e.message;}
})();

$('#quit-app').onclick=async()=>{
 if(!confirm('退出 KurumiFRP 并停止全部托管实例？当前连接会中断，配置会保留。'))return;
 try{await api('quit',{});showLogin();$('#login-error').textContent='程序已退出。双击桌面快捷方式重新打开。';}
 catch(e){toast(e.message);}
};
