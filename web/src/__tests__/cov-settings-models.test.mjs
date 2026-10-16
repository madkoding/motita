// The models tab of the settings: the default model new sessions start with, the model of each
// kind of work, and the model picker with its "Other…" field.
// Run with: node --test web/src/__tests__/cov-settings-models.test.mjs
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
const { ModelSettings } = await load("ModelSettings.tsx");
const { ModelPicker } = await load("ModelPicker.tsx");
const { h, render } = await import("preact");

const base = { access: "", default: false, models: [] };
const P = {
  anthropic: { ...base, id: "anthropic", name: "Anthropic", key: "stored", models: [{ id: "claude-a", label: "Claude A" }, { id: "claude-b", label: "Claude B" }], default: true },
  openai: { ...base, id: "openai", name: "OpenAI", key: "env", models: [{ id: "gpt-x", label: "GPT X" }], listed_models: ["gpt-live", "gpt-x"] },
  ollama: { ...base, id: "ollama", name: "Ollama", key: "not_needed" },
  gone: { ...base, id: "gone", name: "Gone", key: "missing", models: [{ id: "g1", label: "G1" }] },
};
const VIEW = {
  provider: "anthropic",
  model: "claude-a",
  writable: true,
  providers: Object.values(P),
  task_kinds: ["plan", "code", "review", "research", "summary", "triage"],
  tasks: { code: { provider: "openai", model: "gpt-x" } },
};

async function mount(vnode) {
  const root = document.createElement("div");
  document.body.appendChild(root);
  render(vnode, root);
  await settle();
  return {
    root,
    async rerender(v) {
      render(v, root);
      await settle();
    },
    async unmount() {
      render(null, root);
      root.remove();
      await settle();
    },
  };
}
async function select(el, value) {
  el.value = value;
  el.dispatchEvent(new window.Event("change", { bubbles: true }));
  await settle();
}
async function press(el) {
  click(el);
  await settle();
}
const btn = (root, label) => byText(root, "button", label);
const OTHER = "\u0000other";

// ── ModelPicker ─────────────────────────────────────────────────────────────

test("ModelPicker: a field alone for a provider with no list", async () => {
  const got = [];
  const m = await mount(h(ModelPicker, { id: "mp", provider: P.ollama, value: "llama3", disabled: false, ariaLabel: "Model", onChange: (v) => got.push(v) }));
  assert.equal(m.root.querySelector("select"), null);
  const input = m.root.querySelector("input");
  assert.equal(input.id, "mp");
  assert.equal(input.value, "llama3");
  assert.equal(input.getAttribute("aria-label"), "Model");
  type(input, "qwen");
  assert.deepEqual(got, ["qwen"]);
  await m.rerender(h(ModelPicker, { id: "mp", provider: undefined, value: "", disabled: true, onChange() {} }));
  assert.equal(m.root.querySelector("input").disabled, true);
  await m.unmount();
});

test("ModelPicker: a listed model is picked; Other… opens a field; an unlisted value shows in it", async () => {
  let value = "claude-a";
  const got = [];
  const props = () => ({ id: "mp", provider: P.anthropic, value, disabled: false, onChange: (v) => (got.push(v), (value = v)) });
  const m = await mount(h(ModelPicker, props()));
  const sel = m.root.querySelector("select#mp");
  assert.deepEqual([...sel.options].map((o) => o.textContent), ["Claude A", "Claude B", "Other…"]);
  assert.equal(sel.value, "claude-a");
  assert.equal(m.root.querySelector("input"), null);

  await select(sel, "claude-b");
  assert.deepEqual(got, ["claude-b"]);
  await m.rerender(h(ModelPicker, props()));

  // Other… from a listed model: the field opens empty.
  await select(sel, OTHER);
  assert.deepEqual(got, ["claude-b", ""]);
  await m.rerender(h(ModelPicker, props()));
  const field = m.root.querySelector("input#mp-other");
  assert.ok(field);
  assert.equal(sel.value, OTHER);
  type(field, "claude-next");
  await m.rerender(h(ModelPicker, props()));
  assert.equal(m.root.querySelector("input#mp-other").value, "claude-next");
  // Other… again while typing an unlisted one keeps it.
  await select(sel, OTHER);
  assert.equal(got.at(-1), "claude-next");
  await m.unmount();

  // An unlisted value from the start is shown in the field.
  const n = await mount(h(ModelPicker, { id: "x", provider: P.openai, value: "custom-1", disabled: false, onChange() {} }));
  assert.deepEqual([...n.root.querySelectorAll("option")].map((o) => o.value), ["gpt-live", "gpt-x", OTHER]);
  assert.equal(n.root.querySelector("select").value, OTHER);
  assert.equal(n.root.querySelector("input").value, "custom-1");
  await n.unmount();
});

test("ModelPicker: options and the line under it show cost, capability and approximate price", async () => {
  const rich = {
    ...base, id: "rich", name: "Rich", key: "stored",
    models: [
      { id: "big", label: "Big", tier: "frontier", cost: "high", price_in: 4, price_out: 20.5 },
      { id: "small", label: "Small", tier: "basic", cost: "local" },
      { id: "odd", label: "Odd", tier: "weird", cost: "weird" },
      { id: "plain", label: "Plain" },
    ],
  };
  let value = "big";
  const props = () => ({ id: "r", provider: rich, value, disabled: false, onChange: (v) => (value = v) });
  const m = await mount(h(ModelPicker, props()));
  const sel = m.root.querySelector("select#r");
  assert.deepEqual([...sel.options].map((o) => o.textContent), ["Big · $$$ · Frontier", "Small · local · Basic", "Odd", "Plain", "Other…"]);
  assert.equal(text(m.root.querySelector("[data-testid=model-facts]")), "Cost: $$$ · Capability: Frontier · $4 / $20.5 per million tokens, in / out (approximate)");
  await select(sel, "small");
  await m.rerender(h(ModelPicker, props()));
  assert.equal(text(m.root.querySelector("[data-testid=model-facts]")), "Cost: local · Capability: Basic");
  await select(sel, "plain");
  await m.rerender(h(ModelPicker, props()));
  assert.equal(m.root.querySelector("[data-testid=model-facts]"), null);
  await select(sel, "odd");
  await m.rerender(h(ModelPicker, props()));
  assert.equal(m.root.querySelector("[data-testid=model-facts]"), null);
  await m.unmount();
});

// ── ModelSettings ───────────────────────────────────────────────────────────

test("the tab loads, and a refused load says why", async () => {
  let answer;
  const slow = fakeApi({ "GET /v1/llm": () => new Promise((r) => (answer = r)) });
  const s = await mount(h(ModelSettings, { api: slow.api, onNotice() {} }));
  assert.match(text(s.root), /Loading…/);
  answer(json(200, VIEW));
  await settle();
  assert.doesNotMatch(text(s.root), /Loading…/);
  assert.ok(s.root.querySelector("[data-testid=llm-default]"));
  await s.unmount();

  const f = fakeApi({ "GET /v1/llm": json(500, { error: "no llm" }) });
  const m = await mount(h(ModelSettings, { api: f.api, onNotice() {} }));
  assert.equal(text(m.root.querySelector("[role=alert]")), "no llm");
  assert.equal(m.root.querySelector("[data-testid=llm-default]"), null);
  await m.unmount();
});

test("the default model: connected providers, a new pick, and a save", async () => {
  const notices = [];
  const f = fakeApi({
    "GET /v1/llm": json(200, VIEW),
    "PUT /v1/llm/default": (_p, init) => json(200, { ...VIEW, ...JSON.parse(init.body) }),
  });
  const m = await mount(h(ModelSettings, { api: f.api, onNotice: (x) => notices.push(x) }));
  const form = m.root.querySelector("[data-testid=llm-default]");
  const prov = form.querySelector("#llm-default-provider");
  // The provider without a key is not offered.
  assert.deepEqual([...prov.options].map((o) => o.value), ["anthropic", "openai", "ollama"]);
  assert.match(text(form), /Sessions already open keep their own model\./);
  const save = btn(form, "Save");
  assert.equal(save.disabled, true);

  // Another provider starts on the first model its test listed.
  await select(prov, "openai");
  assert.equal(form.querySelector("#llm-default-model").value, "gpt-live");
  assert.equal(save.disabled, false);
  // One without a list: an empty field, and nothing to save until a model is typed.
  await select(prov, "ollama");
  const field = form.querySelector("input#llm-default-model");
  assert.equal(field.value, "");
  assert.equal(btn(form, "Save").disabled, true);
  type(field, "llama3 ");
  await settle();
  // Back to the one in force: its model again.
  await select(prov, "anthropic");
  assert.equal(form.querySelector("#llm-default-model").value, "claude-a");
  assert.equal(btn(form, "Save").disabled, true);
  await select(form.querySelector("#llm-default-model"), "claude-b");
  await press(btn(form, "Save"));
  assert.deepEqual(f.calls.at(-1), { method: "PUT", path: "/v1/llm/default", body: { provider: "anthropic", model: "claude-b" } });
  assert.deepEqual(notices, ["New sessions will start with claude-b"]);
  assert.equal(btn(form, "Save").disabled, true);
  await m.unmount();
});

test("a refused default is shown; a provider that is no longer known is listed and offers nothing", async () => {
  const f = fakeApi({
    "GET /v1/llm": json(200, { ...VIEW, provider: "gone", model: "g1", tasks: undefined, task_kinds: undefined }),
    "PUT /v1/llm/default": json(400, { error: "unknown model" }),
  });
  const m = await mount(h(ModelSettings, { api: f.api, onNotice() {} }));
  const form = m.root.querySelector("[data-testid=llm-default]");
  const prov = form.querySelector("#llm-default-provider");
  // The provider in force stays listed even without its key.
  assert.deepEqual([...prov.options].map((o) => o.value), ["anthropic", "openai", "ollama", "gone"]);
  // A value no option has (the select empties): no provider, no model.
  await select(prov, "nowhere");
  assert.equal(form.querySelector("input#llm-default-model").value, "");
  await select(prov, "anthropic");
  await press(btn(form, "Save"));
  assert.equal(text(form.querySelector("[role=alert]")), "unknown model");
  // A new pick clears the error.
  await select(prov, "openai");
  assert.equal(form.querySelector("[role=alert]"), null);
  // Without kinds from the gateway, the known ones are listed.
  const tasks = m.root.querySelector("[data-testid=llm-tasks]");
  assert.deepEqual([...tasks.querySelectorAll("[data-kind]")].map((r) => r.dataset.kind), ["plan", "code", "review", "research", "summary", "cheap", "strong"]);
  await m.unmount();
});

test("a read-only gateway, and one with no provider connected", async () => {
  const f = fakeApi({
    "GET /v1/llm": json(200, { ...VIEW, writable: false, providers: [P.gone], provider: "gone", model: "g1" }),
  });
  const m = await mount(h(ModelSettings, { api: f.api, onNotice() {} }));
  assert.ok(m.root.querySelector("[data-testid=llm-readonly]"));
  assert.equal(m.root.querySelector("#llm-default-provider").disabled, true);
  const tips = [...m.root.querySelectorAll(".llm-actions p")].map(text);
  assert.deepEqual(tips, ["Connect a provider in Providers first.", "Connect a provider in Providers first."]);
  for (const b of m.root.querySelectorAll("button[type=submit]")) {
    assert.equal(b.disabled, true);
  }
  assert.equal(m.root.querySelector("#llm-task-plan-provider").disabled, true);
  await m.unmount();
});

test("each kind of work can follow the default or get its own model", async () => {
  const notices = [];
  let saving;
  const f = fakeApi({
    "GET /v1/llm": json(200, VIEW),
    "PUT /v1/llm/tasks": () => new Promise((r) => (saving = r)),
  });
  const m = await mount(h(ModelSettings, { api: f.api, onNotice: (x) => notices.push(x) }));
  const form = m.root.querySelector("[data-testid=llm-tasks]");
  const row = (k) => form.querySelector(`[data-kind=${k}]`);
  assert.deepEqual([...form.querySelectorAll("[data-kind]")].map((r) => r.dataset.kind), VIEW.task_kinds);
  assert.match(text(row("plan")), /^PlanningPlan mode: explores and proposes, changes nothing\.Same as default/);
  // A kind the page does not know shows under the gateway's name, with no hint.
  assert.equal(text(row("triage").querySelector("label")), "triage");
  assert.equal(row("triage").querySelector("p"), null);
  assert.match(text(form), /work as a team only inside their own session/);
  const save = btn(form, "Save");
  assert.equal(save.disabled, true);

  // The assigned kind shows its provider and model.
  assert.equal(form.querySelector("#llm-task-code-provider").value, "openai");
  assert.equal(form.querySelector("#llm-task-code-model").value, "gpt-x");
  assert.equal(form.querySelector("#llm-task-code-model").getAttribute("aria-label"), "Model for Coding");
  // Picking its saved provider again brings its saved model back.
  await select(form.querySelector("#llm-task-code-provider"), "anthropic");
  assert.equal(form.querySelector("#llm-task-code-model").value, "claude-a");
  await select(form.querySelector("#llm-task-code-provider"), "openai");
  assert.equal(form.querySelector("#llm-task-code-model").value, "gpt-x");
  assert.equal(save.disabled, true);

  // A new kind: the provider's first listed model. One without a list waits for a typed model.
  await select(form.querySelector("#llm-task-plan-provider"), "openai");
  assert.equal(form.querySelector("#llm-task-plan-model").value, "gpt-live");
  await select(form.querySelector("#llm-task-review-provider"), "ollama");
  assert.equal(form.querySelector("input#llm-task-review-model").value, "");
  assert.equal(btn(form, "Save").disabled, true);
  type(form.querySelector("input#llm-task-review-model"), "llama3");
  await settle();
  assert.equal(btn(form, "Save").disabled, false);
  // Back to the default drops the kind.
  await select(form.querySelector("#llm-task-review-provider"), "");
  assert.equal(form.querySelector("#llm-task-review-model"), null);

  await press(btn(form, "Save"));
  assert.equal(btn(form, "Saving…").disabled, true);
  assert.deepEqual(f.calls.at(-1).body, {
    tasks: { code: { provider: "openai", model: "gpt-x" }, plan: { provider: "openai", model: "gpt-live" } },
  });
  saving(json(200, { ...VIEW, tasks: { code: { provider: "openai", model: "gpt-x" }, plan: { provider: "openai", model: "gpt-live" } } }));
  await settle();
  assert.deepEqual(notices, ["Models per kind of work saved"]);
  assert.equal(btn(form, "Save").disabled, true);
  await m.unmount();
});

test("a kind whose saved model is empty, a provider no option has, and a refused save", async () => {
  const f = fakeApi({
    "GET /v1/llm": json(200, { ...VIEW, tasks: { code: { provider: "openai", model: "" }, plan: { provider: "gone", model: "g1" } } }),
    "PUT /v1/llm/tasks": json(500, { error: "tasks refused" }),
  });
  const m = await mount(h(ModelSettings, { api: f.api, onNotice() {} }));
  const form = m.root.querySelector("[data-testid=llm-tasks]");
  // The saved provider of a kind stays listed even without its key.
  assert.ok([...form.querySelector("#llm-task-plan-provider").options].some((o) => o.value === "gone"));
  // Picking the saved provider of a kind with no saved model: empty.
  await select(form.querySelector("#llm-task-code-provider"), "anthropic");
  await select(form.querySelector("#llm-task-code-provider"), "openai");
  assert.equal(form.querySelector("#llm-task-code-model").value, "");
  assert.equal(form.querySelector("input#llm-task-code-model-other"), null);
  // An incomplete kind cannot be saved.
  assert.equal(btn(form, "Save").disabled, true);
  // A provider value no option names: the kind gets it, with no model to offer.
  const sel = form.querySelector("#llm-task-research-provider");
  const ghost = document.createElement("option");
  ghost.value = "ghost";
  sel.appendChild(ghost);
  await select(sel, "ghost");
  assert.equal(form.querySelector("input#llm-task-research-model").value, "");
  type(form.querySelector("input#llm-task-research-model"), "m1");
  await settle();
  assert.equal(btn(form, "Save").disabled, true);
  await select(form.querySelector("#llm-task-code-model"), "gpt-x");
  await press(btn(form, "Save"));
  assert.equal(text(form.querySelector("[role=alert]")), "tasks refused");
  // A new pick clears it.
  await select(form.querySelector("#llm-task-summary-provider"), "anthropic");
  assert.equal(form.querySelector("[role=alert]"), null);
  await m.unmount();
});
