// The providers tab of the settings: the client of /v1/llm, and the real tab rendered in jsdom
// (bundled with esbuild, since node cannot import TSX) against a fake gateway. A key typed in it
// goes to the gateway and is never drawn back.
// Run with: node --test web/src/__tests__/providers.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { fileURLToPath, pathToFileURL } from "node:url";
import { dirname, join } from "node:path";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { JSDOM } from "jsdom";
import { build } from "esbuild";

const here = dirname(fileURLToPath(import.meta.url));
const src = (f) => readFileSync(join(here, "..", f), "utf8");
const dom = new JSDOM("<!doctype html><html><body></body></html>", {
  url: "http://localhost/",
});
for (const k of [
  "document",
  "window",
  "KeyboardEvent",
  "MouseEvent",
  "Event",
  "HTMLElement",
  "Node",
  "localStorage",
]) {
  globalThis[k] = dom.window[k];
}
Object.defineProperty(globalThis, "navigator", {
  value: dom.window.navigator,
  configurable: true,
});
globalThis.Notification = { permission: "granted" };
globalThis.requestAnimationFrame = (cb) => setTimeout(() => cb(Date.now()), 0);
globalThis.cancelAnimationFrame = (id) => clearTimeout(id);

const out = mkdtempSync(join(here, ".pv-"));
await build({
  entryPoints: [join(here, "..", "Settings.tsx")],
  outfile: join(out, "pv.mjs"),
  bundle: true,
  format: "esm",
  jsx: "automatic",
  jsxImportSource: "preact",
  platform: "node",
  external: ["preact", "preact/*"],
  loader: { ".css": "empty" },
  resolveExtensions: [".ts", ".tsx", ".mjs", ".js"],
  logLevel: "silent",
});
const { SettingsModal } = await import(pathToFileURL(join(out, "pv.mjs")).href);
const { h, render } = await import("preact");
const { act } = await import("preact/test-utils");
rmSync(out, { recursive: true });
const llm = await import("../llmApi.ts");
const es = (await import("../i18n.es.ts")).default;

const HTTP_OK = 200;
const HTTP_BAD_REQUEST = 400;
const HTTP_CONFLICT = 409;
const HTTP_SERVER_ERROR = 500;
const HTTP_BAD_GATEWAY = 502;
const OPENAI_BASE_URL = "https://api.openai.com/v1";
const LLM_FORM = "[data-testid=llm-form]";
const KEY_INPUT = "input[type=password]";
const DEFAULT_MODEL_INPUT = "#llm-default-model";
const CLAUDE_CODE = "claude-code";
const GPT_5_MINI = "gpt-5-mini";
const SIGN_IN = "Sign in with your account";
const PROVIDER_COUNT = 5;
const CLAUDE_CODE_INDEX = 3;
const MIN_PROVIDER_STRINGS = 30;
const SIGN_IN_POLL_MS = 2000;
const FAST_POLL_MS = 20;

const provider = (id, name, extra = {}) => ({
  id,
  name,
  access: "API key from the console",
  key: "missing",
  models: [{ id: id + "-1", label: id + " one" }],
  default: false,
  ...extra,
});
const VIEW = () => ({
  provider: "openai",
  model: GPT_5_MINI,
  writable: true,
  providers: [
    provider("openai", "OpenAI", {
      key: "stored",
      default: true,
      ask_base_url: true,
      base_url: OPENAI_BASE_URL,
      default_base_url: OPENAI_BASE_URL,
      console_url: "https://platform.openai.com/api-keys",
      models: [
        { id: GPT_5_MINI, label: "GPT-5 mini" },
        { id: "gpt-5", label: "GPT-5" },
      ],
    }),
    provider("anthropic", "Anthropic"),
    provider("gemini", "Gemini", {
      key: "env",
      key_variable: "GEMINI_API_KEY",
    }),
    provider(CLAUDE_CODE, "Claude Code", {
      key: "not_needed",
      login: "claude auth login",
    }),
    provider("qwen", "Qwen", { supports_login: true }),
  ],
});

// parsedBody reads a request's JSON body, or undefined when it sent none.
function parsedBody(body) {
  if (!body) {
    return undefined;
  }
  return JSON.parse(body);
}

// fakeGateway answers /v1/llm like the gateway, and records every request.
function fakeGateway(view = VIEW(), answers = {}) {
  const calls = [];
  const api = async (path, init = {}) => {
    const method = init.method || "GET";
    calls.push({
      method,
      path,
      body: parsedBody(init.body),
    });
    const key = method + " " + path;
    if (answers[key]) {
      const [status, body] = answers[key];
      return new Response(JSON.stringify(body), { status });
    }
    if (key === "GET /v1/llm") {
      return new Response(JSON.stringify(view), { status: HTTP_OK });
    }
    if (key === "PUT /v1/llm/default") {
      const b = JSON.parse(init.body);
      view.provider = b.provider;
      view.model = b.model;
      return new Response(JSON.stringify(view), { status: HTTP_OK });
    }
    if (path.endsWith("/test")) {
      return new Response(
        JSON.stringify({ ok: true, models: ["m-a", "m-b"] }),
        {
          status: HTTP_OK,
        },
      );
    }
    return new Response(JSON.stringify(view.providers[0]), { status: HTTP_OK });
  };
  return { api, calls };
}

const settle = () => act(() => new Promise((r) => setTimeout(r, 0)));

async function open(gw) {
  const root = document.createElement("div");
  document.body.appendChild(root);
  const notices = [];
  await act(() => {
    render(
      h(SettingsModal, {
        langSetting: "auto",
        langAvailable: true,
        onLang: () => {},
        onClose: () => {},
        llm: { api: gw.api, onNotice: (m, e) => notices.push([m, !!e]) },
      }),
      root,
    );
  });
  const tab = root.querySelector("#settings-tab-providers");
  assert.ok(tab, "the providers have a tab of their own");
  await act(() => tab.click());
  await settle();
  const done = async () => {
    await act(() => render(null, root));
    root.remove();
  };
  return { root, notices, done };
}

const row = (root, id) => root.querySelector(`li[data-provider="${id}"]`);
const button = (el, text) =>
  [...el.querySelectorAll("button")].find((b) => b.textContent === text);
const type = async (input, value) => {
  input.value = value;
  await act(() =>
    input.dispatchEvent(new window.Event("input", { bubbles: true })),
  );
};

test("trialOf sends only what was typed, and modelChoices keeps the model in force", () => {
  const p = VIEW().providers[0];
  assert.deepEqual(llm.trialOf(p, "  ", p.base_url), {});
  assert.deepEqual(llm.trialOf(p, " sk ", "https://proxy/v1"), {
    api_key: "sk",
    base_url: "https://proxy/v1",
  });
  const fixed = VIEW().providers[1];
  assert.deepEqual(
    llm.trialOf(fixed, "", "https://x"),
    {},
    "no endpoint for a fixed host",
  );
  assert.deepEqual(llm.modelChoices(p, undefined, "gpt-5"), [
    GPT_5_MINI,
    "gpt-5",
  ]);
  assert.deepEqual(llm.modelChoices(p, ["a"], "custom"), ["custom", "a"]);
  assert.deepEqual(llm.modelChoices(undefined, undefined, ""), []);
  assert.equal(llm.isConnected(fixed), false);
  assert.equal(
    llm.canTypeKey(VIEW().providers[2]),
    false,
    "an exported key wins",
  );
  assert.equal(
    llm.canTypeKey(VIEW().providers[CLAUDE_CODE_INDEX]),
    false,
    "claude-code takes none",
  );
});

test("the client sends each change where the gateway expects it and reads its refusals", async () => {
  const gw = fakeGateway(VIEW(), {
    "PUT /v1/llm/providers/gemini": [
      HTTP_CONFLICT,
      { error: "env", code: "env_key" },
    ],
  });
  await llm.saveProvider(gw.api, "anthropic", { api_key: "k" });
  await llm.forgetProvider(gw.api, CLAUDE_CODE);
  await llm.testProvider(gw.api, "openai", {});
  await llm.saveDefault(gw.api, "openai", "gpt-5");
  assert.deepEqual(
    gw.calls.map((c) => c.method + " " + c.path),
    [
      "PUT /v1/llm/providers/anthropic",
      "DELETE /v1/llm/providers/claude-code",
      "POST /v1/llm/providers/openai/test",
      "PUT /v1/llm/default",
    ],
  );
  await assert.rejects(
    llm.saveProvider(gw.api, "gemini", { api_key: "k" }),
    (e) => {
      assert.ok(e instanceof llm.LLMError);
      assert.equal(e.status, HTTP_CONFLICT);
      assert.equal(e.code, "env_key");
      return true;
    },
  );
  const bare = async () => new Response("nope", { status: HTTP_SERVER_ERROR });
  await assert.rejects(llm.loadLLM(bare), /HTTP 500/);
});

test("every provider shows where its key comes from, and no key is ever drawn", async () => {
  const gw = fakeGateway();
  const { root, done } = await open(gw);
  const rows = root.querySelectorAll("[data-testid=llm-providers] li");
  assert.equal(rows.length, PROVIDER_COUNT);
  assert.match(row(root, "openai").textContent, /Default/);
  assert.match(row(root, "openai").textContent, /key saved/);
  assert.match(row(root, "anthropic").textContent, /Not connected/);
  assert.match(row(root, "gemini").textContent, /GEMINI_API_KEY/);
  assert.match(row(root, CLAUDE_CODE).textContent, /no key needed/);
  // The provider picker lists only those that can run.
  const options = [
    ...root.querySelectorAll("#llm-default-provider option"),
  ].map((o) => o.value);
  assert.deepEqual(options, ["openai", "gemini", CLAUDE_CODE]);
  assert.equal(root.querySelector(DEFAULT_MODEL_INPUT).value, GPT_5_MINI);
  assert.equal(root.querySelector("[data-testid=insecure-warning]"), null);
  await done();
});

test("a key is typed, tested and saved, and the field never holds it again", async () => {
  const gw = fakeGateway();
  const { root, notices, done } = await open(gw);
  await act(() => row(root, "anthropic").querySelector("button").click());
  const form = row(root, "anthropic").querySelector(LLM_FORM);
  assert.ok(form, "the form opens under the row");
  const key = form.querySelector(KEY_INPUT);
  assert.equal(key.getAttribute("autocomplete"), "off");
  assert.equal(form.querySelector("input[type=url]"), null, "one fixed host");
  assert.equal(
    button(form, "Save").disabled,
    true,
    "nothing typed, nothing to save",
  );
  await type(key, " sk-ant ");
  await act(() => button(form, "Test connection").click());
  await settle();
  assert.match(form.textContent, /It works: 2 models available/);
  assert.deepEqual(gw.calls.at(-1), {
    method: "POST",
    path: "/v1/llm/providers/anthropic/test",
    body: { api_key: "sk-ant" },
  });
  await act(() => button(form, "Save").click());
  await settle();
  const put = gw.calls.find((c) => c.method === "PUT");
  assert.deepEqual(put.body, { api_key: "sk-ant" });
  assert.deepEqual(notices.at(-1), ["Anthropic saved", false]);
  assert.equal(row(root, "anthropic").querySelector(LLM_FORM), null);
  assert.ok(
    !root.innerHTML.includes("sk-ant"),
    "the key is not left in the page",
  );
  await done();
});

test("each provider shows its logo, and a saved key its last four characters", async () => {
  const view = VIEW();
  view.providers[0].key_hint = "…a1b2";
  view.providers[2].key_hint = "…zz99";
  view.providers.push(provider("acme", "Acme"));
  const { root, done } = await open(fakeGateway(view));
  for (const id of ["openai", "anthropic", "gemini", CLAUDE_CODE, "qwen"]) {
    assert.ok(
      row(root, id).querySelector(`[data-logo="${id}"] svg path`),
      id + " has a logo",
    );
  }
  assert.equal(
    row(root, "acme").querySelector("[data-logo]").textContent,
    "A",
    "an unknown provider keeps its initial",
  );
  assert.match(row(root, "openai").textContent, /key saved …a1b2/);
  assert.match(row(root, "gemini").textContent, /GEMINI_API_KEY …zz99/);
  await act(() => button(row(root, "openai"), "Manage").click());
  assert.equal(
    row(root, "openai").querySelector(KEY_INPUT).placeholder,
    "Saved (…a1b2). Paste a new one to replace it",
  );
  await done();
});

// fastPolls makes the sign-in's two-second polls short for the test it runs in, and poll waits one.
function fastPolls(t) {
  const real = globalThis.setTimeout;
  const shorten = (ms) => {
    if (ms === SIGN_IN_POLL_MS) {
      return FAST_POLL_MS;
    }
    return ms;
  };
  globalThis.setTimeout = (fn, ms, ...a) => real(fn, shorten(ms), ...a);
  t.after(() => {
    globalThis.setTimeout = real;
  });
}

const poll = () => act(() => new Promise((r) => setTimeout(r, 60)));

const LOGIN = {
  id: "f1",
  provider: "qwen",
  method: "device",
  url: "https://chat.qwen.ai/authorize",
  code: "QW-12",
  status: "pending",
};

test("a device sign-in shows the code, waits for the provider and reloads", async (t) => {
  fastPolls(t);
  const gw = fakeGateway(VIEW(), {
    "POST /v1/llm/providers/qwen/login": [HTTP_OK, LOGIN],
    "GET /v1/llm/logins/f1": [HTTP_OK, { ...LOGIN, status: "connected" }],
  });
  const { root, notices, done } = await open(gw);
  await act(() => button(row(root, "qwen"), "Connect").click());
  const form = row(root, "qwen").querySelector(LLM_FORM);
  await act(() => button(form, SIGN_IN).click());
  await settle();
  assert.equal(
    form.querySelector("[data-testid=llm-login-code]").textContent,
    "QW-12",
  );
  assert.equal(form.querySelector(`a[href="${LOGIN.url}"]`).target, "_blank");
  assert.match(form.textContent, /Waiting for the provider/);
  await poll();
  assert.deepEqual(notices.at(-1), ["Signed in to Qwen", false]);
  assert.equal(row(root, "qwen").querySelector(LLM_FORM), null);
  assert.equal(
    gw.calls.filter((c) => c.path === "/v1/llm").length,
    2,
    "the providers are read again",
  );
  assert.equal(
    gw.calls.filter((c) => c.method === "DELETE").length,
    0,
    "a finished sign-in is not cancelled",
  );
  await done();
});

test("a code sign-in takes the pasted address, and one that fails says why", async (t) => {
  fastPolls(t);
  const code = { ...LOGIN, method: "code", code: undefined };
  const gw = fakeGateway(VIEW(), {
    "POST /v1/llm/providers/qwen/login": [HTTP_OK, code],
    "POST /v1/llm/logins/f1/paste": [HTTP_OK, {}],
    "GET /v1/llm/logins/f1": [
      HTTP_OK,
      { ...code, status: "failed", error: "state mismatch" },
    ],
  });
  const { root, done } = await open(gw);
  await act(() => button(row(root, "qwen"), "Connect").click());
  const form = row(root, "qwen").querySelector(LLM_FORM);
  await act(() => button(form, SIGN_IN).click());
  await settle();
  const field = form.querySelector("[data-testid=llm-login] input");
  assert.equal(button(form, "Send").disabled, true);
  await type(field, " http://localhost:1455/auth/callback?code=c ");
  await act(() => button(form, "Send").click());
  assert.deepEqual(gw.calls.at(-1), {
    method: "POST",
    path: "/v1/llm/logins/f1/paste",
    body: { code: "http://localhost:1455/auth/callback?code=c" },
  });
  await poll();
  assert.match(form.textContent, /The sign-in did not finish: state mismatch/);
  assert.ok(button(form, SIGN_IN), "it can start again");
  await done();
});

test("a sign-in left open is cancelled, and a refused one is reported", async () => {
  const gw = fakeGateway(VIEW(), {
    "POST /v1/llm/providers/qwen/login": [HTTP_OK, LOGIN],
    "DELETE /v1/llm/logins/f1": [HTTP_OK, {}],
  });
  const { root, done } = await open(gw);
  await act(() => button(row(root, "qwen"), "Connect").click());
  let form = row(root, "qwen").querySelector(LLM_FORM);
  await act(() => button(form, SIGN_IN).click());
  await settle();
  await act(() => button(form, "Cancel").click());
  assert.deepEqual(gw.calls.at(-1), {
    method: "DELETE",
    path: "/v1/llm/logins/f1",
    body: undefined,
  });
  await act(() => button(form, SIGN_IN).click());
  await settle();
  await act(() => button(row(root, "qwen"), "Close").click());
  assert.equal(
    gw.calls.filter((c) => c.method === "DELETE").length,
    2,
    "closing the form stops the sign-in",
  );
  await done();

  const view = VIEW();
  view.providers[2].supports_login = true;
  view.providers[2].key = "login";
  const second = await open(
    fakeGateway(view, {
      "POST /v1/llm/providers/gemini/login": [
        HTTP_BAD_GATEWAY,
        { error: "run gcloud auth application-default login" },
      ],
    }),
  );
  assert.match(row(second.root, "gemini").textContent, /signed in/);
  await act(() => button(row(second.root, "gemini"), "Manage").click());
  form = row(second.root, "gemini").querySelector(LLM_FORM);
  assert.ok(button(form, "Disconnect"), "a login can be forgotten");
  await act(() => button(form, "Sign in again").click());
  await settle();
  assert.match(form.textContent, /run gcloud auth application-default login/);
  await second.done();
});

test("a failed test and a refused save say why, in the reader's words", async () => {
  const gw = fakeGateway(VIEW(), {
    "POST /v1/llm/providers/openai/test": [
      HTTP_OK,
      { ok: false, error: "401 bad key" },
    ],
    "PUT /v1/llm/providers/openai": [
      HTTP_BAD_REQUEST,
      { error: "x", code: "cleartext" },
    ],
  });
  const { root, done } = await open(gw);
  await act(() => button(row(root, "openai"), "Manage").click());
  const form = row(root, "openai").querySelector(LLM_FORM);
  const base = form.querySelector("input[type=url]");
  assert.equal(base.value, OPENAI_BASE_URL);
  assert.match(form.querySelector(KEY_INPUT).placeholder, /Saved/);
  await act(() => button(form, "Test connection").click());
  await settle();
  assert.match(form.textContent, /It did not connect: 401 bad key/);
  await type(base, "http://example.com/v1");
  await act(() => button(form, "Save").click());
  await settle();
  assert.match(form.textContent, /plain http outside your network/);
  await done();
});

test("a saved key can be disconnected after a confirmation", async () => {
  const gw = fakeGateway();
  const { root, notices, done } = await open(gw);
  await act(() => button(row(root, "openai"), "Manage").click());
  const form = row(root, "openai").querySelector(LLM_FORM);
  await act(() => button(form, "Disconnect").click());
  await act(() => button(form, "Cancel").click());
  assert.equal(button(form, "Confirm"), undefined);
  await act(() => button(form, "Disconnect").click());
  await act(() => button(form, "Confirm").click());
  await settle();
  assert.equal(gw.calls.filter((c) => c.method === "DELETE").length, 1);
  assert.deepEqual(notices.at(-1), ["Disconnected from OpenAI", false]);
  await done();
});

test("a key from the environment or a login command is explained, not asked for", async () => {
  const gw = fakeGateway();
  const { root, done } = await open(gw);
  await act(() => button(row(root, "gemini"), "Manage").click());
  let form = row(root, "gemini").querySelector(LLM_FORM);
  assert.equal(form.querySelector("input"), null);
  assert.match(form.textContent, /GEMINI_API_KEY on the gateway's machine/);
  assert.equal(button(form, "Save"), undefined);
  await act(() => button(row(root, CLAUDE_CODE), "Manage").click());
  form = row(root, CLAUDE_CODE).querySelector(LLM_FORM);
  assert.match(form.textContent, /claude auth login/);
  await act(() => button(row(root, "qwen"), "Connect").click());
  form = row(root, "qwen").querySelector(LLM_FORM);
  assert.ok(button(form, SIGN_IN));
  await done();
});

test("the default model is picked among the connected providers and saved", async () => {
  const gw = fakeGateway();
  const { root, notices, done } = await open(gw);
  const form = root.querySelector("[data-testid=llm-default]");
  const save = button(form, "Save");
  assert.equal(save.disabled, true, "nothing changed yet");
  const select = form.querySelector("select");
  select.value = "gemini";
  await act(() =>
    select.dispatchEvent(new window.Event("change", { bubbles: true })),
  );
  assert.equal(form.querySelector(DEFAULT_MODEL_INPUT).value, "gemini-1");
  await act(() => save.click());
  await settle();
  assert.deepEqual(gw.calls.at(-1).body, {
    provider: "gemini",
    model: "gemini-1",
  });
  assert.deepEqual(notices.at(-1), [
    "New sessions will start with gemini-1",
    false,
  ]);
  assert.match(row(root, "gemini").textContent, /Default|GEMINI/);
  await done();
});

test("a refused default and a gateway with no file say so", async () => {
  const view = VIEW();
  view.writable = false;
  const gw = fakeGateway(view);
  const { root, done } = await open(gw);
  assert.ok(root.querySelector("[data-testid=llm-readonly]"));
  assert.equal(root.querySelector("#llm-default-provider").disabled, true);
  await done();

  const gw2 = fakeGateway(VIEW(), {
    "PUT /v1/llm/default": [
      HTTP_CONFLICT,
      { error: "connect Gemini first", code: "no_key" },
    ],
  });
  const second = await open(gw2);
  const model = second.root.querySelector(DEFAULT_MODEL_INPUT);
  await type(model, "gpt-5");
  await act(() =>
    button(
      second.root.querySelector("[data-testid=llm-default]"),
      "Save",
    ).click(),
  );
  await settle();
  assert.match(second.root.textContent, /connect Gemini first/);
  await second.done();
});

test("on a plain http address the tab warns that a key travels unencrypted", async () => {
  Object.defineProperty(window, "isSecureContext", {
    value: false,
    configurable: true,
  });
  const { root, done } = await open(fakeGateway());
  const pill = root.querySelector("[data-testid=insecure-warning]");
  assert.ok(pill);
  assert.match(pill.textContent, /travels to Motita unencrypted/);
  assert.match(
    pill.querySelector("details").textContent,
    /before pasting a key/,
  );
  delete window.isSecureContext;
  await done();
});

test("a gateway that cannot be read is reported", async () => {
  const api = async () =>
    new Response(JSON.stringify({ error: "down" }), {
      status: HTTP_BAD_GATEWAY,
    });
  const { root, done } = await open({ api });
  assert.match(root.textContent, /down/);
  await done();
});

test("every string of the providers tab has a Spanish translation", () => {
  const keys = new Set();
  for (const f of ["ProviderSettings.tsx", "InsecureWarning.tsx"]) {
    for (const m of src(f).matchAll(
      /\bt[fc]?\(\s*(['"])((?:(?!\1)[^\\]|\\.)*)\1/g,
    )) {
      keys.add(m[2].replaceAll(/\\(['"])/g, "$1"));
    }
  }
  // The catalogue's access lines come from the gateway, in English, and are translated too.
  const catalogue = readFileSync(
    join(here, "..", "..", "..", "internal", "onboard", "providers.go"),
    "utf8",
  );
  for (const m of catalogue.matchAll(/Access:\s+"([^"]+)"/g)) {
    keys.add(m[1]);
  }
  assert.ok(keys.size > MIN_PROVIDER_STRINGS);
  assert.deepEqual(
    [...keys].filter((k) => !es[k]),
    [],
  );
  for (const k of keys) {
    const ph = (s) => (s.match(/\{\w+\}/g) || []).sort().join();
    assert.equal(ph(es[k]), ph(k), k);
  }
});

test("the form opens the full width of its row, and compact density tightens it", () => {
  const css = src("index.css");
  assert.match(css, /\.llm-form \{[^}]*flex: 1 1 100%/);
  assert.match(css, /html\[data-density="compact"\] \.llm-form \{/);
});
