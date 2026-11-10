'use strict';

titles.core = '运行核心';
const coreRenderBase = render;
render = function () {
 coreRenderBase();
 if (!data || page !== 'core') return;
 const embedded = data.runtimeBackend === 'embedded';
 $('#content').innerHTML = header(embedded ? '内置核心' : '运行后端', embedded ? '内置 frp 随工作台一起更新。' : '当前工作台使用自定义运行后端。') +
  `<section class="panel"><div class="panel-content"><p>${embedded ? '当前为开发候选，部分协议、并发运行及异常恢复仍待验收。' : '客户端和服务端的可用状态请查看网络总览。'}</p></div></section>`;
};

if (location.hash === '#core') {page = 'core'; render();}
(async () => {
 const match = /^#launch=([a-f0-9]{64})$/.exec(location.hash);
 if (!match) return;
 history.replaceState(null, '', location.pathname + location.search + '#core');
 try {
  for (let attempt = 0;; attempt++) {
   try {await api('launch-login', {ticket:match[1]}); break;}
   catch (error) {
    if (error.status !== 429 || attempt >= 9) throw error;
    await new Promise(resolve => setTimeout(resolve, 500));
   }
  }
  page = 'core'; await refresh();
 } catch (error) {$('#login-error').textContent = error.message;}
})();

$('#quit-app').onclick = async () => {
 if (!confirm('退出 KurumiFRP 并停止全部托管实例？当前连接会中断，配置会保留。')) return;
 try {await api('quit', {}); showLogin(); $('#login-error').textContent = '程序已退出。双击桌面快捷方式重新打开。';}
 catch (error) {toast(error.message);}
};
