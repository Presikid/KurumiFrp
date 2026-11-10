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
   <p class="muted">保存草稿不会改变运行中的进程。认证令牌留空保留原值；Linux 原生服务监听小于 1024 的端口可能需要额外权限。</p>
   <div class="form-grid">
    ${field('name', '实例名称', v.name, 'text', 'required maxlength="80"')}
    <label>实例类型<select name="kind" ${old ? 'disabled' : ''}><option value="frpc" ${v.kind === 'frpc' ? 'selected' : ''}>frpc · 客户端</option><option value="frps" ${v.kind === 'frps' ? 'selected' : ''}>frps · 服务端</option></select></label>
    ${field('host', '服务器地址 / 本机监听地址', v.host, 'text', 'required')}
    ${field('port', '连接 / 监听端口', v.port, 'number', 'required min="1" max="65535" step="1"')}
    <div class="span-2">${field('token', old?.hasToken ? '认证令牌（已保存，留空保留）' : '认证令牌（8–1024 字节）', '', 'password', `${old?.hasToken ? '' : 'required'} autocomplete="new-password"`)}</div>
    <div class="client-field">${field('user', '客户端用户名称（可选）', v.user || '', 'text', 'pattern="[A-Za-z0-9][A-Za-z0-9_.\\-]{0,63}"')}</div>
    <div class="client-field">${field('natHoleStunServer', 'NAT 穿透 STUN 地址（可选）', v.natHoleStunServer || '', 'text', 'placeholder="host:port"')}</div>
    <div class="client-field">${field('dnsServer', 'DNS 服务器 IP（可选）', v.dnsServer || '', 'text', 'placeholder="例如 1.1.1.1 或 [::1]:53"')}</div>
    <div class="client-field"><label>连接协议<select name="clientTransport">${['tcp','websocket','wss','kcp','quic'].map(protocol => `<option value="${protocol}" ${(v.clientTransport || 'tcp') === protocol ? 'selected' : ''}>${protocol.toUpperCase()}</option>`).join('')}</select></label></div>
    <div class="client-field span-2">${check('loginFailExit', '首次登录失败时退出（关闭时持续重连）', v.loginFailExit)}</div>
    <div class="client-field span-2">${check('clientTLSDisabled', '关闭客户端 TLS（WSS 和 QUIC 始终使用 TLS）', v.clientTLSDisabled)}</div>
    <div class="server-field">${field('kcpBindPort', 'KCP 监听端口（0 关闭）', v.kcpBindPort ?? 0, 'number', 'min="0" max="65535" step="1"')}</div>
    <div class="server-field">${field('quicBindPort', 'QUIC 监听端口（0 关闭）', v.quicBindPort ?? 0, 'number', 'min="0" max="65535" step="1"')}</div>
    <div class="server-field">${field('httpPort', 'HTTP 虚拟主机端口（0 关闭）', v.httpPort ?? 0, 'number', 'min="0" max="65535" step="1"')}</div>
    <div class="server-field">${field('httpsPort', 'HTTPS 虚拟主机端口（0 关闭）', v.httpsPort ?? 0, 'number', 'min="0" max="65535" step="1"')}</div>
    <div class="server-field">${field('tcpmuxHTTPConnectPort', 'TCPMUX HTTP CONNECT 端口（0 关闭）', v.tcpmuxHTTPConnectPort ?? 0, 'number', 'min="0" max="65535" step="1"')}</div>
    <div class="server-field">${field('subDomainHost', '服务端子域名后缀（可选）', v.subDomainHost || '')}</div>
    <div class="server-field span-2">${check('tcpmuxPassthrough', '启用 TCPMUX 透传（需要设置 HTTP CONNECT 端口）', v.tcpmuxPassthrough)}</div>
    <div class="server-field span-2">${check('serverAllowPlaintext', '允许不使用 TLS 的客户端连接', v.serverAllowPlaintext)}</div>
    <div class="span-2">${check('autoStart', '工作台启动时自动启动此实例', v.autoStart)}</div>
   </div>`;
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
    <label><span class="local-host-label">本地目标地址</span><input name="localHost" type="text" value="${esc(v.localHost)}" required></label>
    <label><span class="local-port-label">本地目标端口</span><input name="localPort" type="number" value="${esc(v.localPort)}" required min="1" max="65535" step="1" ${lockedPort ? 'readonly' : ''}><small class="unbound-note" hidden>负数表示仅接收回退连接，不监听本地端口。</small>${lockedPort ? '<small>当前暂不支持更改此规则的回退监听端口。</small>' : ''}</label>
    <div class="port-field">${field('remotePort', '远端端口（0 请求动态分配）', ['tcp', 'udp'].includes(v.type) ? (v.remotePort ?? '') : '', 'number', 'min="0" max="65535" step="1"')}</div>
    <div class="domain-field span-2">${field('domains', '域名（多个用逗号分隔）', (v.domains || []).join(', '))}</div>
    <div class="probe-field span-2 protocol-subgrid">
     ${field('probePath', '本地健康检查路径（仅用于诊断）', v.probePath || '', 'text', 'maxlength="512" placeholder="/（默认）"')}
     <label>HTTP 检测方法<select name="probeMethod"><option value="" ${!v.probeMethod ? 'selected' : ''}>默认 · HEAD</option><option value="HEAD" ${v.probeMethod === 'HEAD' ? 'selected' : ''}>HEAD · 只检查响应头</option><option value="GET" ${v.probeMethod === 'GET' ? 'selected' : ''}>GET · 兼容只支持 GET 的接口</option></select></label>
     <p class="small-note span-2">仅用于 KurumiFRP 本地检测，不会写入 frp 配置；响应正文不会保存或显示。</p>
    </div>
    <div class="health-check-field span-2"><label>代理自动健康检查<select name="healthCheckType"><option value="" ${!v.healthCheckType ? 'selected' : ''}>关闭</option><option value="tcp" ${v.healthCheckType === 'tcp' ? 'selected' : ''}>TCP 连接</option><option value="http" ${v.healthCheckType === 'http' ? 'selected' : ''}>HTTP 请求</option></select></label><small>失败达到设定次数后暂停代理，服务恢复后自动重新注册。</small></div>
    <div class="health-http-field span-2">${field('healthCheckPath', 'HTTP 健康检查路径', v.healthCheckPath || '/', 'text', 'maxlength="512" placeholder="/health"')}</div>
    <div class="health-check-options">${field('healthCheckTimeoutSeconds', '检查超时（秒，0 使用默认）', v.healthCheckTimeoutSeconds ?? 0, 'number', 'min="0" max="86400" step="1"')}</div>
    <div class="health-check-options">${field('healthCheckIntervalSeconds', '检查间隔（秒，0 使用默认）', v.healthCheckIntervalSeconds ?? 0, 'number', 'min="0" max="604800" step="1"')}</div>
    <div class="health-check-options">${field('healthCheckMaxFailed', '连续失败次数（0 使用默认）', v.healthCheckMaxFailed ?? 0, 'number', 'min="0" max="1000" step="1"')}</div>
    <div class="subdomain-field">${field('subdomain', '子域名（可选）', v.subdomain || '')}</div>
    <p class="subdomain-field small-note span-2">至少填写一个自定义域名或子域名；子域名需要在服务端配置后缀。</p>
    <div class="tcpmux-field"><label>多路复用器<select name="multiplexer"><option value="" ${!v.multiplexer ? 'selected' : ''}>默认 · httpconnect</option><option value="httpconnect" ${v.multiplexer === 'httpconnect' ? 'selected' : ''}>httpconnect</option></select></label></div>
    <p class="tcpmux-field small-note span-2">TCPMUX 自定义域名支持 * 和 *.example.com。</p>
    <div class="tcpmux-field">${field('httpUser', 'HTTP 认证用户名（可选）', v.httpUser || '')}</div>
    <div class="tcpmux-field">${field('httpPassword', old?.hasHTTPPassword ? 'HTTP 密码（已保存，留空保留）' : 'HTTP 密码（可选，8–1024 字节）', '', 'password', 'autocomplete="new-password"')}</div>
    <div class="tcpmux-field span-2">${field('routeByHTTPUser', '按 HTTP 用户名路由（可选）', v.routeByHTTPUser || '')}</div>
    <div class="password-clear-field span-2" hidden>${check('clearHTTPPassword', '清除已保存的 TCPMUX HTTP 密码', false)}<p class="small-note password-clear-note">勾选后保存会清除已保存的密码。</p></div>
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
    <div class="batch-proxy-protocol-field"><label>PROXY 协议<select name="proxyProtocolVersion"><option value="">关闭</option><option value="v1">v1</option><option value="v2">v2</option></select></label><small>目标服务必须支持所选版本；当前仅用于 TCP 规则。</small></div>
    ${field('localHost', '本地目标地址', '127.0.0.1', 'text', 'required')}
    <label>本地端口列表<input name="localPorts" type="text" required maxlength="4096" placeholder="例如 8000-8002,8010"></label>
    <label class="span-2">远端端口列表<input name="remotePorts" type="text" required maxlength="4096" placeholder="例如 6000-6002,6010，或填 0 动态分配"></label>
    <p class="small-note span-2">端口可用逗号和范围，固定端口按顺序一一对应。多端口名称加本地端口后缀，TCP+UDP 再加 -tcp / -udp。规则总数最多 512 条。</p>
    <p class="small-note span-2">远端 0 只支持单协议，由服务端为每条规则分配端口；TCP+UDP 成对创建须使用固定端口。草稿检查无法确认远端端口可用。</p>
    <label class="form-check span-2"><input name="enabled" type="checkbox" checked>期望启用这些规则</label>
   </div>`;
 }

 function batchValue(form) {
  const f = form.elements, val = name => f[name].value;
  return {name:val('name').trim(), instanceId:val('instanceId'), protocol:val('protocol'), localHost:val('localHost').trim(),
   localPorts:val('localPorts').trim(), remotePorts:val('remotePorts').trim(), bandwidthKB:+val('bandwidthKB'), bandwidthMode:val('bandwidthMode'), proxyProtocolVersion:val('protocol') === 'tcp' ? val('proxyProtocolVersion') : '', enabled:f.enabled.checked};
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
   visible(form, '.batch-proxy-protocol-field', f.protocol.value === 'tcp');
   return;
  }
  if (editing.kind === 'instance') {
   visible(form, '.client-field', f.kind.value === 'frpc');
   visible(form, '.server-field', f.kind.value === 'frps');
   return;
  }
  const type = f.type.value, visitor = Protocols.isVisitor(type), paired = Protocols.hasPairingSecret(type);
  form.querySelector('.visitor-transfer-note').hidden = !(editing.old && Protocols.isVisitor(editing.old.type) && visitor && f.instanceId.value !== editing.old.instanceId);
  visible(form, '.bandwidth-field', !visitor);
  visible(form, '.proxy-protocol-field', !visitor && !['udp', 'sudp'].includes(type));
  visible(form, '.port-field', ['tcp', 'udp'].includes(type));
  visible(form, '.domain-field', ['http', 'https', 'tcpmux'].includes(type));
  visible(form, '.subdomain-field', ['http', 'https', 'tcpmux'].includes(type));
  visible(form, '.probe-field', ['http', 'https'].includes(type));
  visible(form, '.health-check-field', !visitor);
  visible(form, '.health-check-options', !visitor && f.healthCheckType.value !== '');
  visible(form, '.health-http-field', !visitor && f.healthCheckType.value === 'http');
  f.healthCheckPath.required = !visitor && f.healthCheckType.value === 'http';
  visible(form, '.tcpmux-field', type === 'tcpmux');
  visible(form, '.visitor-field', visitor);
  visible(form, '.secret-field', paired);
  visible(form, '.private-proxy-field', paired && !visitor);
  visible(form, '.xtcp-field', type === 'xtcp' || type === 'xtcp-visitor');
  visible(form, '.xtcp-visitor-field', type === 'xtcp-visitor');
  const oldPassword = !!editing.old?.hasHTTPPassword;
  const clearOnTypeChange = oldPassword && type !== 'tcpmux';
  visible(form, '.password-clear-field', oldPassword);
  f.clearHTTPPassword.required = clearOnTypeChange;
  f.httpPassword.disabled = type !== 'tcpmux' || (oldPassword && f.clearHTTPPassword.checked);
  form.querySelector('.password-clear-note').textContent = clearOnTypeChange
   ? '更改协议会清除已保存的 TCPMUX 密码，请先勾选确认。'
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

 function value(form, editing) {
  const f = form.elements, val = name => f[name].value;
  const result = {...(editing.old || {}), id:editing.id};
  if (editing.kind === 'instance') {
   const kind = val('kind');
   delete result.hasToken;
   Object.assign(result, {name:val('name').trim(), kind, host:val('host').trim(), port:+val('port'), token:val('token'), autoStart:f.autoStart.checked,
    httpPort:kind === 'frps' ? +val('httpPort') : 0, httpsPort:kind === 'frps' ? +val('httpsPort') : 0,
    user:kind === 'frpc' ? val('user').trim() : '', natHoleStunServer:kind === 'frpc' ? val('natHoleStunServer').trim() : '', dnsServer:kind === 'frpc' ? val('dnsServer').trim() : '', clientTransport:kind === 'frpc' ? val('clientTransport') : '', loginFailExit:kind === 'frpc' && f.loginFailExit.checked, clientTLSDisabled:kind === 'frpc' && f.clientTLSDisabled.checked, serverAllowPlaintext:kind === 'frps' && f.serverAllowPlaintext.checked, kcpBindPort:kind === 'frps' ? +val('kcpBindPort') : 0, quicBindPort:kind === 'frps' ? +val('quicBindPort') : 0,
    tcpmuxHTTPConnectPort:kind === 'frps' ? +val('tcpmuxHTTPConnectPort') : 0,
    tcpmuxPassthrough:kind === 'frps' && f.tcpmuxPassthrough.checked, subDomainHost:kind === 'frps' ? val('subDomainHost').trim() : ''});
   return result;
  }
  const type = val('type'), visitor = Protocols.isVisitor(type), paired = Protocols.hasPairingSecret(type);
  delete result.hasSecret; delete result.hasHTTPPassword; delete result.secret; delete result.httpPassword;
  Object.assign(result, {name:val('name').trim(), instanceId:val('instanceId'), type, localHost:val('localHost').trim(), localPort:+val('localPort'),
   bandwidthKB:visitor ? 0 : +val('bandwidthKB'), bandwidthMode:visitor ? '' : val('bandwidthMode'), proxyProtocolVersion:visitor || ['udp', 'sudp'].includes(type) ? '' : val('proxyProtocolVersion'), enabled:f.enabled.checked, useEncryption:f.useEncryption.checked, useCompression:f.useCompression.checked});
  const health = visitor ? '' : val('healthCheckType');
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
  if (paired) {
   result.secret = val('secret');
   if (visitor) { result.serverName = val('serverName').trim(); result.serverUser = val('serverUser').trim(); }
   else result.allowUsers = val('allowUsers').split('\n').map(s => s.trim()).filter(Boolean).map(s => s === '""' ? '' : s);
  }
  if (type === 'xtcp' || type === 'xtcp-visitor') result.disableAssistedAddrs = f.disableAssistedAddrs.checked;
  if (type === 'xtcp-visitor') Object.assign(result, {protocol:val('protocol'), keepTunnelOpen:f.keepTunnelOpen.checked,
   maxRetriesAnHour:+val('maxRetriesAnHour'), minRetryInterval:+val('minRetryInterval'), fallbackTo:val('fallbackTo').trim(), fallbackTimeoutMs:+val('fallbackTimeoutMs')});
  if (type === 'tcpmux') Object.assign(result, {multiplexer:val('multiplexer'), httpUser:val('httpUser'), routeByHTTPUser:val('routeByHTTPUser'),
   httpPassword:f.clearHTTPPassword.checked ? '' : val('httpPassword')});
  if (editing.old?.hasHTTPPassword && f.clearHTTPPassword.checked) result.clearHTTPPassword = true;
  return result;
 }

 return {instance, tunnel, batch, batchValue, update, value};
})();
