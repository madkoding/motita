// The settings' pure clients and rules: the look saved in localStorage, the notification state
// and platform, and the security, network and LLM clients with their refusals and edge cases.
// Run with: node --test web/src/__tests__/cov-settings-api.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { installDom, fakeApi, json } from "./cov-settings-helpers.mjs";

installDom();
const s = await import("../uiPrefs.ts");
const sec = await import("../securityApi.ts");
const net = await import("../networkApi.ts");
const llm = await import("../llmApi.ts");

// A storage whose every call throws, the way a browser with storage disabled behaves.
function brokenStorage(fn) {
  const real = globalThis.localStorage;
  const boom = {
    getItem() {
      throw new Error("denied");
    },
    setItem() {
      throw new Error("denied");
    },
  };
  Object.defineProperty(globalThis, "localStorage", {
    value: boom,
    configurable: true,
  });
  try {
    return fn();
  } finally {
    Object.defineProperty(globalThis, "localStorage", {
      value: real,
      configurable: true,
    });
  }
}

// ── uiPrefs.ts ──────────────────────────────────────────────────────────────

test("loadSettings keeps allowed values and falls back on unknown or broken ones", () => {
  localStorage.clear();
  assert.deepEqual(s.loadSettings(), s.DEFAULT_SETTINGS);
  localStorage.setItem(
    s.SETTINGS_KEY,
    JSON.stringify({ fontSize: "large", density: "compact" }),
  );
  assert.deepEqual(s.loadSettings(), { fontSize: "large", density: "compact" });
  localStorage.setItem(
    s.SETTINGS_KEY,
    JSON.stringify({ fontSize: "huge", density: 3 }),
  );
  assert.deepEqual(s.loadSettings(), s.DEFAULT_SETTINGS);
  localStorage.setItem(s.SETTINGS_KEY, "{not json");
  const got = s.loadSettings();
  assert.deepEqual(got, s.DEFAULT_SETTINGS);
  assert.notEqual(got, s.DEFAULT_SETTINGS);
});

test("saveSettings stores and applies the look, and still applies it when storage fails", () => {
  localStorage.clear();
  s.saveSettings({ fontSize: "small", density: "compact" });
  assert.equal(
    localStorage.getItem(s.SETTINGS_KEY),
    JSON.stringify({ fontSize: "small", density: "compact" }),
  );
  assert.equal(document.documentElement.dataset.fontSize, "small");
  assert.equal(document.documentElement.dataset.density, "compact");

  brokenStorage(() =>
    s.saveSettings({ fontSize: "large", density: "comfortable" }),
  );
  assert.equal(document.documentElement.dataset.fontSize, "large");
  assert.equal(document.documentElement.dataset.density, "comfortable");
  // The saved value did not change.
  assert.match(localStorage.getItem(s.SETTINGS_KEY), /small/);

  // initSettings applies what is saved.
  assert.deepEqual(s.initSettings(), { fontSize: "small", density: "compact" });
  assert.equal(document.documentElement.dataset.fontSize, "small");
});

test("the notification and CI toast switches default to on and survive broken storage", () => {
  localStorage.clear();
  assert.equal(s.loadNotify(), true);
  s.saveNotify(false);
  assert.equal(localStorage.getItem(s.NOTIFY_KEY), "off");
  assert.equal(s.loadNotify(), false);
  s.saveNotify(true);
  assert.equal(localStorage.getItem(s.NOTIFY_KEY), "on");
  assert.equal(s.loadNotify(), true);

  assert.equal(s.loadCiToast(), true);
  s.saveCiToast(false);
  assert.equal(localStorage.getItem(s.CI_TOAST_KEY), "off");
  assert.equal(s.loadCiToast(), false);
  s.saveCiToast(true);
  assert.equal(s.loadCiToast(), true);

  assert.equal(s.loadNextHint(), true);
  s.saveNextHint(false);
  assert.equal(localStorage.getItem(s.NEXT_HINT_KEY), "off");
  assert.equal(s.loadNextHint(), false);
  s.saveNextHint(true);
  assert.equal(s.loadNextHint(), true);

  brokenStorage(() => {
    assert.equal(s.loadNextHint(), true);
    assert.doesNotThrow(() => s.saveNextHint(false));
    assert.equal(s.loadNotify(), true);
    assert.equal(s.loadCiToast(), true);
    assert.doesNotThrow(() => s.saveNotify(false));
    assert.doesNotThrow(() => s.saveCiToast(false));
  });
  assert.equal(localStorage.getItem(s.NOTIFY_KEY), "on");
});

test("notifyState combines the permission, the switch and the address", () => {
  assert.equal(s.notifyState("default", true, false), "insecure");
  assert.equal(s.notifyState(undefined, true, false), "insecure");
  assert.equal(s.notifyState("granted", false, false), "off");
  assert.equal(s.notifyState(undefined, true), "unsupported");
  assert.equal(s.notifyState("denied", true), "blocked");
  assert.equal(s.notifyState("default", true), "ask");
  assert.equal(s.notifyState("granted", true), "on");
  assert.equal(s.notifyState("granted", false), "off");
});

test("notifyPlatform reads the browser from the user agent", () => {
  const p = s.notifyPlatform;
  assert.equal(p("Mozilla (iPhone; CPU iPhone OS 17_0)"), "ios");
  assert.equal(p("Mozilla (iPad)"), "ios");
  assert.equal(p("Mozilla (Macintosh; Intel) Safari/605", 5), "ios");
  assert.equal(p("Mozilla (Macintosh; Intel) Safari/605", 0), "safari");
  assert.equal(p("Mozilla (Macintosh; Intel) Safari/605"), "safari");
  assert.equal(p("Mozilla (Linux; Android 14) Chrome/120"), "android");
  assert.equal(p("Mozilla (Windows) Chrome/120 Safari/537 Edg/120"), "edge");
  assert.equal(p("Mozilla (X11; rv:120) Gecko Firefox/120"), "firefox");
  assert.equal(p("Mozilla (X11) Chrome/120 Safari/537"), "chrome");
  assert.equal(p("Mozilla (X11) Chromium/120"), "chrome");
  assert.equal(p("curl/8.0"), "other");
});

// ── securityApi.ts ──────────────────────────────────────────────────────────

test("getSecurity reads the view, null on 404, and throws the gateway's refusal", async () => {
  const v = { disk_access: "workspace", can_save: true, enforced: false };
  let f = fakeApi({ "GET /v1/security": json(200, v) });
  assert.deepEqual(await sec.getSecurity(f.api), v);
  assert.equal(f.calls[0].path, sec.SECURITY_PATH);

  f = fakeApi({ "GET /v1/security": json(404, { error: "no" }) });
  assert.equal(await sec.getSecurity(f.api), null);

  f = fakeApi({ "GET /v1/security": json(500, { error: "broken" }) });
  await assert.rejects(sec.getSecurity(f.api), /^Error: broken$/);
  // A refusal with no JSON, or a JSON null, is told by its status.
  f = fakeApi({ "GET /v1/security": new Response("x", { status: 502 }) });
  await assert.rejects(sec.getSecurity(f.api), /HTTP 502/);
  f = fakeApi({ "GET /v1/security": json(503, null) });
  await assert.rejects(sec.getSecurity(f.api), /HTTP 503/);
});

test("setDiskAccess confirms only the widening to the whole disk", async () => {
  const v = { disk_access: "full", can_save: true, enforced: true };
  const f = fakeApi({ "PUT /v1/security": json(200, v) });
  assert.deepEqual(await sec.setDiskAccess(f.api, "full"), v);
  await sec.setDiskAccess(f.api, "workspace");
  assert.deepEqual(f.calls[0].body, { disk_access: "full", confirm: true });
  assert.deepEqual(f.calls[1].body, {
    disk_access: "workspace",
    confirm: false,
  });
  const r = fakeApi({ "PUT /v1/security": json(409, { error: "no file" }) });
  await assert.rejects(sec.setDiskAccess(r.api, "full"), /no file/);
});

test("getSessionScope asks for an escaped session id and is null without one or on a refusal", async () => {
  const scope = {
    workspace: "/w",
    project_dir: "",
    home: "",
    private_roots: ["/a"],
  };
  let f = fakeApi({ "GET /v1/sessions/": json(200, scope) });
  assert.equal(await sec.getSessionScope(f.api, ""), null);
  assert.equal(f.calls.length, 0);
  assert.deepEqual(await sec.getSessionScope(f.api, "a b/c"), scope);
  assert.equal(f.calls[0].path, "/v1/sessions/a%20b%2Fc/scope");
  f = fakeApi({ "GET /v1/sessions/": json(404, {}) });
  assert.equal(await sec.getSessionScope(f.api, "s1"), null);
});

// ── networkApi.ts ───────────────────────────────────────────────────────────

test("the network client sends each change and reports refusals with their status", async () => {
  const v = {
    mode: "lan",
    listen: "0.0.0.0:1",
    active_mode: "local",
    active_listen: "x",
    restart_needed: true,
    can_save: true,
  };
  let f = fakeApi({
    "GET /v1/network": json(200, v),
    "PUT /v1/network": json(200, v),
    "POST /v1/network/tailscale": json(200, v),
    "POST /v1/network/restart": json(202, v),
  });
  assert.deepEqual(await net.getNetwork(f.api), v);
  assert.deepEqual(await net.setNetworkMode(f.api, "lan"), v);
  assert.deepEqual(await net.setTailscale(f.api, true), v);
  assert.deepEqual(await net.restartGateway(f.api), v);
  assert.deepEqual(
    f.calls.map((c) => [c.method, c.path, c.body]),
    [
      ["GET", net.NETWORK_PATH, undefined],
      ["PUT", net.NETWORK_PATH, { mode: "lan", confirm: true }],
      ["POST", net.TAILSCALE_PATH, { enable: true }],
      ["POST", net.RESTART_PATH, { confirm: true }],
    ],
  );

  f = fakeApi({ "GET /v1/network": json(404, {}) });
  assert.equal(await net.getNetwork(f.api), null);

  f = fakeApi({ "PUT /v1/network": json(409, { error: "read only" }) });
  await assert.rejects(net.setNetworkMode(f.api, "all"), (e) => {
    assert.ok(e instanceof net.NetworkError);
    assert.equal(e.message, "read only");
    assert.equal(e.status, 409);
    return true;
  });
  f = fakeApi({ "POST /v1/network/tailscale": json(500, null) });
  await assert.rejects(net.setTailscale(f.api, false), /HTTP 500/);
  f = fakeApi({
    "POST /v1/network/restart": new Response("x", { status: 503 }),
  });
  await assert.rejects(net.restartGateway(f.api), (e) => e.status === 503);
});

// ── llmApi.ts ───────────────────────────────────────────────────────────────

test("the LLM client sends each request to its path and throws refusals with status and code", async () => {
  const f = fakeApi({
    "GET /v1/llm/logins/": json(200, { id: "f/1", status: "pending" }),
    "GET /v1/llm": json(200, { provider: "p", providers: [] }),
    "PUT /v1/llm/providers/": json(200, { id: "a b" }),
    "DELETE /v1/llm/providers/": json(200, { id: "a b" }),
    "POST /v1/llm/providers/": (p) =>
      p.endsWith("/test") ? json(200, { ok: true }) : json(200, { id: "f1" }),
    "PUT /v1/llm/default": json(200, { provider: "x", model: "m" }),
    "PUT /v1/llm/tasks": json(200, { tasks: {} }),
    "POST /v1/llm/logins/": json(204),
    "DELETE /v1/llm/logins/": json(204),
  });
  await llm.loadLLM(f.api);
  await llm.saveProvider(f.api, "a b", { api_key: "k" });
  await llm.forgetProvider(f.api, "a b");
  assert.deepEqual(await llm.testProvider(f.api, "a b", { base_url: "u" }), {
    ok: true,
  });
  await llm.saveDefault(f.api, "x", "m");
  await llm.saveTasks(f.api, { chat: { provider: "x", model: "m" } });
  assert.deepEqual(await llm.startLogin(f.api, "a b"), { id: "f1" });
  assert.equal((await llm.loginStatus(f.api, "f/1")).status, "pending");
  assert.equal(
    await llm.pasteLogin(f.api, "f/1", "http://cb?code=1"),
    undefined,
  );
  await llm.cancelLogin(f.api, "f/1");
  assert.deepEqual(
    f.calls.map((c) => [c.method, c.path, c.body]),
    [
      ["GET", "/v1/llm", undefined],
      ["PUT", "/v1/llm/providers/a%20b", { api_key: "k" }],
      ["DELETE", "/v1/llm/providers/a%20b", undefined],
      ["POST", "/v1/llm/providers/a%20b/test", { base_url: "u" }],
      ["PUT", "/v1/llm/default", { provider: "x", model: "m" }],
      [
        "PUT",
        "/v1/llm/tasks",
        { tasks: { chat: { provider: "x", model: "m" } } },
      ],
      ["POST", "/v1/llm/providers/a%20b/login", undefined],
      ["GET", "/v1/llm/logins/f%2F1", undefined],
      ["POST", "/v1/llm/logins/f%2F1/paste", { code: "http://cb?code=1" }],
      ["DELETE", "/v1/llm/logins/f%2F1", undefined],
    ],
  );
});

test("LLM refusals carry the message, status and code, or the status alone", async () => {
  let f = fakeApi({
    "GET /v1/llm": json(403, { error: "nope", code: "read_only" }),
  });
  await assert.rejects(llm.loadLLM(f.api), (e) => {
    assert.ok(e instanceof llm.LLMError);
    assert.equal(e.message, "nope");
    assert.equal(e.status, 403);
    assert.equal(e.code, "read_only");
    return true;
  });
  f = fakeApi({ "POST /v1/llm/logins/": new Response("x", { status: 500 }) });
  await assert.rejects(llm.pasteLogin(f.api, "f", "c"), (e) => {
    assert.equal(e.message, "HTTP 500");
    assert.equal(e.code, undefined);
    return true;
  });
  // A cancel that cannot reach the gateway is quiet.
  f = fakeApi({ "DELETE /v1/llm/logins/": new Error("offline") });
  assert.equal(await llm.cancelLogin(f.api, "f"), undefined);
  assert.equal(f.calls.length, 1);
});

test("taskChoices keeps complete trimmed choices and sameTasks ignores the empty ones", () => {
  const draft = {
    chat: { provider: "a", model: " m1 " },
    title: { provider: "", model: "m2" },
    code: { provider: "b", model: "  " },
  };
  assert.deepEqual(llm.taskChoices(draft), {
    chat: { provider: "a", model: "m1" },
  });
  assert.equal(
    llm.sameTasks(draft, { chat: { provider: "a", model: "m1" } }),
    true,
  );
  assert.equal(llm.sameTasks({}, {}), true);
  assert.equal(
    llm.sameTasks(draft, { chat: { provider: "b", model: "m1" } }),
    false,
  );
  assert.equal(
    llm.sameTasks(draft, { chat: { provider: "a", model: "m2" } }),
    false,
  );
  assert.equal(llm.sameTasks(draft, {}), false);
  assert.equal(llm.sameTasks({}, draft), false);
});

test("isConnected, canTypeKey, trialOf and modelOptions", () => {
  const p = (extra) => ({
    id: "x",
    name: "X",
    access: "",
    key: "missing",
    models: [],
    default: false,
    ...extra,
  });
  assert.equal(llm.isConnected(p()), false);
  assert.equal(llm.isConnected(p({ key: "stored" })), true);
  for (const [key, can] of [
    ["not_needed", false],
    ["env", false],
    ["config", false],
    ["stored", true],
    ["missing", true],
    ["login", true],
  ]) {
    assert.equal(llm.canTypeKey(p({ key })), can, key);
  }

  assert.deepEqual(llm.trialOf(p(), "  k ", " http://x "), { api_key: "k" });
  assert.deepEqual(llm.trialOf(p({ ask_base_url: true }), "", " http://x "), {
    base_url: "http://x",
  });
  assert.deepEqual(
    llm.trialOf(
      p({ ask_base_url: true, base_url: "http://x" }),
      "",
      "http://x",
    ),
    {},
  );
  assert.deepEqual(llm.trialOf(p({ ask_base_url: true }), "", ""), {});
  assert.deepEqual(
    llm.trialOf(p({ ask_base_url: true, base_url: "http://x" }), "", ""),
    { base_url: "" },
  );

  assert.deepEqual(llm.modelOptions(undefined), []);
  const q = p({
    models: [
      { id: "a", label: "Alpha" },
      { id: "b", label: "" },
    ],
  });
  assert.deepEqual(llm.modelOptions(q), [
    { id: "a", label: "Alpha" },
    { id: "b", label: "b" },
  ]);
  assert.deepEqual(
    llm.modelOptions({ ...q, listed_models: [] }),
    llm.modelOptions(q),
  );
  assert.deepEqual(llm.modelOptions({ ...q, listed_models: ["a", "z"] }), [
    { id: "a", label: "Alpha" },
    { id: "z", label: "z" },
  ]);
});

test("saveTasks sends the levels when given, and sameThinking ignores the empty ones", async () => {
  const f = fakeApi({ "PUT /v1/llm/tasks": json(200, {}) });
  await llm.saveTasks(f.api, {}, { plan: "high" });
  assert.deepEqual(f.calls.at(-1).body, {
    tasks: {},
    thinking: { plan: "high" },
  });
  await llm.saveTasks(f.api, {});
  assert.deepEqual(f.calls.at(-1).body, { tasks: {} });
  assert.equal(llm.sameThinking({ a: "" }, {}), true);
  assert.equal(llm.sameThinking({ a: "low" }, { a: "low" }), true);
  assert.equal(llm.sameThinking({ a: "low" }, {}), false);
  assert.equal(llm.sameThinking({}, { b: "high" }), false);
});
