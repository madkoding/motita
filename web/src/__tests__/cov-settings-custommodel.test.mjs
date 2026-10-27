// The custom model form of the models tab, and the placeholders of the custom provider.
// Run with: node --test web/src/__tests__/cov-settings-custommodel.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import {
  installDom,
  load,
  fakeApi,
  json,
  settle,
  click,
  type,
  byText,
} from "./cov-settings-helpers.mjs";

installDom();
const { ModelSettings } = await load("ModelSettings.tsx");
const { ProviderSettings } = await load("ProviderSettings.tsx");
const api = await load("llmApi.ts");
const { h, render } = await import("preact");

const custom = {
  id: "custom",
  name: "Custom (OpenAI-compatible)",
  access: "Your own endpoint: URL and token",
  key: "missing",
  ask_base_url: true,
  models: [],
  default: false,
};
const VIEW = {
  provider: "custom",
  model: "mine",
  writable: true,
  providers: [{ ...custom, key: "stored" }],
  task_kinds: [],
  thinking_levels: ["off"],
};

async function mount(vnode) {
  const root = document.createElement("div");
  document.body.appendChild(root);
  render(vnode, root);
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
const field = (root, key) => root.querySelector(`#llm-custom-${key}`);

test("the custom model form shows examples, saves what was typed and reloads it", async () => {
  const notices = [];
  const f = fakeApi({
    "GET /v1/llm": json(200, VIEW),
    "GET /v1/evals": json(200, {}),
    "PUT /v1/llm/custom-model": (_p, init) =>
      json(200, { ...VIEW, custom_model: JSON.parse(init.body) }),
  });
  const m = await mount(
    h(ModelSettings, { api: f.api, onNotice: (x) => notices.push(x) }),
  );
  const form = m.root.querySelector("[data-testid=llm-custom]");
  assert.ok(form);
  assert.equal(field(form, "model").placeholder, "e.g. llama-3.3-70b-instruct");
  assert.equal(field(form, "temperature").placeholder, "e.g. 0.2");
  assert.equal(field(form, "max_tokens").placeholder, "e.g. 8192");
  assert.equal(field(form, "context_window").placeholder, "e.g. 128000");
  const save = byText(form, "button", "Save");
  assert.equal(save.disabled, true);

  type(field(form, "model"), " llama-3.3-70b ");
  type(field(form, "temperature"), "0.3");
  type(field(form, "max_tokens"), "8192");
  type(field(form, "context_window"), "128000");
  await settle();
  assert.equal(save.disabled, false);
  click(save);
  await settle();
  const put = f.calls.find((c) => c.method === "PUT");
  assert.deepEqual(put.body, {
    model: "llama-3.3-70b",
    temperature: 0.3,
    max_tokens: 8192,
    context_window: 128000,
  });
  assert.deepEqual(notices, ["Custom model saved"]);
  // The saved model is what the form holds now: nothing left to save.
  assert.equal(field(form, "model").value, "llama-3.3-70b");
  assert.equal(field(form, "temperature").value, "0.3");
  assert.equal(byText(form, "button", "Save").disabled, true);
  await m.unmount();
});

test("the custom model form refuses numbers it cannot send, and shows a refused save", async () => {
  const f = fakeApi({
    "GET /v1/llm": json(200, VIEW),
    "GET /v1/evals": json(200, {}),
    "PUT /v1/llm/custom-model": json(400, { error: "no good" }),
  });
  const m = await mount(h(ModelSettings, { api: f.api, onNotice() {} }));
  const form = m.root.querySelector("[data-testid=llm-custom]");
  const save = () => byText(form, "button", "Save");
  type(field(form, "model"), "mine");
  type(field(form, "temperature"), "3");
  await settle();
  assert.equal(save().disabled, true);
  assert.match(form.textContent, /temperature is a number from 0 to 2/);
  assert.equal(field(form, "temperature").getAttribute("aria-invalid"), "true");
  type(field(form, "temperature"), "");
  type(field(form, "max_tokens"), "1.5");
  await settle();
  assert.match(form.textContent, /whole numbers above zero/);
  assert.equal(save().disabled, true);
  type(field(form, "max_tokens"), "-4");
  await settle();
  assert.equal(save().disabled, true);
  type(field(form, "max_tokens"), "");
  type(field(form, "context_window"), "abc");
  await settle();
  assert.equal(save().disabled, true);
  type(field(form, "context_window"), "");
  await settle();
  assert.equal(save().disabled, false);
  click(save());
  await settle();
  assert.match(form.textContent, /no good/);
  await m.unmount();
});

test("a gateway that cannot save locks the custom model form", async () => {
  const f = fakeApi({
    "GET /v1/llm": json(200, {
      ...VIEW,
      writable: false,
      custom_model: { model: "mine", max_tokens: 0, context_window: 0 },
    }),
    "GET /v1/evals": json(200, {}),
  });
  const m = await mount(h(ModelSettings, { api: f.api, onNotice() {} }));
  const form = m.root.querySelector("[data-testid=llm-custom]");
  assert.equal(field(form, "model").disabled, true);
  assert.equal(field(form, "model").value, "mine");
  assert.equal(byText(form, "button", "Save").disabled, true);
  await m.unmount();
});

test("the custom provider's endpoint and token show examples", async () => {
  const f = fakeApi({
    "GET /v1/llm": json(200, { ...VIEW, providers: [custom] }),
  });
  const m = await mount(
    h(ProviderSettings, { api: f.api, onNotice() {}, writable: true }),
  );
  click(m.root.querySelector("[data-provider=custom] button[aria-expanded]"));
  await settle();
  const form = m.root.querySelector("[data-testid=llm-form]");
  assert.equal(
    form.querySelector("input[type=url]").placeholder,
    "https://llm.example.com/v1",
  );
  assert.equal(
    form.querySelector("input[type=password]").placeholder,
    "Paste the token, e.g. sk-proj-… or your server's token",
  );
  await m.unmount();
});

test("the custom form helpers", () => {
  assert.deepEqual(api.customDraft(undefined), {
    model: "",
    temperature: "",
    max_tokens: "",
    context_window: "",
  });
  const saved = {
    model: "m",
    temperature: 0,
    max_tokens: 5,
    context_window: 9,
  };
  assert.deepEqual(api.customDraft(saved), {
    model: "m",
    temperature: "0",
    max_tokens: "5",
    context_window: "9",
  });
  assert.equal(api.sameCustom(api.customDraft(saved), saved), true);
  assert.equal(
    api.sameCustom({ ...api.customDraft(saved), model: " m " }, saved),
    true,
  );
  assert.equal(
    api.sameCustom({ ...api.customDraft(saved), temperature: "1" }, saved),
    false,
  );
  assert.deepEqual(api.customBody(api.customDraft(undefined)), {
    model: "",
    temperature: undefined,
    max_tokens: 0,
    context_window: 0,
  });
  assert.equal(
    api.customProblem({ ...api.customDraft(saved), temperature: "2" }),
    "",
  );
  assert.equal(
    api.customProblem({ ...api.customDraft(saved), temperature: "-1" }),
    "temperature",
  );
});
