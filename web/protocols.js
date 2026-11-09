'use strict';

// Shared protocol presentation for the original list and the rules page.
const Protocols = Object.freeze({
 types: ['tcp', 'udp', 'http', 'https', 'stcp', 'xtcp', 'sudp', 'tcpmux', 'stcp-visitor', 'xtcp-visitor', 'sudp-visitor'],
 isVisitor: type => ['stcp-visitor', 'xtcp-visitor', 'sudp-visitor'].includes(type),
 hasPairingSecret: type => ['stcp', 'xtcp', 'sudp', 'stcp-visitor', 'xtcp-visitor', 'sudp-visitor'].includes(type),
 supportsUnbound: type => ['stcp-visitor', 'xtcp-visitor'].includes(type),
 probeUnavailable(t) {
  if (['udp', 'sudp', 'sudp-visitor'].includes(t.type)) return 'UDP 不支持通用检测';
  if (this.supportsUnbound(t.type) && t.localPort < 0) return '此 Visitor 仅接收回退连接';
  return '';
 },
 endpoint(t) {
  if (this.supportsUnbound(t.type) && t.localPort < 0) return '仅接收回退连接';
  const host = String(t.localHost || '');
  return (host.includes(':') ? `[${host}]` : host) + ':' + t.localPort;
 },
 remote(t) {
  if (this.isVisitor(t.type)) return (t.serverUser ? t.serverUser + ' / ' : '') + (t.serverName || '');
  if (this.hasPairingSecret(t.type)) return '私密配对';
  if (['http', 'https', 'tcpmux'].includes(t.type)) {
   return [...(t.domains || []), ...(t.type === 'tcpmux' && t.subdomain ? [t.subdomain + '（子域名）'] : [])].join(', ');
  }
  return ['tcp', 'udp'].includes(t.type) && t.remotePort === 0 ? '动态分配（查看代理状态）' : t.remotePort;
 },
 matches(t, instance, query, protocol) {
  const words = [t.name, t.type, t.localHost, t.localPort, t.remotePort, ...(t.domains || []), t.subdomain, t.serverName, t.serverUser, instance?.name];
  return (!protocol || t.type === protocol) && words.join(' ').toLocaleLowerCase().includes(query.toLocaleLowerCase());
 }
});
