// Local speech input's logic (localListenCore.ts): the sound as Whisper reads it, its tokens as
// text, and the decoding loop on stand-in sessions that write a scripted transcript.
import test from "node:test";
import assert from "node:assert/strict";
import { source } from "./cov-voice-helpers.mjs";

import { GEN, ID, Tensor, VOCAB, choose, decoder, encoder, tokenizer } from "./listenFakes.mjs";

const core = await source("localListenCore.ts");

test("the mel filters are Whisper's: 80 bands over 201 frequencies, each a triangle", () => {
  const f = core.melFilters();
  assert.equal(f.length, core.N_MELS);
  assert.equal(f[0].length, core.N_FFT / 2 + 1);
  const peaks = f.map((row) => row.indexOf(Math.max(...row)));
  for (let m = 1; m < peaks.length; m++) {
    assert.ok(peaks[m] >= peaks[m - 1], "each band sits above the one before");
  }
  assert.ok(f.every((row) => row.every((w) => w >= 0)));
});

test("the spectrogram: silence is one floor, a tone lights its band, and 30 s are the most", () => {
  const silence = core.logMel(new Float32Array(16000));
  assert.equal(silence.length, core.N_MELS * core.N_FRAMES);
  assert.ok(silence.every((v) => v === silence[0]), "nothing said is the same everywhere");
  assert.equal(silence[0], (-10 + 4) / 4);

  // A 1 kHz tone for a second, then the silence the window is padded with.
  const tone = new Float32Array(16000).map((_, i) => Math.sin((2 * Math.PI * 1000 * i) / 16000));
  const mel = core.logMel(tone);
  const at = (m, t) => mel[m * core.N_FRAMES + t];
  const bands = Array.from({ length: core.N_MELS }, (_, m) => at(m, 50));
  const loudest = bands.indexOf(Math.max(...bands));
  const f = core.melFilters()[loudest];
  const centre = (f.indexOf(Math.max(...f)) * 16000) / core.N_FFT;
  assert.ok(Math.abs(centre - 1000) < 100, `the loudest band is around 1 kHz: ${centre}`);
  assert.equal(at(loudest, 2000), mel.reduce((a, b) => Math.min(a, b)), "past the tone is the floor");

  // Longer than the window: it is cut, and its last frames read past its end, mirrored.
  const long = core.logMel(new Float32Array(31 * 16000).fill(0.1));
  assert.equal(long.length, core.N_MELS * core.N_FRAMES);
  assert.ok(long.every(Number.isFinite));
});

test("the vocabulary names the steering tokens and the languages, or says which is missing", () => {
  const v = core.vocabulary(tokenizer());
  assert.equal(v.eot, ID["<|endoftext|>"]);
  assert.equal(v.sot, ID["<|startoftranscript|>"]);
  assert.equal(v.noSpeech, ID["<|nospeech|>"]);
  assert.deepEqual([...v.langs.keys()].sort(), ["en", "es", "haw"]);
  // Older tokenizers name "nothing said" <|nocaptions|>.
  const old = core.vocabulary(tokenizer({ drop: ["<|nospeech|>"], extra: ["<|nocaptions|>"] }));
  assert.equal(old.noSpeech, 99);
  assert.throws(() => core.vocabulary(tokenizer({ drop: ["<|transcribe|>"] })), /the tokenizer has no <\|transcribe\|>/);
});

test("tokens become text: bytes put back together, special tokens left out", () => {
  const v = core.vocabulary(tokenizer());
  assert.equal(core.textOf([0, 1, 4, ID["<|endoftext|>"]], v), "hola mundo!");
  assert.equal(core.textOf([7, 2], v), "añ", "two characters that are one letter's bytes");
  // A character no byte stands for reads as a space.
  assert.equal(core.textOf([7, 3, 8], v), "a b");
});

test("generation settings, and how the next token is chosen", () => {
  assert.deepEqual(core.generation("{}"), { suppress: [], beginSuppress: [] });
  const g = core.generation(GEN);
  assert.deepEqual(g, { suppress: [8], beginSuppress: [0, ID["<|endoftext|>"]] });
  const v = core.vocabulary(tokenizer());
  const logits = new Float32Array(VOCAB);
  logits[0] = 5; // the likeliest, but never first
  logits[8] = 9; // never written
  logits[v.eot] = 4;
  logits[v.sot] = 20; // a steering token is never written
  logits[1] = 3;
  assert.equal(core.nextOf(logits, v, g, true), 1);
  assert.equal(core.nextOf(logits, v, g, false), 0);

  logits[ID["<|en|>"]] = 1;
  logits[ID["<|es|>"]] = 2;
  assert.equal(core.languageOf(logits, v), ID["<|es|>"]);
  const quiet = new Float32Array(VOCAB);
  quiet[v.noSpeech] = 10;
  assert.ok(core.noSpeechOf(quiet, v) > core.NO_SPEECH);
  assert.ok(core.noSpeechOf(logits, v) < core.NO_SPEECH);
  assert.equal(core.maxTokens(16000), 24);
  assert.equal(core.maxTokens(16000 * 60), 224);
  assert.equal(core.layersOf(["input_ids", "past_key_values.0.decoder.key", "past_key_values.1.decoder.key"]), 2);
  assert.ok(core.isWhisper("whisper-base") && !core.isWhisper("supertonic-F1"));
});

// deps serve the files by name, with their progress, and record what was asked for.
function deps({ dec = decoder(), tok = tokenizer(), gpu, broken = false } = {}) {
  const asked = [];
  const made = [];
  const ort = (kind) => ({
    Tensor,
    async createSession(model) {
      const name = new TextDecoder().decode(model);
      made.push({ kind, name });
      if (broken && name.endsWith("encoder.onnx")) {
        throw new Error("bad model");
      }
      return choose(name.endsWith("encoder.onnx"), encoder, dec);
    },
  });
  const d = {
    asked,
    made,
    dec,
    async bytes(url, progress) {
      asked.push(url);
      let body = url;
      if (url.endsWith("tokenizer.json")) {
        body = tok;
      } else if (url.endsWith("generation_config.json")) {
        body = GEN;
      }
      const bytes = new TextEncoder().encode(body);
      // One file says how big it is, the others do not.
      progress?.(bytes.length / 2, choose(url.endsWith("decoder.onnx"), bytes.length, 0));
      progress?.(bytes.length, choose(url.endsWith("decoder.onnx"), bytes.length, 0));
      return bytes.buffer;
    },
    ort: async () => ort("cpu"),
  };
  if (gpu === "works") {
    d.gpu = async () => ort("gpu");
  } else if (gpu === "fails") {
    d.gpu = async () => {
      throw new Error("no adapter");
    };
  }
  return d;
}

test("a model loads with its progress and writes down what was said, in the language it hears", async () => {
  const d = deps();
  const l = new core.Listener(d);
  const pcts = [];
  assert.deepEqual(await l.warm("whisper-base", (p) => pcts.push(p)), { gpu: false, gpuError: "" });
  assert.equal(pcts[0], 0);
  assert.equal(pcts.at(-1), 100);
  assert.ok(pcts.every((p, i) => i === 0 || p >= pcts[i - 1]), `progress only grows: ${pcts}`);
  assert.deepEqual(d.asked.sort(), [
    "/v1/voice/models/runtime/ort-wasm-simd-threaded.wasm",
    "/v1/voice/models/whisper-base/decoder.onnx",
    "/v1/voice/models/whisper-base/encoder.onnx",
    "/v1/voice/models/whisper-base/generation_config.json",
    "/v1/voice/models/whisper-base/tokenizer.json",
  ]);

  const said = await l.hear("whisper-base", new Float32Array(16000), "");
  assert.deepEqual(said, { text: "mundo hola!", lang: "es" });
  const [first, prompt, ...rest] = d.dec.runs;
  assert.deepEqual([...first.input_ids.data], [BigInt(ID["<|startoftranscript|>"])]);
  assert.equal(first.use_cache_branch.data[0], 0, "the first pass has nothing cached");
  assert.deepEqual(first["past_key_values.1.encoder.key"].dims, [1, 2, 0, 64], "two heads 64 wide, empty");
  assert.deepEqual(
    [...prompt.input_ids.data],
    [ID["<|es|>"], ID["<|transcribe|>"], ID["<|notimestamps|>"]].map(BigInt),
  );
  assert.equal(prompt.use_cache_branch.data[0], 1);
  assert.deepEqual(prompt["past_key_values.0.decoder.key"], { step: 0, part: "decoder" });
  const lastRun = rest.at(-1);
  assert.deepEqual(lastRun["past_key_values.0.decoder.key"], { step: rest.length, part: "decoder" });
  assert.deepEqual(lastRun["past_key_values.0.encoder.key"], { step: 0, part: "encoder" }, "made once");
  assert.deepEqual(encoder.runs.at(-1).input_features.dims, [1, 80, 3000]);
});

test("the language asked for is the one written; nothing said answers nothing", async () => {
  const l = new core.Listener(deps({ dec: decoder({ lang: "<|en|>", script: [5, 6] }) }));
  // Never warmed: the first recording loads the model.
  assert.deepEqual(await l.hear("whisper-tiny", new Float32Array(800), "es"), { text: "the end", lang: "es" });
  const q = deps({ dec: decoder({ quiet: true }) });
  assert.deepEqual(await new core.Listener(q).hear("whisper-base", new Float32Array(800), "en"), {
    text: "",
    lang: "en",
  });
  assert.equal(q.dec.runs.length, 1, "it stops after the first pass");
});

test("a model that never ends stops at its most tokens; one without languages keeps the one asked", async () => {
  const d = deps({ dec: decoder({ script: [], ends: false, layers: 1 }) });
  const r = await new core.Listener(d).hear("whisper-base", new Float32Array(16000), "");
  assert.equal(r.text, "a".repeat(core.maxTokens(16000)));
  const bare = deps({ tok: tokenizer({ drop: ["<|en|>", "<|es|>", "<|haw|>"] }) });
  bare.dec.inputNames = undefined;
  const h = await new core.Listener(bare).hear("whisper-base", new Float32Array(16000), "xx");
  assert.equal(h.lang, "xx");
});

test("the model for the graphics card runs there, or on the processor saying why it could not", async () => {
  const on = deps({ gpu: "works" });
  assert.deepEqual(await new core.Listener(on).warm("whisper-base-gpu"), { gpu: true, gpuError: "" });
  assert.ok(on.asked.includes("/v1/voice/models/whisper-base-gpu/ort-wasm-simd-threaded.asyncify.wasm"));
  assert.ok(!on.asked.some((u) => u.includes("/runtime/")), "the processor's runtime is not read");
  assert.ok(on.made.every((m) => m.kind === "gpu"));

  const off = deps({ gpu: "fails" });
  assert.deepEqual(await new core.Listener(off).warm("whisper-base-gpu"), { gpu: false, gpuError: "no adapter" });
  assert.ok(off.made.every((m) => m.kind === "cpu"));

  // A model for the processor never asks for the card, and a browser without one runs it too.
  const cpu = deps({ gpu: "works" });
  assert.deepEqual(await new core.Listener(cpu).warm("whisper-small"), { gpu: false, gpuError: "" });
  assert.deepEqual(await new core.Listener(deps()).warm("whisper-small-gpu"), { gpu: false, gpuError: "" });
});

test("a model that cannot load is tried again; a forgotten one loads again; a failure lets the next run", async () => {
  const d = deps({ broken: true });
  const l = new core.Listener(d);
  await assert.rejects(l.warm("whisper-base"), /bad model/);
  await assert.rejects(l.hear("whisper-base", new Float32Array(10), ""), /bad model/);
  const tries = d.made.filter((m) => m.name.endsWith("encoder.onnx")).length;
  assert.equal(tries, 2, "the failed load was not kept");

  const ok = deps();
  const k = new core.Listener(ok);
  await k.warm("whisper-base");
  await k.warm("whisper-base");
  assert.equal(ok.made.length, 2, "loaded once");
  k.forget("whisper-base");
  await k.warm("whisper-base");
  assert.equal(ok.made.length, 4, "loaded again once forgotten");
});
