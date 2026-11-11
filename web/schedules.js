'use strict';

window.KurumiSchedules = (() => {
 const days = ['周日','周一','周二','周三','周四','周五','周六'];
 function row(event = {day:1, minute:540, action:'start'}) {
  const hour = String(Math.floor(event.minute / 60)).padStart(2,'0');
  const minute = String(event.minute % 60).padStart(2,'0');
  return `<div class="form-grid" data-schedule-row><label>星期<select data-schedule-day>${days.map((day,index)=>`<option value="${index}" ${index===event.day?'selected':''}>${day}</option>`).join('')}</select></label><label>时间<input type="time" data-schedule-time value="${hour}:${minute}" required></label><label>操作<select data-schedule-action><option value="start" ${event.action==='start'?'selected':''}>启动</option><option value="stop" ${event.action==='stop'?'selected':''}>停止</option></select></label><div class="actions"><button type="button" data-schedule-remove>移除</button></div></div>`;
 }
 function editor(instance, plan, esc) {
  const offset = plan?.offsetMinutes ?? -new Date().getTimezoneOffset();
  const events = plan?.events || [];
  return `<h2>定时计划 · ${esc(instance.name)}</h2>
   <p class="small-note">计划是工作台级设置，仅管理员可修改；创建者账户之后被降权或删除，不会自动撤销计划。</p>
   <p class="small-note">按固定 UTC 偏移的每周事件执行，不随夏令时自动调整。工作台重启后从后续事件继续，离线期间错过的事件不会补发；时钟回拨不会重复执行已记录的时间。</p>
   <p class="small-note">启用后可能按计划停止当前运行实例。手动启停或重启（包括远程操作）会暂停计划；相关配置变更、执行失败及结果未知时也会暂停，请检查后由管理员重新启用。接受启动请求不代表网络连接成功。</p>
   <p class="small-note">保存计划不会立即改变实例，事件从下一分钟开始生效。启用的计划即使暂停也优先于“工作台启动时自动恢复”；关闭或删除计划后，原有自动恢复设置重新生效。默认偏移取自当前浏览器。</p>
   <p class="small-note">启用计划至少需要 1 个事件，每个计划最多 64 个事件。</p>
   ${plan?.reason?`<p class="error" role="status">${esc(plan.reason)}</p>`:''}
   <div class="form-grid"><label class="form-check"><input name="scheduleEnabled" type="checkbox" ${plan?.enabled?'checked':''}>启用计划</label><label>相对 UTC 固定偏移（分钟）<input name="scheduleOffset" type="number" min="-720" max="840" step="1" value="${Number(offset)}" required></label>${plan?.paused?'<label class="form-check"><input name="scheduleResume" type="checkbox">我已核对实例和计划，恢复执行</label>':''}</div>
   <div data-schedule-rows>${events.map(row).join('')}</div>
   <div class="actions"><button type="button" data-schedule-add>＋ 添加事件</button>${plan?'<button type="button" class="danger" data-schedule-delete>删除计划</button>':''}</div>`;
 }
 function value(form) {
  const rows = [...form.querySelectorAll('[data-schedule-row]')];
  if (rows.length > 64) throw new Error('每个计划最多支持 64 个事件。');
  if (form.elements.scheduleEnabled.checked && !rows.length) throw new Error('启用计划至少需要 1 个事件。');
  const events = rows.map(row => {
   const value = row.querySelector('[data-schedule-time]').value;
   const match = /^(\d{2}):(\d{2})$/.exec(value);
   if (!match) throw new Error('请为每个事件选择时间');
   return {day:Number(row.querySelector('[data-schedule-day]').value),minute:Number(match[1])*60+Number(match[2]),action:row.querySelector('[data-schedule-action]').value};
  });
  return {enabled:form.elements.scheduleEnabled.checked,resume:Boolean(form.elements.scheduleResume?.checked),offsetMinutes:Number(form.elements.scheduleOffset.value),events};
 }
 return {editor, row, value};
})();
