// Local voices: a Piper or Kokoro model run in the browser. The synthesis with a stand-in onnxruntime and
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
const KOKORO_VOCAB = { " ": 16, ".": 4, h: 50, l: 54, o: 57, a: 43, k: 53, j: 52, "ɹ": 123, "ˈ": 156 };

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
  assert.deepEqual(core.phonemized('{"phoneme_ids":[1,0,2],"phonemes":["ˈ","o"]}'), { ids: [1, 0, 2], phonemes: "ˈo" });
  assert.deepEqual(core.phonemized('{"phoneme_ids":[1]}'), { ids: [1], phonemes: "" });
  assert.throws(() => core.phonemized('{"phoneme_ids":[]}'), /answered nothing/);
  assert.equal(core.fileURL("a b", "model.onnx"), "/v1/voice/models/a%20b/model.onnx");
});

test("a Kokoro voice is known by its id, and its phonemes become the ids and the style it takes", () => {
  assert.equal(core.isKokoro("kokoro-ef_dora"), true);
  assert.equal(core.isKokoro("es_MX-claude-high"), false);
  assert.deepEqual(
    ["kokoro-af_heart", "kokoro-bm_george", "kokoro-ef_dora", "kokoro-zz"].map(core.kokoroEspeak),
    ["en-us", "en-gb-x-rp", "es", "en-us"],
  );
  const vocab = core.kokoroVocab(JSON.stringify({ model: { vocab: KOKORO_VOCAB } }));
  assert.equal(vocab.get("ˈ"), 156);
  assert.throws(() => core.kokoroVocab("{}"), /not Kokoro's tokenizer/);
  // r and x become the sounds Kokoro knows, a sentence that ends is followed by a space, what the
  // table lacks is dropped, and the whole is padded.
  assert.deepEqual(core.kokoroTokens("hˈola.rxʔ", vocab), [0, 50, 156, 57, 54, 43, 4, 16, 123, 53, 0]);
  assert.deepEqual(core.kokoroTokens("kəkˈoːɹoʊ ʲ", vocab).slice(0, 4), [0, 53, 156, 57]);
  assert.equal(core.kokoroTokens("a".repeat(600), vocab).length, 510, "cut to what the model takes");
  const styles = Float32Array.from({ length: 510 * 256 }, (_, i) => Math.floor(i / 256));
  assert.deepEqual([...core.kokoroStyle(styles, 7)].slice(0, 2), [5, 5], "one row per token, without the pads");
  assert.equal(core.kokoroStyle(styles, 1)[0], 0);
  assert.equal(core.kokoroStyle(styles, 9999)[0], 509);
  assert.equal(core.kokoroStyle(styles, 9999).length, 256);
});

test("Kokoro voices share one model, loaded once, and speak at its rate with their own style", async () => {
  const { deps, log } = fakes();
  const synth = new core.Synth(deps);
  const seen = [];
  await synth.warm("kokoro-ef_dora", (p) => seen.push(p));
  assert.equal(seen.at(-1), 100);
  assert.deepEqual(
    log.bytes.filter((u) => !u.includes("/runtime/")),
    ["/v1/voice/models/kokoro-ef_dora/voice.bin", "/v1/voice/models/kokoro/tokenizer.json", "/v1/voice/models/kokoro/model.onnx"],
  );
  const wav = await synth.say("kokoro-ef_dora", "Hola. Rx", { noise: 0, noiseW: 0 });
  assert.equal(new DataView(wav).getUint32(24, true), core.KOKORO_RATE);
  assert.equal(log.args[0][1], "es");
  const f = log.feeds[0];
  assert.deepEqual([...f.input_ids.data], [0n, 50n, 156n, 57n, 54n, 43n, 4n, 16n, 123n, 53n, 0n]);
  assert.deepEqual(f.input_ids.dims, [1, 11]);
  assert.equal(f.style.data[0], 9, "the style for nine phonemes");
  assert.deepEqual([...f.speed.data], [1], "the page sets the speed");
  // A second voice reads only its own styles.
  await synth.say("kokoro-af_heart", "Hi");
  assert.equal(log.sessions, 1);
  assert.equal(log.args[1][1], "en-us");
  assert.equal(log.bytes.filter((u) => u.endsWith("model.onnx")).length, 1);
  await synth.warm("kokoro-af_heart");
});

test("a Kokoro model that failed to load is loaded again next time", async () => {
  let fail = true;
  const { deps } = fakes();
  const synth = new core.Synth({
    ...deps,
    bytes: async (url, p) => {
      if (url.endsWith("tokenizer.json") && fail) {
        fail = false;
        return enc("{}");
      }
      return deps.bytes(url, p);
    },
  });
  await assert.rejects(synth.say("kokoro-ef_dora", "x"), /not Kokoro's tokenizer/);
  await synth.say("kokoro-ef_dora", "x");
});

// gpuFakes are fakes with WebGPU's build too, which fail when told to.
function gpuFakes({ fail = false } = {}) {
  const { deps, log } = fakes();
  log.gpu = 0;
  deps.gpu = async (wasm) => {
    log.gpu++;
    assert.equal(new TextDecoder().decode(wasm), "/v1/voice/models/accel-kokoro/ort-wasm-simd-threaded.asyncify.wasm");
    if (fail) {
      throw new Error("no WebGPU here");
    }
    const ort = await deps.ort(enc("/v1/voice/models/runtime/ort-wasm-simd-threaded.wasm"));
    log.ort--;
    return ort;
  };
  return { deps, log };
}

test("Kokoro runs on WebGPU when asked and its files are there, and on the CPU otherwise", async () => {
  const { deps, log } = gpuFakes();
  const synth = new core.Synth(deps);
  synth.setGPU(true);
  await synth.warm("kokoro-ef_dora");
  const kokoro = log.bytes.filter((u) => u.includes("kokoro"));
  assert.ok(kokoro.includes("/v1/voice/models/accel-kokoro/model.onnx"), "WebGPU's full model");
  assert.ok(!kokoro.includes("/v1/voice/models/kokoro/model.onnx"), "not the CPU's");
  assert.equal(await synth.onGPU(), true);
  await synth.say("kokoro-ef_dora", "Hola");
  assert.equal(log.gpu, 1);
  synth.setGPU(true);
  assert.equal(await synth.onGPU(), true, "the same choice keeps the model");
  // Turned off, the CPU's model is loaded for the next sentence.
  synth.setGPU(false);
  assert.equal(await synth.onGPU(), false);
  await synth.say("kokoro-ef_dora", "Hola");
  assert.ok(log.bytes.includes("/v1/voice/models/kokoro/model.onnx"));
  assert.equal(log.gpu, 1);
  // A browser without the WebGPU build runs Kokoro on the CPU even when asked.
  const plain = fakes();
  const cpu = new core.Synth(plain.deps);
  cpu.setGPU(true);
  await cpu.say("kokoro-ef_dora", "Hola");
  assert.equal(await cpu.onGPU(), false);
  assert.ok(!plain.log.bytes.some((u) => u.includes("accel-kokoro")));
});

test("Kokoro falls back to the CPU when WebGPU fails, and tries it again only when asked anew", async () => {
  const { deps, log } = gpuFakes({ fail: true });
  const synth = new core.Synth(deps);
  synth.setGPU(true);
  const wav = await synth.say("kokoro-ef_dora", "Hola");
  assert.equal(new DataView(wav).getUint32(24, true), core.KOKORO_RATE, "it still speaks");
  assert.equal(await synth.onGPU(), false);
  assert.equal(log.gpu, 1);
  // The next load skips WebGPU: warming lists the CPU's files.
  synth.setGPU(false);
  synth.setGPU(true);
  await synth.warm("kokoro-af_heart");
  assert.equal(log.gpu, 2, "asked anew, it is tried again");
});

test("a Kokoro voice mixed with another speaks with a blend of both styles", async () => {
  assert.deepEqual([...core.blendStyles(Float32Array.from([0, 2]), Float32Array.from([2, 4]), 0.25)], [0.5, 2.5]);
  const { deps, log } = fakes();
  const synth = new core.Synth({
    ...deps,
    bytes: async (url, p) => {
      if (url.includes("kokoro-af_bella")) {
        return new Float32Array(510 * 256).buffer;
      }
      if (url.includes("kokoro-gone")) {
        throw new Error("HTTP 404 " + url);
      }
      return deps.bytes(url, p);
    },
  });
  const mixed = (id, amount) => ({ noise: 1, noiseW: 1, mix: { id, amount } });
  await synth.say("kokoro-ef_dora", "Hola. Rx", mixed("kokoro-af_bella", 0.5));
  assert.equal(log.feeds.at(-1).style.data[0], 4.5, "halfway between row 9 and a silent voice");
  // Nothing to mix: none at all, itself, a Piper voice, or one that cannot be loaded.
  for (const tune of [mixed("kokoro-af_bella", 0), mixed("kokoro-ef_dora", 1), mixed("v1", 1), mixed("kokoro-gone", 1)]) {
    await synth.say("kokoro-ef_dora", "Hola. Rx", tune);
    assert.equal(log.feeds.at(-1).style.data[0], 9);
  }
});

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
      if (url.endsWith("tokenizer.json")) {
        return enc(JSON.stringify({ model: { vocab: KOKORO_VOCAB } }));
      }
      if (url.endsWith("voice.bin")) {
        // Each style vector holds its own row number.
        return Float32Array.from({ length: 510 * 256 }, (_, i) => Math.floor(i / 256)).buffer;
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
              return { output: { data }, waveform: { data } };
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
  assert.deepEqual(deleted, ["/v1/voice/models/a/model.onnx", "/v1/voice/models/a/model.json", "/v1/voice/models/a/voice.bin"]);
  // Kokoro's model goes from the page's copy with its last voice, and stays while one is left.
  const k = (id, installed) => ({ id, engine: "kokoro", installed });
  const kokoro = apiOf({
    "DELETE /v1/voice/models/kokoro-a": () => new Response(null, { status: 204 }),
    "GET /v1/voice/models": () => new Response(JSON.stringify({ ...cat, models: left }), { status: 200 }),
  });
  let left = [k("kokoro-a", false), k("kokoro-b", true)];
  deleted.length = 0;
  await lv.deleteVoice(kokoro.api, "kokoro-a");
  assert.equal(deleted.length, 3);
  left = [k("kokoro-a", false), k("kokoro-b", false)];
  deleted.length = 0;
  await lv.deleteVoice(kokoro.api, "kokoro-a");
  assert.deepEqual(deleted.slice(3), ["/v1/voice/models/kokoro/model.onnx", "/v1/voice/models/kokoro/tokenizer.json"]);
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
  let changes = 0;
  const off = lv.onLoadState(() => changes++);
  lv.warmLocal("v1");
  assert.deepEqual(posted[0], { kind: "warm", id: "v1", gpu: false });
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
  assert.deepEqual(posted.shift(), { kind: "warm", id: "v1", gpu: false }, "a voice that failed is asked for again");
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
    gpu: false,
  });
  assert.equal(say.ahead, 1, "a Piper voice says one sentence at a time");
  delete globalThis.Worker;
});

test("Kokoro on the CPU says the next sentences at once, in workers of their own", async () => {
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
  assert.equal(lv.lanes("kokoro-ef_dora"), lv.MAX_LANES);
  assert.equal(lv.lanes("v1"), 1, "a Piper voice is quick enough alone");
  cores(2);
  assert.equal(lv.lanes("kokoro-ef_dora"), 1, "one core is left to the page");
  cores(8);
  lv.warmLocal("kokoro-ef_dora");
  assert.equal(made.length, 1, "the helpers wait for the first worker");
  made[0].onmessage({ data: { kind: "ready", id: "kokoro-ef_dora" } });
  assert.equal(made.length, lv.MAX_LANES);
  assert.deepEqual(made[2].posted, [{ kind: "warm", id: "kokoro-ef_dora", gpu: false }]);
  made[1].onmessage({ data: { kind: "progress", id: "kokoro-ef_dora", pct: 5 } });
  assert.equal(lv.loadStateOf("kokoro-ef_dora").ready, true, "a helper's loading is not shown");
  // Each sentence goes to the worker with the fewest waiting.
  const say = lv.localClip("kokoro-ef_dora");
  assert.equal(say.ahead, lv.MAX_LANES);
  const signal = new AbortController().signal;
  const clips = ["a", "b", "c", "d"].map((text) => say(text, signal));
  const said = (w) => w.posted.filter((m) => m.kind === "say").map((m) => m.text);
  assert.deepEqual(made.map(said), [["a", "d"], ["b"], ["c"]]);
  for (const w of made) {
    for (const m of w.posted.filter((p) => p.kind === "say")) {
      w.onmessage({ data: { seq: m.seq, wav: enc(m.text) } });
    }
  }
  assert.deepEqual(await Promise.all(clips.map(async (c) => (await c).text())), ["a", "b", "c", "d"]);
  // A helper that dies fails only its own sentences, and leaves the voice loaded.
  const lost = say("e", signal);
  const kept = say("f", signal);
  made[0].onerror();
  await assert.rejects(lost, /the local voice stopped/);
  made[1].onmessage({ data: { seq: made[1].posted.at(-1).seq, wav: enc("f") } });
  assert.equal(await (await kept).text(), "f");
  made[2].onerror();
  made[1].onerror();
  // On WebGPU one worker says them all, and the voices load again.
  lv.setKokoroGPU(true);
  lv.setKokoroGPU(true);
  assert.equal(lv.kokoroOnGPU(), true);
  assert.equal(lv.lanes("kokoro-ef_dora"), 1);
  lv.warmLocal("kokoro-ef_dora");
  assert.deepEqual(made.at(-1).posted, [{ kind: "warm", id: "kokoro-ef_dora", gpu: true }]);
  made.at(-1).onmessage({ data: { kind: "ready", id: "kokoro-ef_dora", gpu: true } });
  assert.deepEqual(lv.loadStateOf("kokoro-ef_dora"), { pct: 100, ready: true, gpu: true });
  lv.setKokoroGPU(false);
  assert.equal(lv.loadStateOf("kokoro-ef_dora"), undefined, "loaded again on the CPU");
  made.at(-1).onerror();
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

test("a voice is heard before it is downloaded, one sample at a time, and Kokoro is named", async () => {
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
          row("kokoro-af_heart", { engine: "kokoro" }),
        ],
        download: null,
      },
    ],
    "GET /v1/voice/models/a/sample": () => new Response(new Blob(["mp3"], { type: "audio/mpeg" }), { status: 200 }),
    "GET /v1/voice/models/b/sample": [502, { error: "the sample could not be downloaded" }],
  });
  const v = await mount(h(lv.LocalVoices, { api, chosen: "", choose: () => {} }));
  const q = (s) => v.root.querySelector(`[data-testid=${s}]`);
  assert.equal(q("local-voice-sample-kokoro-af_heart"), null, "no sample, no button");
  assert.match(q("local-voice-kokoro-af_heart").textContent, /Kokoro · High quality/);
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
  // Kokoro has no such settings: its voices speak as they were trained.
  v = await mount(h(lv.SpeechSettings, props({ ...prefs, engine: "local", localVoice: "kokoro-ef_dora" }, { local: true, api })));
  assert.equal(v.root.querySelector("[data-testid=settings-speech-expressiveness]"), null);
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
  assert.deepEqual(posted, [{ kind: "warm", id: "mx", gpu: false }], "loaded as soon as it is shown");
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
  assert.deepEqual(posted.at(-1), { kind: "warm", id: "mx", gpu: false });
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

test("the graphics card is offered for Kokoro where WebGPU is, and a Kokoro voice mixes with another", async () => {
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
  const dropped = [];
  globalThis.caches = { open: async () => ({ delete: async (u) => dropped.push(u) }) };
  const kokoro = (id, installed) => ({ id, name: id, lang: "es", quality: "high", engine: "kokoro", size: 1, installed });
  const accel = (installed) => ({
    id: "accel-kokoro",
    name: "Kokoro WebGPU",
    lang: "",
    quality: "high",
    engine: "accelerator",
    size: 352e6,
    installed,
  });
  let state = {
    runtime: { size: 1, installed: true },
    models: [kokoro("kokoro-ef_dora", true), kokoro("kokoro-em_alex", true), kokoro("kokoro-zz", false), accel(false)],
    download: null,
  };
  const { api, calls } = apiOf({
    "GET /v1/voice/models": () => new Response(JSON.stringify(state), { status: HTTP_OK }),
    "POST /v1/voice/models/accel-kokoro": () => {
      state = { ...state, models: [...state.models.slice(0, 3), accel(true)] };
      return new Response("{}", { status: HTTP_ACCEPTED });
    },
    "DELETE /v1/voice/models/accel-kokoro": () => {
      state = { ...state, models: [...state.models.slice(0, 3), accel(false)] };
      return new Response(null, { status: HTTP_NO_CONTENT });
    },
    "DELETE /v1/voice/models/kokoro-em_alex": () => {
      state = { ...state, models: [state.models[0], kokoro("kokoro-em_alex", false), ...state.models.slice(2)] };
      return new Response(null, { status: HTTP_NO_CONTENT });
    },
  });
  const mixes = [];
  const draw = (mix) =>
    h(lv.LocalVoices, { api, chosen: "kokoro-ef_dora", choose: () => {}, mix, setMix: (m) => mixes.push(m) });
  // Without WebGPU it is not offered.
  let v = await mount(draw({ id: "", amount: 0.5 }));
  let q = (s) => v.root.querySelector(`[data-testid=${s}]`);
  assert.equal(q("local-voice-gpu"), null);
  assert.deepEqual([...q("local-voice-language").options].map((o) => o.value), ["", "es"], "it is no language");
  v.done();
  // Nor where WebGPU has no graphics card to use.
  let adapter = null;
  Object.defineProperty(globalThis.navigator, "gpu", {
    value: { requestAdapter: async () => adapter },
    configurable: true,
  });
  v = await mount(draw({ id: "", amount: 0.5 }));
  assert.equal(v.root.querySelector("[data-testid=local-voice-gpu]"), null);
  v.done();
  adapter = {};
  v = await mount(draw({ id: "", amount: 0.5 }));
  q = (s) => v.root.querySelector(`[data-testid=${s}]`);
  assert.match(q("local-voice-gpu-note").textContent, /352 MB/);
  assert.equal(q("local-voice-accel-kokoro"), null, "not listed as a voice");
  await act(async () => q("local-voice-gpu-get").click());
  await settle();
  assert.ok(calls.includes("POST /v1/voice/models/accel-kokoro"));
  assert.equal(lv.kokoroOnGPU(), true);
  assert.deepEqual(posted.at(-1), { kind: "warm", id: "kokoro-ef_dora", gpu: true }, "loaded again for WebGPU");
  assert.match(q("local-voice-gpu-note").textContent, /will use/);
  await act(async () => globalThis.lastWorker.onmessage({ data: { kind: "ready", id: "kokoro-ef_dora", gpu: true } }));
  assert.match(q("local-voice-gpu-note").textContent, /runs on the graphics/);
  await act(async () => globalThis.lastWorker.onmessage({ data: { kind: "ready", id: "kokoro-ef_dora" } }));
  assert.match(q("local-voice-gpu-note").textContent, /could not start/, "it fell back to the processor");
  // The voice in use mixes with the other downloaded Kokoro voices.
  assert.deepEqual([...q("local-voice-mix-voice").options].map((o) => o.value), ["", "kokoro-em_alex"]);
  assert.equal(q("local-voice-mix-amount"), null, "no amount without a voice to mix");
  q("local-voice-mix-voice").value = "kokoro-em_alex";
  await act(async () => q("local-voice-mix-voice").dispatchEvent(new window.Event("change", { bubbles: true })));
  assert.deepEqual(mixes.at(-1), { id: "kokoro-em_alex", amount: 0.5 });
  await act(async () => render(draw({ id: "kokoro-em_alex", amount: 0.5 }), v.root));
  q("local-voice-mix-amount").value = "0.3";
  await act(async () => q("local-voice-mix-amount").dispatchEvent(new window.Event("input", { bubbles: true })));
  assert.deepEqual(mixes.at(-1), { id: "kokoro-em_alex", amount: 0.3 });
  assert.match(q("local-voice-mix").textContent, /50%/);
  // Deleting the voice mixed in leaves the chosen one alone.
  await act(async () => q("local-voice-delete-kokoro-em_alex").click());
  await settle();
  assert.deepEqual(mixes.at(-1), { id: "", amount: 0.5 });
  assert.ok(q("local-voice-mix-none"), "nothing left to mix with");
  await act(async () => q("local-voice-gpu-delete").click());
  await settle();
  assert.ok(calls.includes("DELETE /v1/voice/models/accel-kokoro"));
  assert.ok(dropped.includes("/v1/voice/models/accel-kokoro/ort-wasm-simd-threaded.asyncify.wasm"), "its copy goes too");
  assert.equal(lv.kokoroOnGPU(), false);
  v.done();
  delete globalThis.navigator.gpu;
  globalThis.lastWorker.onerror();
  delete globalThis.Worker;
  delete globalThis.caches;
});
