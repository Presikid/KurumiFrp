'use strict';
titles.health = '链路监测';
const previousHealthRender = render;
let healthSelected = '', healthReport = null, healthBusy = false, healthError = '';
render = function () {
 previousHealthRender();
 if (data && page === 'health') mountHealth();
};
const healthLabels = {
 running:['进程运行','good'], stopped:['已停止',''], exited:['进程已退出','bad'],
 reachable:['可连接','good'], healthy:['HTTP 正常','good'], responding:['服务已响应','warn'],
 unreachable:['不可连接','bad'], unavailable:['接口不可用','warn'],
 registered:['已注册','good'], error:['注册失败','bad'], missing:['未报告','warn'],
 waiting:['等待注册','warn'], disabled:['规则停用',''], unknown:['状态未知','warn'],
 other:['其他状态','warn'], unsupported:['不适用',''], 'not-applicable':['不适用',''],
 'not-reported':['未纳入统计',''], 'not-running':['未运行',''],
 skipped:['未检测',''], cancelled:['已取消','warn'], invalid:['参数无效','bad'],
 pending:['等待检测',''], checking:['检查中','warn']
};
function healthBadge(status) {
 const [label, cls] = healthLabels[status] || [String(status || '未知'), 'warn'];
 return '<span class="pill ' + cls + '">' + esc(label) + '</span>';
}
function healthCheck(label, check) {
 return '<div class="health-row"><div><strong>' + esc(label) + '</strong>' +
  '<small>' + esc(check?.address || '') + (check?.status === 'reachable' ? ' · ' + Number(check.latencyMs || 0) + ' ms' : '') + '</small>' +
  '<small class="health-message">' + esc(check?.message || '') + '</small></div>' +
  healthBadge(check?.status) + '</div>';
}
function healthFresh() {
 return !!(healthReport && data && healthReport.instance.id === healthSelected &&
  healthReport.revision === data.revision && !healthBusy);
}
function healthView() {
 if (!healthFresh()) {
  return empty('尚无诊断结果', healthBusy ? '正在检查服务端与本地规则…' : '选择一个实例，点击开始诊断。');
 }
 const report = healthReport, summary = report.summary;
 const info = [[summary.totalRules,'关联规则'],[summary.registered,'已注册代理'],[summary.targetsReachable,'本地可响应'],[summary.targetsUnreachable,'本地不可达']]
  .map(([n,title])=>'<div class="metric"><span class="label">'+esc(title)+'</span><strong>'+Number(n)+'</strong></div>').join('');
 const processChanged = (data.runtime[healthSelected]?.state || 'stopped') !== report.runtime.state;
 const rows = report.proxies.map(proxy=>'<tr><td><strong>'+esc(proxy.name)+'</strong><small>'+esc(proxy.type)+' · '+(proxy.enabled?'期望启用':'期望停用')+'</small></td>' +
  '<td>'+healthBadge(proxy.registration)+'<small class="health-message">'+esc(proxy.registrationError || proxy.registrationStatus || '')+'</small></td>' +
  '<td>'+healthBadge(proxy.target?.status)+'<small class="health-message">'+esc(proxy.target?.message || '')+'</small></td>' +
  '<td><code>'+esc(proxy.localAddr || '—')+'</code><small>'+esc(proxy.remoteAddr || '')+'</small></td></tr>').join('');
 return '<section class="metrics health-metrics">'+info+'</section>' +
  (processChanged?'<p class="notice">检测后运行状态已改变，建议重新诊断。</p>':'') +
  '<div class="grid-two"><section class="panel"><div class="panel-title"><h3>实例与连接入口</h3></div><div class="panel-content">'+
  healthCheck('实例进程',{status:report.runtime.state,message:report.runtime.pending?'当前草稿尚未应用到运行实例':'进程状态来自本机管理器'})+
  healthCheck(report.instance.kind==='frpc'?'远端 frps 控制端口':'本机 frps 监听端口',report.endpoint)+
  healthCheck('frpc 代理状态接口',report.adminApi)+'</div></section>'+
  '<section class="panel"><div class="panel-title"><h3>检查摘要</h3></div><div class="panel-content"><p class="small-note">诊断时间：'+esc(report.checkedAt)+
  '<br>草稿修订：r'+Number(report.revision)+'<br>期望启用：'+Number(summary.enabledRules)+
  '<br>代理注册异常 / 未报告：'+(Number(summary.registrationErrors)+Number(summary.registrationMissing))+
  '<br>注册状态不明：'+Number(summary.registrationUnknown)+'<br>不支持的本地探测：'+Number(summary.targetsUnsupported)+'</p>'+
  '<div class="notice">入口 TCP 可达不代表认证成功；代理已注册不代表公网用户可访问。UDP 无可靠通用探测，Visitor 的绑定口不是后端服务。</div>'+
  '</div></section></div>'+
  '<section class="panel"><div class="panel-title"><h3>规则详情</h3><span class="muted">'+Number(summary.totalRules)+' 条</span></div>'+
  (rows?'<div class="table-wrap"><table><thead><tr><th>规则</th><th>代理注册</th><th>本地目标</th><th>运行地址</th></tr></thead><tbody>'+rows+'</tbody></table></div>':
   empty('暂无关联规则','请在连接看板新增规则后重新诊断。'))+'</section>';
}
function mountHealth() {
 const instances=data.state.instances;
 if (!instances.some(x=>x.id===healthSelected)) {healthSelected=instances[0]?.id||'';healthReport=null;}
 const instance=instances.find(x=>x.id===healthSelected);
 $('#content').innerHTML=header('链路监测中心','检查实例进程、FRP 入口、代理注册及本地目标；诊断不会修改配置或连接。')+
  '<div class="notice">frpc 的代理注册状态使用本机 Admin API。旧进程需先在「运行实例」页面重启，新启动的 frpc 才会开放受保护的本地状态接口。</div>'+
  (!instance?empty('还没有可监测的实例','先在运行实例页面创建 frpc 或 frps。'):
  '<section class="panel"><div class="panel-content health-controls">'+
  '<label>选择实例<select id="health-instance">'+instances.map(x=>'<option value="'+esc(x.id)+'" '+(x.id===healthSelected?'selected':'')+'>'+esc(x.name)+' · '+esc(x.kind)+'</option>').join('')+'</select></label>'+
  '<button class="primary" id="health-run" '+(healthBusy?'disabled':'')+'>'+(healthBusy?'诊断中…':'开始诊断')+'</button>'+
  '<button id="health-export" '+(healthFresh()?'':'disabled')+'>导出诊断 JSON</button></div>'+
  '<p class="small-note health-hint" role="status">'+esc(healthError || (healthBusy?'正在检查，请勿重复提交。':'报告仅在当前页面暂存，不包含访问凭据，但含网络地址信息。'))+'</p></section>'+
  '<div id="health-results">'+healthView()+'</div>');
 const select=$('#health-instance'), run=$('#health-run'), exportButton=$('#health-export');
 if(!select)return;
 select.onchange=()=>{healthSelected=select.value;healthReport=null;healthError='';render();};
 run.onclick=async()=>{
  if(healthBusy)return;
  const id=healthSelected;
  healthBusy=true;healthError='';healthReport=null;render();
  try {
   const result=await api('health?id='+encodeURIComponent(id));
   if(healthSelected===id)healthReport=result;
  } catch(err){healthError=err.message;}
  finally{healthBusy=false;if(data && page==='health')render();}
 };
 exportButton.onclick=()=>{
  if(!healthFresh())return;
  download('kurumi-health-'+String(healthReport.checkedAt||'report').replace(/[^0-9TZ-]/g,'_')+'.json',
   JSON.stringify(healthReport,null,2));
  toast('诊断报告已导出，请保管其中的网络地址信息');
 };
}
