// The stand-ins the local voice tests share: a Piper voice's settings, and a stand-in onnxruntime
// and phonemizer that answer as Piper's model would.
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

export const VOICE = {
  espeak: { voice: "es-419" },
  audio: { sample_rate: SAMPLE_RATE },
  inference: { noise_scale: 0.667, length_scale: 1, noise_w: 0.8 },
};
export const enc = (s) => new TextEncoder().encode(s).buffer;

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
