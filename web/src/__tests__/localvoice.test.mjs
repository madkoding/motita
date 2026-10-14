// Local voices: a Piper, Supertonic or Chatterbox model run in the browser. The synthesis with a stand-in onnxruntime and
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
      'export * from "./localVoice";\nexport { LocalVoices, DOWNLOAD_POLL_MS, FILTERS_KEY, loadFilters } from "./LocalVoiceSettings";\nexport { SpeechSettings } from "./SpeechSettings";\n',
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
const PHONEMES = ["h", "ˈ", "o", "l", "a", ".", "r", "x"];
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
  assert.deepEqual(core.phonemized('{"phoneme_ids":[1,0,2],"phonemes":["ˈ","o"]}'), { ids: [1, 0, 2] });
  assert.throws(() => core.phonemized('{"phoneme_ids":[]}'), /answered nothing/);
  assert.equal(core.fileURL("a b", "model.onnx"), "/v1/voice/models/a%20b/model.onnx");
});

test("a failure's reason is its message, cut short when long", () => {
  assert.equal(core.reasonOf(new Error(" no adapter ")), "no adapter");
  assert.equal(core.reasonOf("plain"), "plain");
  assert.equal(core.reasonOf(new Error("")), "Error");
  const long = core.reasonOf(new Error("x".repeat(500)));
  assert.equal(long.length, 200);
  assert.ok(long.endsWith("…"));
});

// gpuFakes are fakes with WebGPU's build too, which fail when told to.
function gpuFakes({ fail = false } = {}) {
  const { deps, log } = fakes();
  log.gpu = 0;
  deps.gpu = async () => {
    log.gpu++;
    if (fail) {
      throw new Error("no WebGPU here");
    }
    const ort = await deps.ort(enc("/v1/voice/models/runtime/ort-wasm-simd-threaded.wasm"));
    log.ort--;
    return ort;
  };
  return { deps, log };
}

// fakes are a stand-in runtime and phonemizer that record what they were given.
function fakes({ speakers = 1, failFirstVoice = false, silent = false } = {}) {
  const log = { bytes: [], ort: 0, phon: 0, sessions: 0, feeds: [], args: [] };
  let failed = !failFirstVoice;
  const deps = {
    bytes: async (url, progress) => {
      log.bytes.push(url);
      progress?.(0, 10);
      progress?.(10, 10);
      if (url.endsWith("model.json")) {
        if (!failed) {
          failed = true;
          throw new Error("HTTP 404 " + url);
        }
        return enc(JSON.stringify({ ...VOICE, num_speakers: speakers }));
      }
      if (url.endsWith("chatterbox/tokenizer.json")) {
        return enc(JSON.stringify(CB_TOKENIZER));
      }
      if (url.endsWith("voice.bin") && url.includes("chatterbox-")) {
        return cbVoice();
      }
      if (url.endsWith("unicode_indexer.json")) {
        // Each character's id is its code point plus one; there is none past "z".
        return enc(JSON.stringify(Array.from({ length: 123 }, (_, i) => i + 1)));
      }
      if (url.endsWith("voice.json")) {
        return enc(JSON.stringify({ style_ttl: { data: [[[1, 2]]], dims: [1, 1, 2] }, style_dp: { data: [[[3]]], dims: [1, 1, 1] } }));
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
              const data = Float32Array.from(MODEL_OUTPUT);
              if (feeds.exaggeration) {
                const n = feeds.input_ids.dims[1];
                return { inputs_embeds: { data: new Float32Array(n * 1024).fill(n), dims: [1, n, 1024] } };
              }
              if (feeds.inputs_embeds) {
                // The language model says sound token 7 twice, then that it is done.
                log.lm = (log.lm ?? 0) + 1;
                const logits = new Float32Array(2 * CB_VOCAB);
                logits[CB_VOCAB + choose3(log.lm)] = 1;
                const present = {};
                for (let l = 0; l < 30; l++) {
                  for (const kv of ["key", "value"]) {
                    present["present." + l + "." + kv] = { data: "kv" + log.lm };
                  }
                }
                return { logits: { data: logits, dims: [1, 2, CB_VOCAB] }, ...present };
              }
              // Every engine's outputs: Piper's and Supertonic's four parts'.
              return {
                output: { data },
                waveform: { data },
                wav_tts: { data },
                duration: { data: Float32Array.from([0.1]) },
                text_emb: { data: "emb" },
                denoised_latent: { data: feeds.noisy_latent?.data.map((x) => x / 2) },
              };
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
            print(JSON.stringify({ phoneme_ids: PHONEME_IDS, phonemes: PHONEMES }));
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
  // The sliders scale the intonation and the rhythm; the speed is left to the page.
  await synth.say("v1", "Tono.", { noise: 0.5, noiseW: 2 });
  assert.deepEqual([...log.feeds[2].scales.data].map((x) => Math.round(x * 1000)), [333, 1000, 1600]);
  // Warming a voice loaded already loads nothing more.
  await synth.warm("v1");
  assert.deepEqual([log.ort, log.phon, log.sessions], [1, 1, 1]);
  // A forgotten voice is loaded again.
  synth.forget("v1");
  await synth.say("v1", "Otra.");
  assert.equal(log.sessions, 2);
});

test("warming reports how far the files are read, never backwards, and 100 when the voice is ready", async () => {
  const { deps, log } = fakes();
  const synth = new core.Synth(deps);
  const seen = [];
  await synth.warm("v1", (p) => seen.push(p));
  assert.equal(seen.at(-1), 100);
  const before = core.LOAD_SHARE + core.RUNTIME_SHARE + core.PHONEMIZER_SHARE;
  assert.equal(seen.at(-2), before, "the files, the runtime and the phonemizer, before the model");
  assert.deepEqual([...seen].sort((a, b) => a - b), seen, "never backwards");
  assert.equal(log.bytes.length, 5, "the runtime, the phonemizer and the voice");
  // Everything loaded already: straight to 100, and a warm without a listener is fine too.
  const again = [];
  await synth.warm("v1", (p) => again.push(p));
  assert.deepEqual(again, [100]);
  await synth.warm("v1");
  // A failed warm says so, and the next one loads again.
  const flaky = fakes({ failFirstVoice: true });
  const s2 = new core.Synth(flaky.deps);
  await assert.rejects(s2.warm("v1", () => {}), /HTTP 404/);
  await s2.warm("v1");
});

test("a response is read whole, its progress told as it arrives", async () => {
  const seen = [];
  const body = new Response(new Blob(["abc", "def"]).stream(), { headers: { "Content-Length": "6" } });
  const got = await core.readAll(body, (l, t) => seen.push([l, t]));
  assert.equal(new TextDecoder().decode(got), "abcdef");
  assert.deepEqual(seen[0], [0, 6]);
  assert.deepEqual(seen.at(-1), [6, 6]);
  const none = [];
  const empty = await core.readAll(new Response(null), (l, t) => none.push([l, t]));
  assert.equal(empty.byteLength, 0);
  assert.deepEqual(none, [[0, 0], [0, 0]]);
  assert.equal(new TextDecoder().decode(await core.readAll(new Response("x"))), "x");
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
  const cat = { runtime: { size: 1, installed: true }, models: [{ id: "a" }], download: null };
  const { api, calls } = apiOf({
    "GET /v1/voice/models": [HTTP_OK, cat],
    "POST /v1/voice/models/a": [HTTP_ACCEPTED, {}],
    "POST /v1/voice/models/b": [HTTP_CONFLICT, { error: "another voice is downloading" }],
    "DELETE /v1/voice/models/a": () => new Response(null, { status: HTTP_NO_CONTENT }),
    "DELETE /v1/voice/models/b": () => new Response("<html>", { status: HTTP_SERVER_ERROR }),
  });
  // The first catalog read drops this page's copies of what the catalog no longer has (Kokoro's).
  const page = "http://localhost/v1/voice/models/";
  const cached = ["kokoro/model.onnx", "accel-kokoro/model.onnx", "kokoro-ef_dora/voice.bin", "runtime/ort.wasm",
    "supertonic/vocoder.onnx", "chatterbox/tokenizer.json", "a/model.onnx"].map((f) => ({ url: page + f }));
  const pruned = [];
  globalThis.caches = {
    open: async () => ({ keys: async () => [...cached, { url: "http://localhost/other" }], delete: async (r) => pruned.push(r.url) }),
  };
  assert.deepEqual(await lv.fetchCatalog(api), cat);
  await new Promise((r) => setTimeout(r, 0));
  assert.deepEqual(pruned, cached.slice(0, 3).map((r) => r.url));
  await lv.fetchCatalog(api);
  await new Promise((r) => setTimeout(r, 0));
  assert.equal(pruned.length, 3, "once a page");
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
  assert.deepEqual(deleted, ["/v1/voice/models/a/model.onnx", "/v1/voice/models/a/model.json", "/v1/voice/models/a/voice.bin", "/v1/voice/models/a/voice.json"]);
  // An engine's model goes from the page's copy with its last voice, and stays while one is left.
  const k = (id, installed) => ({ id, engine: "supertonic", installed });
  const kept = apiOf({
    "DELETE /v1/voice/models/supertonic-F1": () => new Response(null, { status: 204 }),
    "GET /v1/voice/models": () => new Response(JSON.stringify({ ...cat, models: [k("supertonic-F1", false), k("supertonic-F2", true)] }), { status: 200 }),
  });
  deleted.length = 0;
  await lv.deleteVoice(kept.api, "supertonic-F1");
  assert.equal(deleted.length, 4);
  // Supertonic's, the same.
  const st = apiOf({
    "DELETE /v1/voice/models/supertonic-F1": () => new Response(null, { status: 204 }),
    "GET /v1/voice/models": () => new Response(JSON.stringify({ ...cat, models: [] }), { status: 200 }),
  });
  deleted.length = 0;
  await lv.deleteVoice(st.api, "supertonic-F1");
  assert.deepEqual(deleted.slice(4), [
    "/v1/voice/models/supertonic/duration_predictor.onnx",
    "/v1/voice/models/supertonic/text_encoder.onnx",
    "/v1/voice/models/supertonic/vector_estimator.onnx",
    "/v1/voice/models/supertonic/vocoder.onnx",
    "/v1/voice/models/supertonic/unicode_indexer.json",
    "/v1/voice/models/supertonic/ort-wasm-simd-threaded.asyncify.wasm",
  ]);
  await assert.rejects(lv.deleteVoice(api, "b"), /HTTP 500/);
  await assert.rejects(lv.fetchCatalog(apiOf({}).api), /not found/);
  const first = ["GET /v1/voice/models", "GET /v1/voice/models", "POST /v1/voice/models/a", "POST /v1/voice/models/b"];
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
  let changes = 0;
  const off = lv.onLoadState(() => changes++);
  lv.warmLocal("v1");
  assert.deepEqual(posted[0], { kind: "warm", id: "v1" });
  posted.shift();
  assert.deepEqual(lv.loadStateOf("v1"), { pct: 0, ready: false });
  lv.warmLocal("v1");
  assert.equal(posted.length, 0, "a voice loading is not asked for again");
  const tell = (data) => globalThis.lastWorker.onmessage({ data });
  tell({ kind: "progress", id: "v1", pct: 40 });
  assert.deepEqual(lv.loadStateOf("v1"), { pct: 40, ready: false });
  tell({ kind: "failed", id: "v1", error: "no memory" });
  assert.deepEqual(lv.loadStateOf("v1"), { pct: 0, ready: false, error: "no memory" });
  lv.warmLocal("v1");
  assert.deepEqual(posted.shift(), { kind: "warm", id: "v1" }, "a voice that failed is asked for again");
  tell({ kind: "ready", id: "v1" });
  assert.deepEqual(lv.loadStateOf("v1"), { pct: 100, ready: true });
  assert.equal(changes, 5);
  off();
  const say = lv.localClip("v1", { noise: 0.5, noiseW: 1.5 });
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
  assert.equal(lv.loadStateOf("v1"), undefined, "a dead worker's voices are loaded again");
  // The next clip makes a new worker.
  await say("Otra vez.", new AbortController().signal);
  assert.equal(made, 2);
  assert.deepEqual(posted[0], {
    kind: "say",
    seq: posted[0].seq,
    id: "v1",
    text: "Hola.",
    tune: { noise: 0.5, noiseW: 1.5 },
  });
  assert.equal(say.ahead, 1, "a Piper voice says one sentence at a time");
  delete globalThis.Worker;
});

test("Supertonic on the CPU says the next sentences at once, in workers of their own", async () => {
  // The worker of an earlier test goes, so these are made with the stand-in below.
  globalThis.lastWorker?.onerror();
  const made = [];
  globalThis.Worker = class {
    constructor() {
      this.posted = [];
      made.push(this);
    }
    postMessage(m) {
      this.posted.push(m);
    }
  };
  const cores = (n) =>
    Object.defineProperty(globalThis.navigator, "hardwareConcurrency", { value: n, configurable: true });
  cores(8);
  const id = "supertonic-F1";
  assert.equal(lv.lanes(id), lv.SUPERTONIC_LANES, "Supertonic's model is large: few copies");
  assert.equal(lv.lanes("v1"), 1, "a Piper voice is quick enough alone");
  assert.equal(lv.lanes("chatterbox-default"), 1, "Chatterbox runs on the graphics card");
  cores(2);
  assert.equal(lv.lanes(id), 1, "one core is left to the page");
  cores(8);
  lv.warmLocal(id);
  assert.equal(made.length, 1, "the helpers wait for the first worker");
  made[0].onmessage({ data: { kind: "ready", id } });
  assert.equal(made.length, lv.SUPERTONIC_LANES);
  assert.deepEqual(made[1].posted, [{ kind: "warm", id }]);
  made[1].onmessage({ data: { kind: "progress", id, pct: 5 } });
  assert.equal(lv.loadStateOf(id).ready, true, "a helper's loading is not shown");
  // Each sentence goes to the worker with the fewest waiting.
  const say = lv.localClip(id);
  assert.equal(say.ahead, lv.SUPERTONIC_LANES);
  const signal = new AbortController().signal;
  const clips = ["a", "b", "c"].map((text) => say(text, signal));
  const said = (w) => w.posted.filter((m) => m.kind === "say").map((m) => m.text);
  assert.deepEqual(made.map(said), [["a", "c"], ["b"]]);
  for (const w of made) {
    for (const m of w.posted.filter((p) => p.kind === "say")) {
      w.onmessage({ data: { seq: m.seq, wav: enc(m.text) } });
    }
  }
  assert.deepEqual(await Promise.all(clips.map(async (c) => (await c).text())), ["a", "b", "c"]);
  // A helper that dies fails only its own sentences, and leaves the voice loaded.
  const lost = say("e", signal);
  const kept = say("f", signal);
  made[0].onerror();
  await assert.rejects(lost, /the local voice stopped/);
  made[1].onmessage({ data: { seq: made[1].posted.at(-1).seq, wav: enc("f") } });
  assert.equal(await (await kept).text(), "f");
  made[1].onerror();
  // On WebGPU one worker says them all; where WebGPU failed, the page is told why.
  lv.warmLocal(id);
  made.at(-1).onmessage({ data: { kind: "ready", id, gpu: true } });
  assert.deepEqual(lv.loadStateOf(id), { pct: 100, ready: true, gpu: true });
  assert.equal(lv.lanes(id), 1);
  made.at(-1).onmessage({ data: { kind: "ready", id, gpuError: "no adapter" } });
  assert.deepEqual(lv.loadStateOf(id), { pct: 100, ready: true, gpuError: "no adapter" }, "why WebGPU failed");
  for (const w of made.slice(-2)) {
    w.onerror();
  }
  delete globalThis.navigator.hardwareConcurrency;
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
  localStorage.removeItem(lv.FILTERS_KEY);
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
  // The list opens on the interface's language; the filters widen or narrow it.
  const pick = async (s, value) => {
    q(s).value = value;
    await act(async () => q(s).dispatchEvent(new window.Event("change", { bubbles: true })));
  };
  assert.equal(q("local-voice-es"), null, "another language is filtered out");
  assert.deepEqual([...q("local-voice-language").options].map((o) => o.value), ["", "en", "es"]);
  await pick("local-voice-quality", "high");
  assert.equal(q("local-voice-en"), null);
  assert.match(q("local-voices").textContent, /No voice matches these filters/);
  await pick("local-voice-quality", "");
  await pick("local-voice-language", "");
  assert.ok(q("local-voice-es"));
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

test("a voice is heard before it is downloaded, one sample at a time, and its engine is named", async () => {
  const played = [];
  globalThis.Audio = class {
    constructor(src) {
      this.src = src;
      played.push(this);
    }
    play() {
      this.paused = false;
      return Promise.resolve();
    }
    pause() {
      this.paused = true;
    }
  };
  const row = (id, extra) => ({ id, name: id, lang: "en-US", quality: "high", size: 1, installed: false, ...extra });
  const { api, calls } = apiOf({
    "GET /v1/voice/models": [
      200,
      {
        runtime: { size: 1, installed: true },
        models: [
          row("a", { sample: true, engine: "piper" }),
          row("b", { sample: true, engine: "piper", quality: "x-low" }),
          row("supertonic-F1", { engine: "supertonic" }),
        ],
        download: null,
      },
    ],
    "GET /v1/voice/models/a/sample": () => new Response(new Blob(["mp3"], { type: "audio/mpeg" }), { status: 200 }),
    "GET /v1/voice/models/b/sample": [502, { error: "the sample could not be downloaded" }],
  });
  const v = await mount(h(lv.LocalVoices, { api, chosen: "", choose: () => {} }));
  const q = (s) => v.root.querySelector(`[data-testid=${s}]`);
  assert.equal(q("local-voice-sample-supertonic-F1"), null, "no sample, no button");
  assert.match(q("local-voice-supertonic-F1").textContent, /Supertonic · High quality/);
  assert.match(q("local-voice-b").textContent, /Very low quality/);
  await act(async () => q("local-voice-sample-a").click());
  await settle();
  assert.ok(calls.includes("GET /v1/voice/models/a/sample"));
  assert.equal(q("local-voice-sample-a").getAttribute("aria-pressed"), "true");
  assert.equal(q("local-voice-sample-a").textContent, "■");
  // Its end frees the button.
  await act(async () => played[0].onended());
  assert.equal(q("local-voice-sample-a").getAttribute("aria-pressed"), "false");
  // Playing again, then pressing it once more, stops it.
  await act(async () => q("local-voice-sample-a").click());
  await settle();
  await act(async () => q("local-voice-sample-a").click());
  assert.equal(played[1].paused, true);
  assert.equal(q("local-voice-sample-a").getAttribute("aria-pressed"), "false");
  // A late end of a sample stopped already changes nothing.
  await act(async () => played[1].onended());
  // Another sample stops the one playing; one that fails says why.
  await act(async () => q("local-voice-sample-a").click());
  await settle();
  await act(async () => q("local-voice-sample-b").click());
  await settle();
  assert.equal(played[2].paused, true);
  assert.match(v.root.textContent, /The sample could not be played: the sample could not be downloaded/);
  assert.equal(q("local-voice-sample-b").getAttribute("aria-pressed"), "false");
  v.done();
  delete globalThis.Audio;
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
    pitch: 1,
    expressiveness: 1,
    variation: 1,
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
  // Each choice says where its voice comes from; the chosen one stands out.
  const about = [...v.root.querySelectorAll("[data-testid=settings-speech-engine-about] li")];
  assert.deepEqual(about.map((li) => li.textContent.split(":")[0]), ["System voices", "Downloaded voice"]);
  assert.match(about[1].className, /text-/);
  assert.equal(about[0].className, "");
  // A downloaded voice has its intonation and rhythm on sliders, each back to normal with a button.
  assert.equal(v.root.querySelector("[data-testid=settings-speech-pitch]"), null, "the pitch is the device's");
  const tune = v.root.querySelector("[data-testid=settings-speech-expressiveness]");
  tune.value = "0.4";
  await act(async () => tune.dispatchEvent(new window.Event("input", { bubbles: true })));
  assert.equal(saved.pop().expressiveness, 0.4);
  v.done();
  v = await mount(h(lv.SpeechSettings, props({ ...prefs, engine: "local", variation: 1.6 }, { local: true, api })));
  await act(async () => v.root.querySelector("[data-testid=settings-speech-variation-reset]").click());
  assert.equal(saved.pop().variation, 1);
  v.done();
  // A Supertonic voice has its pace and its refinement steps instead.
  const st = { ...prefs, engine: "local", localVoice: "supertonic-F1", supertonicSpeed: 1.05, supertonicSteps: 0 };
  v = await mount(h(lv.SpeechSettings, props(st, { local: true, api })));
  assert.equal(v.root.querySelector("[data-testid=settings-speech-expressiveness]"), null);
  const q = (s) => v.root.querySelector(`[data-testid=${s}]`);
  assert.equal(q("settings-speech-supertonic-speed-reset").disabled, true, "at its own pace");
  assert.match(q("settings-speech-supertonic-steps").parentElement.textContent, /Auto/);
  const speed = q("settings-speech-supertonic-speed");
  speed.value = "1.3";
  await act(async () => speed.dispatchEvent(new window.Event("input", { bubbles: true })));
  assert.equal(saved.pop().supertonicSpeed, 1.3);
  const steps = q("settings-speech-supertonic-steps");
  steps.value = "12";
  await act(async () => steps.dispatchEvent(new window.Event("input", { bubbles: true })));
  assert.equal(saved.pop().supertonicSteps, 12);
  v.done();
  v = await mount(h(lv.SpeechSettings, props({ ...st, supertonicSpeed: 1.4, supertonicSteps: 12 }, { local: true, api })));
  assert.match(q("settings-speech-supertonic-steps").parentElement.textContent, /12/);
  await act(async () => q("settings-speech-supertonic-speed-reset").click());
  assert.equal(saved.pop().supertonicSpeed, 1.05);
  await act(async () => q("settings-speech-supertonic-steps-reset").click());
  assert.equal(saved.pop().supertonicSteps, 0);
  v.done();
  // A Chatterbox voice has none here: its emotion is with its voices.
  v = await mount(h(lv.SpeechSettings, props({ ...prefs, engine: "local", localVoice: "chatterbox-default" }, { local: true, api })));
  assert.equal(v.root.querySelector("[data-testid=settings-speech-expressiveness]"), null);
  assert.equal(v.root.querySelector("[data-testid=settings-speech-supertonic-speed]"), null);
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

test("the voice in use shows its loading into memory, and says In use only when it is ready", async () => {
  // The worker of an earlier test goes, so this one is made with the stand-in below.
  globalThis.lastWorker?.onerror();
  const posted = [];
  globalThis.Worker = class {
    constructor() {
      globalThis.lastWorker = this;
    }
    postMessage(m) {
      posted.push(m);
    }
  };
  globalThis.caches = {};
  const cat = {
    runtime: { size: 1, installed: true },
    models: [{ id: "mx", name: "MX", lang: "es-MX", quality: "high", size: 1, installed: true }],
    download: null,
  };
  const { api } = apiOf({ "GET /v1/voice/models": [200, cat] });
  const v = await mount(h(lv.LocalVoices, { api, chosen: "mx", choose: () => {} }));
  const q = (s) => v.root.querySelector(`[data-testid=${s}]`);
  assert.deepEqual(posted, [{ kind: "warm", id: "mx" }], "loaded as soon as it is shown");
  assert.match(q("local-voice-loading-mx").textContent, /0/);
  assert.equal(q("local-voice-use-mx"), null, "not In use while it loads");
  const tell = async (data) => {
    await act(async () => globalThis.lastWorker.onmessage({ data }));
  };
  await tell({ kind: "progress", id: "mx", pct: 45 });
  assert.match(q("local-voice-loading-mx").textContent, /45/);
  await tell({ kind: "failed", id: "mx", error: "out of memory" });
  assert.match(v.root.textContent, /out of memory/);
  await act(async () => q("local-voice-retry-mx").click());
  assert.deepEqual(posted.at(-1), { kind: "warm", id: "mx" });
  await tell({ kind: "ready", id: "mx" });
  assert.equal(q("local-voice-loading-mx"), null);
  assert.equal(q("local-voice-use-mx").getAttribute("aria-pressed"), "true");
  v.done();
  globalThis.lastWorker.onerror();
  delete globalThis.Worker;
  delete globalThis.caches;
});

test("the filters chosen are kept for the next visit, and what cannot be read is left out", async () => {
  localStorage.setItem(lv.FILTERS_KEY, JSON.stringify({ language: "en", quality: "high" }));
  const cat = {
    runtime: { size: 1, installed: true },
    models: [
      { id: "en", name: "EN", lang: "en-US", quality: "high", size: 1, installed: false },
      { id: "es", name: "ES", lang: "es-MX", quality: "high", size: 1, installed: false },
    ],
    download: null,
  };
  const { api } = apiOf({ "GET /v1/voice/models": [HTTP_OK, cat] });
  const v = await mount(h(lv.LocalVoices, { api, chosen: "x", choose: () => {} }));
  const q = (s) => v.root.querySelector(`[data-testid=${s}]`);
  assert.equal(q("local-voice-language").value, "en");
  assert.equal(q("local-voice-quality").value, "high");
  assert.equal(q("local-voice-es"), null);
  q("local-voice-quality").value = "medium";
  await act(async () => q("local-voice-quality").dispatchEvent(new window.Event("change", { bubbles: true })));
  assert.deepEqual(JSON.parse(localStorage.getItem(lv.FILTERS_KEY)), { language: "en", quality: "medium" });
  v.done();
  localStorage.setItem(lv.FILTERS_KEY, JSON.stringify({ language: 3, quality: "loud" }));
  assert.deepEqual(lv.loadFilters(), { language: "en", quality: "" }, "the interface's language, every quality");
  localStorage.setItem(lv.FILTERS_KEY, "{not json");
  assert.deepEqual(lv.loadFilters(), { language: "en", quality: "" });
  localStorage.removeItem(lv.FILTERS_KEY);
});

test("a Supertonic voice says where it runs, and why WebGPU could not start when it failed", async () => {
  globalThis.lastWorker?.onerror();
  globalThis.Worker = class {
    constructor() {
      globalThis.lastWorker = this;
    }
    postMessage() {}
  };
  globalThis.caches = {};
  const cat = {
    runtime: { size: 1, installed: true },
    models: [{ id: "supertonic-F1", name: "F1", lang: "mul", quality: "high", engine: "supertonic", size: 1, installed: true }],
    download: null,
  };
  const { api } = apiOf({ "GET /v1/voice/models": [HTTP_OK, cat] });
  const v = await mount(h(lv.LocalVoices, { api, chosen: "supertonic-F1", choose: () => {} }));
  const q = (s) => v.root.querySelector(`[data-testid=${s}]`);
  const tell = (data) => act(async () => globalThis.lastWorker.onmessage({ data }));
  await tell({ kind: "ready", id: "supertonic-F1", gpu: true });
  assert.match(q("local-voice-supertonic-F1").textContent, /WebGPU/);
  assert.equal(q("local-voice-gpu-note"), null);
  await tell({ kind: "ready", id: "supertonic-F1", gpuError: "the browser gave no graphics card to use" });
  assert.doesNotMatch(q("local-voice-supertonic-F1").textContent, /WebGPU/);
  assert.match(q("local-voice-gpu-note").textContent, /could not start here \(the browser gave no graphics card to use\)/);
  v.done();
  globalThis.lastWorker.onerror();
  delete globalThis.Worker;
  delete globalThis.caches;
});

test("a sentence is written as Supertonic reads it, and its voice and length are read", () => {
  assert.equal(core.isSupertonic("supertonic-F1"), true);
  assert.equal(core.isSupertonic("chatterbox-default"), false);
  assert.equal(core.supertonicText("Hola — “qué” tal 😀   mundo", "es-CL"), '<es>Hola - "qué" tal mundo.</es>'.normalize("NFKD"));
  assert.equal(core.supertonicText("Hi , you [there]!", "en"), "<en>Hi, you there!</en>");
  assert.equal(core.supertonicText("Salut", "xx"), "<na>Salut.</na>", "a language it lacks is its own neutral one");
  assert.deepEqual(core.supertonicIds("ab€", Array(97).fill(0).concat([7, 8])), [7, 8, -1]);
  const st = core.supertonicStyle(JSON.stringify({ style_ttl: { data: [[[1, 2]]], dims: [1, 1, 2] }, style_dp: { data: [[[3]]], dims: [1, 1, 1] } }));
  assert.deepEqual([...st.ttl], [1, 2]);
  assert.deepEqual(st.dpDims, [1, 1, 1]);
  assert.throws(() => core.supertonicStyle("{}"), /not a Supertonic voice/);
  assert.equal(core.supertonicLatent(0), 1, "never empty");
  assert.equal(core.supertonicLatent(1), Math.ceil(44100 / 3072));
  let r = 0;
  const half = core.noise(2, () => [0.5, 0, 0.5, 0.5][r++]);
  assert.ok(Math.abs(half[0] - Math.sqrt(-2 * Math.log(0.5))) < 1e-6);
  assert.ok(half[1] < 0, "cos of half a turn");
  assert.equal(core.noise(3).length, 3);
});

test("a Supertonic voice speaks on its shared model, refined step by step, in the sentence's language", async () => {
  const { deps, log } = fakes();
  const synth = new core.Synth(deps);
  const seen = [];
  await synth.warm("supertonic-F1", (p) => seen.push(p));
  assert.equal(seen.at(-1), 100);
  assert.equal(log.phon, 0, "no phonemizer");
  assert.deepEqual(
    log.bytes.filter((u) => !u.includes("/runtime/")),
    [
      "/v1/voice/models/supertonic-F1/voice.json",
      ...core.SUPERTONIC_PARTS.map((n) => "/v1/voice/models/supertonic/" + n),
      "/v1/voice/models/supertonic/unicode_indexer.json",
    ],
  );
  assert.equal(log.sessions, 4);
  const wav = await synth.say("supertonic-F1", "ab", { noise: 1, noiseW: 1, lang: "es" });
  assert.equal(new DataView(wav).getUint32(24, true), core.SUPERTONIC_RATE);
  const [dp, te, ...rest] = log.feeds;
  const text = [..."<es>ab.</es>"].map((c) => BigInt(c.codePointAt(0) + 1));
  assert.deepEqual([...dp.text_ids.data], text);
  assert.deepEqual([...te.style_ttl.data], [1, 2]);
  assert.equal(rest.length, core.SUPERTONIC_STEPS.cpu + 1, "the steps, then the waveform");
  assert.deepEqual(rest[0].noisy_latent.dims, [1, 144, 2], "0.1 s at its pace is two chunks");
  assert.deepEqual([...rest[2].current_step.data], [2]);
  assert.equal(rest[1].noisy_latent.data[0], rest[0].noisy_latent.data[0] / 2, "each step starts from the last");
  assert.equal(await synth.onGPU("supertonic-F1"), false);
  // Without a language it is told English.
  await synth.say("supertonic-F1", "ab");
  assert.equal(log.feeds.at(-core.SUPERTONIC_STEPS.cpu - 3).text_ids.data[1], BigInt("e".codePointAt(0) + 1));
});

test("a Supertonic voice speaks at the pace and with the steps asked for", async () => {
  const { deps, log } = fakes();
  const synth = new core.Synth(deps);
  // Slower is longer: 0.1 s at half its pace is three chunks; three steps are three runs.
  await synth.say("supertonic-F1", "ab", { noise: 1, noiseW: 1, speed: 0.5, steps: 3 });
  const steps = log.feeds.filter((f) => f.noisy_latent);
  assert.equal(steps.length, 3);
  assert.deepEqual(steps[0].noisy_latent.dims, [1, 144, 3]);
  assert.deepEqual([...steps[0].total_step.data], [3]);
  // Steps of 0 are its own for where it runs.
  log.feeds.length = 0;
  await synth.say("supertonic-F1", "ab", { noise: 1, noiseW: 1, speed: core.SUPERTONIC_SPEED, steps: 0 });
  assert.equal(log.feeds.filter((f) => f.noisy_latent).length, core.SUPERTONIC_STEPS.cpu);
});

test("Supertonic runs on WebGPU where the browser has it, and on the CPU when that fails", async () => {
  const { deps, log } = gpuFakes();
  deps.gpu = ((gpu) => async (wasm) => {
    assert.equal(new TextDecoder().decode(wasm), "/v1/voice/models/supertonic/ort-wasm-simd-threaded.asyncify.wasm");
    return gpu(wasm);
  })(deps.gpu);
  const synth = new core.Synth(deps);
  await synth.warm("supertonic-M1");
  assert.ok(!log.bytes.some((u) => u.includes("/runtime/")), "nothing of the CPU's");
  assert.equal(await synth.onGPU("supertonic-M1"), true);
  await synth.say("supertonic-M1", "a", { noise: 1, noiseW: 1, lang: "en" });
  assert.equal(log.feeds.length, 2 + core.SUPERTONIC_STEPS.gpu + 1);
  assert.equal(synth.gpuError(), "");
  const failing = gpuFakes({ fail: true });
  const cpu = new core.Synth(failing.deps);
  await cpu.say("supertonic-M1", "a");
  assert.equal(await cpu.onGPU("supertonic-M1"), false);
  assert.equal(failing.log.ort, 1, "the CPU's runtime said it");
  assert.equal(cpu.gpuError(), "no WebGPU here", "and the page is told why");
  assert.equal(await cpu.onGPU("v1"), false, "a Piper voice runs on the processor");
});

// Chatterbox's stand-ins: a vocabulary of a few letters, and a voice of two feature frames.
const CB_VOCAB = 6564;
const CB_STOP = 6562;
function choose3(n) {
  if (n <= 2) {
    return 7;
  }
  return CB_STOP;
}
const CB_TOKENIZER = {
  model: { vocab: { "[UNK]": 1, "[SPACE]": 2, h: 3, o: 4, l: 5, a: 6, ".": 7, ho: 8 }, merges: ["h o"] },
  added_tokens: [
    { id: 2, content: "[SPACE]" },
    { id: 9, content: "[es]" },
    { id: 10, content: "[en]" },
    { id: 11, content: "[laughter]" },
  ],
  post_processor: {
    special_tokens: {
      EXAGGERATION: { ids: [6563] },
      BOS: { ids: [255] },
      EOS: { ids: [0] },
      START_SPEECH: { ids: [6561] },
    },
  },
};
function cbVoice() {
  const floats = new Float32Array(2 * 1024 + 3 + 192 + 80).fill(0.5);
  floats.set([11, 12, 13], 2 * 1024);
  const out = new Uint8Array(12 + floats.byteLength);
  out.set(new Uint8Array(Int32Array.from([2, 3, 1]).buffer));
  out.set(new Uint8Array(floats.buffer), 12);
  return out.buffer;
}

test("a sentence is written as Chatterbox reads it, and split into its tokens", () => {
  assert.equal(core.isChatterbox("chatterbox-default"), true);
  assert.equal(core.isChatterbox("supertonic-F1"), false);
  assert.equal(core.chatterboxText("  Hola…  qué  tal: “bien”; ¿Sí?", "es-CL"), '[es]hola,  qué tal, "bien",  ¿sí?'.normalize("NFKD"));
  assert.equal(core.chatterboxText("Done — mostly", "en"), "[en]done - mostly.");
  assert.equal(core.chatterboxText("Ni hao", "zh"), "[en]ni hao.", "a language it cannot read alone is read as English");
  const tk = core.chatterboxTokenizer(JSON.stringify(CB_TOKENIZER));
  assert.deepEqual(core.chatterboxTokens(tk, "[es]hola [laughter]hal?"), [6563, 255, 9, 8, 5, 6, 2, 11, 3, 6, 5, 1, 0, 6561, 6561]);
  assert.throws(() => core.chatterboxTokenizer("{}"), /not Chatterbox's tokenizer/);
  assert.throws(() => core.chatterboxTokenizer(JSON.stringify({ ...CB_TOKENIZER, post_processor: {} })), /not Chatterbox's/);
  const v = core.chatterboxVoice(cbVoice());
  assert.equal(v.featureLen, 2);
  assert.deepEqual(v.tokens, [11, 12, 13]);
  assert.equal(v.embedding.length, 192);
  assert.equal(v.speaker.length, 80);
  assert.throws(() => core.chatterboxVoice(new ArrayBuffer(16)), /not a Chatterbox voice/);
  assert.throws(() => core.chatterboxVoice(new ArrayBuffer(4)), /not a Chatterbox voice/);
  // A token already said is less likely: a positive score divided, a negative one multiplied.
  assert.equal(core.nextToken(Float32Array.from([0, 1.1, 1]), [1]), 2);
  assert.equal(core.nextToken(Float32Array.from([-1, -1.1, -2]), [0]), 1);
});

test("a Chatterbox voice speaks on WebGPU: the voice, then the text, then one sound token at a time", async () => {
  const { deps, log } = gpuFakes();
  const weights = [];
  const ort = deps.gpu;
  deps.gpu = async (w) => {
    const o = await ort(w);
    return { ...o, createSession: (m, x) => (weights.push(x?.path), o.createSession(m)) };
  };
  const synth = new core.Synth(deps);
  await synth.warm("chatterbox-default");
  assert.equal(log.phon, 0, "no phonemizer");
  assert.ok(!log.bytes.some((u) => u.includes("/runtime/")), "nothing of the CPU's");
  assert.deepEqual(weights, core.CHATTERBOX_PARTS.map((p) => p + "_data"), "each part with its weights");
  assert.equal(await synth.onGPU("chatterbox-default"), true);
  const wav = await synth.say("chatterbox-default", "Hola", { noise: 1, noiseW: 1, lang: "es", exaggeration: 1.5 });
  assert.equal(new DataView(wav).getUint32(24, true), core.CHATTERBOX_RATE);
  const embeds = log.feeds.filter((f) => f.exaggeration);
  assert.deepEqual([...embeds[0].input_ids.data].map(Number), [6563, 255, 9, 8, 5, 6, 7, 0, 6561, 6561]);
  assert.deepEqual([...embeds[0].position_ids.data].map(Number), [0, 0, 1, 2, 3, 4, 5, 6, 0, 0]);
  assert.deepEqual([...embeds[0].exaggeration.data], [1.5]);
  assert.deepEqual([...embeds[1].position_ids.data].map(Number), [1]);
  const lm = log.feeds.filter((f) => f.inputs_embeds);
  assert.deepEqual(lm[0].inputs_embeds.dims, [1, 12, 1024], "the voice's two frames before the text");
  assert.equal(lm[0].inputs_embeds.data[0], 0.5);
  assert.deepEqual(lm[0]["past_key_values.0.key"].dims, [1, 16, 0, 64]);
  assert.equal(lm[1]["past_key_values.29.value"].data, "kv1", "each step remembers the last");
  assert.equal(lm[2].attention_mask.dims[1], 14);
  const dec = log.feeds.at(-1);
  assert.deepEqual([...dec.speech_tokens.data].map(Number), [11, 12, 13, 7, 7], "the voice's own, then the new");
  assert.deepEqual(dec.speaker_features.dims, [1, 1, 80]);
  // Without a language or an emotion, English at its default.
  await synth.say("chatterbox-default", "a");
  assert.deepEqual([...log.feeds.filter((f) => f.exaggeration).at(-1).exaggeration.data], [core.CHATTERBOX_EMOTION]);
});

test("Chatterbox does not run without WebGPU, and its sentences stop at the most tokens", async () => {
  const plain = fakes();
  const cpu = new core.Synth(plain.deps);
  await assert.rejects(cpu.say("chatterbox-default", "a"), /needs WebGPU/);
  const { deps, log } = gpuFakes();
  const synth = new core.Synth(deps);
  log.lm = -1e9;
  await synth.say("chatterbox-default", "a");
  assert.equal(log.feeds.filter((f) => f.inputs_embeds).length, core.CHATTERBOX_MAX_TOKENS);
});

test("Supertonic speaks any language of the filter, and Chatterbox is offered on WebGPU with its emotion", async () => {
  globalThis.lastWorker?.onerror();
  globalThis.Worker = class {
    constructor() {
      globalThis.lastWorker = this;
    }
    postMessage() {}
  };
  globalThis.caches = {};
  localStorage.setItem(lv.FILTERS_KEY, JSON.stringify({ language: "en", quality: "" }));
  const many = (id, engine, installed) => ({ id, name: id, lang: "mul", quality: "high", engine, size: 200, installed });
  const cat = {
    runtime: { size: 1, installed: true },
    models: [many("supertonic-F1", "supertonic", false), many("chatterbox-default", "chatterbox", true)],
    download: null,
  };
  const { api } = apiOf({ "GET /v1/voice/models": [HTTP_OK, cat] });
  const emotions = [];
  const draw = (chosen) =>
    h(lv.LocalVoices, { api, chosen, choose: () => {}, emotion: 0.5, setEmotion: (n) => emotions.push(n) });
  let v = await mount(draw("supertonic-F1"));
  let q = (s) => v.root.querySelector(`[data-testid=${s}]`);
  assert.match(q("local-voice-supertonic-F1").textContent, /31 languages.*Supertonic · .*1 MB/);
  assert.equal(q("local-voice-chatterbox-default"), null, "no WebGPU, no Chatterbox");
  assert.equal(q("local-voice-emotion"), null);
  v.done();
  Object.defineProperty(globalThis.navigator, "gpu", { value: { requestAdapter: async () => ({}) }, configurable: true });
  v = await mount(draw("chatterbox-default"));
  q = (s) => v.root.querySelector(`[data-testid=${s}]`);
  assert.match(q("local-voice-chatterbox-default").textContent, /21 languages.*Chatterbox · /);
  q("local-voice-emotion-amount").value = "1.25";
  await act(async () => q("local-voice-emotion-amount").dispatchEvent(new window.Event("input", { bubbles: true })));
  assert.deepEqual(emotions, [1.25]);
  v.done();
  delete globalThis.navigator.gpu;
  localStorage.removeItem(lv.FILTERS_KEY);
  globalThis.lastWorker.onerror();
  delete globalThis.Worker;
  delete globalThis.caches;
});
