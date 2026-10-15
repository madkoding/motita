// Who listens, in the voice settings (VoiceSettings.tsx), and the models to listen with in this
// browser (LocalListenSettings.tsx): downloaded, followed, loaded into memory, chosen and deleted,
// against a stand-in gateway and a stand-in worker.
import test, { beforeEach } from "node:test";
import assert from "node:assert/strict";
import { setupDom, source, settle as settleWith, json } from "./cov-voice-helpers.mjs";

setupDom("https://localhost/");
// The listening worker: what it is sent, and a way to answer as it.
const workers = [];
globalThis.Worker = class {
  constructor() {
    this.sent = [];
    workers.push(this);
  }
  postMessage(m) {
    this.sent.push(m);
  }
};
globalThis.caches = {};
// The microphone is allowed: the settings are unlocked.
Object.defineProperty(navigator, "permissions", {
  value: { query: async () => ({ state: "granted", addEventListener() {}, removeEventListener() {} }) },
  configurable: true,
});
Object.defineProperty(navigator, "mediaDevices", {
  value: { enumerateDevices: async () => [], addEventListener() {}, removeEventListener() {} },
  configurable: true,
});

const { VoiceSettings } = await source("VoiceSettings.tsx");
const { DOWNLOAD_POLL_MS } = await source("LocalVoiceSettings.tsx");
const listenSettings = await source("LocalListenSettings.tsx");
const { h, render } = await import("preact");
const { act } = await import("preact/test-utils");

const settle = () => settleWith(act, 4);
const text = (el) => (el?.textContent ?? "").replace(/\s+/g, " ").trim();
const tell = async (m) => {
  await act(() => workers.at(-1).onmessage({ data: m }));
  await settle();
};

const model = (id, o = {}) => ({
  id,
  name: "Whisper " + id.split("-")[1],
  lang: "mul",
  quality: { tiny: "low", base: "medium", small: "high" }[id.split("-")[1]],
  engine: "whisper",
  size: 80e6,
  installed: false,
  sample: false,
  ...o,
});
const MODELS = ["whisper-tiny", "whisper-base", "whisper-small", "whisper-base-gpu", "whisper-small-gpu"];

// The gateway: the catalog as each test sets it, the requests it got, and a refusal on demand.
const gw = { catalog: null, calls: [], refuse: "", down: "" };
beforeEach(() => {
  gw.catalog = {
    runtime: { size: 33e6, installed: false },
    models: [
      { id: "es_MX-x", name: "X", lang: "es-MX", quality: "high", engine: "piper", size: 1, installed: true, sample: false },
      ...MODELS.map((id) => model(id)),
    ],
    download: null,
  };
  gw.calls = [];
  gw.refuse = "";
  gw.down = "";
});
const api = async (path, init) => {
  const method = init?.method ?? "GET";
  gw.calls.push(method + " " + path);
  if (gw.down) {
    return json({ error: gw.down }, 503);
  }
  if (method === "GET") {
    return json(gw.catalog);
  }
  if (gw.refuse) {
    return json({ error: gw.refuse }, 409);
  }
  return new Response(null, { status: method === "POST" ? 202 : 204 });
};

// mount draws the voice settings with prefs it keeps, as the app does.
async function mount(prefs = {}, info = null) {
  const box = { prefs: { mic: true, listen: false, deviceId: "", engine: "", listener: "", language: "", ...prefs } };
  const root = document.createElement("div");
  document.body.appendChild(root);
  const draw = () =>
    act(() =>
      render(
        h(VoiceSettings, {
          state: "ready",
          hands: "ready",
          phrases: ["oye motita"],
          prefs: box.prefs,
          setPrefs: (p) => {
            box.prefs = p;
            queueMicrotask(() => void draw());
          },
          info,
          api,
          onInfo() {},
        }),
        root,
      ),
    );
  await draw();
  await settle();
  return {
    box,
    root,
    tid: (id) => root.querySelector(`[data-testid=${id}]`),
    done: async () => {
      await act(() => render(null, root));
      root.remove();
    },
  };
}
const click = async (el) => {
  await act(() => el.click());
  await settle();
};
const pick = async (select, value) => {
  select.value = value;
  await act(() => select.dispatchEvent(new Event("change", { bubbles: true })));
  await settle();
};

test("the browser listens by default; the service is the other choice, with its own hints", async () => {
  const info = { enabled: false, wake_phrases: ["oye motita"], max_audio_bytes: 1, can_save: true,
    saved: { enabled: false, base_url: "", endpoint: "", inherits: false, provider: "openai", model: "whisper-1",
      has_key: false, wake_phrases: ["oye motita"] } };
  const m = await mount({}, info);
  assert.equal(m.tid("voice-engine").value, "local");
  assert.match(text(m.root), /A model downloaded once, run in this browser\./);
  assert.match(text(m.root), /What you say is heard in this browser\. Say: “oye motita”/);
  assert.equal(m.tid("voice-gateway-switch"), null, "the service's switch is not shown");
  assert.ok(m.tid("voice-phrases"), "the wake phrases are, whoever listens");
  await pick(m.tid("voice-engine"), "service");
  assert.equal(m.box.prefs.engine, "service");
  assert.ok(m.tid("voice-gateway-switch"));
  assert.equal(m.tid("local-listeners"), null);
  assert.match(text(m.root), /A transcription service, through the gateway\./);
  assert.match(text(m.root), /What you say is sent to the transcription service/);
  await m.done();
});

test("a model is downloaded with its progress, chosen, loaded into memory, and deleted", async () => {
  const m = await mount();
  const list = () => text(m.tid("local-listeners"));
  assert.match(list(), /The first download adds 33 MB for the engine\./);
  assert.doesNotMatch(list(), /X/, "the voices are not listed");
  assert.doesNotMatch(list(), /WebGPU/, "without WebGPU the models for the card are not offered");
  assert.match(text(m.tid("local-listener-whisper-base")), /Whisper base ?Processor · Medium quality · 80 MB · Recommended/);
  assert.match(text(m.root), /Language you speak/);

  // Downloading: its progress, and no second download meanwhile.
  await click(m.tid("local-listener-get-whisper-base"));
  assert.ok(gw.calls.includes("POST /v1/voice/models/whisper-base"));
  gw.catalog.download = { id: "whisper-base", done: 40e6, total: 80e6 };
  await click(m.tid("local-listener-get-whisper-tiny"));
  assert.match(text(m.tid("local-listener-progress-whisper-base")), /Downloading… 50%/);
  assert.equal(m.tid("local-listener-progress-whisper-base").querySelector("progress").value, 50);
  assert.equal(m.tid("local-listener-get-whisper-tiny").disabled, true);

  // Done: the catalog is read again on its own, and the first model downloaded is the one used.
  gw.catalog.download = null;
  gw.catalog.models = gw.catalog.models.map((x) => (x.id === "whisper-base" ? { ...x, installed: true } : x));
  await new Promise((r) => setTimeout(r, DOWNLOAD_POLL_MS + 100));
  await settle();
  assert.equal(m.box.prefs.listener, "whisper-base");
  assert.deepEqual(workers.at(-1).sent.at(-1), { kind: "warm", id: "whisper-base" });
  assert.match(text(m.tid("local-listener-loading-whisper-base")), /Loading into memory… 0%/);
  await tell({ kind: "progress", id: "whisper-base", pct: 60 });
  assert.equal(m.tid("local-listener-loading-whisper-base").querySelector("progress").value, 60);
  await tell({ kind: "ready", id: "whisper-base", gpu: false });
  assert.equal(text(m.tid("local-listener-use-whisper-base")), "In use");

  // Another one downloaded is chosen with Use; one that cannot load is tried again.
  gw.catalog.models = gw.catalog.models.map((x) => (x.id === "whisper-tiny" ? { ...x, installed: true } : x));
  await pick(m.tid("listen-language"), "es");
  assert.equal(m.box.prefs.language, "es");
  await click(m.tid("local-listener-get-whisper-small"));
  await click(m.tid("local-listener-use-whisper-tiny"));
  assert.equal(m.box.prefs.listener, "whisper-tiny");
  await tell({ kind: "failed", id: "whisper-tiny", error: "HTTP 404" });
  assert.match(text(m.root), /The model could not be loaded: HTTP 404/);
  const sent = workers.at(-1).sent.length;
  await click(m.tid("local-listener-retry-whisper-tiny"));
  assert.equal(workers.at(-1).sent.length, sent + 1);

  // Deleting the one in use leaves none chosen; deleting another keeps the choice.
  await tell({ kind: "ready", id: "whisper-tiny" });
  await click(m.tid("local-listener-delete-whisper-base"));
  assert.ok(gw.calls.includes("DELETE /v1/voice/models/whisper-base"));
  assert.equal(m.box.prefs.listener, "whisper-tiny");
  gw.catalog.models = gw.catalog.models.map((x) => ({ ...x, installed: x.engine === "piper" }));
  await click(m.tid("local-listener-delete-whisper-tiny"));
  assert.equal(m.box.prefs.listener, "");
  await m.done();
});

test("a refused deletion keeps the model in use, and a choice the catalog lost moves on", async () => {
  const installed = (...ids) => {
    gw.catalog.models = gw.catalog.models.map((x) => ({ ...x, installed: x.engine === "piper" || ids.includes(x.id) }));
  };
  installed("whisper-base");
  gw.refuse = "the model is in use";
  let m = await mount({ listener: "whisper-base", engine: "local", language: "es" });
  await click(m.tid("local-listener-delete-whisper-base"));
  assert.match(text(m.root), /the model is in use/);
  assert.equal(m.box.prefs.listener, "whisper-base", "a deletion that failed keeps the choice");
  await m.done();

  // Deleted while the language was changed: the language stays, the next model downloaded is used.
  gw.refuse = "";
  installed("whisper-base", "whisper-tiny");
  m = await mount({ listener: "whisper-base", engine: "local" });
  await pick(m.tid("listen-language"), "pt");
  installed("whisper-tiny");
  await click(m.tid("local-listener-delete-whisper-base"));
  assert.equal(m.box.prefs.listener, "whisper-tiny");
  assert.equal(m.box.prefs.language, "pt");
  await m.done();

  // Saved before it was deleted elsewhere, with nothing left downloaded: no model is chosen.
  installed();
  m = await mount({ listener: "whisper-small", engine: "local" });
  assert.equal(m.box.prefs.listener, "");
  await m.done();
});

test("what the gateway refuses, a failed download, and a catalog that cannot be read are said", async () => {
  gw.refuse = "another voice is downloading";
  const m = await mount();
  await click(m.tid("local-listener-get-whisper-small"));
  assert.match(text(m.root), /another voice is downloading/);
  gw.catalog.download = { id: "whisper-small", done: 1, total: 2, error: "checksum" };
  gw.refuse = "";
  await click(m.tid("local-listener-get-whisper-tiny"));
  assert.match(text(m.root), /The download failed: checksum/);
  await m.done();

  gw.down = "the gateway keeps no local voices";
  const n = await mount();
  assert.equal(text(n.tid("local-listeners")), "the gateway keeps no local voices");
  await n.done();
});

test("with WebGPU the models for the card come first, and say where they run", async () => {
  Object.defineProperty(navigator, "gpu", { value: { requestAdapter: async () => ({}) }, configurable: true });
  gw.catalog.runtime.installed = true;
  gw.catalog.models = gw.catalog.models.map((x) => (x.id === "whisper-small-gpu" ? { ...x, installed: true } : x));
  const m = await mount({ listener: "whisper-small-gpu", engine: "local" });
  // The one in use stays in sight; the rest are folded, the ones for the card first.
  const rows = [...m.root.querySelectorAll("[data-testid^=local-listener-whisper]")].map((r) => r.dataset.testid);
  assert.deepEqual(rows.slice(0, 2), ["local-listener-whisper-small-gpu", "local-listener-whisper-base-gpu"]);
  const list = m.tid("local-listener-list");
  assert.equal(list.open, false, "the list starts folded");
  assert.equal(text(list.querySelector("summary")), "Every model (4)");
  assert.equal(list.querySelector("[data-testid=local-listener-whisper-small-gpu]"), null);
  assert.match(text(m.tid("local-listener-whisper-small-gpu")), /Whisper small ?WebGPU · High quality · 80 MB · Recommended/);
  await tell({ kind: "ready", id: "whisper-small-gpu", gpu: true });
  assert.match(text(m.tid("local-listener-whisper-small-gpu")), /on the graphics card/);
  await tell({ kind: "ready", id: "whisper-small-gpu", gpuError: "no adapter" });
  assert.match(text(m.tid("local-listener-gpu-note")), /WebGPU could not start here \(no adapter\)/);
  await m.done();
  delete navigator.gpu;
});

test("a language the browser cannot name is shown by its code", async () => {
  const real = Intl.DisplayNames;
  Intl.DisplayNames = class {
    constructor() {
      throw new RangeError("no names");
    }
  };
  try {
    const m = await mount({ engine: "local" });
    const names = [...m.tid("listen-language").querySelectorAll("option")].map((o) => o.textContent);
    assert.deepEqual(names, ["Detect it", ...listenSettings.LISTEN_LANGUAGES]);
    await m.done();
  } finally {
    Intl.DisplayNames = real;
  }
  assert.equal(listenSettings.recommendedFor(false), "whisper-base");
});
