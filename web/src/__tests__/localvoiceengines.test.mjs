// Local voices beyond Piper: how Supertonic and Chatterbox read a sentence, and how their models
// are run with a stand-in onnxruntime, on the CPU or on WebGPU.
import test from "node:test";
import assert from "node:assert/strict";
import {
  CB_EMBEDDING,
  CB_FRAMES,
  CB_HIDDEN,
  CB_ID,
  CB_SOUND_TOKEN,
  CB_SPEAKER_FEATURES,
  CB_TOKENIZER,
  CB_VOICE_TOKENS,
  CHATTERBOX,
  SAMPLE_RATE_AT,
  SUPERTONIC_VOICE,
  cbVoice,
  fakes,
  gpuFakes,
} from "./localvoiceFakes.mjs";

const core = await import("../localVoiceCore.ts");

const SUPERTONIC = "supertonic-F1";
const SUPERTONIC_M = "supertonic-M1";
const NO_PHONEMIZER = "no phonemizer";
const NOTHING_OF_THE_CPU = "nothing of the CPU's";
const CPU_RUNTIME = "/runtime/ort-wasm-simd-threaded.wasm";
const NOT_A_CHATTERBOX_VOICE = /not a Chatterbox voice/;
// Supertonic's indexer: the ids of "a" and "b", which come after every code point before "a".
const CODE_A = "a".codePointAt(0);
const ID_A = 7;
const ID_B = 8;
// Supertonic's latent: samples per second, samples per chunk, and channels per frame.
const SUPERTONIC_HZ = 44100;
const SUPERTONIC_CHUNK = 3072;
const SUPERTONIC_CHANNELS = 144;
// The Box-Muller transform's factor, and how much noise is asked for.
const BOX_MULLER = -2;
const NOISE_LENGTH = 3;
// Each sentence runs the duration predictor, the text encoder and the vocoder besides the steps.
const RUNS_BESIDE_STEPS = 3;
// At half its pace, 0.1 s is three chunks; and three refinement steps are asked for.
const SLOW_CHUNKS = 3;
const STEPS_ASKED = 3;
// Scores for nextToken: a token already said, a likelier one, and the least likely.
const HIGH_SCORE = 1.1;
const LOW_SCORE = -2;
// Chatterbox's emotion asked for, and its text tokens after "[es]" up to the end.
const EMOTION = 1.5;
const HOLA_TEXT_TOKENS = 6;
// The language model's attention and key/value cache sizes.
const CB_HEADS = 16;
const CB_HEAD_DIM = 64;
const LM_STEPS_BEFORE_THIRD = 2;
const MAX_ERROR = 1e-6;

test("a sentence is written as Supertonic reads it, and its voice and length are read", () => {
  assert.equal(core.isSupertonic(SUPERTONIC), true);
  assert.equal(core.isSupertonic(CHATTERBOX), false);
  assert.equal(
    core.supertonicText("Hola — “qué” tal 😀   mundo", "es-CL"),
    '<es>Hola - "qué" tal mundo.</es>'.normalize("NFKD"),
  );
  assert.equal(
    core.supertonicText("Hi , you [there]!", "en"),
    "<en>Hi, you there!</en>",
  );
  assert.equal(
    core.supertonicText("Salut", "xx"),
    "<na>Salut.</na>",
    "a language it lacks is its own neutral one",
  );
  const indexer = new Array(CODE_A).fill(0).concat([ID_A, ID_B]);
  assert.deepEqual(core.supertonicIds("ab€", indexer), [ID_A, ID_B, -1]);
  const st = core.supertonicStyle(JSON.stringify(SUPERTONIC_VOICE));
  assert.deepEqual([...st.ttl], [1, 2]);
  assert.deepEqual(st.dpDims, [1, 1, 1]);
  assert.throws(() => core.supertonicStyle("{}"), /not a Supertonic voice/);
  assert.equal(core.supertonicLatent(0), 1, "never empty");
  assert.equal(
    core.supertonicLatent(1),
    Math.ceil(SUPERTONIC_HZ / SUPERTONIC_CHUNK),
  );
  const draws = [0.5, 0, 0.5, 0.5];
  let r = 0;
  const half = core.noise(2, () => {
    const x = draws[r];
    r += 1;
    return x;
  });
  assert.ok(
    Math.abs(half[0] - Math.sqrt(BOX_MULLER * Math.log(0.5))) < MAX_ERROR,
  );
  assert.ok(half[1] < 0, "cos of half a turn");
  assert.equal(core.noise(NOISE_LENGTH).length, NOISE_LENGTH);
});

test("a Supertonic voice speaks on its shared model, refined step by step, in the sentence's language", async () => {
  const { deps, log } = fakes();
  const synth = new core.Synth(deps);
  const seen = [];
  await synth.warm(SUPERTONIC, (p) => seen.push(p));
  assert.equal(seen.at(-1), 100);
  assert.equal(log.phon, 0, NO_PHONEMIZER);
  assert.deepEqual(
    log.bytes.filter((u) => !u.includes("/runtime/")),
    [
      "/v1/voice/models/supertonic-F1/voice.json",
      ...core.SUPERTONIC_PARTS.map((n) => "/v1/voice/models/supertonic/" + n),
      "/v1/voice/models/supertonic/unicode_indexer.json",
    ],
  );
  assert.equal(log.sessions, 4);
  const wav = await synth.say(SUPERTONIC, "ab", {
    noise: 1,
    noiseW: 1,
    lang: "es",
  });
  assert.equal(
    new DataView(wav).getUint32(SAMPLE_RATE_AT, true),
    core.SUPERTONIC_RATE,
  );
  const [dp, te, ...rest] = log.feeds;
  const text = [..."<es>ab.</es>"].map((c) => BigInt(c.codePointAt(0) + 1));
  assert.deepEqual([...dp.text_ids.data], text);
  assert.deepEqual([...te.style_ttl.data], [1, 2]);
  assert.equal(
    rest.length,
    core.SUPERTONIC_STEPS.cpu + 1,
    "the steps, then the waveform",
  );
  assert.deepEqual(
    rest[0].noisy_latent.dims,
    [1, SUPERTONIC_CHANNELS, 2],
    "0.1 s at its pace is two chunks",
  );
  assert.deepEqual([...rest[2].current_step.data], [2]);
  assert.equal(
    rest[1].noisy_latent.data[0],
    rest[0].noisy_latent.data[0] / 2,
    "each step starts from the last",
  );
  assert.equal(await synth.onGPU(SUPERTONIC), false);
  // Without a language it is told English.
  await synth.say(SUPERTONIC, "ab");
  const english = log.feeds.at(-core.SUPERTONIC_STEPS.cpu - RUNS_BESIDE_STEPS);
  assert.equal(english.text_ids.data[1], BigInt("e".codePointAt(0) + 1));
});

test("a Supertonic voice speaks at the pace and with the steps asked for", async () => {
  const { deps, log } = fakes();
  const synth = new core.Synth(deps);
  // Slower is longer: 0.1 s at half its pace is three chunks; three steps are three runs.
  await synth.say(SUPERTONIC, "ab", {
    noise: 1,
    noiseW: 1,
    speed: 0.5,
    steps: STEPS_ASKED,
  });
  const steps = log.feeds.filter((f) => f.noisy_latent);
  assert.equal(steps.length, STEPS_ASKED);
  assert.deepEqual(steps[0].noisy_latent.dims, [
    1,
    SUPERTONIC_CHANNELS,
    SLOW_CHUNKS,
  ]);
  assert.deepEqual([...steps[0].total_step.data], [STEPS_ASKED]);
  // Steps of 0 are its own for where it runs.
  log.feeds.length = 0;
  await synth.say(SUPERTONIC, "ab", {
    noise: 1,
    noiseW: 1,
    speed: core.SUPERTONIC_SPEED,
    steps: 0,
  });
  assert.equal(
    log.feeds.filter((f) => f.noisy_latent).length,
    core.SUPERTONIC_STEPS.cpu,
  );
});

test("Supertonic runs on WebGPU where the browser has it, and on the CPU when that fails", async () => {
  const { deps, log } = gpuFakes();
  deps.gpu = ((gpu) => async (wasm) => {
    assert.equal(
      new TextDecoder().decode(wasm),
      "/v1/voice/models/runtime/ort-wasm-simd-threaded.asyncify.wasm",
    );
    return gpu(wasm);
  })(deps.gpu);
  const synth = new core.Synth(deps);
  await synth.warm(SUPERTONIC_M);
  assert.ok(
    !log.bytes.some((u) => u.endsWith(CPU_RUNTIME)),
    NOTHING_OF_THE_CPU,
  );
  assert.equal(await synth.onGPU(SUPERTONIC_M), true);
  await synth.say(SUPERTONIC_M, "a", { noise: 1, noiseW: 1, lang: "en" });
  assert.equal(log.feeds.length, 2 + core.SUPERTONIC_STEPS.gpu + 1);
  assert.equal(synth.gpuError(), "");
  const failing = gpuFakes({ fail: true });
  const cpu = new core.Synth(failing.deps);
  await cpu.say(SUPERTONIC_M, "a");
  assert.equal(await cpu.onGPU(SUPERTONIC_M), false);
  assert.equal(failing.log.ort, 1, "the CPU's runtime said it");
  assert.equal(cpu.gpuError(), "no WebGPU here", "and the page is told why");
  assert.equal(
    await cpu.onGPU("v1"),
    false,
    "a Piper voice runs on the processor",
  );
});

test("a sentence is written as Chatterbox reads it, and split into its tokens", () => {
  assert.equal(core.isChatterbox(CHATTERBOX), true);
  assert.equal(core.isChatterbox(SUPERTONIC), false);
  assert.equal(
    core.chatterboxText("  Hola…  qué  tal: “bien”; ¿Sí?", "es-CL"),
    '[es]hola,  qué tal, "bien",  ¿sí?'.normalize("NFKD"),
  );
  assert.equal(
    core.chatterboxText("Done — mostly", "en"),
    "[en]done - mostly.",
  );
  assert.equal(
    core.chatterboxText("Ni hao", "zh"),
    "[en]ni hao.",
    "a language it cannot read alone is read as English",
  );
  const tk = core.chatterboxTokenizer(JSON.stringify(CB_TOKENIZER));
  const id = CB_ID;
  assert.deepEqual(core.chatterboxTokens(tk, "[es]hola [laughter]hal?"), [
    id.exaggeration,
    id.bos,
    id.es,
    id.ho,
    id.l,
    id.a,
    id.space,
    id.laughter,
    id.h,
    id.a,
    id.l,
    id.unk,
    id.eos,
    id.startSpeech,
    id.startSpeech,
  ]);
  assert.throws(
    () => core.chatterboxTokenizer("{}"),
    /not Chatterbox's tokenizer/,
  );
  assert.throws(
    () =>
      core.chatterboxTokenizer(
        JSON.stringify({ ...CB_TOKENIZER, post_processor: {} }),
      ),
    /not Chatterbox's/,
  );
  const v = core.chatterboxVoice(cbVoice());
  assert.equal(v.featureLen, CB_FRAMES);
  assert.deepEqual(v.tokens, CB_VOICE_TOKENS);
  assert.equal(v.embedding.length, CB_EMBEDDING);
  assert.equal(v.speaker.length, CB_SPEAKER_FEATURES);
  assert.throws(
    () => core.chatterboxVoice(new ArrayBuffer(16)),
    NOT_A_CHATTERBOX_VOICE,
  );
  assert.throws(
    () => core.chatterboxVoice(new ArrayBuffer(4)),
    NOT_A_CHATTERBOX_VOICE,
  );
  // A token already said is less likely: a positive score divided, a negative one multiplied.
  assert.equal(core.nextToken(Float32Array.from([0, HIGH_SCORE, 1]), [1]), 2);
  assert.equal(
    core.nextToken(Float32Array.from([-1, -HIGH_SCORE, LOW_SCORE]), [0]),
    1,
  );
});

test("a Chatterbox voice speaks on WebGPU: the voice, then the text, then one sound token at a time", async () => {
  const { deps, log } = gpuFakes();
  const weights = [];
  const ort = deps.gpu;
  deps.gpu = async (w) => {
    const o = await ort(w);
    return {
      ...o,
      createSession: (m, x) => {
        weights.push(x?.path);
        return o.createSession(m);
      },
    };
  };
  const synth = new core.Synth(deps);
  await synth.warm(CHATTERBOX);
  assert.equal(log.phon, 0, NO_PHONEMIZER);
  assert.ok(
    !log.bytes.some((u) => u.endsWith(CPU_RUNTIME)),
    NOTHING_OF_THE_CPU,
  );
  assert.deepEqual(
    weights,
    core.CHATTERBOX_PARTS.map((p) => p + "_data"),
    "each part with its weights",
  );
  assert.equal(await synth.onGPU(CHATTERBOX), true);
  const wav = await synth.say(CHATTERBOX, "Hola", {
    noise: 1,
    noiseW: 1,
    lang: "es",
    exaggeration: EMOTION,
  });
  assert.equal(
    new DataView(wav).getUint32(SAMPLE_RATE_AT, true),
    core.CHATTERBOX_RATE,
  );
  const embeds = log.feeds.filter((f) => f.exaggeration);
  const id = CB_ID;
  const holaIds = [
    id.exaggeration,
    id.bos,
    id.es,
    id.ho,
    id.l,
    id.a,
    id.dot,
    id.eos,
    id.startSpeech,
    id.startSpeech,
  ];
  assert.deepEqual([...embeds[0].input_ids.data].map(Number), holaIds);
  const textPositions = Array.from(
    { length: HOLA_TEXT_TOKENS },
    (_, i) => i + 1,
  );
  assert.deepEqual([...embeds[0].position_ids.data].map(Number), [
    0,
    0,
    ...textPositions,
    0,
    0,
  ]);
  assert.deepEqual([...embeds[0].exaggeration.data], [EMOTION]);
  assert.deepEqual([...embeds[1].position_ids.data].map(Number), [1]);
  const lm = log.feeds.filter((f) => f.inputs_embeds);
  const prompt = CB_FRAMES + holaIds.length;
  assert.deepEqual(
    lm[0].inputs_embeds.dims,
    [1, prompt, CB_HIDDEN],
    "the voice's two frames before the text",
  );
  assert.equal(lm[0].inputs_embeds.data[0], 0.5);
  assert.deepEqual(lm[0]["past_key_values.0.key"].dims, [
    1,
    CB_HEADS,
    0,
    CB_HEAD_DIM,
  ]);
  assert.equal(
    lm[1]["past_key_values.29.value"].data,
    "kv1",
    "each step remembers the last",
  );
  assert.equal(lm[2].attention_mask.dims[1], prompt + LM_STEPS_BEFORE_THIRD);
  const dec = log.feeds.at(-1);
  assert.ok(dec, "the decoder ran");
  assert.deepEqual(
    [...dec.speech_tokens.data].map(Number),
    [...CB_VOICE_TOKENS, CB_SOUND_TOKEN, CB_SOUND_TOKEN],
    "the voice's own, then the new",
  );
  assert.deepEqual(dec.speaker_features.dims, [1, 1, CB_SPEAKER_FEATURES]);
  // Without a language or an emotion, English at its default.
  await synth.say(CHATTERBOX, "a");
  assert.deepEqual(
    [...log.feeds.findLast((f) => f.exaggeration).exaggeration.data],
    [core.CHATTERBOX_EMOTION],
  );
});

test("a browser known to have no WebGPU never fetches its runtime: Supertonic runs on the CPU", async () => {
  const { deps, log } = gpuFakes();
  deps.webGPU = () => false;
  const synth = new core.Synth(deps);
  await synth.warm(SUPERTONIC);
  assert.equal(await synth.onGPU(SUPERTONIC), false);
  assert.equal(synth.gpuError(), "this browser has no WebGPU in its workers");
  assert.ok(
    log.bytes.some((u) => u.endsWith(CPU_RUNTIME)),
    "the CPU's runtime is read",
  );
  assert.ok(
    !log.bytes.some((u) => u.endsWith(".asyncify.wasm")),
    "WebGPU's is not",
  );
  await assert.rejects(synth.say(CHATTERBOX, "a"), /no WebGPU in its workers/);
  assert.ok(!log.bytes.some((u) => u.endsWith(".asyncify.wasm")));
});

test("Chatterbox does not run without WebGPU, and its sentences stop at the most tokens", async () => {
  const plain = fakes();
  const cpu = new core.Synth(plain.deps);
  await assert.rejects(cpu.say(CHATTERBOX, "a"), /needs WebGPU/);
  const { deps, log } = gpuFakes();
  const synth = new core.Synth(deps);
  log.lm = -1e9;
  await synth.say(CHATTERBOX, "a");
  assert.equal(
    log.feeds.filter((f) => f.inputs_embeds).length,
    core.CHATTERBOX_MAX_TOKENS,
  );
});
