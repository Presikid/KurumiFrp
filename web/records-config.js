'use strict';

// The host owns state, requests and actions; these functions only render the two pages.
window.KurumiRecordsConfig = (() => {
 function config({data, selected, header, picker, time, esc}) {
  const history = data.history.slice().reverse();
  return header('配置中心', '查看当前草稿，管理快照和配置文件。') +
  `<div class="rc-page">
   <section class="rc-banner" aria-label="配置概览">
    <div><span class="rc-kicker">CONFIGURATION / 配置工作台</span><h2>让每一次变更都有迹可循。</h2><p>预览草稿、保存备份，需要时再恢复。</p></div>
    <div class="rc-banner-meta"><span>当前草稿</span><strong>r${data.revision}</strong><small>${history.length} 份历史快照</small></div>
   </section>
   <div class="rc-config-grid">
    <section class="panel rc-preview" aria-labelledby="config-preview-title">
     <div class="panel-title"><div><h3 id="config-preview-title">实例配置预览</h3><p>查看将为所选实例生成的配置</p></div><span class="pill accent">脱敏</span></div>
     <div class="panel-content">
      <div class="rc-preview-controls"><label>选择实例${picker()}</label><button data-action="export-toml" ${selected ? '' : 'disabled'}>导出脱敏 TOML</button></div>
      <p class="rc-note">预览中的 &lt;redacted&gt; 是占位符，不能直接用于部署。</p>
      <div class="rc-output-heading"><span>配置内容</span><small id="config-preview-status">${selected ? '正在读取…' : '尚无实例'}</small></div>
      <pre id="config-output" tabindex="0" aria-label="脱敏配置预览">${selected ? '正在读取…' : '请先创建实例。'}</pre>
     </div>
    </section>
    <section class="panel rc-history" aria-labelledby="history-title">
     <div class="panel-title"><div><h3 id="history-title">历史快照</h3><p>恢复之前保存的草稿</p></div><span class="pill">${history.length} 份</span></div>
     <div class="panel-content">
      <div class="rc-retention"><label>保留数量（0–100）<input id="history-limit" type="number" min="0" max="100" step="1" value="${data.historyLimit}" data-saved-value="${data.historyLimit}"></label><button data-action="history-settings">保存数量</button></div>
      <p class="small-note">减少数量会立即删除最早的快照；设为 0 则不保留快照。</p>
      <div class="rc-history-list">${history.length ? history.map(h => `<div class="rc-history-item"><div><strong>r${h.revision}</strong><p>${esc(h.summary)}</p><small>${time(h.at)}</small></div><button data-action="restore" data-target="${h.revision}" aria-label="恢复快照 r${h.revision}">恢复</button></div>`).join('') : '<div class="rc-empty-compact"><strong>暂无历史快照</strong><p>修改配置时，会按保留数量保存前一版本。</p></div>'}</div>
      <p class="small-note rc-history-note">恢复前须停止全部托管实例。恢复只改变草稿，不会自动重新启动实例。</p>
     </div>
    </section>
   </div>
   <section class="panel rc-webhook" aria-labelledby="webhook-title">
    <div class="panel-title"><div><h3 id="webhook-title">Webhook 通知</h3><p>可选发送实例状态和草稿变更，支持通用 JSON、Discord、Slack、ntfy、Telegram 和 Gotify，默认关闭。</p></div><button data-action="webhook-settings">通知设置</button></div>
    <div class="panel-content"><p class="small-note">仅支持公网 HTTPS，不发送配置内容、密钥或日志。通知地址仅保存在本机；导入及恢复保留本机设置，新设备需要重新设置。旧版导出文件可能包含通知地址，请按凭据保管。</p></div>
   </section>
   <section class="panel" aria-labelledby="toml-import-title">
    <div class="panel-title"><div><h3 id="toml-import-title">导入 frp TOML</h3><p>先预览可保留的配置，再追加为新实例草稿。</p></div><button data-action="import-toml">选择 TOML</button></div>
   </section>
   <section class="panel rc-backup" aria-labelledby="backup-title">
    <div class="rc-backup-symbol" aria-hidden="true">✦</div><div class="rc-backup-copy"><h3 id="backup-title">密码加密备份与恢复</h3><p>使用单独的密码保护配置、历史快照和数据密钥，适合迁移到其他设备。</p><small>建议使用至少 16 个字符的密码，并与文件分开保管。密码不会被保存，忘记后无法恢复；备份不包含 frp 核心文件和本机管理令牌。</small></div>
    <div class="rc-backup-actions"><button class="primary" data-backup-action="export">导出加密备份</button><button data-backup-action="import">恢复加密备份</button></div>
   </section>
   <div class="rc-transfer-grid">
    <section class="panel" aria-labelledby="config-export-title"><div class="panel-title"><div><h3 id="config-export-title">导出配置文件</h3><p>选择需要带走的配置或快照</p></div></div><div class="panel-content rc-form">
     <label>导出范围<select id="export-scope"><option value="all">当前配置和全部快照</option><option value="config">仅当前配置</option><option value="snapshots">仅全部快照</option><option value="snapshot">单个快照</option></select></label>
     <label id="export-snapshot-field" hidden>选择快照<select id="export-snapshot">${history.length ? history.map(h => `<option value="${h.revision}">r${h.revision} · ${esc(h.summary)}</option>`).join('') : '<option value="">暂无可导出的快照</option>'}</select></label>
     <p class="rc-note">此文件包含数据密钥和所选配置，请妥善保管。本机 Webhook 和节点设置不会导出。需要密码保护时可使用上方的加密备份。</p>
     <div><button data-action="export-data">导出配置文件</button></div>
    </div></section>
    <section class="panel" aria-labelledby="config-import-title"><div class="panel-title"><div><h3 id="config-import-title">导入配置文件</h3><p>从已有文件恢复配置和快照</p></div></div><div class="panel-content rc-form">
     <label>选择配置文件<input id="import-file" type="file" accept=".json,application/json"></label>
     <p class="rc-note">导入前须停止全部托管实例，并等待正在进行的操作结束；导入将替换当前配置与历史快照。</p>
     <div><button data-action="import-data">导入并刷新</button></div>
    </div></section>
   </div>
  </div>`;
 }

 function logs({selected, logFilter, header, picker, recent, esc}) {
  return header('运行记录', '查看实例运行输出和最近的配置变更。') +
  `<div class="rc-page">
   <section class="rc-banner rc-banner-logs" aria-label="运行记录概览">
    <div><span class="rc-kicker">ACTIVITY / 运行记录</span><h2>每一条记录，都值得留意。</h2><p>按实例查看运行输出，用关键词快速定位信息。</p></div>
    <div class="rc-banner-meta"><span>日志范围</span><strong>200</strong><small>当前运行代次最近 200 行</small></div>
   </section>
   <div class="rc-logs-grid">
    <section class="panel rc-log-panel" aria-labelledby="logs-title">
     <div class="panel-title"><div><h3 id="logs-title">实例运行日志</h3><p id="log-mode" role="status">${selected ? '正在读取…' : '等待实例'}</p></div><div class="actions"><button data-action="refresh-logs" ${selected ? '' : 'disabled'}>刷新日志</button><button data-action="export-logs" disabled>导出显示内容</button></div></div>
     <div class="panel-content">
      <div class="rc-log-controls"><label>选择实例${picker()}</label><label>筛选当前日志<input id="log-filter" type="search" value="${esc(logFilter)}" placeholder="输入关键词，不区分大小写" ${selected ? '' : 'disabled'}></label><button data-action="clear-log-filter" ${logFilter ? '' : 'hidden'}>清除筛选</button></div>
      <div class="rc-output-heading"><span>运行输出</span><small id="log-count" aria-live="polite">${selected ? '正在读取…' : '尚无实例'}</small></div>
      <pre id="log-output" tabindex="0" aria-label="实例运行日志" hidden></pre>
      <div id="log-empty" class="rc-log-empty" role="status"><span class="rc-empty-mark" aria-hidden="true">⌁</span><strong id="log-empty-title">${selected ? '正在读取日志' : '暂无可选择的实例'}</strong><p id="log-empty-detail">${selected ? '请稍候，正在获取当前实例的运行输出。' : '创建实例后，可在这里查看运行输出。'}</p><button data-action="clear-log-filter" id="log-empty-clear" hidden>清除筛选</button>${selected ? '' : '<button data-go="instances">前往运行实例</button>'}</div>
      <p class="small-note">导出包含当前显示的筛选结果，最多 200 行；日志可能包含敏感信息，请妥善保管。</p>
     </div>
    </section>
    <section class="panel rc-events" aria-labelledby="events-title"><div class="panel-title"><div><h3 id="events-title">最近配置变更</h3><p>工作台记录的最近操作</p></div></div><div class="panel-content">${recent()}</div></section>
   </div>
  </div>`;
 }

 return Object.freeze({config, logs});
})();
