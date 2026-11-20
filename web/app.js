/* ==========================================================================
   KurumiFrp v0.9.4 Frontend Application (Vanilla ES2022, Zero External Deps)
   - Strict Pre-Login Authentication Gate
   - Yuimi-Chaya Layout: Kisara Full-Bleed Stage + WORKS/BLOG Editorial Grid
   - 100% Native Official fatedier/frp v1 UI Controls + Live Bidirectional TOML
   - Connection-Based frps -> frpc Remote Control & XTCP/STCP P2P Orchestrator
   ========================================================================== */
(function () {
  "use strict";

  // Strictly only the 35 user-approved Kurumi illustrations (25 rejected IDs excluded)
  // 39 Curated Sunburst 2.6 Masterpieces & 12-Frame Seamless Animated Loops (65 total archived in assets/material/sunburst_dal/)
  const SUNBURST_ASSETS = [
    { id: "s01", key: "s_01", pack: "sunburst", cat: "16:9", src: "assets/s_01.webp", title: "古典藏书阁斜阳", role: "登录首屏 / 主舞台轮播" },
    { id: "s02", key: "s_02", pack: "sunburst", cat: "16:9", src: "assets/s_02.webp", title: "放学空教室夕照", role: "主舞台轮播 · 来禅制服" },
    { id: "s03", key: "s_03", pack: "sunburst", cat: "16:9", src: "assets/s_03.webp", title: "红丝绒歌剧包厢", role: "主舞台轮播 · 神威灵装" },
    { id: "s04", key: "s_04", pack: "sunburst", cat: "16:9", src: "assets/s_04.webp", title: "猫咖午后阳光", role: "主舞台轮播 · 狂三与猫" },
    { id: "s05", key: "s_05", pack: "sunburst", cat: "16:9", src: "assets/s_05.webp", title: "月夜天台栏杆", role: "主舞台轮播 · 满月夜色" },
    { id: "s06", key: "s_06", pack: "sunburst", cat: "16:9", src: "assets/s_06.webp", title: "银河铁道站台", role: "主舞台轮播 · 暮色海站" },
    { id: "s07", key: "s_07", pack: "sunburst", cat: "16:9", src: "assets/s_07.webp", title: "烛影西洋棋局", role: "主舞台轮播 · 烛光对弈" },
    { id: "s08", key: "s_08", pack: "sunburst", cat: "16:9", src: "assets/s_08.webp", title: "天台烟花绽放", role: "主舞台轮播 · 夏祭振袖" },
    { id: "s09", key: "s_09", pack: "sunburst", cat: "16:9", src: "assets/s_09.webp", title: "水晶吊灯舞厅", role: "主舞台轮播 · 月白礼服" },
    { id: "s10", key: "s_10", pack: "sunburst", cat: "16:9", src: "assets/s_10.webp", title: "双姝黄昏祭典", role: "主舞台轮播 · 狂三与十香" },
    { id: "s11", key: "s_11", pack: "sunburst", cat: "16:9", src: "assets/s_11.webp", title: "天宫市樱道暮色", role: "主舞台轮播 · 夜刀神十香" },
    { id: "s12", key: "s_12", pack: "sunburst", cat: "16:9", src: "assets/s_12.webp", title: "白羽晴空露台", role: "主舞台轮播 · 鸢一折纸" },
    { id: "s13", key: "s_13", pack: "sunburst", cat: "banner", src: "assets/s_13.webp", title: "雪夜街角暖灯", role: "FRPS 服务端板块顶栏横幅" },
    { id: "s14", key: "s_14", pack: "sunburst", cat: "banner", src: "assets/s_14.webp", title: "黄昏天台远眺", role: "FRPC 客户端板块顶栏横幅" },
    { id: "s15", key: "s_15", pack: "sunburst", cat: "banner", src: "assets/s_15.webp", title: "星桥双姝组网", role: "跨实例远控与 P2P 板块顶栏横幅" },
    { id: "s16", key: "s_16", pack: "sunburst", cat: "banner", src: "assets/s_16.webp", title: "晴空海岸公路", role: "应用控制与画廊板块顶栏横幅" },
    { id: "s17", key: "s_17", pack: "sunburst", cat: "3:4", src: "assets/s_17.webp", title: "丝带编发清晨", role: "实例与隧道立绘卡" },
    { id: "s18", key: "s_18", pack: "sunburst", cat: "3:4", src: "assets/s_18.webp", title: "冬日红围巾", role: "实例与隧道立绘卡" },
    { id: "s19", key: "s_19", pack: "sunburst", cat: "3:4", src: "assets/s_19.webp", title: "暖阳猫耳发箍", role: "实例与隧道立绘卡" },
    { id: "s20", key: "s_20", pack: "sunburst", cat: "3:4", src: "assets/s_20.webp", title: "琉璃灯下品茗", role: "实例与隧道立绘卡" },
    { id: "s21", key: "s_21", pack: "sunburst", cat: "3:4", src: "assets/s_21.webp", title: "丝绒舞台谢幕", role: "实例与隧道立绘卡" },
    { id: "s22", key: "s_22", pack: "sunburst", cat: "3:4", src: "assets/s_22.webp", title: "美术馆画框旁", role: "实例与隧道立绘卡" },
    { id: "s23", key: "s_23", pack: "sunburst", cat: "3:4", src: "assets/s_23.webp", title: "雨后彩虹天台", role: "实例与隧道立绘卡" },
    { id: "s24", key: "s_24", pack: "sunburst", cat: "3:4", src: "assets/s_24.webp", title: "甜品店下午茶", role: "双人同框立绘卡" },
    { id: "s25", key: "s_25", pack: "sunburst", cat: "3:4", src: "assets/s_25.webp", title: "夜刀神十香·星夜礼服", role: "精灵精选立绘卡" },
    { id: "s26", key: "s_26", pack: "sunburst", cat: "3:4", src: "assets/s_26.webp", title: "鸢一折纸·冬雪白衣", role: "精灵精选立绘卡" },
    { id: "s27", key: "s_27", pack: "sunburst", cat: "3:4", src: "assets/s_27.webp", title: "四糸乃·雨滴绣球花", role: "精灵精选立绘卡" },
    { id: "s28", key: "s_28", pack: "sunburst", cat: "1:1", src: "assets/s_28.webp", title: "Q版狂三 · 歪头空列表提示", role: "空列表状态透明立绘", sticker: true },
    { id: "s29", key: "s_29", pack: "sunburst", cat: "1:1", src: "assets/s_29.webp", title: "Q版狂三 · 提灯链路巡检", role: "远控拓扑巡检透明立绘", sticker: true },
    { id: "s30", key: "s_30", pack: "sunburst", cat: "1:1", src: "assets/s_30.webp", title: "Q版狂三 · 敲键盘配置维护", role: "TOML 与方案透明立绘", sticker: true },
    { id: "s31", key: "s_31", pack: "sunburst", cat: "1:1", src: "assets/s_31.webp", title: "Q版狂三 · 连接就绪 OK", role: "登录卡片与在线透明立绘", sticker: true },
    { id: "s32", key: "s_32", pack: "sunburst", cat: "1:1", src: "assets/s_32.webp", title: "Q版狂三 · 版本与原画礼盒", role: "热更新与原画包透明立绘", sticker: true },
    { id: "s33", key: "s_33", pack: "sunburst", cat: "1:1", src: "assets/s_33.webp", title: "Q版狂三 · 蕾丝伞安全守护", role: "账户与端口安全透明立绘", sticker: true },
    { id: "s34", key: "s_34", pack: "sunburst", cat: "gif", src: "assets/s_34.webp", title: "动态主视觉 · 星宫六喰星海漂浮", role: "主舞台动态壁纸 / 画廊 GIF", animated: true },
    { id: "s35", key: "s_35", pack: "sunburst", cat: "gif", src: "assets/s_35.webp", title: "动态主视觉 · 崇宫澪花海微风", role: "主舞台动态壁纸 / 画廊 GIF", animated: true },
    { id: "s36", key: "s_36", pack: "sunburst", cat: "gif", src: "assets/s_36.webp", title: "动态Q版 · 狂三品红茶待机", role: "登录页 / 主舞台胶囊动态伴侣", animated: true },
    { id: "s37", key: "s_37", pack: "sunburst", cat: "gif", src: "assets/s_37.webp", title: "动态Q版 · 狂三撸黑猫健康在线", role: "服务端/客户端在线状态动图", animated: true },
    { id: "s38", key: "s_38", pack: "sunburst", cat: "gif", src: "assets/s_38.webp", title: "动态Q版 · 狂三敲键盘同步配置", role: "双向 TOML 编辑器与原画拉取动图", animated: true },
    { id: "s39", key: "s_39", pack: "sunburst", cat: "gif", src: "assets/s_39.webp", title: "动态Q版 · 狂三歪头等待接入", role: "远控拓扑与空隧道等待动图", animated: true }
  ];

  // Classic 35 v0.9.4 curated illustrations
  const CLASSIC_KURUMI_ASSETS = [
    { id: "02", cat: "16:9", src: "assets/k_02.webp", title: "午后花房红茶" },
    { id: "03", cat: "16:9", src: "assets/k_03.webp", title: "放学后夕阳教室" },
    { id: "06", cat: "16:9", src: "assets/k_06.webp", title: "樱花铁道口白裙" },
    { id: "07", cat: "16:9", src: "assets/k_07.webp", title: "雨夜霓虹咖啡馆" },
    { id: "08", cat: "16:9", src: "assets/k_08.webp", title: "晴光花窗纯白礼拜堂" },
    { id: "09", cat: "16:9", src: "assets/k_09.webp", title: "银月彼岸花海" },
    { id: "10", cat: "16:9", src: "assets/k_10.webp", title: "午后书桌与小黑猫" },
    { id: "12", cat: "16:9", src: "assets/k_12.webp", title: "极简双屏工作台" },
    { id: "13", cat: "16:9", src: "assets/k_13.webp", title: "晴空碧海白石露台" },
    { id: "15", cat: "16:9", src: "assets/k_15.webp", title: "清晨阳光床畔" },
    { id: "16", cat: "16:9", src: "assets/k_16.webp", title: "暮色钟楼露台" },

    { id: "17", cat: "3:4", src: "assets/k_17.webp", title: "粉绒披肩挥手Q版", sticker: true },
    { id: "23", cat: "3:4", src: "assets/k_23.webp", title: "玫瑰庭院女仆红茶" },
    { id: "25", cat: "3:4", src: "assets/k_25.webp", title: "冬夜初雪红格围巾" },
    { id: "26", cat: "3:4", src: "assets/k_26.webp", title: "碧海晴空草帽白裙" },
    { id: "27", cat: "3:4", src: "assets/k_27.webp", title: "猫耳卫衣与黑猫玩偶" },
    { id: "30", cat: "3:4", src: "assets/k_30.webp", title: "樱花鸟居绯袴巫女" },
    { id: "33", cat: "3:4", src: "assets/k_33.webp", title: "草莓蛋糕甜点围裙" },
    { id: "34", cat: "3:4", src: "assets/k_34.webp", title: "星夜漫天祈愿灯" },
    { id: "35", cat: "3:4", src: "assets/k_35.webp", title: "原画风轻提双马尾" },
    { id: "36", cat: "3:4", src: "assets/k_36.webp", title: "阳光花店玫瑰花束" },
    { id: "37", cat: "3:4", src: "assets/k_37.webp", title: "白金室内乐小提琴" },

    { id: "41", cat: "4:3", src: "assets/k_41.webp", title: "星夜红线P2P拓扑" },
    { id: "43", cat: "4:3", src: "assets/k_43.webp", title: "黄昏海景电车耳机" },
    { id: "44", cat: "4:3", src: "assets/k_44.webp", title: "阳光白木图书馆书梯" },
    { id: "45", cat: "4:3", src: "assets/k_45.webp", title: "露台马卡龙玫瑰茶歇" },
    { id: "46", cat: "4:3", src: "assets/k_46.webp", title: "夏日缘侧水手服弹珠汽水" },

    { id: "47", cat: "1:1", src: "assets/k_47.webp", title: "水彩抱枕轻甜插画" },
    { id: "51", cat: "1:1", src: "assets/k_51.webp", title: "金瞳小黑猫治愈头像" },
    { id: "53", cat: "1:1", src: "assets/k_53.webp", title: "Q版贴纸 · 怀表守护", sticker: true },
    { id: "54", cat: "1:1", src: "assets/k_54.webp", title: "Q版贴纸 · 极客敲代码", sticker: true },
    { id: "55", cat: "1:1", src: "assets/k_55.webp", title: "Q版贴纸 · 双手捧脸凝视", sticker: true },
    { id: "56", cat: "1:1", src: "assets/k_56.webp", title: "Q版贴纸 · 毛毯热可可待机", sticker: true },
    { id: "58", cat: "1:1", src: "assets/k_58.webp", title: "Q版贴纸 · 红丝线P2P直连", sticker: true },
    { id: "59", cat: "1:1", src: "assets/k_59.webp", title: "Q版贴纸 · 鼓腮离线提示", sticker: true }
  ];

  const KURUMI_ASSETS = SUNBURST_ASSETS.concat(
    CLASSIC_KURUMI_ASSETS.map((a) => Object.assign({ key: "k_" + a.id, pack: "classic" }, a))
  );
  const HERO_SLIDES = SUNBURST_ASSETS.filter((a) => a.cat === "16:9" || (a.animated && (a.key === "s_34" || a.key === "s_35")));
  const PORTRAIT_COVERS = SUNBURST_ASSETS.filter((a) => a.cat === "3:4").concat(
    CLASSIC_KURUMI_ASSETS.filter((a) => a.cat === "3:4" && !a.sticker)
  );

  const STATE = {
    authenticated: false,
    username: "admin",
    needsPasswordSetup: false,
    version: "0.9.11",
    etag: "",
    activeView: "home", // home | frps | frpc | topology | system
    slideIdx: 0,
    autoPlay: true,
    filmstripOpen: false,
    loginSlideIdx: 0,

    // Backend data
    activeConfigFile: "default.json",
    configFiles: [],
    servers: [],
    clients: [],
    runtime: {},
    core: {},
    updateProxy: "",
    systemProxy: "",
    lastUpdateCheck: null,
    panel: {
      currentHost: "127.0.0.1",
      currentPort: 9000,
      listenAddr: "127.0.0.1:9000",
      bindHost: "127.0.0.1",
      savedPort: 9000,
      customized: false,
      canHotSwap: true
    },
    hdAssets: {
      activeTier: "embedded",
      preferHD: false,
      installed: false,
      installedCount: 0,
      expectedCount: 74,
      installedBytes: 0,
      embeddedBytes: 8720000,
      packId: "sunburst_dal",
      packVersion: "0.9.4",
      sourceRepo: "Presikid/KurumiFrp",
      sourcePath: "assets/material/sunburst_dal",
      sourceRef: "main",
      cacheToken: "std-0-1",
      dimensions: {},
      downloading: false,
      progressFiles: 0,
      progressTotal: 74,
      progressBytes: 0,
      expectedBytes: 0,
      currentAsset: "",
      lastError: ""
    },

    // Selection & editing states
    selectedServerId: "",
    frpsMode: "local", // "local" | "remoteClient"
    frpsSubTab: "studio", // "studio" | "conns" | "remotes" | "logs"
    serverDraft: null,
    serverDraftDirty: false,
    srvTomlDirty: false,
    srvTomlRaw: "",
    openServerAccs: { s1: true, s2: true, s3: false, s4: false, s5: false, s6: false, s7: false },

    // Remote connected client inspected via FRPS
    remoteReqSeq: 0,
    remoteTarget: null, // { serverId, clientId }
    remoteSnapshot: null, // { summary, profile, runtime, toml, logs }
    remoteClientDraft: null,
    remoteSubTab: "tunnels", // "tunnels" | "studio" | "logs"
    remoteAllowUpstream: false,

    // Local FRPC selection
    selectedClientId: "",
    frpcSubTab: "tunnels", // "tunnels" | "studio" | "logs"
    tunnelFilter: "all", // "all" | "port" | "web" | "p2p" | "visitor"
    clientDraft: null,
    clientDraftDirty: false,
    cliTomlDirty: false,
    cliTomlRaw: "",
    openClientAccs: { c1: true, c2: false, c3: true, c4: false, c5: false },

    // Topology & P2P
    topologyTab: "mesh", // "mesh" | "p2pWizard"
    lastP2PResult: null,

    // System & Gallery
    systemTab: "schemes", // "schemes" | "updates" | "gallery" | "security"
    galleryCat: "sunburst"
  };

  let homeSlideTimer = null;
  let loginSlideTimer = null;
  let statePollTimer = null;
  let hdPollTimer = null;
  let activeLogES = null;
  let appliedCacheToken = "std-0-1";

  // ==================== Helpers ====================
  function $(id) {
    return document.getElementById(id);
  }

  function esc(v) {
    return String(v == null ? "" : v)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;")
      .replace(/'/g, "&#39;");
  }

  const SVG_EYE_CLOSED = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94"/><path d="M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19"/><path d="M14.12 14.12a3 3 0 1 1-4.24-4.24"/><line x1="1" y1="1" x2="23" y2="23"/></svg>';
  const SVG_EYE_OPEN = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/></svg>';

  function bindEyeToggleButton(btn, inputEl) {
    if (!btn || !inputEl || btn.dataset.eyeBound === "1") return;
    btn.dataset.eyeBound = "1";
    const syncIcon = () => {
      const showing = inputEl.type === "text";
      btn.classList.toggle("is-visible", showing);
      btn.innerHTML = showing ? SVG_EYE_OPEN : SVG_EYE_CLOSED;
      btn.title = showing ? "隐藏密码" : "显示密码";
      btn.setAttribute("aria-label", showing ? "隐藏密码" : "显示密码");
    };
    syncIcon();
    btn.addEventListener("click", (ev) => {
      ev.preventDefault();
      ev.stopPropagation();
      inputEl.type = inputEl.type === "password" ? "text" : "password";
      syncIcon();
      try {
        inputEl.focus({ preventScroll: true });
      } catch (_) {}
    });
  }

  function enhancePasswordEyes(root) {
    const scope = root || document;
    scope.querySelectorAll("[data-eye-for]").forEach((btn) => {
      const target = document.getElementById(btn.dataset.eyeFor) || scope.querySelector("#" + btn.dataset.eyeFor);
      if (target) bindEyeToggleButton(btn, target);
    });
    scope.querySelectorAll('input[type="password"]').forEach((inp) => {
      if (inp.closest(".yc-pass-wrap")) return;
      const wrap = document.createElement("div");
      wrap.className = "yc-pass-wrap";
      inp.parentNode.insertBefore(wrap, inp);
      wrap.appendChild(inp);
      const btn = document.createElement("button");
      btn.type = "button";
      btn.className = "yc-eye-btn";
      wrap.appendChild(btn);
      bindEyeToggleButton(btn, inp);
    });
  }

  function deepClone(obj) {
    return JSON.parse(JSON.stringify(obj == null ? null : obj));
  }

  function isoToLocalDatetimeInput(isoStr) {
    if (!isoStr) return "";
    const d = new Date(isoStr);
    if (isNaN(d.getTime())) return String(isoStr).slice(0, 16);
    return new Date(d.getTime() - d.getTimezoneOffset() * 60000).toISOString().slice(0, 16);
  }

  function pad2(n) {
    return String(n).padStart(2, "0");
  }

  function randomSecret(len) {
    const chars = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789";
    let out = "";
    for (let i = 0; i < (len || 16); i++) {
      out += chars[Math.floor(Math.random() * chars.length)];
    }
    return out;
  }

  function formatBytes(bytes) {
    const b = Number(bytes || 0);
    if (b < 1024) return b + " B";
    if (b < 1024 * 1024) return (b / 1024).toFixed(1) + " KB";
    if (b < 1024 * 1024 * 1024) return (b / (1024 * 1024)).toFixed(2) + " MB";
    return (b / (1024 * 1024 * 1024)).toFixed(2) + " GB";
  }

  function assetUrl(rawSrc) {
    const s = String(rawSrc || "");
    if (!s || s.startsWith("data:") || s.startsWith("blob:")) return s;
    const base = s.split("?")[0];
    const tok = (STATE.hdAssets && STATE.hdAssets.cacheToken) || "std";
    return base + "?v=" + encodeURIComponent(tok);
  }

  function refreshAllDOMAssetQuality() {
    const tok = (STATE.hdAssets && STATE.hdAssets.cacheToken) || "std";
    if (tok === appliedCacheToken) return;
    appliedCacheToken = tok;

    document.querySelectorAll("img").forEach((img) => {
      const raw = img.getAttribute("src") || "";
      if (raw.includes("assets/k_") || raw.includes("assets/s_")) {
        img.setAttribute("src", assetUrl(raw));
      }
    });
    document.querySelectorAll("[style*='assets/']").forEach((el) => {
      const st = el.getAttribute("style") || "";
      const next = st.replace(/assets\/(?:k|s)_[0-9a-zA-Z_]+\.(?:webp|gif|png|jpg)(?:\?[^'")\s]*)?/g, (m) => assetUrl(m));
      if (next !== st) el.setAttribute("style", next);
    });
    updateLoginWallpaper();
    if ($("homeSlidesContainer") && $("homeSlidesContainer").children.length > 0) {
      syncHomeSlideIndicators();
    }
  }

  function syncHDAssetPoll() {
    const shouldPoll = Boolean(STATE.authenticated && STATE.hdAssets && STATE.hdAssets.downloading);
    if (shouldPoll && !hdPollTimer) {
      hdPollTimer = setInterval(async () => {
        try {
          const st = await apiFetch("/api/assets/hd/status");
          const wasDownloading = STATE.hdAssets.downloading;
          STATE.hdAssets = Object.assign({}, STATE.hdAssets, st || {});
          refreshAllDOMAssetQuality();
          if (STATE.activeView === "system" && (STATE.systemTab === "updates" || STATE.systemTab === "gallery") && !isEditingActiveInput()) {
            renderActiveView();
          }
          if (!STATE.hdAssets.downloading) {
            clearInterval(hdPollTimer);
            hdPollTimer = null;
            if (wasDownloading) {
              if (STATE.hdAssets.lastError) {
                toast(STATE.hdAssets.lastError, true);
              } else if (STATE.hdAssets.installed) {
                toast("GitHub 原画质素材包拉取并校验完成，已自动启用高清原图！");
              }
              await refreshState(true);
            }
          }
        } catch (_) {}
      }, 450);
    } else if (!shouldPoll && hdPollTimer) {
      clearInterval(hdPollTimer);
      hdPollTimer = null;
    }
  }

  function coverForIndex(coverId, idx) {
    if (coverId) {
      const found = KURUMI_ASSETS.find((a) => a.key === coverId || "k_" + a.id === coverId || a.id === coverId || a.src === coverId);
      if (found) return assetUrl(found.src);
    }
    return assetUrl(PORTRAIT_COVERS[idx % PORTRAIT_COVERS.length].src);
  }

  function toast(msg, isErr) {
    const stack = $("toastStack");
    if (!stack) return;
    const el = document.createElement("div");
    el.className = "yc-toast" + (isErr ? " is-err" : "");
    el.textContent = msg;
    stack.appendChild(el);
    setTimeout(() => {
      if (el.parentNode) el.parentNode.removeChild(el);
    }, 3600);
  }

  function copyText(text, label) {
    const done = () => toast((label || "内容") + " 已复制到剪贴板");
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(done).catch(() => fallbackCopy(text, done));
    } else {
      fallbackCopy(text, done);
    }
  }

  function fallbackCopy(text, cb) {
    const ta = document.createElement("textarea");
    ta.value = text;
    ta.style.position = "fixed";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    ta.select();
    try {
      document.execCommand("copy");
      if (cb) cb();
    } catch (_e) {
      toast("复制失败，请手动选中复制", true);
    }
    document.body.removeChild(ta);
  }

  async function apiFetch(path, opts) {
    const options = Object.assign({ method: "GET", headers: {} }, opts || {});
    if (options.body && typeof options.body === "object" && !(options.body instanceof FormData)) {
      options.headers["Content-Type"] = "application/json";
      options.body = JSON.stringify(options.body);
    }
    const resp = await fetch(path, options);
    if (resp.status === 401 && path !== "/api/login" && path !== "/api/session") {
      enterLoginGate("登录会话已过期，请重新验证身份");
      throw new Error("未登录或会话已失效");
    }
    if (resp.status === 304) {
      return { notModified: true };
    }
    const ct = resp.headers.get("Content-Type") || "";
    const data = ct.includes("application/json") ? await resp.json() : await resp.text();
    if (!resp.ok) {
      const errMsg = data && typeof data === "object" && data.error ? data.error : "请求失败 (" + resp.status + ")";
      throw new Error(errMsg);
    }
    if (path === "/api/state") {
      const et = resp.headers.get("ETag");
      if (et) STATE.etag = et;
    }
    return data;
  }

  // ==================== Modal Helper (Zero alert/confirm/prompt) ====================
  function openModal(html, onMount, maxWidthPx) {
    const backdrop = $("modalBackdrop");
    const dialog = $("modalDialog");
    dialog.style.width = maxWidthPx ? "min(" + maxWidthPx + "px, 96vw)" : "min(920px, 96vw)";
    dialog.innerHTML = html;
    backdrop.hidden = false;
    const closeBtns = dialog.querySelectorAll("[data-modal-close]");
    closeBtns.forEach((b) => b.addEventListener("click", closeModal));
    if (onMount) onMount(dialog);
  }

  function closeModal() {
    const backdrop = $("modalBackdrop");
    if (backdrop) backdrop.hidden = true;
    $("modalDialog").innerHTML = "";
  }

  function confirmModal(title, desc, confirmText, onConfirm) {
    openModal(
      `<div class="yc-modal-head">
         <h3 class="yc-modal-title">${esc(title)}</h3>
         <button type="button" class="yc-btn yc-btn-outline yc-btn-sm" data-modal-close>关闭</button>
       </div>
       <p style="margin:0;font-size:14px;line-height:1.7;color:var(--yc-muted);">${esc(desc)}</p>
       <div class="yc-modal-foot">
         <button type="button" class="yc-btn yc-btn-outline" data-modal-close>取消</button>
         <button type="button" id="modalConfirmActBtn" class="yc-btn yc-btn-danger">${esc(confirmText || "确认执行")}</button>
       </div>`,
      (dlg) => {
        dlg.querySelector("#modalConfirmActBtn").addEventListener("click", async () => {
          closeModal();
          await onConfirm();
        });
      },
      480
    );
  }

  // ==================== Deterministic Client-Side Live TOML Generator ====================
  function tq(s) {
    return JSON.stringify(String(s == null ? "" : s));
  }
  function tArr(arr) {
    const clean = (arr || []).map((x) => String(x || "").trim()).filter(Boolean);
    return "[" + clean.map(tq).join(", ") + "]";
  }
  function dotKey(k) {
    const s = String(k || "").trim();
    return /^[a-zA-Z0-9_-]+$/.test(s) ? s : tq(s);
  }

  function renderServerTOMLLive(srv) {
    if (!srv) return "";
    const lines = ["# Generated by KurumiFrp (Official frps TOML v1)"];
    lines.push(`bindAddr = ${tq(srv.bindAddr || "0.0.0.0")}`);
    lines.push(`bindPort = ${Number(srv.bindPort) || 7000}`);
    if (Number(srv.kcpBindPort) > 0) lines.push(`kcpBindPort = ${Number(srv.kcpBindPort)}`);
    if (Number(srv.quicBindPort) > 0) lines.push(`quicBindPort = ${Number(srv.quicBindPort)}`);
    if (srv.proxyBindAddr) lines.push(`proxyBindAddr = ${tq(srv.proxyBindAddr.trim())}`);
    if (Number(srv.vhostHTTPPort) > 0) lines.push(`vhostHTTPPort = ${Number(srv.vhostHTTPPort)}`);
    if (Number(srv.vhostHTTPSPort) > 0) lines.push(`vhostHTTPSPort = ${Number(srv.vhostHTTPSPort)}`);
    if (Number(srv.vhostHTTPTimeout) > 0) lines.push(`vhostHTTPTimeout = ${Number(srv.vhostHTTPTimeout)}`);
    if (Number(srv.tcpmuxHTTPConnectPort) > 0) lines.push(`tcpmuxHTTPConnectPort = ${Number(srv.tcpmuxHTTPConnectPort)}`);
    if (srv.tcpmuxPassthrough) lines.push(`tcpmuxPassthrough = true`);
    if (srv.subDomainHost) lines.push(`subDomainHost = ${tq(srv.subDomainHost.trim())}`);
    if (srv.custom404Page) lines.push(`custom404Page = ${tq(srv.custom404Page.trim())}`);

    if (Number(srv.maxPortsPerClient) > 0) lines.push(`maxPortsPerClient = ${Number(srv.maxPortsPerClient)}`);
    if (Number(srv.userConnTimeout) > 0) lines.push(`userConnTimeout = ${Number(srv.userConnTimeout)}`);
    if (Number(srv.udpPacketSize) > 0) lines.push(`udpPacketSize = ${Number(srv.udpPacketSize)}`);
    if (Number(srv.natholeAnalysisDataReserveHours) > 0) {
      lines.push(`natholeAnalysisDataReserveHours = ${Number(srv.natholeAnalysisDataReserveHours)}`);
    }
    if (typeof srv.detailedErrorsToClient === "boolean") {
      lines.push(`detailedErrorsToClient = ${srv.detailedErrorsToClient}`);
    }

    if (srv.logLevel) lines.push(`log.level = ${tq(srv.logLevel)}`);
    if (Number(srv.logMaxDays) > 0) lines.push(`log.maxDays = ${Number(srv.logMaxDays)}`);
    if (srv.disablePrintColor) lines.push(`log.disablePrintColor = true`);

    if (Number(srv.maxPoolCount) > 0) lines.push(`transport.maxPoolCount = ${Number(srv.maxPoolCount)}`);
    if (typeof srv.tcpMux === "boolean") lines.push(`transport.tcpMux = ${srv.tcpMux}`);
    if (Number(srv.tcpMuxKeepaliveInterval) > 0) lines.push(`transport.tcpMuxKeepaliveInterval = ${Number(srv.tcpMuxKeepaliveInterval)}`);
    if (Number(srv.tcpKeepalive) !== 0 && srv.tcpKeepalive != null && srv.tcpKeepalive !== "") {
      lines.push(`transport.tcpKeepalive = ${Number(srv.tcpKeepalive)}`);
    }
    if (Number(srv.heartbeatTimeout) !== 0 && srv.heartbeatTimeout != null && srv.heartbeatTimeout !== "") {
      lines.push(`transport.heartbeatTimeout = ${Number(srv.heartbeatTimeout)}`);
    }
    if (Number(srv.quicKeepalivePeriod) > 0) lines.push(`transport.quic.keepalivePeriod = ${Number(srv.quicKeepalivePeriod)}`);
    if (Number(srv.quicMaxIdleTimeout) > 0) lines.push(`transport.quic.maxIdleTimeout = ${Number(srv.quicMaxIdleTimeout)}`);
    if (Number(srv.quicMaxIncomingStreams) > 0) lines.push(`transport.quic.maxIncomingStreams = ${Number(srv.quicMaxIncomingStreams)}`);
    if (srv.tlsForce) lines.push(`transport.tls.force = true`);
    if (srv.tlsCertFile) lines.push(`transport.tls.certFile = ${tq(srv.tlsCertFile.trim())}`);
    if (srv.tlsKeyFile) lines.push(`transport.tls.keyFile = ${tq(srv.tlsKeyFile.trim())}`);
    if (srv.tlsTrustedCaFile) lines.push(`transport.tls.trustedCaFile = ${tq(srv.tlsTrustedCaFile.trim())}`);

    if (Number(srv.sshGatewayBindPort) > 0) {
      lines.push(`sshTunnelGateway.bindPort = ${Number(srv.sshGatewayBindPort)}`);
      if (srv.sshPrivateKeyFile) lines.push(`sshTunnelGateway.privateKeyFile = ${tq(srv.sshPrivateKeyFile.trim())}`);
      if (srv.sshAutoGenPrivateKeyPath) lines.push(`sshTunnelGateway.autoGenPrivateKeyPath = ${tq(srv.sshAutoGenPrivateKeyPath.trim())}`);
      if (srv.sshAuthorizedKeysFile) lines.push(`sshTunnelGateway.authorizedKeysFile = ${tq(srv.sshAuthorizedKeysFile.trim())}`);
    }

    const scopes = (srv.authAdditionalScopes || []).filter(Boolean);
    if (srv.authMethod === "oidc" || (!srv.authMethod && srv.oidcIssuer)) {
      lines.push(`auth.method = "oidc"`);
      if (scopes.length) lines.push(`auth.additionalScopes = ${tArr(scopes)}`);
      if (srv.oidcIssuer) lines.push(`auth.oidc.issuer = ${tq(srv.oidcIssuer.trim())}`);
      if (srv.oidcAudience) lines.push(`auth.oidc.audience = ${tq(srv.oidcAudience.trim())}`);
      if (srv.oidcSkipExpiryCheck) lines.push(`auth.oidc.skipExpiryCheck = true`);
      if (srv.oidcSkipIssuerCheck) lines.push(`auth.oidc.skipIssuerCheck = true`);
    } else if (srv.authTokenSourceFile) {
      lines.push(`auth.method = "token"`);
      if (scopes.length) lines.push(`auth.additionalScopes = ${tArr(scopes)}`);
      lines.push(`auth.tokenSource.type = "file"`);
      lines.push(`auth.tokenSource.file.path = ${tq(srv.authTokenSourceFile.trim())}`);
    } else if (srv.authToken) {
      lines.push(`auth.method = "token"`);
      if (scopes.length) lines.push(`auth.additionalScopes = ${tArr(scopes)}`);
      lines.push(`auth.token = ${tq(srv.authToken)}`);
    }

    if (srv.extraToml && srv.extraToml.trim()) {
      lines.push("", "# Custom full-parameter TOML", srv.extraToml.trim());
    }

    if (srv.allowPorts && srv.allowPorts.trim()) {
      const parts = srv.allowPorts.split(",").map((s) => s.trim()).filter(Boolean);
      if (parts.length) lines.push("");
      parts.forEach((p) => {
        const dash = p.indexOf("-");
        if (dash > 0) {
          const a = parseInt(p.slice(0, dash), 10);
          const b = parseInt(p.slice(dash + 1), 10);
          if (a > 0 && b >= a) {
            lines.push("[[allowPorts]]", a === b ? `single = ${a}` : `start = ${a}\nend = ${b}`);
          }
        } else {
          const n = parseInt(p, 10);
          if (n > 0) lines.push("[[allowPorts]]", `single = ${n}`);
        }
      });
    }

    (srv.httpPlugins || []).forEach((hp) => {
      if (!hp || !hp.name || !hp.addr) return;
      lines.push("", "[[httpPlugins]]");
      lines.push(`name = ${tq(hp.name.trim())}`);
      lines.push(`addr = ${tq(hp.addr.trim())}`);
      lines.push(`path = ${tq((hp.path || "/handler").trim())}`);
      if (hp.ops && hp.ops.length) lines.push(`ops = ${tArr(hp.ops)}`);
      if (hp.tlsVerify) lines.push(`tlsVerify = true`);
    });

    return lines.join("\n") + "\n";
  }

  function renderClientTOMLLive(cli) {
    if (!cli) return "";
    const lines = ["# Generated by KurumiFrp (Official frpc TOML v1)"];
    lines.push(`serverAddr = ${tq(cli.serverAddr || "127.0.0.1")}`);
    lines.push(`serverPort = ${Number(cli.serverPort) || 7000}`);
    lines.push(`loginFailExit = ${Boolean(cli.loginFailExit)}`);
    if (cli.user) lines.push(`user = ${tq(cli.user.trim())}`);
    if (cli.startProxies && cli.startProxies.filter(Boolean).length) lines.push(`start = ${tArr(cli.startProxies)}`);
    if (cli.includes && cli.includes.filter(Boolean).length) lines.push(`includes = ${tArr(cli.includes)}`);
    if (cli.dnsServer) lines.push(`dnsServer = ${tq(cli.dnsServer.trim())}`);
    if (cli.natHoleStunServer) lines.push(`natHoleStunServer = ${tq(cli.natHoleStunServer.trim())}`);
    if (Number(cli.udpPacketSize) > 0) lines.push(`udpPacketSize = ${Number(cli.udpPacketSize)}`);

    let effVNetAddr = (cli.virtualNetAddr || "").trim();
    let hasVNet = Boolean(effVNetAddr);
    (cli.tunnels || []).forEach((t) => {
      if (t && t.plugin === "virtual_net") {
        hasVNet = true;
        if (!effVNetAddr && (t.mode || "proxy") !== "visitor" && t.pluginLocalAddr) {
          effVNetAddr = t.pluginLocalAddr.trim();
        }
      }
    });
    if (hasVNet) {
      if (!String(cli.extraToml || "").includes("featureGates")) {
        lines.push(`featureGates = { VirtualNet = true }`);
      }
      if (effVNetAddr) lines.push(`virtualNet.address = ${tq(effVNetAddr)}`);
    }

    if (cli.logLevel) lines.push(`log.level = ${tq(cli.logLevel)}`);
    if (Number(cli.logMaxDays) > 0) lines.push(`log.maxDays = ${Number(cli.logMaxDays)}`);
    if (cli.disablePrintColor) lines.push(`log.disablePrintColor = true`);

    const proto = (cli.protocol || "tcp").toLowerCase().trim();
    if (proto && proto !== "tcp") lines.push(`transport.protocol = ${tq(proto)}`);
    if (Number(cli.poolCount) > 0) lines.push(`transport.poolCount = ${Number(cli.poolCount)}`);
    if (Number(cli.dialServerTimeout) > 0) lines.push(`transport.dialServerTimeout = ${Number(cli.dialServerTimeout)}`);
    if (Number(cli.dialServerKeepAlive) !== 0 && cli.dialServerKeepAlive != null && cli.dialServerKeepAlive !== "") {
      lines.push(`transport.dialServerKeepAlive = ${Number(cli.dialServerKeepAlive)}`);
    }
    if (cli.connectServerLocalIP) lines.push(`transport.connectServerLocalIP = ${tq(cli.connectServerLocalIP.trim())}`);
    if (cli.proxyURL) lines.push(`transport.proxyURL = ${tq(cli.proxyURL.trim())}`);
    if (typeof cli.tcpMux === "boolean") lines.push(`transport.tcpMux = ${cli.tcpMux}`);
    if (Number(cli.tcpMuxKeepaliveInterval) > 0) lines.push(`transport.tcpMuxKeepaliveInterval = ${Number(cli.tcpMuxKeepaliveInterval)}`);
    if (Number(cli.heartbeatInterval) !== 0 && cli.heartbeatInterval != null && cli.heartbeatInterval !== "") {
      lines.push(`transport.heartbeatInterval = ${Number(cli.heartbeatInterval)}`);
    }
    if (Number(cli.heartbeatTimeout) !== 0 && cli.heartbeatTimeout != null && cli.heartbeatTimeout !== "") {
      lines.push(`transport.heartbeatTimeout = ${Number(cli.heartbeatTimeout)}`);
    }
    if (Number(cli.quicKeepalivePeriod) > 0) lines.push(`transport.quic.keepalivePeriod = ${Number(cli.quicKeepalivePeriod)}`);
    if (Number(cli.quicMaxIdleTimeout) > 0) lines.push(`transport.quic.maxIdleTimeout = ${Number(cli.quicMaxIdleTimeout)}`);
    if (Number(cli.quicMaxIncomingStreams) > 0) lines.push(`transport.quic.maxIncomingStreams = ${Number(cli.quicMaxIncomingStreams)}`);

    lines.push(`transport.tls.enable = ${Boolean(cli.tlsEnable)}`);
    if (cli.tlsEnable || proto === "wss") {
      lines.push(`transport.tls.disableCustomTLSFirstByte = ${Boolean(cli.tlsDisableCustomFirstByte)}`);
    }
    if (cli.tlsServerName) lines.push(`transport.tls.serverName = ${tq(cli.tlsServerName.trim())}`);
    if (cli.tlsCertFile) lines.push(`transport.tls.certFile = ${tq(cli.tlsCertFile.trim())}`);
    if (cli.tlsKeyFile) lines.push(`transport.tls.keyFile = ${tq(cli.tlsKeyFile.trim())}`);
    if (cli.tlsTrustedCaFile) lines.push(`transport.tls.trustedCaFile = ${tq(cli.tlsTrustedCaFile.trim())}`);

    const scopes = (cli.authAdditionalScopes || []).filter(Boolean);
    if (cli.authMethod === "oidc" || (!cli.authMethod && cli.oidcTokenEndpointURL)) {
      lines.push(`auth.method = "oidc"`);
      if (scopes.length) lines.push(`auth.additionalScopes = ${tArr(scopes)}`);
      if (cli.oidcClientId) lines.push(`auth.oidc.clientID = ${tq(cli.oidcClientId.trim())}`);
      if (cli.oidcClientSecret) lines.push(`auth.oidc.clientSecret = ${tq(cli.oidcClientSecret)}`);
      if (cli.oidcAudience) lines.push(`auth.oidc.audience = ${tq(cli.oidcAudience.trim())}`);
      if (cli.oidcScope) lines.push(`auth.oidc.scope = ${tq(cli.oidcScope.trim())}`);
      if (cli.oidcTokenEndpointURL) lines.push(`auth.oidc.tokenEndpointURL = ${tq(cli.oidcTokenEndpointURL.trim())}`);
      Object.keys(cli.oidcAdditionalParams || {}).sort().forEach((k) => {
        if (k.trim()) lines.push(`auth.oidc.additionalEndpointParams.${dotKey(k)} = ${tq(cli.oidcAdditionalParams[k])}`);
      });
    } else if (cli.authTokenSourceFile) {
      lines.push(`auth.method = "token"`);
      if (scopes.length) lines.push(`auth.additionalScopes = ${tArr(scopes)}`);
      lines.push(`auth.tokenSource.type = "file"`);
      lines.push(`auth.tokenSource.file.path = ${tq(cli.authTokenSourceFile.trim())}`);
    } else if (cli.authToken) {
      lines.push(`auth.method = "token"`);
      if (scopes.length) lines.push(`auth.additionalScopes = ${tArr(scopes)}`);
      lines.push(`auth.token = ${tq(cli.authToken)}`);
    }

    Object.keys(cli.metadatas || {}).sort().forEach((k) => {
      if (k.trim() && !k.startsWith("kurumi_")) {
        lines.push(`metadatas.${dotKey(k)} = ${tq(cli.metadatas[k])}`);
      }
    });
    if (cli.clientId) lines.push(`metadatas.client_id = ${tq(cli.clientId.trim())}`);

    if (cli.extraToml && cli.extraToml.trim()) {
      lines.push("", "# Custom global full-parameter TOML", cli.extraToml.trim());
    }

    (cli.tunnels || []).forEach((tun) => {
      if (!tun) return;
      const isEnabled = tun.enabled !== false;
      const typ = (tun.type || "tcp").toLowerCase().trim();
      if ((tun.mode || "proxy") === "visitor") {
        lines.push("", "[[visitors]]");
        lines.push(`name = ${tq(tun.name || "")}`);
        lines.push(`type = ${tq(typ)}`);
        if (!isEnabled) lines.push(`enabled = false`);
        lines.push(`serverName = ${tq(tun.serverName || "")}`);
        if (tun.serverUser) lines.push(`serverUser = ${tq(tun.serverUser.trim())}`);
        if (tun.secretKey) lines.push(`secretKey = ${tq(tun.secretKey)}`);
        lines.push(`bindAddr = ${tq(tun.localIP || "127.0.0.1")}`);
        lines.push(`bindPort = ${Number(tun.localPort || 0)}`);
        if (tun.useEncryption) lines.push(`transport.useEncryption = true`);
        if (tun.useCompression) lines.push(`transport.useCompression = true`);
        if (typ === "xtcp") {
          if (tun.visitorProtocol) lines.push(`protocol = ${tq(tun.visitorProtocol)}`);
          if (tun.keepTunnelOpen) lines.push(`keepTunnelOpen = true`);
          if (Number(tun.maxRetriesAnHour) > 0) lines.push(`maxRetriesAnHour = ${Number(tun.maxRetriesAnHour)}`);
          if (Number(tun.minRetryInterval) > 0) lines.push(`minRetryInterval = ${Number(tun.minRetryInterval)}`);
          if (tun.fallbackTo) lines.push(`fallbackTo = ${tq(tun.fallbackTo.trim())}`);
          if (Number(tun.fallbackTimeoutMs) > 0) lines.push(`fallbackTimeoutMs = ${Number(tun.fallbackTimeoutMs)}`);
        }
        if (tun.plugin === "virtual_net") {
          lines.push(`plugin.type = "virtual_net"`);
          if (tun.pluginLocalAddr) lines.push(`plugin.destinationIP = ${tq(tun.pluginLocalAddr.trim())}`);
        }
        if (tun.extraToml && tun.extraToml.trim()) lines.push(tun.extraToml.trim());
        return;
      }

      if ((typ === "tcp" || typ === "udp") && tun.portRangeLocal && tun.portRangeRemote) {
        lines.push("", `{{- range $_, $v := parseNumberRangePair ${tq(tun.portRangeLocal.trim())} ${tq(tun.portRangeRemote.trim())} }}`);
        lines.push("[[proxies]]");
        lines.push(`name = "${(tun.name || "range").replace(/"/g, '\\"')}-{{ $v.First }}"`);
        lines.push(`type = ${tq(typ)}`);
        if (!isEnabled) lines.push(`enabled = false`);
        if (!tun.plugin) {
          lines.push(`localIP = ${tq(tun.localIP || "127.0.0.1")}`);
          lines.push("localPort = {{ $v.First }}");
        }
        lines.push("remotePort = {{ $v.Second }}");
        appendProxyCommonTOML(lines, tun);
        appendProxyPluginAndExtraTOML(lines, tun);
        lines.push("{{- end }}");
        return;
      }

      lines.push("", "[[proxies]]");
      lines.push(`name = ${tq(tun.name || "")}`);
      lines.push(`type = ${tq(typ)}`);
      if (!isEnabled) lines.push(`enabled = false`);
      if (!tun.plugin) {
        lines.push(`localIP = ${tq(tun.localIP || "127.0.0.1")}`);
        if (Number(tun.localPort) > 0) lines.push(`localPort = ${Number(tun.localPort)}`);
      }
      if (typ === "tcp" || typ === "udp") {
        if (Number(tun.remotePort) > 0) lines.push(`remotePort = ${Number(tun.remotePort)}`);
      } else if (typ === "http" || typ === "https" || typ === "tcpmux") {
        if (typ === "tcpmux") lines.push(`multiplexer = ${tq(tun.multiplexer || "httpconnect")}`);
        if (tun.customDomains && tun.customDomains.filter(Boolean).length) lines.push(`customDomains = ${tArr(tun.customDomains)}`);
        if (tun.subDomain) lines.push(`subdomain = ${tq(tun.subDomain.trim())}`);
        if (typ === "http" || typ === "tcpmux") {
          if (tun.routeByHTTPUser) lines.push(`routeByHTTPUser = ${tq(tun.routeByHTTPUser.trim())}`);
          if (tun.httpUser) {
            lines.push(`httpUser = ${tq(tun.httpUser.trim())}`);
            lines.push(`httpPassword = ${tq(tun.httpPassword || "")}`);
          }
        }
        if (typ === "http") {
          if (tun.locations && tun.locations.filter(Boolean).length) lines.push(`locations = ${tArr(tun.locations)}`);
          if (tun.hostHeaderRewrite) lines.push(`hostHeaderRewrite = ${tq(tun.hostHeaderRewrite.trim())}`);
          Object.keys(tun.requestHeaders || {}).sort().forEach((k) => {
            if (k.trim()) lines.push(`requestHeaders.set.${dotKey(k)} = ${tq(tun.requestHeaders[k])}`);
          });
          Object.keys(tun.responseHeaders || {}).sort().forEach((k) => {
            if (k.trim()) lines.push(`responseHeaders.set.${dotKey(k)} = ${tq(tun.responseHeaders[k])}`);
          });
        }
      } else if (typ === "stcp" || typ === "sudp" || typ === "xtcp") {
        if (tun.secretKey) lines.push(`secretKey = ${tq(tun.secretKey)}`);
        if (tun.allowUsers && tun.allowUsers.filter(Boolean).length) lines.push(`allowUsers = ${tArr(tun.allowUsers)}`);
      }

      appendProxyCommonTOML(lines, tun);

      appendProxyPluginAndExtraTOML(lines, tun);
    });

    return lines.join("\n") + "\n";
  }

  function appendProxyPluginAndExtraTOML(lines, tun) {
    if (tun.plugin) {
      const pt = tun.plugin.trim();
      lines.push(`plugin.type = ${tq(pt)}`);
      if (pt === "static_file") {
        lines.push(`plugin.localPath = ${tq(tun.pluginLocalPath || "")}`);
        if (tun.pluginStripPrefix) lines.push(`plugin.stripPrefix = ${tq(tun.pluginStripPrefix)}`);
        if (tun.pluginUser) {
          lines.push(`plugin.httpUser = ${tq(tun.pluginUser)}`);
          lines.push(`plugin.httpPassword = ${tq(tun.pluginPassword || "")}`);
        }
      } else if (pt === "socks5") {
        if (tun.pluginUser) {
          lines.push(`plugin.username = ${tq(tun.pluginUser)}`);
          lines.push(`plugin.password = ${tq(tun.pluginPassword || "")}`);
        }
      } else if (pt === "http_proxy") {
        if (tun.pluginUser) {
          lines.push(`plugin.httpUser = ${tq(tun.pluginUser)}`);
          lines.push(`plugin.httpPassword = ${tq(tun.pluginPassword || "")}`);
        }
      } else if (pt === "unix_domain_socket") {
        lines.push(`plugin.unixPath = ${tq(tun.pluginLocalPath || "")}`);
      } else if (pt === "virtual_net") {
        // virtualNet.address is emitted globally on ClientCommonConfig per upstream fatedier/frp v1 spec
      } else if (pt === "https2http" || pt === "https2https" || pt === "http2https" || pt === "http2http" || pt === "tls2raw") {
        lines.push(`plugin.localAddr = ${tq(tun.pluginLocalAddr || "")}`);
        if (tun.pluginHostHeaderRewrite && pt !== "tls2raw") lines.push(`plugin.hostHeaderRewrite = ${tq(tun.pluginHostHeaderRewrite)}`);
        if ((pt === "https2http" || pt === "https2https" || pt === "tls2raw") && tun.pluginCrtPath) {
          lines.push(`plugin.crtPath = ${tq(tun.pluginCrtPath)}`);
          if (tun.pluginKeyPath) lines.push(`plugin.keyPath = ${tq(tun.pluginKeyPath)}`);
        }
        if ((pt === "https2http" || pt === "https2https") && typeof tun.pluginEnableHTTP2 === "boolean") {
          lines.push(`plugin.enableHTTP2 = ${tun.pluginEnableHTTP2}`);
        }
        Object.keys(tun.pluginHeaders || {}).sort().forEach((k) => {
          if (k.trim()) lines.push(`plugin.requestHeaders.set.${dotKey(k)} = ${tq(tun.pluginHeaders[k])}`);
        });
      }
    }
    if (tun.extraToml && tun.extraToml.trim()) lines.push(tun.extraToml.trim());
    if (tun.healthCheckType === "http" && tun.healthCheckHeaders) {
      Object.keys(tun.healthCheckHeaders).sort().forEach((hk) => {
        if (hk.trim()) {
          lines.push("[[proxies.healthCheck.httpHeaders]]", `name = ${tq(hk)}`, `value = ${tq(tun.healthCheckHeaders[hk])}`);
        }
      });
    }
  }

  function appendProxyCommonTOML(lines, tun) {
    const bw = (tun.bandwidthLimit || "").trim() || (Number(tun.bandwidthLimitKB) > 0 ? Number(tun.bandwidthLimitKB) + "KB" : "");
    if (bw) {
      lines.push(`transport.bandwidthLimit = ${tq(bw)}`);
      lines.push(`transport.bandwidthLimitMode = ${tq(tun.bandwidthLimitMode === "server" ? "server" : "client")}`);
    }
    if (tun.useEncryption) lines.push(`transport.useEncryption = true`);
    if (tun.useCompression) lines.push(`transport.useCompression = true`);
    if (tun.proxyProtocolVersion === "v1" || tun.proxyProtocolVersion === "v2") {
      lines.push(`transport.proxyProtocolVersion = ${tq(tun.proxyProtocolVersion)}`);
    }
    if (tun.loadBalancerGroup) {
      lines.push(`loadBalancer.group = ${tq(tun.loadBalancerGroup.trim())}`);
      if (tun.loadBalancerGroupKey) lines.push(`loadBalancer.groupKey = ${tq(tun.loadBalancerGroupKey)}`);
    }
    if (tun.healthCheckType === "tcp" || tun.healthCheckType === "http") {
      lines.push(`healthCheck.type = ${tq(tun.healthCheckType)}`);
      if (Number(tun.healthCheckTimeoutSeconds) > 0) lines.push(`healthCheck.timeoutSeconds = ${Number(tun.healthCheckTimeoutSeconds)}`);
      if (Number(tun.healthCheckMaxFailed) > 0) lines.push(`healthCheck.maxFailed = ${Number(tun.healthCheckMaxFailed)}`);
      if (Number(tun.healthCheckIntervalSeconds) > 0) lines.push(`healthCheck.intervalSeconds = ${Number(tun.healthCheckIntervalSeconds)}`);
      if (tun.healthCheckType === "http" && tun.healthCheckPath) lines.push(`healthCheck.path = ${tq(tun.healthCheckPath)}`);
    }
    Object.keys(tun.metadatas || {}).sort().forEach((k) => {
      if (k.trim()) lines.push(`metadatas.${dotKey(k)} = ${tq(tun.metadatas[k])}`);
    });
    Object.keys(tun.annotations || {}).sort().forEach((k) => {
      if (k.trim()) lines.push(`annotations.${dotKey(k)} = ${tq(tun.annotations[k])}`);
    });
  }

  // ==================== 1. Strict Login Gate & Session Boot ====================
  function updateLoginWallpaper() {
    const slide = HERO_SLIDES[STATE.loginSlideIdx % HERO_SLIDES.length];
    const bg = $("loginBgLayer");
    const cap = $("loginSlideCaption");
    if (bg && slide) bg.style.backgroundImage = `url('${assetUrl(slide.src)}')`;
    if (cap && slide) cap.textContent = `${pad2((STATE.loginSlideIdx % HERO_SLIDES.length) + 1)} · ${slide.title}`;
  }

  function enterLoginGate(errMsg) {
    STATE.authenticated = false;
    stopLogStream();
    if (statePollTimer) {
      clearInterval(statePollTimer);
      statePollTimer = null;
    }
    if (hdPollTimer) {
      clearInterval(hdPollTimer);
      hdPollTimer = null;
    }
    if (homeSlideTimer) {
      clearInterval(homeSlideTimer);
      homeSlideTimer = null;
    }
    $("appShell").hidden = true;
    $("loginGate").hidden = false;
    updateLoginWallpaper();
    if (!loginSlideTimer) {
      loginSlideTimer = setInterval(() => {
        STATE.loginSlideIdx = (STATE.loginSlideIdx + 1) % HERO_SLIDES.length;
        updateLoginWallpaper();
      }, 6500);
    }
    const errBox = $("loginErrorBox");
    if (errMsg) {
      errBox.textContent = errMsg;
      errBox.hidden = false;
    } else {
      errBox.hidden = true;
    }
  }

  async function enterAuthenticatedApp() {
    STATE.authenticated = true;
    if (loginSlideTimer) {
      clearInterval(loginSlideTimer);
      loginSlideTimer = null;
    }
    $("loginGate").hidden = true;
    $("appShell").hidden = false;
    initHomeStageDOM();
    startHomeCarouselTimer();
    STATE.etag = "";
    await refreshState(true);
    if (statePollTimer) clearInterval(statePollTimer);
    statePollTimer = setInterval(() => {
      refreshState(false).catch(() => {});
    }, 3500);
  }

  async function bootSession() {
    updateLoginWallpaper();
    try {
      const sess = await apiFetch("/api/session");
      STATE.version = sess.version || "0.9.11";
      if (sess.assetCacheToken) {
        STATE.hdAssets.cacheToken = sess.assetCacheToken;
        STATE.hdAssets.activeTier = sess.activeAssetTier || "embedded";
        refreshAllDOMAssetQuality();
      }
      STATE.needsPasswordSetup = Boolean(sess.needsPasswordSetup);
      $("loginVersionBadge").textContent = "v" + STATE.version;
      $("topVersionBadge").textContent = "v" + STATE.version;
      if (sess.defaultUsername) $("loginUsername").value = sess.defaultUsername;
      $("loginFirstRunHint").hidden = !STATE.needsPasswordSetup;

      if (sess.authenticated) {
        STATE.username = sess.username || "admin";
        await enterAuthenticatedApp();
      } else {
        enterLoginGate("");
      }
    } catch (err) {
      enterLoginGate("连接本地服务失败: " + err.message);
    }
  }

  // ==================== 2. State Synchronization (Focus-Safe) ====================
  function isEditingActiveInput() {
    const ae = document.activeElement;
    if (!ae) return false;
    const tag = ae.tagName;
    if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") {
      return $("appShell") && $("appShell").contains(ae);
    }
    return false;
  }

  async function refreshState(forceRender) {
    if (!STATE.authenticated) return;
    const headers = {};
    if (!forceRender && STATE.etag) {
      headers["If-None-Match"] = STATE.etag;
    }
    const res = await apiFetch("/api/state", { headers });
    if (res && res.notModified) return;

    STATE.version = res.version || STATE.version;
    STATE.username = res.username || STATE.username;
    STATE.needsPasswordSetup = Boolean(res.needsPasswordSetup);
    STATE.activeConfigFile = res.activeConfigFile || "default.json";
    STATE.configFiles = res.configFiles || [];
    const doc = res.config || { servers: [], clients: [] };
    STATE.servers = doc.servers || [];
    STATE.clients = doc.clients || [];
    STATE.runtime = res.runtime || {};
    STATE.core = res.core || {};
    STATE.updateProxy = res.updateProxy || "";
    STATE.systemProxy = res.systemProxy || "";
    if (res.panel) {
      STATE.panel = Object.assign({}, STATE.panel, res.panel);
    }
    if (res.hdAssets) {
      STATE.hdAssets = Object.assign({}, STATE.hdAssets, res.hdAssets);
      refreshAllDOMAssetQuality();
      syncHDAssetPoll();
    }

    if (!STATE.selectedServerId || !STATE.servers.some((s) => s.id === STATE.selectedServerId)) {
      STATE.selectedServerId = STATE.servers[0] ? STATE.servers[0].id : "";
      STATE.serverDraft = null;
      STATE.serverDraftDirty = false;
    }
    if (!STATE.selectedClientId || !STATE.clients.some((c) => c.id === STATE.selectedClientId)) {
      STATE.selectedClientId = STATE.clients[0] ? STATE.clients[0].id : "";
      STATE.clientDraft = null;
      STATE.clientDraftDirty = false;
    }

    if (!STATE.serverDraftDirty && !STATE.srvTomlDirty && STATE.selectedServerId) {
      const s = STATE.servers.find((x) => x.id === STATE.selectedServerId);
      if (s) STATE.serverDraft = deepClone(s);
    }
    if (!STATE.clientDraftDirty && !STATE.cliTomlDirty && STATE.selectedClientId) {
      const c = STATE.clients.find((x) => x.id === STATE.selectedClientId);
      if (c) STATE.clientDraft = deepClone(c);
    }

    renderTopBar();
    renderHomeMetrics();

    if (forceRender || (!isEditingActiveInput() && !STATE.srvTomlDirty && !STATE.cliTomlDirty && $("modalBackdrop").hidden)) {
      renderActiveView();
    }
  }

  function countAllRemoteClients() {
    let total = 0;
    STATE.servers.forEach((s) => {
      const rt = STATE.runtime[s.id];
      if (rt && rt.remoteClients) total += rt.remoteClients.length;
    });
    return total;
  }

  function renderTopBar() {
    $("topVersionBadge").textContent = "v" + STATE.version;
    $("topUserBadge").textContent = STATE.username;
    const activeCfg = STATE.configFiles.find((f) => f.active);
    $("topSchemeLabel").textContent = activeCfg ? activeCfg.displayName : STATE.activeConfigFile;
    $("navBadgeFrps").textContent = String(STATE.servers.length);
    $("navBadgeFrpc").textContent = String(STATE.clients.length);
    $("navBadgeRemote").textContent = String(countAllRemoteClients());

    document.querySelectorAll(".yc-nav-item[data-nav]").forEach((btn) => {
      btn.classList.toggle("is-active", btn.dataset.nav === STATE.activeView);
    });
  }

  function switchView(viewName) {
    STATE.activeView = viewName;
    stopLogStream();
    renderTopBar();
    $("viewHome").hidden = viewName !== "home";
    $("viewFrps").hidden = viewName !== "frps";
    $("viewFrpc").hidden = viewName !== "frpc";
    $("viewTopology").hidden = viewName !== "topology";
    $("viewSystem").hidden = viewName !== "system";
    renderActiveView();
  }

  function renderActiveView() {
    if (STATE.activeView === "home") {
      renderHomeMetrics();
    } else if (STATE.activeView === "frps") {
      renderFrpsWorkspace();
    } else if (STATE.activeView === "frpc") {
      renderFrpcWorkspace();
    } else if (STATE.activeView === "topology") {
      renderTopologyWorkspace();
    } else if (STATE.activeView === "system") {
      renderSystemWorkspace();
    }
  }

  // ==================== 3. View 1: HOME Full-Viewport Stage ====================
  function initHomeStageDOM() {
    const container = $("homeSlidesContainer");
    if (!container || container.children.length > 0) return;
    container.innerHTML = HERO_SLIDES.map(
      (s, i) => `<div class="yc-home-slide${i === STATE.slideIdx ? " is-active" : ""}" data-slide-idx="${i}" style="background-image:url('${assetUrl(s.src)}');"></div>`
    ).join("");
    renderHomeFilmstrip();
    syncHomeSlideIndicators();
  }

  function syncHomeSlideIndicators() {
    const idx = ((STATE.slideIdx % HERO_SLIDES.length) + HERO_SLIDES.length) % HERO_SLIDES.length;
    STATE.slideIdx = idx;
    const slide = HERO_SLIDES[idx];
    document.querySelectorAll("#homeSlidesContainer .yc-home-slide").forEach((el, i) => {
      el.classList.toggle("is-active", i === idx);
    });
    $("homeCapsuleThumb").src = assetUrl(slide.src);
    $("homeCapsuleIndex").textContent = `SLIDE ${pad2(idx + 1)} / ${pad2(HERO_SLIDES.length)}`;
    $("homeCapsuleTitle").textContent = slide.title;
    document.querySelectorAll("#homeFilmstrip .yc-film-item").forEach((el, i) => {
      el.classList.toggle("is-active", i === idx);
    });
  }

  function renderHomeFilmstrip() {
    const strip = $("homeFilmstrip");
    if (!strip) return;
    strip.innerHTML = HERO_SLIDES.map(
      (s, i) => `
      <button type="button" class="yc-film-item${i === STATE.slideIdx ? " is-active" : ""}" data-pick-slide="${i}">
        <img src="${assetUrl(s.src)}" alt="${esc(s.title)}" />
        <span>${pad2(i + 1)} · ${esc(s.title)}</span>
      </button>`
    ).join("");
    strip.querySelectorAll("[data-pick-slide]").forEach((btn) => {
      btn.addEventListener("click", () => {
        STATE.slideIdx = Number(btn.dataset.pickSlide);
        syncHomeSlideIndicators();
      });
    });
  }

  function startHomeCarouselTimer() {
    if (homeSlideTimer) clearInterval(homeSlideTimer);
    homeSlideTimer = setInterval(() => {
      if (STATE.autoPlay && STATE.activeView === "home") {
        STATE.slideIdx = (STATE.slideIdx + 1) % HERO_SLIDES.length;
        syncHomeSlideIndicators();
      }
    }, 6200);
  }

  function renderHomeMetrics() {
    const bar = $("homeMetricsBar");
    if (!bar) return;
    const runningSrvs = STATE.servers.filter((s) => STATE.runtime[s.id] && STATE.runtime[s.id].running).length;
    const runningClis = STATE.clients.filter((c) => STATE.runtime[c.id] && STATE.runtime[c.id].running).length;
    const remoteCnt = countAllRemoteClients();
    let totalTunnels = 0;
    STATE.clients.forEach((c) => {
      totalTunnels += (c.tunnels || []).filter((t) => t.enabled).length;
    });

    bar.innerHTML = `
      <button type="button" class="yc-metric-pill" data-metric-jump="frps">
        <span class="m-val">${runningSrvs} / ${STATE.servers.length}</span>
        <span class="m-lbl">在线 FRPS 服务端实例</span>
      </button>
      <button type="button" class="yc-metric-pill" data-metric-jump="frpc">
        <span class="m-val">${runningClis} / ${STATE.clients.length}</span>
        <span class="m-lbl">在线本机 FRPC 实例</span>
      </button>
      <button type="button" class="yc-metric-pill" data-metric-jump="topology">
        <span class="m-val">${remoteCnt}</span>
        <span class="m-lbl">已连接可远控客户端</span>
      </button>
      <button type="button" class="yc-metric-pill" data-metric-jump="frpc">
        <span class="m-val">${totalTunnels}</span>
        <span class="m-lbl">本机已启用隧道规则</span>
      </button>`;
    bar.querySelectorAll("[data-metric-jump]").forEach((b) => {
      b.addEventListener("click", () => switchView(b.dataset.metricJump));
    });
  }

  // ==================== 4. View 2: FRPS Server & Connected Client Remote Control ====================
  function renderFrpsWorkspace() {
    const sb = $("frpsSidebar");
    const main = $("frpsMainPane");
    if (!sb || !main) return;

    // Build Left Directory Sidebar: Local FRPS instances + Connected Remote FRPC instances underneath!
    let dirHTML = `
      <div class="yc-side-box">
        <div class="yc-side-heading">
          <span>FRPS 服务端目录 (${STATE.servers.length})</span>
          <button type="button" class="yc-btn yc-btn-outline yc-btn-sm" id="sideAddSrvBtn">+ 新建</button>
        </div>
        <div class="yc-dir-list">`;

    if (STATE.servers.length === 0) {
      dirHTML += `<div style="padding:12px 6px;font-size:12.5px;color:var(--yc-muted);">暂无服务端实例，点击右上「+ 新建」建立第一个 frps。</div>`;
    } else {
      STATE.servers.forEach((srv, idx) => {
        const rt = STATE.runtime[srv.id] || {};
        const isSel = STATE.frpsMode === "local" && STATE.selectedServerId === srv.id;
        const cover = coverForIndex(srv.coverAsset, idx);
        dirHTML += `
          <button type="button" class="yc-dir-item${isSel ? " is-active" : ""}" data-select-srv="${esc(srv.id)}">
            <img src="${cover}" alt="" class="yc-dir-thumb" />
            <div class="yc-dir-meta">
              <div class="yc-dir-title">
                <span class="yc-dot ${rt.running ? "yc-dot-ok" : ""}"></span>
                <span>${pad2(idx + 1)} · ${esc(srv.name)}</span>
              </div>
              <div class="yc-dir-sub">${esc(srv.bindAddr || "0.0.0.0")}:${srv.bindPort || 7000} · ${rt.running ? "运行中" : "已停止"}</div>
            </div>
          </button>`;

        // Connected Remote Clients under this FRPS!
        const remotes = rt.remoteClients || [];
        remotes.forEach((rc) => {
          const isRemSel =
            STATE.frpsMode === "remoteClient" &&
            STATE.remoteTarget &&
            STATE.remoteTarget.serverId === srv.id &&
            STATE.remoteTarget.clientId === rc.clientId;
          dirHTML += `
            <button type="button" class="yc-dir-item${isRemSel ? " is-active" : ""}" style="padding-left:22px;" data-select-remote="${esc(srv.id)}::${esc(rc.clientId)}">
              <img src="assets/k_47.webp" alt="" class="yc-dir-thumb" style="width:32px;height:38px;" />
              <div class="yc-dir-meta">
                <div class="yc-dir-title" style="font-size:12.5px;">
                  <span class="yc-dot ${rc.online ? "yc-dot-ok" : "yc-dot-err"}"></span>
                  <span>↳ ${esc(rc.clientName)}</span>
                </div>
                <div class="yc-dir-sub">通过连接远控 · ${rc.frpUser ? "user:" + esc(rc.frpUser) : "直连"}</div>
              </div>
            </button>`;
        });
      });
    }
    dirHTML += `</div></div>`;
    sb.innerHTML = dirHTML;

    const sideAddBtn = sb.querySelector("#sideAddSrvBtn");
    if (sideAddBtn) sideAddBtn.addEventListener("click", createBlankServer);
    sb.querySelectorAll("[data-select-srv]").forEach((btn) => {
      btn.addEventListener("click", () => {
        const nextId = btn.dataset.selectSrv;
        if (STATE.frpsMode === "local" && nextId === STATE.selectedServerId) return;
        const doSwitch = () => {
          stopLogStream();
          STATE.frpsMode = "local";
          STATE.selectedServerId = nextId;
          const found = STATE.servers.find((x) => x.id === STATE.selectedServerId);
          STATE.serverDraft = found ? deepClone(found) : null;
          STATE.serverDraftDirty = false;
          STATE.srvTomlDirty = false;
          STATE.srvTomlRaw = "";
          renderFrpsWorkspace();
        };
        if (STATE.serverDraftDirty || STATE.srvTomlDirty || (STATE.frpsMode === "remoteClient" && STATE.cliTomlDirty)) {
          confirmModal("放弃未保存更改", "当前实例存在尚未保存的表单或 TOML 修改，确定切换并放弃这些修改吗？", "放弃并切换", doSwitch);
        } else {
          doSwitch();
        }
      });
    });
    sb.querySelectorAll("[data-select-remote]").forEach((btn) => {
      btn.addEventListener("click", () => {
        const [sid, cid] = btn.dataset.selectRemote.split("::");
        if (STATE.frpsMode === "remoteClient" && STATE.remoteTarget && STATE.remoteTarget.serverId === sid && STATE.remoteTarget.clientId === cid) return;
        const doOpen = () => {
          STATE.serverDraftDirty = false;
          STATE.srvTomlDirty = false;
          STATE.srvTomlRaw = "";
          openRemoteClientConsole(sid, cid);
        };
        if (STATE.serverDraftDirty || STATE.srvTomlDirty || (STATE.frpsMode === "remoteClient" && STATE.cliTomlDirty)) {
          confirmModal("放弃未保存更改", "当前实例存在尚未保存的表单或 TOML 修改，确定切换到远控客户端并放弃这些修改吗？", "放弃并切换", doOpen);
        } else {
          doOpen();
        }
      });
    });

    if (STATE.frpsMode === "remoteClient" && STATE.remoteTarget) {
      renderRemoteClientPane(main);
      return;
    }

    if (!STATE.selectedServerId || !STATE.serverDraft) {
      main.innerHTML = `
        <div class="yc-card yc-empty-state">
          <img src="assets/s_28.webp" alt="" class="yc-empty-sticker" />
          <h3 style="margin:0 0 8px;color:var(--yc-indigo-ink);">尚未创建本机 FRPS 服务端实例</h3>
          <p style="margin:0 0 18px;font-size:13.5px;">本机有公网 IP（如云服务器 A 或公司电脑 D）时，可在此建立 frps 实例等待客户端接入并直接远控已连接客户端。</p>
          <button type="button" class="yc-btn yc-btn-primary" id="emptyCreateSrvBtn">+ 立即创建第一个 FRPS 实例</button>
        </div>`;
      main.querySelector("#emptyCreateSrvBtn").addEventListener("click", createBlankServer);
      return;
    }

    const srvIdx = Math.max(0, STATE.servers.findIndex((s) => s.id === STATE.selectedServerId));
    const srv = STATE.serverDraft;
    const rt = STATE.runtime[srv.id] || { state: "stopped", connections: [], remoteClients: [] };
    const coverSrc = coverForIndex(srv.coverAsset, srvIdx);

    let html = `
      <div class="yc-profile-hero-card">
        <div class="yc-framed-portrait">
          <img src="${coverSrc}" alt="${esc(srv.name)}" />
          <span class="yc-portrait-num">${pad2(srvIdx + 1)}</span>
        </div>
        <div>
          <div class="yc-profile-head-row">
            <div class="yc-profile-title-wrap">
              <h3 class="yc-profile-title">${esc(srv.name)}</h3>
              <span class="yc-tag ${rt.running ? "yc-tag-ok" : rt.state === "error" ? "yc-tag-danger" : ""}">${rt.running ? "运行中 · PID " + (rt.pid || "-") : rt.state === "error" ? "异常退出" : "已停止"}</span>
              ${srv.autoStart ? `<span class="yc-tag yc-tag-pink">随软件自启</span>` : ""}
              ${STATE.serverDraftDirty ? `<span class="yc-tag yc-tag-warn">有未保存修改</span>` : ""}
            </div>
            <button type="button" class="yc-btn yc-btn-outline yc-btn-sm" id="srvPickCoverBtn">更换卡片狂三立绘</button>
          </div>
          <div class="yc-profile-kv-row">
            <span>监听地址: <code>${esc(srv.bindAddr || "0.0.0.0")}:${srv.bindPort || 7000}</code></span>
            ${srv.vhostHTTPPort ? `<span>HTTP Vhost: <code>:${srv.vhostHTTPPort}</code></span>` : ""}
            ${srv.vhostHTTPSPort ? `<span>HTTPS Vhost: <code>:${srv.vhostHTTPSPort}</code></span>` : ""}
            ${srv.kcpBindPort ? `<span>KCP: <code>:${srv.kcpBindPort}</code></span>` : ""}
            ${srv.quicBindPort ? `<span>QUIC: <code>:${srv.quicBindPort}</code></span>` : ""}
            <span>已接入可远控客户端: <strong>${(rt.remoteClients || []).length}</strong> 台</span>
            <span>活跃映射连接: <strong>${(rt.connections || []).length}</strong> 条</span>
          </div>
          ${rt.lastError ? `<div class="yc-notice yc-notice-danger" style="margin:8px 0;">最近错误：${esc(rt.lastError)}</div>` : ""}
          <div class="yc-profile-actions">
            ${
              rt.running
                ? `<button type="button" class="yc-btn yc-btn-indigo" data-srv-ctrl="restart">重启实例</button>
                   <button type="button" class="yc-btn yc-btn-danger" data-srv-ctrl="stop">停止实例</button>`
                : `<button type="button" class="yc-btn yc-btn-primary" data-srv-ctrl="start">▶ 启动 FRPS 实例</button>`
            }
            <button type="button" class="yc-btn yc-btn-primary" id="srvQuickSaveBtn">保存配置${rt.running ? "并生效" : ""}</button>
            <button type="button" class="yc-btn yc-btn-outline" id="srvDupBtn">复制副本</button>
            <button type="button" class="yc-btn yc-btn-danger" id="srvDelBtn">删除实例</button>
          </div>
        </div>
      </div>

      <div class="yc-subtabs">
        <button type="button" class="yc-subtab${STATE.frpsSubTab === "studio" ? " is-active" : ""}" data-srv-tab="studio">
          全量参数配置 ⇄ 实时双向 TOML
        </button>
        <button type="button" class="yc-subtab${STATE.frpsSubTab === "remotes" ? " is-active" : ""}" data-srv-tab="remotes">
          已接入客户端远控 (${(rt.remoteClients || []).length})
        </button>
        <button type="button" class="yc-subtab${STATE.frpsSubTab === "conns" ? " is-active" : ""}" data-srv-tab="conns">
          连接管控看板 · 限流/限速/限时/开关 (${(rt.connections || []).length})
        </button>
        <button type="button" class="yc-subtab${STATE.frpsSubTab === "logs" ? " is-active" : ""}" data-srv-tab="logs">
          实时运行日志
        </button>
      </div>
      <div id="frpsSubBody"></div>`;

    main.innerHTML = html;

    main.querySelector("#srvPickCoverBtn").addEventListener("click", () => {
      openCoverPickerModal(srv.coverAsset, (picked) => {
        srv.coverAsset = picked;
        STATE.serverDraftDirty = true;
        renderFrpsWorkspace();
      });
    });
    main.querySelectorAll("[data-srv-ctrl]").forEach((b) => {
      b.addEventListener("click", async () => {
        try {
          if (STATE.serverDraftDirty) await saveCurrentServerDraft(true);
          await apiFetch("/api/servers/control", { method: "POST", body: { id: srv.id, action: b.dataset.srvCtrl } });
          toast("已执行 FRPS 动作：" + b.dataset.srvCtrl);
          await refreshState(true);
        } catch (err) {
          toast(err.message, true);
        }
      });
    });
    main.querySelector("#srvQuickSaveBtn").addEventListener("click", () => saveCurrentServerDraft(false));
    main.querySelector("#srvDupBtn").addEventListener("click", async () => {
      try {
        const out = await apiFetch("/api/servers/duplicate", { method: "POST", body: { id: srv.id } });
        STATE.selectedServerId = out.id;
        STATE.serverDraftDirty = false;
        toast("已复制 FRPS 服务端配置副本");
        await refreshState(true);
      } catch (err) {
        toast(err.message, true);
      }
    });
    main.querySelector("#srvDelBtn").addEventListener("click", () => {
      confirmModal("删除 FRPS 服务端实例", `确定删除「${srv.name}」吗？运行中的进程会先优雅停止。`, "确认删除", async () => {
        try {
          await apiFetch("/api/servers/delete", { method: "POST", body: { id: srv.id } });
          STATE.selectedServerId = "";
          STATE.serverDraft = null;
          STATE.serverDraftDirty = false;
          toast("已删除 FRPS 实例");
          await refreshState(true);
        } catch (err) {
          toast(err.message, true);
        }
      });
    });
    main.querySelectorAll("[data-srv-tab]").forEach((b) => {
      b.addEventListener("click", () => {
        stopLogStream();
        STATE.frpsSubTab = b.dataset.srvTab;
        renderFrpsWorkspace();
      });
    });

    const subBody = main.querySelector("#frpsSubBody");
    if (STATE.frpsSubTab === "studio") {
      renderServerStudioSubTab(subBody, srv);
    } else if (STATE.frpsSubTab === "remotes") {
      renderServerRemoteClientsSubTab(subBody, srv, rt.remoteClients || []);
    } else if (STATE.frpsSubTab === "conns") {
      renderServerConnsSubTab(subBody, srv, rt.connections || []);
    } else if (STATE.frpsSubTab === "logs") {
      renderInstanceLogsBox(subBody, srv.id, rt.running);
    }
  }

  function renderAccordion(accKey, openMap, stepCode, title, innerHTML) {
    const open = Boolean(openMap[accKey]);
    return `
      <div class="yc-accordion${open ? " is-open" : ""}" data-acc-key="${accKey}">
        <button type="button" class="yc-acc-header" data-acc-toggle="${accKey}">
          <span class="yc-acc-title-group">
            <span class="yc-acc-step">${esc(stepCode)}</span>
            <span class="yc-acc-title">${esc(title)}</span>
          </span>
          <span class="yc-acc-chevron">⌄</span>
        </button>
        <div class="yc-acc-body">${innerHTML}</div>
      </div>`;
  }

  function renderServerStudioSubTab(container, srv) {
    const scopes = srv.authAdditionalScopes || [];
    const plugins = srv.httpPlugins || [];
    const triBoolVal = (v) => (typeof v === "boolean" ? String(v) : "");

    const leftHTML = `
      <div class="yc-form-stack" id="srvFormStack">
        ${renderAccordion(
          "s1",
          STATE.openServerAccs,
          "01",
          "基础标识与核心监听端口 (Bind & Listeners)",
          `<div class="yc-grid-2">
             <label class="yc-field">
               <span class="yc-field-label">实例显示名称 <span class="yc-field-hint">必填</span></span>
               <input type="text" data-sf="name" value="${esc(srv.name || "")}" placeholder="云服务器 A" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">备注说明 <span class="yc-field-hint">可选</span></span>
               <input type="text" data-sf="note" value="${esc(srv.note || "")}" placeholder="公网主转发中枢" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">监听地址 <span class="yc-field-hint">bindAddr</span></span>
               <input type="text" data-sf="bindAddr" value="${esc(srv.bindAddr || "0.0.0.0")}" placeholder="0.0.0.0" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">主监听端口 <span class="yc-field-hint">bindPort</span></span>
               <input type="number" data-sf-num="bindPort" value="${srv.bindPort || 7000}" min="1" max="65535" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">KCP 监听端口 (UDP) <span class="yc-field-hint">kcpBindPort</span></span>
               <input type="number" data-sf-num="kcpBindPort" value="${srv.kcpBindPort || ""}" placeholder="留空不启用" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">QUIC 监听端口 (UDP) <span class="yc-field-hint">quicBindPort</span></span>
               <input type="number" data-sf-num="quicBindPort" value="${srv.quicBindPort || ""}" placeholder="留空不启用" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">代理对外监听地址 <span class="yc-field-hint">proxyBindAddr</span></span>
               <input type="text" data-sf="proxyBindAddr" value="${esc(srv.proxyBindAddr || "")}" placeholder="默认跟随 bindAddr" />
             </label>
           </div>
           <div class="yc-check-row">
             <label class="yc-switch-label">
               <input type="checkbox" data-sf-bool="autoStart" ${srv.autoStart ? "checked" : ""} />
               <span>KurumiFrp 启动时自动拉起此服务端实例</span>
             </label>
           </div>`
        )}

        ${renderAccordion(
          "s2",
          STATE.openServerAccs,
          "02",
          "虚拟主机与多路复用 (Vhost HTTP / HTTPS / TCPMux)",
          `<div class="yc-grid-2">
             <label class="yc-field">
               <span class="yc-field-label">HTTP 虚拟主机端口 <span class="yc-field-hint">vhostHTTPPort</span></span>
               <input type="number" data-sf-num="vhostHTTPPort" value="${srv.vhostHTTPPort || ""}" placeholder="如 80 或 8080" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">HTTPS 虚拟主机端口 <span class="yc-field-hint">vhostHTTPSPort</span></span>
               <input type="number" data-sf-num="vhostHTTPSPort" value="${srv.vhostHTTPSPort || ""}" placeholder="如 443 或 8443" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">Vhost 响应超时(秒) <span class="yc-field-hint">vhostHTTPTimeout</span></span>
               <input type="number" data-sf-num="vhostHTTPTimeout" value="${srv.vhostHTTPTimeout || ""}" placeholder="默认 60" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">TCPMux HTTP CONNECT 端口 <span class="yc-field-hint">tcpmuxHTTPConnectPort</span></span>
               <input type="number" data-sf-num="tcpmuxHTTPConnectPort" value="${srv.tcpmuxHTTPConnectPort || ""}" placeholder="如 1337" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">泛解析二级域名后缀 <span class="yc-field-hint">subDomainHost</span></span>
               <input type="text" data-sf="subDomainHost" value="${esc(srv.subDomainHost || "")}" placeholder="如 frp.example.com" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">自定义 404 页面路径 <span class="yc-field-hint">custom404Page</span></span>
               <input type="text" data-sf="custom404Page" value="${esc(srv.custom404Page || "")}" placeholder="/etc/frp/404.html" />
             </label>
           </div>
           <div class="yc-check-row">
             <label class="yc-switch-label">
               <input type="checkbox" data-sf-bool="tcpmuxPassthrough" ${srv.tcpmuxPassthrough ? "checked" : ""} />
               <span>启用 TCPMux 透传 (tcpmuxPassthrough)</span>
             </label>
           </div>`
        )}

        ${renderAccordion(
          "s3",
          STATE.openServerAccs,
          "03",
          "客户端鉴权认证 (Token / TokenSource 文件 / OIDC)",
          `<div class="yc-grid-2">
             <label class="yc-field">
               <span class="yc-field-label">鉴权方式 <span class="yc-field-hint">auth.method</span></span>
               <select data-sf="authMethod">
                 <option value="token" ${(srv.authMethod || "token") === "token" ? "selected" : ""}>token (共享密钥 / 密钥文件)</option>
                 <option value="oidc" ${srv.authMethod === "oidc" ? "selected" : ""}>oidc (OpenID Connect 令牌验证)</option>
               </select>
             </label>
             <label class="yc-field">
               <span class="yc-field-label">
                 <span>连接令牌 <span class="yc-field-hint">auth.token</span></span>
                 <button type="button" class="yc-btn yc-btn-outline yc-btn-sm" style="padding:1px 7px;font-size:11px;" id="genSrvTokenBtn">随机生成</button>
               </span>
               <input type="text" data-sf="authToken" value="${esc(srv.authToken || "")}" placeholder="客户端须一致的鉴权密钥" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">从外部文件读取 Token <span class="yc-field-hint">auth.tokenSource.file.path</span></span>
               <input type="text" data-sf="authTokenSourceFile" value="${esc(srv.authTokenSourceFile || "")}" placeholder="留空则直接使用上方 auth.token" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">OIDC 签发方地址 <span class="yc-field-hint">auth.oidc.issuer</span></span>
               <input type="text" data-sf="oidcIssuer" value="${esc(srv.oidcIssuer || "")}" placeholder="https://idp.example.com/" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">OIDC 受众标识 <span class="yc-field-hint">auth.oidc.audience</span></span>
               <input type="text" data-sf="oidcAudience" value="${esc(srv.oidcAudience || "")}" placeholder="frps-cluster" />
             </label>
           </div>
           <div class="yc-check-row">
             <label class="yc-switch-label">
               <input type="checkbox" data-sf-scope="HeartBeats" ${scopes.includes("HeartBeats") ? "checked" : ""} />
               <span>附加鉴权范围: HeartBeats (心跳包签名)</span>
             </label>
             <label class="yc-switch-label">
               <input type="checkbox" data-sf-scope="NewWorkConns" ${scopes.includes("NewWorkConns") ? "checked" : ""} />
               <span>附加鉴权范围: NewWorkConns (工作连接签名)</span>
             </label>
             <label class="yc-switch-label">
               <input type="checkbox" data-sf-bool="oidcSkipExpiryCheck" ${srv.oidcSkipExpiryCheck ? "checked" : ""} />
               <span>OIDC 跳过过期校验</span>
             </label>
             <label class="yc-switch-label">
               <input type="checkbox" data-sf-bool="oidcSkipIssuerCheck" ${srv.oidcSkipIssuerCheck ? "checked" : ""} />
               <span>OIDC 跳过签发者校验</span>
             </label>
           </div>`
        )}

        ${renderAccordion(
          "s4",
          STATE.openServerAccs,
          "04",
          "传输层、多路复用、QUIC 调优与强制 TLS 证书",
          `<div class="yc-grid-3">
             <label class="yc-field">
               <span class="yc-field-label">单隧道最大连接池 <span class="yc-field-hint">maxPoolCount</span></span>
               <input type="number" data-sf-num="maxPoolCount" value="${srv.maxPoolCount || ""}" placeholder="默认 5" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">TCP 多路复用 <span class="yc-field-hint">transport.tcpMux</span></span>
               <select data-sf-tri="tcpMux">
                 <option value="" ${triBoolVal(srv.tcpMux) === "" ? "selected" : ""}>默认 (开启)</option>
                 <option value="true" ${triBoolVal(srv.tcpMux) === "true" ? "selected" : ""}>显式开启 (true)</option>
                 <option value="false" ${triBoolVal(srv.tcpMux) === "false" ? "selected" : ""}>关闭 (false)</option>
               </select>
             </label>
             <label class="yc-field">
               <span class="yc-field-label">Mux 保活间隔(秒) <span class="yc-field-hint">tcpMuxKeepaliveInterval</span></span>
               <input type="number" data-sf-num="tcpMuxKeepaliveInterval" value="${srv.tcpMuxKeepaliveInterval || ""}" placeholder="默认 30" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">TCP Keepalive(秒) <span class="yc-field-hint">tcpKeepalive</span></span>
               <input type="number" data-sf-num="tcpKeepalive" value="${srv.tcpKeepalive || ""}" placeholder="默认 7200" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">心跳超时(秒) <span class="yc-field-hint">heartbeatTimeout</span></span>
               <input type="number" data-sf-num="heartbeatTimeout" value="${srv.heartbeatTimeout || ""}" placeholder="默认 90" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">QUIC 保活周期(秒) <span class="yc-field-hint">quic.keepalivePeriod</span></span>
               <input type="number" data-sf-num="quicKeepalivePeriod" value="${srv.quicKeepalivePeriod || ""}" placeholder="默认 10" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">QUIC 空闲超时(秒) <span class="yc-field-hint">quic.maxIdleTimeout</span></span>
               <input type="number" data-sf-num="quicMaxIdleTimeout" value="${srv.quicMaxIdleTimeout || ""}" placeholder="默认 30" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">QUIC 最大并发流 <span class="yc-field-hint">quic.maxIncomingStreams</span></span>
               <input type="number" data-sf-num="quicMaxIncomingStreams" value="${srv.quicMaxIncomingStreams || ""}" placeholder="默认 100000" />
             </label>
           </div>
           <div class="yc-grid-3" style="margin-top:12px;">
             <label class="yc-field">
               <span class="yc-field-label">TLS 证书文件 <span class="yc-field-hint">tls.certFile</span></span>
               <input type="text" data-sf="tlsCertFile" value="${esc(srv.tlsCertFile || "")}" placeholder="/path/to/server.crt" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">TLS 私钥文件 <span class="yc-field-hint">tls.keyFile</span></span>
               <input type="text" data-sf="tlsKeyFile" value="${esc(srv.tlsKeyFile || "")}" placeholder="/path/to/server.key" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">受信任客户端 CA <span class="yc-field-hint">tls.trustedCaFile</span></span>
               <input type="text" data-sf="tlsTrustedCaFile" value="${esc(srv.tlsTrustedCaFile || "")}" placeholder="启用 mTLS 时填写" />
             </label>
           </div>
           <div class="yc-check-row">
             <label class="yc-switch-label">
               <input type="checkbox" data-sf-bool="tlsForce" ${srv.tlsForce ? "checked" : ""} />
               <span>仅接受 TLS 加密客户端连接 (transport.tls.force = true)</span>
             </label>
           </div>`
        )}

        ${renderAccordion(
          "s5",
          STATE.openServerAccs,
          "05",
          "SSH 隧道网关 (sshTunnelGateway · 免安装客户端直通)",
          `<div class="yc-grid-2">
             <label class="yc-field">
               <span class="yc-field-label">SSH 网关监听端口 <span class="yc-field-hint">sshTunnelGateway.bindPort</span></span>
               <input type="number" data-sf-num="sshGatewayBindPort" value="${srv.sshGatewayBindPort || ""}" placeholder="如 2200 (留空关闭)" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">主机私钥路径 <span class="yc-field-hint">privateKeyFile</span></span>
               <input type="text" data-sf="sshPrivateKeyFile" value="${esc(srv.sshPrivateKeyFile || "")}" placeholder="可选自定义私钥" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">自动生成私钥路径 <span class="yc-field-hint">autoGenPrivateKeyPath</span></span>
               <input type="text" data-sf="sshAutoGenPrivateKeyPath" value="${esc(srv.sshAutoGenPrivateKeyPath || "")}" placeholder="./.autogen_ssh_key" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">授权公钥列表文件 <span class="yc-field-hint">authorizedKeysFile</span></span>
               <input type="text" data-sf="sshAuthorizedKeysFile" value="${esc(srv.sshAuthorizedKeysFile || "")}" placeholder="~/.ssh/authorized_keys" />
             </label>
           </div>`
        )}

        ${renderAccordion(
          "s6",
          STATE.openServerAccs,
          "06",
          "端口白名单、客户端配额与 NAT 打洞分析",
          `<div class="yc-grid-2">
             <label class="yc-field">
               <span class="yc-field-label">允许开放端口范围 <span class="yc-field-hint">allowPorts</span></span>
               <input type="text" data-sf="allowPorts" value="${esc(srv.allowPorts || "")}" placeholder="如 2000-3000,3001,9999,10000" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">单客户端最大代理数 <span class="yc-field-hint">maxPortsPerClient</span></span>
               <input type="number" data-sf-num="maxPortsPerClient" value="${srv.maxPortsPerClient || ""}" placeholder="0 表示不限制" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">用户连接等待超时(秒) <span class="yc-field-hint">userConnTimeout</span></span>
               <input type="number" data-sf-num="userConnTimeout" value="${srv.userConnTimeout || ""}" placeholder="默认 10" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">UDP 数据包大小(字节) <span class="yc-field-hint">udpPacketSize</span></span>
               <input type="number" data-sf-num="udpPacketSize" value="${srv.udpPacketSize || ""}" placeholder="默认 1500" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">打洞分析保留时长(小时) <span class="yc-field-hint">natholeAnalysisDataReserveHours</span></span>
               <input type="number" data-sf-num="natholeAnalysisDataReserveHours" value="${srv.natholeAnalysisDataReserveHours || ""}" placeholder="默认 168" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">向客户端返回详细错误 <span class="yc-field-hint">detailedErrorsToClient</span></span>
               <select data-sf-tri="detailedErrorsToClient">
                 <option value="" ${triBoolVal(srv.detailedErrorsToClient) === "" ? "selected" : ""}>默认 (true)</option>
                 <option value="true" ${triBoolVal(srv.detailedErrorsToClient) === "true" ? "selected" : ""}>开启 (true)</option>
                 <option value="false" ${triBoolVal(srv.detailedErrorsToClient) === "false" ? "selected" : ""}>隐藏内部细节 (false)</option>
               </select>
             </label>
           </div>`
        )}

        ${renderAccordion(
          "s7",
          STATE.openServerAccs,
          "07",
          "日志级别、自定义 HTTP Server Plugins 与附加 TOML",
          `<div class="yc-grid-3">
             <label class="yc-field">
               <span class="yc-field-label">日志级别 <span class="yc-field-hint">log.level</span></span>
               <select data-sf="logLevel">
                 <option value="" ${!srv.logLevel ? "selected" : ""}>默认 (info)</option>
                 <option value="trace" ${srv.logLevel === "trace" ? "selected" : ""}>trace</option>
                 <option value="debug" ${srv.logLevel === "debug" ? "selected" : ""}>debug</option>
                 <option value="info" ${srv.logLevel === "info" ? "selected" : ""}>info</option>
                 <option value="warn" ${srv.logLevel === "warn" ? "selected" : ""}>warn</option>
                 <option value="error" ${srv.logLevel === "error" ? "selected" : ""}>error</option>
               </select>
             </label>
             <label class="yc-field">
               <span class="yc-field-label">日志保留天数 <span class="yc-field-hint">log.maxDays</span></span>
               <input type="number" data-sf-num="logMaxDays" value="${srv.logMaxDays || ""}" placeholder="默认 3" />
             </label>
             <label class="yc-field" style="justify-content:flex-end;">
               <label class="yc-switch-label">
                 <input type="checkbox" data-sf-bool="disablePrintColor" ${srv.disablePrintColor ? "checked" : ""} />
                 <span>禁用终端颜色码</span>
               </label>
             </label>
           </div>

           <div style="margin-top:16px;padding-top:14px;border-top:1px solid var(--yc-border);">
             <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:10px;">
               <strong style="font-size:13px;">自定义服务端 HTTP 插件 ([[httpPlugins]])</strong>
               <button type="button" class="yc-btn yc-btn-outline yc-btn-sm" id="addSrvHttpPluginBtn">+ 添加 HTTP 插件</button>
             </div>
             <div id="srvHttpPluginsList" style="display:flex;flex-direction:column;gap:10px;">
               ${
                 plugins.length === 0
                   ? `<div style="font-size:12px;color:var(--yc-muted);">KurumiFrp 运行时已自动挂载内置连接管控与远控握手插件，如有额外外部鉴权服务可在此追加。</div>`
                   : plugins
                       .map(
                         (hp, idx) => `
                   <div style="padding:10px;border:1px solid var(--yc-border);border-radius:8px;background:#faf9fd;">
                     <div class="yc-grid-3">
                       <input type="text" placeholder="插件名称" data-hp-idx="${idx}" data-hp-k="name" value="${esc(hp.name || "")}" />
                       <input type="text" placeholder="127.0.0.1:9001" data-hp-idx="${idx}" data-hp-k="addr" value="${esc(hp.addr || "")}" />
                       <div style="display:flex;gap:6px;">
                         <input type="text" placeholder="/handler" data-hp-idx="${idx}" data-hp-k="path" value="${esc(hp.path || "/handler")}" />
                         <button type="button" class="yc-btn yc-btn-danger yc-btn-sm" data-hp-del="${idx}">删</button>
                       </div>
                     </div>
                     <div class="yc-check-row" style="margin-top:6px;font-size:12px;">
                       ${["Login", "NewProxy", "CloseProxy", "Ping", "NewWorkConn", "NewUserConn"]
                         .map(
                           (op) => `
                         <label class="yc-switch-label" style="font-size:12px;">
                           <input type="checkbox" data-hp-op="${idx}::${op}" ${(hp.ops || []).includes(op) ? "checked" : ""} />
                           <span>${op}</span>
                         </label>`
                         )
                         .join("")}
                     </div>
                   </div>`
                       )
                       .join("")
               }
             </div>
           </div>

           <label class="yc-field" style="margin-top:14px;">
             <span class="yc-field-label">自定义附加 TOML 原文 <span class="yc-field-hint">extraToml</span></span>
             <textarea rows="3" data-sf="extraToml" placeholder="# 可填写任意实验性或自定义顶层参数">${esc(srv.extraToml || "")}</textarea>
           </label>`
        )}
      </div>`;

    const liveToml = STATE.srvTomlDirty && STATE.srvTomlRaw ? STATE.srvTomlRaw : renderServerTOMLLive(srv);
    const rightHTML = `
      <div class="yc-toml-studio">
        <div class="yc-toml-head">
          <div class="yc-toml-title">
            <span class="yc-dot yc-dot-ok"></span>
            <span>frps.toml · 双向实时代码编辑器</span>
          </div>
          <div style="display:flex;gap:6px;flex-wrap:wrap;">
            <button type="button" class="yc-btn yc-btn-ghost-dark yc-btn-sm" id="srvTomlCopyBtn">复制</button>
            <button type="button" class="yc-btn yc-btn-ghost-dark yc-btn-sm" id="srvTomlParseBtn" title="将下方修改后的 TOML 解析同步回左侧表单">← 解析同步至表单</button>
            <button type="button" class="yc-btn yc-btn-primary yc-btn-sm" id="srvTomlSaveBtn">保存并应用 TOML</button>
          </div>
        </div>
        <textarea id="srvTomlTextarea" class="yc-toml-editor" spellcheck="false">${esc(liveToml)}</textarea>
        <div class="yc-toml-foot">
          <span>修改左侧任一控件会自动实时更新此文件；也可直接编辑代码并一键反向同步至 UI。</span>
        </div>
      </div>`;

    container.innerHTML = `<div class="yc-studio-split">${leftHTML}${rightHTML}</div>`;

    const srvTaEl = container.querySelector("#srvTomlTextarea");
    if (srvTaEl) {
      srvTaEl.addEventListener("input", () => {
        STATE.srvTomlDirty = true;
        STATE.srvTomlRaw = srvTaEl.value;
      });
    }
    const syncTomlPreview = () => {
      STATE.serverDraftDirty = true;
      const ta = container.querySelector("#srvTomlTextarea");
      if (ta && document.activeElement !== ta && !STATE.srvTomlDirty) {
        ta.value = renderServerTOMLLive(srv);
      }
    };

    container.querySelectorAll("[data-acc-toggle]").forEach((btn) => {
      btn.addEventListener("click", () => {
        const k = btn.dataset.accToggle;
        STATE.openServerAccs[k] = !STATE.openServerAccs[k];
        btn.closest(".yc-accordion").classList.toggle("is-open", STATE.openServerAccs[k]);
      });
    });

    container.querySelectorAll("[data-sf]").forEach((el) => {
      el.addEventListener("input", () => {
        srv[el.dataset.sf] = el.value;
        syncTomlPreview();
      });
    });
    container.querySelectorAll("[data-sf-num]").forEach((el) => {
      el.addEventListener("input", () => {
        const v = el.value.trim();
        srv[el.dataset.sfNum] = v === "" ? 0 : parseInt(v, 10) || 0;
        syncTomlPreview();
      });
    });
    container.querySelectorAll("[data-sf-bool]").forEach((el) => {
      el.addEventListener("change", () => {
        srv[el.dataset.sfBool] = el.checked;
        syncTomlPreview();
      });
    });
    container.querySelectorAll("[data-sf-tri]").forEach((el) => {
      el.addEventListener("change", () => {
        const v = el.value;
        if (v === "") delete srv[el.dataset.sfTri];
        else srv[el.dataset.sfTri] = v === "true";
        syncTomlPreview();
      });
    });
    container.querySelectorAll("[data-sf-scope]").forEach((el) => {
      el.addEventListener("change", () => {
        const set = new Set(srv.authAdditionalScopes || []);
        if (el.checked) set.add(el.dataset.sfScope);
        else set.delete(el.dataset.sfScope);
        srv.authAdditionalScopes = Array.from(set);
        syncTomlPreview();
      });
    });

    const genBtn = container.querySelector("#genSrvTokenBtn");
    if (genBtn) {
      genBtn.addEventListener("click", () => {
        srv.authToken = randomSecret(20);
        const inp = container.querySelector('[data-sf="authToken"]');
        if (inp) inp.value = srv.authToken;
        syncTomlPreview();
      });
    }

    const addHpBtn = container.querySelector("#addSrvHttpPluginBtn");
    if (addHpBtn) {
      addHpBtn.addEventListener("click", () => {
        srv.httpPlugins = srv.httpPlugins || [];
        srv.httpPlugins.push({ name: "custom-plugin-" + (srv.httpPlugins.length + 1), addr: "127.0.0.1:9001", path: "/handler", ops: ["Login", "NewProxy"] });
        STATE.serverDraftDirty = true;
        renderServerStudioSubTab(container, srv);
      });
    }
    container.querySelectorAll("[data-hp-k]").forEach((el) => {
      el.addEventListener("input", () => {
        const idx = Number(el.dataset.hpIdx);
        if (srv.httpPlugins && srv.httpPlugins[idx]) {
          srv.httpPlugins[idx][el.dataset.hpK] = el.value;
          syncTomlPreview();
        }
      });
    });
    container.querySelectorAll("[data-hp-op]").forEach((el) => {
      el.addEventListener("change", () => {
        const [idxStr, op] = el.dataset.hpOp.split("::");
        const hp = srv.httpPlugins && srv.httpPlugins[Number(idxStr)];
        if (!hp) return;
        const set = new Set(hp.ops || []);
        if (el.checked) set.add(op);
        else set.delete(op);
        hp.ops = Array.from(set);
        syncTomlPreview();
      });
    });
    container.querySelectorAll("[data-hp-del]").forEach((el) => {
      el.addEventListener("click", () => {
        srv.httpPlugins.splice(Number(el.dataset.hpDel), 1);
        STATE.serverDraftDirty = true;
        renderServerStudioSubTab(container, srv);
      });
    });

    container.querySelector("#srvTomlCopyBtn").addEventListener("click", () => {
      copyText(container.querySelector("#srvTomlTextarea").value, "frps.toml");
    });
    container.querySelector("#srvTomlParseBtn").addEventListener("click", async () => {
      try {
        const raw = container.querySelector("#srvTomlTextarea").value;
        const out = await apiFetch("/api/toml/parse", { method: "POST", body: { kind: "frps", prevServer: srv, toml: raw } });
        STATE.serverDraft = out.server;
        STATE.serverDraftDirty = true;
        STATE.srvTomlDirty = false;
        STATE.srvTomlRaw = "";
        toast("已从 TOML 代码反向解析并更新左侧全部 UI 控件");
        renderFrpsWorkspace();
      } catch (err) {
        toast(err.message, true);
      }
    });
    container.querySelector("#srvTomlSaveBtn").addEventListener("click", async () => {
      try {
        const raw = container.querySelector("#srvTomlTextarea").value;
        const out = await apiFetch("/api/servers/save-toml", { method: "POST", body: { id: srv.id, toml: raw } });
        STATE.serverDraft = out.server;
        STATE.serverDraftDirty = false;
        STATE.srvTomlDirty = false;
        STATE.srvTomlRaw = "";
        toast("已保存并应用 frps.toml 配置");
        await refreshState(true);
      } catch (err) {
        toast(err.message, true);
      }
    });
  }

  async function saveCurrentServerDraft(silent) {
    if (!STATE.serverDraft) return;
    try {
      const out = await apiFetch("/api/servers/save", { method: "POST", body: STATE.serverDraft });
      STATE.selectedServerId = out.id;
      STATE.serverDraftDirty = false;
      STATE.srvTomlDirty = false;
      STATE.srvTomlRaw = "";
      if (!silent) toast("FRPS 服务端配置已保存");
      await refreshState(true);
    } catch (err) {
      toast(err.message, true);
      throw err;
    }
  }

  async function createBlankServer() {
    const usedPorts = new Set(STATE.servers.map((s) => s.bindPort));
    let port = 7000;
    while (usedPorts.has(port)) port += 10;
    const cover = PORTRAIT_COVERS[STATE.servers.length % PORTRAIT_COVERS.length];
    try {
      const out = await apiFetch("/api/servers/save", {
        method: "POST",
        body: {
          name: "转发服务端 " + String.fromCharCode(65 + (STATE.servers.length % 26)),
          coverAsset: "k_" + cover.id,
          bindAddr: "0.0.0.0",
          bindPort: port,
          authMethod: "token",
          authToken: randomSecret(16),
          autoStart: false
        }
      });
      STATE.frpsMode = "local";
      STATE.selectedServerId = out.id;
      STATE.serverDraftDirty = false;
      toast("已新建 FRPS 服务端实例");
      await refreshState(true);
    } catch (err) {
      toast(err.message, true);
    }
  }

  // ==================== FRPS Sub-Tab: Connected Remote Clients ====================
  function renderServerRemoteClientsSubTab(container, srv, remotes) {
    if (remotes.length === 0) {
      container.innerHTML = `
        <div class="yc-card yc-empty-state">
          <img src="assets/s_37.webp" alt="" class="yc-empty-sticker" />
          <h4 style="margin:0 0 8px;color:var(--yc-indigo-ink);">当前尚无 KurumiFrp 客户端连接到「${esc(srv.name)}」</h4>
          <p style="margin:0 auto;max-width:560px;font-size:13px;line-height:1.7;">
            当任意电脑（如学校电脑 B、家里电脑 C、公司客户端 D、朋友电脑 E）安装 KurumiFrp 并启动一个指向 <code>${esc(srv.bindAddr === "0.0.0.0" ? "<本机公网IP>" : srv.bindAddr)}:${srv.bindPort}</code> 的 <code>frpc</code> 实例后，它会自动出现在此处，无需开放任何额外端口即可直接远控其配置与 P2P 规则。
          </p>
        </div>`;
      return;
    }

    container.innerHTML = `
      <div class="yc-cards-grid">
        ${remotes
          .map(
            (rc, idx) => `
          <div class="yc-work-card">
            <div class="yc-work-cover">
              <img src="${PORTRAIT_COVERS[(idx + 2) % PORTRAIT_COVERS.length].src}" alt="${esc(rc.clientName)}" />
              <span class="w-num">${pad2(idx + 1)}</span>
            </div>
            <div class="yc-work-body">
              <div>
                <h4 class="yc-work-title">
                  <span>${esc(rc.clientName)}</span>
                  <span class="yc-tag ${rc.online ? "yc-tag-ok" : "yc-tag-danger"}">${rc.online ? "已连接 · 可远控" : "离线"}</span>
                </h4>
                <div class="yc-work-route">
                  主机: ${esc(rc.hostname || "-")} (${esc(rc.os || "-")}/${esc(rc.arch || "-")}) · 来源: ${esc(rc.remoteAddr || "NAT")}
                </div>
                <div class="yc-tag-row">
                  ${rc.frpUser ? `<span class="yc-tag yc-tag-pink">user: ${esc(rc.frpUser)}</span>` : `<span class="yc-tag">无前缀用户</span>`}
                  <span class="yc-tag">活跃映射: ${rc.proxyCount || 0}</span>
                  ${rc.appVersion ? `<span class="yc-tag">v${esc(rc.appVersion)}</span>` : ""}
                </div>
              </div>
              <div style="display:flex;gap:6px;flex-wrap:wrap;">
                <button type="button" class="yc-btn yc-btn-primary yc-btn-sm" data-open-rc="${esc(srv.id)}::${esc(rc.clientId)}">
                  远控编辑配置 &amp; 隧道
                </button>
                <button type="button" class="yc-btn yc-btn-outline yc-btn-sm" data-reload-rc="${esc(srv.id)}::${esc(rc.clientId)}">
                  热重载
                </button>
                <button type="button" class="yc-btn yc-btn-outline yc-btn-sm" data-p2p-rc="${esc(srv.id)}::${esc(rc.clientId)}">
                  发起 P2P
                </button>
              </div>
            </div>
          </div>`
          )
          .join("")}
      </div>`;

    container.querySelectorAll("[data-open-rc]").forEach((b) => {
      b.addEventListener("click", () => {
        const [sid, cid] = b.dataset.openRc.split("::");
        openRemoteClientConsole(sid, cid);
      });
    });
    container.querySelectorAll("[data-reload-rc]").forEach((b) => {
      b.addEventListener("click", async () => {
        const [sid, cid] = b.dataset.reloadRc.split("::");
        try {
          await apiFetch("/api/remote/client/reload", { method: "POST", body: { serverId: sid, clientId: cid } });
          toast("已通过实例连接触发远端客户端无缝热重载");
        } catch (err) {
          toast(err.message, true);
        }
      });
    });
    container.querySelectorAll("[data-p2p-rc]").forEach((b) => {
      b.addEventListener("click", () => {
        const [sid] = b.dataset.p2pRc.split("::");
        if (sid) STATE.selectedServerId = sid;
        STATE.topologyTab = "p2pWizard";
        switchView("topology");
      });
    });
  }

  // ==================== FRPS Sub-Tab: Connection Dashboard ====================
  function renderServerConnsSubTab(container, srv, conns) {
    if (conns.length === 0) {
      container.innerHTML = `
        <div class="yc-card yc-empty-state">
          <img src="assets/s_28.webp" alt="" class="yc-empty-sticker" />
          <h4 style="margin:0 0 8px;">暂无活跃或已登记的客户端代理连接</h4>
          <p style="margin:0;font-size:13px;">当客户端将端口映射到此服务端时，所有隧道会自动收录于此，支持实时流量统计、一键开关、限流(MB)、限速(KB/s)与到期时间管控。</p>
        </div>`;
      return;
    }

    container.innerHTML = `
      <div class="yc-card">
        <div class="yc-table-wrap">
          <table class="yc-table">
            <thead>
              <tr>
                <th>代理名称</th>
                <th>协议</th>
                <th>状态</th>
                <th>当前连接</th>
                <th>累计入 / 出站</th>
                <th>实时速率</th>
                <th>管控策略 (限流/限速/限时)</th>
                <th>开关与操作</th>
              </tr>
            </thead>
            <tbody>
              ${conns
                .map((c) => {
                  const isOk = c.status === "online";
                  const isBlocked = c.status === "blocked";
                  const polParts = [];
                  if (c.trafficLimitMB > 0) polParts.push("限流 " + c.trafficLimitMB + "MB");
                  if (c.speedLimitKBps > 0) polParts.push("限速 " + c.speedLimitKBps + "KB/s");
                  if (c.expiresAt) polParts.push("限时 " + c.expiresAt.slice(0, 16).replace("T", " "));
                  return `
                    <tr>
                      <td><strong>${esc(c.proxyName)}</strong>${c.note ? `<div style="font-size:11px;color:var(--yc-muted);">${esc(c.note)}</div>` : ""}</td>
                      <td><code>${esc(c.proxyType || "-")}</code></td>
                      <td>
                        <span class="yc-tag ${isOk ? "yc-tag-ok" : isBlocked ? "yc-tag-danger" : ""}">${esc(c.status)}</span>
                        ${c.blockReason ? `<div style="font-size:11px;color:var(--yc-danger);margin-top:3px;">${esc(c.blockReason)}</div>` : ""}
                      </td>
                      <td>${c.curConns || 0}</td>
                      <td style="font-family:var(--yc-mono);font-size:12px;">↓ ${formatBytes(c.totalInBytes)} / ↑ ${formatBytes(c.totalOutBytes)}</td>
                      <td style="font-family:var(--yc-mono);font-size:12px;">${formatBytes((c.speedInBps || 0) + (c.speedOutBps || 0))}/s</td>
                      <td>${polParts.length ? polParts.map((p) => `<span class="yc-tag yc-tag-pink">${esc(p)}</span>`).join(" ") : `<span style="color:var(--yc-subtle);">无限制</span>`}</td>
                      <td>
                        <div style="display:flex;gap:6px;align-items:center;">
                          <button type="button" class="yc-btn ${c.enabled ? "yc-btn-outline" : "yc-btn-primary"} yc-btn-sm" data-conn-toggle="${esc(c.proxyName)}">
                            ${c.enabled ? "阻断" : "放行"}
                          </button>
                          <button type="button" class="yc-btn yc-btn-outline yc-btn-sm" data-conn-edit="${esc(c.proxyName)}">策略</button>
                        </div>
                      </td>
                    </tr>`;
                })
                .join("")}
            </tbody>
          </table>
        </div>
      </div>`;

    container.querySelectorAll("[data-conn-toggle]").forEach((btn) => {
      btn.addEventListener("click", async () => {
        const item = conns.find((x) => x.proxyName === btn.dataset.connToggle);
        if (!item) return;
        try {
          await apiFetch("/api/servers/conn-policy", {
            method: "POST",
            body: {
              serverId: srv.id,
              proxyName: item.proxyName,
              enabled: !item.enabled,
              trafficLimitMB: item.trafficLimitMB || 0,
              speedLimitKBps: item.speedLimitKBps || 0,
              expiresAt: item.expiresAt || "",
              note: item.note || ""
            }
          });
          toast((!item.enabled ? "已放行连接: " : "已阻断连接: ") + item.proxyName);
          await refreshState(true);
        } catch (err) {
          toast(err.message, true);
        }
      });
    });

    container.querySelectorAll("[data-conn-edit]").forEach((btn) => {
      btn.addEventListener("click", () => {
        const item = conns.find((x) => x.proxyName === btn.dataset.connEdit);
        if (item) openConnPolicyModal(srv.id, item);
      });
    });
  }

  function openConnPolicyModal(serverId, item) {
    const localDT = isoToLocalDatetimeInput(item.expiresAt);
    openModal(
      `<div class="yc-modal-head">
         <h3 class="yc-modal-title">连接管控策略 · ${esc(item.proxyName)}</h3>
         <button type="button" class="yc-btn yc-btn-outline yc-btn-sm" data-modal-close>关闭</button>
       </div>
       <div class="yc-notice yc-notice-pink" style="margin-bottom:12px;font-size:12px;line-height:1.55;">
         说明：禁用开关、到期时间与流量配额会通过官方插件立即拦截一切新建连接与重连注册（已建立的存量长连接将在重连或重启服务端时释放；硬限速可在对应客户端隧道设置 <code>bandwidthLimit</code>）。
       </div>
       <div class="yc-grid-2">
         <label class="yc-field">
           <span class="yc-field-label">流量配额上限 (MB) <span class="yc-field-hint">0 为不限配额</span></span>
           <input id="polTrafficMB" type="number" min="0" value="${item.trafficLimitMB || 0}" />
         </label>
         <label class="yc-field">
           <span class="yc-field-label">超速告警阈值 (KB/s) <span class="yc-field-hint">0 为关闭超速告警</span></span>
           <input id="polSpeedKB" type="number" min="0" value="${item.speedLimitKBps || 0}" />
         </label>
         <label class="yc-field">
           <span class="yc-field-label">到期自动阻断时间 <span class="yc-field-hint">留空长期有效</span></span>
           <input id="polExpires" type="datetime-local" value="${esc(localDT)}" />
         </label>
         <label class="yc-field">
           <span class="yc-field-label">策略备注</span>
           <input id="polNote" type="text" value="${esc(item.note || "")}" placeholder="如：临时远程协助" />
         </label>
       </div>
       <div class="yc-check-row" style="margin-top:10px;">
         <label class="yc-switch-label">
           <input id="polEnabled" type="checkbox" ${item.enabled ? "checked" : ""} />
           <span>允许该连接转发 (连接总开关)</span>
         </label>
         <label class="yc-switch-label">
           <input id="polResetTraffic" type="checkbox" />
           <span>同时清零历史累计统计流量</span>
         </label>
       </div>
       <div class="yc-modal-foot">
         <button type="button" class="yc-btn yc-btn-outline" data-modal-close>取消</button>
         <button type="button" id="polSaveBtn" class="yc-btn yc-btn-primary">保存策略</button>
       </div>`,
      (dlg) => {
        dlg.querySelector("#polSaveBtn").addEventListener("click", async () => {
          const dtRaw = dlg.querySelector("#polExpires").value.trim();
          const iso = dtRaw ? new Date(dtRaw).toISOString() : "";
          try {
            await apiFetch("/api/servers/conn-policy", {
              method: "POST",
              body: {
                serverId,
                proxyName: item.proxyName,
                enabled: dlg.querySelector("#polEnabled").checked,
                trafficLimitMB: parseInt(dlg.querySelector("#polTrafficMB").value, 10) || 0,
                speedLimitKBps: parseInt(dlg.querySelector("#polSpeedKB").value, 10) || 0,
                expiresAt: iso,
                note: dlg.querySelector("#polNote").value,
                resetTraffic: dlg.querySelector("#polResetTraffic").checked
              }
            });
            closeModal();
            toast("连接策略已更新");
            await refreshState(true);
          } catch (err) {
            toast(err.message, true);
          }
        });
      },
      560
    );
  }

  // ==================== Remote Client Inspector (Inside FRPS via Connection!) ====================
  async function openRemoteClientConsole(serverId, clientId) {
    stopLogStream();
    const reqSeq = ++STATE.remoteReqSeq;
    STATE.activeView = "frps";
    STATE.frpsMode = "remoteClient";
    STATE.remoteTarget = { serverId, clientId };
    STATE.remoteSnapshot = null;
    STATE.remoteClientDraft = null;
    STATE.cliTomlDirty = false;
    STATE.cliTomlRaw = "";
    renderTopBar();
    $("viewHome").hidden = true;
    $("viewFrps").hidden = false;
    $("viewFrpc").hidden = true;
    $("viewTopology").hidden = true;
    $("viewSystem").hidden = true;
    renderFrpsWorkspace();

    try {
      const snap = await apiFetch(`/api/remote/client?serverId=${encodeURIComponent(serverId)}&clientId=${encodeURIComponent(clientId)}`);
      if (reqSeq !== STATE.remoteReqSeq || !STATE.remoteTarget || STATE.remoteTarget.serverId !== serverId || STATE.remoteTarget.clientId !== clientId) {
        return;
      }
      STATE.remoteSnapshot = snap;
      STATE.remoteClientDraft = deepClone(snap.profile);
      renderFrpsWorkspace();
    } catch (err) {
      if (reqSeq !== STATE.remoteReqSeq) return;
      toast("拉取远端客户端配置失败: " + err.message, true);
      STATE.frpsMode = "local";
      renderFrpsWorkspace();
    }
  }

  function renderRemoteClientPane(main) {
    const snap = STATE.remoteSnapshot;
    const cli = STATE.remoteClientDraft;
    const parentSrv = STATE.servers.find((s) => s.id === STATE.remoteTarget.serverId);
    if (!snap || !cli) {
      main.innerHTML = `<div class="yc-card">正在通过连接通道读取远端客户端配置…</div>`;
      return;
    }

    main.innerHTML = `
      <div class="yc-remote-banner">
        <div>
          <div style="font-size:11px;letter-spacing:0.16em;color:#ff9ec0;font-weight:700;">CONNECTION-SCOPED REMOTE MANAGEMENT</div>
          <div style="font-size:16px;font-weight:700;margin-top:3px;">
            正在通过服务端「${esc(parentSrv ? parentSrv.name : STATE.remoteTarget.serverId)}」连接远控客户端实例：「${esc(cli.name)}」
          </div>
          <div style="font-size:12.5px;color:rgba(248,245,255,0.8);margin-top:4px;">
            远端主机: ${esc(snap.summary.hostname || "-")} · 来源地址: ${esc(snap.summary.remoteAddr || "NAT")} · 仅限管理当前已接入的这一个 frpc 实例配置
          </div>
        </div>
        <div style="display:flex;gap:8px;flex-wrap:wrap;">
          <button type="button" class="yc-btn yc-btn-ghost-dark yc-btn-sm" id="rcRefreshBtn">刷新快照</button>
          <button type="button" class="yc-btn yc-btn-ghost-dark yc-btn-sm" id="rcHotReloadBtn">远端热重载</button>
          <button type="button" class="yc-btn yc-btn-primary yc-btn-sm" id="rcBackSrvBtn">← 返回服务端本身配置</button>
        </div>
      </div>

      <div class="yc-subtabs">
        <button type="button" class="yc-subtab${STATE.remoteSubTab === "tunnels" ? " is-active" : ""}" data-rc-tab="tunnels">
          远端隧道与访问端管理 (${(cli.tunnels || []).length})
        </button>
        <button type="button" class="yc-subtab${STATE.remoteSubTab === "studio" ? " is-active" : ""}" data-rc-tab="studio">
          远端全局参数 ⇄ 实时双向 frpc.toml
        </button>
        <button type="button" class="yc-subtab${STATE.remoteSubTab === "logs" ? " is-active" : ""}" data-rc-tab="logs">
          远端实例运行日志
        </button>
      </div>
      <div id="remoteSubBody"></div>`;

    main.querySelector("#rcBackSrvBtn").addEventListener("click", () => {
      STATE.frpsMode = "local";
      renderFrpsWorkspace();
    });
    main.querySelector("#rcRefreshBtn").addEventListener("click", () => {
      openRemoteClientConsole(STATE.remoteTarget.serverId, STATE.remoteTarget.clientId);
    });
    main.querySelector("#rcHotReloadBtn").addEventListener("click", async () => {
      try {
        await apiFetch("/api/remote/client/reload", { method: "POST", body: STATE.remoteTarget });
        toast("远端客户端实例已完成热重载");
      } catch (err) {
        toast(err.message, true);
      }
    });
    main.querySelectorAll("[data-rc-tab]").forEach((b) => {
      b.addEventListener("click", () => {
        STATE.remoteSubTab = b.dataset.rcTab;
        renderRemoteClientPane(main);
      });
    });

    const sub = main.querySelector("#remoteSubBody");
    const boundSrvId = (snap.summary && snap.summary.serverId) || STATE.remoteTarget.serverId;
    const boundCliId = (snap.profile && snap.profile.id) || STATE.remoteTarget.clientId;
    const remoteCallbacks = {
      isRemote: true,
      runtimeView: snap.runtime || {},
      onSaveClient: async (updatedCli) => {
        const res = await apiFetch("/api/remote/client/save", {
          method: "POST",
          body: {
            serverId: boundSrvId,
            clientId: boundCliId,
            profile: updatedCli,
            allowUpstreamChange: STATE.remoteAllowUpstream
          }
        });
        if (STATE.remoteTarget && STATE.remoteTarget.clientId === boundCliId) {
          STATE.remoteClientDraft = deepClone(res.profile);
          STATE.remoteSnapshot.profile = deepClone(res.profile);
          STATE.remoteSnapshot.toml = res.toml;
          STATE.cliTomlDirty = false;
          STATE.cliTomlRaw = "";
        }
        toast("已通过连接远控下发并热重载远端客户端配置！");
        await refreshState(true);
      },
      onSaveClientTOML: async (rawToml) => {
        const res = await apiFetch("/api/remote/client/save-toml", {
          method: "POST",
          body: {
            serverId: boundSrvId,
            clientId: boundCliId,
            toml: rawToml,
            allowUpstreamChange: STATE.remoteAllowUpstream
          }
        });
        if (STATE.remoteTarget && STATE.remoteTarget.clientId === boundCliId) {
          STATE.remoteClientDraft = deepClone(res.profile);
          STATE.remoteSnapshot.profile = deepClone(res.profile);
          STATE.remoteSnapshot.toml = res.toml;
          STATE.cliTomlDirty = false;
          STATE.cliTomlRaw = "";
        }
        toast("已向远端客户端下发 frpc.toml 并完成热重载！");
        await refreshState(true);
      }
    };

    if (STATE.remoteSubTab === "tunnels") {
      renderClientTunnelsSection(sub, cli, remoteCallbacks);
    } else if (STATE.remoteSubTab === "studio") {
      renderClientStudioSection(sub, cli, remoteCallbacks);
    } else {
      const lines = snap.logs || [];
      sub.innerHTML = `
        <div class="yc-card">
          <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:10px;">
            <strong>远端客户端最近日志 (${lines.length} 行 · 敏感密钥已自动脱敏)</strong>
          </div>
          <div class="yc-log-box">${lines.length ? esc(lines.join("\n")) : "暂无远端日志输出"}</div>
        </div>`;
    }
  }

  // ==================== 5. View 3: Local FRPC Client & Full Tunnel Studio ====================
  function renderFrpcWorkspace() {
    const sb = $("frpcSidebar");
    const main = $("frpcMainPane");
    if (!sb || !main) return;

    sb.innerHTML = `
      <div class="yc-side-box">
        <div class="yc-side-heading">
          <span>本机 FRPC 客户端 (${STATE.clients.length})</span>
          <button type="button" class="yc-btn yc-btn-outline yc-btn-sm" id="sideAddCliBtn">+ 新建</button>
        </div>
        <div class="yc-dir-list">
          ${
            STATE.clients.length === 0
              ? `<div style="padding:12px 6px;font-size:12.5px;color:var(--yc-muted);">暂无客户端实例，点击「+ 新建」连接远端 frps。</div>`
              : STATE.clients
                  .map((cli, idx) => {
                    const rt = STATE.runtime[cli.id] || {};
                    const isSel = cli.id === STATE.selectedClientId;
                    return `
                      <button type="button" class="yc-dir-item${isSel ? " is-active" : ""}" data-select-cli="${esc(cli.id)}">
                        <img src="${coverForIndex(cli.coverAsset, idx + 3)}" alt="" class="yc-dir-thumb" />
                        <div class="yc-dir-meta">
                          <div class="yc-dir-title">
                            <span class="yc-dot ${rt.running ? "yc-dot-ok" : ""}"></span>
                            <span>${pad2(idx + 1)} · ${esc(cli.name)}</span>
                          </div>
                          <div class="yc-dir-sub">→ ${esc(cli.serverAddr)}:${cli.serverPort} · ${(cli.tunnels || []).length} 条规则</div>
                        </div>
                      </button>`;
                  })
                  .join("")
          }
        </div>
      </div>
      <div class="yc-side-box" style="font-size:12px;line-height:1.65;color:var(--yc-muted);">
        <strong style="color:var(--yc-indigo-ink);display:block;margin-bottom:4px;">连接权限说明</strong>
        每个 <code>frpc</code> 实例仅管理本实例自身的配置文件与隧道；当它连接到某台运行 KurumiFrp 的 <code>frps</code> 服务端后，该服务端可通过此连接对本实例进行远控协同。
      </div>`;

    sb.querySelector("#sideAddCliBtn").addEventListener("click", createBlankClient);
    sb.querySelectorAll("[data-select-cli]").forEach((b) => {
      b.addEventListener("click", () => {
        const nextId = b.dataset.selectCli;
        if (nextId === STATE.selectedClientId) return;
        const doSwitch = () => {
          stopLogStream();
          STATE.selectedClientId = nextId;
          const found = STATE.clients.find((x) => x.id === STATE.selectedClientId);
          STATE.clientDraft = found ? deepClone(found) : null;
          STATE.clientDraftDirty = false;
          STATE.cliTomlDirty = false;
          STATE.cliTomlRaw = "";
          renderFrpcWorkspace();
        };
        if (STATE.clientDraftDirty || STATE.cliTomlDirty) {
          confirmModal("放弃未保存更改", "当前客户端存在尚未保存的表单或 TOML 修改，确定切换实例并放弃这些修改吗？", "放弃并切换", doSwitch);
        } else {
          doSwitch();
        }
      });
    });

    if (!STATE.selectedClientId || !STATE.clientDraft) {
      main.innerHTML = `
        <div class="yc-card yc-empty-state">
          <img src="assets/s_38.webp" alt="" class="yc-empty-sticker" />
          <h3 style="margin:0 0 8px;color:var(--yc-indigo-ink);">尚未创建本机 FRPC 客户端实例</h3>
          <p style="margin:0 0 18px;font-size:13.5px;">无公网 IP 主机（如学校电脑 B、家里电脑 C、朋友电脑 E）或需级联上报的主机（如公司电脑 D）可建立 frpc 实例接入公网 frps。</p>
          <button type="button" class="yc-btn yc-btn-primary" id="emptyCreateCliBtn">+ 立即创建第一个 FRPC 实例</button>
        </div>`;
      main.querySelector("#emptyCreateCliBtn").addEventListener("click", createBlankClient);
      return;
    }

    const cliIdx = Math.max(0, STATE.clients.findIndex((c) => c.id === STATE.selectedClientId));
    const cli = STATE.clientDraft;
    const rt = STATE.runtime[cli.id] || { state: "stopped", tunnelStats: [] };
    const coverSrc = coverForIndex(cli.coverAsset, cliIdx + 3);
    const remoteAllowed = cli.remoteCtrlEnabled !== false;

    main.innerHTML = `
      <div class="yc-profile-hero-card">
        <div class="yc-framed-portrait">
          <img src="${coverSrc}" alt="${esc(cli.name)}" />
          <span class="yc-portrait-num">${pad2(cliIdx + 1)}</span>
        </div>
        <div>
          <div class="yc-profile-head-row">
            <div class="yc-profile-title-wrap">
              <h3 class="yc-profile-title">${esc(cli.name)}</h3>
              <span class="yc-tag ${rt.running ? "yc-tag-ok" : rt.state === "error" ? "yc-tag-danger" : ""}">${rt.running ? "进程运行中 · PID " + (rt.pid || "-") + " · 在网规则 " + ((rt.tunnelStats || []).filter((x) => x.status === "running").length) + " 条" : rt.state === "error" ? "异常退出" : "未启动"}</span>
              <span class="yc-tag yc-tag-pink">${esc((cli.protocol || "tcp").toUpperCase())}</span>
              ${remoteAllowed ? `<span class="yc-tag">已开启服务端连接远控</span>` : `<span class="yc-tag yc-tag-warn">仅本地管理</span>`}
              ${STATE.clientDraftDirty ? `<span class="yc-tag yc-tag-warn">有未保存修改</span>` : ""}
            </div>
            <button type="button" class="yc-btn yc-btn-outline yc-btn-sm" id="cliPickCoverBtn">更换卡片狂三立绘</button>
          </div>
          <div class="yc-profile-kv-row">
            <span>目标服务端: <code>${esc(cli.serverAddr)}:${cli.serverPort}</code></span>
            ${cli.user ? `<span>客户端用户前缀: <code>${esc(cli.user)}</code></span>` : ""}
            <span>已配置规则: <strong>${(cli.tunnels || []).length}</strong> 条 (启用 ${(cli.tunnels || []).filter((t) => t.enabled).length} 条)</span>
          </div>
          ${rt.lastError ? `<div class="yc-notice yc-notice-danger" style="margin:8px 0;">最近错误：${esc(rt.lastError)}</div>` : ""}
          <div class="yc-profile-actions">
            ${
              rt.running
                ? `<button type="button" class="yc-btn yc-btn-primary" data-cli-ctrl="reload">⚡ 零中断热重载</button>
                   <button type="button" class="yc-btn yc-btn-indigo" data-cli-ctrl="restart">重启进程</button>
                   <button type="button" class="yc-btn yc-btn-danger" data-cli-ctrl="stop">停止实例</button>`
                : `<button type="button" class="yc-btn yc-btn-primary" data-cli-ctrl="start">▶ 启动并连接 FRPS</button>`
            }
            <button type="button" class="yc-btn yc-btn-outline" id="cliSaveAllBtn">保存全局配置</button>
            <button type="button" class="yc-btn yc-btn-outline" id="cliDupBtn">复制副本</button>
            <button type="button" class="yc-btn yc-btn-danger" id="cliDelBtn">删除实例</button>
          </div>
        </div>
      </div>

      <div class="yc-subtabs">
        <button type="button" class="yc-subtab${STATE.frpcSubTab === "tunnels" ? " is-active" : ""}" data-cli-tab="tunnels">
          隧道与访问端管理 · 全 8 类协议 &amp; 插件 (${(cli.tunnels || []).length})
        </button>
        <button type="button" class="yc-subtab${STATE.frpcSubTab === "studio" ? " is-active" : ""}" data-cli-tab="studio">
          客户端全局参数 ⇄ 实时双向 frpc.toml
        </button>
        <button type="button" class="yc-subtab${STATE.frpcSubTab === "logs" ? " is-active" : ""}" data-cli-tab="logs">
          实时运行日志
        </button>
      </div>
      <div id="frpcSubBody"></div>`;

    main.querySelector("#cliPickCoverBtn").addEventListener("click", () => {
      openCoverPickerModal(cli.coverAsset, (picked) => {
        cli.coverAsset = picked;
        STATE.clientDraftDirty = true;
        renderFrpcWorkspace();
      });
    });

    main.querySelectorAll("[data-cli-ctrl]").forEach((b) => {
      b.addEventListener("click", async () => {
        try {
          if (STATE.clientDraftDirty) await saveLocalClientProfile(cli, true);
          await apiFetch("/api/clients/control", { method: "POST", body: { id: cli.id, action: b.dataset.cliCtrl } });
          toast("已执行客户端动作: " + b.dataset.cliCtrl);
          await refreshState(true);
        } catch (err) {
          toast(err.message, true);
        }
      });
    });

    main.querySelector("#cliSaveAllBtn").addEventListener("click", () => saveLocalClientProfile(cli, false));
    main.querySelector("#cliDupBtn").addEventListener("click", async () => {
      try {
        const out = await apiFetch("/api/clients/duplicate", { method: "POST", body: { id: cli.id } });
        STATE.selectedClientId = out.id;
        STATE.clientDraftDirty = false;
        toast("已复制客户端实例副本");
        await refreshState(true);
      } catch (err) {
        toast(err.message, true);
      }
    });
    main.querySelector("#cliDelBtn").addEventListener("click", () => {
      confirmModal("删除 FRPC 客户端实例", `确定删除「${cli.name}」及其名下所有隧道规则吗？`, "确认删除", async () => {
        try {
          await apiFetch("/api/clients/delete", { method: "POST", body: { id: cli.id } });
          STATE.selectedClientId = "";
          STATE.clientDraft = null;
          STATE.clientDraftDirty = false;
          toast("已删除 FRPC 实例");
          await refreshState(true);
        } catch (err) {
          toast(err.message, true);
        }
      });
    });

    main.querySelectorAll("[data-cli-tab]").forEach((b) => {
      b.addEventListener("click", () => {
        stopLogStream();
        STATE.frpcSubTab = b.dataset.cliTab;
        renderFrpcWorkspace();
      });
    });

    const localCallbacks = {
      isRemote: false,
      runtimeView: rt,
      onSaveClient: async (updatedCli) => {
        await saveLocalClientProfile(updatedCli, false);
      },
      onSaveClientTOML: async (rawToml) => {
        const out = await apiFetch("/api/clients/save-toml", { method: "POST", body: { id: cli.id, toml: rawToml } });
        STATE.clientDraft = deepClone(out.client);
        STATE.clientDraftDirty = false;
        toast("已保存并热重载 frpc.toml！");
        await refreshState(true);
      }
    };

    const sub = main.querySelector("#frpcSubBody");
    if (STATE.frpcSubTab === "tunnels") {
      renderClientTunnelsSection(sub, cli, localCallbacks);
    } else if (STATE.frpcSubTab === "studio") {
      renderClientStudioSection(sub, cli, localCallbacks);
    } else {
      renderInstanceLogsBox(sub, cli.id, rt.running);
    }
  }

  async function saveLocalClientProfile(cliObj, silent) {
    try {
      const out = await apiFetch("/api/clients/save", { method: "POST", body: cliObj });
      STATE.selectedClientId = out.id;
      STATE.clientDraftDirty = false;
      if (!silent) toast("FRPC 客户端配置已保存并自动同步热重载");
      await refreshState(true);
    } catch (err) {
      toast(err.message, true);
      throw err;
    }
  }

  async function createBlankClient() {
    const firstSrv = STATE.servers[0];
    const cover = PORTRAIT_COVERS[(STATE.clients.length + 3) % PORTRAIT_COVERS.length];
    try {
      const out = await apiFetch("/api/clients/save", {
        method: "POST",
        body: {
          name: "客户端实例 " + String.fromCharCode(66 + (STATE.clients.length % 25)),
          coverAsset: "k_" + cover.id,
          serverAddr: firstSrv ? (firstSrv.bindAddr === "0.0.0.0" ? "127.0.0.1" : firstSrv.bindAddr) : "127.0.0.1",
          serverPort: firstSrv ? firstSrv.bindPort : 7000,
          authMethod: "token",
          authToken: firstSrv ? firstSrv.authToken || "" : "",
          protocol: "tcp",
          tlsEnable: true,
          tunnels: []
        }
      });
      STATE.selectedClientId = out.id;
      STATE.clientDraftDirty = false;
      toast("已新建 FRPC 客户端实例");
      await refreshState(true);
    } catch (err) {
      toast(err.message, true);
    }
  }

  // ==================== Shared Client Tunnel Studio (Used by Both Local FRPC & Remote FRPS->FRPC!) ====================
  function renderClientTunnelsSection(container, cli, ctxCallbacks) {
    const allTunnels = cli.tunnels || [];
    const liveMap = {};
    ((ctxCallbacks.runtimeView && ctxCallbacks.runtimeView.tunnelStats) || []).forEach((s) => {
      liveMap[s.name] = s;
      const dot = s.name.indexOf(".");
      if (dot >= 0) liveMap[s.name.slice(dot + 1)] = s;
    });

    const filtered = allTunnels.filter((t) => {
      const mode = t.mode || "proxy";
      const tp = (t.type || "tcp").toLowerCase();
      if (STATE.tunnelFilter === "visitor") return mode === "visitor";
      if (STATE.tunnelFilter === "port") return mode !== "visitor" && (tp === "tcp" || tp === "udp");
      if (STATE.tunnelFilter === "web") return mode !== "visitor" && (tp === "http" || tp === "https" || tp === "tcpmux");
      if (STATE.tunnelFilter === "p2p") return tp === "xtcp" || tp === "stcp" || tp === "sudp";
      return true;
    });

    container.innerHTML = `
      <div class="yc-card" style="padding:16px 20px;">
        <div style="display:flex;justify-content:space-between;align-items:center;flex-wrap:wrap;gap:12px;">
          <div style="display:flex;gap:6px;flex-wrap:wrap;">
            ${[
              ["all", "全部规则 (" + allTunnels.length + ")"],
              ["port", "TCP / UDP 端口映射"],
              ["web", "HTTP / HTTPS / TCPMUX"],
              ["p2p", "XTCP / STCP / SUDP 私密与P2P"],
              ["visitor", "Visitors 访问发起端"]
            ]
              .map(
                ([k, lbl]) => `
              <button type="button" class="yc-btn ${STATE.tunnelFilter === k ? "yc-btn-indigo" : "yc-btn-outline"} yc-btn-sm" data-tun-filter="${k}">
                ${esc(lbl)}
              </button>`
              )
              .join("")}
          </div>
          <div style="display:flex;gap:8px;flex-wrap:wrap;">
            <button type="button" class="yc-btn yc-btn-primary yc-btn-sm" id="btnAddProxyRule">+ 新增映射隧道 (Proxy)</button>
            <button type="button" class="yc-btn yc-btn-outline yc-btn-sm" id="btnAddVisitorRule">+ 新增访问发起端 / P2P (Visitor)</button>
          </div>
        </div>
      </div>

      ${
        filtered.length === 0
          ? `<div class="yc-card yc-empty-state">
               <img src="assets/s_39.webp" alt="" class="yc-empty-sticker" />
               <h4 style="margin:0 0 8px;color:var(--yc-indigo-ink);">当前筛选下暂无隧道规则</h4>
               <p style="margin:0;font-size:13px;">点击右上「+ 新增映射隧道」暴露本机端口/网站/内置插件服务，或「+ 新增访问发起端」发起 XTCP P2P 点对点直连。</p>
             </div>`
          : `<div class="yc-cards-grid">
               ${filtered
                 .map((t, idx) => {
                   const isVis = (t.mode || "proxy") === "visitor";
                   const tp = (t.type || "tcp").toUpperCase();
                   const live = liveMap[t.name];
                   let routeDesc = "";
                   if (isVis) {
                     routeDesc = `本机监听 ${t.localIP || "127.0.0.1"}:${t.localPort} ⇄ 对端 [${t.serverUser ? t.serverUser + "." : ""}${t.serverName || "?"}]`;
                   } else if (t.portRangeLocal && t.portRangeRemote) {
                     routeDesc = `批量端口段 ${t.localIP || "127.0.0.1"}:${t.portRangeLocal} → 远端 :${t.portRangeRemote}`;
                   } else if (t.plugin) {
                     routeDesc = `插件 [${t.plugin}] → ${t.remotePort ? "远端 :" + t.remotePort : (t.customDomains || []).join(",") || t.subDomain || tp}`;
                   } else if (tp === "HTTP" || tp === "HTTPS" || tp === "TCPMUX") {
                     routeDesc = `${t.localIP || "127.0.0.1"}:${t.localPort || "-"} → ${(t.customDomains || []).join(", ") || (t.subDomain ? t.subDomain + ".*" : "域名路由")}`;
                   } else if (tp === "XTCP" || tp === "STCP" || tp === "SUDP") {
                     routeDesc = `本地服务 ${t.localIP || "127.0.0.1"}:${t.localPort || "-"} → 私密/P2P 标识 [${t.name}]`;
                   } else {
                     routeDesc = `${t.localIP || "127.0.0.1"}:${t.localPort || "-"} → 服务端端口 :${t.remotePort || "随机"}`;
                   }

                   return `
                     <div class="yc-work-card">
                       <div class="yc-work-cover">
                         <img src="${PORTRAIT_COVERS[(idx + 5) % PORTRAIT_COVERS.length].src}" alt="" />
                         <span class="w-num">${pad2(idx + 1)}</span>
                       </div>
                       <div class="yc-work-body">
                         <div>
                           <h4 class="yc-work-title">
                             <span>${esc(t.name)}</span>
                             <span class="yc-tag ${!t.enabled ? "" : live && live.status === "running" ? "yc-tag-ok" : "yc-tag-pink"}">
                               ${!t.enabled ? "已停用" : live ? live.status : "就绪"}
                             </span>
                           </h4>
                           <div class="yc-work-route">${esc(routeDesc)}</div>
                           <div class="yc-tag-row">
                             <span class="yc-tag yc-tag-pink">${isVis ? "VISITOR · " + tp : tp}</span>
                             ${t.plugin ? `<span class="yc-tag">插件: ${esc(t.plugin)}</span>` : ""}
                             ${t.useEncryption ? `<span class="yc-tag">加密</span>` : ""}
                             ${t.useCompression ? `<span class="yc-tag">压缩</span>` : ""}
                             ${t.bandwidthLimit || t.bandwidthLimitKB ? `<span class="yc-tag">限速 ${esc(t.bandwidthLimit || t.bandwidthLimitKB + "KB")}</span>` : ""}
                             ${t.loadBalancerGroup ? `<span class="yc-tag">LB: ${esc(t.loadBalancerGroup)}</span>` : ""}
                             ${t.healthCheckType ? `<span class="yc-tag">HC: ${esc(t.healthCheckType)}</span>` : ""}
                             ${t.fallbackTo ? `<span class="yc-tag">回退: ${esc(t.fallbackTo)}</span>` : ""}
                           </div>
                           ${live && live.err ? `<div style="font-size:11.5px;color:var(--yc-danger);margin-bottom:6px;">${esc(live.err)}</div>` : ""}
                         </div>
                         <div style="display:flex;gap:6px;flex-wrap:wrap;">
                           <button type="button" class="yc-btn yc-btn-outline yc-btn-sm" data-tun-toggle="${idx}">
                             ${t.enabled ? "暂停" : "启用"}
                           </button>
                           <button type="button" class="yc-btn yc-btn-primary yc-btn-sm" data-tun-edit="${esc(t.id || t.name)}">编辑全参数</button>
                           <button type="button" class="yc-btn yc-btn-danger yc-btn-sm" data-tun-del="${esc(t.id || t.name)}">删除</button>
                         </div>
                       </div>
                     </div>`;
                 })
                 .join("")}
             </div>`
      }`;

    container.querySelectorAll("[data-tun-filter]").forEach((b) => {
      b.addEventListener("click", () => {
        STATE.tunnelFilter = b.dataset.tunFilter;
        renderClientTunnelsSection(container, cli, ctxCallbacks);
      });
    });

    container.querySelector("#btnAddProxyRule").addEventListener("click", () => {
      openFullTunnelModal(
        cli,
        {
          id: "",
          name: "proxy-" + ((cli.tunnels || []).length + 1),
          mode: "proxy",
          type: "tcp",
          enabled: true,
          localIP: "127.0.0.1",
          localPort: 7777,
          remotePort: 9999
        },
        ctxCallbacks
      );
    });

    container.querySelector("#btnAddVisitorRule").addEventListener("click", () => {
      openFullTunnelModal(
        cli,
        {
          id: "",
          name: "p2p-visitor-" + ((cli.tunnels || []).length + 1),
          mode: "visitor",
          type: "xtcp",
          enabled: true,
          localIP: "127.0.0.1",
          localPort: 17777,
          keepTunnelOpen: true,
          useEncryption: true,
          useCompression: true
        },
        ctxCallbacks
      );
    });

    container.querySelectorAll("[data-tun-toggle]").forEach((b) => {
      b.addEventListener("click", async () => {
        const target = filtered[Number(b.dataset.tunToggle)];
        if (!target) return;
        const prevEnabled = target.enabled;
        target.enabled = !target.enabled;
        try {
          await ctxCallbacks.onSaveClient(cli);
        } catch (err) {
          target.enabled = prevEnabled;
          toast(err.message || "保存隧道开关状态失败", true);
          renderClientTunnelsSection(container, cli, ctxCallbacks);
        }
      });
    });

    container.querySelectorAll("[data-tun-edit]").forEach((b) => {
      b.addEventListener("click", () => {
        const key = b.dataset.tunEdit;
        const found = (cli.tunnels || []).find((x) => (x.id || x.name) === key);
        if (found) openFullTunnelModal(cli, deepClone(found), ctxCallbacks);
      });
    });

    container.querySelectorAll("[data-tun-del]").forEach((b) => {
      b.addEventListener("click", () => {
        const key = b.dataset.tunDel;
        const found = (cli.tunnels || []).find((x) => (x.id || x.name) === key);
        if (!found) return;
        confirmModal("删除隧道规则", `确定删除规则「${found.name}」吗？`, "确认删除", async () => {
          const backupTunnels = deepClone(cli.tunnels || []);
          cli.tunnels = (cli.tunnels || []).filter((x) => (x.id || x.name) !== key);
          try {
            await ctxCallbacks.onSaveClient(cli);
          } catch (err) {
            cli.tunnels = backupTunnels;
            toast(err.message || "删除隧道规则失败", true);
            renderClientTunnelsSection(container, cli, ctxCallbacks);
          }
        });
      });
    });
  }

  // ==================== Full-Featured Official FRP v1 Tunnel & Visitor Modal ====================
  function mapToLines(m) {
    return Object.keys(m || {})
      .map((k) => `${k}=${m[k]}`)
      .join("\n");
  }
  function linesToMap(txt) {
    const out = {};
    String(txt || "")
      .split("\n")
      .forEach((line) => {
        const idx = line.indexOf("=");
        if (idx > 0) {
          const k = line.slice(0, idx).trim();
          const v = line.slice(idx + 1).trim();
          if (k) out[k] = v;
        }
      });
    return out;
  }

  function openFullTunnelModal(cli, tunDraft, ctxCallbacks) {
    const renderBody = (dlg) => {
      const isVis = (tunDraft.mode || "proxy") === "visitor";
      const tp = (tunDraft.type || "tcp").toLowerCase();
      const plug = tunDraft.plugin || "";
      const bwStr = tunDraft.bandwidthLimit || (tunDraft.bandwidthLimitKB ? tunDraft.bandwidthLimitKB + "KB" : "");

      dlg.innerHTML = `
        <div class="yc-modal-head">
          <h3 class="yc-modal-title">${tunDraft.id ? "编辑隧道规则 · " + esc(tunDraft.name) : "新建全功能隧道 / 访问端规则"}</h3>
          <button type="button" class="yc-btn yc-btn-outline yc-btn-sm" data-modal-close>关闭</button>
        </div>

        <div class="yc-grid-3" style="margin-bottom:14px;">
          <label class="yc-field">
            <span class="yc-field-label">规则角色模式</span>
            <select id="tmMode">
              <option value="proxy" ${!isVis ? "selected" : ""}>[[proxies]] 暴露本机/内网服务</option>
              <option value="visitor" ${isVis ? "selected" : ""}>[[visitors]] 访问端 / P2P 发起端</option>
            </select>
          </label>
          <label class="yc-field">
            <span class="yc-field-label">隧道协议类型 <span class="yc-field-hint">type</span></span>
            <select id="tmType">
              ${(isVis
                ? [
                    ["xtcp", "xtcp (P2P 点对点直连打洞)"],
                    ["stcp", "stcp (安全私密 TCP 访问端)"],
                    ["sudp", "sudp (安全私密 UDP 访问端)"]
                  ]
                : [
                    ["tcp", "tcp (端口转发 / 支持端口段)"],
                    ["udp", "udp (UDP 转发 / 支持端口段)"],
                    ["http", "http (域名路由 / Header 改写)"],
                    ["https", "https (TLS 域名透传)"],
                    ["tcpmux", "tcpmux (HTTP CONNECT 复用)"],
                    ["xtcp", "xtcp (P2P 点对点被访问端)"],
                    ["stcp", "stcp (安全私密 TCP 服务端)"],
                    ["sudp", "sudp (安全私密 UDP 服务端)"]
                  ]
              )
                .map(([v, l]) => `<option value="${v}" ${tp === v ? "selected" : ""}>${l}</option>`)
                .join("")}
            </select>
          </label>
          <label class="yc-field">
            <span class="yc-field-label">规则唯一名称 <span class="yc-field-hint">name</span></span>
            <input id="tmName" type="text" value="${esc(tunDraft.name || "")}" placeholder="如 school-7777" />
          </label>
        </div>

        ${
          isVis
            ? `<!-- Visitor Configuration -->
               <div class="yc-card" style="padding:16px;background:#faf9fe;margin-bottom:14px;">
                 <strong style="display:block;margin-bottom:10px;font-size:13.5px;">访问端连接目标与 P2P 高级打洞参数</strong>
                 <div class="yc-grid-3">
                   <label class="yc-field">
                     <span class="yc-field-label">对端隧道名 <span class="yc-field-hint">serverName</span></span>
                     <input id="tmServerName" type="text" value="${esc(tunDraft.serverName || "")}" placeholder="被访问端代理名称" />
                   </label>
                   <label class="yc-field">
                     <span class="yc-field-label">对端客户端用户名 <span class="yc-field-hint">serverUser</span></span>
                     <input id="tmServerUser" type="text" value="${esc(tunDraft.serverUser || "")}" placeholder="对端设置了 user 时必填" />
                   </label>
                   <label class="yc-field">
                     <span class="yc-field-label">共享密钥 <span class="yc-field-hint">secretKey</span></span>
                     <input id="tmSecretKey" type="text" value="${esc(tunDraft.secretKey || "")}" placeholder="需与被访问端一致" />
                   </label>
                   <label class="yc-field">
                     <span class="yc-field-label">本地绑定地址 <span class="yc-field-hint">bindAddr</span></span>
                     <input id="tmLocalIP" type="text" value="${esc(tunDraft.localIP || "127.0.0.1")}" />
                   </label>
                   <label class="yc-field">
                     <span class="yc-field-label">本地监听端口 <span class="yc-field-hint">bindPort (-1 为纯回退)</span></span>
                     <input id="tmLocalPort" type="number" value="${tunDraft.localPort != null ? tunDraft.localPort : 17777}" />
                   </label>
                   ${
                     tp === "xtcp"
                       ? `<label class="yc-field">
                            <span class="yc-field-label">底层打洞协议 <span class="yc-field-hint">protocol</span></span>
                            <select id="tmVisitorProto">
                              <option value="" ${!tunDraft.visitorProtocol ? "selected" : ""}>默认 (quic)</option>
                              <option value="quic" ${tunDraft.visitorProtocol === "quic" ? "selected" : ""}>quic</option>
                              <option value="kcp" ${tunDraft.visitorProtocol === "kcp" ? "selected" : ""}>kcp</option>
                            </select>
                          </label>
                          <label class="yc-field">
                            <span class="yc-field-label">每小时重试上限 <span class="yc-field-hint">maxRetriesAnHour</span></span>
                            <input id="tmMaxRetries" type="number" value="${tunDraft.maxRetriesAnHour || ""}" placeholder="默认 8" />
                          </label>
                          <label class="yc-field">
                            <span class="yc-field-label">重试最小间隔(秒) <span class="yc-field-hint">minRetryInterval</span></span>
                            <input id="tmMinRetry" type="number" value="${tunDraft.minRetryInterval || ""}" placeholder="默认 90" />
                          </label>
                          <label class="yc-field">
                            <span class="yc-field-label">打洞失败回退 Visitor 名 <span class="yc-field-hint">fallbackTo</span></span>
                            <input id="tmFallbackTo" type="text" value="${esc(tunDraft.fallbackTo || "")}" placeholder="关联的 stcp visitor 名称" />
                          </label>
                          <label class="yc-field">
                            <span class="yc-field-label">回退超时(毫秒) <span class="yc-field-hint">fallbackTimeoutMs</span></span>
                            <input id="tmFallbackMs" type="number" value="${tunDraft.fallbackTimeoutMs || ""}" placeholder="如 500" />
                          </label>`
                       : ""
                   }
                 </div>
                 <div class="yc-check-row">
                   ${
                     tp === "xtcp"
                       ? `<label class="yc-switch-label"><input id="tmKeepOpen" type="checkbox" ${tunDraft.keepTunnelOpen ? "checked" : ""} /><span>保持 P2P 隧道常驻保活 (keepTunnelOpen)</span></label>`
                       : ""
                   }
                   <label class="yc-switch-label"><input id="tmEnc" type="checkbox" ${tunDraft.useEncryption ? "checked" : ""} /><span>传输加密</span></label>
                   <label class="yc-switch-label"><input id="tmComp" type="checkbox" ${tunDraft.useCompression ? "checked" : ""} /><span>传输压缩</span></label>
                 </div>
               </div>`
            : `<!-- Proxy Configuration -->
               <div class="yc-card" style="padding:16px;background:#faf9fe;margin-bottom:14px;">
                 <strong style="display:block;margin-bottom:10px;font-size:13.5px;">1. 本地目标、服务端映射与内置客户端插件 ([proxies.plugin])</strong>
                 <div class="yc-grid-3">
                   <label class="yc-field">
                     <span class="yc-field-label">内置客户端插件 <span class="yc-field-hint">全部 10 种</span></span>
                     <select id="tmPlugin">
                       <option value="" ${plug === "" ? "selected" : ""}>不使用插件 (直接转发 localIP:localPort)</option>
                       <option value="static_file" ${plug === "static_file" ? "selected" : ""}>static_file (本地静态文件服务)</option>
                       <option value="http_proxy" ${plug === "http_proxy" ? "selected" : ""}>http_proxy (正向 HTTP 代理)</option>
                       <option value="socks5" ${plug === "socks5" ? "selected" : ""}>socks5 (正向 SOCKS5 代理)</option>
                       <option value="https2http" ${plug === "https2http" ? "selected" : ""}>https2http (HTTPS 转本地 HTTP)</option>
                       <option value="https2https" ${plug === "https2https" ? "selected" : ""}>https2https (HTTPS 转本地 HTTPS)</option>
                       <option value="http2https" ${plug === "http2https" ? "selected" : ""}>http2https (HTTP 转本地 HTTPS)</option>
                       <option value="http2http" ${plug === "http2http" ? "selected" : ""}>http2http (HTTP Host 改写转发)</option>
                       <option value="tls2raw" ${plug === "tls2raw" ? "selected" : ""}>tls2raw (TLS 解密转原始流)</option>
                       <option value="unix_domain_socket" ${plug === "unix_domain_socket" ? "selected" : ""}>unix_domain_socket (Unix 套接字)</option>
                       <option value="virtual_net" ${plug === "virtual_net" ? "selected" : ""}>virtual_net (实验性虚拟网卡 TUN)</option>
                     </select>
                   </label>
                   ${
                     plug === ""
                       ? `<label class="yc-field">
                            <span class="yc-field-label">内网目标 IP <span class="yc-field-hint">localIP</span></span>
                            <input id="tmLocalIP" type="text" value="${esc(tunDraft.localIP || "127.0.0.1")}" />
                          </label>
                          <label class="yc-field">
                            <span class="yc-field-label">内网目标端口 <span class="yc-field-hint">localPort</span></span>
                            <input id="tmLocalPort" type="number" value="${tunDraft.localPort || ""}" placeholder="如 7777" />
                          </label>`
                       : `<label class="yc-field">
                            <span class="yc-field-label">插件路径 / 套接字 <span class="yc-field-hint">localPath / unixPath</span></span>
                            <input id="tmPlugPath" type="text" value="${esc(tunDraft.pluginLocalPath || "")}" placeholder="/var/www 或 /tmp/docker.sock" />
                          </label>
                          <label class="yc-field">
                            <span class="yc-field-label">插件后端地址 / 网段 <span class="yc-field-hint">localAddr / address</span></span>
                            <input id="tmPlugAddr" type="text" value="${esc(tunDraft.pluginLocalAddr || "")}" placeholder="127.0.0.1:8080 或 100.86.1.1/24" />
                          </label>
                          <label class="yc-field">
                            <span class="yc-field-label">URL 去前缀 <span class="yc-field-hint">stripPrefix</span></span>
                            <input id="tmPlugPrefix" type="text" value="${esc(tunDraft.pluginStripPrefix || "")}" placeholder="static_file 可选" />
                          </label>
                          <label class="yc-field">
                            <span class="yc-field-label">插件认证用户名 <span class="yc-field-hint">user / httpUser</span></span>
                            <input id="tmPlugUser" type="text" value="${esc(tunDraft.pluginUser || "")}" />
                          </label>
                          <label class="yc-field">
                            <span class="yc-field-label">插件认证密码 <span class="yc-field-hint">password</span></span>
                            <input id="tmPlugPass" type="text" value="${esc(tunDraft.pluginPassword || "")}" />
                          </label>
                          <label class="yc-field">
                            <span class="yc-field-label">插件证书 / 私钥 <span class="yc-field-hint">crtPath | keyPath</span></span>
                            <div style="display:flex;gap:4px;">
                              <input id="tmPlugCrt" type="text" value="${esc(tunDraft.pluginCrtPath || "")}" placeholder="crtPath" />
                              <input id="tmPlugKey" type="text" value="${esc(tunDraft.pluginKeyPath || "")}" placeholder="keyPath" />
                            </div>
                          </label>`
                   }

                   ${
                     tp === "tcp" || tp === "udp"
                       ? `<label class="yc-field">
                            <span class="yc-field-label">服务端映射端口 <span class="yc-field-hint">remotePort</span></span>
                            <input id="tmRemotePort" type="number" value="${tunDraft.remotePort || ""}" placeholder="如 9999 / 10000" />
                          </label>
                          <label class="yc-field">
                            <span class="yc-field-label">批量本地端口段(可选) <span class="yc-field-hint">parseNumberRangePair</span></span>
                            <input id="tmRangeLocal" type="text" value="${esc(tunDraft.portRangeLocal || "")}" placeholder="如 6000-6006,6007" />
                          </label>
                          <label class="yc-field">
                            <span class="yc-field-label">批量远端端口段(可选) <span class="yc-field-hint">与本地段一一对应</span></span>
                            <input id="tmRangeRemote" type="text" value="${esc(tunDraft.portRangeRemote || "")}" placeholder="如 16000-16006,16007" />
                          </label>`
                       : ""
                   }

                   ${
                     tp === "http" || tp === "https" || tp === "tcpmux"
                       ? `<label class="yc-field">
                            <span class="yc-field-label">自定义域名(逗号分隔) <span class="yc-field-hint">customDomains</span></span>
                            <input id="tmDomains" type="text" value="${esc((tunDraft.customDomains || []).join(", "))}" placeholder="app.example.com" />
                          </label>
                          <label class="yc-field">
                            <span class="yc-field-label">二级子域名 <span class="yc-field-hint">subdomain</span></span>
                            <input id="tmSubDomain" type="text" value="${esc(tunDraft.subDomain || "")}" placeholder="blog" />
                          </label>
                          ${
                            tp === "http"
                              ? `<label class="yc-field">
                                   <span class="yc-field-label">URL 路径路由(逗号分隔) <span class="yc-field-hint">locations</span></span>
                                   <input id="tmLocations" type="text" value="${esc((tunDraft.locations || []).join(", "))}" placeholder="/, /api" />
                                 </label>
                                 <label class="yc-field">
                                   <span class="yc-field-label">Host 请求头改写 <span class="yc-field-hint">hostHeaderRewrite</span></span>
                                   <input id="tmHostRewrite" type="text" value="${esc(tunDraft.hostHeaderRewrite || "")}" placeholder="127.0.0.1" />
                                 </label>`
                              : ""
                          }
                          <label class="yc-field">
                            <span class="yc-field-label">HTTP Basic 用户名 <span class="yc-field-hint">httpUser</span></span>
                            <input id="tmHttpUser" type="text" value="${esc(tunDraft.httpUser || "")}" />
                          </label>
                          <label class="yc-field">
                            <span class="yc-field-label">HTTP Basic 密码 <span class="yc-field-hint">httpPassword</span></span>
                            <input id="tmHttpPass" type="text" value="${esc(tunDraft.httpPassword || "")}" />
                          </label>
                          <label class="yc-field">
                            <span class="yc-field-label">按用户分流标识 <span class="yc-field-hint">routeByHTTPUser</span></span>
                            <input id="tmRouteUser" type="text" value="${esc(tunDraft.routeByHTTPUser || "")}" />
                          </label>`
                       : ""
                   }

                   ${
                     tp === "stcp" || tp === "sudp" || tp === "xtcp"
                       ? `<label class="yc-field">
                            <span class="yc-field-label">私密/P2P 共享密钥 <span class="yc-field-hint">secretKey</span></span>
                            <input id="tmSecretKey" type="text" value="${esc(tunDraft.secretKey || randomSecret(12))}" />
                          </label>
                          <label class="yc-field">
                            <span class="yc-field-label">允许访问的用户(逗号分隔) <span class="yc-field-hint">allowUsers</span></span>
                            <input id="tmAllowUsers" type="text" value="${esc((tunDraft.allowUsers || []).join(", "))}" placeholder="留空仅同用户, * 允许全部" />
                          </label>`
                       : ""
                   }
                 </div>
               </div>

               <div class="yc-card" style="padding:16px;background:#ffffff;margin-bottom:14px;">
                 <strong style="display:block;margin-bottom:10px;font-size:13.5px;">2. 带宽限速、Proxy Protocol、负载均衡与健康检查</strong>
                 <div class="yc-grid-3">
                   <label class="yc-field">
                     <span class="yc-field-label">隧道限速 <span class="yc-field-hint">如 512KB 或 5MB</span></span>
                     <input id="tmBwLimit" type="text" value="${esc(bwStr)}" placeholder="留空不限速" />
                   </label>
                   <label class="yc-field">
                     <span class="yc-field-label">限速执行端 <span class="yc-field-hint">bandwidthLimitMode</span></span>
                     <select id="tmBwMode">
                       <option value="client" ${(tunDraft.bandwidthLimitMode || "client") === "client" ? "selected" : ""}>client (客户端限速)</option>
                       <option value="server" ${tunDraft.bandwidthLimitMode === "server" ? "selected" : ""}>server (服务端限速)</option>
                     </select>
                   </label>
                   <label class="yc-field">
                     <span class="yc-field-label">传递真实源 IP <span class="yc-field-hint">proxyProtocolVersion</span></span>
                     <select id="tmProxyProto">
                       <option value="" ${!tunDraft.proxyProtocolVersion ? "selected" : ""}>不启用</option>
                       <option value="v1" ${tunDraft.proxyProtocolVersion === "v1" ? "selected" : ""}>v1</option>
                       <option value="v2" ${tunDraft.proxyProtocolVersion === "v2" ? "selected" : ""}>v2</option>
                     </select>
                   </label>
                   <label class="yc-field">
                     <span class="yc-field-label">负载均衡分组名 <span class="yc-field-hint">loadBalancer.group</span></span>
                     <input id="tmLbGroup" type="text" value="${esc(tunDraft.loadBalancerGroup || "")}" placeholder="如 web-cluster" />
                   </label>
                   <label class="yc-field">
                     <span class="yc-field-label">负载均衡分组密钥 <span class="yc-field-hint">loadBalancer.groupKey</span></span>
                     <input id="tmLbKey" type="text" value="${esc(tunDraft.loadBalancerGroupKey || "")}" />
                   </label>
                   <label class="yc-field">
                     <span class="yc-field-label">本地健康检查 <span class="yc-field-hint">healthCheck.type</span></span>
                     <select id="tmHcType">
                       <option value="" ${!tunDraft.healthCheckType ? "selected" : ""}>关闭</option>
                       <option value="tcp" ${tunDraft.healthCheckType === "tcp" ? "selected" : ""}>tcp 端口探测</option>
                       <option value="http" ${tunDraft.healthCheckType === "http" ? "selected" : ""}>http 状态码探测</option>
                     </select>
                   </label>
                   <label class="yc-field">
                     <span class="yc-field-label">HTTP 探测路径 <span class="yc-field-hint">healthCheck.path</span></span>
                     <input id="tmHcPath" type="text" value="${esc(tunDraft.healthCheckPath || "")}" placeholder="/healthz" />
                   </label>
                   <label class="yc-field">
                     <span class="yc-field-label">探测间隔 / 超时(秒) <span class="yc-field-hint">interval | timeout</span></span>
                     <div style="display:flex;gap:4px;">
                       <input id="tmHcInt" type="number" value="${tunDraft.healthCheckIntervalSeconds || ""}" placeholder="间隔10" />
                       <input id="tmHcTo" type="number" value="${tunDraft.healthCheckTimeoutSeconds || ""}" placeholder="超时3" />
                     </div>
                   </label>
                   <label class="yc-field">
                     <span class="yc-field-label">连续失败摘除次数 <span class="yc-field-hint">maxFailed</span></span>
                     <input id="tmHcFail" type="number" value="${tunDraft.healthCheckMaxFailed || ""}" placeholder="默认 3" />
                   </label>
                 </div>
                 <div class="yc-check-row">
                   <label class="yc-switch-label"><input id="tmEnc" type="checkbox" ${tunDraft.useEncryption ? "checked" : ""} /><span>启用隧道加密 (useEncryption)</span></label>
                   <label class="yc-switch-label"><input id="tmComp" type="checkbox" ${tunDraft.useCompression ? "checked" : ""} /><span>启用流量压缩 (useCompression)</span></label>
                 </div>
               </div>

               <div class="yc-grid-2">
                 ${
                   tp === "http"
                     ? `<label class="yc-field">
                          <span class="yc-field-label">注入请求头 (每行 Key=Value) <span class="yc-field-hint">requestHeaders.set</span></span>
                          <textarea id="tmReqHeaders" rows="2" placeholder="X-From-Where=frp">${esc(mapToLines(tunDraft.requestHeaders))}</textarea>
                        </label>
                        <label class="yc-field">
                          <span class="yc-field-label">注入响应头 (每行 Key=Value) <span class="yc-field-hint">responseHeaders.set</span></span>
                          <textarea id="tmRespHeaders" rows="2" placeholder="Access-Control-Allow-Origin=*">${esc(mapToLines(tunDraft.responseHeaders))}</textarea>
                        </label>`
                     : ""
                 }
                 <label class="yc-field">
                   <span class="yc-field-label">代理元数据 (每行 Key=Value) <span class="yc-field-hint">metadatas</span></span>
                   <textarea id="tmMetas" rows="2" placeholder="env=prod">${esc(mapToLines(tunDraft.metadatas))}</textarea>
                 </label>
                 <label class="yc-field">
                   <span class="yc-field-label">代理注解与附加 TOML <span class="yc-field-hint">extraToml</span></span>
                   <textarea id="tmExtra" rows="2" placeholder="可选附加参数">${esc(tunDraft.extraToml || "")}</textarea>
                 </label>
               </div>`
        }

        <div class="yc-modal-foot">
          <label class="yc-switch-label" style="margin-right:auto;">
            <input id="tmEnabled" type="checkbox" ${tunDraft.enabled !== false ? "checked" : ""} />
            <span>立即启用此规则</span>
          </label>
          <button type="button" class="yc-btn yc-btn-outline" data-modal-close>取消</button>
          <button type="button" id="tmSaveBtn" class="yc-btn yc-btn-primary">保存规则并同步</button>
        </div>`;

      dlg.querySelectorAll("[data-modal-close]").forEach((b) => b.addEventListener("click", closeModal));

      const collectModalIntoDraft = () => {
        if (dlg.querySelector("#tmName")) tunDraft.name = dlg.querySelector("#tmName").value.trim();
        if (dlg.querySelector("#tmEnabled")) tunDraft.enabled = dlg.querySelector("#tmEnabled").checked;
        const val = (sel) => (dlg.querySelector(sel) ? dlg.querySelector(sel).value.trim() : "");
        const has = (sel) => Boolean(dlg.querySelector(sel));
        const num = (sel) => (dlg.querySelector(sel) && dlg.querySelector(sel).value.trim() !== "" ? parseInt(dlg.querySelector(sel).value, 10) || 0 : 0);
        const chk = (sel) => Boolean(dlg.querySelector(sel) && dlg.querySelector(sel).checked);

        if (has("#tmEnc")) tunDraft.useEncryption = chk("#tmEnc");
        if (has("#tmComp")) tunDraft.useCompression = chk("#tmComp");

        if ((tunDraft.mode || "proxy") === "visitor") {
          if (has("#tmServerName")) tunDraft.serverName = val("#tmServerName");
          if (has("#tmServerUser")) tunDraft.serverUser = val("#tmServerUser");
          if (has("#tmSecretKey")) tunDraft.secretKey = val("#tmSecretKey");
          if (has("#tmLocalIP")) tunDraft.localIP = val("#tmLocalIP") || "127.0.0.1";
          if (has("#tmLocalPort")) tunDraft.localPort = parseInt(dlg.querySelector("#tmLocalPort").value, 10) || 0;
          if (has("#tmVisitorProto")) tunDraft.visitorProtocol = val("#tmVisitorProto");
          if (has("#tmKeepOpen")) tunDraft.keepTunnelOpen = chk("#tmKeepOpen");
          if (has("#tmMaxRetries")) tunDraft.maxRetriesAnHour = num("#tmMaxRetries");
          if (has("#tmMinRetry")) tunDraft.minRetryInterval = num("#tmMinRetry");
          if (has("#tmFallbackTo")) tunDraft.fallbackTo = val("#tmFallbackTo");
          if (has("#tmFallbackMs")) tunDraft.fallbackTimeoutMs = num("#tmFallbackMs");
        } else {
          if (has("#tmLocalIP")) tunDraft.localIP = val("#tmLocalIP") || "127.0.0.1";
          if (has("#tmLocalPort")) tunDraft.localPort = num("#tmLocalPort");
          if (has("#tmPlugPath")) tunDraft.pluginLocalPath = val("#tmPlugPath");
          if (has("#tmPlugAddr")) tunDraft.pluginLocalAddr = val("#tmPlugAddr");
          if (has("#tmPlugPrefix")) tunDraft.pluginStripPrefix = val("#tmPlugPrefix");
          if (has("#tmPlugUser")) tunDraft.pluginUser = val("#tmPlugUser");
          if (has("#tmPlugPass")) tunDraft.pluginPassword = val("#tmPlugPass");
          if (has("#tmPlugCrt")) tunDraft.pluginCrtPath = val("#tmPlugCrt");
          if (has("#tmPlugKey")) tunDraft.pluginKeyPath = val("#tmPlugKey");
          if (has("#tmRemotePort")) tunDraft.remotePort = num("#tmRemotePort");
          if (has("#tmRangeLocal")) tunDraft.portRangeLocal = val("#tmRangeLocal");
          if (has("#tmRangeRemote")) tunDraft.portRangeRemote = val("#tmRangeRemote");
          if (has("#tmDomains")) tunDraft.customDomains = val("#tmDomains").split(",").map((s) => s.trim()).filter(Boolean);
          if (has("#tmSubDomain")) tunDraft.subDomain = val("#tmSubDomain");
          if (has("#tmLocations")) tunDraft.locations = val("#tmLocations").split(",").map((s) => s.trim()).filter(Boolean);
          if (has("#tmHostRewrite")) tunDraft.hostHeaderRewrite = val("#tmHostRewrite");
          if (has("#tmHttpUser")) tunDraft.httpUser = val("#tmHttpUser");
          if (has("#tmHttpPass")) tunDraft.httpPassword = val("#tmHttpPass");
          if (has("#tmRouteUser")) tunDraft.routeByHTTPUser = val("#tmRouteUser");
          if (has("#tmSecretKey")) tunDraft.secretKey = val("#tmSecretKey");
          if (has("#tmAllowUsers")) tunDraft.allowUsers = val("#tmAllowUsers").split(",").map((s) => s.trim()).filter(Boolean);
          if (has("#tmBwLimit")) {
            tunDraft.bandwidthLimit = val("#tmBwLimit");
            tunDraft.bandwidthLimitKB = 0;
          }
          if (has("#tmBwMode")) tunDraft.bandwidthLimitMode = val("#tmBwMode") || "client";
          if (has("#tmProxyProto")) tunDraft.proxyProtocolVersion = val("#tmProxyProto");
          if (has("#tmLbGroup")) tunDraft.loadBalancerGroup = val("#tmLbGroup");
          if (has("#tmLbKey")) tunDraft.loadBalancerGroupKey = val("#tmLbKey");
          if (has("#tmHcType")) tunDraft.healthCheckType = val("#tmHcType");
          if (has("#tmHcPath")) tunDraft.healthCheckPath = val("#tmHcPath");
          if (has("#tmHcInt")) tunDraft.healthCheckIntervalSeconds = num("#tmHcInt");
          if (has("#tmHcTo")) tunDraft.healthCheckTimeoutSeconds = num("#tmHcTo");
          if (has("#tmHcFail")) tunDraft.healthCheckMaxFailed = num("#tmHcFail");
          if (has("#tmReqHeaders")) tunDraft.requestHeaders = linesToMap(dlg.querySelector("#tmReqHeaders").value);
          if (has("#tmRespHeaders")) tunDraft.responseHeaders = linesToMap(dlg.querySelector("#tmRespHeaders").value);
          if (has("#tmMetas")) tunDraft.metadatas = linesToMap(dlg.querySelector("#tmMetas").value);
          if (has("#tmExtra")) tunDraft.extraToml = dlg.querySelector("#tmExtra").value;
        }
      };

      dlg.querySelector("#tmMode").addEventListener("change", (e) => {
        collectModalIntoDraft();
        tunDraft.mode = e.target.value;
        if (tunDraft.mode === "visitor" && !["xtcp", "stcp", "sudp"].includes(tunDraft.type)) {
          tunDraft.type = "xtcp";
        }
        renderBody(dlg);
      });
      dlg.querySelector("#tmType").addEventListener("change", (e) => {
        collectModalIntoDraft();
        tunDraft.type = e.target.value;
        renderBody(dlg);
      });
      const plugSel = dlg.querySelector("#tmPlugin");
      if (plugSel) {
        plugSel.addEventListener("change", (e) => {
          collectModalIntoDraft();
          tunDraft.plugin = e.target.value;
          renderBody(dlg);
        });
      }

      dlg.querySelector("#tmSaveBtn").addEventListener("click", async () => {
        collectModalIntoDraft();
        if (!tunDraft.name) {
          toast("请填写隧道规则名称", true);
          return;
        }
        cli.tunnels = cli.tunnels || [];
        // F32: Block duplicate tunnel name collisions across distinct IDs; match existing entry strictly by ID!
        const dup = cli.tunnels.find((x) => x.name === tunDraft.name && (!tunDraft.id || x.id !== tunDraft.id));
        if (dup) {
          toast(`已存在同名规则「${tunDraft.name}」，请更换名称后再保存`, true);
          return;
        }

        const backupTunnels = deepClone(cli.tunnels);
        const existingIdx = tunDraft.id ? cli.tunnels.findIndex((x) => x.id === tunDraft.id) : -1;
        if (existingIdx >= 0) {
          cli.tunnels[existingIdx] = tunDraft;
        } else {
          tunDraft.id = "tun-" + Math.random().toString(16).slice(2, 10);
          cli.tunnels.push(tunDraft);
        }

        try {
          await ctxCallbacks.onSaveClient(cli);
          closeModal();
        } catch (err) {
          cli.tunnels = backupTunnels;
          toast(err.message || "保存隧道规则失败", true);
        }
      });
    };

    openModal("<div></div>", (dlg) => renderBody(dlg), 940);
  }

  // ==================== Shared Client Global Studio + Bidirectional TOML ====================
  function renderClientStudioSection(container, cli, ctxCallbacks) {
    const scopes = cli.authAdditionalScopes || [];
    const triBoolVal = (v) => (typeof v === "boolean" ? String(v) : "");
    const remoteCtrlOn = cli.remoteCtrlEnabled !== false;

    const leftHTML = `
      <div class="yc-form-stack">
        ${
          ctxCallbacks.isRemote
            ? `<div class="yc-notice yc-notice-pink" style="margin:0;">
                 <label class="yc-switch-label">
                   <input type="checkbox" id="rcUnlockUpstreamChk" ${STATE.remoteAllowUpstream ? "checked" : ""} />
                   <span>解锁修改此远端客户端的上游服务器地址/端口/Token（默认锁定保护，防止误改导致远控断连）</span>
                 </label>
               </div>`
            : ""
        }
        ${renderAccordion(
          "c1",
          STATE.openClientAccs,
          "01",
          "上游服务器连接与运行策略 (Common & Remote Link)",
          `<div class="yc-grid-2">
             <label class="yc-field">
               <span class="yc-field-label">客户端实例名称</span>
               <input type="text" data-cf="name" value="${esc(cli.name || "")}" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">客户端用户名前缀 <span class="yc-field-hint">user</span></span>
               <input type="text" data-cf="user" value="${esc(cli.user || "")}" placeholder="如 school / home / friend" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">远端 frps 服务器地址 <span class="yc-field-hint">serverAddr</span></span>
               <input type="text" data-cf="serverAddr" value="${esc(cli.serverAddr || "")}" placeholder="203.0.113.10 或域名" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">远端 frps 服务器端口 <span class="yc-field-hint">serverPort</span></span>
               <input type="number" data-cf-num="serverPort" value="${cli.serverPort || 7000}" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">自定义 DNS 服务器 <span class="yc-field-hint">dnsServer</span></span>
               <input type="text" data-cf="dnsServer" value="${esc(cli.dnsServer || "")}" placeholder="如 223.5.5.5" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">NAT 打洞 STUN 服务 <span class="yc-field-hint">natHoleStunServer</span></span>
               <input type="text" data-cf="natHoleStunServer" value="${esc(cli.natHoleStunServer || "")}" placeholder="默认 stun.easyvoip.com:3478" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">仅启动指定隧道(逗号分隔) <span class="yc-field-hint">start</span></span>
               <input type="text" data-cf-arr="startProxies" value="${esc((cli.startProxies || []).join(", "))}" placeholder="留空启动全部已启用规则" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">包含外部子配置(逗号分隔) <span class="yc-field-hint">includes</span></span>
               <input type="text" data-cf-arr="includes" value="${esc((cli.includes || []).join(", "))}" placeholder="./confd/*.toml" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">UDP 包大小 <span class="yc-field-hint">udpPacketSize</span></span>
               <input type="number" data-cf-num="udpPacketSize" value="${cli.udpPacketSize || ""}" placeholder="默认 1500" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">虚拟网卡地址(实验) <span class="yc-field-hint">virtualNet.address</span></span>
               <input type="text" data-cf="virtualNetAddr" value="${esc(cli.virtualNetAddr || "")}" placeholder="100.86.0.1/24" />
             </label>
           </div>
           <div class="yc-check-row">
             ${
               !ctxCallbacks.isRemote
                 ? `<label class="yc-switch-label">
                      <input type="checkbox" data-cf-bool="autoStart" ${cli.autoStart ? "checked" : ""} />
                      <span>软件启动时自动运行此客户端</span>
                    </label>
                    <label class="yc-switch-label">
                      <input type="checkbox" id="cliRemoteCtrlChk" ${remoteCtrlOn ? "checked" : ""} />
                      <span>允许所连接的 KurumiFrp 服务端通过连接远控本实例</span>
                    </label>`
                 : ""
             }
             <label class="yc-switch-label">
               <input type="checkbox" data-cf-bool="loginFailExit" ${cli.loginFailExit ? "checked" : ""} />
               <span>首次登录失败立即退出 (loginFailExit；关闭则断线自动重连)</span>
             </label>
           </div>`
        )}

        ${renderAccordion(
          "c2",
          STATE.openClientAccs,
          "02",
          "客户端鉴权认证 (Token / TokenSource / OIDC 全参数)",
          `<div class="yc-grid-2">
             <label class="yc-field">
               <span class="yc-field-label">鉴权模式 <span class="yc-field-hint">auth.method</span></span>
               <select data-cf="authMethod">
                 <option value="token" ${(cli.authMethod || "token") === "token" ? "selected" : ""}>token</option>
                 <option value="oidc" ${cli.authMethod === "oidc" ? "selected" : ""}>oidc</option>
               </select>
             </label>
             <label class="yc-field">
               <span class="yc-field-label">连接令牌 <span class="yc-field-hint">auth.token</span></span>
               <input type="text" data-cf="authToken" value="${esc(cli.authToken || "")}" placeholder="与 frps 一致的密钥" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">Token 外部文件路径 <span class="yc-field-hint">auth.tokenSource.file.path</span></span>
               <input type="text" data-cf="authTokenSourceFile" value="${esc(cli.authTokenSourceFile || "")}" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">OIDC Client ID <span class="yc-field-hint">auth.oidc.clientID</span></span>
               <input type="text" data-cf="oidcClientId" value="${esc(cli.oidcClientId || "")}" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">OIDC Client Secret <span class="yc-field-hint">auth.oidc.clientSecret</span></span>
               <input type="text" data-cf="oidcClientSecret" value="${esc(cli.oidcClientSecret || "")}" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">OIDC Token Endpoint <span class="yc-field-hint">tokenEndpointURL</span></span>
               <input type="text" data-cf="oidcTokenEndpointURL" value="${esc(cli.oidcTokenEndpointURL || "")}" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">OIDC Audience / Scope</span>
               <div style="display:flex;gap:4px;">
                 <input type="text" data-cf="oidcAudience" value="${esc(cli.oidcAudience || "")}" placeholder="audience" />
                 <input type="text" data-cf="oidcScope" value="${esc(cli.oidcScope || "")}" placeholder="scope" />
               </div>
             </label>
           </div>
           <div class="yc-check-row">
             <label class="yc-switch-label"><input type="checkbox" data-cf-scope="HeartBeats" ${scopes.includes("HeartBeats") ? "checked" : ""} /><span>附加鉴权: HeartBeats</span></label>
             <label class="yc-switch-label"><input type="checkbox" data-cf-scope="NewWorkConns" ${scopes.includes("NewWorkConns") ? "checked" : ""} /><span>附加鉴权: NewWorkConns</span></label>
           </div>`
        )}

        ${renderAccordion(
          "c3",
          STATE.openClientAccs,
          "03",
          "底层传输协议、前置代理与 QUIC / 多路复用",
          `<div class="yc-grid-3">
             <label class="yc-field">
               <span class="yc-field-label">底层通信协议 <span class="yc-field-hint">transport.protocol</span></span>
               <select data-cf="protocol">
                 ${["tcp", "quic", "kcp", "websocket", "wss"]
                   .map((p) => `<option value="${p}" ${(cli.protocol || "tcp") === p ? "selected" : ""}>${p.toUpperCase()}</option>`)
                   .join("")}
               </select>
             </label>
             <label class="yc-field">
               <span class="yc-field-label">预建连接池数量 <span class="yc-field-hint">poolCount</span></span>
               <input type="number" data-cf-num="poolCount" value="${cli.poolCount || ""}" placeholder="默认 1" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">前置连接代理 <span class="yc-field-hint">transport.proxyURL</span></span>
               <input type="text" data-cf="proxyURL" value="${esc(cli.proxyURL || "")}" placeholder="http:// 或 socks5://127.0.0.1:1080" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">指定源出口网卡 IP <span class="yc-field-hint">connectServerLocalIP</span></span>
               <input type="text" data-cf="connectServerLocalIP" value="${esc(cli.connectServerLocalIP || "")}" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">拨号超时(秒) <span class="yc-field-hint">dialServerTimeout</span></span>
               <input type="number" data-cf-num="dialServerTimeout" value="${cli.dialServerTimeout || ""}" placeholder="默认 10" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">TCPMux 多路复用 <span class="yc-field-hint">transport.tcpMux</span></span>
               <select data-cf-tri="tcpMux">
                 <option value="" ${triBoolVal(cli.tcpMux) === "" ? "selected" : ""}>默认 (开启)</option>
                 <option value="true" ${triBoolVal(cli.tcpMux) === "true" ? "selected" : ""}>开启 (true)</option>
                 <option value="false" ${triBoolVal(cli.tcpMux) === "false" ? "selected" : ""}>关闭 (false)</option>
               </select>
             </label>
             <label class="yc-field">
               <span class="yc-field-label">心跳发送间隔(秒) <span class="yc-field-hint">heartbeatInterval</span></span>
               <input type="number" data-cf-num="heartbeatInterval" value="${cli.heartbeatInterval || ""}" placeholder="默认 30" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">心跳超时(秒) <span class="yc-field-hint">heartbeatTimeout</span></span>
               <input type="number" data-cf-num="heartbeatTimeout" value="${cli.heartbeatTimeout || ""}" placeholder="默认 90" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">QUIC 保活(秒) <span class="yc-field-hint">quic.keepalivePeriod</span></span>
               <input type="number" data-cf-num="quicKeepalivePeriod" value="${cli.quicKeepalivePeriod || ""}" placeholder="默认 10" />
             </label>
           </div>`
        )}

        ${renderAccordion(
          "c4",
          STATE.openClientAccs,
          "04",
          "TLS 加密与双向 mTLS 证书 (transport.tls)",
          `<div class="yc-grid-2">
             <label class="yc-field">
               <span class="yc-field-label">TLS 校验域名 (SNI) <span class="yc-field-hint">tls.serverName</span></span>
               <input type="text" data-cf="tlsServerName" value="${esc(cli.tlsServerName || "")}" placeholder="可选自定义 SNI" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">受信任根证书 <span class="yc-field-hint">tls.trustedCaFile</span></span>
               <input type="text" data-cf="tlsTrustedCaFile" value="${esc(cli.tlsTrustedCaFile || "")}" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">客户端证书 <span class="yc-field-hint">tls.certFile</span></span>
               <input type="text" data-cf="tlsCertFile" value="${esc(cli.tlsCertFile || "")}" />
             </label>
             <label class="yc-field">
               <span class="yc-field-label">客户端私钥 <span class="yc-field-hint">tls.keyFile</span></span>
               <input type="text" data-cf="tlsKeyFile" value="${esc(cli.tlsKeyFile || "")}" />
             </label>
           </div>
           <div class="yc-check-row">
             <label class="yc-switch-label"><input type="checkbox" data-cf-bool="tlsEnable" ${cli.tlsEnable ? "checked" : ""} /><span>启用连接服务端 TLS 加密 (transport.tls.enable)</span></label>
             <label class="yc-switch-label"><input type="checkbox" data-cf-bool="tlsDisableCustomFirstByte" ${cli.tlsDisableCustomFirstByte ? "checked" : ""} /><span>禁用首字节特征 (disableCustomTLSFirstByte，增强公网端口复用穿透性)</span></label>
           </div>`
        )}

        ${renderAccordion(
          "c5",
          STATE.openClientAccs,
          "05",
          "全局元数据 (metadatas)、日志级别与附加 TOML",
          `<div class="yc-grid-2">
             <label class="yc-field">
               <span class="yc-field-label">全局 Metadatas (每行 Key=Value)</span>
               <textarea rows="3" id="cliMetadatasArea" placeholder="region=cn-east\nowner=presikid">${esc(mapToLines(cli.metadatas))}</textarea>
             </label>
             <label class="yc-field">
               <span class="yc-field-label">附加全局 TOML 原文 <span class="yc-field-hint">extraToml</span></span>
               <textarea rows="3" data-cf="extraToml" placeholder="# 自定义附加参数">${esc(cli.extraToml || "")}</textarea>
             </label>
           </div>`
        )}

        <div>
          <button type="button" class="yc-btn yc-btn-primary" id="cliStudioSaveFormBtn">
            ${ctxCallbacks.isRemote ? "通过连接下发修改并远端热重载" : "保存客户端配置并热重载"}
          </button>
        </div>
      </div>`;

    const liveToml = STATE.cliTomlDirty && STATE.cliTomlRaw ? STATE.cliTomlRaw : renderClientTOMLLive(cli);
    const rightHTML = `
      <div class="yc-toml-studio">
        <div class="yc-toml-head">
          <div class="yc-toml-title">
            <span class="yc-dot yc-dot-ok"></span>
            <span>frpc.toml · 完整实例双向代码编辑器</span>
          </div>
          <div style="display:flex;gap:6px;flex-wrap:wrap;">
            <button type="button" class="yc-btn yc-btn-ghost-dark yc-btn-sm" id="cliTomlCopyBtn">复制</button>
            <button type="button" class="yc-btn yc-btn-ghost-dark yc-btn-sm" id="cliTomlParseBtn">← 解析同步至表单与隧道</button>
            <button type="button" class="yc-btn yc-btn-primary yc-btn-sm" id="cliTomlSaveBtn">
              ${ctxCallbacks.isRemote ? "下发并应用 TOML" : "保存并应用 TOML"}
            </button>
          </div>
        </div>
        <textarea id="cliTomlTextarea" class="yc-toml-editor" spellcheck="false">${esc(liveToml)}</textarea>
        <div class="yc-toml-foot">
          <span>包含全局配置及全部已启用 [[proxies]] / [[visitors]]，支持直接手改代码后一键反向解析或保存。</span>
        </div>
      </div>`;

    container.innerHTML = `<div class="yc-studio-split">${leftHTML}${rightHTML}</div>`;

    const cliTaEl = container.querySelector("#cliTomlTextarea");
    if (cliTaEl) {
      cliTaEl.addEventListener("input", () => {
        STATE.cliTomlDirty = true;
        STATE.cliTomlRaw = cliTaEl.value;
      });
    }
    const syncPreview = () => {
      if (!ctxCallbacks.isRemote) STATE.clientDraftDirty = true;
      const ta = container.querySelector("#cliTomlTextarea");
      if (ta && document.activeElement !== ta && !STATE.cliTomlDirty) {
        ta.value = renderClientTOMLLive(cli);
      }
    };

    const unlockChk = container.querySelector("#rcUnlockUpstreamChk");
    if (unlockChk) {
      unlockChk.addEventListener("change", () => {
        STATE.remoteAllowUpstream = unlockChk.checked;
      });
    }

    container.querySelectorAll("[data-acc-toggle]").forEach((btn) => {
      btn.addEventListener("click", () => {
        const k = btn.dataset.accToggle;
        STATE.openClientAccs[k] = !STATE.openClientAccs[k];
        btn.closest(".yc-accordion").classList.toggle("is-open", STATE.openClientAccs[k]);
      });
    });

    container.querySelectorAll("[data-cf]").forEach((el) => {
      el.addEventListener("input", () => {
        cli[el.dataset.cf] = el.value;
        syncPreview();
      });
    });
    container.querySelectorAll("[data-cf-num]").forEach((el) => {
      el.addEventListener("input", () => {
        const v = el.value.trim();
        cli[el.dataset.cfNum] = v === "" ? 0 : parseInt(v, 10) || 0;
        syncPreview();
      });
    });
    container.querySelectorAll("[data-cf-bool]").forEach((el) => {
      el.addEventListener("change", () => {
        cli[el.dataset.cfBool] = el.checked;
        syncPreview();
      });
    });
    container.querySelectorAll("[data-cf-tri]").forEach((el) => {
      el.addEventListener("change", () => {
        const v = el.value;
        if (v === "") delete cli[el.dataset.cfTri];
        else cli[el.dataset.cfTri] = v === "true";
        syncPreview();
      });
    });
    container.querySelectorAll("[data-cf-arr]").forEach((el) => {
      el.addEventListener("input", () => {
        cli[el.dataset.cfArr] = el.value.split(",").map((s) => s.trim()).filter(Boolean);
        syncPreview();
      });
    });
    container.querySelectorAll("[data-cf-scope]").forEach((el) => {
      el.addEventListener("change", () => {
        const set = new Set(cli.authAdditionalScopes || []);
        if (el.checked) set.add(el.dataset.cfScope);
        else set.delete(el.dataset.cfScope);
        cli.authAdditionalScopes = Array.from(set);
        syncPreview();
      });
    });
    const rcChk = container.querySelector("#cliRemoteCtrlChk");
    if (rcChk) {
      rcChk.addEventListener("change", () => {
        cli.remoteCtrlEnabled = rcChk.checked;
        syncPreview();
      });
    }
    const metaArea = container.querySelector("#cliMetadatasArea");
    if (metaArea) {
      metaArea.addEventListener("input", () => {
        cli.metadatas = linesToMap(metaArea.value);
        syncPreview();
      });
    }

    container.querySelector("#cliStudioSaveFormBtn").addEventListener("click", async () => {
      try {
        await ctxCallbacks.onSaveClient(cli);
        STATE.cliTomlDirty = false;
        STATE.cliTomlRaw = "";
      } catch (err) {
        toast(err.message || "保存客户端配置失败", true);
      }
    });
    container.querySelector("#cliTomlCopyBtn").addEventListener("click", () => {
      copyText(container.querySelector("#cliTomlTextarea").value, "frpc.toml");
    });
    container.querySelector("#cliTomlParseBtn").addEventListener("click", async () => {
      try {
        const raw = container.querySelector("#cliTomlTextarea").value;
        const out = await apiFetch("/api/toml/parse", { method: "POST", body: { kind: "frpc", prevClient: cli, toml: raw } });
        Object.assign(cli, out.client);
        STATE.cliTomlDirty = false;
        STATE.cliTomlRaw = "";
        toast("已将 frpc.toml 反向解析同步回 UI 表单与隧道列表");
        renderClientStudioSection(container, cli, ctxCallbacks);
      } catch (err) {
        toast(err.message, true);
      }
    });
    container.querySelector("#cliTomlSaveBtn").addEventListener("click", async () => {
      try {
        const raw = container.querySelector("#cliTomlTextarea").value;
        await ctxCallbacks.onSaveClientTOML(raw);
        STATE.cliTomlDirty = false;
        STATE.cliTomlRaw = "";
      } catch (err) {
        toast(err.message || "保存 frpc.toml 失败", true);
      }
    });
  }

  // ==================== 6. View 4: TOPOLOGY & P2P Orchestrator ====================
  function renderTopologyWorkspace() {
    const sb = $("topologySidebar");
    const main = $("topologyMainPane");
    if (!sb || !main) return;

    sb.innerHTML = `
      <div class="yc-side-box">
        <div class="yc-side-heading"><span>拓扑与 P2P 导航</span></div>
        <div class="yc-dir-list">
          <button type="button" class="yc-dir-item${STATE.topologyTab === "mesh" ? " is-active" : ""}" data-topo-tab="mesh">
            <img src="assets/k_58.webp" alt="" class="yc-dir-thumb" style="object-fit:contain;background:#fff;" />
            <div class="yc-dir-meta">
              <div class="yc-dir-title">01 · 实时连接远控拓扑</div>
              <div class="yc-dir-sub">Server → Client 实例连接图</div>
            </div>
          </button>
          <button type="button" class="yc-dir-item${STATE.topologyTab === "p2pWizard" ? " is-active" : ""}" data-topo-tab="p2pWizard">
            <img src="assets/k_41.webp" alt="" class="yc-dir-thumb" />
            <div class="yc-dir-meta">
              <div class="yc-dir-title">02 · XTCP P2P 协同向导</div>
              <div class="yc-dir-sub">跨无公网客户端一键打洞</div>
            </div>
          </button>
        </div>
      </div>`;

    sb.querySelectorAll("[data-topo-tab]").forEach((b) => {
      b.addEventListener("click", () => {
        STATE.topologyTab = b.dataset.topoTab;
        renderTopologyWorkspace();
      });
    });

    if (STATE.topologyTab === "p2pWizard") {
      renderP2PWizardPane(main);
    } else {
      STATE.topologyTab = "mesh";
      renderTopologyMeshPane(main);
    }
  }

  function renderTopologyMeshPane(main) {
    let html = "";
    if (STATE.servers.length === 0 && STATE.clients.length === 0) {
      main.innerHTML = `
        <div class="yc-card yc-empty-state">
          <img src="assets/s_39.webp" alt="" class="yc-empty-sticker" />
          <h3 style="margin:0 0 8px;">暂无实例拓扑节点</h3>
          <p style="margin:0;">先在「FRPS 服务端」或「FRPC 客户端」板块创建实例后，此处会自动绘制实例间连接与远控关系。</p>
        </div>`;
      return;
    }

    STATE.servers.forEach((srv, idx) => {
      const rt = STATE.runtime[srv.id] || {};
      const remotes = rt.remoteClients || [];
      html += `
        <div class="yc-card">
          <div style="display:flex;justify-content:space-between;align-items:center;flex-wrap:wrap;gap:10px;margin-bottom:16px;">
            <div>
              <span class="yc-tag yc-tag-pink">FRPS 中心节点 ${pad2(idx + 1)}</span>
              <h3 style="margin:6px 0 2px;font-family:var(--yc-serif);font-size:20px;">${esc(srv.name)} (${esc(srv.bindAddr)}:${srv.bindPort})</h3>
              <span style="font-size:12.5px;color:var(--yc-muted);">该服务端可通过已建立的实例连接控制下方所有连入的客户端实例</span>
            </div>
            <button type="button" class="yc-btn yc-btn-primary yc-btn-sm" id="jumpP2PFromSrv_${idx}">发起跨客户端 P2P 配对 →</button>
          </div>
          ${
            remotes.length === 0
              ? `<div style="padding:18px;border:1px dashed var(--yc-border-strong);border-radius:10px;font-size:13px;color:var(--yc-muted);">
                   暂无已接入的 KurumiFrp 客户端实例（启动客户端连接至 <code>:${srv.bindPort}</code> 后自动点亮）。
                 </div>`
              : `<div class="yc-cards-grid">
                   ${remotes
                     .map(
                       (rc, rIdx) => `
                     <div class="yc-work-card">
                       <div class="yc-work-cover">
                         <img src="${PORTRAIT_COVERS[(rIdx + 1) % PORTRAIT_COVERS.length].src}" alt="" />
                         <span class="w-num">C${rIdx + 1}</span>
                       </div>
                       <div class="yc-work-body">
                         <div>
                           <h4 class="yc-work-title">
                             <span>${esc(rc.clientName)}</span>
                             <span class="yc-tag ${rc.online ? "yc-tag-ok" : ""}">${rc.online ? "连接在线" : "离线"}</span>
                           </h4>
                           <div class="yc-work-route">${esc(srv.name)} ═══(受控连接)═══▶ ${esc(rc.clientName)}</div>
                           <div class="yc-tag-row">
                             <span class="yc-tag">${esc(rc.hostname || "远端节点")}</span>
                             ${rc.frpUser ? `<span class="yc-tag yc-tag-pink">user: ${esc(rc.frpUser)}</span>` : ""}
                             <span class="yc-tag">映射数: ${rc.proxyCount || 0}</span>
                           </div>
                         </div>
                         <button type="button" class="yc-btn yc-btn-primary yc-btn-sm" data-topo-rc="${esc(srv.id)}::${esc(rc.clientId)}">
                           进入远控管理该客户端
                         </button>
                       </div>
                     </div>`
                     )
                     .join("")}
                 </div>`
          }
        </div>`;
    });

    if (STATE.clients.length > 0) {
      html += `
        <div class="yc-card">
          <h3 style="margin:0 0 12px;font-family:var(--yc-serif);font-size:19px;">本机独立运行的 FRPC 客户端实例 (${STATE.clients.length})</h3>
          <div class="yc-cards-grid">
            ${STATE.clients
              .map((c, idx) => {
                const rt = STATE.runtime[c.id] || {};
                return `
                  <div class="yc-work-card">
                    <div class="yc-work-cover">
                      <img src="${coverForIndex(c.coverAsset, idx + 4)}" alt="" />
                      <span class="w-num">L${idx + 1}</span>
                    </div>
                    <div class="yc-work-body">
                      <div>
                        <h4 class="yc-work-title">
                          <span>${esc(c.name)}</span>
                          <span class="yc-tag ${rt.running ? "yc-tag-ok" : ""}">${rt.running ? "已上联" : "未启动"}</span>
                        </h4>
                        <div class="yc-work-route">本机实例 ──▶ 上游服务端 ${esc(c.serverAddr)}:${c.serverPort}</div>
                        <div class="yc-tag-row">
                          <span class="yc-tag">仅限管理自身 ${c.tunnels ? c.tunnels.length : 0} 条隧道</span>
                        </div>
                      </div>
                      <button type="button" class="yc-btn yc-btn-outline yc-btn-sm" data-topo-local-cli="${esc(c.id)}">编辑本机客户端</button>
                    </div>
                  </div>`;
              })
              .join("")}
          </div>
        </div>`;
    }

    main.innerHTML = html;

    STATE.servers.forEach((srv, idx) => {
      const btn = main.querySelector("#jumpP2PFromSrv_" + idx);
      if (btn) {
        btn.addEventListener("click", () => {
          STATE.selectedServerId = srv.id;
          STATE.topologyTab = "p2pWizard";
          renderTopologyWorkspace();
        });
      }
    });
    main.querySelectorAll("[data-topo-rc]").forEach((b) => {
      b.addEventListener("click", () => {
        const [sid, cid] = b.dataset.topoRc.split("::");
        openRemoteClientConsole(sid, cid);
      });
    });
    main.querySelectorAll("[data-topo-local-cli]").forEach((b) => {
      b.addEventListener("click", () => {
        STATE.selectedClientId = b.dataset.topoLocalCli;
        STATE.clientDraft = deepClone(STATE.clients.find((x) => x.id === STATE.selectedClientId));
        STATE.clientDraftDirty = false;
        switchView("frpc");
      });
    });
  }

  function renderP2PWizardPane(main) {
    const firstSrv = STATE.servers[0];
    const selectedSrvId = STATE.selectedServerId || (firstSrv ? firstSrv.id : "");
    const rt = (selectedSrvId && STATE.runtime[selectedSrvId]) || {};
    const remotes = rt.remoteClients || [];

    main.innerHTML = `
      <div class="yc-card">
        <div style="display:flex;align-items:center;gap:14px;margin-bottom:16px;">
          <img src="assets/k_58.webp" alt="" style="width:58px;height:58px;object-fit:contain;" />
          <div>
            <h3 style="margin:0;font-family:var(--yc-serif);font-size:21px;">跨客户端 XTCP 点对点直连协同向导</h3>
            <p style="margin:4px 0 0;font-size:13px;color:var(--yc-muted);">
              支持两种实战流程：① 通过你的服务端直接远控配置被访问端（如家里电脑 C），并生成配对片段供对端用户（如朋友电脑 E）在本地发起 P2P；② 若两端均由本服务端管控，可一键双端同步打通！
            </p>
          </div>
        </div>

        <div class="yc-grid-3">
          <label class="yc-field">
            <span class="yc-field-label">协调中枢服务端 (FRPS)</span>
            <select id="p2pSrvSel">
              ${
                STATE.servers.length === 0
                  ? `<option value="">暂无本地 FRPS 服务端</option>`
                  : STATE.servers
                      .map((s) => `<option value="${esc(s.id)}" ${s.id === selectedSrvId ? "selected" : ""}>${esc(s.name)} (:${s.bindPort})</option>`)
                      .join("")
              }
            </select>
          </label>
          <label class="yc-field">
            <span class="yc-field-label">P2P 隧道标识基名</span>
            <input id="p2pBaseName" type="text" value="ce-p2p-link" placeholder="如 ce-p2p-link" />
          </label>
          <label class="yc-field">
            <span class="yc-field-label">通道类型</span>
            <select id="p2pTypeSel">
              <option value="xtcp">xtcp (NAT 穿透直连 P2P)</option>
              <option value="stcp">stcp (加密私密中继 TCP)</option>
              <option value="sudp">sudp (加密私密中继 UDP)</option>
            </select>
          </label>

          <label class="yc-field">
            <span class="yc-field-label">被访问端 (服务提供方，如家里电脑 C)</span>
            <select id="p2pHostSel">
              <option value="">仅生成配置片段（不自动远控下发）</option>
              ${remotes.map((rc) => `<option value="${esc(rc.clientId)}">${esc(rc.clientName)} (${rc.frpUser ? "user:" + esc(rc.frpUser) : "已连接"})</option>`).join("")}
            </select>
          </label>
          <label class="yc-field">
            <span class="yc-field-label">被访问端本地目标 IP</span>
            <input id="p2pHostIP" type="text" value="127.0.0.1" />
          </label>
          <label class="yc-field">
            <span class="yc-field-label">被访问端本地服务端口</span>
            <input id="p2pHostPort" type="number" value="7777" min="1" max="65535" />
          </label>

          <label class="yc-field">
            <span class="yc-field-label">访问发起端 (如朋友电脑 E)</span>
            <select id="p2pVisitorSel">
              <option value="">由对端用户在本地 WebUI 自行发起 (生成配对代码)</option>
              ${remotes.map((rc, i) => `<option value="${esc(rc.clientId)}" ${i === 1 ? "selected" : ""}>同时远控下发至：${esc(rc.clientName)}</option>`).join("")}
            </select>
          </label>
          <label class="yc-field">
            <span class="yc-field-label">发起端本地绑定端口</span>
            <input id="p2pVisitorPort" type="number" value="17777" min="1" max="65535" />
          </label>
          <label class="yc-field">
            <span class="yc-field-label">共享打洞密钥 (secretKey)</span>
            <input id="p2pSecret" type="text" value="${esc(randomSecret(14))}" />
          </label>
        </div>

        <div class="yc-check-row" style="margin:12px 0 18px;">
          <label class="yc-switch-label">
            <input id="p2pKeepOpen" type="checkbox" checked />
            <span>保持 P2P 通道常驻预热 (keepTunnelOpen = true)</span>
          </label>
          <label class="yc-switch-label">
            <input id="p2pFallback" type="checkbox" checked />
            <span>同时配置 STCP 自动回退保底通道 (严格对称型 NAT 下 800ms 无缝回退不断连)</span>
          </label>
          <label class="yc-switch-label">
            <input id="p2pOverwrite" type="checkbox" />
            <span>允许覆盖同名已有 P2P 规则 (默认重名时拦截保护)</span>
          </label>
        </div>

        <button type="button" id="p2pExecuteBtn" class="yc-btn yc-btn-primary">
          ⚡ 执行 P2P 协同下发并生成对端配对凭证
        </button>

        ${
          STATE.lastP2PResult
            ? `<div style="margin-top:20px;padding:16px;border-radius:12px;background:var(--yc-code-bg);color:#f4f2ff;">
                 <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:8px;">
                   <strong style="color:#ff9ec0;font-size:13px;">
                     配对就绪 · ${STATE.lastP2PResult.hostApplied && STATE.lastP2PResult.visitorApplied ? "已双端远控自动下发并热重载！" : STATE.lastP2PResult.hostApplied ? "已下发被访问端，请将下方 Visitor 配置交给发起端本地导入" : STATE.lastP2PResult.visitorApplied ? "已下发访问发起端，请确保被访问端也配置对应代理规则" : "已生成配对配置片段（未自动下发被访问端）"}
                   </strong>
                   <button type="button" class="yc-btn yc-btn-ghost-dark yc-btn-sm" id="copyP2PSnippetBtn">复制对端 TOML</button>
                 </div>
                 <pre style="margin:0;font-family:var(--yc-mono);font-size:12px;line-height:1.6;white-space:pre-wrap;">${esc(STATE.lastP2PResult.visitorTomlSnippet || "")}</pre>
               </div>`
            : ""
        }
      </div>`;

    const srvSel = main.querySelector("#p2pSrvSel");
    if (srvSel) {
      srvSel.addEventListener("change", () => {
        STATE.selectedServerId = srvSel.value;
        renderP2PWizardPane(main);
      });
    }

    main.querySelector("#p2pExecuteBtn").addEventListener("click", async () => {
      try {
        const res = await apiFetch("/api/remote/p2p-pair", {
          method: "POST",
          body: {
            serverId: main.querySelector("#p2pSrvSel").value,
            tunnelBaseName: main.querySelector("#p2pBaseName").value,
            p2pType: main.querySelector("#p2pTypeSel").value,
            secretKey: main.querySelector("#p2pSecret").value,
            hostClientId: main.querySelector("#p2pHostSel").value,
            hostLocalIP: main.querySelector("#p2pHostIP").value,
            hostLocalPort: parseInt(main.querySelector("#p2pHostPort").value, 10) || 7777,
            visitorClientId: main.querySelector("#p2pVisitorSel").value,
            visitorBindIP: "127.0.0.1",
            visitorBindPort: parseInt(main.querySelector("#p2pVisitorPort").value, 10) || 17777,
            keepTunnelOpen: main.querySelector("#p2pKeepOpen").checked,
            enableStcpFallback: main.querySelector("#p2pFallback").checked,
            overwrite: main.querySelector("#p2pOverwrite").checked
          }
        });
        STATE.lastP2PResult = res;
        toast("P2P 协同编排已完成！");
        await refreshState(true);
      } catch (err) {
        toast(err.message, true);
      }
    });

    const cpBtn = main.querySelector("#copyP2PSnippetBtn");
    if (cpBtn && STATE.lastP2PResult) {
      cpBtn.addEventListener("click", () => copyText(STATE.lastP2PResult.visitorTomlSnippet, "对端 P2P Visitor 配置"));
    }
  }


  // ==================== 7. View 5: SYSTEM, Hot-Update & 35 Curated Gallery ====================
  function renderSystemWorkspace() {
    const sb = $("systemSidebar");
    const main = $("systemMainPane");
    if (!sb || !main) return;

    sb.innerHTML = `
      <div class="yc-side-box">
        <div class="yc-side-heading"><span>应用与画廊目录</span></div>
        <div class="yc-dir-list">
          ${[
            ["schemes", "s_30.webp", "01 · 多配置方案管理", "独立 JSON 方案与导入导出"],
            ["updates", "s_32.webp", "02 · 原画包与核心热更新", "GitHub 原画拉取 · FRP & 本体升级"],
            ["gallery", "s_19.webp", "03 · 约战 4K 与动图画廊", "39 项新精选 + 6 动图 + 经典馆藏"],
            ["security", "s_33.webp", "04 · 面板端口与账户安全", "服务端口修改 · 四项改密 · 后台控制"]
          ]
            .map(
              ([k, img, t, s]) => `
            <button type="button" class="yc-dir-item${STATE.systemTab === k ? " is-active" : ""}" data-sys-tab="${k}">
              <img src="${assetUrl("assets/" + img)}" alt="" class="yc-dir-thumb" />
              <div class="yc-dir-meta">
                <div class="yc-dir-title">${esc(t)}</div>
                <div class="yc-dir-sub">${esc(s)}</div>
              </div>
            </button>`
            )
            .join("")}
        </div>
      </div>`;

    sb.querySelectorAll("[data-sys-tab]").forEach((b) => {
      b.addEventListener("click", () => {
        STATE.systemTab = b.dataset.sysTab;
        renderSystemWorkspace();
      });
    });

    if (STATE.systemTab === "schemes") {
      renderSystemSchemesPane(main);
    } else if (STATE.systemTab === "updates") {
      renderSystemUpdatesPane(main);
    } else if (STATE.systemTab === "gallery") {
      renderSystemGalleryPane(main);
    } else {
      renderSystemSecurityPane(main);
    }
  }

  function renderSystemSchemesPane(main) {
    main.innerHTML = `
      <div class="yc-card">
        <div style="display:flex;justify-content:space-between;align-items:center;flex-wrap:wrap;gap:10px;margin-bottom:16px;">
          <div>
            <h3 style="margin:0;font-family:var(--yc-serif);font-size:20px;">独立工作区配置方案 (data/configs/*.json)</h3>
            <span style="font-size:12.5px;color:var(--yc-muted);">支持在同一软件内保存多套互不干扰的服务端/客户端环境方案，零磨损按需切换。</span>
          </div>
          <div style="display:flex;gap:8px;flex-wrap:wrap;">
            <button type="button" class="yc-btn yc-btn-primary yc-btn-sm" id="newSchemeModalBtn">+ 新建配置方案</button>
            <button type="button" class="yc-btn yc-btn-outline yc-btn-sm" id="importSchemeJsonBtn">导入方案 JSON</button>
            <button type="button" class="yc-btn yc-btn-outline yc-btn-sm" id="exportSchemeJsonBtn">导出当前 JSON</button>
          </div>
        </div>
        <div class="yc-table-wrap">
          <table class="yc-table">
            <thead>
              <tr><th>方案名称</th><th>文件名</th><th>FRPS / FRPC 数量</th><th>最近更新</th><th>操作</th></tr>
            </thead>
            <tbody>
              ${STATE.configFiles
                .map(
                  (f) => `
                <tr>
                  <td><strong>${esc(f.displayName)}</strong> ${f.active ? `<span class="yc-tag yc-tag-ok">当前启用</span>` : ""}</td>
                  <td><code>${esc(f.fileName)}</code></td>
                  <td>${f.serverCount} 服务端 / ${f.clientCount} 客户端</td>
                  <td style="font-size:12px;color:var(--yc-muted);">${esc((f.updated || "").slice(0, 19).replace("T", " "))}</td>
                  <td>
                    ${
                      f.active
                        ? `<span style="font-size:12px;color:var(--yc-subtle);">使用中</span>`
                        : `<div style="display:flex;gap:6px;">
                             <button type="button" class="yc-btn yc-btn-outline yc-btn-sm" data-switch-cfg="${esc(f.fileName)}">切换启用</button>
                             <button type="button" class="yc-btn yc-btn-danger yc-btn-sm" data-del-cfg="${esc(f.fileName)}">删除</button>
                           </div>`
                    }
                  </td>
                </tr>`
                )
                .join("")}
            </tbody>
          </table>
        </div>
      </div>`;

    main.querySelector("#importSchemeJsonBtn").addEventListener("click", () => {
      openModal(
        `<div class="yc-modal-head">
           <h3 class="yc-modal-title">导入工作区配置方案 JSON</h3>
           <button type="button" class="yc-btn yc-btn-outline yc-btn-sm" data-modal-close>关闭</button>
         </div>
         <label class="yc-field">
           <span class="yc-field-label">选择导出的 JSON 方案文件</span>
           <input id="impJsonFile" type="file" accept=".json,application/json" />
         </label>
         <label class="yc-field">
           <span class="yc-field-label">保存方案文件名 (留空自动按原文件名保存)</span>
           <input id="impJsonSlug" type="text" placeholder="如 backup-prod" />
         </label>
         <div class="yc-check-row" style="margin:8px 0 4px;">
           <label class="yc-switch-label">
             <input id="impJsonActivate" type="checkbox" checked />
             <span>导入后立即切换启用该方案</span>
           </label>
         </div>
         <div class="yc-modal-foot">
           <button type="button" class="yc-btn yc-btn-outline" data-modal-close>取消</button>
           <button type="button" id="impJsonConfirmBtn" class="yc-btn yc-btn-primary">上传并导入</button>
         </div>`,
        (dlg) => {
          const fileInp = dlg.querySelector("#impJsonFile");
          const slugInp = dlg.querySelector("#impJsonSlug");
          fileInp.addEventListener("change", () => {
            const f = fileInp.files && fileInp.files[0];
            if (f && !slugInp.value.trim()) {
              slugInp.value = f.name.replace(/\.json$/i, "");
            }
          });
          dlg.querySelector("#impJsonConfirmBtn").addEventListener("click", async () => {
            const f = fileInp.files && fileInp.files[0];
            if (!f) {
              toast("请先选择一个 .json 配置文件", true);
              return;
            }
            try {
              const txt = await f.text();
              const parsed = JSON.parse(txt);
              await apiFetch("/api/configs/import-json", {
                method: "POST",
                body: {
                  fileName: slugInp.value.trim() || f.name,
                  content: parsed,
                  activate: dlg.querySelector("#impJsonActivate").checked
                }
              });
              closeModal();
              toast("已成功导入工作区配置方案！");
              await refreshState(true);
            } catch (err) {
              toast("导入 JSON 失败: " + err.message, true);
            }
          });
        },
        480
      );
    });
    main.querySelector("#exportSchemeJsonBtn").addEventListener("click", async () => {
      try {
        const docData = await apiFetch("/api/configs/export-json");
        const blob = new Blob([typeof docData === "string" ? docData : JSON.stringify(docData, null, 2)], { type: "application/json" });
        const url = URL.createObjectURL(blob);
        const a = document.createElement("a");
        a.href = url;
        a.download = STATE.activeConfigFile || "kurumifrp-config.json";
        document.body.appendChild(a);
        a.click();
        document.body.removeChild(a);
        URL.revokeObjectURL(url);
        toast("已导出当前配置方案 JSON");
      } catch (err) {
        toast(err.message, true);
      }
    });
    main.querySelector("#newSchemeModalBtn").addEventListener("click", () => {
      openModal(
        `<div class="yc-modal-head">
           <h3 class="yc-modal-title">新建独立工作区配置方案</h3>
           <button type="button" class="yc-btn yc-btn-outline yc-btn-sm" data-modal-close>关闭</button>
         </div>
         <label class="yc-field">
           <span class="yc-field-label">方案英文/中文文件名标识</span>
           <input id="nsSlug" type="text" placeholder="如 lab-mesh" />
         </label>
         <label class="yc-field">
           <span class="yc-field-label">方案显示名称</span>
           <input id="nsTitle" type="text" placeholder="如 实验室联调方案" />
         </label>
         <div class="yc-modal-foot">
           <button type="button" class="yc-btn yc-btn-outline" data-modal-close>取消</button>
           <button type="button" id="nsCreateBtn" class="yc-btn yc-btn-primary">创建并切换</button>
         </div>`,
        (dlg) => {
          dlg.querySelector("#nsCreateBtn").addEventListener("click", async () => {
            try {
              await apiFetch("/api/configs/select", {
                method: "POST",
                body: { fileName: dlg.querySelector("#nsSlug").value, displayName: dlg.querySelector("#nsTitle").value }
              });
              closeModal();
              toast("已创建并切换至新配置方案");
              await refreshState(true);
            } catch (err) {
              toast(err.message, true);
            }
          });
        },
        460
      );
    });

    main.querySelectorAll("[data-switch-cfg]").forEach((b) => {
      b.addEventListener("click", async () => {
        try {
          await apiFetch("/api/configs/select", { method: "POST", body: { fileName: b.dataset.switchCfg } });
          toast("已切换配置方案");
          await refreshState(true);
        } catch (err) {
          toast(err.message, true);
        }
      });
    });

    main.querySelectorAll("[data-del-cfg]").forEach((b) => {
      b.addEventListener("click", () => {
        const fn = b.dataset.delCfg;
        confirmModal("删除配置方案", `确定删除方案文件 ${fn} 吗？`, "确认删除", async () => {
          try {
            await apiFetch("/api/configs/delete", { method: "POST", body: { fileName: fn } });
            toast("已删除方案文件");
            await refreshState(true);
          } catch (err) {
            toast(err.message, true);
          }
        });
      });
    });
  }

  function buildHDAssetPackCardHTML() {
    const hd = STATE.hdAssets || {};
    const isHD = hd.activeTier === "hd";
    const pct = hd.downloading && hd.progressTotal > 0 ? Math.min(100, Math.round((hd.progressFiles / hd.progressTotal) * 100)) : 0;
    return `
      <div class="yc-hd-card">
        <div style="display:flex;justify-content:space-between;align-items:flex-start;flex-wrap:wrap;gap:12px;">
          <div>
            <div style="display:flex;align-items:center;gap:8px;flex-wrap:wrap;">
              <h3 style="margin:0;font-family:var(--yc-serif);font-size:19px;">WebUI 视觉素材画质与 GitHub 原画质包</h3>
              <span class="yc-hd-pill ${isHD ? "yc-hd-pill-hd" : "yc-hd-pill-std"}">
                ${isHD ? "★ 当前生效：GitHub 原画质 (HD)" : "当前生效：内置轻量压缩 (WebP)"}
              </span>
            </div>
            <p style="margin:6px 0 0;font-size:12.5px;color:var(--yc-muted);line-height:1.55;">
              安装包默认仅内嵌轻量压缩 WebP（共 <code>${formatBytes(hd.embeddedBytes || 2450000)}</code>）以保持极速启动与小巧体积；你可以随时从仓库
              <code>${esc(hd.sourceRepo || "Presikid/KurumiFrp")}/${esc(hd.sourcePath || "assets/material/webui_v094")}</code>
              一键按需拉取无损级高清原画与高分透明贴纸包（逐张 SHA-256 校验，本地按需覆盖，无需重启服务）。
            </p>
          </div>
          <div class="yc-hd-tier-switch" role="group" aria-label="画质档位切换">
            <button type="button" class="yc-hd-tier-btn ${!isHD ? "is-active" : ""}" data-hd-mode="embedded" ${hd.downloading ? "disabled" : ""}>
              内置压缩画质 (${formatBytes(hd.embeddedBytes || 2450000)})
            </button>
            <button type="button" class="yc-hd-tier-btn ${isHD ? "is-active-hd" : ""}" data-hd-mode="hd" ${!hd.installed || hd.downloading ? "disabled title='请先从 GitHub 拉取原画包'" : ""}>
              GitHub 原画质 (${hd.installed ? formatBytes(hd.installedBytes) : "未拉取"})
            </button>
          </div>
        </div>

        <div class="yc-grid-2" style="margin-top:14px;gap:12px;">
          <div style="padding:12px 14px;border-radius:10px;background:#ffffff;border:1px solid var(--yc-border);">
            <div style="font-size:12px;color:var(--yc-muted);">默认内嵌压缩方案 (随软件自带)</div>
            <div style="font-size:16px;font-weight:700;margin:3px 0;">${hd.expectedCount || 35} 张轻量 WebP · ${formatBytes(hd.embeddedBytes || 2450000)}</div>
            <div style="font-size:12px;color:var(--yc-ok);">零外网依赖 · 开箱即用</div>
          </div>
          <div style="padding:12px 14px;border-radius:10px;background:#ffffff;border:1px solid var(--yc-border);">
            <div style="font-size:12px;color:var(--yc-muted);">GitHub 仓库原画质素材包 (${esc(hd.sourceRef || "main")})</div>
            <div style="font-size:16px;font-weight:700;margin:3px 0;">
              ${hd.installed ? `已就绪 ${hd.installedCount} / ${hd.expectedCount || 35} 张 · ${formatBytes(hd.installedBytes)}` : "本地尚未下载原画包"}
            </div>
            <div style="font-size:12px;color:${hd.installed ? "var(--yc-rose)" : "var(--yc-muted)"};">
              ${hd.installed ? "SHA-256 校验通过 · 支持随时无感秒切" : "拉取后自动保存至 data/hd_assets/ 目录"}
            </div>
          </div>
        </div>

        ${
          hd.downloading
            ? `<div style="margin-top:14px;padding:12px 14px;border-radius:10px;background:#fff4f8;border:1px solid var(--yc-rose-border);">
                 <div style="display:flex;justify-content:space-between;align-items:center;font-size:12.5px;font-weight:600;color:var(--yc-indigo-ink);">
                   <span>正在拉取并校验 GitHub 原画包：${hd.progressFiles} / ${hd.progressTotal || 35} 张 (${pct}%) ${hd.currentAsset ? "· " + esc(hd.currentAsset) : ""}</span>
                   <span>${formatBytes(hd.progressBytes)} / ${hd.expectedBytes ? formatBytes(hd.expectedBytes) : "计算中"}</span>
                 </div>
                 <div class="yc-progress-track"><div class="yc-progress-fill" style="width:${pct}%;"></div></div>
                 <div style="display:flex;justify-content:flex-end;margin-top:6px;">
                   <button type="button" class="yc-btn yc-btn-outline yc-btn-sm" data-hd-cancel>取消拉取 (保留已下载项)</button>
                 </div>
               </div>`
            : ""
        }

        ${
          !hd.downloading && hd.lastError
            ? `<div class="yc-notice yc-notice-danger" style="margin-top:12px;">${esc(hd.lastError)}</div>`
            : ""
        }

        <div style="display:flex;gap:8px;flex-wrap:wrap;align-items:center;margin-top:14px;">
          <button type="button" class="yc-btn yc-btn-primary" data-hd-pull ${hd.downloading ? "disabled" : ""}>
            ${hd.installed ? "重新校验 / 同步 GitHub 原画包" : "⬇ 从 GitHub 仓库拉取原画质素材包"}
          </button>
          ${
            hd.installed
              ? `<button type="button" class="yc-btn yc-btn-outline" data-hd-purge ${hd.downloading ? "disabled" : ""}>
                   清理本地原画缓存 (切回内置压缩包)
                 </button>`
              : ""
          }
          <span style="font-size:12px;color:var(--yc-muted);">自动复用下方已保存的 GitHub 代理或本机系统代理 ${STATE.systemProxy ? "(" + esc(STATE.systemProxy) + ")" : ""}</span>
        </div>
      </div>`;
  }

  function wireHDAssetCardEvents(container, proxyInputSelector) {
    container.querySelectorAll("[data-hd-pull]").forEach((btn) => {
      btn.addEventListener("click", async () => {
        try {
          const proxyEl = proxyInputSelector ? container.querySelector(proxyInputSelector) : null;
          const body = {};
          if (proxyEl) body.proxyURL = proxyEl.value;
          const st = await apiFetch("/api/assets/hd/pull", { method: "POST", body });
          STATE.hdAssets = Object.assign({}, STATE.hdAssets, st || {});
          toast("已开始从 GitHub 仓库并发拉取原画质素材包…");
          syncHDAssetPoll();
          renderActiveView();
        } catch (err) {
          toast(err.message, true);
        }
      });
    });

    container.querySelectorAll("[data-hd-cancel]").forEach((btn) => {
      btn.addEventListener("click", async () => {
        try {
          const st = await apiFetch("/api/assets/hd/cancel", { method: "POST", body: {} });
          STATE.hdAssets = Object.assign({}, STATE.hdAssets, st || {});
          renderActiveView();
        } catch (err) {
          toast(err.message, true);
        }
      });
    });

    container.querySelectorAll("[data-hd-mode]").forEach((btn) => {
      btn.addEventListener("click", async () => {
        const wantHD = btn.dataset.hdMode === "hd";
        try {
          const st = await apiFetch("/api/assets/hd/mode", { method: "POST", body: { preferHD: wantHD } });
          STATE.hdAssets = Object.assign({}, STATE.hdAssets, st || {});
          refreshAllDOMAssetQuality();
          toast(wantHD ? "已切换为 GitHub 原画质显示！" : "已切换回内置轻量压缩画质！");
          renderActiveView();
        } catch (err) {
          toast(err.message, true);
        }
      });
    });

    container.querySelectorAll("[data-hd-purge]").forEach((btn) => {
      btn.addEventListener("click", () => {
        confirmModal(
          "清理本地原画质缓存",
          "确定删除本地已拉取的 GitHub 原画缓存文件并恢复为内置轻量压缩 WebP 画质吗？（后续可随时再次拉取）",
          "确认清理",
          async () => {
            try {
              const st = await apiFetch("/api/assets/hd/purge", { method: "POST", body: {} });
              STATE.hdAssets = Object.assign({}, STATE.hdAssets, st || {});
              refreshAllDOMAssetQuality();
              toast("已清理本地原画缓存并切回内置轻量画质");
              renderActiveView();
            } catch (err) {
              toast(err.message, true);
            }
          }
        );
      });
    });
  }

  function renderSystemUpdatesPane(main) {
    const core = STATE.core || {};
    const chk = STATE.lastUpdateCheck;
    main.innerHTML = `
      ${buildHDAssetPackCardHTML()}

      <div class="yc-card">
        <h3 style="margin:0 0 12px;font-family:var(--yc-serif);font-size:20px;">内置核心状态与一键无缝热更新</h3>
        <div class="yc-grid-2" style="margin-bottom:16px;">
          <div style="padding:14px;border-radius:10px;background:#faf9fe;border:1px solid var(--yc-border);">
            <div style="font-size:12px;color:var(--yc-muted);">KurumiFrp 当前版本</div>
            <div style="font-size:20px;font-weight:700;margin:4px 0;">v${esc(STATE.version)}</div>
            ${chk && chk.app ? `<div style="font-size:12.5px;color:${chk.app.hasUpdate ? "var(--yc-rose)" : "var(--yc-ok)"};">最新构建: v${esc(chk.app.latest)} ${chk.app.error ? "· " + esc(chk.app.error) : ""}</div>` : ""}
          </div>
          <div style="padding:14px;border-radius:10px;background:#faf9fe;border:1px solid var(--yc-border);">
            <div style="font-size:12px;color:var(--yc-muted);">上游官方 fatedier/frp 核心</div>
            <div style="font-size:20px;font-weight:700;margin:4px 0;">frps ${esc(core.frpsVersion || "未检测")} / frpc ${esc(core.frpcVersion || "未检测")}</div>
            <div style="font-size:12px;color:var(--yc-muted);">来源: <code>${esc(core.coreSource || "bundled")}</code></div>
          </div>
        </div>

        <label class="yc-field">
          <span class="yc-field-label">GitHub 下载代理 (同时供原画包拉取与核心升级使用，留空自动识别系统代理 ${STATE.systemProxy ? esc(STATE.systemProxy) : ""})</span>
          <div style="display:flex;gap:8px;flex-wrap:wrap;">
            <input id="updProxyInput" type="text" style="flex:1;min-width:240px;" value="${esc(STATE.updateProxy || "")}" placeholder="如 http://127.0.0.1:2080 或 socks5://127.0.0.1:10808" />
            <button type="button" class="yc-btn yc-btn-outline" id="btnSaveUpdProxy">保存代理</button>
            <button type="button" class="yc-btn yc-btn-primary" id="btnCheckUpdates">检查更新</button>
            <button type="button" class="yc-btn yc-btn-indigo" id="btnApplyFrpUpdate">热更新官方 FRP 核心</button>
            <button type="button" class="yc-btn yc-btn-outline" id="btnApplyAppUpdate">自更新 KurumiFrp</button>
          </div>
        </label>
      </div>`;

    wireHDAssetCardEvents(main, "#updProxyInput");

    main.querySelector("#btnSaveUpdProxy").addEventListener("click", async () => {
      try {
        await apiFetch("/api/update/settings", { method: "POST", body: { proxyURL: main.querySelector("#updProxyInput").value } });
        toast("GitHub 下载代理已保存");
        await refreshState(true);
      } catch (err) {
        toast(err.message, true);
      }
    });
    main.querySelector("#btnCheckUpdates").addEventListener("click", async () => {
      try {
        toast("正在检查 GitHub 最新版本…");
        STATE.lastUpdateCheck = await apiFetch("/api/update/check", {
          method: "POST",
          body: { proxyURL: main.querySelector("#updProxyInput").value }
        });
        toast("版本检查完成");
        renderSystemUpdatesPane(main);
      } catch (err) {
        toast(err.message, true);
      }
    });
    main.querySelector("#btnApplyFrpUpdate").addEventListener("click", async () => {
      try {
        toast("正在下载并 SHA-256 校验官方 FRP 核心…");
        const res = await apiFetch("/api/update/apply-frp", {
          method: "POST",
          body: { proxyURL: main.querySelector("#updProxyInput").value }
        });
        toast(`官方 FRP 核心热替换成功，已自动恢复 ${res.restarted || 0} 个运行实例！`);
        await refreshState(true);
      } catch (err) {
        toast(err.message, true);
      }
    });
    main.querySelector("#btnApplyAppUpdate").addEventListener("click", async () => {
      try {
        const res = await apiFetch("/api/update/apply-app", {
          method: "POST",
          body: { proxyURL: main.querySelector("#updProxyInput").value }
        });
        toast(`已启动 v${res.targetVersion} 静默升级，稍后自动重连…`);
      } catch (err) {
        toast(err.message, true);
      }
    });
  }

  function renderSystemGalleryPane(main) {
    const hd = STATE.hdAssets || {};
    const isHD = hd.activeTier === "hd";
    const items = KURUMI_ASSETS.filter((a) => {
      if (STATE.galleryCat === "all") return true;
      if (STATE.galleryCat === "sunburst") return a.pack === "sunburst";
      if (STATE.galleryCat === "gif") return Boolean(a.animated);
      if (STATE.galleryCat === "16:9") return a.cat === "16:9" || a.cat === "banner";
      return a.cat === STATE.galleryCat;
    });
    const pct = hd.downloading && hd.progressTotal > 0 ? Math.min(100, Math.round((hd.progressFiles / hd.progressTotal) * 100)) : 0;

    main.innerHTML = `
      <div class="yc-hd-banner">
        <div>
          <div style="display:flex;align-items:center;gap:8px;flex-wrap:wrap;">
            <strong style="font-size:14.5px;">画廊当前渲染画质：${isHD ? "GitHub 原画质素材包 (HD 无损级原图)" : "内置轻量压缩包 (标准 WebP)"}</strong>
            <span class="yc-hd-pill ${isHD ? "yc-hd-pill-hd" : "yc-hd-pill-std"}" style="${!isHD ? "background:rgba(255,255,255,0.16);color:#fff;" : ""}">
              ${isHD ? "HD ACTIVE · " + formatBytes(hd.installedBytes) : "EMBEDDED · " + formatBytes(hd.embeddedBytes || 2450000)}
            </span>
          </div>
          <div style="font-size:12px;color:rgba(255,255,255,0.78);margin-top:3px;">
            ${
              hd.downloading
                ? `正在从 GitHub 仓库拉取原画：${hd.progressFiles} / ${hd.progressTotal || 35} (${pct}%) ${hd.currentAsset ? "· " + esc(hd.currentAsset) : ""}`
                : hd.installed
                  ? "本地已缓存全部 GitHub 原画与透明贴纸，可随时一键切换对比清晰度。"
                  : "默认使用内置压缩图以节省安装包体积；点击右侧按钮即可从 GitHub 仓库拉取原画质素材包。"
            }
          </div>
        </div>
        <div style="display:flex;gap:8px;flex-wrap:wrap;align-items:center;">
          ${
            hd.installed
              ? `<button type="button" class="yc-btn ${isHD ? "yc-btn-outline" : "yc-btn-primary"} yc-btn-sm" style="${isHD ? "background:#fff;color:#111636;border-color:#fff;" : ""}" data-hd-mode="${isHD ? "embedded" : "hd"}">
                   ${isHD ? "切回内置压缩画质" : "启用 GitHub 原画质"}
                 </button>`
              : `<button type="button" class="yc-btn yc-btn-primary yc-btn-sm" data-hd-pull ${hd.downloading ? "disabled" : ""}>
                   ${hd.downloading ? "拉取中 (" + pct + "%)…" : "⬇ 从 GitHub 拉取原画质包"}
                 </button>`
          }
          <button type="button" class="yc-btn yc-btn-outline yc-btn-sm" style="color:#fff;border-color:rgba(255,255,255,0.35);background:transparent;" id="btnJumpHDSettings">
            画质与代理设置 →
          </button>
        </div>
      </div>

      <div class="yc-card">
        <div style="display:flex;justify-content:space-between;align-items:center;flex-wrap:wrap;gap:10px;margin-bottom:18px;">
          <div>
            <h3 style="margin:0;font-family:var(--yc-serif);font-size:20px;">《约会大作战》× 时崎狂三精选画廊 (在线 74 项 · 另归档 26 张 4K/2K 储备原画)</h3>
            <span style="font-size:12.5px;color:var(--yc-muted);">已接入 39 项 Sunburst 4K/2K/透明立绘与 12 帧无缝循环动图（仓库 <code>assets/material/sunburst_dal/</code> 完整归档全部 65 项保留精品）。</span>
          </div>
          <div style="display:flex;gap:6px;flex-wrap:wrap;">
            ${[
              ["sunburst", "★ Sunburst 新精选 (39)"],
              ["gif", "动态循环动图 (6)"],
              ["16:9", "16:9 主舞台与顶栏 (27)"],
              ["3:4", "3:4 竖版立绘卡 (22)"],
              ["1:1", "1:1 透明贴纸 (14)"],
              ["all", "全部在线馆藏 (74)"]
            ]
              .map(
                ([k, l]) => `<button type="button" class="yc-btn ${STATE.galleryCat === k ? "yc-btn-indigo" : "yc-btn-outline"} yc-btn-sm" data-gal-cat="${k}">${l}</button>`
              )
              .join("")}
          </div>
        </div>

        <div class="yc-gallery-wall">
          ${items
            .map(
              (a, i) => {
                const aKey = a.key || ("k_" + a.id); const dimLabel = isHD && hd.dimensions && hd.dimensions[aKey] ? hd.dimensions[aKey] : (a.animated ? "GIF LOOP" : a.cat);
                return `
            <div class="yc-gallery-tile" data-gal-open="${esc(a.id)}">
              <div class="yc-gallery-img-wrap">
                <img src="${assetUrl(a.src)}" alt="${esc(a.title)}" class="${a.sticker ? "is-sticker" : ""}" />
              </div>
              <div class="yc-gallery-caption">
                <span>${pad2(i + 1)} · ${esc(a.title)}</span>
                <span class="yc-tag">${esc(dimLabel)}</span>
              </div>
            </div>`;
              }
            )
            .join("")}
        </div>
      </div>`;

    wireHDAssetCardEvents(main, null);
    const jumpBtn = main.querySelector("#btnJumpHDSettings");
    if (jumpBtn) {
      jumpBtn.addEventListener("click", () => {
        STATE.systemTab = "updates";
        renderSystemWorkspace();
      });
    }

    main.querySelectorAll("[data-gal-cat]").forEach((b) => {
      b.addEventListener("click", () => {
        STATE.galleryCat = b.dataset.galCat;
        renderSystemGalleryPane(main);
      });
    });

    main.querySelectorAll("[data-gal-open]").forEach((tile) => {
      tile.addEventListener("click", () => {
        const art = KURUMI_ASSETS.find((x) => x.id === tile.dataset.galOpen);
        if (!art) return;
        const heroIdx = HERO_SLIDES.findIndex((h) => h.id === art.id);
        const dim = (STATE.hdAssets.dimensions || {})[art.key || ("k_" + art.id)];
        const qualityText = STATE.hdAssets.activeTier === "hd"
          ? `GitHub 原画质已启用${dim ? " · " + dim + " px" : ""}`
          : "当前为内置轻量压缩 WebP（可在画廊顶部一键拉取 GitHub 原画包）";
        openModal(
          `<div class="yc-modal-head">
             <div>
               <h3 class="yc-modal-title">#${art.id} · ${esc(art.title)} (${art.cat})</h3>
               <div style="font-size:12px;color:var(--yc-muted);margin-top:2px;">${esc(qualityText)}</div>
             </div>
             <button type="button" class="yc-btn yc-btn-outline yc-btn-sm" data-modal-close>关闭</button>
           </div>
           <div style="text-align:center;background:#111636;border-radius:12px;padding:14px;">
             <img src="${assetUrl(art.src)}" alt="${esc(art.title)}" style="max-width:100%;max-height:68vh;border-radius:8px;object-fit:contain;" />
           </div>
           <div class="yc-modal-foot">
             ${heroIdx >= 0 ? `<button type="button" id="setHeroNowBtn" class="yc-btn yc-btn-primary">设为主舞台当前展示壁纸</button>` : ""}
             <button type="button" class="yc-btn yc-btn-outline" data-modal-close>返回画廊</button>
           </div>`,
          (dlg) => {
            const hb = dlg.querySelector("#setHeroNowBtn");
            if (hb) {
              hb.addEventListener("click", () => {
                STATE.slideIdx = heroIdx;
                syncHomeSlideIndicators();
                closeModal();
                switchView("home");
              });
            }
          },
          880
        );
      });
    });
  }

  function renderSystemSecurityPane(main) {
    const p = STATE.panel || {};
    const curPort = Number(p.currentPort || p.savedPort || 9000);
    const curHost = p.bindHost || p.currentHost || "127.0.0.1";
    const isStandardHost = curHost === "127.0.0.1" || curHost === "0.0.0.0";

    main.innerHTML = `
      <div class="yc-grid-2" style="align-items:start;gap:20px;">
        <!-- Card 1: Panel Service Port & Bind Scope Settings -->
        <div class="yc-card">
          <div style="display:flex;justify-content:space-between;align-items:center;flex-wrap:wrap;gap:8px;margin-bottom:12px;">
            <h3 style="margin:0;font-family:var(--yc-serif);font-size:20px;">面板服务端口与监听设置</h3>
            <span class="yc-tag yc-tag-ok">当前监听：${esc(p.listenAddr || (curHost + ":" + curPort))}</span>
          </div>
          <p style="margin:0 0 14px;font-size:12.5px;color:var(--yc-muted);line-height:1.55;">
            修改后将预先检测目标端口可用性并立即<strong>零停机热切换</strong>面板服务端口（所有运行中的 <code>frps</code> / <code>frpc</code> 隧道均不会中断），同时持久化保存至 <code>data/panel_settings.json</code>，重启软件后继续生效。
          </p>

          <div class="yc-field">
            <label class="yc-field-label" for="panelPortInput">面板服务端口 (1 ~ 65535)</label>
            <input id="panelPortInput" type="number" min="1" max="65535" step="1" value="${curPort}" placeholder="默认 9000" />
          </div>

          <div class="yc-field">
            <label class="yc-field-label" for="panelBindHostSelect">面板监听网卡范围</label>
            <select id="panelBindHostSelect">
              <option value="127.0.0.1" ${curHost === "127.0.0.1" ? "selected" : ""}>仅本机回环访问 (127.0.0.1 · 桌面端安全默认)</option>
              <option value="0.0.0.0" ${curHost === "0.0.0.0" ? "selected" : ""}>允许局域网与公网访问 (0.0.0.0 · 云服务器远程访问)</option>
              ${!isStandardHost ? `<option value="${esc(curHost)}" selected>保持当前自定义网卡 (${esc(curHost)})</option>` : ""}
            </select>
          </div>

          <div id="panelPortNotice" class="yc-notice yc-notice-pink" hidden></div>

          <div style="display:flex;gap:10px;margin-top:16px;flex-wrap:wrap;align-items:center;">
            <button type="button" class="yc-btn yc-btn-primary" id="panelSavePortBtn">保存并切换面板端口</button>
            <button type="button" class="yc-btn yc-btn-danger" id="secQuitDaemonBtn">优雅停止后台服务并退出</button>
          </div>
        </div>

        <!-- Card 2: 4-Field Administrator Credentials Form with Eye Toggles -->
        <div class="yc-card">
          <div style="display:flex;justify-content:space-between;align-items:center;flex-wrap:wrap;gap:8px;margin-bottom:12px;">
            <h3 style="margin:0;font-family:var(--yc-serif);font-size:20px;">管理员账号与密码修改</h3>
            <span class="yc-tag ${STATE.needsPasswordSetup ? "yc-tag-warn" : "yc-tag-ok"}">
              ${STATE.needsPasswordSetup ? "当前为空密码 · 建议立即设密" : "PBKDF2-SHA256 保护中"}
            </span>
          </div>

          <div class="yc-field">
            <label class="yc-field-label" for="secCurPass">
              原密码 ${STATE.needsPasswordSetup ? '<span style="font-weight:400;color:var(--yc-muted);">（当前尚未设密，留空即可）</span>' : '<span style="color:var(--yc-rose);">*</span>'}
            </label>
            <div class="yc-pass-wrap">
              <input id="secCurPass" type="password" autocomplete="current-password" placeholder="${STATE.needsPasswordSetup ? "首次未设密码可留空" : "请输入当前正在使用的原密码"}" />
              <button type="button" class="yc-eye-btn" data-eye-for="secCurPass"></button>
            </div>
          </div>

          <div class="yc-field">
            <label class="yc-field-label" for="secNewUser">新用户名 <span style="color:var(--yc-rose);">*</span></label>
            <input id="secNewUser" type="text" maxlength="64" autocomplete="username" value="${esc(STATE.username)}" placeholder="请输入新管理员用户名 (1~64 位，不含空格)" />
          </div>

          <div class="yc-field">
            <label class="yc-field-label" for="secNewPass">新密码 <span style="color:var(--yc-rose);">*</span></label>
            <div class="yc-pass-wrap">
              <input id="secNewPass" type="password" autocomplete="new-password" placeholder="请输入新登录密码（点击右侧小眼睛可查看明文）" />
              <button type="button" class="yc-eye-btn" data-eye-for="secNewPass"></button>
            </div>
          </div>

          <div class="yc-field">
            <label class="yc-field-label" for="secConfirmPass">新密码确认 <span style="color:var(--yc-rose);">*</span></label>
            <div class="yc-pass-wrap">
              <input id="secConfirmPass" type="password" autocomplete="new-password" placeholder="请再次输入相同的新密码" />
              <button type="button" class="yc-eye-btn" data-eye-for="secConfirmPass"></button>
            </div>
            <div id="secPassMatchHint" class="yc-pass-hint"></div>
          </div>

          <div style="display:flex;gap:10px;margin-top:14px;flex-wrap:wrap;">
            <button type="button" class="yc-btn yc-btn-primary" id="secSaveBtn">确认修改账号与密码</button>
          </div>
        </div>
      </div>`;

    enhancePasswordEyes(main);

    const newPassEl = main.querySelector("#secNewPass");
    const confirmPassEl = main.querySelector("#secConfirmPass");
    const matchHintEl = main.querySelector("#secPassMatchHint");
    const checkPassMatch = () => {
      const p1 = newPassEl.value;
      const p2 = confirmPassEl.value;
      if (!p1 && !p2) {
        matchHintEl.textContent = "";
        matchHintEl.className = "yc-pass-hint";
        return;
      }
      if (!p2) {
        matchHintEl.textContent = "请继续输入确认密码";
        matchHintEl.className = "yc-pass-hint";
        return;
      }
      if (p1 === p2) {
        matchHintEl.textContent = "✓ 两次输入的新密码完全一致";
        matchHintEl.className = "yc-pass-hint is-ok";
      } else {
        matchHintEl.textContent = "✗ 两次输入的新密码不一致，请核对";
        matchHintEl.className = "yc-pass-hint is-err";
      }
    };
    newPassEl.addEventListener("input", checkPassMatch);
    confirmPassEl.addEventListener("input", checkPassMatch);

    main.querySelector("#secSaveBtn").addEventListener("click", async () => {
      const curPass = main.querySelector("#secCurPass").value;
      const nextUser = main.querySelector("#secNewUser").value.trim();
      const nextPass = newPassEl.value;
      const confPass = confirmPassEl.value;

      if (!STATE.needsPasswordSetup && !curPass) {
        toast("请输入原密码", true);
        main.querySelector("#secCurPass").focus();
        return;
      }
      if (!nextUser || /\s/.test(nextUser)) {
        toast("新用户名不能为空且不能包含空格", true);
        main.querySelector("#secNewUser").focus();
        return;
      }
      if (!nextPass) {
        toast("新密码不能为空", true);
        newPassEl.focus();
        return;
      }
      if (nextPass !== confPass) {
        toast("两次输入的新密码不一致，请点击小眼睛核对后再提交", true);
        confirmPassEl.focus();
        return;
      }

      const btn = main.querySelector("#secSaveBtn");
      btn.disabled = true;
      try {
        await apiFetch("/api/account", {
          method: "POST",
          body: {
            currentPassword: curPass,
            newUsername: nextUser,
            newPassword: nextPass,
            confirmPassword: confPass
          }
        });
        STATE.username = nextUser;
        $("loginUsername").value = nextUser;
        toast("管理员用户名与密码已更新，请使用新凭据重新登录");
        enterLoginGate("账号密码已成功修改，请使用新用户名与新密码登录");
      } catch (err) {
        toast(err.message, true);
      } finally {
        btn.disabled = false;
      }
    });

    main.querySelector("#panelSavePortBtn").addEventListener("click", async () => {
      const rawPort = Number(main.querySelector("#panelPortInput").value);
      const bindHost = main.querySelector("#panelBindHostSelect").value;
      const notice = main.querySelector("#panelPortNotice");

      if (!Number.isInteger(rawPort) || rawPort < 1 || rawPort > 65535) {
        toast("请输入 1 ~ 65535 之间的有效端口号", true);
        main.querySelector("#panelPortInput").focus();
        return;
      }

      const saveBtn = main.querySelector("#panelSavePortBtn");
      saveBtn.disabled = true;
      try {
        const res = await apiFetch("/api/panel/settings", {
          method: "POST",
          body: { port: rawPort, bindHost }
        });
        if (res.panel) {
          STATE.panel = Object.assign({}, STATE.panel, res.panel);
        }
        const newPort = (res.panel && res.panel.currentPort) || rawPort;
        if (!res.changed) {
          toast("面板端口配置已保存（端口未变动）");
          renderSystemSecurityPane(main);
          return;
        }
        const canNavigate =
          (window.location.protocol === "http:" || window.location.protocol === "https:") &&
          window.location.hostname &&
          !window.__KURUMI_DISABLE_SSE__;
        if (canNavigate) {
          const curHostname = window.location.hostname;
          const csMatch = curHostname.match(/^(.*)-(\d+)(\.(?:app\.github\.dev|preview\.app\.github\.dev|githubpreview\.dev))$/i);
          const isReverseProxyHTTPS = window.location.protocol === "https:" && !window.location.port && !csMatch;
          if (isReverseProxyHTTPS) {
            const actualAddr = (res.panel && res.panel.listenAddr) || (bindHost + ":" + newPort);
            notice.hidden = false;
            notice.innerHTML = `✓ 后端监听已热切换至 <code>${esc(actualAddr)}</code>（当前通过无端口 HTTPS 反向代理访问，请同步更新反代上游端口）。`;
            toast(`后端面板端口已切换至 ${newPort}`);
          } else {
            let nextURL = "";
            if (csMatch) {
              nextURL = `${window.location.protocol}//${csMatch[1]}-${newPort}${csMatch[3]}/`;
            } else {
              const isLocalHost = curHostname === "127.0.0.1" || curHostname === "localhost" || curHostname === "::1" || curHostname === "[::1]";
              let targetHost = bindHost === "127.0.0.1" && isLocalHost && curHostname !== "localhost" ? "127.0.0.1" : curHostname;
              if (targetHost.includes(":") && !targetHost.startsWith("[")) targetHost = `[${targetHost}]`;
              nextURL = `${window.location.protocol}//${targetHost}:${newPort}/`;
            }
            notice.hidden = false;
            notice.innerHTML = `✓ 面板端口已平滑切换，新访问地址：<a href="${esc(nextURL)}" style="color:var(--yc-rose);font-weight:700;">${esc(nextURL)}</a>（正在自动跳转…）`;
            toast(`面板端口已切换至 ${newPort}，即将自动跳转…`);
            setTimeout(() => {
              window.location.href = nextURL;
            }, 600);
          }
        } else {
          toast(`面板监听已切换至 ${(res.panel && res.panel.listenAddr) || (bindHost + ":" + newPort)}`);
          renderSystemSecurityPane(main);
        }
      } catch (err) {
        toast(err.message, true);
      } finally {
        saveBtn.disabled = false;
      }
    });

    main.querySelector("#secQuitDaemonBtn").addEventListener("click", () => {
      confirmModal("停止 KurumiFrp 后台进程", "确定停止所有正在运行的 frps/frpc 隧道并退出 KurumiFrp 服务吗？", "确认停止退出", async () => {
        try {
          await apiFetch("/api/quit", { method: "POST" });
          toast("KurumiFrp 后台进程已优雅停止");
        } catch (err) {
          toast(err.message, true);
        }
      });
    });
  }

  // ==================== Cover Picker & TOML Import Modals & Live SSE Logs ====================
  function openCoverPickerModal(currentAsset, onPick) {
    openModal(
      `<div class="yc-modal-head">
         <h3 class="yc-modal-title">选择实例卡片狂三竖版立绘 (3:4 精选)</h3>
         <button type="button" class="yc-btn yc-btn-outline yc-btn-sm" data-modal-close>关闭</button>
       </div>
       <div class="yc-gallery-wall" style="grid-template-columns:repeat(auto-fill,minmax(145px,1fr));">
         ${PORTRAIT_COVERS.map(
           (a, i) => `
           <div class="yc-gallery-tile" data-pick-cover="k_${a.id}" style="${currentAsset === "k_" + a.id ? "border-color:var(--yc-rose);background:var(--yc-rose-tint);" : ""}">
             <div class="yc-framed-portrait" style="border-width:3px;">
               <img src="${assetUrl(a.src)}" alt="${esc(a.title)}" />
               <span class="yc-portrait-num">${pad2(i + 1)}</span>
             </div>
             <div style="margin-top:6px;font-size:12px;font-weight:600;text-align:center;">${esc(a.title)}</div>
           </div>`
         ).join("")}
       </div>`,
      (dlg) => {
        dlg.querySelectorAll("[data-pick-cover]").forEach((el) => {
          el.addEventListener("click", () => {
            closeModal();
            onPick(el.dataset.pickCover);
          });
        });
      },
      780
    );
  }

  function openImportTOMLModal(kind) {
    openModal(
      `<div class="yc-modal-head">
         <h3 class="yc-modal-title">导入标准 ${kind === "frps" ? "frps.toml" : "frpc.toml"} 配置文件</h3>
         <button type="button" class="yc-btn yc-btn-outline yc-btn-sm" data-modal-close>关闭</button>
       </div>
       <label class="yc-field">
         <span class="yc-field-label">导入后实例名称</span>
         <input id="impName" type="text" value="${kind === "frps" ? "导入服务端配置" : "导入客户端配置"}" />
       </label>
       <label class="yc-field">
         <span class="yc-field-label">粘贴或选择本地 .toml 文件内容</span>
         <textarea id="impTomlText" rows="12" style="font-family:var(--yc-mono);font-size:12.5px;" placeholder="# 粘贴官方 ${kind}.toml 内容…"></textarea>
       </label>
       <div class="yc-modal-foot">
         <input type="file" id="impFileInput" accept=".toml,.ini,.txt" style="margin-right:auto;font-size:12px;" />
         <button type="button" class="yc-btn yc-btn-outline" data-modal-close>取消</button>
         <button type="button" id="impSubmitBtn" class="yc-btn yc-btn-primary">解析并创建实例</button>
       </div>`,
      (dlg) => {
        dlg.querySelector("#impFileInput").addEventListener("change", (e) => {
          const f = e.target.files && e.target.files[0];
          if (!f) return;
          const reader = new FileReader();
          reader.onload = () => {
            dlg.querySelector("#impTomlText").value = String(reader.result || "");
          };
          reader.readAsText(f);
        });
        dlg.querySelector("#impSubmitBtn").addEventListener("click", async () => {
          try {
            const res = await apiFetch("/api/configs/import-toml", {
              method: "POST",
              body: {
                name: dlg.querySelector("#impName").value,
                kind,
                toml: dlg.querySelector("#impTomlText").value
              }
            });
            closeModal();
            if (res.kind === "frps") STATE.selectedServerId = res.id || STATE.selectedServerId;
            else STATE.selectedClientId = res.id || STATE.selectedClientId;
            toast("TOML 配置已完整解析并导入！");
            await refreshState(true);
          } catch (err) {
            toast(err.message, true);
          }
        });
      },
      700
    );
  }

  function stopLogStream() {
    if (activeLogES) {
      try {
        activeLogES.close();
      } catch (_e) {}
      activeLogES = null;
    }
  }

  async function renderInstanceLogsBox(container, instanceId, isRunning) {
    stopLogStream();
    container.innerHTML = `
      <div class="yc-card">
        <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:12px;">
          <div style="display:flex;align-items:center;gap:10px;">
            <img src="${assetUrl("assets/" + (isRunning ? "k_54.webp" : "k_59.webp"))}" alt="" style="width:38px;height:38px;object-fit:contain;" />
            <div>
              <strong style="font-size:14px;">实例实时运行日志 (敏感密钥自动脱敏 · ANSI 已净化)</strong>
              <div style="font-size:12px;color:var(--yc-muted);">${isRunning ? "SSE 实时流连接中" : "当前实例未运行，展示最近缓冲日志"}</div>
            </div>
          </div>
          <button type="button" class="yc-btn yc-btn-outline yc-btn-sm" id="copyLogBtn">复制日志</button>
        </div>
        <div id="liveLogBox" class="yc-log-box">正在加载日志…</div>
      </div>`;

    const box = container.querySelector("#liveLogBox");
    container.querySelector("#copyLogBtn").addEventListener("click", () => copyText(box.textContent, "运行日志"));

    try {
      const snap = await apiFetch(`/api/logs?id=${encodeURIComponent(instanceId)}`);
      const lines = snap.lines || [];
      box.textContent = lines.length ? lines.join("\n") : "暂无日志输出。启动实例后官方核心输出将实时滚动显示于此。";
      box.scrollTop = box.scrollHeight;
    } catch (err) {
      box.textContent = "读取日志失败: " + err.message;
    }

    if (isRunning && typeof EventSource !== "undefined" && !window.__KURUMI_DISABLE_SSE__) {
      try {
        const es = new EventSource(`/api/logs/stream?id=${encodeURIComponent(instanceId)}`);
        let liveLines = [];
        let resetPending = true;
        es.onopen = () => {
          resetPending = true;
        };
        es.onmessage = (ev) => {
          if (resetPending) {
            liveLines = [];
            resetPending = false;
          }
          liveLines.push(ev.data);
          if (liveLines.length > 600) {
            liveLines = liveLines.slice(liveLines.length - 600);
          }
          box.textContent = liveLines.join("\n");
          box.scrollTop = box.scrollHeight;
        };
        activeLogES = es;
      } catch (_e) {}
    }
  }

  // ==================== Global Event Wiring ====================
  function bindStaticEvents() {
    $("loginForm").addEventListener("submit", async (e) => {
      e.preventDefault();
      const btn = $("loginSubmitBtn");
      btn.disabled = true;
      try {
        const res = await apiFetch("/api/login", {
          method: "POST",
          body: {
            username: $("loginUsername").value.trim(),
            password: $("loginPassword").value
          }
        });
        STATE.username = res.username || "admin";
        STATE.needsPasswordSetup = Boolean(res.needsPasswordSetup);
        $("loginPassword").value = "";
        await enterAuthenticatedApp();
      } catch (err) {
        const eb = $("loginErrorBox");
        eb.textContent = err.message;
        eb.hidden = false;
      } finally {
        btn.disabled = false;
      }
    });

    $("logoutBtn").addEventListener("click", async () => {
      try {
        await apiFetch("/api/logout", { method: "POST" });
      } catch (_e) {}
      enterLoginGate("您已安全退出控制台");
    });

    document.querySelectorAll("[data-nav]").forEach((el) => {
      el.addEventListener("click", () => switchView(el.dataset.nav));
    });
    document.querySelectorAll("[data-nav-jump]").forEach((el) => {
      el.addEventListener("click", () => switchView(el.dataset.navJump));
    });
    $("topSchemeBtn").addEventListener("click", () => {
      STATE.systemTab = "schemes";
      switchView("system");
    });

    $("homePrevSlideBtn").addEventListener("click", () => {
      STATE.slideIdx = (STATE.slideIdx - 1 + HERO_SLIDES.length) % HERO_SLIDES.length;
      syncHomeSlideIndicators();
    });
    $("homeNextSlideBtn").addEventListener("click", () => {
      STATE.slideIdx = (STATE.slideIdx + 1) % HERO_SLIDES.length;
      syncHomeSlideIndicators();
    });
    $("homeStripToggleBtn").addEventListener("click", () => {
      STATE.filmstripOpen = !STATE.filmstripOpen;
      $("homeFilmstrip").hidden = !STATE.filmstripOpen;
    });
    $("homeAutoBtn").addEventListener("click", () => {
      STATE.autoPlay = !STATE.autoPlay;
      const btn = $("homeAutoBtn");
      btn.classList.toggle("is-on", STATE.autoPlay);
      btn.textContent = STATE.autoPlay ? "AUTO 轮播 · ON" : "AUTO 轮播 · OFF";
    });

    $("btnNewServer").addEventListener("click", createBlankServer);
    $("btnImportServerToml").addEventListener("click", () => openImportTOMLModal("frps"));
    $("btnNewClient").addEventListener("click", createBlankClient);
    $("btnImportClientToml").addEventListener("click", () => openImportTOMLModal("frpc"));

    $("modalBackdrop").addEventListener("click", (e) => {
      if (e.target === $("modalBackdrop")) closeModal();
    });
  }

  window.addEventListener("DOMContentLoaded", () => {
    enhancePasswordEyes(document);
    if (typeof MutationObserver !== "undefined" && document.body) {
      const obs = new MutationObserver(() => enhancePasswordEyes(document));
      obs.observe(document.body, { childList: true, subtree: true });
      window.__kurumiEyeObserver = obs;
    }
    bindStaticEvents();
    bootSession();
  });
})();
