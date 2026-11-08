// Executed by browser-smoke.py in the real page, against its disposable server.
(async ({token,checkLayout = true}) => {
  const check = (condition, message) => { if (!condition) throw new Error(message); };
  const gate = () => { let release; const promise = new Promise(resolve => { release = resolve; }); return {promise, release}; };
  const clearSelection = () => { window.getSelection().removeAllRanges(); document.activeElement?.blur(); };
  const select = node => { const range = document.createRange(); range.selectNodeContents(node); window.getSelection().removeAllRanges(); window.getSelection().addRange(range); return window.getSelection().toString(); };
  const originalFetch = window.fetch;
  const realFetch = (path, options) => path === '/api/update/releases'
    ? Promise.resolve(new Response(JSON.stringify({versions:[],status:''}), {headers:{'Content-Type':'application/json'}}))
    : originalFetch(path, options);
  window.fetch = realFetch;
  try {
    navigate('tunnels'); clearSelection(); await refresh();
    const originalTunnelName = data.state.tunnels[0].name, originalInstanceName = data.state.instances[0].name;
    const reads = [];
    window.fetch = async (path, options = {}) => {
      const response = await realFetch(path, options);
      if (path === '/api/state') reads.push({status:response.status,etag:new Headers(options.headers).get('If-None-Match')});
      return response;
    };
    const table = document.querySelector('tbody');
    await refresh(true);
    check(document.querySelector('tbody') === table, 'unchanged state replaced the table DOM');
    const selection = select(table.querySelector('strong'));
    const tunnel = {...data.state.tunnels[0],name:'desktop-rdp-audited'};
    await api('save', {revision:data.revision,kind:'tunnel',value:tunnel});
    await refresh(true);
    check(document.querySelector('tbody') === table && window.getSelection().toString() === selection, 'state polling destroyed a text selection');
    clearSelection(); await refresh(true);
    check(document.querySelector('tbody') !== table && document.querySelector('tbody strong').textContent === tunnel.name, 'deferred state was not rendered after selection cleared');
    check(reads.some(read => read.status === 304 && read.etag), 'state polling did not use If-None-Match / 304');

    // Slow polls coalesce. A manual post-save refresh must then fetch new state.
    const pending = gate(), entered = gate();
    let calls = 0, active = 0, maximum = 0;
    window.fetch = async (path, options) => {
      if (path !== '/api/state') return realFetch(path, options);
      ++calls; ++active; maximum = Math.max(maximum, active);
      try {
        const response = await realFetch(path, options);
        if (calls === 1) {entered.release(); await pending.promise;}
        return response;
      } finally {--active;}
    };
    const first = refresh(true), coalesced = refresh(true);
    await entered.promise;
    const instance = {...data.state.instances[0],name:'Tokyo audited entry'};
    await api('save', {revision:data.revision,kind:'instance',value:instance});
    const manual = refresh();
    check(calls === 1, 'refresh requests overlapped while the first poll was blocked');
    pending.release(); await Promise.all([first,coalesced,manual]);
    check(calls === 2 && maximum === 1 && data.state.instances[0].name === instance.name, 'manual refresh reused a pre-save state response');

    // Logs are independent of the state ETag and keep both selection and scroll.
    let lines = Array.from({length:150}, (_,index) => `log line ${index}: local test output`);
    window.fetch = (path, options) => path.startsWith('/api/logs?')
      ? Promise.resolve(new Response(JSON.stringify({lines}), {headers:{'Content-Type':'application/json'}}))
      : realFetch(path, options);
    navigate('logs'); clearSelection(); await refresh();
    let output = document.querySelector('#log-output');
    output.scrollTop = 137;
    if (checkLayout) check(output.scrollTop === 137, 'log fixture did not create a scrollable output');
    const logSelection = select(output);
    const selectedScroll = output.scrollTop;
    lines = [...lines,'new log line after a 304 state response'];
    await refresh(true);
    check(window.getSelection().toString() === logSelection, 'log polling disturbed the selection');
    if (checkLayout) check(output.scrollTop === selectedScroll, 'log polling disturbed the scroll position');
    clearSelection(); await refresh(true);
    check(document.querySelector('#log-output') === output && output.textContent.includes(lines.at(-1)), 'logs did not refresh independently');
    if (checkLayout) check(output.scrollTop === selectedScroll, 'log updates reset the scroll position');
    output.scrollTop = 137; window.scrollTo(0,40);
    const windowY = window.scrollY;
    await api('save', {revision:data.revision,kind:'instance',value:{...data.state.instances[0],name:originalInstanceName}});
    await refresh(true);
    output = document.querySelector('#log-output');
    if (checkLayout) check(output.scrollTop === 137 && window.scrollY === windowY, 'changed state reset log or document scroll');

    // A delayed preview must be retained, not discarded when text is selected.
    const configPending = gate(), configEntered = gate();
    window.fetch = async (path, options) => {
      const response = await realFetch(path, options);
      if (!path.startsWith('/api/config?')) return response;
      const text = await response.text();
      configEntered.release(); await configPending.promise;
      return new Response(text, {status:response.status,headers:response.headers});
    };
    navigate('config'); clearSelection(); await configEntered.promise;
    select(document.querySelector('#content h1'));
    configPending.release(); await refresh(true);
    check(document.querySelector('#config-output').textContent === '正在读取…', 'delayed preview overwrote selected content');
    clearSelection(); await refresh(true);
    check(document.querySelector('#config-output').textContent.includes('redacted'), 'deferred preview was lost after a 304 response');

    // A completed old request can still arrive after logout even if aborted.
    const oldPending = gate(), oldEntered = gate();
    let holdOld = true;
    window.fetch = async (path, options) => {
      if (path !== '/api/state' || !holdOld) return realFetch(path, options);
      holdOld = false;
      const response = await realFetch(path, {...options,headers:{}});
      const text = await response.text();
      oldEntered.release(); await oldPending.promise;
      return new Response(text, {status:response.status,headers:response.headers});
    };
    const oldSuccess = refresh(true);
    await oldEntered.promise; await api('logout', {}); showLogin();
    oldPending.release(); await oldSuccess;
    check(data === null && document.querySelector('#app').hidden, 'old state response restored the app after logout');

    const deniedPending = gate(), deniedEntered = gate();
    let denyOld = true, postLoginTag = 'unobserved';
    window.fetch = async (path, options = {}) => {
      if (path === '/api/state' && denyOld) {
        denyOld = false; deniedEntered.release(); await deniedPending.promise;
        return new Response(JSON.stringify({error:'expired old session'}), {status:401,headers:{'Content-Type':'application/json'}});
      }
      if (path === '/api/state') postLoginTag = new Headers(options.headers).get('If-None-Match');
      return realFetch(path, options);
    };
    const oldFailure = refresh(true);
    await deniedEntered.promise; await api('login', {token}); await refresh();
    deniedPending.release(); await oldFailure;
    check(data !== null && !document.querySelector('#app').hidden && postLoginTag === null, 'old unauthorized response logged out the new session or reused its predecessor ETag');
    await api('save', {revision:data.revision,kind:'tunnel',value:{...data.state.tunnels[0],name:originalTunnelName}});
    await refresh();
    return 'conditional polling, selection, deferred rendering, serialized refresh, live logs, preview and auth races' + (checkLayout ? ', scroll positions' : ' (layout checks skipped)');
  } finally {
    window.fetch = originalFetch;
    clearSelection();
  }
})
