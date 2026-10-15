// The model switcher's provider picker: one button per provider the session can switch to, with
// its logo, and the model select underneath listing the picked provider's models. The real modal
// is rendered in jsdom (bundled with esbuild, since node cannot import TSX) over a fake store.
// Run with: node --test web/src/__tests__/modelswitcher.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { fileURLToPath, pathToFileURL } from "node:url";
import { dirname, join } from "node:path";
import { mkdtempSync, rmSync } from "node:fs";
import { JSDOM } from "jsdom";
import { build } from "esbuild";

const here = dirname(fileURLToPath(import.meta.url));
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
globalThis.requestAnimationFrame = (cb) => setTimeout(() => cb(Date.now()), 0);
globalThis.cancelAnimationFrame = (id) => clearTimeout(id);

const out = mkdtempSync(join(here, ".ms-"));
const entry = join(out, "entry.ts");
const { writeFileSync } = await import("node:fs");
writeFileSync(
  entry,
  `export { ModelSwitcherModal } from ${JSON.stringify(join(here, "..", "ModelSwitcherModal.tsx"))};
export { AppCtx } from ${JSON.stringify(join(here, "..", "appStore.ts"))};`,
);
await build({
  sourcemap: "inline",
  entryPoints: [entry],
  outfile: join(out, "ms.mjs"),
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
const { ModelSwitcherModal, AppCtx } = await import(
  pathToFileURL(join(out, "ms.mjs")).href
);
const { h, render } = await import("preact");
const { useState } = await import("preact/hooks");
const { act } = await import("preact/test-utils");
rmSync(out, { recursive: true });

const GPT_MINI = "gpt-5-mini";
const GEMINI_PRO = "gemini-2.5-pro";

const PROVIDERS = [
  {
    id: "openai",
    name: "OpenAI",
    models: ["gpt-5", GPT_MINI],
    fetch_models: true,
    key_present: true,
    is_current: true,
  },
  {
    id: "gemini",
    name: "Gemini",
    models: [GEMINI_PRO],
    fetch_models: false,
    key_present: true,
    is_current: false,
  },
  {
    id: "acme",
    name: "Acme",
    models: ["acme-1"],
    fetch_models: false,
    key_present: true,
    is_current: false,
  },
];

// Harness holds the draft the way useModelSwitcher does, so a pick re-renders the modal.
function Harness() {
  const [draftProvider, setDraftProvider] = useState("openai");
  const [draftModel, setDraftModel] = useState(GPT_MINI);
  const [draftReasoning, setDraftReasoning] = useState("medium");
  const model = {
    config: {
      provider: "openai",
      model: GPT_MINI,
      reasoning: "medium",
      api_key_present: true,
    },
    draftProvider,
    setDraftProvider,
    draftModel,
    setDraftModel,
    draftReasoning,
    setDraftReasoning,
    savingConfig: false,
    providers: PROVIDERS,
    modelList: [],
    fetchingModels: false,
    closeModelSwitcher() {},
    discardModelSwitcher() {},
    applyProviderModel() {},
  };
  return h(
    AppCtx.Provider,
    { value: { model, rs: { messages: [], running: false } } },
    h(ModelSwitcherModal, {}),
  );
}

async function mount() {
  const root = document.createElement("div");
  document.body.appendChild(root);
  await act(async () => render(h(Harness, {}), root));
  return root;
}

const choices = () => [
  ...document.querySelectorAll('[role="radiogroup"] .provider-choice'),
];
const modelSelect = () => document.querySelector("#model-switcher-model");

test("each provider is a button with its logo, the saved one checked", async () => {
  const root = await mount();
  const buttons = choices();
  assert.deepEqual(
    buttons.map((b) => b.querySelector(".provider-choice-name").textContent),
    ["OpenAI", "Gemini", "Acme"],
  );
  assert.deepEqual(
    buttons.map((b) => b.getAttribute("aria-checked")),
    ["true", "false", "false"],
  );
  assert.ok(
    buttons[0].querySelector('[data-logo="openai"] svg path'),
    "OpenAI draws its logo",
  );
  assert.equal(
    buttons[2].querySelector("[data-logo]").textContent,
    "A",
    "no logo: the initial",
  );
  assert.match(
    buttons[0].textContent,
    /\(active\)/,
    "the running provider is marked",
  );
  render(null, root);
});

test("picking a provider lists its models in the select below", async () => {
  const root = await mount();
  assert.deepEqual(
    [...modelSelect().options].map((o) => o.value),
    ["gpt-5", GPT_MINI],
  );
  await act(async () => choices()[1].click());
  assert.equal(choices()[1].getAttribute("aria-checked"), "true");
  assert.deepEqual(
    [...modelSelect().options].map((o) => o.value),
    [GEMINI_PRO],
  );
  assert.equal(modelSelect().value, GEMINI_PRO);
  // Back to the saved provider: the saved model comes back too.
  await act(async () => choices()[0].click());
  assert.equal(modelSelect().value, GPT_MINI);
  render(null, root);
});
