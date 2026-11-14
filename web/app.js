/* KurumiFrp v0.9 WebUI Controller — Pure Vanilla JS, Zero Bloat */
(function () {
  'use strict';

  const state = {
    tab: 'server',
    selectedServerId: '',
    selectedClientId: '',
    selectedLogId: '',
    etag: '',
    data: null,
    logStream: null,
  };

  const TAB_META = {
    server: {
      title: '转发端 Server 与连接看板',
      desc: '管理多套互相独立的官方 frps 服务端配置，并在连接看板中实时掌控每条隧道的流量统计、限流、限速、限时与开关。',
    },
    client: {
      title: '客户端 Client 多配置穿透管理',
      desc: '从零创建与管理多套独立 frpc 客户端配置，支持全类型穿透、P2P 打洞、官方插件与不断流无感热重载。',
    },
    control: {
      title: '应用控制 App Control',
      desc: '在软件目录内自包含选择、导入或导出配置文件，管理唯一管理员用户名密码、查看脱敏日志与核心状态。',
    },
  };

  const $ = (sel, root = document) => root.querySelector(sel);
  const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));

  function esc(val) {
    return String(val ?? '')
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;');
  }

  function formatBytes(bytes) {
    const n = Number(bytes || 0);
    if (n < 1024) return n + ' B';
    if (n < 1024 * 1024) return (n / 1024).toFixed(1) + ' KB';
    if (n < 1024 * 1024 * 1024) return (n / (1024 * 1024)).toFixed(2) + ' MB';
    return (n / (1024 * 1024 * 1024)).toFixed(2) + ' GB';
  }

  function formatRate(bps) {
    const n = Number(bps || 0);
    if (n <= 0) return '0 B/s';
    return formatBytes(n) + '/s';
  }

  function toast(msg) {
    const box = $('#toast-container');
    const el = document.createElement('div');
    el.className = 'toast';
    el.textContent = msg;
    box.appendChild(el);
    setTimeout(() => el.remove(), 3200);
  }

  function downloadBlob(fileName, text, mime = 'text/plain;charset=utf-8') {
    const url = URL.createObjectURL(new Blob([text], { type: mime }));
    const a = document.createElement('a');
    a.href = url;
    a.download = fileName;
    document.body.appendChild(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  }

  async function api(path, opts = {}) {
    const headers = Object.assign({}, opts.headers || {});
    let body = opts.body;
    if (body && typeof body === 'object' && !(body instanceof FormData)) {
      headers['Content-Type'] = 'application/json';
      body = JSON.stringify(body);
    }
    const resp = await fetch(path, Object.assign({}, opts, { headers, body }));
    if (resp.status === 304) return { notModified: true };
    if (resp.status === 401 && path !== '/api/login' && path !== '/api/session') {
      showLoginScreen();
      throw new Error('请先登录');
    }
    const data = await resp.json().catch(() => ({}));
    if (!resp.ok) {
      throw new Error(data.error || `请求失败 (${resp.status})`);
    }
    const etag = resp.headers.get('ETag');
    if (etag) state.etag = etag;
    return data;
  }

  function canSilentRender() {
    const dlg = $('#modal-dialog');
    if (dlg && dlg.open) return false;
    const active = document.activeElement;
    if (active && ['INPUT', 'TEXTAREA', 'SELECT'].includes(active.tagName)) {
      return false;
    }
    const sel = window.getSelection ? window.getSelection() : null;
    if (sel && sel.toString().length > 0) {
      return false;
    }
    return true;
  }

  /* ==================== Skin & Navigation ==================== */
  function initSkin() {
    const saved = localStorage.getItem('kurumi_skin') || 'zafkiel-crimson';
    document.documentElement.setAttribute('data-skin', saved);
    const select = $('#skin-select');
    if (select) {
      select.value = saved;
      select.addEventListener('change', () => {
        document.documentElement.setAttribute('data-skin', select.value);
        localStorage.setItem('kurumi_skin', select.value);
      });
    }
  }

  function switchTab(tab) {
    state.tab = tab;
    $$('.tab-btn').forEach((b) => b.classList.toggle('active', b.dataset.tab === tab));
    $('#panel-server').hidden = tab !== 'server';
    $('#panel-client').hidden = tab !== 'client';
    $('#panel-control').hidden = tab !== 'control';
    const meta = TAB_META[tab] || TAB_META.server;
    $('#hero-heading').textContent = meta.title;
    $('#hero-desc').textContent = meta.desc;
    if (tab === 'control') {
      refreshLogsOnce();
    }
  }

  /* ==================== Auth Flow ==================== */
  function showLoginScreen(sessionInfo) {
    closeLogStream();
    $('#workbench').hidden = true;
    $('#login-screen').hidden = false;
    if (sessionInfo && sessionInfo.defaultUsername) {
      $('#login-username').value = sessionInfo.defaultUsername;
    }
    if (sessionInfo && sessionInfo.needsPasswordSetup) {
      $('#login-hint').textContent = '首次启动尚未设置密码：保持默认用户名 admin、密码留空即可进入并在应用控制中设置密码。';
    } else {
      $('#login-hint').textContent = '请输入唯一管理员用户名与密码进入控制台';
    }
  }

  function showWorkbench() {
    $('#login-screen').hidden = true;
    $('#workbench').hidden = false;
  }

  /* ==================== Main State Refresh & Render ==================== */
  async function loadState(silent = false) {
    const headers = {};
    if (silent && state.etag) {
      headers['If-None-Match'] = state.etag;
    }
    const res = await api('/api/state', { headers });
    if (res.notModified) return;
    state.data = res;
    if (!silent || canSilentRender()) {
      renderAll();
    }
  }

  function renderAll() {
    const d = state.data;
    if (!d) return;
    $('#app-version').textContent = 'v' + (d.version || '0.9.1');
    $('#top-active-config').textContent = d.activeConfigFile || 'default.json';
    $('#top-username').textContent = d.username || 'admin';
    $('#acc-username').value = d.username || 'admin';
    $('#password-setup-alert').hidden = !d.needsPasswordSetup;

    const servers = (d.config && d.config.servers) || [];
    const clients = (d.config && d.config.clients) || [];
    $('#badge-servers').textContent = String(servers.length);
    $('#badge-clients').textContent = String(clients.length);

    if (servers.length > 0 && !servers.some((s) => s.id === state.selectedServerId)) {
      state.selectedServerId = servers[0].id;
    }
    if (clients.length > 0 && !clients.some((c) => c.id === state.selectedClientId)) {
      state.selectedClientId = clients[0].id;
    }

    renderServerSection(servers, d.runtime || {});
    renderClientSection(clients, d.runtime || {});
    renderControlSection(d);
  }

  /* ==================== Section 1: Server (frps) & Connection Dashboard ==================== */
  function renderServerSection(servers, runtimeMap) {
    const pills = $('#server-pills');
    const box = $('#server-workspace');
    if (!servers.length) {
      pills.innerHTML = '';
      box.innerHTML = `
        <div class="card empty-state">
          <img src="/assets/empty-welcome.webp" alt="Empty Server" />
          <h4>尚未创建转发端 (frps) 配置文件</h4>
          <p class="muted-text">点击右上角「+ 新建转发端配置」即可创建独立的服务端配置文件 A / B / C，并启用实时连接看板。</p>
        </div>`;
      return;
    }

    pills.innerHTML = servers
      .map((s) => {
        const rt = runtimeMap[s.id] || { state: 'stopped' };
        const active = s.id === state.selectedServerId ? 'active' : '';
        return `<button type="button" class="pill-btn ${active}" data-select-server="${esc(s.id)}">
          <span class="dot ${esc(rt.state)}"></span>
          <span>${esc(s.name)}</span>
          <small class="muted-text">:${esc(s.bindPort)}</small>
        </button>`;
      })
      .join('');

    const srv = servers.find((s) => s.id === state.selectedServerId) || servers[0];
    const rt = runtimeMap[srv.id] || { state: 'stopped', running: false, connections: [] };
    const conns = rt.connections || [];

    const statusBadge = rt.running
      ? `<span class="badge badge-ok">● 运行中 (PID ${esc(rt.pid)})</span>`
      : rt.state === 'error'
      ? `<span class="badge badge-danger">● 异常退出</span>`
      : `<span class="badge badge-off">○ 已停止</span>`;

    box.innerHTML = `
      <div class="card">
        <div class="card-header">
          <div>
            <h3>${esc(srv.name)} ${statusBadge}</h3>
            <p class="muted-text">独立转发端配置 · 支持总参数编辑与原版 frps 全配置导出</p>
            ${rt.lastError ? `<div class="error-banner" style="margin-top:8px">${esc(rt.lastError)}</div>` : ''}
          </div>
          <div class="btn-row">
            ${
              rt.running
                ? `<button type="button" class="btn btn-secondary btn-sm" data-srv-ctrl="restart" data-id="${esc(srv.id)}">重启服务</button>
                   <button type="button" class="btn btn-danger btn-sm" data-srv-ctrl="stop" data-id="${esc(srv.id)}">停止服务</button>`
                : `<button type="button" class="btn btn-primary btn-sm" data-srv-ctrl="start" data-id="${esc(srv.id)}">▶ 启动转发端</button>`
            }
            <button type="button" class="btn btn-secondary btn-sm" data-srv-edit="${esc(srv.id)}">编辑总参数</button>
            <button type="button" class="btn btn-secondary btn-sm" data-srv-toml="${esc(srv.id)}">预览/导出 TOML</button>
            <button type="button" class="btn btn-ghost btn-sm" data-srv-dup="${esc(srv.id)}">复制独立配置</button>
            <button type="button" class="btn btn-danger btn-sm" data-srv-del="${esc(srv.id)}">删除</button>
          </div>
        </div>

        <div class="summary-grid">
          <div class="stat-tile">
            <div class="label">监听地址与主端口</div>
            <div class="value">${esc(srv.bindAddr || '0.0.0.0')}:${esc(srv.bindPort)}</div>
          </div>
          <div class="stat-tile">
            <div class="label">HTTP / HTTPS 虚拟主机端口</div>
            <div class="value">${srv.vhostHTTPPort || '-'} / ${srv.vhostHTTPSPort || '-'}</div>
          </div>
          <div class="stat-tile">
            <div class="label">KCP / QUIC 端口</div>
            <div class="value">${srv.kcpBindPort || '-'} / ${srv.quicBindPort || '-'}</div>
          </div>
          <div class="stat-tile">
            <div class="label">开放端口范围 / 自启动</div>
            <div class="value">${esc(srv.allowPorts || '全部允许')} · ${srv.autoStart ? '随软件自启' : '手动启动'}</div>
          </div>
        </div>
      </div>

      <div class="card top-gap">
        <div class="card-header">
          <div>
            <h3>连接看板 (Connection Dashboard)</h3>
            <p class="muted-text">针对接入本转发端的每条连接独立管控：流量统计、限流、限速、限时与开关。</p>
          </div>
          <button type="button" class="btn btn-secondary btn-sm" data-add-conn-policy="${esc(srv.id)}">+ 预设连接管控规则</button>
        </div>

        ${
          conns.length === 0
            ? `<div class="empty-state">
                 <img src="/assets/empty-search.webp" alt="No Connections" />
                 <h4>暂无活跃连接或预设管控项</h4>
                 <p class="muted-text">客户端连入后将自动出现在此看板中，也可点击右上角预先为指定代理名称配置限流、限速、限时或开关。</p>
               </div>`
            : `<div class="table-wrap">
                 <table class="data-table">
                   <thead>
                     <tr>
                       <th>连接名称</th>
                       <th>类型 / 状态</th>
                       <th>当前连接数</th>
                       <th>实时速率 (入 / 出)</th>
                       <th>累计流量统计</th>
                       <th>限流上限</th>
                       <th>限速阈值</th>
                       <th>限时截止</th>
                       <th>开关</th>
                       <th>操作</th>
                     </tr>
                   </thead>
                   <tbody>
                     ${conns
                       .map((c) => {
                         const stBadge =
                           c.status === 'online'
                             ? `<span class="badge badge-ok">在线</span>`
                             : c.status === 'blocked'
                             ? `<span class="badge badge-danger">拦截: ${esc(c.BlockReason || c.blockReason || '受限')}</span>`
                             : `<span class="badge badge-off">离线</span>`;
                         const totalBytes = (c.totalInBytes || 0) + (c.totalOutBytes || 0);
                         const expText = c.expiresAt ? new Date(c.expiresAt).toLocaleString() : '不限时';
                         return `<tr>
                           <td><strong>${esc(c.proxyName)}</strong>${c.note ? `<br><small class="muted-text">${esc(c.note)}</small>` : ''}</td>
                           <td><code>${esc(c.proxyType || '-')}</code> ${stBadge}</td>
                           <td>${esc(c.curConns || 0)}</td>
                           <td>↓ ${formatRate(c.speedInBps)}<br>↑ ${formatRate(c.speedOutBps)}</td>
                           <td>共 ${formatBytes(totalBytes)}<br><small class="muted-text">入 ${formatBytes(c.totalInBytes)} / 出 ${formatBytes(c.totalOutBytes)}</small></td>
                           <td>${c.trafficLimitMB > 0 ? esc(c.trafficLimitMB) + ' MB' : '不限'}</td>
                           <td>${c.speedLimitKBps > 0 ? esc(c.speedLimitKBps) + ' KB/s' : '不限'}</td>
                           <td>${esc(expText)}</td>
                           <td>
                             <button type="button" class="switch-btn ${c.enabled ? 'on' : ''}" data-conn-toggle="${esc(srv.id)}" data-proxy="${esc(c.proxyName)}" data-next="${!c.enabled}">
                               ${c.enabled ? '已开启' : '已关闭'}
                             </button>
                           </td>
                           <td>
                             <div class="btn-row">
                               <button type="button" class="btn btn-secondary btn-sm" data-conn-edit="${esc(srv.id)}" data-proxy="${esc(c.proxyName)}">限流/限速/限时</button>
                               <button type="button" class="btn btn-ghost btn-sm" data-conn-del="${esc(srv.id)}" data-proxy="${esc(c.proxyName)}">移除</button>
                             </div>
                           </td>
                         </tr>`;
                       })
                       .join('')}
                   </tbody>
                 </table>
               </div>`
        }
      </div>`;
  }

  /* ==================== Section 2: Client (frpc) & Tunnels ==================== */
  function renderClientSection(clients, runtimeMap) {
    const pills = $('#client-pills');
    const box = $('#client-workspace');
    if (!clients.length) {
      pills.innerHTML = '';
      box.innerHTML = `
        <div class="card empty-state">
          <img src="/assets/empty-welcome.webp" alt="Empty Client" />
          <h4>尚未创建客户端 (frpc) 配置文件</h4>
          <p class="muted-text">点击右上角「+ 新建客户端配置」从头配置你的第一个穿透实例（支持 A / B / C / D 多套独立配置）。</p>
        </div>`;
      return;
    }

    pills.innerHTML = clients
      .map((c) => {
        const rt = runtimeMap[c.id] || { state: 'stopped' };
        const active = c.id === state.selectedClientId ? 'active' : '';
        return `<button type="button" class="pill-btn ${active}" data-select-client="${esc(c.id)}">
          <span class="dot ${esc(rt.state)}"></span>
          <span>${esc(c.name)}</span>
          <small class="muted-text">${(c.tunnels || []).length} 条规则</small>
        </button>`;
      })
      .join('');

    const cli = clients.find((c) => c.id === state.selectedClientId) || clients[0];
    const rt = runtimeMap[cli.id] || { state: 'stopped', running: false, tunnelStats: [] };
    const liveByName = {};
    (rt.tunnelStats || []).forEach((st) => {
      liveByName[st.name] = st;
      if (cli.user && st.name.startsWith(cli.user + '.')) {
        liveByName[st.name.slice(cli.user.length + 1)] = st;
      }
    });

    const statusBadge = rt.running
      ? `<span class="badge badge-ok">● 运行中 (PID ${esc(rt.pid)})</span>`
      : rt.state === 'error'
      ? `<span class="badge badge-danger">● 启动失败</span>`
      : `<span class="badge badge-off">○ 已停止</span>`;

    const tunnels = cli.tunnels || [];

    box.innerHTML = `
      <div class="card">
        <div class="card-header">
          <div>
            <h3>${esc(cli.name)} ${statusBadge}</h3>
            <p class="muted-text">远端服务器：<code>${esc(cli.serverAddr)}:${esc(cli.serverPort)}</code> · 传输协议：<code>${esc((cli.protocol || 'tcp').toUpperCase())}</code> · TLS：${cli.tlsEnable ? '开启' : '关闭'}</p>
            ${rt.lastError ? `<div class="error-banner" style="margin-top:8px">${esc(rt.lastError)}</div>` : ''}
          </div>
          <div class="btn-row">
            ${
              rt.running
                ? `<button type="button" class="btn btn-primary btn-sm" data-cli-ctrl="reload" data-id="${esc(cli.id)}">↻ 热重载配置</button>
                   <button type="button" class="btn btn-danger btn-sm" data-cli-ctrl="stop" data-id="${esc(cli.id)}">停止客户端</button>`
                : `<button type="button" class="btn btn-primary btn-sm" data-cli-ctrl="start" data-id="${esc(cli.id)}">▶ 启动客户端</button>`
            }
            <button type="button" class="btn btn-secondary btn-sm" data-cli-edit="${esc(cli.id)}">服务器与全局参数</button>
            <button type="button" class="btn btn-secondary btn-sm" data-cli-toml="${esc(cli.id)}">预览/导出 TOML</button>
            <button type="button" class="btn btn-ghost btn-sm" data-cli-dup="${esc(cli.id)}">复制独立配置</button>
            <button type="button" class="btn btn-danger btn-sm" data-cli-del="${esc(cli.id)}">删除</button>
          </div>
        </div>
      </div>

      <div class="card top-gap">
        <div class="card-header">
          <div>
            <h3>穿透规则与访问端列表 (${tunnels.length})</h3>
            <p class="muted-text">修改或开关任意规则时，运行中的客户端会自动通过官方 reload 无感热更新，不中断其他正常隧道。</p>
          </div>
          <button type="button" class="btn btn-primary btn-sm" data-tun-add="${esc(cli.id)}">+ 添加穿透规则</button>
        </div>

        ${
          tunnels.length === 0
            ? `<div class="empty-state">
                 <img src="/assets/empty-search.webp" alt="No Tunnels" />
                 <h4>当前客户端配置暂无穿透规则</h4>
                 <p class="muted-text">点击右上角「+ 添加穿透规则」可配置 TCP / UDP / HTTP(S) / STCP / XTCP 打洞或本地静态文件/SOCKS5 插件。</p>
               </div>`
            : `<div class="table-wrap">
                 <table class="data-table">
                   <thead>
                     <tr>
                       <th>规则名称</th>
                       <th>模式 / 协议</th>
                       <th>本地目标 / 插件</th>
                       <th>远端暴露端口 / 域名</th>
                       <th>限速 / 传输</th>
                       <th>实时连接状态</th>
                       <th>开关</th>
                       <th>操作</th>
                     </tr>
                   </thead>
                   <tbody>
                     ${tunnels
                       .map((t) => {
                         const live = liveByName[t.name];
                         let liveHTML = `<span class="badge badge-off">未运行</span>`;
                         if (rt.running) {
                           if (!t.enabled) {
                             liveHTML = `<span class="badge badge-off">已停用</span>`;
                           } else if (live && live.status === 'running') {
                             liveHTML = `<span class="badge badge-ok">已贯通</span>`;
                           } else if (live && live.err) {
                             liveHTML = `<span class="badge badge-danger" title="${esc(live.err)}">失败: ${esc(live.err)}</span>`;
                           } else {
                             liveHTML = `<span class="badge badge-off">连接中...</span>`;
                           }
                         }
                         const localDesc = t.plugin
                           ? `插件 <code>${esc(t.plugin)}</code>`
                           : `${esc(t.localIP || '127.0.0.1')}:${esc(t.localPort || 0)}`;
                         let remoteDesc = '-';
                         if (t.mode === 'visitor') {
                           remoteDesc = `访问目标 <code>${esc(t.serverName)}</code>`;
                         } else if (t.type === 'tcp' || t.type === 'udp') {
                           remoteDesc = `远端端口 <code>:${esc(t.remotePort)}</code>`;
                         } else if ((t.customDomains && t.customDomains.length) || t.subDomain) {
                           remoteDesc = esc([...(t.customDomains || []), t.subDomain ? `*.${t.subDomain}` : ''].filter(Boolean).join(', '));
                         } else if (t.secretKey) {
                           remoteDesc = '密钥私密穿透';
                         }
                         const feat = [
                           t.bandwidthLimitKB > 0 ? `${t.bandwidthLimitKB} KB/s` : '',
                           t.useEncryption ? '加密' : '',
                           t.useCompression ? '压缩' : '',
                         ]
                           .filter(Boolean)
                           .join(' · ') || '默认';

                         return `<tr>
                           <td><strong>${esc(t.name)}</strong></td>
                           <td><code>${esc((t.type || 'tcp').toUpperCase())}</code>${t.mode === 'visitor' ? ' <small>(Visitor)</small>' : ''}</td>
                           <td>${localDesc}</td>
                           <td>${remoteDesc}</td>
                           <td>${esc(feat)}</td>
                           <td>${liveHTML}</td>
                           <td>
                             <button type="button" class="switch-btn ${t.enabled ? 'on' : ''}" data-tun-toggle="${esc(cli.id)}" data-tun-id="${esc(t.id)}" data-next="${!t.enabled}">
                               ${t.enabled ? '启用' : '停用'}
                             </button>
                           </td>
                           <td>
                             <div class="btn-row">
                               <button type="button" class="btn btn-secondary btn-sm" data-tun-edit="${esc(cli.id)}" data-tun-id="${esc(t.id)}">编辑</button>
                               <button type="button" class="btn btn-danger btn-sm" data-tun-del="${esc(cli.id)}" data-tun-id="${esc(t.id)}">删除</button>
                             </div>
                           </td>
                         </tr>`;
                       })
                       .join('')}
                   </tbody>
                 </table>
               </div>`
        }
      </div>`;
  }

  /* ==================== Section 3: App Control ==================== */
  function renderControlSection(d) {
    const files = d.configFiles || [];
    $('#config-files-list').innerHTML = files
      .map(
        (f) => `
        <div class="config-item ${f.active ? 'active' : ''}">
          <div>
            <strong>${esc(f.displayName)}</strong> <code>(${esc(f.fileName)})</code>
            ${f.active ? '<span class="badge badge-ok">当前使用中</span>' : ''}
            <div class="muted-text">转发端 ${f.serverCount} 个 · 客户端 ${f.clientCount} 个 · ${formatBytes(f.sizeBytes)}</div>
          </div>
          <div class="btn-row">
            ${
              f.active
                ? ''
                : `<button type="button" class="btn btn-primary btn-sm" data-cfg-select="${esc(f.fileName)}">切换至此配置</button>
                   <button type="button" class="btn btn-danger btn-sm" data-cfg-del="${esc(f.fileName)}">删除</button>`
            }
          </div>
        </div>`
      )
      .join('');

    const core = d.core || {};
    const srcTag = core.coreSource === 'data-bin' ? ' · 热更新目录' : '';
    $('#core-status-box').innerHTML = `
      <div class="kv-row"><span>内置 frps 服务端核心</span><strong>${core.frpsFound ? `已就绪 (v${esc(core.frpsVersion)}${srcTag})` : '未检测到二进制'}</strong></div>
      <div class="kv-row"><span>内置 frpc 客户端核心</span><strong>${core.frpcFound ? `已就绪 (v${esc(core.frpcVersion)}${srcTag})` : '未检测到二进制'}</strong></div>
      <div class="kv-row"><span>核心检索目录</span><code>${esc(core.bundledDir || './bundled')}</code></div>
    `;
    const proxyInput = $('#update-proxy-input');
    if (proxyInput && document.activeElement !== proxyInput) {
      proxyInput.value = d.updateProxy || '';
    }

    // Populate log instance selector
    const logSel = $('#log-instance-select');
    const items = [
      ...((d.config && d.config.servers) || []).map((s) => ({ id: s.id, label: `[转发端] ${s.name}` })),
      ...((d.config && d.config.clients) || []).map((c) => ({ id: c.id, label: `[客户端] ${c.name}` })),
    ];
    if (items.length && !items.some((x) => x.id === state.selectedLogId)) {
      state.selectedLogId = items[0].id;
    }
    logSel.innerHTML = items.length
      ? items.map((it) => `<option value="${esc(it.id)}" ${it.id === state.selectedLogId ? 'selected' : ''}>${esc(it.label)}</option>`).join('')
      : `<option value="">暂无实例</option>`;
  }

  /* ==================== Logs & SSE ==================== */
  function closeLogStream() {
    if (state.logStream) {
      state.logStream.close();
      state.logStream = null;
    }
  }

  async function refreshLogsOnce() {
    closeLogStream();
    const id = state.selectedLogId;
    const viewer = $('#log-viewer');
    if (!id) {
      viewer.textContent = '暂无可查看日志的配置实例。';
      return;
    }
    try {
      const res = await api(`/api/logs?id=${encodeURIComponent(id)}`);
      const lines = res.lines || [];
      viewer.textContent = lines.length ? lines.join('\n') : '该实例暂未产生运行日志。';
      viewer.scrollTop = viewer.scrollHeight;

      const es = new EventSource(`/api/logs/stream?id=${encodeURIComponent(id)}`);
      let linesArr = lines.slice();
      es.onmessage = (ev) => {
        if (!ev.data) return;
        linesArr.push(ev.data);
        if (linesArr.length > 500) linesArr = linesArr.slice(-500);
        const sel = window.getSelection ? window.getSelection() : null;
        if (!sel || sel.toString().length === 0) {
          viewer.textContent = linesArr.join('\n');
          viewer.scrollTop = viewer.scrollHeight;
        }
      };
      state.logStream = es;
    } catch (e) {
      viewer.textContent = '读取日志失败：' + e.message;
    }
  }

  /* ==================== Modals ==================== */
  function openModal(title, html, onMount) {
    $('#modal-title').textContent = title;
    const body = $('#modal-body');
    body.innerHTML = html;
    const dlg = $('#modal-dialog');
    if (!dlg.open) dlg.showModal();
    if (onMount) onMount(body, () => dlg.close());
  }

  function openServerModal(existing) {
    const s = existing || {
      id: '',
      name: '转发端配置 ' + String.fromCharCode(65 + (((state.data?.config?.servers || []).length) % 26)),
      bindAddr: '0.0.0.0',
      bindPort: 7000,
      vhostHTTPPort: 0,
      vhostHTTPSPort: 0,
      kcpBindPort: 0,
      quicBindPort: 0,
      subDomainHost: '',
      authToken: '',
      allowPorts: '',
      maxPoolCount: 0,
      maxPortsPerClient: 0,
      tlsForce: false,
      autoStart: false,
      extraToml: '',
    };
    openModal(existing ? `编辑转发端总参数 · ${s.name}` : '新建独立转发端 (frps) 配置', `
      <form id="modal-srv-form" class="stack-form">
        <div class="form-row-2">
          <label class="field"><span>配置名称</span><input type="text" name="name" value="${esc(s.name)}" required /></label>
          <label class="field"><span>认证密钥 (auth.token)</span><input type="text" name="authToken" value="${esc(s.authToken || '')}" placeholder="建议设置高强度连接密钥" /></label>
        </div>
        <div class="form-row-3">
          <label class="field"><span>绑定地址 (bindAddr)</span><input type="text" name="bindAddr" value="${esc(s.bindAddr || '0.0.0.0')}" required /></label>
          <label class="field"><span>服务端口 (bindPort)</span><input type="number" name="bindPort" value="${esc(s.bindPort || 7000)}" min="1" max="65535" required /></label>
          <label class="field"><span>开放端口范围 (allowPorts)</span><input type="text" name="allowPorts" value="${esc(s.allowPorts || '')}" placeholder="如 10000-20000,3389" /></label>
        </div>
        <div class="form-row-2">
          <label class="field"><span>HTTP 虚拟主机端口 (可选)</span><input type="number" name="vhostHTTPPort" value="${esc(s.vhostHTTPPort || 0)}" min="0" max="65535" /></label>
          <label class="field"><span>HTTPS 虚拟主机端口 (可选)</span><input type="number" name="vhostHTTPSPort" value="${esc(s.vhostHTTPSPort || 0)}" min="0" max="65535" /></label>
        </div>
        <div class="form-row-3">
          <label class="field"><span>KCP 绑定端口 (可选)</span><input type="number" name="kcpBindPort" value="${esc(s.kcpBindPort || 0)}" min="0" max="65535" /></label>
          <label class="field"><span>QUIC 绑定端口 (可选)</span><input type="number" name="quicBindPort" value="${esc(s.quicBindPort || 0)}" min="0" max="65535" /></label>
          <label class="field"><span>子域名后缀 (subDomainHost)</span><input type="text" name="subDomainHost" value="${esc(s.subDomainHost || '')}" placeholder="如 frp.example.com" /></label>
        </div>
        <div class="form-row-2">
          <label class="field"><span>最大连接池 (maxPoolCount)</span><input type="number" name="maxPoolCount" value="${esc(s.maxPoolCount || 0)}" min="0" /></label>
          <label class="field"><span>单客户端最大端口数</span><input type="number" name="maxPortsPerClient" value="${esc(s.maxPortsPerClient || 0)}" min="0" /></label>
        </div>
        <div class="btn-row">
          <label class="check-inline"><input type="checkbox" name="tlsForce" ${s.tlsForce ? 'checked' : ''} /> 强制要求客户端启用 TLS 加密</label>
          <label class="check-inline"><input type="checkbox" name="autoStart" ${s.autoStart ? 'checked' : ''} /> 随 KurumiFrp 启动时自动运行</label>
        </div>
        <label class="field">
          <span>附加官方全参数 TOML（可选，原样追加至 frps.toml）</span>
          <textarea name="extraToml" class="mono-input" placeholder="# 可填写任意官方 frps 高级配置项">${esc(s.extraToml || '')}</textarea>
        </label>
        <div class="btn-row">
          <button type="submit" class="btn btn-primary">保存配置</button>
        </div>
      </form>
    `, (root, close) => {
      $('#modal-srv-form', root).addEventListener('submit', async (ev) => {
        ev.preventDefault();
        const fd = new FormData(ev.target);
        const payload = {
          id: s.id,
          name: fd.get('name'),
          bindAddr: fd.get('bindAddr'),
          bindPort: Number(fd.get('bindPort') || 7000),
          vhostHTTPPort: Number(fd.get('vhostHTTPPort') || 0),
          vhostHTTPSPort: Number(fd.get('vhostHTTPSPort') || 0),
          kcpBindPort: Number(fd.get('kcpBindPort') || 0),
          quicBindPort: Number(fd.get('quicBindPort') || 0),
          subDomainHost: fd.get('subDomainHost'),
          authToken: fd.get('authToken'),
          allowPorts: fd.get('allowPorts'),
          maxPoolCount: Number(fd.get('maxPoolCount') || 0),
          maxPortsPerClient: Number(fd.get('maxPortsPerClient') || 0),
          tlsForce: fd.get('tlsForce') === 'on',
          autoStart: fd.get('autoStart') === 'on',
          extraToml: fd.get('extraToml'),
        };
        const res = await api('/api/servers/save', { method: 'POST', body: payload });
        state.selectedServerId = res.id || s.id;
        close();
        toast('转发端配置已保存');
        await loadState();
      });
    });
  }

  function openConnPolicyModal(serverId, existingConn) {
    const c = existingConn || {
      proxyName: '',
      enabled: true,
      trafficLimitMB: 0,
      speedLimitKBps: 0,
      expiresAt: '',
      note: '',
    };
    const localDT = c.expiresAt ? new Date(c.expiresAt).toISOString().slice(0, 16) : '';
    openModal(c.proxyName ? `连接看板管控 · ${c.proxyName}` : '预设连接看板管控规则', `
      <form id="modal-conn-form" class="stack-form">
        <div class="form-row-2">
          <label class="field">
            <span>连接代理名称 (proxyName)</span>
            <input type="text" name="proxyName" value="${esc(c.proxyName)}" ${c.proxyName ? 'readonly' : 'required'} placeholder="如 ssh 或 user.nas" />
          </label>
          <label class="field">
            <span>备注说明</span>
            <input type="text" name="note" value="${esc(c.note || '')}" placeholder="如：朋友联机临时通道" />
          </label>
        </div>
        <div class="form-row-2">
          <label class="field">
            <span>限流上限 (MB，0 表示不限流)</span>
            <input type="number" name="trafficLimitMB" value="${esc(c.trafficLimitMB || 0)}" min="0" />
          </label>
          <label class="field">
            <span>限速阈值 (KB/s，0 表示不限速)</span>
            <input type="number" name="speedLimitKBps" value="${esc(c.speedLimitKBps || 0)}" min="0" />
          </label>
        </div>
        <label class="field">
          <span>限时截止时间（留空表示长期有效）</span>
          <input type="datetime-local" id="conn-exp-input" name="expiresLocal" value="${esc(localDT)}" />
        </label>
        <div class="btn-row">
          <button type="button" class="btn btn-ghost btn-sm" data-quick-hours="1">+1 小时</button>
          <button type="button" class="btn btn-ghost btn-sm" data-quick-hours="24">+24 小时</button>
          <button type="button" class="btn btn-ghost btn-sm" data-quick-hours="168">+7 天</button>
          <button type="button" class="btn btn-ghost btn-sm" data-quick-hours="0">清除限时</button>
        </div>
        <div class="btn-row">
          <label class="check-inline"><input type="checkbox" name="enabled" ${c.enabled !== false ? 'checked' : ''} /> 允许该连接通过 (开关开启)</label>
          <label class="check-inline"><input type="checkbox" name="resetTraffic" /> 同时清零累计流量基线</label>
        </div>
        <div class="btn-row">
          <button type="submit" class="btn btn-primary">应用连接看板规则</button>
        </div>
      </form>
    `, (root, close) => {
      $$('[data-quick-hours]', root).forEach((btn) => {
        btn.addEventListener('click', () => {
          const hrs = Number(btn.dataset.quickHours);
          const inp = $('#conn-exp-input', root);
          if (hrs <= 0) {
            inp.value = '';
          } else {
            const d = new Date(Date.now() + hrs * 3600 * 1000 - new Date().getTimezoneOffset() * 60000);
            inp.value = d.toISOString().slice(0, 16);
          }
        });
      });

      $('#modal-conn-form', root).addEventListener('submit', async (ev) => {
        ev.preventDefault();
        const fd = new FormData(ev.target);
        const expLocal = fd.get('expiresLocal');
        const expiresAt = expLocal ? new Date(expLocal).toISOString() : '';
        await api('/api/servers/conn-policy', {
          method: 'POST',
          body: {
            serverId,
            proxyName: fd.get('proxyName'),
            enabled: fd.get('enabled') === 'on',
            trafficLimitMB: Number(fd.get('trafficLimitMB') || 0),
            speedLimitKBps: Number(fd.get('speedLimitKBps') || 0),
            expiresAt,
            note: fd.get('note'),
            resetTraffic: fd.get('resetTraffic') === 'on',
          },
        });
        close();
        toast('连接看板规则已生效');
        await loadState();
      });
    });
  }

  function openClientModal(existing) {
    const c = existing || {
      id: '',
      name: '客户端配置 ' + String.fromCharCode(65 + (((state.data?.config?.clients || []).length) % 26)),
      serverAddr: '',
      serverPort: 7000,
      user: '',
      authToken: '',
      protocol: 'tcp',
      poolCount: 1,
      tlsEnable: true,
      tlsServerName: '',
      natHoleSTUNServer: '',
      autoStart: false,
      loginFailExit: false,
      extraToml: '',
    };
    openModal(existing ? `编辑客户端参数 · ${c.name}` : '新建独立客户端 (frpc) 配置', `
      <form id="modal-cli-form" class="stack-form">
        <div class="form-row-2">
          <label class="field"><span>客户端配置名称</span><input type="text" name="name" value="${esc(c.name)}" required /></label>
          <label class="field"><span>用户前缀 (user，可选)</span><input type="text" name="user" value="${esc(c.user || '')}" placeholder="避免多人共用服务端时规则重名" /></label>
        </div>
        <div class="form-row-2">
          <label class="field"><span>远端 frps 服务器地址</span><input type="text" name="serverAddr" value="${esc(c.serverAddr)}" placeholder="公网 IP 或域名" required /></label>
          <label class="field"><span>远端服务器端口 (serverPort)</span><input type="number" name="serverPort" value="${esc(c.serverPort || 7000)}" min="1" max="65535" required /></label>
        </div>
        <div class="form-row-3">
          <label class="field"><span>认证密钥 (auth.token)</span><input type="text" name="authToken" value="${esc(c.authToken || '')}" /></label>
          <label class="field">
            <span>底层传输协议</span>
            <select name="protocol">
              ${['tcp', 'quic', 'kcp', 'websocket', 'wss']
                .map((p) => `<option value="${p}" ${(c.protocol || 'tcp') === p ? 'selected' : ''}>${p.toUpperCase()}</option>`)
                .join('')}
            </select>
          </label>
          <label class="field"><span>连接池数量 (poolCount)</span><input type="number" name="poolCount" value="${esc(c.poolCount || 1)}" min="0" max="64" /></label>
        </div>
        <div class="form-row-2">
          <label class="field"><span>TLS ServerName (可选)</span><input type="text" name="tlsServerName" value="${esc(c.tlsServerName || '')}" /></label>
          <label class="field"><span>P2P 打洞 STUN 服务 (可选)</span><input type="text" name="natHoleSTUNServer" value="${esc(c.natHoleSTUNServer || '')}" placeholder="默认使用官方内置 STUN" /></label>
        </div>
        <div class="btn-row">
          <label class="check-inline"><input type="checkbox" name="tlsEnable" ${c.tlsEnable ? 'checked' : ''} /> 启用传输层 TLS 加密</label>
          <label class="check-inline"><input type="checkbox" name="autoStart" ${c.autoStart ? 'checked' : ''} /> 随 KurumiFrp 自动启动</label>
        </div>
        <label class="field">
          <span>附加官方全参数 TOML（可选，原样写入 frpc.toml 顶部）</span>
          <textarea name="extraToml" class="mono-input" placeholder="# 可自由附加任意官方 frpc 全局参数">${esc(c.extraToml || '')}</textarea>
        </label>
        <div class="btn-row">
          <button type="submit" class="btn btn-primary">保存客户端配置</button>
        </div>
      </form>
    `, (root, close) => {
      $('#modal-cli-form', root).addEventListener('submit', async (ev) => {
        ev.preventDefault();
        const fd = new FormData(ev.target);
        const res = await api('/api/clients/save', {
          method: 'POST',
          body: {
            id: c.id,
            name: fd.get('name'),
            serverAddr: fd.get('serverAddr'),
            serverPort: Number(fd.get('serverPort') || 7000),
            user: fd.get('user'),
            authToken: fd.get('authToken'),
            protocol: fd.get('protocol'),
            poolCount: Number(fd.get('poolCount') || 0),
            tlsEnable: fd.get('tlsEnable') === 'on',
            tlsServerName: fd.get('tlsServerName'),
            natHoleSTUNServer: fd.get('natHoleSTUNServer'),
            autoStart: fd.get('autoStart') === 'on',
            loginFailExit: false,
            extraToml: fd.get('extraToml'),
            tunnels: c.tunnels,
          },
        });
        state.selectedClientId = res.id || c.id;
        close();
        toast('客户端配置已保存');
        await loadState();
      });
    });
  }

  function openTunnelModal(clientId, existingTun) {
    const t = existingTun || {
      id: '',
      name: '',
      mode: 'proxy',
      type: 'tcp',
      enabled: true,
      localIP: '127.0.0.1',
      localPort: 8080,
      remotePort: 18080,
      customDomains: [],
      subDomain: '',
      secretKey: '',
      serverName: '',
      bandwidthLimitKB: 0,
      useEncryption: false,
      useCompression: true,
      plugin: '',
      pluginLocalPath: '',
      pluginUser: '',
      pluginPassword: '',
      extraToml: '',
    };
    openModal(existingTun ? `编辑穿透规则 · ${t.name}` : '添加穿透规则 / P2P 访问端', `
      <form id="modal-tun-form" class="stack-form">
        <div class="form-row-3">
          <label class="field"><span>规则名称</span><input type="text" name="name" value="${esc(t.name)}" placeholder="如 rdp-home 或 web-blog" required /></label>
          <label class="field">
            <span>工作模式</span>
            <select name="mode" id="tun-mode">
              <option value="proxy" ${(t.mode || 'proxy') === 'proxy' ? 'selected' : ''}>常规暴露端 (Proxy)</option>
              <option value="visitor" ${t.mode === 'visitor' ? 'selected' : ''}>私密/P2P 访问端 (Visitor)</option>
            </select>
          </label>
          <label class="field">
            <span>穿透协议类型</span>
            <select name="type" id="tun-type">
              ${['tcp', 'udp', 'http', 'https', 'stcp', 'xtcp', 'sudp', 'tcpmux']
                .map((tp) => `<option value="${tp}" ${(t.type || 'tcp') === tp ? 'selected' : ''}>${tp.toUpperCase()}</option>`)
                .join('')}
            </select>
          </label>
        </div>

        <div class="form-row-3">
          <label class="field"><span>本地地址 / Visitor 绑定地址</span><input type="text" name="localIP" value="${esc(t.localIP || '127.0.0.1')}" /></label>
          <label class="field"><span>本地端口 / Visitor 监听端口</span><input type="number" name="localPort" value="${esc(t.localPort || 0)}" min="0" max="65535" /></label>
          <label class="field"><span>远端端口 (TCP/UDP 必填)</span><input type="number" name="remotePort" value="${esc(t.remotePort || 0)}" min="0" max="65535" /></label>
        </div>

        <div class="form-row-2">
          <label class="field"><span>自定义域名 (HTTP/HTTPS，逗号分隔)</span><input type="text" name="customDomains" value="${esc((t.customDomains || []).join(', '))}" placeholder="blog.example.com" /></label>
          <label class="field"><span>子域名前缀 (subdomain)</span><input type="text" name="subDomain" value="${esc(t.subDomain || '')}" placeholder="nas" /></label>
        </div>

        <div class="form-row-2">
          <label class="field"><span>私密/P2P 密钥 (STCP/XTCP/SUDP secretKey)</span><input type="text" name="secretKey" value="${esc(t.secretKey || '')}" /></label>
          <label class="field"><span>目标服务名 (仅 Visitor 模式填写 serverName)</span><input type="text" name="serverName" value="${esc(t.serverName || '')}" placeholder="对端暴露的规则名" /></label>
        </div>

        <div class="form-row-3">
          <label class="field">
            <span>官方客户端插件 (可选)</span>
            <select name="plugin">
              <option value="" ${!t.plugin ? 'selected' : ''}>不使用插件 (转发本地端口)</option>
              <option value="static_file" ${t.plugin === 'static_file' ? 'selected' : ''}>static_file (本地静态文件共享)</option>
              <option value="socks5" ${t.plugin === 'socks5' ? 'selected' : ''}>socks5 (SOCKS5 代理跳板)</option>
              <option value="http_proxy" ${t.plugin === 'http_proxy' ? 'selected' : ''}>http_proxy (HTTP 代理)</option>
              <option value="unix_domain_socket" ${t.plugin === 'unix_domain_socket' ? 'selected' : ''}>unix_domain_socket</option>
              <option value="https2http" ${t.plugin === 'https2http' ? 'selected' : ''}>https2http</option>
            </select>
          </label>
          <label class="field"><span>插件路径 / 目标地址 (可选)</span><input type="text" name="pluginLocalPath" value="${esc(t.pluginLocalPath || t.pluginLocalAddr || '')}" placeholder="如 D:/Share 或 /var/run/docker.sock" /></label>
          <label class="field"><span>单隧道限速 (KB/s，0 不限)</span><input type="number" name="bandwidthLimitKB" value="${esc(t.bandwidthLimitKB || 0)}" min="0" /></label>
        </div>

        <div class="form-row-2">
          <label class="field"><span>插件或 HTTP 认证用户名 (可选)</span><input type="text" name="pluginUser" value="${esc(t.pluginUser || t.httpUser || '')}" /></label>
          <label class="field"><span>插件或 HTTP 认证密码 (可选)</span><input type="text" name="pluginPassword" value="${esc(t.pluginPassword || t.httpPassword || '')}" /></label>
        </div>

        <div class="btn-row">
          <label class="check-inline"><input type="checkbox" name="enabled" ${t.enabled !== false ? 'checked' : ''} /> 立即启用该规则</label>
          <label class="check-inline"><input type="checkbox" name="useEncryption" ${t.useEncryption ? 'checked' : ''} /> 流量加密</label>
          <label class="check-inline"><input type="checkbox" name="useCompression" ${t.useCompression ? 'checked' : ''} /> 流量压缩</label>
          <label class="check-inline"><input type="checkbox" name="keepTunnelOpen" ${t.keepTunnelOpen ? 'checked' : ''} /> XTCP 保持打洞常驻</label>
        </div>

        <label class="field">
          <span>单规则附加 TOML 参数（可选）</span>
          <textarea name="extraToml" class="mono-input" placeholder="# 如 hostHeaderRewrite = &quot;127.0.0.1&quot;">${esc(t.extraToml || '')}</textarea>
        </label>

        <div class="btn-row">
          <button type="submit" class="btn btn-primary">保存规则</button>
        </div>
      </form>
    `, (root, close) => {
      $('#modal-tun-form', root).addEventListener('submit', async (ev) => {
        ev.preventDefault();
        const fd = new FormData(ev.target);
        const plug = fd.get('plugin');
        const plugPath = fd.get('pluginLocalPath');
        const domains = String(fd.get('customDomains') || '')
          .split(',')
          .map((x) => x.trim())
          .filter(Boolean);

        const tunnel = {
          id: t.id,
          name: fd.get('name'),
          mode: fd.get('mode'),
          type: fd.get('type'),
          enabled: fd.get('enabled') === 'on',
          localIP: fd.get('localIP'),
          localPort: Number(fd.get('localPort') || 0),
          remotePort: Number(fd.get('remotePort') || 0),
          customDomains: domains,
          subDomain: fd.get('subDomain'),
          secretKey: fd.get('secretKey'),
          serverName: fd.get('serverName'),
          bandwidthLimitKB: Number(fd.get('bandwidthLimitKB') || 0),
          useEncryption: fd.get('useEncryption') === 'on',
          useCompression: fd.get('useCompression') === 'on',
          keepTunnelOpen: fd.get('keepTunnelOpen') === 'on',
          plugin: plug,
          pluginLocalPath: plug === 'https2http' ? '' : plugPath,
          pluginLocalAddr: plug === 'https2http' ? plugPath : '',
          pluginUser: plug ? fd.get('pluginUser') : '',
          pluginPassword: plug ? fd.get('pluginPassword') : '',
          httpUser: !plug ? fd.get('pluginUser') : '',
          httpPassword: !plug ? fd.get('pluginPassword') : '',
          extraToml: fd.get('extraToml'),
        };

        await api('/api/clients/tunnel-save', {
          method: 'POST',
          body: { clientId, tunnel },
        });
        close();
        toast('隧道规则已保存');
        await loadState();
      });
    });
  }

  async function openTOMLPreviewModal(kind, id) {
    const endpoint = kind === 'frps' ? `/api/servers/toml?id=${encodeURIComponent(id)}` : `/api/clients/toml?id=${encodeURIComponent(id)}`;
    const res = await api(endpoint);
    const fileName = `${res.name || kind}-${kind}.toml`;
    openModal(`标准官方 ${kind}.toml 预览 · ${res.name}`, `
      <div class="stack-form">
        <pre class="log-viewer">${esc(res.toml)}</pre>
        <div class="btn-row">
          <button type="button" id="btn-dl-toml" class="btn btn-primary btn-sm">下载 ${esc(fileName)}</button>
          <button type="button" id="btn-cp-toml" class="btn btn-secondary btn-sm">复制内容</button>
        </div>
      </div>
    `, (root) => {
      $('#btn-dl-toml', root).addEventListener('click', () => downloadBlob(fileName, res.toml));
      $('#btn-cp-toml', root).addEventListener('click', async () => {
        await navigator.clipboard.writeText(res.toml);
        toast('TOML 配置已复制到剪贴板');
      });
    });
  }

  /* ==================== Event Bindings ==================== */
  function bindEvents() {
    $('#modal-close').addEventListener('click', () => $('#modal-dialog').close());

    $$('.tab-btn').forEach((btn) => {
      btn.addEventListener('click', () => switchTab(btn.dataset.tab));
    });

    $('#btn-account-quick').addEventListener('click', () => {
      switchTab('control');
      $('#acc-new-pass').focus();
    });

    $('#btn-setup-now').addEventListener('click', () => {
      switchTab('control');
      $('#acc-new-pass').focus();
    });

    $('#login-form').addEventListener('submit', async (ev) => {
      ev.preventDefault();
      const errBox = $('#login-error');
      errBox.hidden = true;
      try {
        await api('/api/login', {
          method: 'POST',
          body: {
            username: $('#login-username').value,
            password: $('#login-password').value,
          },
        });
        $('#login-password').value = '';
        showWorkbench();
        await loadState();
      } catch (e) {
        errBox.textContent = e.message;
        errBox.hidden = false;
      }
    });

    $('#btn-logout').addEventListener('click', async () => {
      await api('/api/logout', { method: 'POST' }).catch(() => {});
      const sess = await api('/api/session').catch(() => ({}));
      showLoginScreen(sess);
    });

    $('#btn-new-server').addEventListener('click', () => openServerModal(null));
    $('#btn-new-client').addEventListener('click', () => openClientModal(null));

    // Delegated clicks inside main container
    document.body.addEventListener('click', async (ev) => {
      const btn = ev.target.closest('button');
      if (!btn) return;
      try {
        if (btn.dataset.selectServer) {
          state.selectedServerId = btn.dataset.selectServer;
          renderAll();
        } else if (btn.dataset.srvCtrl) {
          await api('/api/servers/control', { method: 'POST', body: { id: btn.dataset.id, action: btn.dataset.srvCtrl } });
          toast('转发端指令已执行');
          await loadState();
        } else if (btn.dataset.srvEdit) {
          const srv = (state.data?.config?.servers || []).find((x) => x.id === btn.dataset.srvEdit);
          if (srv) openServerModal(srv);
        } else if (btn.dataset.srvToml) {
          await openTOMLPreviewModal('frps', btn.dataset.srvToml);
        } else if (btn.dataset.srvDup) {
          const res = await api('/api/servers/duplicate', { method: 'POST', body: { id: btn.dataset.srvDup } });
          state.selectedServerId = res.id;
          toast('已复制为独立转发端配置');
          await loadState();
        } else if (btn.dataset.srvDel) {
          if (!confirm('确认删除该转发端配置吗？')) return;
          await api('/api/servers/delete', { method: 'POST', body: { id: btn.dataset.srvDel } });
          toast('转发端配置已删除');
          await loadState();
        } else if (btn.dataset.addConnPolicy) {
          openConnPolicyModal(btn.dataset.addConnPolicy, null);
        } else if (btn.dataset.connToggle) {
          const srvId = btn.dataset.connToggle;
          const pName = btn.dataset.proxy;
          const rt = state.data?.runtime?.[srvId];
          const cur = (rt?.connections || []).find((x) => x.proxyName === pName) || {};
          await api('/api/servers/conn-policy', {
            method: 'POST',
            body: {
              serverId: srvId,
              proxyName: pName,
              enabled: btn.dataset.next === 'true',
              trafficLimitMB: cur.trafficLimitMB || 0,
              speedLimitKBps: cur.speedLimitKBps || 0,
              expiresAt: cur.expiresAt || '',
              note: cur.note || '',
            },
          });
          toast('连接开关状态已更新');
          await loadState();
        } else if (btn.dataset.connEdit) {
          const srvId = btn.dataset.connEdit;
          const pName = btn.dataset.proxy;
          const rt = state.data?.runtime?.[srvId];
          const cur = (rt?.connections || []).find((x) => x.proxyName === pName);
          openConnPolicyModal(srvId, cur);
        } else if (btn.dataset.connDel) {
          await api('/api/servers/conn-delete', {
            method: 'POST',
            body: { serverId: btn.dataset.connDel, proxyName: btn.dataset.proxy },
          });
          toast('连接规则已移除');
          await loadState();
        } else if (btn.dataset.selectClient) {
          state.selectedClientId = btn.dataset.selectClient;
          renderAll();
        } else if (btn.dataset.cliCtrl) {
          await api('/api/clients/control', { method: 'POST', body: { id: btn.dataset.id, action: btn.dataset.cliCtrl } });
          toast('客户端指令已执行');
          await loadState();
        } else if (btn.dataset.cliEdit) {
          const cli = (state.data?.config?.clients || []).find((x) => x.id === btn.dataset.cliEdit);
          if (cli) openClientModal(cli);
        } else if (btn.dataset.cliToml) {
          await openTOMLPreviewModal('frpc', btn.dataset.cliToml);
        } else if (btn.dataset.cliDup) {
          const res = await api('/api/clients/duplicate', { method: 'POST', body: { id: btn.dataset.cliDup } });
          state.selectedClientId = res.id;
          toast('已复制为独立客户端配置');
          await loadState();
        } else if (btn.dataset.cliDel) {
          if (!confirm('确认删除该客户端配置及其下属规则吗？')) return;
          await api('/api/clients/delete', { method: 'POST', body: { id: btn.dataset.cliDel } });
          toast('客户端配置已删除');
          await loadState();
        } else if (btn.dataset.tunAdd) {
          openTunnelModal(btn.dataset.tunAdd, null);
        } else if (btn.dataset.tunToggle) {
          await api('/api/clients/tunnel-toggle', {
            method: 'POST',
            body: {
              clientId: btn.dataset.tunToggle,
              tunnelId: btn.dataset.tunId,
              enabled: btn.dataset.next === 'true',
            },
          });
          await loadState();
        } else if (btn.dataset.tunEdit) {
          const cli = (state.data?.config?.clients || []).find((x) => x.id === btn.dataset.tunEdit);
          const tun = (cli?.tunnels || []).find((x) => x.id === btn.dataset.tunId);
          if (tun) openTunnelModal(cli.id, tun);
        } else if (btn.dataset.tunDel) {
          if (!confirm('确认删除这条穿透规则吗？')) return;
          await api('/api/clients/tunnel-delete', {
            method: 'POST',
            body: { clientId: btn.dataset.tunDel, tunnelId: btn.dataset.tunId },
          });
          toast('穿透规则已删除');
          await loadState();
        } else if (btn.dataset.cfgSelect) {
          await api('/api/configs/select', { method: 'POST', body: { fileName: btn.dataset.cfgSelect } });
          toast('已切换软件内活动配置文件');
          await loadState();
        } else if (btn.dataset.cfgDel) {
          if (!confirm(`确认从软件目录删除配置文件 ${btn.dataset.cfgDel} 吗？`)) return;
          await api('/api/configs/delete', { method: 'POST', body: { fileName: btn.dataset.cfgDel } });
          toast('配置文件已删除');
          await loadState();
        }
      } catch (e) {
        toast('操作失败：' + e.message);
      }
    });

    // App Control actions
    $('#btn-new-config-file').addEventListener('click', () => {
      openModal('新建软件内独立配置文件', `
        <form id="modal-new-cfg" class="stack-form">
          <label class="field"><span>文件标识名 (保存于 data/configs/)</span><input type="text" name="fileName" placeholder="如 home-server 或 gaming" required /></label>
          <label class="field"><span>显示名称</span><input type="text" name="displayName" placeholder="如 家里服务器方案" required /></label>
          <div class="btn-row"><button type="submit" class="btn btn-primary">创建并切换</button></div>
        </form>
      `, (root, close) => {
        $('#modal-new-cfg', root).addEventListener('submit', async (ev) => {
          ev.preventDefault();
          const fd = new FormData(ev.target);
          await api('/api/configs/select', {
            method: 'POST',
            body: { fileName: fd.get('fileName'), displayName: fd.get('displayName') },
          });
          close();
          toast('已创建并切换至新配置文件');
          await loadState();
        });
      });
    });

    $('#btn-export-json').addEventListener('click', () => {
      window.location.href = '/api/configs/export-json';
    });

    $('#input-import-json').addEventListener('change', async (ev) => {
      const file = ev.target.files && ev.target.files[0];
      if (!file) return;
      try {
        const text = await file.text();
        const parsed = JSON.parse(text);
        await api('/api/configs/import-json', {
          method: 'POST',
          body: { fileName: file.name, content: parsed, activate: true },
        });
        toast('配置文件已导入并激活');
        await loadState();
      } catch (e) {
        toast('导入 JSON 失败：' + e.message);
      } finally {
        ev.target.value = '';
      }
    });

    $('#btn-open-import-toml').addEventListener('click', () => {
      openModal('导入官方标准 frps.toml / frpc.toml', `
        <form id="modal-imp-toml" class="stack-form">
          <div class="form-row-2">
            <label class="field"><span>导入后配置名称</span><input type="text" name="name" placeholder="如 阿里云转发端" required /></label>
            <label class="field">
              <span>配置类型</span>
              <select name="kind">
                <option value="">自动识别 (frpc / frps)</option>
                <option value="frpc">客户端 (frpc.toml)</option>
                <option value="frps">转发端 (frps.toml)</option>
              </select>
            </label>
          </div>
          <label class="field">
            <span>粘贴或选择本地 .toml 内容（未映射的高级字段将自动完整保留在附加参数中）</span>
            <textarea name="toml" id="imp-toml-text" class="mono-input" rows="8" required placeholder="serverAddr = &quot;1.2.3.4&quot;&#10;serverPort = 7000"></textarea>
          </label>
          <div class="btn-row">
            <label class="btn btn-secondary btn-sm file-btn">读取本地 .toml 文件<input type="file" id="pick-toml-file" accept=".toml,.ini,.txt" hidden /></label>
            <button type="submit" class="btn btn-primary">解析并导入</button>
          </div>
        </form>
      `, (root, close) => {
        $('#pick-toml-file', root).addEventListener('change', async (e) => {
          const f = e.target.files && e.target.files[0];
          if (f) $('#imp-toml-text', root).value = await f.text();
        });
        $('#modal-imp-toml', root).addEventListener('submit', async (ev) => {
          ev.preventDefault();
          const fd = new FormData(ev.target);
          const res = await api('/api/configs/import-toml', {
            method: 'POST',
            body: { name: fd.get('name'), kind: fd.get('kind'), toml: fd.get('toml') },
          });
          close();
          toast('TOML 配置已导入');
          await loadState();
          switchTab(res.kind === 'frps' ? 'server' : 'client');
        });
      });
    });

    $('#account-form').addEventListener('submit', async (ev) => {
      ev.preventDefault();
      try {
        await api('/api/account', {
          method: 'POST',
          body: {
            currentPassword: $('#acc-current-pass').value,
            newUsername: $('#acc-username').value,
            newPassword: $('#acc-new-pass').value,
          },
        });
        $('#acc-current-pass').value = '';
        $('#acc-new-pass').value = '';
        toast('唯一管理员用户名与密码已更新');
        await loadState();
      } catch (e) {
        toast('修改失败：' + e.message);
      }
    });

    $('#log-instance-select').addEventListener('change', (ev) => {
      state.selectedLogId = ev.target.value;
      refreshLogsOnce();
    });

    $('#btn-refresh-logs').addEventListener('click', () => refreshLogsOnce());
    $('#btn-download-logs').addEventListener('click', () => {
      const txt = $('#log-viewer').textContent || '';
      downloadBlob(`kurumifrp-${state.selectedLogId || 'logs'}.txt`, txt);
    });

    function renderUpdateCheckPanel(res) {
      const box = $('#update-result-box');
      box.hidden = false;
      const appSt = res.app || {};
      const frpSt = res.frp || {};

      let appDesc = '';
      let appBtns = '';
      if (appSt.error) {
        appDesc = `<strong>KurumiFrp 本体</strong>：${esc(appSt.error)}`;
        appBtns = `<a href="${esc(appSt.releaseURL || 'https://github.com/Presikid/KurumiFrp/releases')}" target="_blank" rel="noopener" class="btn btn-secondary btn-sm">打开发布页</a>`;
      } else if (appSt.hasUpdate) {
        appDesc = `<strong>KurumiFrp 本体</strong>：发现新版本 <strong style="color:var(--gold)">v${esc(appSt.latest)}</strong>（当前 v${esc(appSt.current)}）`;
        if (appSt.canAutoUpdate) {
          appBtns += `<button type="button" class="btn btn-primary btn-sm" data-act-update-app="${esc(appSt.latest)}">一键无感更新至 v${esc(appSt.latest)}</button>`;
        } else if (appSt.manualHint) {
          appDesc += `<div class="muted-text">${esc(appSt.manualHint)}</div>`;
        }
        appBtns += `<a href="${esc(appSt.releaseURL)}" target="_blank" rel="noopener" class="btn btn-secondary btn-sm">前往下载页</a>`;
      } else {
        appDesc = `<strong>KurumiFrp 本体</strong>：已是最新版本 (v${esc(appSt.current)})`;
      }

      let frpDesc = '';
      let frpBtns = '';
      if (frpSt.error) {
        frpDesc = `<strong>官方 FRP 核心</strong>：${esc(frpSt.error)}`;
        frpBtns = `<a href="${esc(frpSt.releaseURL || 'https://github.com/fatedier/frp/releases')}" target="_blank" rel="noopener" class="btn btn-secondary btn-sm">查看上游</a>`;
      } else if (frpSt.hasUpdate) {
        frpDesc = `<strong>官方 FRP 核心</strong>：发现新版本 <strong style="color:var(--gold)">v${esc(frpSt.latest)}</strong>（当前 v${esc(frpSt.current)}）`;
        if (frpSt.canAutoUpdate) {
          frpBtns += `<button type="button" class="btn btn-primary btn-sm" data-act-update-frp="${esc(frpSt.latest)}">一键热更新核心至 v${esc(frpSt.latest)}</button>`;
        }
      } else {
        frpDesc = `<strong>官方 FRP 核心</strong>：已是最新原版 (v${esc(frpSt.current)})`;
        if (frpSt.canAutoUpdate) {
          frpBtns += `<button type="button" class="btn btn-secondary btn-sm" data-act-update-frp="${esc(frpSt.latest)}">重新校准/重装核心</button>`;
        }
      }

      box.innerHTML = `
        <div class="update-grid">
          <div class="update-item">
            <div class="update-item-info">${appDesc}</div>
            <div class="update-item-actions">${appBtns}</div>
          </div>
          <div class="update-item">
            <div class="update-item-info">${frpDesc}</div>
            <div class="update-item-actions">${frpBtns}</div>
          </div>
        </div>`;
    }

    $('#btn-save-proxy').addEventListener('click', async () => {
      const proxyURL = ($('#update-proxy-input').value || '').trim();
      try {
        const res = await api('/api/update/settings', { method: 'POST', body: { proxyURL } });
        $('#update-proxy-input').value = res.proxyURL || '';
        toast(res.proxyURL ? '已保存下载代理配置' : '已清除代理，恢复系统默认直连');
        await loadState();
      } catch (e) {
        toast(e.message, true);
      }
    });

    $('#btn-check-update').addEventListener('click', async () => {
      const box = $('#update-result-box');
      box.hidden = false;
      box.textContent = '正在同步检查 KurumiFrp 本体与上游官方 fatedier/frp 最新版本...';
      const proxyURL = ($('#update-proxy-input').value || '').trim();
      try {
        const res = await api('/api/update/check', { method: 'POST', body: { proxyURL } });
        renderUpdateCheckPanel(res);
      } catch (e) {
        box.textContent = '检查失败：' + e.message;
      }
    });

    $('#update-result-box').addEventListener('click', async (ev) => {
      const frpBtn = ev.target.closest('[data-act-update-frp]');
      if (frpBtn) {
        const ver = frpBtn.dataset.actUpdateFrp;
        frpBtn.disabled = true;
        frpBtn.textContent = '正在下载并校验 SHA-256...';
        const proxyURL = ($('#update-proxy-input').value || '').trim();
        try {
          const res = await api('/api/update/apply-frp', { method: 'POST', body: { proxyURL } });
          await loadState();
          const checkRes = await api('/api/update/check', { method: 'POST', body: { proxyURL } });
          renderUpdateCheckPanel(checkRes);
          toast(`官方 FRP 核心已无感热更新至 v${ver}（自动平滑恢复 ${res.restarted || 0} 个运行实例）`);
        } catch (e) {
          frpBtn.disabled = false;
          frpBtn.textContent = `重试热更新核心`;
          toast(e.message, true);
        }
        return;
      }

      const appBtn = ev.target.closest('[data-act-update-app]');
      if (appBtn) {
        const targetVer = appBtn.dataset.actUpdateApp;
        appBtn.disabled = true;
        appBtn.textContent = '正在下载新版安装包并校验...';
        const proxyURL = ($('#update-proxy-input').value || '').trim();
        try {
          await api('/api/update/apply-app', { method: 'POST', body: { proxyURL } });
          state.selfUpdating = true;
          const box = $('#update-result-box');
          box.innerHTML = `<div class="update-item-info">⏳ 安装包校验通过！正在后台静默升级至 <strong>v${esc(targetVer)}</strong> 并自动重启服务（若 Windows 弹出管理员授权请点「是」），页面将自动重连...</div>`;
          const startAt = Date.now();
          const timer = setInterval(async () => {
            try {
              const sess = await fetch('/api/session', { cache: 'no-store' }).then((r) => r.json());
              if (sess && sess.version === targetVer) {
                clearInterval(timer);
                state.selfUpdating = false;
                toast(`🎉 已无感升级至 KurumiFrp v${targetVer}！`);
                setTimeout(() => window.location.reload(), 600);
                return;
              }
            } catch (_) {
              // Expected briefly while daemon restarts
            }
            if (Date.now() - startAt > 50000) {
              clearInterval(timer);
              state.selfUpdating = false;
              box.innerHTML = `<div class="update-item-info">等待新版本超时（若刚才取消了 Windows 管理员授权弹窗，可重新点击上方「检查更新」再次升级）。</div>`;
            }
          }, 1000);
        } catch (e) {
          state.selfUpdating = false;
          appBtn.disabled = false;
          appBtn.textContent = `一键无感更新至 v${targetVer}`;
          toast(e.message, true);
        }
      }
    });

    $('#btn-stop-daemon').addEventListener('click', async () => {
      if (!confirm('确认关闭 KurumiFrp 后台进程并停止所有正在运行的穿透实例吗？')) return;
      await api('/api/quit', { method: 'POST' });
      toast('后台服务正在优雅退出...');
    });
  }

  /* ==================== Bootstrap ==================== */
  async function boot() {
    initSkin();
    bindEvents();
    try {
      const sess = await api('/api/session');
      if (!sess.authenticated) {
        showLoginScreen(sess);
      } else {
        showWorkbench();
        await loadState();
      }
    } catch (_) {
      showLoginScreen({});
    }

    setInterval(() => {
      if (!$('#workbench').hidden && !state.selfUpdating) {
        loadState(true).catch(() => {});
      }
    }, 4000);
  }

  boot();
})();
