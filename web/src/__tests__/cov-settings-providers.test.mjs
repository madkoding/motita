// The providers tab of the settings: each provider's state line and connection form (key,
// endpoint, test, save, disconnect), the refusals the gateway names, and the sign-in with the
// provider's account (device code, pasted address, polling with mocked timers).
// Run with: node --test web/src/__tests__/cov-settings-providers.test.mjs
import test, { mock } from "node:test";
import assert from "node:assert/strict";
import {
  installDom,
  load,
  fakeApi,
  json,
  text,
  settle,
  click,
  type,
  byText,
} from "./cov-settings-helpers.mjs";

installDom();
const { ProviderSettings, failureText, stateText, LOGIN_POLL } = await load(
  "ProviderSettings.tsx",
);
const { LLMError } = await import("../llmApi.ts");
const { h, render } = await import("preact");

const base = {
  access: "Paste an API key from its console.",
  models: [],
  default: false,
};
const P = {
  anthropic: {
    ...base,
    id: "anthropic",
    name: "Anthropic",
    key: "stored",
    key_hint: "…abcd",
    console_url: "https://console.anthropic.com",
    models: [{ id: "claude-a", label: "Claude A" }],
    default: true,
  },
  openai: {
    ...base,
    id: "openai",
    name: "OpenAI",
    key: "env",
    key_variable: "OPENAI_API_KEY",
    console_url: "https://platform.openai.com",
  },
  groq: { ...base, id: "groq", name: "Groq", key: "config" },
  codex: {
    ...base,
    id: "codex",
    name: "Codex",
    key: "login",
    supports_login: true,
  },
  claudecli: {
    ...base,
    id: "claudecli",
    name: "Claude CLI",
    key: "not_needed",
    login: "claude login",
  },
  ollama: {
    ...base,
    id: "ollama",
    name: "Ollama",
    key: "not_needed",
    ask_base_url: true,
    base_url: "http://localhost:11434",
    default_base_url: "http://localhost:11434",
  },
  mistral: { ...base, id: "mistral", name: "Mistral", key: "missing" },
  custom: {
    ...base,
    id: "custom",
    name: "Custom",
    key: "missing",
    ask_base_url: true,
    base_url: "https://llm.corp/v1",
    default_base_url: "https://api.example.com/v1",
  },
};
const VIEW = {
  provider: "anthropic",
  model: "claude-a",
  writable: true,
  providers: Object.values(P),
};

async function mount(api, notices = []) {
  const root = document.createElement("div");
  document.body.appendChild(root);
  render(
    h(ProviderSettings, {
      api,
      onNotice: (m, e) => notices.push(e ? ["error", m] : m),
    }),
    root,
  );
  await settle();
  return {
    root,
    notices,
    row: (id) => root.querySelector(`[data-provider=${id}]`),
    async unmount() {
      render(null, root);
      root.remove();
      await settle();
    },
  };
}
const btn = (root, label) => byText(root, "button", label);
async function press(el) {
  click(el);
  await settle();
}
async function open(m, id) {
  await press(m.row(id).querySelector("button[aria-expanded]"));
  return m.row(id).querySelector("[data-testid=llm-form]");
}

test("failureText names the refusals the gateway codes, and passes anything else through", () => {
  assert.match(
    failureText(new LLMError("x", 409, "env_key")),
    /environment variable on the gateway's machine/,
  );
  assert.match(
    failureText(new LLMError("x", 400, "cleartext")),
    /plain http outside your network/,
  );
  assert.match(
    failureText(new LLMError("x", 400, "key_for_new_address")),
    /Type the key again/,
  );
  for (const code of [
    "gemini_no_gcloud",
    "gemini_bad_login",
    "gemini_no_project",
    "gemini_expired",
  ]) {
    assert.match(
      failureText(new LLMError("english", 502, code)),
      /Paste an AI Studio key instead/,
      code,
    );
  }
  assert.equal(failureText(new LLMError("bad key", 401, "other")), "bad key");
  assert.equal(failureText(new LLMError("plain", 500)), "plain");
  assert.equal(failureText(new Error("boom")), "boom");
  assert.equal(failureText("text"), "text");
});

test("stateText says how each provider connects, with the key's hint when it has one", () => {
  assert.equal(stateText(P.anthropic), "Connected · key saved …abcd");
  assert.equal(
    stateText({ ...P.anthropic, key_hint: "" }),
    "Connected · key saved",
  );
  assert.equal(stateText(P.openai), "Connected · key from OPENAI_API_KEY");
  assert.equal(
    stateText({ ...P.openai, key_variable: undefined }),
    "Connected · key from ",
  );
  assert.equal(stateText(P.groq), "Connected · key in the configuration file");
  assert.equal(stateText(P.codex), "Connected · signed in");
  assert.equal(stateText(P.ollama), "Ready · no key needed");
  assert.equal(stateText(P.mistral), "Not connected");
  // A hint without a connection is not shown.
  assert.equal(stateText({ ...P.mistral, key_hint: "…zz" }), "Not connected");
});

test("the list shows every provider with its state, and a refused load says why", async () => {
  const f = fakeApi({ "GET /v1/llm": json(200, VIEW) });
  const m = await mount(f.api);
  const rows = [...m.root.querySelectorAll("[data-provider]")];
  assert.deepEqual(
    rows.map((r) => r.dataset.provider),
    Object.keys(P),
  );
  assert.match(
    text(m.row("anthropic")),
    /^AnthropicDefaultConnected · key saved …abcdManage$/,
  );
  assert.match(text(m.row("mistral")), /Not connected\s*Connect$/);
  assert.match(
    m.row("mistral").querySelector(".git-account-state").className,
    /7a7a8c/,
  );
  // The logo where there is one, else the initial.
  assert.ok(m.row("openai").querySelector("[data-logo=openai] svg"));
  assert.equal(
    text(m.row("mistral").querySelector("[data-logo=mistral]")),
    "M",
  );
  assert.equal(m.root.querySelector("[data-testid=llm-readonly]"), null);
  assert.equal(m.root.querySelector("[data-testid=insecure-warning]"), null);
  await m.unmount();

  const bad = fakeApi({ "GET /v1/llm": json(500, { error: "llm down" }) });
  const b = await mount(bad.api);
  assert.equal(text(b.root.querySelector("[role=alert]")), "llm down");
  assert.equal(b.root.querySelector("[data-provider]"), null);
  await b.unmount();

  // Before the answer: loading.
  let answer;
  const slow = fakeApi({
    "GET /v1/llm": () => new Promise((r) => (answer = r)),
  });
  const s = await mount(slow.api);
  assert.match(text(s.root), /Loading…/);
  answer(json(200, VIEW));
  await settle();
  assert.doesNotMatch(text(s.root), /Loading…/);
  await s.unmount();
});

test("a read-only gateway and an insecure page each say so", async () => {
  Object.defineProperty(window, "isSecureContext", {
    configurable: true,
    value: false,
  });
  try {
    const f = fakeApi({
      "GET /v1/llm": json(200, { ...VIEW, writable: false }),
    });
    const m = await mount(f.api);
    assert.ok(m.root.querySelector("[data-testid=llm-readonly]"));
    const warn = m.root.querySelector("[data-testid=insecure-warning]");
    assert.match(text(warn), /travels to Motita unencrypted/);
    assert.match(text(warn), /Open Motita through https before pasting a key/);
    // Nothing can be saved or disconnected there, and no sign-in is offered.
    let form = await open(m, "anthropic");
    assert.equal(btn(form, "Save"), undefined);
    assert.equal(btn(form, "Disconnect"), undefined);
    assert.ok(btn(form, "Test connection"));
    form = await open(m, "codex");
    assert.equal(form.querySelector("[data-testid=llm-login]"), null);
    await m.unmount();
  } finally {
    delete window.isSecureContext;
  }
});

test("one form is open at a time, and each explains how its provider connects", async () => {
  const f = fakeApi({ "GET /v1/llm": json(200, VIEW) });
  const m = await mount(f.api);
  const expect = {
    anthropic: /^Paste an API key from its console\. Get a key ↗/,
    openai: /The key comes from OPENAI_API_KEY on the gateway's machine/,
    groq: /The key is written in the configuration file \(llm\.api_key\)/,
    claudecli: /run claude login on the gateway's machine/,
    ollama: /Your own server needs no key\./,
  };
  for (const [id, re] of Object.entries(expect)) {
    const form = await open(m, id);
    assert.match(text(form.querySelector("p")), re, id);
    assert.equal(m.root.querySelectorAll("[data-testid=llm-form]").length, 1);
    assert.equal(
      text(m.row(id).querySelector("button[aria-expanded]")),
      "Close",
    );
  }
  // The key link is only where a key can be typed.
  assert.equal(byText(m.row("ollama"), "a", "Get a key"), undefined);
  const form = await open(m, "openai");
  assert.equal(byText(form, "a", "Get a key"), undefined);
  assert.equal(form.querySelector("input[type=password]"), null);
  assert.equal(btn(form, "Save"), undefined);
  // An env key without a variable name still explains.
  await press(m.row("openai").querySelector("button[aria-expanded]"));
  assert.equal(m.root.querySelector("[data-testid=llm-form]"), null);
  await m.unmount();

  const g = fakeApi({
    "GET /v1/llm": json(200, {
      ...VIEW,
      providers: [{ ...P.openai, key_variable: undefined }],
    }),
  });
  const n = await mount(g.api);
  const fm = await open(n, "openai");
  assert.match(
    text(fm.querySelector("p")),
    /^The key comes from on the gateway's machine/,
  );
  await n.unmount();
});

test("a key is tested, then saved; the form closes and the list reloads", async () => {
  let loads = 0;
  let saving;
  const f = fakeApi({
    "GET /v1/llm": () => (loads++, json(200, VIEW)),
    "POST /v1/llm/providers/mistral/test": json(200, {
      ok: true,
      models: ["a", "b", "c"],
    }),
    "PUT /v1/llm/providers/mistral": () => new Promise((r) => (saving = r)),
  });
  const m = await mount(f.api);
  const form = await open(m, "mistral");
  const key = form.querySelector("input[type=password]");
  assert.equal(key.placeholder, "Paste the key");
  // Nothing typed: Save is off.
  assert.equal(btn(form, "Save").disabled, true);
  type(key, "  sk-123  ");
  await settle();
  assert.equal(btn(form, "Save").disabled, false);
  await press(btn(form, "Test connection"));
  assert.deepEqual(f.calls.at(-1), {
    method: "POST",
    path: "/v1/llm/providers/mistral/test",
    body: { api_key: "sk-123" },
  });
  assert.equal(
    text(form.querySelector("output")),
    "It works: 3 models available.",
  );
  await press(btn(form, "Save"));
  assert.equal(btn(form, "Saving…").disabled, true);
  saving(json(200, P.mistral));
  await settle();
  assert.deepEqual(f.calls.at(-2), {
    method: "PUT",
    path: "/v1/llm/providers/mistral",
    body: { api_key: "sk-123" },
  });
  assert.deepEqual(m.notices, ["Mistral saved"]);
  assert.equal(loads, 2);
  assert.equal(m.root.querySelector("[data-testid=llm-form]"), null);
  await m.unmount();
});

test("a failed test says why; a test that lists no models says zero; a refused save is shown", async () => {
  const answers = [
    json(200, { ok: false, error: "401 unauthorized" }),
    json(200, { ok: false }),
    json(200, { ok: true }),
    json(500, { error: "gateway exploded" }),
  ];
  let testing;
  const f = fakeApi({
    "GET /v1/llm": json(200, VIEW),
    "POST /v1/llm/providers/anthropic/test": () => answers.shift(),
    "POST /v1/llm/providers/custom/test": () =>
      new Promise((r) => (testing = r)),
    "PUT /v1/llm/providers/": json(409, { error: "x", code: "env_key" }),
  });
  const m = await mount(f.api);
  const form = await open(m, "anthropic");
  assert.equal(
    form.querySelector("input[type=password]").placeholder,
    "Saved (…abcd). Paste a new one to replace it",
  );
  await press(btn(form, "Test connection"));
  assert.equal(
    text(form.querySelector("[role=alert]")),
    "It did not connect: 401 unauthorized",
  );
  await press(btn(form, "Test connection"));
  assert.equal(text(form.querySelector("[role=alert]")), "It did not connect:");
  await press(btn(form, "Test connection"));
  assert.equal(
    text(form.querySelector("output")),
    "It works: 0 models available.",
  );
  await press(btn(form, "Test connection"));
  assert.equal(form.querySelector("output"), null);
  assert.equal(text(form.querySelector("[role=alert]")), "gateway exploded");
  type(form.querySelector("input[type=password]"), "k");
  await settle();
  await press(btn(form, "Save"));
  assert.match(
    text(form.querySelector("[role=alert]")),
    /environment variable/,
  );
  assert.deepEqual(m.notices, []);

  // While a test runs the buttons wait.
  const cf = await open(m, "custom");
  click(btn(cf, "Test connection"));
  await settle();
  assert.equal(text(btn(cf, "Testing…")), "Testing…");
  assert.equal(btn(cf, "Testing…").disabled, true);
  assert.equal(btn(cf, "Save").disabled, true);
  testing(json(200, { ok: true, models: ["m"] }));
  await settle();
  assert.equal(
    text(cf.querySelector("output")),
    "It works: 1 models available.",
  );
  await m.unmount();
});

test("a stored key without a hint, and an endpoint that only sends what changed", async () => {
  const f = fakeApi({
    "GET /v1/llm": json(200, {
      ...VIEW,
      providers: [{ ...P.anthropic, key_hint: undefined }, P.custom, P.ollama],
    }),
    "POST /v1/llm/providers/": json(200, { ok: true, models: [] }),
    "PUT /v1/llm/providers/ollama": json(200, P.ollama),
  });
  const m = await mount(f.api);
  let form = await open(m, "anthropic");
  assert.equal(
    form.querySelector("input[type=password]").placeholder,
    "Saved (…). Paste a new one to replace it",
  );

  form = await open(m, "custom");
  const url = form.querySelector("input[type=url]");
  assert.equal(url.value, "https://llm.corp/v1");
  assert.equal(url.placeholder, "https://api.example.com/v1");
  // An endpoint of its own can be disconnected.
  assert.ok(btn(form, "Disconnect"));
  // Unchanged: nothing to send. Changed: the endpoint only.
  await press(btn(form, "Test connection"));
  assert.deepEqual(f.calls.at(-1).body, {});
  type(url, "https://other/v1");
  await settle();
  await press(btn(form, "Test connection"));
  assert.deepEqual(f.calls.at(-1).body, { base_url: "https://other/v1" });

  // A provider that needs no key still saves its endpoint; its default one cannot be disconnected.
  form = await open(m, "ollama");
  assert.equal(form.querySelector("input[type=password]"), null);
  assert.equal(btn(form, "Disconnect"), undefined);
  assert.equal(btn(form, "Save").disabled, true);
  type(form.querySelector("input[type=url]"), "http://box:11434");
  await settle();
  await press(btn(form, "Save"));
  assert.deepEqual(f.calls.at(-2), {
    method: "PUT",
    path: "/v1/llm/providers/ollama",
    body: { base_url: "http://box:11434" },
  });
  assert.deepEqual(m.notices, ["Ollama saved"]);
  await m.unmount();
});

test("disconnect asks first, then forgets the provider", async () => {
  let loads = 0;
  let forgetting;
  const f = fakeApi({
    "GET /v1/llm": () => (loads++, json(200, VIEW)),
    "DELETE /v1/llm/providers/anthropic": () =>
      new Promise((r) => (forgetting = r)),
    "DELETE /v1/llm/providers/codex": json(500, { error: "cannot forget" }),
  });
  const m = await mount(f.api);
  let form = await open(m, "anthropic");
  await press(btn(form, "Disconnect"));
  assert.equal(btn(form, "Disconnect"), undefined);
  await press(btn(form, "Cancel"));
  assert.ok(btn(form, "Disconnect"));
  await press(btn(form, "Disconnect"));
  click(btn(form, "Confirm"));
  await settle();
  assert.equal(btn(form, "Confirm").disabled, true);
  forgetting(json(200, P.anthropic));
  await settle();
  assert.deepEqual(
    f.calls.filter((c) => c.method === "DELETE").map((c) => c.path),
    ["/v1/llm/providers/anthropic"],
  );
  assert.deepEqual(m.notices, ["Disconnected from Anthropic"]);
  assert.equal(loads, 2);
  assert.equal(m.root.querySelector("[data-testid=llm-form]"), null);

  // A signed-in provider can be disconnected too; a refusal stays in the form.
  form = await open(m, "codex");
  await press(btn(form, "Disconnect"));
  await press(btn(form, "Confirm"));
  assert.equal(text(form.querySelector("[role=alert]")), "cannot forget");
  assert.ok(m.root.querySelector("[data-testid=llm-form]"));
  await m.unmount();
});

// ── sign-in ─────────────────────────────────────────────────────────────────

const loginRow = (m) => m.row("codex").querySelector("[data-testid=llm-login]");

test("a device sign-in shows the code and link, polls, and closes when connected", async () => {
  mock.timers.enable({ apis: ["setTimeout"] });
  try {
    const status = [
      json(200, {
        id: "f1",
        provider: "codex",
        method: "device",
        status: "pending",
        url: "https://auth/device",
        code: "AB-12",
      }),
      json(200, {
        id: "f1",
        provider: "codex",
        method: "device",
        status: "connected",
      }),
    ];
    let loads = 0;
    let starting;
    const f = fakeApi({
      "GET /v1/llm/logins/f1": () => status.shift(),
      "GET /v1/llm": () => (
        loads++,
        json(200, { ...VIEW, providers: [{ ...P.codex, key: "missing" }] })
      ),
      "POST /v1/llm/providers/codex/login": () =>
        new Promise((r) => (starting = r)),
    });
    const m = await mount(f.api);
    await open(m, "codex");
    const startBtn = btn(loginRow(m), "Sign in with your account");
    await press(startBtn);
    assert.equal(text(startBtn), "Starting…");
    assert.equal(startBtn.disabled, true);
    starting(
      json(200, {
        id: "f1",
        provider: "codex",
        method: "device",
        status: "pending",
        url: "https://auth/device",
        code: "AB-12",
      }),
    );
    await settle();
    assert.equal(
      text(loginRow(m).querySelector("[data-testid=llm-login-code]")),
      "AB-12",
    );
    const link = byText(loginRow(m), "a", "https://auth/device");
    assert.equal(link.getAttribute("href"), "https://auth/device");
    assert.match(text(loginRow(m)), /Waiting for the provider…/);
    mock.timers.tick(LOGIN_POLL);
    await settle();
    assert.equal(
      f.calls.filter((c) => c.path === "/v1/llm/logins/f1").length,
      1,
    );
    assert.match(text(loginRow(m)), /AB-12/);
    mock.timers.tick(LOGIN_POLL);
    await settle();
    assert.deepEqual(m.notices, ["Signed in to Codex"]);
    assert.equal(loads, 2);
    assert.equal(m.root.querySelector("[data-testid=llm-form]"), null);
    // Nothing is cancelled: the flow ended.
    assert.equal(f.calls.filter((c) => c.method === "DELETE").length, 0);
    await m.unmount();
  } finally {
    mock.timers.reset();
  }
});

test("a code sign-in takes the pasted address, and Cancel stops it", async () => {
  let paste = json(500, { error: "bad address" });
  const f = fakeApi({
    "GET /v1/llm": json(200, VIEW),
    "POST /v1/llm/providers/codex/login": json(200, {
      id: "f2",
      provider: "codex",
      method: "code",
      status: "pending",
      url: "https://auth/start",
    }),
    "POST /v1/llm/logins/f2/paste": () => paste,
    "DELETE /v1/llm/logins/f2": json(204),
  });
  const m = await mount(f.api);
  await open(m, "codex");
  await press(btn(loginRow(m), "Sign in again"));
  const lr = loginRow(m);
  assert.equal(
    byText(lr, "a", "Open the sign-in page").getAttribute("href"),
    "https://auth/start",
  );
  assert.equal(lr.querySelector("[data-testid=llm-login-code]"), null);
  const input = lr.querySelector(
    "input[aria-label='Address the browser ended on']",
  );
  const send = btn(lr, "Send");
  assert.equal(send.disabled, true);
  type(input, "  http://localhost:1455/auth/callback?code=x  ");
  await settle();
  assert.equal(send.disabled, false);
  await press(send);
  assert.deepEqual(f.calls.at(-1), {
    method: "POST",
    path: "/v1/llm/logins/f2/paste",
    body: { code: "http://localhost:1455/auth/callback?code=x" },
  });
  assert.equal(text(lr.querySelector("[role=alert]")), "bad address");
  paste = json(204);
  await press(send);
  assert.equal(lr.querySelector("[role=alert]"), null);
  assert.equal(send.disabled, true);

  await press(btn(lr, "Cancel"));
  assert.deepEqual(f.calls.at(-1), {
    method: "DELETE",
    path: "/v1/llm/logins/f2",
    body: undefined,
  });
  assert.ok(btn(loginRow(m), "Sign in again"));
  await m.unmount();
});

test("a sign-in that fails, cannot start, or cannot be polled says why", async () => {
  mock.timers.enable({ apis: ["setTimeout"] });
  try {
    const starts = [
      json(200, {
        id: "f3",
        provider: "codex",
        method: "device",
        status: "failed",
        error: "denied by user",
      }),
      json(200, {
        id: "f4",
        provider: "codex",
        method: "device",
        status: "failed",
      }),
      json(403, { error: "logins disabled" }),
      json(200, {
        id: "f5",
        provider: "codex",
        method: "device",
        status: "pending",
        code: "C",
        url: "https://u",
      }),
    ];
    const f = fakeApi({
      "GET /v1/llm/logins/f5": json(500, { error: "flow lost" }),
      "GET /v1/llm": json(200, VIEW),
      "POST /v1/llm/providers/codex/login": () => starts.shift(),
    });
    const m = await mount(f.api);
    await open(m, "codex");
    await press(btn(loginRow(m), "Sign in again"));
    assert.equal(
      text(loginRow(m).querySelector("[role=alert]")),
      "The sign-in did not finish: denied by user",
    );
    await press(btn(loginRow(m), "Sign in again"));
    assert.equal(
      text(loginRow(m).querySelector("[role=alert]")),
      "The sign-in did not finish:",
    );
    await press(btn(loginRow(m), "Sign in again"));
    assert.match(text(loginRow(m)), /logins disabled/);
    await press(btn(loginRow(m), "Sign in again"));
    assert.match(text(loginRow(m)), /Waiting for the provider…/);
    mock.timers.tick(LOGIN_POLL);
    await settle();
    assert.equal(text(loginRow(m).querySelector("[role=alert]")), "flow lost");
    assert.ok(btn(loginRow(m), "Sign in again"));
    // The flow is forgotten: closing the form cancels nothing.
    await m.unmount();
    assert.equal(f.calls.filter((c) => c.method === "DELETE").length, 0);
  } finally {
    mock.timers.reset();
  }
});

test("an instant sign-in ends at once; one left pending is cancelled when the form closes", async () => {
  const starts = [
    json(200, {
      id: "f6",
      provider: "codex",
      method: "instant",
      status: "connected",
    }),
    json(200, {
      id: "f7",
      provider: "codex",
      method: "device",
      status: "pending",
      code: "Z",
      url: "https://u",
    }),
  ];
  const f = fakeApi({
    "GET /v1/llm": json(200, VIEW),
    "POST /v1/llm/providers/codex/login": () => starts.shift(),
    "DELETE /v1/llm/logins/": new Error("offline"),
  });
  const m = await mount(f.api);
  await open(m, "codex");
  await press(btn(loginRow(m), "Sign in again"));
  assert.deepEqual(m.notices, ["Signed in to Codex"]);
  assert.equal(m.root.querySelector("[data-testid=llm-form]"), null);
  await open(m, "codex");
  await press(btn(loginRow(m), "Sign in again"));
  assert.match(text(loginRow(m)), /Waiting/);
  // Closing the form stops the pending sign-in, even if the gateway cannot be reached.
  await press(m.row("codex").querySelector("button[aria-expanded]"));
  assert.deepEqual(f.calls.at(-1), {
    method: "DELETE",
    path: "/v1/llm/logins/f7",
    body: undefined,
  });
  await m.unmount();
});
