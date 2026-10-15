// The stand-ins the local voice tests share: a Piper voice's settings, and a stand-in onnxruntime
// and phonemizer that answer as Piper's, Supertonic's and Chatterbox's models would.
import assert from "node:assert/strict";

export const SAMPLE_RATE = 22050;
// Where a WAV header keeps its sample rate.
export const SAMPLE_RATE_AT = 24;
// What the stand-in model says, and the phoneme ids the stand-in phonemizer answers.
const HALF = 0.5;
export const MODEL_OUTPUT = [HALF, -HALF];
const PHONEME_ID_O = 5;
export const PHONEME_IDS = [1, 0, PHONEME_ID_O, 0, 2];
const PHONEMES = ["h", "ˈ", "o", "l", "a", ".", "r", "x"];

export const CHATTERBOX = "chatterbox-default";

export const VOICE = {
  espeak: { voice: "es-419" },
  audio: { sample_rate: SAMPLE_RATE },
  inference: { noise_scale: 0.667, length_scale: 1, noise_w: 0.8 },
};
export const enc = (s) => new TextEncoder().encode(s).buffer;

// A Supertonic voice's style: two values for the text, one for the durations.
const STYLE_DP = 3;
export const SUPERTONIC_VOICE = {
  style_ttl: { data: [[[1, 2]]], dims: [1, 1, 2] },
  style_dp: { data: [[[STYLE_DP]]], dims: [1, 1, 1] },
};

// Chatterbox's stand-ins: a vocabulary of a few letters, and a voice of two feature frames.
export const CB_VOCAB = 6564;
const CB_STOP = 6562;
// The sound token the stand-in language model says.
export const CB_SOUND_TOKEN = 7;
// Its language model's layers, and the width of each embedding.
const CB_LAYERS = 30;
export const CB_HIDDEN = 1024;
// The ids of the stand-in vocabulary and of its special tokens.
export const CB_ID = {
  unk: 1,
  space: 2,
  h: 3,
  o: 4,
  l: 5,
  a: 6,
  dot: 7,
  ho: 8,
  es: 9,
  en: 10,
  laughter: 11,
  exaggeration: 6563,
  bos: 255,
  eos: 0,
  startSpeech: 6561,
};
// The voice: its feature frames, its own sound tokens, its speaker embedding and its features.
export const CB_FRAMES = 2;
const CB_FIRST_VOICE_TOKEN = 11;
export const CB_VOICE_TOKENS = [
  CB_FIRST_VOICE_TOKEN,
  CB_FIRST_VOICE_TOKEN + 1,
  CB_FIRST_VOICE_TOKEN + 2,
];
export const CB_EMBEDDING = 192;
export const CB_SPEAKER_FEATURES = 80;
// The voice file starts with three 32-bit counts.
const CB_HEADER_BYTES = 12;

// The language model says sound token 7 twice, then that it is done.
function choose3(n) {
  if (n <= 2) {
    return CB_SOUND_TOKEN;
  }
  return CB_STOP;
}

export const CB_TOKENIZER = {
  model: {
    vocab: {
      "[UNK]": CB_ID.unk,
      "[SPACE]": CB_ID.space,
      h: CB_ID.h,
      o: CB_ID.o,
      l: CB_ID.l,
      a: CB_ID.a,
      ".": CB_ID.dot,
      ho: CB_ID.ho,
    },
    merges: ["h o"],
  },
  added_tokens: [
    { id: CB_ID.space, content: "[SPACE]" },
    { id: CB_ID.es, content: "[es]" },
    { id: CB_ID.en, content: "[en]" },
    { id: CB_ID.laughter, content: "[laughter]" },
  ],
  post_processor: {
    special_tokens: {
      EXAGGERATION: { ids: [CB_ID.exaggeration] },
      BOS: { ids: [CB_ID.bos] },
      EOS: { ids: [CB_ID.eos] },
      START_SPEECH: { ids: [CB_ID.startSpeech] },
    },
  },
};

export function cbVoice() {
  const tokensAt = CB_FRAMES * CB_HIDDEN;
  const floats = new Float32Array(
    tokensAt + CB_VOICE_TOKENS.length + CB_EMBEDDING + CB_SPEAKER_FEATURES,
  ).fill(HALF);
  floats.set(CB_VOICE_TOKENS, tokensAt);
  const bytes = new Uint8Array(CB_HEADER_BYTES + floats.byteLength);
  bytes.set(
    new Uint8Array(
      Int32Array.from([CB_FRAMES, CB_VOICE_TOKENS.length, 1]).buffer,
    ),
  );
  bytes.set(new Uint8Array(floats.buffer), CB_HEADER_BYTES);
  return bytes.buffer;
}

// gpuFakes are fakes with WebGPU's build too, which fail when told to.
export function gpuFakes({ fail = false } = {}) {
  const { deps, log } = fakes();
  log.gpu = 0;
  deps.gpu = async () => {
    log.gpu++;
    if (fail) {
      throw new Error("no WebGPU here");
    }
    const ort = await deps.ort(
      enc("/v1/voice/models/runtime/ort-wasm-simd-threaded.wasm"),
    );
    log.ort--;
    return ort;
  };
  return { deps, log };
}

// fakes are a stand-in runtime and phonemizer that record what they were given.
export function fakes({
  speakers = 1,
  failFirstVoice = false,
  silent = false,
} = {}) {
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
        return enc(
          JSON.stringify(Array.from({ length: 123 }, (_, i) => i + 1)),
        );
      }
      if (url.endsWith("voice.json")) {
        return enc(JSON.stringify(SUPERTONIC_VOICE));
      }
      return enc(url);
    },
    ort: async (wasm) => {
      log.ort++;
      assert.equal(
        new TextDecoder().decode(wasm),
        "/v1/voice/models/runtime/ort-wasm-simd-threaded.wasm",
      );
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
                return {
                  inputs_embeds: {
                    data: new Float32Array(n * CB_HIDDEN).fill(n),
                    dims: [1, n, CB_HIDDEN],
                  },
                };
              }
              if (feeds.inputs_embeds) {
                log.lm = (log.lm ?? 0) + 1;
                const logits = new Float32Array(2 * CB_VOCAB);
                logits[CB_VOCAB + choose3(log.lm)] = 1;
                const present = {};
                for (let l = 0; l < CB_LAYERS; l++) {
                  for (const kv of ["key", "value"]) {
                    present["present." + l + "." + kv] = {
                      data: "kv" + log.lm,
                    };
                  }
                }
                return {
                  logits: { data: logits, dims: [1, 2, CB_VOCAB] },
                  ...present,
                };
              }
              // Every engine's outputs: Piper's and Supertonic's four parts'.
              return {
                output: { data },
                waveform: { data },
                wav_tts: { data },
                duration: { data: Float32Array.from([0.1]) },
                text_emb: { data: "emb" },
                denoised_latent: {
                  data: feeds.noisy_latent?.data.map((x) => x / 2),
                },
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
            print(
              JSON.stringify({ phoneme_ids: PHONEME_IDS, phonemes: PHONEMES }),
            );
          }
        },
      };
    },
  };
  return { deps, log };
}
