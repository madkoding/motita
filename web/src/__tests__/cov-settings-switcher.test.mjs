// The model switcher of a session, through the real useModelSwitcher hook: the providers and the
// live model list it loads, the draft that only Done sends, the warnings before and after a
// switch, and the refusals.
// Run with: node --test web/src/__tests__/cov-settings-switcher.test.mjs
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
  byText,
} from "./cov-settings-helpers.mjs";

installDom();
const { useModelSwitcher } = await load("useModelSwitcher.ts");
const { ModelSwitcherModal } = await load("ModelSwitcherModal.tsx");
const { AppCtx } = await load("appStore.ts");
const { h, render } = await import("preact");

const PROVIDERS = [
  { id: "anthropic", name: "Anthropic", models: ["claude-a", "claude-b"], fetch_models: false, key_present: true, is_current: true },
  { id: "ollama", name: "Ollama", models: ["llama3"], fetch_models: true, key_present: false, is_current: false },
  { id: "empty", name: "Empty", models: [], fetch_models: false, key_present: true, is_current: false },
];
const CONFIG = { provider: "anthropic", model: "claude-a", reasoning: "high", reasoning_enabled: true, api_key_present: true };

function mount(api, { sessionId = "s1", rs = { messages: [], running: false }, config = CONFIG } = {}) {
  const log = { states: [], toasts: [], saved: 0 };
  const ref = {};
  function Harness({ rs }) {
    const model = useModelSwitcher(
      api,
      sessionId,
      (s, bad) => log.states.push([s, bad]),
      (t) => log.toasts.push(t),
      () => log.saved++,
    );
    ref.m = model;
    return h(AppCtx.Provider, { value: { model, rs } }, model.showModelSwitcher ? h(ModelSwitcherModal, null) : null);
  }
  const root = document.createElement("div");
  document.body.appendChild(root);
  render(h(Harness, { rs }), root);
  return {
    root,
    log,
    ref,
    async init() {
      await settle();
      if (config) {
        ref.m.setConfig(config);
        await settle();
      }
    },
    async rerender(rs2) {
      render(h(Harness, { rs: rs2 }), root);
      await settle();
    },
    async open() {
      ref.m.openModelSwitcher();
      await settle();
    },
    async unmount() {
      render(null, root);
      root.remove();
      await settle();
    },
  };
}
async function press(el) {
  click(el);
  await settle();
}
const done = (root) => byText(root, "button", "Done");
const modelSel = (root) => root.querySelector("#model-switcher-model");
const choice = (root, name) => byText(root, "[role=radio].provider-choice", name);
const level = (root, l) => byText(root, ".reasoning-level", l);

test("opening loads the session's providers and live models, seeded from the saved choice", async () => {
  let list;
  const f = fakeApi({
    "GET /v1/sessions/s1/providers": json(200, { providers: PROVIDERS }),
    "GET /v1/sessions/s1/model-list": () => new Promise((r) => (list = r)),
  });
  const m = mount(f.api);
  await m.init();
  await m.open();
  assert.deepEqual(f.calls.map((c) => c.path), ["/v1/sessions/s1/providers", "/v1/sessions/s1/model-list"]);
  // The saved provider's models are loading.
  assert.match(text(m.root), /Loading models\.\.\./);
  assert.equal(modelSel(m.root), null);
  list(json(200, { models: ["claude-live", "claude-a"] }));
  await settle();
  assert.deepEqual([...modelSel(m.root).options].map((o) => o.value), ["claude-live", "claude-a", "claude-b"]);
  assert.equal(modelSel(m.root).value, "claude-a");
  // The providers, with their logo or initial, the active one marked.
  const a = choice(m.root, "Anthropic");
  assert.equal(a.getAttribute("aria-checked"), "true");
  assert.match(a.className, /is-active/);
  assert.equal(a.title, "Anthropic");
  assert.match(text(a), /\(active\)/);
  const o = choice(m.root, "Ollama");
  assert.equal(o.title, "Ollama — no key needed");
  assert.equal(o.getAttribute("aria-checked"), "false");
  assert.equal(text(choice(m.root, "Empty").querySelector("[data-logo=empty]")), "E");
  assert.equal(level(m.root, "high").getAttribute("aria-checked"), "true");
  assert.match(text(m.root), /API key: ✓ set/);
  // Nothing changed: Done is off.
  assert.equal(done(m.root).disabled, true);
  await m.unmount();
});

test("the draft changes nothing until Done, and closing drops it", async () => {
  const f = fakeApi({
    "GET /v1/sessions/s1/providers": json(200, { providers: PROVIDERS }),
    "GET /v1/sessions/s1/model-list": json(200, { models: [] }),
  });
  const m = mount(f.api);
  await m.init();
  await m.open();
  // Another provider: its first model. Picking it again changes nothing.
  await press(choice(m.root, "Ollama"));
  assert.equal(modelSel(m.root).value, "llama3");
  assert.equal(done(m.root).disabled, false);
  await press(choice(m.root, "Ollama"));
  assert.equal(m.ref.m.draftProvider, "ollama");
  // One with no models: none.
  await press(choice(m.root, "Empty"));
  assert.equal(m.ref.m.draftModel, "");
  assert.equal(modelSel(m.root).options.length, 0);
  // Back to the saved provider: the saved model.
  await press(choice(m.root, "Anthropic"));
  assert.equal(modelSel(m.root).value, "claude-a");
  assert.equal(done(m.root).disabled, true);
  // Another model, or another reasoning level, is a change.
  modelSel(m.root).value = "claude-b";
  modelSel(m.root).dispatchEvent(new window.Event("change", { bubbles: true }));
  await settle();
  assert.equal(done(m.root).disabled, false);
  modelSel(m.root).value = "claude-a";
  modelSel(m.root).dispatchEvent(new window.Event("change", { bubbles: true }));
  await settle();
  await press(level(m.root, "low"));
  assert.equal(level(m.root, "low").getAttribute("aria-checked"), "true");
  assert.equal(done(m.root).disabled, false);

  // The ✕ drops the draft and closes.
  await press(m.root.querySelector("button[aria-label=Close]"));
  assert.equal(m.ref.m.showModelSwitcher, false);
  assert.equal(m.ref.m.draftReasoning, "high");
  assert.equal(m.ref.m.draftProvider, "anthropic");
  // A click on the backdrop closes too; nothing was ever sent.
  await m.open();
  await press(choice(m.root, "Ollama"));
  await press(m.root.querySelector("[data-modal]"));
  assert.equal(m.ref.m.showModelSwitcher, false);
  assert.equal(f.calls.filter((c) => c.method !== "GET").length, 0);
  // The next open seeds from the saved choice again.
  await m.open();
  assert.equal(m.ref.m.draftProvider, "anthropic");
  await m.unmount();
});

test("before Done the switcher warns about a conversation that talked or a running turn", async () => {
  const f = fakeApi({
    "GET /v1/sessions/s1/providers": json(200, { providers: PROVIDERS }),
    "GET /v1/sessions/s1/model-list": json(200, {}),
  });
  const m = mount(f.api, { rs: { messages: [{}], running: false } });
  await m.init();
  await m.open();
  assert.equal(m.root.querySelector("[role=note]"), null);
  await press(choice(m.root, "Ollama"));
  assert.match(text(m.root.querySelector("[role=note]")), /already has messages/);
  await m.rerender({ messages: [{}], running: true });
  assert.match(text(m.root.querySelector("[role=note]")), /A turn is running/);
  await m.rerender({ messages: [], running: false });
  assert.equal(m.root.querySelector("[role=note]"), null);
  // A model change alone is a change too.
  await press(choice(m.root, "Anthropic"));
  modelSel(m.root).value = "claude-b";
  modelSel(m.root).dispatchEvent(new window.Event("change", { bubbles: true }));
  await m.rerender({ messages: [{}], running: false });
  assert.ok(m.root.querySelector("[role=note]"));
  await m.unmount();
});

test("Done sends the draft, closes, warns about the switch and refreshes the list", async () => {
  let answer;
  const f = fakeApi({
    "GET /v1/sessions/s1/providers": json(200, { providers: PROVIDERS }),
    "GET /v1/sessions/s1/model-list": json(200, { models: ["claude-a"] }),
    "PATCH /v1/sessions/s1/config": () => new Promise((r) => (answer = r)),
  });
  const m = mount(f.api);
  await m.init();
  await m.open();
  await press(choice(m.root, "Ollama"));
  await press(level(m.root, "off"));
  await press(done(m.root));
  assert.deepEqual(f.calls.at(-1), {
    method: "PATCH",
    path: "/v1/sessions/s1/config",
    body: { provider: "ollama", model: "llama3", reasoning: "off" },
  });
  // While it saves, Done waits with a spinner.
  assert.equal(done(m.root).disabled, true);
  assert.ok(done(m.root).querySelector("svg"));
  answer(json(200, { provider: "ollama", model: "llama3", reasoning: "", reasoning_enabled: false, api_key_present: false, warnings: ["history", "running"], previous_model: "claude-a" }));
  await settle();
  assert.equal(m.ref.m.showModelSwitcher, false);
  assert.equal(m.ref.m.config.provider, "ollama");
  assert.equal(m.log.saved, 1);
  assert.equal(m.log.toasts.length, 1);
  assert.match(m.log.toasts[0].message, /Model switched to llama3/);

  // The next open shows the new choice; a switch without warnings shows no toast.
  await m.open();
  assert.match(text(m.root), /API key: ✗ missing/);
  assert.equal(level(m.root, "off").getAttribute("aria-checked"), "true");
  await press(choice(m.root, "Anthropic"));
  await press(done(m.root));
  answer(json(200, CONFIG));
  await settle();
  assert.equal(m.log.toasts.length, 1);
  assert.equal(m.log.saved, 2);
  await m.unmount();
});

test("a refused or failed save keeps the modal open and says why", async () => {
  const answers = [json(409, { error: "provider has no key" }), new Response("x", { status: 500 }), new Error("offline")];
  const f = fakeApi({
    "GET /v1/sessions/s1/providers": json(200, { providers: PROVIDERS }),
    "GET /v1/sessions/s1/model-list": json(200, { models: [] }),
    "PATCH /v1/sessions/s1/config": () => answers.shift(),
  });
  const m = mount(f.api);
  await m.init();
  await m.open();
  await press(choice(m.root, "Ollama"));
  for (let i = 0; i < 3; i++) {
    await press(done(m.root));
    assert.equal(m.ref.m.showModelSwitcher, true);
    assert.equal(done(m.root).disabled, false);
  }
  assert.deepEqual(m.log.states, [
    ["provider has no key", true],
    ["could not update the configuration", true],
    ["could not update the configuration", true],
  ]);
  assert.equal(m.log.saved, 0);
  await m.unmount();
});

test("lists that cannot be read leave the modal usable on the catalogue", async () => {
  const f = fakeApi({
    "GET /v1/sessions/s1/providers": new Error("offline"),
    "GET /v1/sessions/s1/model-list": json(500, { error: "x" }),
  });
  const m = mount(f.api);
  await m.init();
  await m.open();
  assert.deepEqual(m.ref.m.providers, []);
  assert.deepEqual(m.ref.m.modelList, []);
  assert.equal(m.ref.m.fetchingModels, false);
  assert.deepEqual([...modelSel(m.root).options].map((o) => o.value), ["claude-a"]);
  await m.unmount();

  const g = fakeApi({
    "GET /v1/sessions/s1/providers": json(200, {}),
    "GET /v1/sessions/s1/model-list": new Error("offline"),
  });
  const n = mount(g.api);
  await n.init();
  await n.open();
  assert.deepEqual(n.ref.m.providers, []);
  assert.equal(n.ref.m.fetchingModels, false);
  await n.unmount();
});

test("without a session nothing is asked or sent; without a configuration the draft is empty", async () => {
  const f = fakeApi({});
  const m = mount(f.api, { sessionId: "", config: null });
  await m.init();
  await m.open();
  assert.equal(f.calls.length, 0);
  assert.equal(m.ref.m.draftProvider, "");
  assert.equal(m.ref.m.draftReasoning, "medium");
  assert.equal(done(m.root).disabled, true);
  // No configuration: no key line.
  assert.doesNotMatch(text(m.root), /API key/);
  // A provider picked with no configuration saved is a change.
  m.ref.m.setDraftProvider("x");
  await settle();
  assert.equal(done(m.root).disabled, false);
  await m.ref.m.applyProviderModel();
  assert.equal(f.calls.length, 0);
  m.ref.m.discardModelSwitcher();
  await settle();
  assert.equal(m.ref.m.draftProvider, "");
  m.ref.m.closeModelSwitcher();
  await m.unmount();
});

test("picking a provider with no saved configuration, or back to a saved one with no model", async () => {
  const f = fakeApi({
    "GET /v1/sessions/s1/providers": json(200, { providers: PROVIDERS }),
    "GET /v1/sessions/s1/model-list": json(200, { models: [] }),
  });
  const m = mount(f.api, { config: null });
  await m.init();
  await m.open();
  await press(choice(m.root, "Ollama"));
  assert.equal(m.ref.m.draftModel, "llama3");
  await m.unmount();

  const n = mount(f.api, { config: { ...CONFIG, model: "" } });
  await n.init();
  await n.open();
  await press(choice(n.root, "Ollama"));
  await press(choice(n.root, "Anthropic"));
  assert.equal(n.ref.m.draftModel, "");
  await n.unmount();
});
