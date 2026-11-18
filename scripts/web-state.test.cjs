const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

const app = fs.readFileSync(path.join(__dirname, "../web/app.js"), "utf8");

function openApp(apiResponse) {
  const requests = [];
  const elements = new Map();
  function element() {
    const children = new Map();
    return {
      hidden: true, value: "",
      querySelectorAll(selector) {
        if (selector !== "[data-sf]" && selector !== "[data-cf]") return [];
        const input = this.querySelector(selector);
        input.dataset = selector === "[data-sf]" ? { sf: "name" } : { cf: "name" };
        return [input];
      },
      querySelector(selector) {
        if (!children.has(selector)) children.set(selector, element());
        return children.get(selector);
      },
      addEventListener(event, listener) { this[event] = listener; },
    };
  }
  const document = {
    querySelectorAll: () => [],
    getElementById(id) {
      if (id === "toastStack") return null;
      if (!elements.has(id)) elements.set(id, element());
      return elements.get(id);
    },
  };
  const session = vm.runInNewContext(
    app.replace(/\}\)\(\);\s*$/, `return { state: STATE, refresh: refreshState, api: apiFetch,
      renderServer: renderFrpsWorkspace, renderClient: renderFrpcWorkspace, renderRemote: renderRemoteClientPane }; })();`),
    {
      document,
      window: { addEventListener() {} },
      AbortController,
      FormData,
      fetch: async (path, options) => {
        if (path !== "/api/state") return apiResponse(path, options);
        let etag;
        const body = new Promise(resolve => {
          requests.push((file, ids = ["shared"], generation) => {
            const instances = ids.map(id => ({ id, name: file + "-" + id }));
            etag = file;
            resolve({
              activeConfigFile: file,
              configGeneration: generation,
              config: { servers: instances, clients: instances },
            });
          });
        });
        return {
          ok: true, status: 200, json: () => body,
          headers: { get: name => name === "ETag" ? etag : "application/json" },
        };
      },
    }
  );
  session.state.authenticated = true;
  return { ...session, requests, document };
}

test("a delayed poll cannot restore the previous scheme or its ETag", async () => {
  const { state, refresh, requests } = openApp();
  const poll = refresh(false);
  await Promise.resolve(); // The old response headers arrive before the scheme switch.
  const switched = refresh(true);
  requests[1]("new.json");
  await switched;
  requests[0]("old.json");
  await poll;
  assert.equal(state.activeConfigFile, "new.json");
  assert.equal(state.etag, "new.json");
});

async function checkSchemeChange(sameFile) {
  const { state, refresh, requests, document } = openApp();
  const load = async (file, ids, generation) => {
    const pending = refresh(false);
    requests.shift()(file, ids, generation);
    await pending;
  };
  const initialGeneration = sameFile ? 1 : undefined;
  await load("old.json", ["shared"], initialGeneration);
  state.serverDraft.name = "edited server";
  state.clientDraft.name = "edited client";
  state.serverDraftDirty = state.clientDraftDirty = true;
  state.srvTomlDirty = state.cliTomlDirty = true;
  state.srvTomlRaw = state.cliTomlRaw = "old TOML";
  await load("old.json", ["shared"], initialGeneration);
  assert.equal(state.serverDraft.name, "edited server");
  assert.equal(state.clientDraft.name, "edited client");
  state.frpsMode = "remoteClient";
  state.remoteTarget = { serverId: "shared", clientId: "shared" };
  state.remoteSnapshot = { profile: state.clientDraft };
  document.getElementById("modalBackdrop").hidden = false;
  const nextFile = sameFile ? "old.json" : "new.json";
  await load(nextFile, ["first", "shared"], sameFile ? 2 : undefined);
  assert.equal(state.selectedServerId, "first");
  assert.equal(state.selectedClientId, "first");
  assert.equal(state.serverDraft.name, nextFile + "-first");
  assert.equal(state.clientDraft.name, nextFile + "-first");
  assert.equal(state.serverDraftDirty || state.clientDraftDirty, false);
  assert.equal(state.srvTomlDirty || state.cliTomlDirty, false);
  assert.equal(state.srvTomlRaw + state.cliTomlRaw, "");
  assert.equal(state.frpsMode, "local");
  assert.equal(state.remoteTarget, null);
  assert.equal(state.remoteSnapshot, null);
  assert.equal(document.getElementById("modalBackdrop").hidden, true);
}

test("dirty drafts reset on scheme switch without a generation", () => checkSchemeChange(false));
test("dirty drafts reset when the active filename is reused", () => checkSchemeChange(true));

test("removing the selected client discards its raw TOML and modal", async () => {
  const { state, refresh, requests, document } = openApp();
  const initial = refresh(true);
  requests.shift()("old.json", ["selected", "other"], 1);
  await initial;
  state.activeView = "frpc";
  state.cliTomlDirty = true;
  state.cliTomlRaw = "removed client's TOML";
  document.getElementById("modalBackdrop").hidden = false;
  const updated = refresh(false);
  requests.shift()("old.json", ["other"], 1);
  await updated;
  assert.equal(state.selectedClientId, "other");
  assert.equal(state.clientDraft.id, "other");
  assert.equal(state.cliTomlRaw, "");
  assert.equal(document.getElementById("modalBackdrop").hidden, true);
});

test("only local mutations send a known generation and surface conflicts", async () => {
  let sent;
  let status = 200;
  const { api, state } = openApp((path, options) => {
    sent = options.headers["X-Config-Generation"];
    return Response.json({ error: "方案已切换" }, { status });
  });
  const post = path => api(path, { method: "POST" });
  await post("/api/clients/save");
  assert.equal(sent, undefined);
  state.configGeneration = 0;
  for (const path of ["/api/clients/save", "/api/servers/control", "/api/configs/import-toml"]) {
    await post(path);
    assert.equal(sent, "0");
  }
  for (const path of ["/api/configs/select", "/api/remote/client/save"]) {
    await post(path);
    assert.equal(sent, undefined);
  }
  status = 409;
  await assert.rejects(post("/api/clients/save"), /方案已切换/);
});

test("a completed save from the previous scheme cannot update the editor", async () => {
  let completeSave;
  const { api, refresh, requests } = openApp(() => new Promise(resolve => { completeSave = resolve; }));
  const save = api("/api/clients/save", { method: "POST" });
  const switched = refresh(true);
  requests.shift()("new.json");
  await switched;
  completeSave(Response.json({ id: "shared" }));
  await assert.rejects(save, /已忽略原方案的操作结果/);
});

test("a background refresh keeps the draft bound to the focused input", async () => {
  const { state, requests, refresh, document } = openApp();
  const initial = refresh(true);
  requests.shift()("default.json");
  await initial;
  state.activeView = "frpc";
  document.activeElement = { tagName: "INPUT" };
  document.getElementById("appShell").contains = () => true;
  const displayed = state.clientDraft;
  const poll = refresh(false);
  requests.shift()("default.json");
  await poll;
  assert.equal(state.clientDraft, displayed);
});

for (const edit of ["switch instance", "type TOML", "edit form", "edit form during refresh"]) {
  test("pending editor response while the user chooses to " + edit, async t => {
    for (const [editor, button] of [
      ["server", "srvQuickSaveBtn"], ["server", "srvTomlParseBtn"], ["server", "srvTomlSaveBtn"],
      ["client", "cliStudioSaveFormBtn"], ["client", "cliTomlParseBtn"], ["client", "cliTomlSaveBtn"],
      ["remote", "cliTomlSaveBtn"], ["remote", "cliStudioSaveFormBtn"], ["remote", "cliTomlParseBtn"],
    ]) {
      if (edit === "edit form during refresh" && button.includes("Parse")) continue;
      await t.test(editor + " " + button, async () => {
        let respond;
        const { state, requests, refresh, document, renderServer, renderClient, renderRemote } =
          openApp(() => new Promise(resolve => { respond = resolve; }));
        const ids = ["A", "B"];
        const ready = refresh(true);
        requests.shift()("default.json", ids);
        await ready;
        state.frpcSubTab = state.remoteSubTab = "studio";
        const select = id => {
          if (editor === "remote") {
            state.activeView = "frps";
            state.frpsMode = "remoteClient";
            state.remoteTarget = { serverId: id, clientId: "shared" };
            state.remoteClientDraft = { id: "shared", name: id };
            state.remoteSnapshot = { summary: { serverId: id }, profile: state.remoteClientDraft };
            const pane = document.getElementById("frpsMainPane");
            renderRemote(pane);
            return pane.querySelector("#remoteSubBody");
          }
          if (editor === "server") {
            state.activeView = "frps";
            state.selectedServerId = id;
            state.serverDraft = { id, name: id };
            const pane = document.getElementById("frpsMainPane");
            renderServer();
            return button === "srvQuickSaveBtn" ? pane : pane.querySelector("#frpsSubBody");
          }
          state.activeView = "frpc";
          state.selectedClientId = id;
          state.clientDraft = { id, name: id };
          renderClient();
          return document.getElementById("frpcMainPane").querySelector("#frpcSubBody");
        };
        const pane = select("A");
        const pending = pane.querySelector("#" + button).click();
        const currentDraft = () => editor === "remote" ? state.remoteClientDraft :
          editor === "server" ? state.serverDraft : state.clientDraft;
        if (edit === "switch instance") select("B");
        const sub = button === "srvQuickSaveBtn" ? pane.querySelector("#frpsSubBody") : pane;
        const editName = () => {
          const input = sub.querySelector(editor === "server" ? "[data-sf]" : "[data-cf]");
          input.value = "B";
          input.input();
        };
        if (edit === "type TOML") {
          const raw = sub.querySelector(editor === "server" ? "#srvTomlTextarea" : "#cliTomlTextarea");
          raw.value = "B edits";
          raw.input();
        } else if (edit !== "edit form during refresh") {
          editName();
        }
        if (edit === "switch instance") {
          state.cliTomlDirty = state.srvTomlDirty = true;
          state.srvTomlRaw = state.cliTomlRaw = "B edits";
        }
        const saved = { id: editor === "remote" ? "shared" : "A", name: "A response" };
        respond(Response.json({ ...saved, server: saved, client: saved, profile: saved }));
        await new Promise(resolve => setImmediate(resolve));
        if (edit === "edit form during refresh") {
          assert.equal(requests.length, 1);
          editName();
        }
        if (requests.length) requests.shift()("default.json", ids);
        await pending;
        const selected = edit === "switch instance" ? "B" : "A";
        if (editor === "server") assert.equal(state.selectedServerId, selected);
        if (editor === "client") assert.equal(state.selectedClientId, selected);
        if (edit !== "type TOML") {
          assert.equal(currentDraft().name, "B");
          if (editor !== "remote") assert.equal(editor === "server" ? state.serverDraftDirty : state.clientDraftDirty, true);
        }
        if (edit === "switch instance" || edit === "type TOML") {
          assert.equal(editor === "server" ? state.srvTomlRaw : state.cliTomlRaw, "B edits");
          assert.equal(editor === "server" ? state.srvTomlDirty : state.cliTomlDirty, true);
        }
      });
    }
  });
}
