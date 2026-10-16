// The browser model on the page's side (browserLLM.ts) and its settings (BrowserModelSettings.tsx):
// turned on, its files followed while the gateway downloads them, its worker loaded, and the
// gateway's requests answered, against a stand-in gateway and a stand-in worker.
import test, { beforeEach } from "node:test";
import assert from "node:assert/strict";
import {
  setupDom,
  source,
  stub,
  settle as settleWith,
  json,
} from "./cov-voice-helpers.mjs";

setupDom("https://localhost/");

// Timers run at once: the poll between two reads of the catalog, and the wait after a failed ask.
const realTimeout = globalThis.setTimeout;
globalThis.setTimeout = (f, _ms, ...a) => realTimeout(f, 0, ...a);

// The gateway's catalog, as each test sets it, and the downloads it was asked for.
const gw = { catalogs: [], installs: [], install: null };
stub(
  "./localVoice",
  `export async function fetchCatalog() {
     const g = globalThis.__covGW;
     const c = g.catalogs.length > 1 ? g.catalogs.shift() : g.catalogs[0];
     if (c instanceof Error) { throw c; }
     return c;
   }
   export async function installVoice(api, id) { globalThis.__covGW.installs.push(id); }`,
);
globalThis.__covGW = gw;

// The worker: what it is sent, and a way to answer as it.
const workers = [];
globalThis.Worker = class {
  constructor(url, opts) {
    this.url = String(url);
    this.opts = opts;
    this.sent = [];
    this.terminated = false;
    workers.push(this);
  }
  postMessage(m) {
    this.sent.push(m);
  }
  terminate() {
    this.terminated = true;
  }
};

const page = await source("browserLLM.ts");
const { BrowserModelSettings } = await source("BrowserModelSettings.tsx");
const { h, render } = await import("preact");
const { act } = await import("preact/test-utils");
const { useBrowserModel, useBrowserModelState } = page;

const settle = (n = 4) => settleWith(act, n);
const wait = async (cond, what) => {
  for (let i = 0; i < 3000; i++) {
    if (cond()) {
      return;
    }
    await new Promise((r) => realTimeout(r, 1));
  }
  throw new Error("never happened: " + what);
};
const tell = (m) => workers.at(-1).onmessage({ data: m });

const card = (features = ["shader-f16"]) => ({ features: new Set(features) });
const withGPU = (adapter) =>
  Object.defineProperty(navigator, "gpu", {
    value: { requestAdapter: async () => adapter },
    configurable: true,
  });
const noGPU = () => delete navigator.gpu;

const model = (id, o = {}) => ({
  id,
  name: id,
  lang: "mul",
  quality: "low",
  engine: "llm",
  size: 1,
  installed: false,
  sample: false,
  ...o,
});
const catalogOf = (models, download = null) => ({
  runtime: { size: 1, installed: false },
  models,
  download,
});

// The gateway's answers to the tab: a queue of what /v1/browser-llm/next says, and what it was told.
const tab = { next: [], told: [], calls: [] };
const api = async (path, init) => {
  tab.calls.push(path);
  if (path === "/v1/browser-llm/next") {
    // The gateway holds the request open for a while: the page's own turns run meanwhile.
    await new Promise((r) => realTimeout(r, 2));
    const step = tab.next.length > 1 ? tab.next.shift() : tab.next[0];
    if (step === "down") {
      throw new Error("offline");
    }
    if (step === "stop") {
      page.stopBrowserModel();
      return new Response(null, { status: 204 });
    }
    return step === 204 || step === 500
      ? new Response(null, { status: step })
      : json(step);
  }
  if (path.startsWith("/v1/browser-llm/jobs/")) {
    tab.told.push([path, JSON.parse(init.body)]);
    return new Response(null, { status: 204 });
  }
  if (path === "/v1/browser-llm/test") {
    return tab.test();
  }
  throw new Error("unexpected " + path);
};

beforeEach(() => {
  page.stopBrowserModel();
  workers.length = 0;
  gw.catalogs = [catalogOf([model("llm-qwen25-0.5b", { installed: true })])];
  gw.installs = [];
  tab.next = ["stop"];
  tab.told = [];
  tab.calls = [];
  localStorage.clear();
  withGPU(card());
});

test("it is off until turned on, and the choice is kept in this browser", () => {
  assert.equal(page.browserModelOn(), false);
  page.setBrowserModelOn(true);
  assert.equal(page.browserModelOn(), true);
  page.setBrowserModelOn(false);
  assert.equal(page.browserModelOn(), false);
  assert.equal(page.browserState().phase, "off");
});

test("a browser that cannot keep its choices forgets it", () => {
  const real = Object.getOwnPropertyDescriptor(globalThis, "localStorage");
  Object.defineProperty(globalThis, "localStorage", {
    get() {
      throw new Error("blocked");
    },
    configurable: true,
  });
  assert.equal(page.browserModelOn(), false);
  page.setBrowserModelOn(true);
  Object.defineProperty(globalThis, "localStorage", real);
});

test("a browser without WebGPU is told so, and nothing is fetched", async () => {
  noGPU();
  assert.equal(page.browserGPU(), false);
  page.startBrowserModel(api);
  assert.equal(page.browserState().phase, "unsupported");
  assert.equal(tab.calls.length, 0);
  assert.equal(workers.length, 0);
});

test("with the files there it loads in a worker and answers the gateway's requests", async () => {
  tab.next = [
    { id: "7", messages: [{ role: "user", content: "title?" }] },
    204,
    500,
    "down",
    { id: "8", messages: [{ role: "user", content: "boom" }] },
    "stop",
  ];
  page.startBrowserModel(api);
  page.startBrowserModel(api); // asked again while it runs: nothing changes
  await wait(() => workers.length === 1, "a worker");
  assert.equal(workers[0].opts.type, "module");
  assert.equal(page.browserState().build, "llm-qwen25-0.5b");
  assert.deepEqual(workers[0].sent, [{ kind: "load" }]);
  assert.equal(page.browserState().phase, "loading");
  tell({ kind: "progress", pct: 40 });
  assert.equal(page.browserState().pct, 40);
  tell({ kind: "ready", build: "llm-qwen25-0.5b" });

  await wait(
    () => workers[0].sent.some((m) => m.kind === "generate"),
    "the first job",
  );
  const first = workers[0].sent.find((m) => m.kind === "generate");
  assert.deepEqual(first.messages, [{ role: "user", content: "title?" }]);
  assert.equal(page.browserState().phase, "ready");
  tell({ seq: first.seq, text: "A title" });
  await wait(() => tab.told.length === 1, "the answer");
  assert.deepEqual(tab.told[0], [
    "/v1/browser-llm/jobs/7",
    { text: "A title" },
  ]);

  // A job the model cannot run is answered with why; then the tab is stopped from the gateway's side.
  await wait(
    () => workers[0].sent.filter((m) => m.kind === "generate").length === 2,
    "the second job",
  );
  tell({ seq: 2, error: "out of memory" });
  await wait(() => tab.told.length === 2, "the failure");
  assert.deepEqual(tab.told[1], [
    "/v1/browser-llm/jobs/8",
    { error: "out of memory" },
  ]);
  await wait(() => page.browserState().phase === "off", "stopped");
  assert.equal(workers[0].terminated, true);
});

test("a build the card cannot run in 16 bits is the larger one", async () => {
  withGPU(card([]));
  gw.catalogs = [
    catalogOf([model("llm-qwen25-0.5b-f32", { installed: true })]),
  ];
  page.startBrowserModel(api);
  await wait(() => workers.length === 1, "a worker");
  assert.equal(page.browserState().build, "llm-qwen25-0.5b-f32");
  withGPU(null); // no card handed out: the lighter build is not the one
  page.stopBrowserModel();
});

test("the gateway downloads the files first, and the page follows", async () => {
  gw.catalogs = [
    catalogOf([model("llm-qwen25-0.5b")]),
    catalogOf([model("llm-qwen25-0.5b")], {
      id: "llm-qwen25-0.5b",
      done: 0,
      total: 0,
    }),
    catalogOf([model("llm-qwen25-0.5b")], {
      id: "llm-qwen25-0.5b",
      done: 25,
      total: 100,
    }),
    catalogOf([model("llm-qwen25-0.5b", { installed: true })]),
  ];
  page.startBrowserModel(api);
  await wait(() => workers.length === 1, "a worker");
  assert.deepEqual(gw.installs, ["llm-qwen25-0.5b"], "asked for once");
  page.stopBrowserModel();
});

test("what stops it is said: a gateway without the model, a failed download, a worker that fails", async () => {
  gw.catalogs = [catalogOf([])];
  page.startBrowserModel(api);
  await wait(() => page.browserState().phase === "failed", "no model");
  assert.match(page.browserState().error, /does not offer/);
  assert.equal(workers.length, 0);

  gw.catalogs = [
    catalogOf([model("llm-qwen25-0.5b")], {
      id: "llm-qwen25-0.5b",
      done: 1,
      total: 2,
      error: "disk full",
    }),
  ];
  page.startBrowserModel(api);
  await wait(
    () => page.browserState().error === "disk full",
    "the download's failure",
  );

  gw.catalogs = [new Error("gateway down")];
  page.startBrowserModel(api);
  await wait(
    () => page.browserState().error === "gateway down",
    "the catalog's failure",
  );

  gw.catalogs = [catalogOf([model("llm-qwen25-0.5b", { installed: true })])];
  page.startBrowserModel(api);
  await wait(() => workers.length === 1, "a worker");
  tell({ kind: "failed", error: "no adapter" });
  await wait(
    () => page.browserState().error === "no adapter",
    "the worker's failure",
  );
  assert.equal(page.browserState().phase, "failed");
  assert.equal(
    workers.at(-1).terminated,
    true,
    "a failed load leaves no worker",
  );

  // A failure that is not an Error is still told.
  gw.catalogs = [
    {
      get models() {
        throw "plain";
      },
    },
  ];
  page.startBrowserModel(api);
  await wait(() => page.browserState().error === "plain", "a plain failure");
});

test("a worker that dies is a failure, before it is ready and while a job runs", async () => {
  page.startBrowserModel(api);
  await wait(() => workers.length === 1, "a worker");
  workers[0].onerror({ message: "script failed to load" });
  await wait(() => page.browserState().phase === "failed", "the early crash");
  assert.equal(page.browserState().error, "script failed to load");
  assert.equal(workers[0].terminated, true);

  // Dead while a job runs: the job is told as failed, not left to the gateway's timeout.
  tab.next = [{ id: "9", messages: [{ role: "user", content: "x" }] }, 204];
  page.startBrowserModel(api);
  await wait(() => workers.length === 2, "a second worker");
  tell({ kind: "ready", build: "llm-qwen25-0.5b" });
  await wait(
    () => workers[1].sent.some((m) => m.kind === "generate"),
    "the job",
  );
  workers[1].onerror({ message: "" });
  await wait(() => page.browserState().phase === "failed", "the late crash");
  assert.match(page.browserState().error, /worker stopped/);
  await wait(() => tab.told.length === 1, "the failed job told");
  assert.equal(tab.told[0][0], "/v1/browser-llm/jobs/9");
  assert.ok(tab.told[0][1].error);
});

test("turning it off with a job running tells the gateway the job failed", async () => {
  tab.next = [{ id: "5", messages: [{ role: "user", content: "x" }] }, 204];
  page.startBrowserModel(api);
  await wait(() => workers.length === 1, "a worker");
  tell({ kind: "ready", build: "llm-qwen25-0.5b" });
  await wait(
    () => workers[0].sent.some((m) => m.kind === "generate"),
    "the job",
  );
  page.stopBrowserModel();
  await wait(() => tab.told.length === 1, "the failed job told");
  assert.equal(tab.told[0][0], "/v1/browser-llm/jobs/5");
  assert.equal(page.browserState().phase, "off");
});

test("a run stopped while loading does not paint over the state afterwards", async () => {
  page.startBrowserModel(api);
  await wait(() => workers.length === 1, "a worker");
  page.stopBrowserModel();
  tell({ kind: "progress", pct: 70 });
  assert.equal(page.browserState().phase, "off");
  assert.equal(page.browserState().pct, 0);
});

test("stopping while the files download ends it quietly", async () => {
  gw.catalogs = [catalogOf([model("llm-qwen25-0.5b")])];
  page.startBrowserModel(api);
  await wait(() => gw.installs.length === 1, "the download asked for");
  page.stopBrowserModel();
  page.stopBrowserModel(); // nothing runs now
  await settle();
  assert.equal(page.browserState().phase, "off");
  assert.equal(workers.length, 0);

  // Stopped as the files arrive: the worker is never made.
  let release;
  gw.catalogs = [catalogOf([model("llm-qwen25-0.5b", { installed: true })])];
  withGPU({
    get features() {
      return new Set(["shader-f16"]);
    },
  });
  page.startBrowserModel(api);
  page.stopBrowserModel();
  await settle();
  assert.equal(workers.length, 0);
  release?.();
});

test("it starts by itself in a signed-in tab where it was turned on", async () => {
  const root = document.createElement("div");
  document.body.appendChild(root);
  const Probe = ({ authed }) => {
    useBrowserModel(api, authed);
    return h("span", { "data-phase": useBrowserModelState().phase });
  };
  await act(() => render(h(Probe, { authed: false }), root));
  assert.equal(workers.length, 0, "signed out: nothing starts");
  await act(() => render(h(Probe, { authed: true }), root));
  assert.equal(
    workers.length,
    0,
    "signed in but never turned on: nothing starts",
  );
  page.setBrowserModelOn(true);
  await act(() => render(h(Probe, { authed: false }), root));
  await act(() => render(h(Probe, { authed: true }), root));
  await wait(() => workers.length === 1, "a worker");
  await settle();
  assert.equal(root.querySelector("span").dataset.phase, "loading");
  page.stopBrowserModel();
  await settle();
  assert.equal(root.querySelector("span").dataset.phase, "off");
  await act(() => render(null, root));
  root.remove();
});

const text = (el) => (el?.textContent ?? "").replace(/\s+/g, " ").trim();
async function mount() {
  const root = document.createElement("div");
  document.body.appendChild(root);
  await act(() => render(h(BrowserModelSettings, { api }), root));
  await settle();
  return {
    root,
    box: () => root.querySelector("[data-testid=llm-browser]"),
    status: () => text(root.querySelector("[data-testid=llm-browser-status]")),
    done: async () => {
      page.stopBrowserModel();
      await act(() => render(null, root));
      root.remove();
    },
  };
}
const tryButton = (m) =>
  m.root
    .querySelector("#llm-browser-try")
    .closest(".settings-row")
    .querySelector("button");
const click = async (el) => {
  await act(() => el.click());
  await settle();
};

test("the settings turn it on, follow it, let it be tried, and turn it off", async (ctx) => {
  tab.next = [204]; // nothing to do for the model: it only waits
  const m = await mount();
  ctx.after(m.done);
  const sw = m.root.querySelector("input[role=switch]");
  assert.equal(sw.checked, false);
  assert.equal(sw.disabled, false);
  assert.equal(m.status(), "");
  assert.match(
    text(m.box()),
    /Qwen2\.5 0\.5B, free\. Downloads about 517 MB once/,
  );
  assert.equal(
    m.root.querySelector("#llm-browser-try"),
    null,
    "nothing to try before it is ready",
  );

  gw.catalogs = [
    catalogOf([model("llm-qwen25-0.5b")]),
    catalogOf([model("llm-qwen25-0.5b")], {
      id: "llm-qwen25-0.5b",
      done: 50,
      total: 100,
    }),
  ];
  await click(sw);
  assert.equal(page.browserModelOn(), true);
  await wait(() => /downloading the model/.test(m.status()), "downloading");
  gw.catalogs = [catalogOf([model("llm-qwen25-0.5b", { installed: true })])];
  await wait(() => workers.length === 1, "a worker");
  await settle();
  assert.match(m.status(), /Loading the model into the graphics card/);
  await act(() => tell({ kind: "ready", build: "llm-qwen25-0.5b" }));
  await wait(() => page.browserState().phase === "ready", "ready");
  await settle();
  assert.equal(m.status(), "Ready: it answers while this tab stays open.");

  // Try it: the title and how long it took, or why not.
  tab.test = async () => json({ text: "Fix the test", ms: 4200 });
  await click(tryButton(m));
  assert.match(text(m.box()), /“Fix the test” in 4\.2 s/);
  tab.test = async () => json({ error: "no tab" }, 502);
  await click(tryButton(m));
  assert.match(text(m.box()), /no tab/);
  tab.test = async () => json({}, 500);
  await click(tryButton(m));
  assert.match(text(m.box()), /HTTP 500/);
  tab.test = async () =>
    new Response("<html>Bad Gateway</html>", { status: 502 });
  await click(tryButton(m));
  assert.match(text(m.box()), /HTTP 502/);
  tab.test = async () => json({});
  await click(tryButton(m));
  assert.match(text(m.box()), /“” in 0\.0 s/);

  await click(sw);
  assert.equal(page.browserModelOn(), false);
  assert.equal(m.status(), "");
  await m.done();
});

test("a failure and a device that cannot run it are said under the switch", async () => {
  let m = await mount();
  gw.catalogs = [catalogOf([])];
  await click(m.root.querySelector("input[role=switch]"));
  await wait(() => /could not start/.test(m.status()), "the failure");
  assert.match(m.status(), /does not offer the browser model/);
  await m.done();

  noGPU();
  m = await mount();
  const sw = m.root.querySelector("input[role=switch]");
  assert.equal(sw.disabled, true, "no WebGPU: it cannot be turned on");
  assert.match(
    m.status(),
    /needs WebGPU, and a phone is left out/,
    "and it says why before anything is tried",
  );
  await m.done();
});
