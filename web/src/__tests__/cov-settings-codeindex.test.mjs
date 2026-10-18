// The Indexing tab: the switches, where the embeddings come from, the endpoint and limits, and
// the confirmation an address outside this machine needs.
// Run with: node --test web/src/__tests__/cov-settings-codeindex.test.mjs
import test from "node:test";
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
const { CodeIndexSettings } = await load("CodeIndexSettings.tsx");
const api = await import("../codeIndexApi.ts");
const es = (await import("../i18n.es.ts")).default;
const { h, render } = await import("preact");

const VIEW = {
  enabled: true,
  semantic: true,
  embeddings_auto: true,
  embeddings_url: "",
  embeddings_model: "",
  embeddings_key: false,
  max_file_kb: 256,
  max_files: 5000,
  embeddings: "auto",
  endpoint_local: false,
  can_save: true,
};

const mounted = [];
async function mount(fake) {
  const root = document.createElement("div");
  document.body.appendChild(root);
  render(h(CodeIndexSettings, { api: fake.api }), root);
  await settle();
  mounted.push(root);
  return root;
}

// gateway keeps the saved settings and derives the source the way the Go side does.
function gateway(initial, refuse) {
  let state = { ...initial };
  return fakeApi({
    "GET /v1/code-index": () => json(200, state),
    "PUT /v1/code-index": (_p, init) => {
      const { confirm, ...body } = JSON.parse(init.body);
      if (refuse) {
        return json(400, { error: refuse });
      }
      state = { ...state, ...body };
      state.embeddings = state.embeddings_url
        ? "endpoint"
        : state.embeddings_auto
          ? "auto"
          : "off";
      state.endpoint_local = /localhost/.test(state.embeddings_url);
      return json(200, state);
    },
  });
}

const radio = (root, name, value) =>
  root.querySelector(`input[name=${name}][value=${value}]`);
async function pick(root, name, value) {
  const r = radio(root, name, value);
  r.checked = true;
  r.dispatchEvent(new window.Event("change", { bubbles: true }));
  await settle();
}
const status = (root) =>
  text(root.querySelector("[data-testid=code-index-status]"));

test("the switches save one field at a time", async () => {
  const gw = gateway(VIEW);
  const root = await mount(gw);
  await pick(root, "settings-code-index-enabled", "off");
  await pick(root, "settings-code-index-semantic", "off");
  await pick(root, "settings-code-index-auto", "off");
  const puts = gw.calls.filter((c) => c.method === "PUT").map((c) => c.body);
  assert.deepEqual(puts, [
    { enabled: false },
    { semantic: false },
    { embeddings_auto: false },
  ]);
  assert.match(status(root), /No embeddings/);
});

test("the status line says where the vectors come from", async () => {
  let root = await mount(gateway(VIEW));
  assert.match(status(root), /local Ollama/);
  root = await mount(
    gateway({
      ...VIEW,
      embeddings: "endpoint",
      embeddings_url: "http://localhost:11434/v1",
      embeddings_model: "nomic-embed-text",
      endpoint_local: true,
      embeddings_key: true,
    }),
  );
  assert.match(status(root), /on this machine or its network/);
  assert.match(text(root), /A key is set/);
  root = await mount(
    gateway({
      ...VIEW,
      embeddings: "endpoint",
      embeddings_url: "https://api.openai.com/v1",
      embeddings_model: "m",
    }),
  );
  assert.match(status(root), /every question go there/);
});

test("a local address is saved at once, with the limits", async () => {
  const gw = gateway(VIEW);
  const root = await mount(gw);
  const [url, model] = root.querySelectorAll(
    "input[type=url], input[type=text]",
  );
  type(url, " http://localhost:11434/v1 ");
  await settle();
  type(model, "nomic-embed-text");
  await settle();
  const [kb, files] = root.querySelectorAll("input[type=number]");
  type(kb, "64");
  await settle();
  type(files, "100");
  await settle();
  click(byText(root, "button", "Save"));
  await settle();
  assert.deepEqual(gw.calls.at(-1).body, {
    embeddings_url: "http://localhost:11434/v1",
    embeddings_model: "nomic-embed-text",
    max_file_kb: 64,
    max_files: 100,
    confirm: false,
  });
  assert.match(status(root), /nomic-embed-text/);
  assert.equal(root.querySelector("[data-testid=code-index-confirm]"), null);
});

test("an address outside the network asks first, and Cancel sends nothing", async () => {
  const gw = gateway(VIEW);
  const root = await mount(gw);
  type(root.querySelector("input[type=url]"), "https://api.openai.com/v1");
  await settle();
  click(byText(root, "button", "Save"));
  await settle();
  const ask = root.querySelector("[data-testid=code-index-confirm]");
  assert.match(text(ask), /sent to https:\/\/api.openai.com\/v1/);
  click(byText(ask, "button", "Cancel"));
  await settle();
  assert.equal(root.querySelector("[data-testid=code-index-confirm]"), null);
  assert.equal(gw.calls.filter((c) => c.method === "PUT").length, 0);

  click(byText(root, "button", "Save"));
  await settle();
  click(
    byText(
      root.querySelector("[data-testid=code-index-confirm]"),
      "button",
      "Send my code there",
    ),
  );
  await settle();
  assert.equal(gw.calls.at(-1).body.confirm, true);
  assert.equal(root.querySelector("[data-testid=code-index-confirm]"), null);
});

test("clearing the address needs no confirmation", async () => {
  const gw = gateway({
    ...VIEW,
    embeddings: "endpoint",
    embeddings_url: "https://api.openai.com/v1",
  });
  const root = await mount(gw);
  type(root.querySelector("input[type=url]"), "");
  await settle();
  click(byText(root, "button", "Save"));
  await settle();
  assert.equal(gw.calls.at(-1).body.embeddings_url, "");
  assert.match(status(root), /local Ollama/);
});

test("a refusal is shown and the settings stay", async () => {
  const root = await mount(gateway(VIEW, "embeddings_model is empty"));
  await pick(root, "settings-code-index-semantic", "off");
  assert.match(
    text(root.querySelector("[data-testid=code-index-error]")),
    /embeddings_model is empty/,
  );
});

test("without a file nothing can be changed, and the tab says why", async () => {
  const root = await mount(gateway({ ...VIEW, can_save: false }));
  assert.match(text(root), /built-in defaults/);
  assert.ok(radio(root, "settings-code-index-enabled", "off").disabled);
});

test("a gateway without the API hides the tab, and a failed read shows the error", async () => {
  let root = await mount(fakeApi({ "GET /v1/code-index": json(404) }));
  assert.equal(root.querySelector("section"), null);
  root = await mount(
    fakeApi({ "GET /v1/code-index": json(500, { error: "boom" }) }),
  );
  assert.match(text(root), /boom/);
  assert.equal(root.querySelector("input"), null);
});

test("codeIndexApi reads errors without a body and guesses what is local", async () => {
  await assert.rejects(
    api.getCodeIndex(async () => new Response("x", { status: 500 })),
    /HTTP 500/,
  );
  for (const [raw, want] of Object.entries({
    "http://localhost:11434/v1": true,
    "http://[::1]:8080": true,
    "http://127.0.0.1:1": true,
    "http://192.168.1.5": true,
    "http://172.20.0.1": true,
    "http://172.32.0.1": false,
    "http://nas.local": true,
    "https://api.openai.com/v1": false,
    "not a url": false,
  })) {
    assert.equal(api.looksLocal(raw), want, raw);
  }
});

test("every new string has its Spanish", async () => {
  const src = (await import("node:fs")).readFileSync(
    new URL("../CodeIndexSettings.tsx", import.meta.url),
    "utf8",
  );
  for (const m of src.matchAll(/\bt[f]?\(\s*"((?:[^"\\]|\\.)*)"/g)) {
    assert.ok(es[m[1]], `missing Spanish for: ${m[1]}`);
  }
});

test("leaving the tab stops listening for the answer", async () => {
  const root = await mount(gateway(VIEW));
  for (const r of mounted) {
    render(null, r);
  }
  await settle();
  assert.equal(root.querySelector("section"), null);
});

test("the index cache is cleared and the result is shown", async () => {
  const gw = fakeApi({
    "GET /v1/code-index": () => json(200, VIEW),
    "DELETE /v1/code-index/cache": () => json(200, { removed: 2, bytes: 3000 }),
  });
  const root = await mount(gw);
  click(byText(root, "button", "Clear index cache"));
  await settle();
  assert.equal(gw.calls.at(-1).method, "DELETE");
  assert.match(
    text(root.querySelector("[data-testid=code-index-cleared]")),
    /2 index files deleted \(3 KB\)/,
  );
});

test("a cache that cannot be cleared shows the error", async () => {
  const gw = fakeApi({
    "GET /v1/code-index": () => json(200, VIEW),
    "DELETE /v1/code-index/cache": () => json(500, { error: "disk is busy" }),
  });
  const root = await mount(gw);
  click(byText(root, "button", "Clear index cache"));
  await settle();
  assert.match(
    text(root.querySelector("[data-testid=code-index-error]")),
    /disk is busy/,
  );
  assert.equal(root.querySelector("[data-testid=code-index-cleared]"), null);
});

test("clearing the cache falls back to the status when the error has no body", async () => {
  const fake = async () => ({ ok: false, status: 502, json: async () => ({}) });
  await assert.rejects(api.clearCodeIndexCache(fake), /HTTP 502/);
  const broken = async () => ({
    ok: false,
    status: 500,
    json: async () => {
      throw new Error("no body");
    },
  });
  await assert.rejects(api.clearCodeIndexCache(broken), /HTTP 500/);
});
