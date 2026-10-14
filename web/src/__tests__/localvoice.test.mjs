// Local voices: a Piper model run in the browser. The synthesis with a stand-in onnxruntime and
// phonemizer, the page's side of the worker and of the gateway's catalog, and the settings that
// download, choose and delete a voice (bundled with esbuild, since node cannot import TSX).
import test from "node:test";
import assert from "node:assert/strict";
import { fileURLToPath, pathToFileURL } from "node:url";
import { dirname, join } from "node:path";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { JSDOM } from "jsdom";
import { build } from "esbuild";

const here = dirname(fileURLToPath(import.meta.url));
const dom = new JSDOM("<!doctype html><html><body></body></html>", {
  url: "http://localhost/",
});
for (const k of ["document", "window", "Event", "HTMLElement", "Node", "localStorage"]) {
  globalThis[k] = dom.window[k];
}

const core = await import("../localVoiceCore.ts");

const out = mkdtempSync(join(here, ".lv-"));
await build({
  stdin: {
    contents:
      'export * from "./localVoice";\nexport { LocalVoices, DOWNLOAD_POLL_MS } from "./LocalVoiceSettings";\nexport { SpeechSettings } from "./SpeechSettings";\n',
    resolveDir: join(here, ".."),
    loader: "ts",
  },
  outfile: join(out, "lv.mjs"),
  bundle: true,
  format: "esm",
  jsx: "automatic",
  jsxImportSource: "preact",
  platform: "node",
  external: ["preact", "preact/*"],
  loader: { ".css": "empty" },
  logLevel: "silent",
});
const lv = await import(pathToFileURL(join(out, "lv.mjs")).href);
const { h, render } = await import("preact");
const { act } = await import("preact/test-utils");
rmSync(out, { recursive: true });

const HTTP_OK = 200;
const HTTP_ACCEPTED = 202;
const HTTP_NO_CONTENT = 204;
const HTTP_NOT_FOUND = 404;
const HTTP_CONFLICT = 409;
const HTTP_SERVER_ERROR = 500;

const SAMPLE_RATE = 22050;
// A WAV file: a 44-byte header, then 16-bit samples.
const WAV_HEADER_BYTES = 44;
const BYTES_PER_SAMPLE = 2;
const WAVE_AT = 8;
const DATA_TAG_AT = 36;
const CHANNELS_AT = 22;
const SAMPLE_RATE_AT = 24;
const BITS_PER_SAMPLE_AT = 34;
const DATA_SIZE_AT = 40;
const INT16_MAX = 32767;
// A sample beyond full scale, which the WAV clips.
const OVER_FULL_SCALE = -2;
// What the stand-in model says, and the phoneme ids the stand-in phonemizer answers.
const HALF = 0.5;
const MODEL_OUTPUT = [HALF, -HALF];
const PHONEME_IDS = [1, 0, 5, 0, 2];
// The text to phonemize is the fourth argument of the phonemizer's command line.
const INPUT_ARG = 3;
// The voice's noise_scale, length_scale and noise_w, in thousandths.
const SCALES_MILLI = [667, 1000, 800];
// settle waits this many rounds for the requests in flight.
const SETTLE_ROUNDS = 3;

const VOICE = {
  espeak: { voice: "es-419" },
  audio: { sample_rate: SAMPLE_RATE },
  inference: { noise_scale: 0.667, length_scale: 1, noise_w: 0.8 },
};
const enc = (s) => new TextEncoder().encode(s).buffer;

test("the WAV header says mono 16-bit at the voice's rate, and the samples are clipped", () => {
  const samples = [0, 1, OVER_FULL_SCALE];
  const dataBytes = samples.length * BYTES_PER_SAMPLE;
  const wav = new DataView(core.wavOf(Float32Array.from(samples), SAMPLE_RATE));
  const text = (at) => String.fromCodePoint(...new Uint8Array(wav.buffer, at, 4));
  assert.equal(text(0), "RIFF");
  assert.equal(text(WAVE_AT), "WAVE");
  assert.equal(text(DATA_TAG_AT), "data");
  assert.equal(wav.byteLength, WAV_HEADER_BYTES + dataBytes);
  assert.equal(wav.getUint16(CHANNELS_AT, true), 1, "mono");
  assert.equal(wav.getUint32(SAMPLE_RATE_AT, true), SAMPLE_RATE);
  assert.equal(wav.getUint16(BITS_PER_SAMPLE_AT, true), 16);
  assert.equal(wav.getUint32(DATA_SIZE_AT, true), dataBytes);
  assert.deepEqual(
    [0, 1, 2].map((i) => wav.getInt16(WAV_HEADER_BYTES + i * BYTES_PER_SAMPLE, true)),
    [0, INT16_MAX, -INT16_MAX],
  );
});

test("a voice's settings, the phonemizer's command line and its answer are read", () => {
  assert.equal(core.parseVoiceConfig(JSON.stringify(VOICE)).espeak.voice, "es-419");
  assert.throws(() => core.parseVoiceConfig("{}"), /not a Piper voice/);
  assert.deepEqual(core.phonemizeArgs("es-419", "Hola"), [
    "-l",
    "es-419",
    "--input",
    '[{"text":"Hola"}]',
    "--espeak_data",
    "/espeak-ng-data",
  ]);
  assert.deepEqual(core.phonemeIds('{"phoneme_ids":[1,0,2]}'), [1, 0, 2]);
  assert.throws(() => core.phonemeIds('{"phoneme_ids":[]}'), /answered nothing/);
  assert.equal(core.fileURL("a b", "model.onnx"), "/v1/voice/models/a%20b/model.onnx");
});

// fakes are a stand-in runtime and phonemizer that record what they were given.
function fakes({ speakers = 1, failFirstVoice = false, silent = false } = {}) {
  const log = { bytes: [], ort: 0, phon: 0, sessions: 0, feeds: [], args: [] };
  let failed = !failFirstVoice;
  const deps = {
    bytes: async (url) => {
      log.bytes.push(url);
      if (url.endsWith("model.json")) {
        if (!failed) {
          failed = true;
          throw new Error("HTTP 404 " + url);
        }
        return enc(JSON.stringify({ ...VOICE, num_speakers: speakers }));
      }
      return enc(url);
    },
    ort: async (wasm) => {
      log.ort++;
      assert.equal(new TextDecoder().decode(wasm), "/v1/voice/models/runtime/ort-wasm-simd-threaded.wasm");
      return {
        Tensor: function Tensor(type, data, dims) {
          Object.assign(this, { type, data, dims });
        },
        createSession: async () => {
          log.sessions++;
          return {
            run: async (feeds) => {
              log.feeds.push(feeds);
              return { output: { data: Float32Array.from(MODEL_OUTPUT) } };
            },
          };
        },
      };
    },
    phonemizer: async (_wasm, data, print) => {
      log.phon++;
      assert.match(new TextDecoder().decode(data), /piper_phonemize\.data$/);
      return {
        callMain: (args) => {
          log.args.push(args);
          if (!silent) {
            print("[espeak] ready");
            print(JSON.stringify({ phoneme_ids: PHONEME_IDS }));
          }
        },
      };
    },
  };
  return { deps, log };
}

test("the synthesis loads the runtime, the phonemizer and each voice once, and feeds the model", async () => {
  const { deps, log } = fakes();
  const synth = new core.Synth(deps);
  const [a, b] = await Promise.all([synth.say("v1", "Hola."), synth.say("v1", "Chao.")]);
  assert.equal(new DataView(a).getUint32(SAMPLE_RATE_AT, true), SAMPLE_RATE);
  assert.equal(b.byteLength, WAV_HEADER_BYTES + MODEL_OUTPUT.length * BYTES_PER_SAMPLE);
  assert.deepEqual([log.ort, log.phon, log.sessions], [1, 1, 1]);
  assert.deepEqual(log.args.map((x) => JSON.parse(x[INPUT_ARG])[0].text), ["Hola.", "Chao."], "in order");
  const f = log.feeds[0];
  assert.deepEqual([...f.input.data], [1n, 0n, 5n, 0n, 2n]);
  assert.deepEqual(f.input.dims, [1, PHONEME_IDS.length]);
  assert.deepEqual([...f.input_lengths.data], [5n]);
  assert.deepEqual([...f.scales.data].map((x) => Math.round(x * 1000)), SCALES_MILLI);
  assert.equal(f.sid, undefined, "one speaker: no speaker id");
  // Warming a voice loaded already loads nothing more.
  await synth.warm("v1");
  assert.deepEqual([log.ort, log.phon, log.sessions], [1, 1, 1]);
  // A forgotten voice is loaded again.
  synth.forget("v1");
  await synth.say("v1", "Otra.");
  assert.equal(log.sessions, 2);
});

test("a voice with several speakers gets a speaker id; a failure is reported and tried again", async () => {
  const many = fakes({ speakers: 4 });
  await new core.Synth(many.deps).say("v1", "Hola.");
  assert.deepEqual([...many.log.feeds[0].sid.data], [0n]);

  const flaky = fakes({ failFirstVoice: true });
  const synth = new core.Synth(flaky.deps);
  await assert.rejects(synth.say("v1", "Hola."), /HTTP 404/);
  await synth.say("v1", "Hola.");
  assert.equal(flaky.log.sessions, 1);

  const silent = fakes({ silent: true });
  await assert.rejects(new core.Synth(silent.deps).say("v1", "Hola."), /answered nothing/);

  // A runtime or phonemizer that failed to load is tried again too.
  const broken = fakes();
  let ortFails = 1;
  let phonFails = 1;
  const ort = broken.deps.ort;
  const phonemizer = broken.deps.phonemizer;
  const retry = new core.Synth({
    ...broken.deps,
    ort: async (w) => {
      ortFails -= 1;
      if (ortFails >= 0) {
        throw new Error("no runtime");
      }
      return ort(w);
    },
    phonemizer: async (...a) => {
      phonFails -= 1;
      if (phonFails >= 0) {
        throw new Error("no phonemizer");
      }
      return phonemizer(...a);
    },
  });
  await assert.rejects(retry.say("v1", "x"), /no runtime/);
  await assert.rejects(retry.say("v1", "x"), /no phonemizer/);
  await retry.say("v1", "x");
});

// apiOf answers the gateway's endpoints from a table, and records the requests.
function apiOf(table) {
  const calls = [];
  const api = async (path, init = {}) => {
    calls.push((init.method || "GET") + " " + path);
    const r = table[(init.method || "GET") + " " + path];
    if (typeof r === "function") {
      return r();
    }
    const [status, body] = r ?? [HTTP_NOT_FOUND, { error: "not found" }];
    return new Response(JSON.stringify(body), { status });
  };
  return { api, calls };
}

test("the page asks the gateway for the catalog, a download and a removal, and says why one failed", async () => {
  const cat = { runtime: { size: 1, installed: true }, models: [], download: null };
  const { api, calls } = apiOf({
    "GET /v1/voice/models": [HTTP_OK, cat],
    "POST /v1/voice/models/a": [HTTP_ACCEPTED, {}],
    "POST /v1/voice/models/b": [HTTP_CONFLICT, { error: "another voice is downloading" }],
    "DELETE /v1/voice/models/a": () => new Response(null, { status: HTTP_NO_CONTENT }),
    "DELETE /v1/voice/models/b": () => new Response("<html>", { status: HTTP_SERVER_ERROR }),
  });
  assert.deepEqual(await lv.fetchCatalog(api), cat);
  await lv.installVoice(api, "a");
  await assert.rejects(lv.installVoice(api, "b"), /another voice is downloading/);
  const deleted = [];
  globalThis.caches = {
    open: async (name) => {
      assert.equal(name, "motita-voices-v1");
      return { delete: async (u) => deleted.push(u) };
    },
  };
  await lv.deleteVoice(api, "a");
  assert.deepEqual(deleted, ["/v1/voice/models/a/model.onnx", "/v1/voice/models/a/model.json"]);
  await assert.rejects(lv.deleteVoice(api, "b"), /HTTP 500/);
  await assert.rejects(lv.fetchCatalog(apiOf({}).api), /not found/);
  const first = ["GET /v1/voice/models", "POST /v1/voice/models/a", "POST /v1/voice/models/b"];
  assert.deepEqual(calls.slice(0, first.length), first);
  delete globalThis.caches;
  assert.equal(lv.localVoicesSupported(), false, "no worker, no Cache Storage");
});

test("a local clip is said by the worker; an aborted one is dropped, and a dead worker fails what waits", async () => {
  const posted = [];
  let made = 0;
  globalThis.Worker = class {
    constructor(url, opts) {
      made++;
      assert.match(String(url), /speechModel\.worker/);
      assert.equal(opts.type, "module");
      globalThis.lastWorker = this;
    }
    postMessage(m) {
      posted.push(m);
      if (m.kind !== "say") {
        return;
      }
      queueMicrotask(() => {
        if (m.text === "boom") {
          this.onmessage({ data: { seq: m.seq, error: "the model failed" } });
          return;
        }
        if (m.text === "hang") {
          return;
        }
        this.onmessage({ data: { seq: m.seq, wav: enc("RIFF") } });
      });
    }
  };
  lv.warmLocal("v1");
  assert.deepEqual(posted[0], { kind: "warm", id: "v1" });
  posted.shift();
  const say = lv.localClip("v1");
  const blob = await say("Hola.", new AbortController().signal);
  assert.equal(blob.type, "audio/wav");
  assert.equal(await blob.text(), "RIFF");
  await assert.rejects(say("boom", new AbortController().signal), /the model failed/);
  const ab = new AbortController();
  const late = say("Tarde.", ab.signal);
  ab.abort();
  await assert.rejects(late, { name: "AbortError" });
  const waiting = say("hang", new AbortController().signal);
  globalThis.lastWorker.onerror();
  await assert.rejects(waiting, /the local voice stopped/);
  // The next clip makes a new worker.
  await say("Otra vez.", new AbortController().signal);
  assert.equal(made, 2);
  assert.deepEqual(posted[0], { kind: "say", seq: posted[0].seq, id: "v1", text: "Hola." });
  delete globalThis.Worker;
});

// settle lets the requests in flight answer and the page draw what they said.
async function settle() {
  for (let i = 0; i < SETTLE_ROUNDS; i++) {
    await act(() => new Promise((r) => setTimeout(r, 0)));
  }
}

// mount renders a component and returns its root.
async function mount(node) {
  const root = document.createElement("div");
  document.body.appendChild(root);
  await act(async () => render(node, root));
  await settle();
  return {
    root,
    done: () => {
      render(null, root);
      root.remove();
    },
  };
}

test("the settings download a voice, follow its progress, choose it and delete it", async (t) => {
  t.mock.timers.enable({ apis: ["setInterval"] });
  const model = (id, lang, installed) => ({ id, name: id.toUpperCase(), lang, quality: "medium", size: 63e6, installed });
  let state = {
    runtime: { size: 33e6, installed: false },
    models: [model("en", "en-US", false), model("es", "es-MX", false)],
    download: null,
  };
  const { api, calls } = apiOf({
    "GET /v1/voice/models": () => new Response(JSON.stringify(state), { status: HTTP_OK }),
    "POST /v1/voice/models/es": () => {
      state = { ...state, download: { id: "es", done: 48e6, total: 96e6 } };
      return new Response("{}", { status: HTTP_ACCEPTED });
    },
    "DELETE /v1/voice/models/es": () => {
      state = { ...state, models: [state.models[0], model("es", "es-MX", false)] };
      return new Response(null, { status: HTTP_NO_CONTENT });
    },
  });
  const chosen = [];
  const draw = (current) => h(lv.LocalVoices, { api, chosen: current, choose: (id) => chosen.push(id) });
  const v = await mount(draw(""));
  const q = (s) => v.root.querySelector(`[data-testid=${s}]`);
  assert.match(q("local-voices").textContent, /33 MB/, "the engine's size is named before the first download");
  await act(async () => q("local-voice-get-es").click());
  await settle();
  assert.ok(calls.includes("POST /v1/voice/models/es"));
  assert.match(q("local-voice-progress-es").textContent, /50/);
  assert.equal(q("local-voice-get-en").disabled, true, "one download at a time");
  // The download ends: the next poll shows it installed, and as nothing was chosen it is chosen.
  state = { ...state, runtime: { ...state.runtime, installed: true }, models: [state.models[0], model("es", "es-MX", true)], download: null };
  await act(async () => t.mock.timers.tick(lv.DOWNLOAD_POLL_MS));
  await settle();
  assert.equal(q("local-voice-progress-es"), null);
  assert.deepEqual(chosen, ["es"]);
  assert.doesNotMatch(q("local-voices").textContent, /33 MB/);
  await act(async () => render(draw("es"), v.root));
  assert.equal(q("local-voice-use-es").getAttribute("aria-pressed"), "true");
  await act(async () => q("local-voice-delete-es").click());
  await settle();
  assert.deepEqual(chosen, ["es", ""], "deleting the voice in use leaves none chosen");
  assert.ok(q("local-voice-get-es"));
  v.done();
});

test("a failed download and an unreachable catalog are shown", async () => {
  const failed = {
    runtime: { size: 1, installed: true },
    models: [{ id: "es", name: "ES", lang: "es-MX", quality: "high", size: 1, installed: true }],
    download: { id: "es", done: 0, total: 1, error: "model.onnx is not the file the catalog names" },
  };
  const ok = apiOf({ "GET /v1/voice/models": [HTTP_OK, failed], "POST /v1/voice/models/es": [HTTP_CONFLICT, { error: "busy" }] });
  let v = await mount(h(lv.LocalVoices, { api: ok.api, chosen: "es", choose: () => {} }));
  assert.match(v.root.textContent, /model\.onnx is not the file/);
  // Use on the chosen voice says it is in use.
  assert.equal(v.root.querySelector("[data-testid=local-voice-use-es]").getAttribute("aria-pressed"), "true");
  v.done();
  const down = apiOf({});
  v = await mount(h(lv.LocalVoices, { api: down.api, chosen: "", choose: () => {} }));
  assert.match(v.root.textContent, /not found/);
  v.done();
  // A refused download names why.
  const refusing = apiOf({
    "GET /v1/voice/models": [HTTP_OK, { ...failed, models: [{ ...failed.models[0], installed: false }], download: null }],
    "POST /v1/voice/models/es": [HTTP_CONFLICT, { error: "another voice is downloading" }],
  });
  v = await mount(h(lv.LocalVoices, { api: refusing.api, chosen: "", choose: () => {} }));
  await act(async () => v.root.querySelector("[data-testid=local-voice-get-es]").click());
  await settle();
  assert.match(v.root.textContent, /another voice is downloading/);
  v.done();
});

test("who speaks offers the local voice when the gateway has them, and shows them when chosen", async () => {
  const prefs = {
    mode: "voice",
    style: "literal",
    engine: "device",
    gatewayVoice: "",
    localVoice: "",
    voiceURI: "",
    rate: 1,
    maxChars: 600,
    conversation: false,
    notices: false,
    byLanguage: false,
    langVoices: {},
    pronunciations: [],
  };
  const saved = [];
  const { api } = apiOf({
    "GET /v1/voice/models": [HTTP_OK, { runtime: { size: 1, installed: true }, models: [], download: null }],
  });
  const props = (p, extra = {}) => ({ supported: true, prefs: p, setPrefs: (x) => saved.push(x), voices: [], test: () => {}, ...extra });
  let v = await mount(h(lv.SpeechSettings, props(prefs)));
  assert.equal(v.root.querySelector("input[name=settings-speech-engine]"), null, "one engine: no choice");
  v.done();
  v = await mount(h(lv.SpeechSettings, props(prefs, { local: true, api, gatewayVoices: ["nova"] })));
  const values = [...v.root.querySelectorAll("input[name=settings-speech-engine]")].map((i) => i.value);
  assert.deepEqual(values, ["device", "gateway", "local"]);
  await act(async () => v.root.querySelector("input[name=settings-speech-engine][value=local]").click());
  assert.equal(saved.pop().engine, "local");
  v.done();
  v = await mount(h(lv.SpeechSettings, props({ ...prefs, engine: "local" }, { local: true, api })));
  assert.ok(v.root.querySelector("[data-testid=local-voices]"));
  assert.equal(v.root.querySelector("[aria-labelledby=settings-speech-voice]"), null, "no device voice list");
  assert.equal(v.root.querySelector("input[name=settings-speech-engine][value=local]").checked, true);
  v.done();
});

test("the runtime the gateway downloads is the one the page is built with", () => {
  const pkg = JSON.parse(readFileSync(join(here, "..", "..", "package.json"), "utf8"));
  const catalog = readFileSync(join(here, "..", "..", "..", "internal", "voice", "models.go"), "utf8");
  for (const [name, file] of [
    ["onnxruntime-web", "onnxruntime-web-"],
    ["@diffusionstudio/piper-wasm", "piper-wasm-"],
  ]) {
    const version = pkg.dependencies[name];
    assert.match(version, /^\d+\.\d+\.\d+$/, name + " is pinned to one version");
    assert.ok(catalog.includes(file + version + ".tgz"), name + " " + version + " is the catalog's");
  }
});
