'use strict';

// Forms operate on the redacted state snapshot; credentials are never populated.
const ProtocolEditor = (() => {
 const field = (name, label, value = '', type = 'text', extra = '') =>
  `<label>${label}<input name="${name}" type="${type}" value="${esc(value)}" ${extra}></label>`;
 const check = (name, label, checked) =>
  `<label class="form-check"><input name="${name}" type="checkbox" ${checked ? 'checked' : ''}>${label}</label>`;

 function instance(old) {
  const v = old || {kind:'frpc', host:'127.0.0.1', port:7000, httpPort:0, httpsPort:0, autoStart:false};
  return `<h2>${old ? '编辑实例' : '新建运行实例'}</h2>
   <p class="muted">保存草稿不会改变运行中的实例。认证令牌留空保留原值；Linux 原生服务监听小于 1024 的端口可能需要额外权限。</p>
   <div class="form-grid">
    ${field('name', '实例名称', v.name, 'text', 'required maxlength="80"')}
    <label>实例类型<select name="kind" ${old ? 'disabled' : ''}><option value="frpc" ${v.kind === 'frpc' ? 'selected' : ''}>frpc · 客户端</option><option value="frps" ${v.kind === 'frps' ? 'selected' : ''}>frps · 服务端</option></select></label>
    ${field('host', '服务器地址 / 本机监听地址', v.host, 'text', 'required')}
    ${field('port', '连接 / 监听端口', v.port, 'number', 'required min="1" max="65535" step="1"')}
    <div class="span-2">${field('token', old?.hasToken ? '认证令牌（已保存，留空保留）' : '认证令牌（8–1024 字节）', '', 'password', `${old?.hasToken ? '' : 'required'} autocomplete="new-password"`)}</div>
    <fieldset class="span-2"><legend>frp 附加认证范围</legend>${check('authHeartbeats', '对心跳附加认证（HeartBeats）', v.authHeartbeats)}${check('authNewWorkConns', '对新工作连接附加认证（NewWorkConns）', v.authNewWorkConns)}<p class="small-note">默认关闭。服务端启用某项时，连接它的客户端也须启用该项，否则心跳或代理连接可能失败。默认 TCP 多路复用的保活不属于心跳认证范围。仍使用现有共享令牌，不能据此识别账户或限制流量；更改后需重启实例。</p></fieldset>
    <div class="client-field">${field('user', '客户端用户名称（可选）', v.user || '', 'text', 'pattern="[A-Za-z0-9][A-Za-z0-9_.\\-]{0,63}"')}</div>
    <div class="client-field">${field('natHoleStunServer', 'NAT 穿透 STUN 地址（可选）', v.natHoleStunServer || '', 'text', 'placeholder="host:port"')}</div>
    <div class="client-field">${field('dnsServer', 'DNS 服务器 IP（可选）', v.dnsServer || '', 'text', 'placeholder="例如 1.1.1.1 或 [::1]:53"')}</div>
    <div class="client-field"><label>连接协议<select name="clientTransport">${['tcp','websocket','wss','kcp','quic'].map(protocol => `<option value="${protocol}" ${(v.clientTransport || 'tcp') === protocol ? 'selected' : ''}>${protocol.toUpperCase()}</option>`).join('')}</select></label></div>
    <div class="client-field span-2">${field('clientBindIP', '连接服务端时使用的本机 IP（可选）', v.clientBindIP || '', 'text', 'maxlength="64" placeholder="例如 192.0.2.10 或 2001:db8::10"')}<small>仅 TCP、WebSocket 和 WSS 生效；地址必须属于本机，并与服务端连接的地址族兼容。</small></div>
    <div class="client-field">${field('clientDialTimeout', '连接超时（秒，0 默认 10）', v.clientDialTimeout ?? 0, 'number', 'min="0" max="3600" step="1"')}</div>
    <div class="client-field">${field('clientPoolCount', '客户端预建工作连接数（0 默认 1）', v.clientPoolCount ?? 0, 'number', 'min="0" max="10000" step="1"')}</div>
    <div class="span-2">${check('transportMuxDisabled', '关闭客户端与服务端之间的 TCP 多路复用', v.transportMuxDisabled)}<small>两端设置必须一致；这不是规则中的 TCPMUX 协议。</small></div>
    <div>${field('tcpKeepalive', 'TCP 保活间隔（秒）', v.tcpKeepalive ?? 0, 'number', 'min="-1" max="86400" step="1"')}<small>0 使用默认 7200 秒，-1 关闭；客户端 KCP/QUIC 不使用此项。</small></div>
    <div>${field('muxKeepalive', '多路复用保活间隔（秒）', v.muxKeepalive ?? 0, 'number', 'min="0" max="3600" step="1"')}<small>0 使用默认 30 秒；需启用 TCP 多路复用。</small></div>
    <div class="client-field span-2">${check('loginFailExit', '首次登录失败时退出（关闭时持续重连）', v.loginFailExit)}</div>
    <div class="client-field span-2">${check('clientTLSDisabled', '关闭客户端 TLS（WSS 和 QUIC 始终使用 TLS）', v.clientTLSDisabled)}</div>
    <div class="client-field span-2">${check('clientTLSUseSystemCA', '使用系统信任库验证服务端 TLS 证书', v.clientTLSUseSystemCA)}<small>可与下方 CA 文件合用；两者都未配置时不验证服务端证书。此设置不识别账户。</small></div>
    <div class="span-2">${field('tlsCertFile', 'frp TLS 证书文件绝对路径（可选）', v.tlsCertFile || '', 'text', 'maxlength="1024" autocomplete="off"')}</div>
    <div class="span-2">${field('tlsKeyFile', 'frp TLS 私钥文件绝对路径（可选）', v.tlsKeyFile || '', 'text', 'maxlength="1024" autocomplete="off"')}<small>证书和私钥须成对填写。只保存路径，不上传或复制文件；工作台账户须有读取权限。配置检查不读取文件，启动或连接时才加载。</small></div>
    <div class="span-2">${field('tlsTrustedCAFile', 'frp TLS 可信 CA 文件绝对路径（可选）', v.tlsTrustedCAFile || '', 'text', 'maxlength="1024" autocomplete="off"')}</div>
    <p class="client-field small-note span-2">客户端 CA 用于验证服务端证书；与系统信任库同时启用时合并信任。证书、私钥和 CA 文件均要求启用客户端 TLS。</p>
    <div class="client-field span-2">${field('clientTLSServerName', '验证服务端证书时使用的名称（可选）', v.clientTLSServerName || '', 'text', 'maxlength="253" placeholder="例如 frps.example.com"')}<small>留空使用连接地址；填写 DNS 名称或 IP，不含端口。仅设置名称不会启用证书验证。</small></div>
    <p class="server-field small-note span-2">服务端 CA 用于要求并验证 frp 客户端证书，填写后不能允许明文控制连接；不保护公网代理端口。</p>
    <div class="server-field">${field('kcpBindPort', 'KCP 监听端口（0 关闭）', v.kcpBindPort ?? 0, 'number', 'min="0" max="65535" step="1"')}</div>
    <div class="server-field">${field('quicBindPort', 'QUIC 监听端口（0 关闭）', v.quicBindPort ?? 0, 'number', 'min="0" max="65535" step="1"')}</div>
    <div class="server-field">${field('httpPort', 'HTTP 虚拟主机端口（0 关闭）', v.httpPort ?? 0, 'number', 'min="0" max="65535" step="1"')}</div>
    <div class="server-field">${field('httpsPort', 'HTTPS 虚拟主机端口（0 关闭）', v.httpsPort ?? 0, 'number', 'min="0" max="65535" step="1"')}</div>
    <div class="server-field">${field('tcpmuxHTTPConnectPort', 'TCPMUX HTTP CONNECT 端口（0 关闭）', v.tcpmuxHTTPConnectPort ?? 0, 'number', 'min="0" max="65535" step="1"')}</div>
    <div class="server-field">${field('subDomainHost', '服务端子域名后缀（可选）', v.subDomainHost || '')}</div>
    <div class="server-field span-2">${field('proxyBindAddr', '代理监听地址（留空使用服务端地址）', v.proxyBindAddr || '', 'text', 'placeholder="例如 0.0.0.0"')}<small>用于 TCP/UDP 代理和 HTTP(S)、TCPMUX 虚拟主机监听；不改变客户端连接端口的监听地址。</small></div>
    <div class="server-field span-2">${field('maxPortsPerClient', '每客户端代理端口上限（0 不限）', v.maxPortsPerClient ?? 0, 'number', 'min="0" max="131070" step="1"')}<small>由服务端按客户端连接统计占用的代理端口，不等同于规则数量或流量配额。</small></div>
    <div class="server-field span-2">${field('serverAllowedPorts', '允许分配的代理端口（留空不限）', v.serverAllowedPorts || '', 'text', 'maxlength="1024" placeholder="例如 6000-6010,7000"')}<small>限制 TCP/UDP 代理端口及动态分配范围；不影响 HTTP(S) 虚拟主机监听端口。</small></div>
    <div class="server-field span-2"><label>控制入口来源限制<select name="serverAccessMode"><option value="" ${!v.serverAccessMode ? 'selected' : ''}>关闭</option><option value="allow" ${v.serverAccessMode === 'allow' ? 'selected' : ''}>只允许列表中的地址</option><option value="block" ${v.serverAccessMode === 'block' ? 'selected' : ''}>阻止列表中的地址</option></select></label><small>只检查连接到本实例 frp 控制入口的直接对端，不过滤公开代理端口；不能代替系统防火墙。</small></div>
    <div class="server-field span-2"><label>来源 IP 或 CIDR（一行一项，最多 128 项）<textarea name="serverAccessList" rows="3" maxlength="8192" spellcheck="false" placeholder="例如 192.0.2.0/24&#10;2001:db8::1">${esc(v.serverAccessList || '')}</textarea></label><small>保存后需重启应用；关闭限制时保存会清空列表。无法识别的对端地址会被拒绝。</small></div>
    <div class="server-field">${field('vhostHTTPTimeout', 'HTTP 响应头超时（秒，0 默认 60）', v.vhostHTTPTimeout ?? 0, 'number', 'min="0" max="86400" step="1"')}</div>
    <div class="server-field">${field('userConnTimeout', '等待代理工作连接超时（秒，0 默认 10）', v.userConnTimeout ?? 0, 'number', 'min="0" max="86400" step="1"')}</div>
    <div class="server-field">${field('serverMaxPoolCount', '每客户端最大预建连接数（0 默认 5）', v.serverMaxPoolCount ?? 0, 'number', 'min="0" max="10000" step="1"')}<small>服务端会限制客户端请求的预建连接数量。</small></div>
    <div class="server-field span-2">${check('tcpmuxPassthrough', '启用 TCPMUX 透传（需要设置 HTTP CONNECT 端口）', v.tcpmuxPassthrough)}</div>
    <div class="server-field span-2">${check('serverAllowPlaintext', '允许不使用 TLS 的客户端连接', v.serverAllowPlaintext)}</div>
    <div class="span-2">${check('autoStart', '工作台启动时自动启动此实例', v.autoStart)}</div>
   </div>`;
 }

 function httpHeaderRow(header = {}) {
  const saved = !!header.hasValue;
  return `<div class="http-header-row" data-http-header-row data-saved="${saved ? 'true' : 'false'}"><label>名称<input data-header-name type="text" value="${esc(header.name || '')}" maxlength="128" required></label><label>值<input data-header-value type="password" maxlength="1024" autocomplete="new-password" placeholder="${saved ? '已保存，留空保留' : '留空表示空值'}"></label><label class="form-check"><input data-header-empty type="checkbox">设为空值</label><button type="button" data-http-header-remove>删除</button></div>`;
 }

 function httpHeaderEditor(v) {
  const section = (label, direction, headers) => `<div class="http-header-group"><div class="http-header-group-title"><strong>${label}</strong><button type="button" data-http-header-add="${direction}">添加</button></div><div data-http-header-list="${direction}">${(headers || []).map(httpHeaderRow).join('')}</div></div>`;
  return `<div class="http-routing-field span-2 http-header-editor"><h3>自定义 HTTP 头</h3><p class="small-note">每个方向最多 32 项，每个客户端最多 128 项；名称不区分大小写。值加密保存，不会回显。已有项留空保留，勾选“设为空值”可清空；新项留空即为空值。重命名需重新输入值或明确设为空值。</p>${section('请求头', 'request', v.requestHeaders)}${section('响应头', 'response', v.responseHeaders)}<p class="small-note">非空短值可能与普通文字相同，命中的日志或诊断内容会整条隐藏。空值不参与脱敏匹配。</p></div><p class="http-header-removal-note small-note span-2" hidden>更换协议前，请返回 HTTP 并删除全部自定义头后再保存。</p>`;
 }

 function actionClick(form, target) {
  const generate = target.closest('[data-socks-generate]');
  if (generate) {
   if (!window.crypto?.getRandomValues) throw new Error('当前浏览器无法生成安全随机密码，请自行填写。');
   const bytes = window.crypto.getRandomValues(new Uint8Array(24));
   const input = form.elements.pluginPassword;
   input.value = Array.from(bytes, value => value.toString(16).padStart(2, '0')).join('');
   input.type = 'text';
   form.querySelector('[data-socks-visibility]').textContent = '隐藏密码';
   input.select();
   return;
  }
  const visibility = target.closest('[data-socks-visibility]');
  if (visibility) {
   const input = form.elements.pluginPassword;
   input.type = input.type === 'password' ? 'text' : 'password';
   visibility.textContent = input.type === 'password' ? '显示密码' : '隐藏密码';
   return;
  }
  const add = target.closest('[data-http-header-add]');
  if (add) {
   const list = form.querySelector(`[data-http-header-list="${add.dataset.httpHeaderAdd}"]`);
   if (!list) return;
   if (list.querySelectorAll('[data-http-header-row]').length >= 32) {
    form.querySelector('#form-error').textContent = '每个方向最多支持 32 个 HTTP 头。';
    return;
   }
   list.insertAdjacentHTML('beforeend', httpHeaderRow());
   list.lastElementChild?.querySelector('[data-header-name]')?.focus();
   return;
  }
  const remove = target.closest('[data-http-header-remove]');
  if (remove) remove.closest('[data-http-header-row]')?.remove();
 }

 function readHTTPHeaders(form, direction) {
  const list = form.querySelector(`[data-http-header-list="${direction}"]`);
  return [...list.querySelectorAll('[data-http-header-row]')].map(row => {
   const name = row.querySelector('[data-header-name]').value.trim();
   const value = row.querySelector('[data-header-value]').value;
   const empty = row.querySelector('[data-header-empty]').checked;
   if (empty && value !== '') throw new Error('设置空值时请清空输入框。');
   const item = {name};
   if (empty || row.dataset.saved !== 'true' || value !== '') item.value = empty ? '' : value;
   return item;
  });
 }

 function tunnel(old, clients) {
  const v = old || {instanceId:clients[0].id, type:'tcp', localHost:'127.0.0.1', localPort:8080, remotePort:6000, domains:[], probePath:'', probeMethod:'HEAD', bandwidthKB:0, enabled:true};
  const types = Protocols.types.includes(v.type) ? Protocols.types : [...Protocols.types, v.type];
  const lockedPort = !!old && v.type === 'stcp-visitor' && v.localPort < 0;
  const lockedType = !!old && (lockedPort || !['tcp', 'udp', 'http', 'https', 'stcp', 'stcp-visitor'].includes(v.type));
  const users = (v.allowUsers || []).map(user => user === '' ? '""' : user).join('\n');
  return `<h2>${old ? '编辑规则' : '新建连接规则'}</h2>
   <p class="muted">保存只更新草稿。启停意图在下次启动或重启应用时生效；Linux 原生服务的 Visitor 监听低于 1024 的端口可能需要额外权限。</p>
   <div class="form-grid">
    ${field('name', '规则名称', v.name, 'text', 'required pattern="[A-Za-z0-9][A-Za-z0-9_.\\-]{0,63}"')}
    <label>归属实例<select name="instanceId">${clients.map(i => `<option value="${esc(i.id)}" ${v.instanceId === i.id ? 'selected' : ''}>${esc(i.name)}</option>`).join('')}</select></label>
    <p class="visitor-transfer-note small-note span-2" hidden>目标客户端可能连接不同的服务端，请核对目标服务。未填写服务端用户时，跨实例移动会保留源客户端的有效用户；如要清空已有的显式用户，请在移动后单独修改。若源和目标客户端用户都为空，之后更改目标客户端用户会改变默认服务用户。移动时 XTCP 不能携带回退：如原规则已有回退，请先在原实例单独清除，移动后再设置。</p>
    <label>协议<select name="type" ${lockedType ? 'disabled' : ''}>${types.map(type => `<option value="${esc(type)}" ${type === v.type ? 'selected' : ''}>${esc(type)}</option>`).join('')}</select>${lockedType ? '<small>当前暂不支持更改此规则的协议类型。</small>' : ''}</label>
    <div class="bandwidth-field">${field('bandwidthKB', '带宽上限（KB/s，0 不限）', v.bandwidthKB ?? 0, 'number', 'min="0" max="10000000" step="1"')}</div>
    <div class="bandwidth-field"><label>限速位置<select name="bandwidthMode"><option value="client" ${(v.bandwidthMode || 'client') === 'client' ? 'selected' : ''}>客户端</option><option value="server" ${v.bandwidthMode === 'server' ? 'selected' : ''}>服务端</option></select></label></div>
    <div class="proxy-protocol-field span-2"><label>PROXY 协议<select name="proxyProtocolVersion"><option value="" ${!v.proxyProtocolVersion ? 'selected' : ''}>关闭</option><option value="v1" ${v.proxyProtocolVersion === 'v1' ? 'selected' : ''}>v1</option><option value="v2" ${v.proxyProtocolVersion === 'v2' ? 'selected' : ''}>v2</option></select></label><small>目标服务必须支持所选版本；frp 取得来源地址时会传递连接信息。</small></div>
    <div class="unix-target-choice span-2"><label>本地目标类型<select name="targetKind"><option value="network" ${!v.unixSocketPath && v.pluginType !== 'socks5' ? 'selected' : ''}>TCP 地址和端口</option><option value="unix" ${v.unixSocketPath ? 'selected' : ''}>Unix 域套接字</option><option value="socks5" ${v.pluginType === 'socks5' ? 'selected' : ''}>SOCKS5 代理</option></select></label><small>Unix 和 SOCKS5 目标仅用于 TCP 规则；Unix 目标支持连接式代理健康检查。切换为 SOCKS5 会清除代理健康检查、PROXY 协议和本地诊断参数。</small></div>
    <label class="network-target-field"><span class="local-host-label">本地目标地址</span><input name="localHost" type="text" value="${esc(v.localHost)}" required></label>
    <label class="network-target-field"><span class="local-port-label">本地目标端口</span><input name="localPort" type="number" value="${esc(v.localPort)}" required min="1" max="65535" step="1" ${lockedPort ? 'readonly' : ''}><small class="unbound-note" hidden>负数表示仅接收回退连接，不监听本地端口。</small>${lockedPort ? '<small>当前暂不支持更改此规则的回退监听端口。</small>' : ''}</label>
    <div class="unix-target-field span-2">${field('unixSocketPath', 'Unix 套接字绝对路径', v.unixSocketPath || '', 'text', 'maxlength="1024" placeholder="/run/service.sock"')}<small>需运行在支持 Unix 域套接字的平台，且工作台账户有访问该套接字的权限。</small></div>
    <div class="socks-target-field">${field('pluginUser', 'SOCKS5 用户名（1–255 UTF-8 字节）', v.pluginType === 'socks5' ? (v.pluginUser || '') : '', 'text', 'maxlength="255" autocomplete="off"')}</div>
    <div class="socks-target-field">${field('pluginPassword', old?.pluginType === 'socks5' && old?.hasPluginPassword ? 'SOCKS5 密码（已保存，留空保留）' : 'SOCKS5 密码（1–255 UTF-8 字节）', '', 'password', 'maxlength="255" autocomplete="new-password"')}<div class="actions"><button type="button" data-socks-generate>生成随机密码</button><button type="button" data-socks-visibility>显示密码</button></div><small>建议使用随机密码；生成后可复制，保存时才会替换已有密码。</small></div>
    <p class="socks-target-field small-note span-2">仅支持 TCP CONNECT。代理可访问 frpc 主机能够访问的内网；用户名和密码只验证代理入口，不限制目标地址，也不加密 SOCKS5 连接。请限制远端代理端口的访问并保护连接。客户端限速暂不可用，可选择服务端限速。</p>
    <div class="socks-removal-field span-2" hidden>${check('confirmPluginRemoval', '确认切换目标并清除已保存的 SOCKS5 认证信息', false)}<small>保存后原有 SOCKS5 用户名和密码将被清除；切换回来须重新输入。</small></div>
    <div class="port-field">${field('remotePort', '远端端口（0 请求动态分配）', ['tcp', 'udp'].includes(v.type) ? (v.remotePort ?? '') : '', 'number', 'min="0" max="65535" step="1"')}</div>
    <div class="domain-field span-2">${field('domains', '域名（多个用逗号分隔）', (v.domains || []).join(', '))}<small>支持 * 和 *.example.com；后者不匹配 example.com 本身。HTTPS 证书须覆盖访问的具体域名。</small></div>
    <div class="probe-field span-2 protocol-subgrid">
     ${field('probePath', '本地健康检查路径（仅用于诊断）', v.probePath || '', 'text', 'maxlength="512" placeholder="/（默认）"')}
     <label>HTTP 检测方法<select name="probeMethod"><option value="" ${!v.probeMethod ? 'selected' : ''}>默认 · HEAD</option><option value="HEAD" ${v.probeMethod === 'HEAD' ? 'selected' : ''}>HEAD · 只检查响应头</option><option value="GET" ${v.probeMethod === 'GET' ? 'selected' : ''}>GET · 兼容只支持 GET 的接口</option></select></label>
     <p class="small-note span-2">仅用于 KurumiFRP 本地检测，不会写入 frp 配置；响应正文不会保存或显示。</p>
    </div>
    <div class="health-check-field span-2"><label>代理自动健康检查<select name="healthCheckType"><option value="" ${!v.healthCheckType ? 'selected' : ''}>关闭</option><option value="tcp" ${v.healthCheckType === 'tcp' ? 'selected' : ''}>TCP 连接</option><option value="http" ${v.healthCheckType === 'http' ? 'selected' : ''}>HTTP 请求</option></select></label><small>失败达到设定次数后暂停代理，服务恢复后自动重新注册。</small><small class="health-unix-note" hidden>Unix 目标只检查能否连接套接字，不检查应用响应；不支持 HTTP 检查，请选择连接式检查或关闭。</small></div>
    <div class="health-http-field span-2">${field('healthCheckPath', 'HTTP 健康检查路径', v.healthCheckPath || '/', 'text', 'maxlength="512" placeholder="/health"')}</div>
    <div class="health-check-options">${field('healthCheckTimeoutSeconds', '检查超时（秒，0 使用默认）', v.healthCheckTimeoutSeconds ?? 0, 'number', 'min="0" max="86400" step="1"')}</div>
    <div class="health-check-options">${field('healthCheckIntervalSeconds', '检查间隔（秒，0 使用默认）', v.healthCheckIntervalSeconds ?? 0, 'number', 'min="0" max="604800" step="1"')}</div>
    <div class="health-check-options">${field('healthCheckMaxFailed', '连续失败次数（0 使用默认）', v.healthCheckMaxFailed ?? 0, 'number', 'min="0" max="1000" step="1"')}</div>
    <div class="subdomain-field">${field('subdomain', '子域名（可选）', v.subdomain || '')}</div>
    <p class="subdomain-field small-note span-2">至少填写一个自定义域名或子域名；子域名需要在服务端配置后缀。</p>
    <div class="http-routing-field span-2">${field('hostHeaderRewrite', '转发时重写 Host（可选）', v.hostHeaderRewrite || '', 'text', 'maxlength="512" placeholder="例如 internal.example.com"')}</div>
    <div class="http-routing-field span-2"><label>HTTP 路径前缀（每行一条）<textarea name="locations" rows="3" placeholder="/api&#10;/assets">${esc((v.locations || []).join('\n'))}</textarea></label><small>留空匹配全部路径；按解码后的请求路径作前缀匹配，不包含查询字符串。例如 /api 也会匹配 /apix。</small></div>
    <div class="http-routing-field span-2">${field('httpRouteByUser', '按 HTTP 用户名路由（可选）', v.httpRouteByUser || '')}<small>只选择路由，不能验证身份或限制访问。需要保护目标时，请另外启用认证；同域名可为不同用户名配置不同路由。</small></div>
    <div class="http-routing-field span-2">${check('httpAuthEnabled', '启用 HTTP Basic 认证', v.httpAuthEnabled)}<small>请通过 TLS 保护访问链路；Basic 认证本身不加密用户名和密码。</small></div>
    ${httpHeaderEditor(v)}
    <div class="tcpmux-field"><label>多路复用器<select name="multiplexer"><option value="" ${!v.multiplexer ? 'selected' : ''}>默认 · httpconnect</option><option value="httpconnect" ${v.multiplexer === 'httpconnect' ? 'selected' : ''}>httpconnect</option></select></label></div>
    <div class="http-credential-field">${field('httpUser', 'HTTP 认证用户名', v.httpUser || '')}</div>
    <div class="http-credential-field">${field('httpPassword', old?.hasHTTPPassword ? 'HTTP 密码（已保存，留空保留）' : 'HTTP 密码（8–1024 字节）', '', 'password', 'autocomplete="new-password"')}</div>
    <div class="tcpmux-field span-2">${field('routeByHTTPUser', '按 HTTP 用户名路由（可选）', v.routeByHTTPUser || '')}</div>
    <div class="password-clear-field span-2" hidden>${check('clearHTTPPassword', '清除已保存的 HTTP 密码', false)}<p class="small-note password-clear-note">勾选后保存会清除已保存的密码。</p></div>
    <div class="secret-removal-field span-2" hidden>${check('confirmSecretRemoval', '确认更改协议并清除已保存的配对密钥', false)}</div>
    <div class="visitor-field">${field('serverName', '服务端规则名', v.serverName || '', 'text', 'pattern="[A-Za-z0-9][A-Za-z0-9_.\\-]{0,63}"')}</div>
    <div class="visitor-field">${field('serverUser', '服务端用户名称（可选）', v.serverUser || '', 'text', 'pattern="[A-Za-z0-9][A-Za-z0-9_.\\-]{0,63}"')}</div>
    <div class="secret-field span-2">${field('secret', old?.hasSecret ? '配对密钥（已保存，留空保留）' : '配对密钥（8–1024 字节）', '', 'password', 'autocomplete="new-password"')}</div>
    <div class="private-proxy-field span-2"><label>允许访问的用户（一行一个；可选）<textarea name="allowUsers" rows="3" spellcheck="false">${esc(users)}</textarea><small>留空仅允许规则所属用户；* 表示全部用户，单独一行写 "" 表示无前缀用户。</small></label></div>
    <div class="xtcp-field span-2">${check('disableAssistedAddrs', '禁用辅助地址（NAT 穿透）', v.disableAssistedAddrs)}</div>
    <div class="xtcp-visitor-field"><label>XTCP 传输协议<select name="protocol"><option value="" ${!v.protocol ? 'selected' : ''}>默认</option><option value="quic" ${v.protocol === 'quic' ? 'selected' : ''}>quic</option><option value="kcp" ${v.protocol === 'kcp' ? 'selected' : ''}>kcp</option></select></label></div>
    <div class="xtcp-visitor-field">${field('fallbackTo', '回退 Visitor 名称（可选）', v.fallbackTo || '', 'text', 'list="fallback-candidates" pattern="[A-Za-z0-9][A-Za-z0-9_.\\-]{0,63}"')}<datalist id="fallback-candidates"></datalist></div>
    <div class="xtcp-visitor-field">${field('fallbackTimeoutMs', '回退超时（毫秒，0 使用默认）', v.fallbackTimeoutMs ?? 0, 'number', 'min="0" max="2000000000" step="1"')}</div>
    <div class="xtcp-visitor-field">${field('maxRetriesAnHour', '每小时最大重试次数（0 使用默认）', v.maxRetriesAnHour ?? 0, 'number', 'min="0" max="1000000" step="1"')}</div>
    <div class="xtcp-visitor-field">${field('minRetryInterval', '最短重试间隔（秒，0 使用默认）', v.minRetryInterval ?? 0, 'number', 'min="0" max="2147483" step="1"')}</div>
    <div class="xtcp-visitor-field">${check('keepTunnelOpen', '保持隧道打开', v.keepTunnelOpen)}</div>
    <p class="xtcp-visitor-field small-note span-2">启用时，回退目标须为同实例中已启用的 STCP 或 XTCP Visitor，且回退链不能循环。停用的草稿可先填写将来创建的目标。</p>
    <div>${check('useEncryption', '启用传输加密', v.useEncryption)}</div>
    <div>${check('useCompression', '启用传输压缩', v.useCompression)}</div>
    <div class="span-2">${check('enabled', '期望启用此规则', v.enabled)}</div>
   </div>`;
 }

 function batch(clients) {
  return `<h2>批量创建连接规则</h2><p class="muted">一次保存全部规则草稿；运行中的实例需重启后应用。生成的规则可逐条编辑。</p>
   <div class="form-grid">
    ${field('name', '规则名称前缀', '', 'text', 'required maxlength="64" pattern="[A-Za-z0-9][A-Za-z0-9_.\\-]{0,63}"')}
    <label>归属实例<select name="instanceId">${clients.map(i => `<option value="${esc(i.id)}">${esc(i.name)}</option>`).join('')}</select></label>
    <label>协议<select name="protocol"><option value="tcp+udp">TCP+UDP · 成对创建</option><option value="tcp">TCP</option><option value="udp">UDP</option></select></label>
    ${field('bandwidthKB', '每条规则的带宽上限（KB/s，0 不限）', 0, 'number', 'required min="0" max="10000000" step="1"')}
    <label>限速位置<select name="bandwidthMode"><option value="client">客户端</option><option value="server">服务端</option></select></label>
    <div class="batch-proxy-protocol-field"><label>PROXY 协议<select name="proxyProtocolVersion"><option value="">关闭</option><option value="v1">v1</option><option value="v2">v2</option></select></label><small>仅应用于 TCP 规则，UDP 规则不使用；TCP 目标服务必须支持所选版本。</small></div>
    ${field('localHost', '本地目标地址', '127.0.0.1', 'text', 'required')}
    <label>本地端口列表<input name="localPorts" type="text" required maxlength="4096" placeholder="例如 8000-8002,8010"></label>
    <label class="span-2">远端端口列表<input name="remotePorts" type="text" required maxlength="4096" placeholder="例如 6000-6002,6010，或填 0 动态分配"></label>
    <p class="small-note span-2">端口可用逗号和范围，固定端口按顺序一一对应。多端口名称加本地端口后缀，TCP+UDP 再加 -tcp / -udp。规则总数最多 512 条。</p>
    <p class="small-note span-2">远端 0 只支持单协议，由服务端为每条规则分配端口；TCP+UDP 成对创建须使用固定端口。草稿检查无法确认远端端口可用。</p>
    <div>${check('useEncryption', '为所有生成的规则启用传输加密', false)}</div>
    <div>${check('useCompression', '为所有生成的规则启用传输压缩', false)}</div>
    <label class="form-check span-2"><input name="enabled" type="checkbox" checked>期望启用这些规则</label>
   </div>`;
 }

 function batchValue(form) {
  const f = form.elements, val = name => f[name].value;
  return {name:val('name').trim(), instanceId:val('instanceId'), protocol:val('protocol'), localHost:val('localHost').trim(),
   localPorts:val('localPorts').trim(), remotePorts:val('remotePorts').trim(), bandwidthKB:+val('bandwidthKB'), bandwidthMode:val('bandwidthMode'), proxyProtocolVersion:['tcp','tcp+udp'].includes(val('protocol')) ? val('proxyProtocolVersion') : '',
   useEncryption:f.useEncryption.checked, useCompression:f.useCompression.checked, enabled:f.enabled.checked};
 }

 function visible(form, selector, show) {
  form.querySelectorAll(selector).forEach(el => {
   el.hidden = !show;
   el.querySelectorAll('input, select, textarea').forEach(input => { input.disabled = !show; });
  });
 }

 function update(form, editing, tunnels) {
  const f = form.elements;
  if (editing.kind === 'tunnel-batch') {
   visible(form, '.batch-proxy-protocol-field', ['tcp','tcp+udp'].includes(f.protocol.value));
   return;
  }
  if (editing.kind === 'instance') {
   visible(form, '.client-field', f.kind.value === 'frpc');
   visible(form, '.server-field', f.kind.value === 'frps');
   return;
  }
  const type = f.type.value, visitor = Protocols.isVisitor(type), paired = Protocols.hasPairingSecret(type);
  const unixTarget = type === 'tcp' && f.targetKind.value === 'unix', socksTarget = type === 'tcp' && f.targetKind.value === 'socks5';
  const networkTarget = !unixTarget && !socksTarget;
  visible(form, '.unix-target-choice', type === 'tcp');
  visible(form, '.unix-target-field', unixTarget);
  visible(form, '.socks-target-field', socksTarget);
  visible(form, '.network-target-field', networkTarget);
  f.unixSocketPath.required = unixTarget;
  f.localHost.required = networkTarget;
  f.localPort.required = networkTarget;
  f.pluginUser.required = socksTarget;
  f.pluginPassword.required = socksTarget && !(editing.old?.pluginType === 'socks5' && editing.old?.hasPluginPassword);
  const removeSocks = editing.old?.pluginType === 'socks5' && !socksTarget;
  visible(form, '.socks-removal-field', removeSocks);
  f.confirmPluginRemoval.required = removeSocks;
  if (!removeSocks) f.confirmPluginRemoval.checked = false;
  form.querySelector('.visitor-transfer-note').hidden = !(editing.old && Protocols.isVisitor(editing.old.type) && visitor && f.instanceId.value !== editing.old.instanceId);
  visible(form, '.bandwidth-field', !visitor);
  visible(form, '.proxy-protocol-field', !visitor && !socksTarget && !['udp', 'sudp'].includes(type));
  visible(form, '.port-field', ['tcp', 'udp'].includes(type));
  visible(form, '.domain-field', ['http', 'https', 'tcpmux'].includes(type));
  visible(form, '.subdomain-field', ['http', 'https', 'tcpmux'].includes(type));
  visible(form, '.http-routing-field', type === 'http');
  visible(form, '.http-header-removal-note', type !== 'http' && !!form.querySelector('[data-http-header-row]'));
  visible(form, '.probe-field', ['http', 'https'].includes(type));
  visible(form, '.health-check-field', !visitor && !socksTarget);
  visible(form, '.health-check-options', !visitor && !socksTarget && f.healthCheckType.value !== '');
  visible(form, '.health-http-field', !visitor && !unixTarget && !socksTarget && f.healthCheckType.value === 'http');
  f.healthCheckPath.required = !visitor && !unixTarget && !socksTarget && f.healthCheckType.value === 'http';
  const httpHealth = f.healthCheckType.querySelector('option[value="http"]');
  httpHealth.disabled = unixTarget && f.healthCheckType.value !== 'http';
  f.healthCheckType.querySelector('option[value="tcp"]').textContent = unixTarget ? 'Unix 套接字连接' : 'TCP 连接';
  f.healthCheckType.setCustomValidity(unixTarget && f.healthCheckType.value === 'http' ? 'Unix 套接字不支持 HTTP 健康检查，请选择连接式检查或关闭。' : '');
  form.querySelector('.health-unix-note').hidden = !unixTarget;
  visible(form, '.tcpmux-field', type === 'tcpmux');
  visible(form, '.http-credential-field', type === 'tcpmux' || (type === 'http' && f.httpAuthEnabled.checked));
  visible(form, '.visitor-field', visitor);
  visible(form, '.secret-field', paired);
  visible(form, '.private-proxy-field', paired && !visitor);
  visible(form, '.xtcp-field', type === 'xtcp' || type === 'xtcp-visitor');
  visible(form, '.xtcp-visitor-field', type === 'xtcp-visitor');
  const oldPassword = !!editing.old?.hasHTTPPassword;
  const clearOnTypeChange = oldPassword && type !== editing.old.type;
  visible(form, '.password-clear-field', oldPassword);
  f.clearHTTPPassword.required = clearOnTypeChange;
  f.httpPassword.disabled = !(type === 'tcpmux' || (type === 'http' && f.httpAuthEnabled.checked)) || (oldPassword && f.clearHTTPPassword.checked);
  form.querySelector('.password-clear-note').textContent = clearOnTypeChange
   ? '更改协议会清除已保存的 HTTP 密码，请先勾选确认。'
   : type === 'http' && f.httpAuthEnabled.checked
    ? '清除密码时请先关闭 HTTP Basic 认证；修改密码请取消勾选并输入新密码。'
    : '勾选后保存会清除已保存的密码；输入新密码前请取消勾选。';
  const clearSecret = !!editing.old?.hasSecret && !paired;
  visible(form, '.secret-removal-field', clearSecret);
  f.confirmSecretRemoval.required = clearSecret;
  f.secret.required = paired && !editing.old?.hasSecret;
  f.serverName.required = visitor;
  f.remotePort.required = ['tcp', 'udp'].includes(type);
  f.domains.required = false; // The server validates the domain/subdomain pair.
  form.querySelector('.local-host-label').textContent = visitor ? 'Visitor 监听地址' : '本地目标地址';
  form.querySelector('.local-port-label').textContent = visitor ? 'Visitor 监听端口' : '本地目标端口';
  const unbound = Protocols.supportsUnbound(type);
  form.querySelector('.unbound-note').hidden = !unbound;
  if (unbound) f.localPort.removeAttribute('min'); else f.localPort.min = '1';
  f.localPort.setCustomValidity(unbound && f.localPort.value !== '' && Number(f.localPort.value) === 0 ? '请输入正端口，或使用负数表示仅接收回退连接。' : '');
  const options = tunnels.filter(t => t.instanceId === f.instanceId.value && t.id !== editing.id && t.enabled && Protocols.supportsUnbound(t.type));
  form.querySelector('#fallback-candidates').innerHTML = options.map(t => `<option value="${esc(t.name)}">${esc(t.type)}</option>`).join('');
 }

 function validSocksCredential(text) {
  const bytes = new TextEncoder().encode(text).length;
  return bytes >= 1 && bytes <= 255 && !/[\u0000-\u001f\u007f-\u009f\ud800-\udfff]/u.test(text);
 }

 function value(form, editing) {
  const f = form.elements, val = name => f[name].value;
  const result = {...(editing.old || {}), id:editing.id};
  if (editing.kind === 'instance') {
   const kind = val('kind');
   const certFile = val('tlsCertFile'), keyFile = val('tlsKeyFile'), trustedCAFile = val('tlsTrustedCAFile');
   const tlsServerName = kind === 'frpc' ? val('clientTLSServerName').trim() : '';
   if (!!certFile !== !!keyFile) throw new Error('TLS 证书和私钥路径须同时填写或同时留空。');
   if (kind === 'frpc' && f.clientTLSDisabled.checked && (certFile || keyFile || trustedCAFile || tlsServerName)) throw new Error('配置客户端证书、私钥、CA 或服务端名称时不能关闭 TLS。');
   if (kind === 'frps' && f.serverAllowPlaintext.checked && trustedCAFile) throw new Error('配置客户端证书 CA 时不能允许明文连接。');
   delete result.hasToken;
   Object.assign(result, {name:val('name').trim(), kind, host:val('host').trim(), port:+val('port'), token:val('token'), autoStart:f.autoStart.checked, authHeartbeats:f.authHeartbeats.checked, authNewWorkConns:f.authNewWorkConns.checked,
    tlsCertFile:certFile, tlsKeyFile:keyFile, tlsTrustedCAFile:trustedCAFile, clientTLSServerName:tlsServerName,
    httpPort:kind === 'frps' ? +val('httpPort') : 0, httpsPort:kind === 'frps' ? +val('httpsPort') : 0,
    user:kind === 'frpc' ? val('user').trim() : '', natHoleStunServer:kind === 'frpc' ? val('natHoleStunServer').trim() : '', dnsServer:kind === 'frpc' ? val('dnsServer').trim() : '', clientTransport:kind === 'frpc' ? val('clientTransport') : '', clientBindIP:kind === 'frpc' ? val('clientBindIP').trim() : '', loginFailExit:kind === 'frpc' && f.loginFailExit.checked, clientTLSDisabled:kind === 'frpc' && f.clientTLSDisabled.checked, clientTLSUseSystemCA:kind === 'frpc' && f.clientTLSUseSystemCA.checked, serverAllowPlaintext:kind === 'frps' && f.serverAllowPlaintext.checked, kcpBindPort:kind === 'frps' ? +val('kcpBindPort') : 0, quicBindPort:kind === 'frps' ? +val('quicBindPort') : 0,
    tcpmuxHTTPConnectPort:kind === 'frps' ? +val('tcpmuxHTTPConnectPort') : 0, serverAccessMode:kind === 'frps' ? val('serverAccessMode') : '', serverAccessList:kind === 'frps' && val('serverAccessMode') ? val('serverAccessList').trim() : '', clientDialTimeout:kind === 'frpc' ? +val('clientDialTimeout') : 0, tcpKeepalive:+val('tcpKeepalive'), muxKeepalive:+val('muxKeepalive'),
    tcpmuxPassthrough:kind === 'frps' && f.tcpmuxPassthrough.checked, subDomainHost:kind === 'frps' ? val('subDomainHost').trim() : '', maxPortsPerClient:kind === 'frps' ? +val('maxPortsPerClient') : 0, serverAllowedPorts:kind === 'frps' ? val('serverAllowedPorts').trim() : '', proxyBindAddr:kind === 'frps' ? val('proxyBindAddr').trim() : '', vhostHTTPTimeout:kind === 'frps' ? +val('vhostHTTPTimeout') : 0, userConnTimeout:kind === 'frps' ? +val('userConnTimeout') : 0, clientPoolCount:kind === 'frpc' ? +val('clientPoolCount') : 0, serverMaxPoolCount:kind === 'frps' ? +val('serverMaxPoolCount') : 0, transportMuxDisabled:f.transportMuxDisabled.checked});
   return result;
  }
  const type = val('type'), visitor = Protocols.isVisitor(type), paired = Protocols.hasPairingSecret(type);
  const unixTarget = type === 'tcp' && val('targetKind') === 'unix', socksTarget = type === 'tcp' && val('targetKind') === 'socks5';
  if (socksTarget) {
   if (!validSocksCredential(val('pluginUser'))) throw new Error('SOCKS5 用户名须为 1–255 个 UTF-8 字节，不能包含控制字符。');
   const password = val('pluginPassword'), hasSaved = editing.old?.pluginType === 'socks5' && editing.old?.hasPluginPassword;
   if ((password === '' && !hasSaved) || (password !== '' && !validSocksCredential(password))) throw new Error('SOCKS5 密码须为 1–255 个 UTF-8 字节，不能包含控制字符；已有密码可留空保留。');
   if (+val('bandwidthKB') > 0 && val('bandwidthMode') === 'client') throw new Error('SOCKS5 暂不支持客户端限速，请关闭限速或选择服务端限速。');
  }
  if (editing.old?.pluginType === 'socks5' && !socksTarget && !f.confirmPluginRemoval.checked) throw new Error('请确认切换目标并清除已保存的 SOCKS5 认证信息。');
  delete result.hasSecret; delete result.hasHTTPPassword; delete result.secret; delete result.httpPassword;
  delete result.hasPluginPassword; delete result.pluginPassword; delete result.clearPluginPassword;
  Object.assign(result, {name:val('name').trim(), instanceId:val('instanceId'), type, localHost:unixTarget || socksTarget ? '' : val('localHost').trim(), localPort:unixTarget || socksTarget ? 0 : +val('localPort'), unixSocketPath:unixTarget ? val('unixSocketPath').trim() : '',
   pluginType:socksTarget ? 'socks5' : '', pluginUser:socksTarget ? val('pluginUser') : '', pluginPassword:socksTarget ? val('pluginPassword') : '',
   bandwidthKB:visitor ? 0 : +val('bandwidthKB'), bandwidthMode:visitor ? '' : val('bandwidthMode'), proxyProtocolVersion:visitor || socksTarget || ['udp', 'sudp'].includes(type) ? '' : val('proxyProtocolVersion'), enabled:f.enabled.checked, useEncryption:f.useEncryption.checked, useCompression:f.useCompression.checked});
  if (editing.old?.pluginType === 'socks5' && !socksTarget) result.clearPluginPassword = true;
  const health = visitor || socksTarget ? '' : val('healthCheckType');
  if (unixTarget && health !== '' && health !== 'tcp') throw new Error('Unix 套接字仅支持连接式健康检查。');
  Object.assign(result, {healthCheckType:health, healthCheckPath:health === 'http' ? val('healthCheckPath').trim() : '',
   healthCheckTimeoutSeconds:health ? +val('healthCheckTimeoutSeconds') : 0,
   healthCheckIntervalSeconds:health ? +val('healthCheckIntervalSeconds') : 0,
   healthCheckMaxFailed:health ? +val('healthCheckMaxFailed') : 0});
  if (['tcp', 'udp'].includes(type)) result.remotePort = +val('remotePort');
  if (['http', 'https', 'tcpmux'].includes(type)) {
   result.domains = val('domains').split(',').map(s => s.trim()).filter(Boolean);
   result.subdomain = val('subdomain').trim();
  } else result.subdomain = '';
  if (['http', 'https'].includes(type)) { result.probePath = val('probePath').trim(); result.probeMethod = val('probeMethod'); }
  else if (socksTarget) { result.probePath = ''; result.probeMethod = ''; }
  result.hostHeaderRewrite = type === 'http' ? val('hostHeaderRewrite').trim() : '';
  result.locations = type === 'http' ? val('locations').split(/\r?\n/).map(s => s.trim()).filter(Boolean) : [];
  result.httpRouteByUser = type === 'http' ? val('httpRouteByUser') : '';
  result.requestHeaders = readHTTPHeaders(form, 'request');
  result.responseHeaders = readHTTPHeaders(form, 'response');
  result.httpAuthEnabled = type === 'http' && f.httpAuthEnabled.checked;
  if (type === 'http' || type === 'tcpmux') Object.assign(result, {httpUser:val('httpUser'), httpPassword:f.clearHTTPPassword.checked ? '' : val('httpPassword')});
  if (paired) {
   result.secret = val('secret');
   if (visitor) { result.serverName = val('serverName').trim(); result.serverUser = val('serverUser').trim(); }
   else result.allowUsers = val('allowUsers').split('\n').map(s => s.trim()).filter(Boolean).map(s => s === '""' ? '' : s);
  }
  if (type === 'xtcp' || type === 'xtcp-visitor') result.disableAssistedAddrs = f.disableAssistedAddrs.checked;
  if (type === 'xtcp-visitor') Object.assign(result, {protocol:val('protocol'), keepTunnelOpen:f.keepTunnelOpen.checked,
   maxRetriesAnHour:+val('maxRetriesAnHour'), minRetryInterval:+val('minRetryInterval'), fallbackTo:val('fallbackTo').trim(), fallbackTimeoutMs:+val('fallbackTimeoutMs')});
  if (type === 'tcpmux') Object.assign(result, {multiplexer:val('multiplexer'), routeByHTTPUser:val('routeByHTTPUser')});
  if (editing.old?.hasHTTPPassword && f.clearHTTPPassword.checked) result.clearHTTPPassword = true;
  return result;
 }

 return {instance, tunnel, batch, batchValue, update, value, actionClick};
})();
