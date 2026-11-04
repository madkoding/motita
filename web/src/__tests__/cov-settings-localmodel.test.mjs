// Motita's own local model in Settings → Providers: the GPU line, why it cannot run, the download
// with its progress, start, stop and delete, and the client behind them.
// Run with: node --test web/src/__tests__/cov-settings-localmodel.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import {
  installDom,
  load,
  fakeApi,
  json,
  settle,
  click,
  byText,
} from "./cov-settings-helpers.mjs";

installDom();
const { LocalModelSettings } = await load("LocalModelSettings.tsx");
const { ProviderSettings } = await load("ProviderSettings.tsx");
const lm = await load("localModelApi.ts");
const { h, render } = await import("preact");

const BASE = {
  model_id: "qwen3-8b-gsq-q2_k",
  model_name: "Qwen3-8B GSQ (Q2_K, local)",
  repo: "ISTA-DASLab/Qwen3-8B-GGUF-GSQ",
  file: "qwen3-8b-Q2_K.gguf",
  size: 3281732960,
  dir: "/home/me/.motita/local",
  gpu: {
    name: "RTX 3060",
    vendor: "nvidia",
    total_mb: 12288,
    source: "nvidia-smi",
  },
  min_vram_mb: 3800,
  eligible: true,
  model_present: false,
  runtime_present: false,
  task: { running: false },
  server: { running: false },
};
const status = (over = {}) => ({ ...BASE, ...over });

async function mount(api, extra = {}) {
  const root = document.createElement("div");
  document.body.appendChild(root);
  const changed = [];
  const notices = [];
  render(
    h(LocalModelSettings, {
      api,
      writable: true,
      onChanged: () => changed.push(1),
      onNotice: (m) => notices.push(m),
      ...extra,
    }),
    root,
  );
  await settle();
  return {
    root,
    changed,
    notices,
    async unmount() {
      render(null, root);
      root.remove();
      await settle();
    },
  };
}

test("a machine without a GPU sees the reason and a disabled download", async () => {
  const f = fakeApi({
    "GET /v1/local-model": json(
      200,
      status({ gpu: undefined, eligible: false, reason: "no_gpu" }),
    ),
  });
  const m = await mount(f.api);
  assert.match(m.root.textContent, /No GPU found/);
  assert.match(m.root.textContent, /needs at least 4 GB of GPU memory/);
  assert.equal(byText(m.root, "button", "Download (3.3 GB)").disabled, true);
  assert.equal(byText(m.root, "button", "Delete"), undefined);
  await m.unmount();
});

test("a small GPU says how much it has", async () => {
  const f = fakeApi({
    "GET /v1/local-model": json(
      200,
      status({
        gpu: { ...BASE.gpu, total_mb: 2048 },
        eligible: false,
        reason: "low_vram",
      }),
    ),
  });
  const m = await mount(f.api);
  assert.match(
    m.root.querySelector("[data-testid=llm-local-reason]").textContent,
    /2\.0 GB of memory/,
  );
  await m.unmount();
});

test("download shows its progress, can be cancelled, and reads again while it runs", async () => {
  let reads = 0;
  const f = fakeApi({
    "GET /v1/local-model": () => {
      reads++;
      return json(
        200,
        status({
          task: { running: true, phase: "model", done: 50, total: 200 },
        }),
      );
    },
    "POST /v1/local-model/install": json(
      202,
      status({ task: { running: true, phase: "runtime" } }),
    ),
    "POST /v1/local-model/cancel": json(
      200,
      status({ task: { running: false, error: "cancelled" } }),
    ),
  });
  const m = await mount(f.api);
  assert.match(m.root.textContent, /Downloading the model… 25%/);
  await new Promise((r) => setTimeout(r, 1100));
  await settle();
  assert.ok(reads >= 2, "the status is read again while busy");
  click(byText(m.root, "button", "Cancel"));
  await settle();
  assert.match(m.root.textContent, /cancelled/);
  click(byText(m.root, "button", "Download"));
  await settle();
  assert.match(
    m.root.textContent,
    /Downloading the runtime \(llama\.cpp\)… 0%/,
  );
  await m.unmount();
});

test("an installed model starts, runs, stops and is deleted after a confirmation", async () => {
  const installed = {
    model_present: true,
    runtime_present: true,
    runtime_source: "installed",
  };
  const f = fakeApi({
    "GET /v1/local-model": json(
      200,
      status({ ...installed, server: { running: false, error: "stopped" } }),
    ),
    "POST /v1/local-model/start": json(
      202,
      status({ ...installed, server: { running: false, starting: true } }),
    ),
    "POST /v1/local-model/stop": json(
      200,
      status({
        ...installed,
        server: { running: true, port: 8471, context: 16384 },
      }),
    ),
    "DELETE /v1/local-model": json(200, status()),
  });
  const m = await mount(f.api);
  assert.match(m.root.textContent, /Installed · stopped/);
  assert.equal(
    m.root.querySelector("pre"),
    null,
    "a stop the user asked for is no error",
  );
  click(byText(m.root, "button", "Start"));
  await settle();
  assert.match(m.root.textContent, /Loading the model into the GPU/);
  // The stop route answers "running" here so the running line can be seen.
  click(byText(m.root, "button", "Stop"));
  await settle();
  assert.match(
    m.root.textContent,
    /Running · 16384 tokens of context · port 8471/,
  );
  assert.equal(
    m.changed.length,
    1,
    "the provider list reads again once the server runs",
  );
  click(byText(m.root, "button", "Delete"));
  await settle();
  click(byText(m.root, "button", "Delete 3.3 GB from this computer"));
  await settle();
  assert.deepEqual(m.notices, ["Local model deleted"]);
  assert.equal(m.changed.length, 2);
  assert.match(m.root.textContent, /Download \(3\.3 GB\)/);
  await m.unmount();
});

test("a crashed server shows its last words, and refusals are shown", async () => {
  const f = fakeApi({
    "GET /v1/local-model": json(
      200,
      status({
        model_present: true,
        runtime_present: true,
        runtime_source: "path",
        server: { running: false, error: "out of memory" },
      }),
    ),
    "POST /v1/local-model/start": json(409, { error: "busy now" }),
  });
  const m = await mount(f.api);
  assert.match(m.root.querySelector("pre").textContent, /out of memory/);
  click(byText(m.root, "button", "Start"));
  await settle();
  assert.match(m.root.textContent, /busy now/);
  await m.unmount();
});

test("a status that cannot be read is shown", async () => {
  const f = fakeApi({
    "GET /v1/local-model": new Response("nope", { status: 500 }),
  });
  const m = await mount(f.api);
  assert.match(m.root.querySelector("[role=alert]").textContent, /HTTP 500/);
  await m.unmount();
});

test("the providers tab shows the local model only when the gateway offers it", async () => {
  const local = {
    id: "local",
    name: "Motita local",
    access: "x",
    key: "missing",
    models: [],
    default: false,
  };
  const other = {
    id: "openai",
    name: "OpenAI",
    access: "y",
    key: "missing",
    models: [],
    default: false,
  };
  const f = fakeApi({
    "GET /v1/llm": json(200, {
      provider: "openai",
      model: "m",
      writable: true,
      providers: [other, local],
      task_kinds: [],
      thinking_levels: [],
    }),
    "GET /v1/local-model": json(200, status()),
  });
  const root = document.createElement("div");
  document.body.appendChild(root);
  render(h(ProviderSettings, { api: f.api, onNotice() {} }), root);
  await settle(10);
  assert.ok(root.querySelector("[data-testid=llm-local]"));
  assert.equal(
    root.querySelector("[data-provider=local]"),
    null,
    "no key row for it",
  );
  assert.ok(root.querySelector("[data-provider=openai]"));
  render(null, root);
  root.remove();
});

test("the client's helpers", () => {
  assert.equal(lm.percent({ running: true }), 0);
  assert.equal(lm.percent({ running: true, total: 10 }), 0);
  assert.equal(lm.percent({ running: true, done: 20, total: 10 }), 100);
  assert.equal(lm.serverProblem({ running: false }), "");
  assert.equal(lm.removable(status()), false);
  assert.equal(lm.removable(status({ runtime_source: "installed" })), true);
  assert.equal(
    lm.busy(status({ server: { running: false, starting: true } })),
    true,
  );
  assert.equal(lm.busy(status()), false);
  assert.equal(lm.vram(4096), "4.0 GB");
});
