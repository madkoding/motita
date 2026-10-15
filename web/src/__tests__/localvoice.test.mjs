// Local voices: a Piper, Supertonic or Chatterbox model run in the browser. The synthesis with a stand-in onnxruntime and
// phonemizer, the page's side of the worker and of the gateway's catalog, and the settings that
// download, choose and delete a voice (via tsx-hooks.mjs, since node cannot import TSX). How
// Supertonic and Chatterbox read and speak a sentence is in localvoiceengines.test.mjs.
import test from "node:test";
import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { readFileSync } from "node:fs";
import { JSDOM } from "jsdom";
import { load } from "./tsx-hooks.mjs";
import { CHATTERBOX, MODEL_OUTPUT, PHONEME_IDS, SAMPLE_RATE, SAMPLE_RATE_AT, VOICE, enc, fakes } from "./localvoiceFakes.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const dom = new JSDOM("<!doctype html><html><body></body></html>", {
  url: "http://localhost/",
});
for (const k of ["document", "window", "Event", "HTMLElement", "Node", "localStorage"]) {
  globalThis[k] = dom.window[k];
}

const core = await import("../localVoiceCore.ts");

// The modules the voice settings are made of, as one namespace.
const lv = {
  ...(await load("localVoice.ts")),
  ...(await load("LocalVoiceSettings.tsx")),
  ...(await load("SpeechSettings.tsx")),
};
const { h, render } = await import("preact");
const { act } = await import("preact/test-utils");

const HTTP_OK = 200;
const HTTP_ACCEPTED = 202;
const HTTP_NO_CONTENT = 204;
const HTTP_NOT_FOUND = 404;
const HTTP_CONFLICT = 409;
const HTTP_SERVER_ERROR = 500;

// A WAV file: a 44-byte header, then 16-bit samples.
const WAV_HEADER_BYTES = 44;
const BYTES_PER_SAMPLE = 2;
const WAVE_AT = 8;
const DATA_TAG_AT = 36;
const CHANNELS_AT = 22;
const BITS_PER_SAMPLE_AT = 34;
const DATA_SIZE_AT = 40;
const INT16_MAX = 32767;
// A sample beyond full scale, which the WAV clips.
const OVER_FULL_SCALE = -2;
// The text to phonemize is the fourth argument of the phonemizer's command line.
const INPUT_ARG = 3;
// The voice's noise_scale, length_scale and noise_w, in thousandths.
const NOISE_SCALE_MILLI = 667;
const LENGTH_SCALE_MILLI = 1000;
const NOISE_W_MILLI = 800;
const SCALES_MILLI = [NOISE_SCALE_MILLI, LENGTH_SCALE_MILLI, NOISE_W_MILLI];
// The same with the intonation halved (rounded as the float falls) and the rhythm doubled.
const TUNED_NOISE_MILLI = 333;
const TUNED_NOISE_W = 2;
// settle waits this many rounds for the requests in flight.
const SETTLE_ROUNDS = 3;
// A failure's reason is cut to this many characters; this one is longer.
const REASON_MAX = 200;
const LONG_REASON = 500;
// What warming a Piper voice reads: the runtime, the phonemizer's two files and the voice's two.
const PIPER_FILES = 5;
const SECOND_LAST = -2;
// The bytes of the response read whole.
const BODY_BYTES = 6;
// The page's stale copies of what the catalog no longer has.
const STALE_COPIES = 3;
// How often a voice's loading state changes while it is warmed, failed and warmed again.
const LOAD_CHANGES = 5;
const LAST_TWO = -2;
const HTTP_BAD_GATEWAY = 502;

const CATALOG = "GET /v1/voice/models";
const SUPERTONIC = "supertonic-F1";
const NO_ADAPTER = "no adapter";
const ENGINE_SUPERTONIC = "supertonic";
const ARIA_PRESSED = "aria-pressed";
const ROW_SUPERTONIC = "local-voice-supertonic-F1";
const SAMPLE_A = "local-voice-sample-a";
const LOCAL_VOICES = "local-voices";
const ROW_ES = "local-voice-es";
const LANGUAGE = "local-voice-language";
const QUALITY = "local-voice-quality";
const LOADING_MX = "local-voice-loading-mx";
const EXPRESSIVENESS_SLIDER = "[data-testid=settings-speech-expressiveness]";
const SUPERTONIC_STEPS = "settings-speech-supertonic-steps";
const DOWNLOAD_ES = "POST /v1/voice/models/es";
// What the sliders are moved to: the intonation, Supertonic's pace and steps, Chatterbox's emotion.
const EXPRESSIVENESS = 0.4;
const SUPERTONIC_PACE = 1.05;
const FASTER = 1.3;
const MORE_STEPS = 12;
const EMOTION = 1.25;

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
  assert.equal(core.reasonOf(new Error(" no adapter ")), NO_ADAPTER);
  assert.equal(core.reasonOf("plain"), "plain");
  const unsaid = new Error("unsaid");
  unsaid.message = "";
  assert.equal(core.reasonOf(unsaid), "Error");
  const long = core.reasonOf(new Error("x".repeat(LONG_REASON)));
  assert.equal(long.length, REASON_MAX);
  assert.ok(long.endsWith("…"));
});

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
  await synth.say("v1", "Tono.", { noise: 0.5, noiseW: TUNED_NOISE_W });
  assert.deepEqual(
    [...log.feeds[2].scales.data].map((x) => Math.round(x * 1000)),
    [TUNED_NOISE_MILLI, LENGTH_SCALE_MILLI, NOISE_W_MILLI * TUNED_NOISE_W],
  );
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
  assert.equal(seen.at(SECOND_LAST), before, "the files, the runtime and the phonemizer, before the model");
  assert.deepEqual([...seen].sort((a, b) => a - b), seen, "never backwards");
  assert.equal(log.bytes.length, PIPER_FILES, "the runtime, the phonemizer and the voice");
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
  assert.deepEqual(seen[0], [0, BODY_BYTES]);
  assert.deepEqual(seen.at(-1), [BODY_BYTES, BODY_BYTES]);
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
    [CATALOG]: [HTTP_OK, cat],
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
  assert.deepEqual(pruned, cached.slice(0, STALE_COPIES).map((r) => r.url));
  await lv.fetchCatalog(api);
  await new Promise((r) => setTimeout(r, 0));
  assert.equal(pruned.length, STALE_COPIES, "once a page");
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
  // A model to listen with takes its own files; WebGPU's runtime is the runtime's, and stays.
  const whisper = apiOf({ "DELETE /v1/voice/models/whisper-base-gpu": () => new Response(null, { status: HTTP_NO_CONTENT }) });
  deleted.length = 0;
  await lv.deleteVoice(whisper.api, "whisper-base-gpu");
  assert.deepEqual(
    deleted,
    ["tokenizer.json", "generation_config.json", "encoder.onnx", "decoder.onnx"].map(
      (n) => "/v1/voice/models/whisper-base-gpu/" + n,
    ),
  );
  // An engine's model goes from the page's copy with its last voice, and stays while one is left.
  const k = (id, installed) => ({ id, engine: ENGINE_SUPERTONIC, installed });
  const kept = apiOf({
    "DELETE /v1/voice/models/supertonic-F1": () => new Response(null, { status: HTTP_NO_CONTENT }),
    [CATALOG]: () => new Response(JSON.stringify({ ...cat, models: [k(SUPERTONIC, false), k("supertonic-F2", true)] }), { status: HTTP_OK }),
  });
  deleted.length = 0;
  await lv.deleteVoice(kept.api, SUPERTONIC);
  assert.equal(deleted.length, 4);
  // Supertonic's, the same.
  const st = apiOf({
    "DELETE /v1/voice/models/supertonic-F1": () => new Response(null, { status: HTTP_NO_CONTENT }),
    [CATALOG]: () => new Response(JSON.stringify({ ...cat, models: [] }), { status: HTTP_OK }),
  });
  deleted.length = 0;
  await lv.deleteVoice(st.api, SUPERTONIC);
  assert.deepEqual(deleted.slice(4), [
    "/v1/voice/models/supertonic/duration_predictor.onnx",
    "/v1/voice/models/supertonic/text_encoder.onnx",
    "/v1/voice/models/supertonic/vector_estimator.onnx",
    "/v1/voice/models/supertonic/vocoder.onnx",
    "/v1/voice/models/supertonic/unicode_indexer.json",
  ]);
  await assert.rejects(lv.deleteVoice(api, "b"), /HTTP 500/);
  await assert.rejects(lv.fetchCatalog(apiOf({}).api), /not found/);
  const first = [CATALOG, CATALOG, "POST /v1/voice/models/a", "POST /v1/voice/models/b"];
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
  const off = lv.onLoadState(() => {
    changes += 1;
  });
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
  assert.equal(changes, LOAD_CHANGES);
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
  const id = SUPERTONIC;
  assert.equal(lv.lanes(id), lv.SUPERTONIC_LANES, "Supertonic's model is large: few copies");
  assert.equal(lv.lanes("v1"), 1, "a Piper voice is quick enough alone");
  assert.equal(lv.lanes(CHATTERBOX), 1, "Chatterbox runs on the graphics card");
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
  made.at(-1).onmessage({ data: { kind: "ready", id, gpuError: NO_ADAPTER } });
  assert.deepEqual(lv.loadStateOf(id), { pct: 100, ready: true, gpuError: NO_ADAPTER }, "why WebGPU failed");
  for (const w of made.slice(LAST_TWO)) {
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
    [CATALOG]: () => new Response(JSON.stringify(state), { status: HTTP_OK }),
    [DOWNLOAD_ES]: () => {
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
  assert.match(q(LOCAL_VOICES).textContent, /33 MB/, "the engine's size is named before the first download");
  // The list opens on the interface's language; the filters widen or narrow it.
  const pick = async (s, value) => {
    q(s).value = value;
    await act(async () => q(s).dispatchEvent(new window.Event("change", { bubbles: true })));
  };
  assert.equal(q(ROW_ES), null, "another language is filtered out");
  assert.deepEqual([...q(LANGUAGE).options].map((o) => o.value), ["", "en", "es"]);
  await pick(QUALITY, "high");
  assert.equal(q("local-voice-en"), null);
  assert.match(q(LOCAL_VOICES).textContent, /No voice matches these filters/);
  await pick(QUALITY, "");
  await pick(LANGUAGE, "");
  assert.ok(q(ROW_ES));
  await act(async () => q("local-voice-get-es").click());
  await settle();
  assert.ok(calls.includes(DOWNLOAD_ES));
  assert.match(q("local-voice-progress-es").textContent, /50/);
  assert.equal(q("local-voice-get-en").disabled, true, "one download at a time");
  // The download ends: the next poll shows it installed, and as nothing was chosen it is chosen.
  state = { ...state, runtime: { ...state.runtime, installed: true }, models: [state.models[0], model("es", "es-MX", true)], download: null };
  await act(async () => t.mock.timers.tick(lv.DOWNLOAD_POLL_MS));
  await settle();
  assert.equal(q("local-voice-progress-es"), null);
  assert.deepEqual(chosen, ["es"]);
  assert.doesNotMatch(q(LOCAL_VOICES).textContent, /33 MB/);
  await act(async () => render(draw("es"), v.root));
  assert.equal(q("local-voice-use-es").getAttribute(ARIA_PRESSED), "true");
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
    [CATALOG]: [
      HTTP_OK,
      {
        runtime: { size: 1, installed: true },
        models: [
          row("a", { sample: true, engine: "piper" }),
          row("b", { sample: true, engine: "piper", quality: "x-low" }),
          row(SUPERTONIC, { engine: ENGINE_SUPERTONIC }),
        ],
        download: null,
      },
    ],
    "GET /v1/voice/models/a/sample": () => new Response(new Blob(["mp3"], { type: "audio/mpeg" }), { status: HTTP_OK }),
    "GET /v1/voice/models/b/sample": [HTTP_BAD_GATEWAY, { error: "the sample could not be downloaded" }],
  });
  const v = await mount(h(lv.LocalVoices, { api, chosen: "", choose: () => {} }));
  const q = (s) => v.root.querySelector(`[data-testid=${s}]`);
  assert.equal(q("local-voice-sample-supertonic-F1"), null, "no sample, no button");
  assert.match(q(ROW_SUPERTONIC).textContent, /Supertonic · High quality/);
  assert.match(q("local-voice-b").textContent, /Very low quality/);
  await act(async () => q(SAMPLE_A).click());
  await settle();
  assert.ok(calls.includes("GET /v1/voice/models/a/sample"));
  assert.equal(q(SAMPLE_A).getAttribute(ARIA_PRESSED), "true");
  assert.equal(q(SAMPLE_A).textContent, "■");
  // Its end frees the button.
  await act(async () => played[0].onended());
  assert.equal(q(SAMPLE_A).getAttribute(ARIA_PRESSED), "false");
  // Playing again, then pressing it once more, stops it.
  await act(async () => q(SAMPLE_A).click());
  await settle();
  await act(async () => q(SAMPLE_A).click());
  assert.equal(played[1].paused, true);
  assert.equal(q(SAMPLE_A).getAttribute(ARIA_PRESSED), "false");
  // A late end of a sample stopped already changes nothing.
  await act(async () => played[1].onended());
  // Another sample stops the one playing; one that fails says why.
  await act(async () => q(SAMPLE_A).click());
  await settle();
  await act(async () => q("local-voice-sample-b").click());
  await settle();
  assert.equal(played[2].paused, true);
  assert.match(v.root.textContent, /The sample could not be played: the sample could not be downloaded/);
  assert.equal(q("local-voice-sample-b").getAttribute(ARIA_PRESSED), "false");
  v.done();
  delete globalThis.Audio;
});

test("a failed download and an unreachable catalog are shown", async () => {
  const failed = {
    runtime: { size: 1, installed: true },
    models: [{ id: "es", name: "ES", lang: "es-MX", quality: "high", size: 1, installed: true }],
    download: { id: "es", done: 0, total: 1, error: "model.onnx is not the file the catalog names" },
  };
  const ok = apiOf({ [CATALOG]: [HTTP_OK, failed], [DOWNLOAD_ES]: [HTTP_CONFLICT, { error: "busy" }] });
  let v = await mount(h(lv.LocalVoices, { api: ok.api, chosen: "es", choose: () => {} }));
  assert.match(v.root.textContent, /model\.onnx is not the file/);
  // Use on the chosen voice says it is in use.
  assert.equal(v.root.querySelector("[data-testid=local-voice-use-es]").getAttribute(ARIA_PRESSED), "true");
  v.done();
  const down = apiOf({});
  v = await mount(h(lv.LocalVoices, { api: down.api, chosen: "", choose: () => {} }));
  assert.match(v.root.textContent, /not found/);
  v.done();
  // A refused download names why.
  const refusing = apiOf({
    [CATALOG]: [HTTP_OK, { ...failed, models: [{ ...failed.models[0], installed: false }], download: null }],
    [DOWNLOAD_ES]: [HTTP_CONFLICT, { error: "another voice is downloading" }],
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
    [CATALOG]: [HTTP_OK, { runtime: { size: 1, installed: true }, models: [], download: null }],
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
  const tune = v.root.querySelector(EXPRESSIVENESS_SLIDER);
  tune.value = String(EXPRESSIVENESS);
  await act(async () => tune.dispatchEvent(new window.Event("input", { bubbles: true })));
  assert.equal(saved.pop().expressiveness, EXPRESSIVENESS);
  v.done();
  v = await mount(h(lv.SpeechSettings, props({ ...prefs, engine: "local", variation: 1.6 }, { local: true, api })));
  await act(async () => v.root.querySelector("[data-testid=settings-speech-variation-reset]").click());
  assert.equal(saved.pop().variation, 1);
  v.done();
  // A Supertonic voice has its pace and its refinement steps instead.
  const st = { ...prefs, engine: "local", localVoice: SUPERTONIC, supertonicSpeed: SUPERTONIC_PACE, supertonicSteps: 0 };
  v = await mount(h(lv.SpeechSettings, props(st, { local: true, api })));
  assert.equal(v.root.querySelector(EXPRESSIVENESS_SLIDER), null);
  const q = (s) => v.root.querySelector(`[data-testid=${s}]`);
  assert.equal(q("settings-speech-supertonic-speed-reset").disabled, true, "at its own pace");
  assert.match(q(SUPERTONIC_STEPS).parentElement.textContent, /Auto/);
  const speed = q("settings-speech-supertonic-speed");
  speed.value = String(FASTER);
  await act(async () => speed.dispatchEvent(new window.Event("input", { bubbles: true })));
  assert.equal(saved.pop().supertonicSpeed, FASTER);
  const steps = q(SUPERTONIC_STEPS);
  steps.value = String(MORE_STEPS);
  await act(async () => steps.dispatchEvent(new window.Event("input", { bubbles: true })));
  assert.equal(saved.pop().supertonicSteps, MORE_STEPS);
  v.done();
  v = await mount(h(lv.SpeechSettings, props({ ...st, supertonicSpeed: 1.4, supertonicSteps: MORE_STEPS }, { local: true, api })));
  assert.match(q(SUPERTONIC_STEPS).parentElement.textContent, new RegExp(String(MORE_STEPS)));
  await act(async () => q("settings-speech-supertonic-speed-reset").click());
  assert.equal(saved.pop().supertonicSpeed, SUPERTONIC_PACE);
  await act(async () => q("settings-speech-supertonic-steps-reset").click());
  assert.equal(saved.pop().supertonicSteps, 0);
  v.done();
  // A Chatterbox voice has none here: its emotion is with its voices.
  v = await mount(h(lv.SpeechSettings, props({ ...prefs, engine: "local", localVoice: CHATTERBOX }, { local: true, api })));
  assert.equal(v.root.querySelector(EXPRESSIVENESS_SLIDER), null);
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
  const { api } = apiOf({ [CATALOG]: [HTTP_OK, cat] });
  const v = await mount(h(lv.LocalVoices, { api, chosen: "mx", choose: () => {} }));
  const q = (s) => v.root.querySelector(`[data-testid=${s}]`);
  assert.deepEqual(posted, [{ kind: "warm", id: "mx" }], "loaded as soon as it is shown");
  assert.match(q(LOADING_MX).textContent, /0/);
  assert.equal(q("local-voice-use-mx"), null, "not In use while it loads");
  const tell = async (data) => {
    await act(async () => globalThis.lastWorker.onmessage({ data }));
  };
  await tell({ kind: "progress", id: "mx", pct: 45 });
  assert.match(q(LOADING_MX).textContent, /45/);
  await tell({ kind: "failed", id: "mx", error: "out of memory" });
  assert.match(v.root.textContent, /out of memory/);
  await act(async () => q("local-voice-retry-mx").click());
  assert.deepEqual(posted.at(-1), { kind: "warm", id: "mx" });
  await tell({ kind: "ready", id: "mx" });
  assert.equal(q(LOADING_MX), null);
  assert.equal(q("local-voice-use-mx").getAttribute(ARIA_PRESSED), "true");
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
  const { api } = apiOf({ [CATALOG]: [HTTP_OK, cat] });
  const v = await mount(h(lv.LocalVoices, { api, chosen: "x", choose: () => {} }));
  const q = (s) => v.root.querySelector(`[data-testid=${s}]`);
  assert.equal(q(LANGUAGE).value, "en");
  assert.equal(q(QUALITY).value, "high");
  assert.equal(q(ROW_ES), null);
  q(QUALITY).value = "medium";
  await act(async () => q(QUALITY).dispatchEvent(new window.Event("change", { bubbles: true })));
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
    postMessage() {
      // Nothing is said here: the voice is told ready by hand below.
    }
  };
  globalThis.caches = {};
  const cat = {
    runtime: { size: 1, installed: true },
    models: [{ id: SUPERTONIC, name: "F1", lang: "mul", quality: "high", engine: ENGINE_SUPERTONIC, size: 1, installed: true }],
    download: null,
  };
  const { api } = apiOf({ [CATALOG]: [HTTP_OK, cat] });
  const v = await mount(h(lv.LocalVoices, { api, chosen: SUPERTONIC, choose: () => {} }));
  const q = (s) => v.root.querySelector(`[data-testid=${s}]`);
  const tell = (data) => act(async () => globalThis.lastWorker.onmessage({ data }));
  await tell({ kind: "ready", id: SUPERTONIC, gpu: true });
  assert.match(q(ROW_SUPERTONIC).textContent, /WebGPU/);
  assert.equal(q("local-voice-gpu-note"), null);
  await tell({ kind: "ready", id: SUPERTONIC, gpuError: "the browser gave no graphics card to use" });
  assert.doesNotMatch(q(ROW_SUPERTONIC).textContent, /WebGPU/);
  assert.match(q("local-voice-gpu-note").textContent, /could not start here \(the browser gave no graphics card to use\)/);
  v.done();
  globalThis.lastWorker.onerror();
  delete globalThis.Worker;
  delete globalThis.caches;
});

test("Supertonic speaks any language of the filter, and Chatterbox is offered on WebGPU with its emotion", async () => {
  globalThis.lastWorker?.onerror();
  globalThis.Worker = class {
    constructor() {
      globalThis.lastWorker = this;
    }
    postMessage() {
      // Nothing is said here: the voices are only listed.
    }
  };
  globalThis.caches = {};
  localStorage.setItem(lv.FILTERS_KEY, JSON.stringify({ language: "en", quality: "" }));
  const many = (id, engine, installed) => ({ id, name: id, lang: "mul", quality: "high", engine, size: 200, installed });
  const cat = {
    runtime: { size: 1, installed: true },
    models: [many(SUPERTONIC, ENGINE_SUPERTONIC, false), many(CHATTERBOX, "chatterbox", true)],
    download: null,
  };
  const { api } = apiOf({ [CATALOG]: [HTTP_OK, cat] });
  const emotions = [];
  const draw = (chosen) =>
    h(lv.LocalVoices, { api, chosen, choose: () => {}, emotion: 0.5, setEmotion: (n) => emotions.push(n) });
  let v = await mount(draw(SUPERTONIC));
  let q = (s) => v.root.querySelector(`[data-testid=${s}]`);
  assert.match(q(ROW_SUPERTONIC).textContent, /31 languages.*Supertonic · .*1 MB/);
  assert.equal(q("local-voice-chatterbox-default"), null, "no WebGPU, no Chatterbox");
  assert.equal(q("local-voice-emotion"), null);
  v.done();
  Object.defineProperty(globalThis.navigator, "gpu", { value: { requestAdapter: async () => ({}) }, configurable: true });
  v = await mount(draw(CHATTERBOX));
  q = (s) => v.root.querySelector(`[data-testid=${s}]`);
  assert.match(q("local-voice-chatterbox-default").textContent, /21 languages.*Chatterbox · /);
  q("local-voice-emotion-amount").value = String(EMOTION);
  await act(async () => q("local-voice-emotion-amount").dispatchEvent(new window.Event("input", { bubbles: true })));
  assert.deepEqual(emotions, [EMOTION]);
  v.done();
  delete globalThis.navigator.gpu;
  localStorage.removeItem(lv.FILTERS_KEY);
  globalThis.lastWorker.onerror();
  delete globalThis.Worker;
  delete globalThis.caches;
});
