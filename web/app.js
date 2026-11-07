'use strict';
const $ = (s) => document.querySelector(s);
const esc = (v = '') => String(v ?? '').replace(/[&<>"']/g, (c) => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const titles = {overview:'网络总览',tunnels:'连接看板',instances:'运行实例',config:'配置中心',logs:'运行记录',update:'软件更新'};
let updateChecked = false, releases = [], updateStatus = '';
let data, page = 'overview', selected = '', filter = '', protocol = '', editing, editRev, serial = 0, toastTimer;
const dialog = $('#dialog');
function toast(text) { $('#toast').textContent = text; $('#toast').hidden = false; clearTimeout(toastTimer); toastTimer = setTimeout(() => $('#toast').hidden = true, 8000); }
async function api(path, body) {
 const response = await fetch('/api/' + path, {method:body === undefined ? 'GET':'POST',credentials:'same-origin',headers:body === undefined ? {}:{'Content-Type':'application/json'},body:body === undefined ? undefined:JSON.stringify(body)});
 const json = await response.json();
 if (!response.ok) {if(response.status === 401 && path !== 'login') showLogin(); throw new Error(json.error || `HTTP ${response.status}`);}
 return json;
}
function showLogin() {updateChecked=false;$('#login').hidden = false; $('#app').hidden = true; data = null; if (dialog.open) dialog.close();}
async function refresh(silent = false) {
 try { const fresh = await api('state'); data = fresh; $('#login').hidden = true; $('#app').hidden = false; if(!updateChecked){updateChecked=true;checkUpdates(true);}
 $('#revision').textContent = `DRAFT / r${data.revision}`; $('#sync-time').textContent = `更新于 ${new Date().toLocaleTimeString('zh-CN')}`;
 if (!dialog.open && !$('#content').contains(document.activeElement)) render(); else if (!silent && !dialog.open) render();
 } catch(e) {if (!silent && data) toast(e.message);}
}
const stateOf = (id) => data.runtime[id] || {state:'stopped'};
function badge(id) {const v=stateOf(id);return `<span class="pill ${v.state==='running'?'good':v.state==='exited'?'warn':''}">${({running:'进程运行',stopped:'未启动',exited:'进程已退出'})[v.state] || esc(v.state)}</span>${v.pending?'<span class="pill warn">待应用</span>':''}`;}
function header(title,subtitle,action='') {return `<div class="page-head"><div><p class="eyebrow">KURUMI / ${esc(page.toUpperCase())}</p><h1>${title}</h1><p>${subtitle}</p></div>${action}</div>`;}
function empty(title,text,action='') {return `<div class="empty"><span class="empty-symbol">◈</span><strong>${title}</strong><p>${text}</p>${action}</div>`;}
function time(v) {return new Date(v).toLocaleString('zh-CN',{hour12:false});}
function recent() {return data.events.length?`<div class="timeline">${data.events.slice(-6).reverse().map(e=>`<div class="event"><p>${esc(e.message)}</p><small>${time(e.at)}</small></div>`).join('')}</div>`:empty('还没有操作记录','保存第一份配置后，变更记录会出现在这里。');}
function render() {
 if (!data) return;
 const n = ++serial; $('#page-title').textContent=titles[page]; document.title=titles[page]+' · KurumiFRP';
 document.querySelectorAll('[data-page]').forEach(el=>el.classList.toggle('active',el.dataset.page===page));
 const content=$('#content');
 if(page==='overview') {
  const running=Object.values(data.runtime).filter(v=>v.state==='running').length;
  const pending=Object.values(data.runtime).filter(v=>v.pending).length;
  content.innerHTML=header('连接，尽在掌握','管理本机的每一个实例，清楚区分配置意图与运行状态。','<button class="primary" data-action="new-instance">＋ 新建实例</button>')+
  `<section class="hero"><div><p class="eyebrow">KURUMI · CONNECTION ATELIER</p><h2>把连接交给时间，<br>把从容留给自己。<span class="hero-heart"> ♡</span></h2><p>和狂三一起管理本机的每一条连接。先保存草稿、检查配置，再决定何时应用。</p><div class="hero-caption"><span>Ⅻ</span> EACH MOMENT, CONNECTED</div></div><div class="hero-graphic" aria-hidden="true"><span class="hero-stamp">KURUMI<br>FRP</span></div></section>`+
  `<section class="metrics">${[[data.state.instances.length,'已登记实例','本机 frpc / frps'],[running,'进程运行中','链路状态尚未探测'],[data.state.tunnels.filter(t=>t.enabled).length,'期望启用规则','配置意图，不等于已注册'],[pending,'存在待应用配置','保存不会打断当前连接']].map(([v,l,h])=>`<div class="metric"><span class="label">${l}</span><strong>${v.toString().padStart(2,'0')}</strong><small>${h}</small></div>`).join('')}</section>`+
  `<div class="grid-two"><section class="panel"><div class="panel-title"><h3>连接概览</h3><button class="quiet" data-go="tunnels">全部连接 →</button></div>${tunnelTable(data.state.tunnels.slice(0,5))}</section><section class="panel"><div class="panel-title"><h3>环境与边界</h3><span class="pill accent">LOCAL</span></div><div class="panel-content"><div class="check-row"><span>frpc 二进制</span><span class="pill ${data.binaries.frpc?'good':'warn'}">${data.binaries.frpc?'已找到':'未安装'}</span></div><div class="check-row"><span>frps 二进制</span><span class="pill ${data.binaries.frps?'good':'warn'}">${data.binaries.frps?'已找到':'未安装'}</span></div><div class="check-row"><span>配置保存</span><span class="pill good">本地持久化</span></div><div class="check-row"><span>远端设备管理</span><span class="pill">尚未实现</span></div><p class="small-note blank">配置文件包含密钥，请妥善保管。指定 frp-dir 中的二进制属于受信任程序；请仅放入官方或自行审计的版本。</p></div></section></div>`+
  `<section class="panel"><div class="panel-title"><h3>最近变更</h3><span class="muted">版本可恢复 · 最多保留 ${data.historyLimit} 份</span></div><div class="panel-content">${recent()}</div></section>`;
 } else if(page==='instances') {
  content.innerHTML=header('运行实例','一个实例对应一个本机 frp 进程；远程服务器地址不代表远程管理权限。','<button class="primary" data-action="new-instance">＋ 新建实例</button>')+
  `<div class="notice">启动前会调用官方 frp verify。重启应用会中断该实例上的现有连接；本版本没有自动回退和开机自动启动实例。</div>`+
  (data.state.instances.length?`<div class="instance-grid">${data.state.instances.map(i=>{const v=stateOf(i.id);return `<article class="instance-card"><div class="instance-title"><div><span class="type-icon">${i.kind.toUpperCase()}</span><h3>${esc(i.name)}</h3><small>${i.kind==='frpc'?'客户端 · 上游入口':'服务端 · 本机监听'}</small></div><div>${badge(i.id)}</div></div><div class="instance-endpoint">${esc(i.host)}:${i.port}</div><div class="instance-info"><span>${data.state.tunnels.filter(t=>t.instanceId===i.id).length} 条规则</span><span>PID ${v.pid || '—'}</span><span>认证令牌 ${i.hasToken?'已保存':'缺失'}</span></div><div class="actions"><button data-action="edit-instance" data-id="${i.id}">编辑</button><button data-action="duplicate-instance" data-id="${i.id}">复制实例</button><button data-action="validate" data-id="${i.id}">校验</button>${v.state==='running'?`<button data-action="restart" data-id="${i.id}">重启应用</button><button data-action="stop" data-id="${i.id}">停止</button>`:`<button class="primary" data-action="start" data-id="${i.id}">启动</button>`}<button data-action="logs" data-id="${i.id}">日志</button><button class="danger" data-action="delete-instance" data-id="${i.id}">删除</button></div>${v.exit?`<p class="small-note">${esc(v.exit)}</p>`:''}</article>`;}).join('')}</div>`:empty('添加第一个运行实例','本机可以同时运行多个 frpc 和 frps。','<button data-action="new-instance" class="primary">新建实例</button>'));
 } else if(page==='tunnels') {
  content.innerHTML=header('连接看板','把需要访问的服务整理成规则；修改后在运行实例中校验并应用。','<button class="primary" data-action="new-tunnel">＋ 新建规则</button>')+
  `<div class="toolbar"><input id="search" placeholder="搜索规则、地址或实例…" aria-label="搜索规则" value="${esc(filter)}"><select id="protocol" aria-label="按协议筛选"><option value="">所有协议</option>${['tcp','udp','http','https','stcp','stcp-visitor'].map(t=>`<option ${protocol===t?'selected':''}>${t}</option>`).join('')}</select></div><section class="panel" id="tunnel-list">${filteredTable()}</section>`;
 } else if(page==='config') {
  ensureSelected();
  content.innerHTML=header('配置中心','预览配置、导出数据和恢复历史快照。')+
  `<div class="grid-two"><section class="panel"><div class="panel-title"><h3>实例配置预览</h3><button data-action="export-toml">导出脱敏 TOML</button></div><div class="panel-content">${picker()}<div class="notice">此预览中的 &lt;redacted&gt; 是占位符，不能直接部署。</div><pre id="config-output">${selected?'正在读取…':'先创建一个实例。'}</pre></div></section><section class="panel"><div class="panel-title"><h3>历史快照</h3><span class="pill">当前 r${data.revision}</span></div><div class="panel-content"><label>保留数量（0–100）<input id="history-limit" type="number" min="0" max="100" step="1" value="${data.historyLimit}"></label><button data-action="history-settings">保存数量</button><p class="small-note">减少数量时立即删除最早的快照；设为 0 则不保留快照。</p>${data.history.length?data.history.slice().reverse().map(h=>`<div class="check-row"><div><b>r${h.revision}</b><p class="small-note blank">${esc(h.summary)}<br>${time(h.at)}</p></div><button data-action="restore" data-target="${h.revision}">恢复</button></div>`).join(''):empty('暂无历史版本','修改配置时按保留数量保存前一版本。')}<p class="small-note blank">恢复前须停止全部托管实例。恢复只改变草稿，不会自动重新启动进程。</p></div></section></div><section class="panel"><div class="panel-title"><h3>导出配置和快照</h3></div><div class="panel-content"><label>导出范围<select id="export-scope"><option value="all">当前配置和全部快照</option><option value="config">仅当前配置</option><option value="snapshots">仅全部快照</option><option value="snapshot">单个快照</option></select></label><label>单个快照<select id="export-snapshot">${data.history.slice().reverse().map(h=>`<option value="${h.revision}">r${h.revision} · ${esc(h.summary)}</option>`).join('')}</select></label><p class="small-note">导出的单个配置文件包含密钥和所选快照，可直接导入其他设备。任何持有该文件的人都能访问其中的密钥，请妥善保管。</p><button data-action="export-data">导出配置文件</button><hr><label>选择配置文件<input id="import-file" type="file" accept=".json,application/json"></label><button data-action="import-data">导入并刷新</button><p class="small-note">导入会替换当前配置与历史快照，运行中的实例需重启应用新配置。</p></div></section>`;
  if(selected) api('config?id='+encodeURIComponent(selected)).then(v=>{if(serial===n && $('#config-output')) $('#config-output').textContent=v.toml;}).catch(e=>toast(e.message));
 } else if(page==='update') {
  content.innerHTML=header('软件更新','从 GitHub 获取官方版本，选择可用的新版本进行更新。')+`<section class="panel"><div class="panel-content"><p>当前版本 ${esc(data.version)}。更新期间工作台和托管连接可能中断；Windows 需要管理员授权。</p><label>选择版本<select id="update-version">${releases.filter(v=>v.newer&&v.available).map(v=>`<option value="${esc(v.tag)}">${esc(v.tag)}${v.prerelease?' · 预览版':''}</option>`).join('')}</select></label><div class="actions"><button data-action="check-update">检查版本</button><button class="primary" data-action="install-update" ${releases.some(v=>v.newer&&v.available)?'':'disabled'}>更新所选版本</button></div><p class="small-note">${esc(updateStatus||'选择版本后将下载并校验官方安装包。')}</p></div></section>`;
 } else if(page==='logs') {
  ensureSelected(); content.innerHTML=header('运行记录','真实子进程输出与配置变更记录。进程日志仅保留当前会话最近 200 行。')+
  `<div class="grid-two"><section class="panel"><div class="panel-title"><h3>frp 标准输出</h3><span class="pill">自动刷新</span></div><div class="panel-content">${picker()}<pre id="log-output">正在读取…</pre></div></section><section class="panel"><div class="panel-title"><h3>配置变更</h3></div><div class="panel-content">${recent()}</div></section></div>`;
  api('logs?id='+encodeURIComponent(selected)).then(v=>{if(serial===n && $('#log-output')) $('#log-output').textContent=v.lines.join('\n')||'没有日志。启动实例后，真实输出将显示在这里。';}).catch(e=>toast(e.message));
 }
}
async function checkUpdates(promptUser=false){try{const v=await api('update/releases');releases=v.versions;updateStatus=v.status||'';if(page==='update'&&data)render();const latest=releases.find(v=>v.newer&&v.available);if(promptUser&&latest&&confirm(`发现新版本 ${latest.tag}。现在查看更新吗？`))navigate('update');}catch(e){updateStatus='无法获取版本列表：'+e.message;if(page==='update'&&data)render();}}
async function pollUpdate(){try{const v=await api('update/status');updateStatus=v.status;if(page==='update'&&data)render();if(updateStatus.startsWith('正在'))setTimeout(pollUpdate,2000);}catch(_){updateStatus='工作台暂时不可用，请稍后刷新。';}}
function ensureSelected(){if(!data.state.instances.some(i=>i.id===selected))selected=data.state.instances[0]?.id||'';}
function picker(){return `<select class="config-picker" id="instance-picker" aria-label="选择实例">${data.state.instances.length?data.state.instances.map(i=>`<option value="${i.id}" ${selected===i.id?'selected':''}>${esc(i.name)} · ${i.kind}</option>`).join(''):'<option>尚无实例</option>'}</select>`;}
function filteredTable(){return tunnelTable(data.state.tunnels.filter(t=>(!protocol||t.type===protocol)&&`${t.name} ${t.localHost} ${t.remotePort} ${data.state.instances.find(i=>i.id===t.instanceId)?.name}`.toLowerCase().includes(filter.toLowerCase())));}
function tunnelTable(rows) {
 if(!rows.length)return empty('这里还静悄悄的 ♡','先添加 frpc 实例，再创建 TCP、UDP、Web 或 STCP 规则。','<button data-action="new-tunnel">＋ 添加规则</button>');
 return `<div class="table-wrap"><table><thead><tr><th>服务 / 归属</th><th>协议</th><th>本地目标</th><th>远端 / 配对</th><th>配置意图</th><th>操作</th></tr></thead><tbody>${rows.map(t=>`<tr><td><strong>${esc(t.name)}</strong><small>${esc(data.state.instances.find(i=>i.id===t.instanceId)?.name)}</small></td><td><span class="type-icon">${t.type.toUpperCase()}</span></td><td><code>${esc(t.localHost)}:${t.localPort}</code></td><td><code>${esc(t.type==='stcp-visitor'?t.serverName:t.type==='stcp'?'私密配对':t.type==='http'||t.type==='https'?t.domains.join(', '):t.remotePort)}</code></td><td><span class="pill ${t.enabled?'good':''}">${t.enabled?'期望启用':'期望停用'}</span></td><td><div class="actions"><button data-action="edit-tunnel" data-id="${t.id}">编辑</button><button data-action="duplicate-tunnel" data-id="${t.id}">复制规则</button><button data-action="toggle" data-id="${t.id}">${t.enabled?'停用':'启用'}</button><button class="danger" data-action="delete-tunnel" data-id="${t.id}">删除</button></div></td></tr>`).join('')}</tbody></table></div>`;
}
function field(name,label,value='',type='text',extra=''){return `<label>${label}<input name="${name}" type="${type}" value="${esc(value)}" ${extra}></label>`;}
function openEditor(kind,id) {
 const old=(kind==='instance'?data.state.instances:data.state.tunnels).find(v=>v.id===id);editing={kind,id:old?.id||''};editRev=data.revision;
 $('#form-error').textContent='';
 if(kind==='instance') {
  const v=old||{kind:'frpc',host:'127.0.0.1',port:7000,httpPort:0,httpsPort:0};
  $('#dialog-body').innerHTML=`<h2>${old?'编辑实例':'新建运行实例'}</h2><p class="muted">只管理本机进程。留空的认证令牌会保留原值，不会清除。Linux 原生服务通常不能监听小于 1024 的端口；如需使用，请先配置适当权限。</p><div class="form-grid">${field('name','实例名称',v.name,'text','required maxlength="80"')}<label>实例类型<select name="kind" ${old?'disabled':''}><option value="frpc" ${v.kind==='frpc'?'selected':''}>frpc · 客户端</option><option value="frps" ${v.kind==='frps'?'selected':''}>frps · 服务端</option></select></label>${field('host','服务器地址 / 本机监听地址',v.host,'text','required')}${field('port','连接 / 监听端口',v.port,'number','required min="1" max="65535"')}<div class="span-2">${field('token',old&&v.hasToken?'认证令牌（已保存，留空保留）':'认证令牌（至少 8 字节）','','password',old?'autocomplete="new-password"':'required autocomplete="new-password"')}</div><div class="server-field">${field('httpPort','HTTP 虚拟主机端口（0 关闭）',v.httpPort,'number','min="0" max="65535"')}</div><div class="server-field">${field('httpsPort','HTTPS 虚拟主机端口（0 关闭）',v.httpsPort,'number','min="0" max="65535"')}</div></div>`;
 } else {
  const clients=data.state.instances.filter(i=>i.kind==='frpc');if(!clients.length){toast('请先添加一个 frpc 客户端实例');return;}
  const v=old||{instanceId:clients[0].id,type:'tcp',localHost:'127.0.0.1',localPort:8080,remotePort:6000,domains:[],bandwidthKB:0,enabled:true};
  $('#dialog-body').innerHTML=`<h2>${old?'编辑规则':'新建连接规则'}</h2><p class="muted">保存只更新草稿。启停意图在下次启动或重启应用时生效。Linux 原生服务的 Visitor 通常不能监听小于 1024 的端口。</p><div class="form-grid">${field('name','规则名称',v.name,'text','required pattern="[A-Za-z0-9][A-Za-z0-9_.\\-]{0,63}"')}<label>归属实例<select name="instanceId">${clients.map(i=>`<option value="${i.id}" ${v.instanceId===i.id?'selected':''}>${esc(i.name)}</option>`).join('')}</select></label><label>协议<select name="type">${['tcp','udp','http','https','stcp','stcp-visitor'].map(p=>`<option ${p===v.type?'selected':''}>${p}</option>`).join('')}</select></label>${field('bandwidthKB','带宽上限（KB/s，0 不限）',v.bandwidthKB,'number','min="0" max="10000000"')}${field('localHost','本地目标 / Visitor 监听地址',v.localHost,'text','required')}${field('localPort','本地端口',v.localPort,'number','required min="1" max="65535"')}<div class="port-field">${field('remotePort','远端端口',v.remotePort,'number','min="1" max="65535"')}</div><div class="domain-field span-2">${field('domains','域名（多个用逗号分隔）',(v.domains||[]).join(', '))}</div><div class="visitor-field">${field('serverName','服务端 STCP 规则名',v.serverName)}</div><div class="secret-field">${field('secret',old&&v.hasSecret?'配对密钥（留空保留）':'配对密钥（至少 8 字节）','','password','autocomplete="new-password"')}</div><label class="form-check span-2"><input name="enabled" type="checkbox" ${v.enabled?'checked':''}>期望启用此规则</label></div>`;
 }
 updateFields();dialog.showModal();
}
function updateFields(){
 const f=$('#editor');
 if(editing.kind==='instance'){document.querySelectorAll('.server-field').forEach(e=>e.hidden=f.elements.kind.value!=='frps');return;}
 const t=f.elements.type.value;const show={'.port-field':['tcp','udp'].includes(t),'.domain-field':['http','https'].includes(t),'.visitor-field':t==='stcp-visitor','.secret-field':t.startsWith('stcp')};
 for(const [q,v] of Object.entries(show)) document.querySelectorAll(q).forEach(el=>{el.hidden=!v;el.querySelectorAll('input').forEach(input=>input.disabled=!v);});
 f.elements.bandwidthKB.disabled=t==='stcp-visitor';
}
function navigate(to){if(!titles[to])return;page=to;location.hash=to;render();}
function download(name,text,type='application/json'){const u=URL.createObjectURL(new Blob([text],{type}));const a=document.createElement('a');a.href=u;a.download=name;a.click();setTimeout(()=>URL.revokeObjectURL(u),1000);}
$('#login-form').addEventListener('submit',async e=>{e.preventDefault();const b=e.target.querySelector('button');b.disabled=true;try{await api('login',{token:e.target.elements.token.value.trim()});e.target.reset();$('#login-error').textContent='';await refresh();}catch(err){$('#login-error').textContent=err.message;}finally{b.disabled=false;}});
$('#logout').addEventListener('click',async()=>{try{await api('logout',{});showLogin();}catch(e){toast(e.message);}});
$('#refresh').addEventListener('click',()=>refresh());
$('#theme').addEventListener('click',()=>{document.body.classList.toggle('dark');try{localStorage.setItem('kurumi-theme',document.body.classList.contains('dark')?'dark':'light');}catch(_){/* private browsing may deny storage */}});
try{document.body.classList.toggle('dark',localStorage.getItem('kurumi-theme')==='dark');}catch(_){/* theme storage is optional */}
$('#nav').addEventListener('click',e=>{const b=e.target.closest('[data-page]');if(b)navigate(b.dataset.page);});
$('#cancel').addEventListener('click',()=>dialog.close());
$('#editor').addEventListener('change',updateFields);
$('#editor').addEventListener('submit',async e=>{
 e.preventDefault();const f=e.target,b=f.querySelector('button[type="submit"]');b.disabled=true;const val=n=>f.elements[n].value;let value;
 if(editing.kind==='instance') {const kind=val('kind');value={id:editing.id,name:val('name').trim(),kind,host:val('host').trim(),port:+val('port'),token:val('token'),httpPort:kind==='frps'?+val('httpPort'):0,httpsPort:kind==='frps'?+val('httpsPort'):0};}
 else {const type=val('type');value={id:editing.id,name:val('name').trim(),instanceId:val('instanceId'),type,localHost:val('localHost').trim(),localPort:+val('localPort'),remotePort:['tcp','udp'].includes(type)?+val('remotePort'):0,domains:['http','https'].includes(type)?val('domains').split(',').map(v=>v.trim()).filter(Boolean):[],serverName:type==='stcp-visitor'?val('serverName').trim():'',secret:type.startsWith('stcp')?val('secret'):'',bandwidthKB:type==='stcp-visitor'?0:+val('bandwidthKB'),enabled:f.elements.enabled.checked};}
 try{await api('save',{revision:editRev,kind:editing.kind,value});dialog.close();toast('草稿已保存；运行中的实例未改变');await refresh();}catch(err){$('#form-error').textContent=err.message;}finally{b.disabled=false;}
});
$('#content').addEventListener('input',e=>{if(e.target.id==='search'){filter=e.target.value;$('#tunnel-list').innerHTML=filteredTable();}});
$('#content').addEventListener('change',e=>{if(e.target.id==='protocol'){protocol=e.target.value;$('#tunnel-list').innerHTML=filteredTable();}if(e.target.id==='instance-picker'){selected=e.target.value;render();}});
$('#content').addEventListener('click',async e=>{
 const b=e.target.closest('button');if(!b||!data)return;if(b.dataset.go){navigate(b.dataset.go);return;}
 const action=b.dataset.action,id=b.dataset.id;if(!action)return;
 if(action==='new-instance'||action==='edit-instance'){openEditor('instance',id);return;}
 if(action==='new-tunnel'||action==='edit-tunnel'){openEditor('tunnel',id);return;}
 if(action==='logs'){selected=id;navigate('logs');return;}
 b.disabled=true;
 try{
  if(action==='duplicate-instance'){
   const instance=data.state.instances.find(i=>i.id===id);
   if(!instance)throw new Error('源实例已不存在');
   const rules=data.state.tunnels.filter(t=>t.instanceId===id).length;
   const includeTunnels=instance.kind==='frpc'&&rules>0&&confirm('是否同时复制 '+rules+' 条关联规则？\n复制的规则默认停用，以避免端口和域名冲突。');
   if(!confirm('复制「'+instance.name+'」作为独立草稿实例？\n不会启动进程；若为服务端将另选监听端口并关闭 HTTP(S) 监听。'))return;
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
   const copied=await api('duplicate',{revision:data.revision,kind:'tunnel',id,targetInstanceId:target.id});
   toast('已创建停用的规则副本。请检查本地目标及远端端口后再启用');
   await refresh();return;
  }
  if(action==='check-update'){await checkUpdates();return;}
  if(action==='install-update'){const tag=$('#update-version').value;if(!tag)throw new Error('没有可更新的版本');if(!confirm(`更新到 ${tag}？更新将中断工作台和托管连接。`))return;await api('update/install',{tag});updateStatus='正在检查并准备更新';render();setTimeout(pollUpdate,1000);return;}
  if(action==='export-data'){const scope=$('#export-scope').value, revision=$('#export-snapshot').value;if(scope==='snapshot'&&!revision)throw new Error('没有可导出的快照');const query=new URLSearchParams({scope});if(scope==='snapshot')query.set('revision',revision);const result=await api('export?'+query);download(`kurumi-${scope}.json`,JSON.stringify(result,null,2));toast('配置文件已导出，请妥善保管');return;}
  if(action==='import-data'){const file=$('#import-file').files[0];if(!file)throw new Error('请选择配置文件');if(file.size>128*1024*1024)throw new Error('配置文件超过 128 MiB');if(!confirm('导入将替换当前配置和快照；运行中的实例需要重新应用。继续？'))return;let parsed;try{parsed=JSON.parse(await file.text());}catch(_){throw new Error('配置文件不是有效 JSON');}await api('import?revision='+data.revision,parsed);toast('导入成功，界面已刷新');await refresh();return;}
  if(action==='history-settings'){const input=$('#history-limit'), limit=Number(input.value);if(input.value===''||!Number.isInteger(limit)||limit<0||limit>100)throw new Error('快照数量须为 0–100');if(limit<data.history.length&&!confirm(`将删除 ${data.history.length-limit} 份最早快照，继续？`))return;await api('history-settings',{revision:data.revision,historyLimit:limit});toast('快照保留数量已更新');await refresh();return;}
  if(action==='export-toml'){if(!selected)throw new Error('先选择一个实例');const v=await api('config?id='+selected);download('kurumi-redacted.toml',v.toml,'text/plain');return;}
  if(action==='validate'){const v=await api('validate',{revision:data.revision,id});toast('官方 frp 配置校验通过；尚未执行网络探测');return;}
  if(['start','restart','stop'].includes(action)){
   if(action==='restart'&&!confirm('重启应用将中断该实例的现有连接。配置检查失败时不会停止旧进程；新进程启动失败时不会自动回退。继续？'))return;
   if(action==='stop'&&!confirm('停止该实例及其正在转发的连接？'))return;
   await api('control',{revision:data.revision,id,action});toast(action==='stop'?'进程已停止':'启动请求完成，正在刷新真实进程状态');
  }else if(action.startsWith('delete-')){if(!confirm('删除这项草稿配置？历史版本仍保留。'))return;await api('delete',{revision:data.revision,id,kind:action.slice(7)});toast('草稿配置已删除');}
  else if(action==='toggle'){const t=data.state.tunnels.find(t=>t.id===id);await api('save',{revision:data.revision,kind:'tunnel',value:{...t,enabled:!t.enabled}});toast('启停意图已保存，请在实例页应用');}
  else if(action==='restore'){if(!confirm('恢复历史草稿？全部托管实例必须已停止，当前草稿也会保留为历史版本。'))return;await api('restore',{revision:data.revision,target:+b.dataset.target});toast('历史草稿已恢复，实例不会自动启动');}
  await refresh();
 }catch(err){toast(err.message);}finally{b.disabled=false;}
});
window.addEventListener('hashchange',()=>{const p=location.hash.slice(1);if(titles[p]&&page!==p){page=p;render();}});
page=titles[location.hash.slice(1)]?location.hash.slice(1):'overview';
// core.js consumes a launch ticket before the first authenticated state request.
if(!/^#launch=([a-f0-9]{64})$/.test(location.hash))refresh(true);
setInterval(()=>{if(data&&!document.hidden&&!dialog.open)refresh(true);},5000);
