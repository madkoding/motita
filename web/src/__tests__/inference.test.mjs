// Settings → Inference: the list of servers that run models. A key goes to the gateway and never
// comes back, a test shows how the server answered, and a gateway without the API hides the section.
// Run with: node --test web/src/__tests__/inference.test.mjs
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
} from "./cov-settings-helpers.mjs";

installDom();
const { InferenceSettings } = await load("InferenceSettings.tsx");
const { SettingsModal } = await load("Settings.tsx");
const api = await import("../inferenceApi.ts");
const es = (await import("../i18n.es.ts")).default;
const { h, render } = await import("preact");

const KINDS = [
  {
    id: "llamacpp",
    name: "llama.cpp",
    default_base_url: "http://127.0.0.1:8080/v1",
    provider: "custom",
  },
  {
    id: "openai",
    name: "OpenAI-compatible",
    default_base_url: "",
    provider: "custom",
  },
];
const GPU = {
  id: "gpu",
  name: "GPU box",
  kind: "llamacpp",
  base_url: "http://10.0.0.5:8080/v1",
  model: "qwen",
  provider: "custom",
  has_key: true,
  key_hint: "…1234",
  in_use: false,
};
const PLAIN = {
  id: "plain",
  name: "Plain",
  kind: "openai",
  base_url: "https://x.test/v1",
  provider: "custom",
  has_key: false,
  in_use: false,
};
const SHORT = { ...PLAIN, id: "short", name: "Short", has_key: true };
const LOCAL = {
  id: "motita-local",
  name: "Motita local model",
  kind: "llamacpp",
  base_url: "http://127.0.0.1:8471/v1",
  provider: "local",
  has_key: false,
  in_use: true,
  managed: true,
};
const ODD = { ...PLAIN, id: "odd", name: "Odd", kind: "gone" };
const VIEW = {
  can_save: true,
  max: 32,
  kinds: KINDS,
  servers: [GPU, PLAIN, SHORT],
};

async function mount(a) {
  const root = document.createElement("div");
  document.body.appendChild(root);
  render(h(InferenceSettings, { api: a }), root);
  await settle();
  return {
    root,
    async unmount() {
      render(null, root);
      root.remove();
      await settle();
    },
  };
}

function gateway(initial, probe) {
  let state = structuredClone(initial);
  return fakeApi({
    "GET /v1/inference-servers": () => json(200, state),
    "POST /v1/inference-servers/": (p) => {
      const id = p.split("/")[3];
      if (p.endsWith("/use")) {
        state.servers = state.servers.map((s) => ({
          ...s,
          in_use: s.id === id,
        }));
        return json(200, state);
      }
      return probe(id);
    },
    "POST /v1/inference-servers": (_p, init) => {
      const b = JSON.parse(init.body);
      if (b.name === "Refused") {
        return json(400, { error: "that address is not an endpoint" });
      }
      state.servers = [...state.servers, { ...PLAIN, id: "new", name: b.name }];
      return json(201, state);
    },
    "PUT /v1/inference-servers/": (p, init) => {
      const b = JSON.parse(init.body);
      state.servers = state.servers.map((s) =>
        s.id === p.split("/").pop() ? { ...s, name: b.name } : s,
      );
      return json(200, state);
    },
    "DELETE /v1/inference-servers/": (p) => {
      state.servers = state.servers.filter((s) => s.id !== p.split("/").pop());
      return json(200, state);
    },
  });
}

const q = (root, id) => root.querySelector(`[data-testid="${id}"]`);
const probes = {
  gpu: () => json(200, { ok: true, latency_ms: 12, models: ["a", "b"] }),
  plain: () => json(200, { ok: false, latency_ms: 3, error: "refused" }),
  short: () => json(200, { ok: true, latency_ms: 1 }),
  boom: () => json(500, { error: "gateway down" }),
};

test("it lists servers, adds, edits and removes one", async () => {
  const f = gateway(VIEW, (id) => probes[id]());
  const m = await mount(f.api);
  const list = text(q(m.root, "inference-list"));
  assert.match(list, /GPU box/);
  assert.match(list, /qwen/);
  assert.match(list, /Key saved \(…1234\)/);
  assert.match(list, /No key/);
  assert.match(list, /Key saved(?! \()/);
  assert.equal(q(m.root, "inference-save").disabled, true);

  type(q(m.root, "inference-name"), "Fresh");
  await settle();
  assert.equal(
    q(m.root, "inference-save").disabled,
    false,
    "the kind's usual address stands in",
  );
  type(q(m.root, "inference-base"), "http://h:1/v1");
  type(q(m.root, "inference-model"), "m");
  type(q(m.root, "inference-key"), "sekret");
  const kind = q(m.root, "inference-kind");
  kind.value = "openai";
  kind.dispatchEvent(new window.Event("change", { bubbles: true }));
  await settle();
  click(q(m.root, "inference-save"));
  await settle();
  const post = f.calls.find((c) => c.method === "POST");
  assert.deepEqual(post.body, {
    name: "Fresh",
    kind: "openai",
    base_url: "http://h:1/v1",
    model: "m",
    api_key: "sekret",
  });
  assert.match(text(q(m.root, "inference-list")), /Fresh/);
  assert.equal(q(m.root, "inference-name").value, "", "the form starts over");

  click(q(m.root, "inference-edit-gpu"));
  await settle();
  assert.equal(q(m.root, "inference-name").value, "GPU box");
  assert.match(q(m.root, "inference-key").placeholder, /keep the saved key/);
  click(q(m.root, "inference-cancel"));
  await settle();
  assert.equal(q(m.root, "inference-cancel"), null);
  click(q(m.root, "inference-edit-gpu"));
  await settle();
  type(q(m.root, "inference-name"), "GPU renamed");
  await settle();
  click(q(m.root, "inference-save"));
  await settle();
  const put = f.calls.find((c) => c.method === "PUT");
  assert.equal(put.path, "/v1/inference-servers/gpu");
  assert.equal(
    "api_key" in put.body,
    false,
    "an empty key leaves the saved one",
  );
  assert.match(text(q(m.root, "inference-list")), /GPU renamed/);

  click(q(m.root, "inference-remove-plain"));
  await settle();
  assert.doesNotMatch(text(q(m.root, "inference-list")), /Plain/);
  await m.unmount();
});

test("a test shows how the server answered", async () => {
  const f = gateway(VIEW, (id) => probes[id]());
  const m = await mount(f.api);
  click(q(m.root, "inference-test-gpu"));
  await settle();
  assert.match(text(q(m.root, "inference-probe-gpu")), /2 models in 12 ms/);
  click(q(m.root, "inference-test-plain"));
  await settle();
  assert.match(
    text(q(m.root, "inference-probe-plain")),
    /Not reachable: refused/,
  );
  click(q(m.root, "inference-test-short"));
  await settle();
  assert.match(text(q(m.root, "inference-probe-short")), /0 models in 1 ms/);
  await m.unmount();

  const g = gateway(VIEW, probes.boom);
  const n = await mount(g.api);
  click(q(n.root, "inference-test-gpu"));
  await settle();
  assert.match(text(q(n.root, "inference-probe-gpu")), /gateway down/);
  await n.unmount();
});

test("a server of a kind the page does not know can still be edited, and a silent failure is shown", async () => {
  const f = gateway({ ...VIEW, servers: [ODD] }, () =>
    json(200, { ok: false, latency_ms: 2 }),
  );
  const m = await mount(f.api);
  click(q(m.root, "inference-edit-odd"));
  await settle();
  assert.equal(q(m.root, "inference-base").placeholder, "https://host:port/v1");
  click(q(m.root, "inference-test-odd"));
  await settle();
  assert.match(text(q(m.root, "inference-probe-odd")), /Not reachable:/);
  await m.unmount();
});

test("a test in flight says so", async () => {
  let release;
  const f = gateway(VIEW, () => new Promise((r) => (release = r)));
  const m = await mount(f.api);
  click(q(m.root, "inference-test-gpu"));
  await settle();
  assert.match(text(q(m.root, "inference-probe-gpu")), /Testing…/);
  assert.equal(q(m.root, "inference-test-gpu").disabled, true);
  release(json(200, { ok: true, latency_ms: 1, models: [] }));
  await settle();
  assert.match(text(q(m.root, "inference-probe-gpu")), /0 models in 1 ms/);
  await m.unmount();
});

test("a refused add shows the reason and keeps what was typed", async () => {
  const m = await mount(gateway(VIEW, probes.gpu).api);
  type(q(m.root, "inference-name"), "Refused");
  await settle();
  click(q(m.root, "inference-save"));
  await settle();
  assert.match(text(q(m.root, "inference-error")), /not an endpoint/);
  assert.equal(q(m.root, "inference-name").value, "Refused");
  await m.unmount();
});

test("an empty list says so, and no file means no saving", async () => {
  const m = await mount(
    gateway({ can_save: false, max: 32, kinds: KINDS, servers: [] }, probes.gpu)
      .api,
  );
  assert.ok(q(m.root, "inference-empty"));
  assert.equal(q(m.root, "inference-name").disabled, true);
  assert.match(text(m.root), /cannot be saved here/);
  assert.match(text(m.root), /^((?!Up to).)*$/s);
  await m.unmount();

  const n = await mount(gateway({ ...VIEW, can_save: false }, probes.gpu).api);
  assert.equal(q(n.root, "inference-edit-gpu").disabled, true);
  assert.equal(q(n.root, "inference-remove-gpu").disabled, true);
  await n.unmount();

  const o = await mount(gateway({ ...VIEW, servers: [] }, probes.gpu).api);
  assert.match(text(o.root), /Up to 32 servers/);
  await o.unmount();
});

test("a gateway without the API hides the section, and a failing one says why", async () => {
  let m = await mount(
    fakeApi({ "GET /v1/inference-servers": json(404, {}) }).api,
  );
  assert.equal(q(m.root, "inference-setting"), null);
  await m.unmount();
  m = await mount(
    fakeApi({ "GET /v1/inference-servers": json(500, { error: "boom" }) }).api,
  );
  assert.match(text(q(m.root, "inference-error")), /boom/);
  await m.unmount();
  m = await mount(fakeApi({ "GET /v1/inference-servers": json(500, {}) }).api);
  assert.match(text(q(m.root, "inference-error")), /HTTP 500/);
  await m.unmount();
});

test("the client encodes the id and the dictionary speaks Spanish", async () => {
  const f = fakeApi({
    "PUT /v1/inference-servers/": json(200, VIEW),
    "DELETE /v1/inference-servers/": json(200, VIEW),
    "POST /v1/inference-servers/": json(200, { ok: true, latency_ms: 1 }),
    "GET /v1/inference-servers": new Response("oops", { status: 502 }),
  });
  await api.editInference(f.api, "a b", {});
  await api.removeInference(f.api, "a b");
  await api.probeInference(f.api, "a b");
  assert.deepEqual(
    f.calls.map((c) => c.path),
    [
      "/v1/inference-servers/a%20b",
      "/v1/inference-servers/a%20b",
      "/v1/inference-servers/a%20b/probe",
    ],
  );
  await assert.rejects(api.getInference(f.api), /HTTP 502/);
  for (const k of [
    "Inference",
    "Inference servers",
    "No servers yet.",
    "Server name",
    "Test {name}",
    "{count} models in {ms} ms",
    "Not reachable: {error}",
  ]) {
    assert.ok(es[k], k);
  }
});

test("the Inference tab appears in Settings only when the page hands it an API", async () => {
  const root = document.createElement("div");
  document.body.appendChild(root);
  const f = gateway(VIEW, probes.gpu);
  render(
    h(SettingsModal, {
      onClose() {},
      inference: { api: f.api },
      tab: "inference",
    }),
    root,
  );
  await settle();
  assert.ok(root.querySelector("#settings-tab-inference"));
  assert.ok(q(root, "inference-setting"));
  render(h(SettingsModal, { onClose() {} }), root);
  await settle();
  assert.equal(root.querySelector("#settings-tab-inference"), null);
  render(null, root);
});

test("using a server marks it in use, and the managed local model has no buttons", async () => {
  const f = gateway({ ...VIEW, servers: [LOCAL, GPU, PLAIN] }, probes.gpu);
  const m = await mount(f.api);
  assert.ok(q(m.root, "inference-inuse-motita-local"));
  assert.match(
    text(q(m.root, "inference-row-motita-local")),
    /Managed in Settings → Providers/,
  );
  assert.equal(q(m.root, "inference-use-motita-local"), null);
  assert.equal(q(m.root, "inference-remove-motita-local"), null);
  assert.equal(q(m.root, "inference-inuse-gpu"), null);
  click(q(m.root, "inference-use-gpu"));
  await settle();
  assert.equal(f.calls.at(-1).path, "/v1/inference-servers/gpu/use");
  assert.ok(q(m.root, "inference-inuse-gpu"));
  assert.equal(q(m.root, "inference-use-gpu").disabled, true);
  await m.unmount();
  assert.ok(es["Use {name}"] && es["In use"] && es["Use"]);
});
