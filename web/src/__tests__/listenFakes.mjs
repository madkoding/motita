// Stand-ins for local speech input's tests: a tokenizer the size of a test, Whisper's generation
// settings, and onnxruntime sessions that write a scripted transcript.

// A tokenizer the size of a test: ten words, then Whisper's special tokens.
export const WORDS = ["Ġhola", "Ġmundo", "Ã±", "€", "!", "Ġthe", "Ġend", "a", "b", "c"];
const SPECIAL = [
  "<|endoftext|>",
  "<|startoftranscript|>",
  "<|en|>",
  "<|es|>",
  "<|haw|>",
  "<|transcribe|>",
  "<|notimestamps|>",
  "<|nospeech|>",
];
export const ID = Object.fromEntries(SPECIAL.map((s, i) => [s, WORDS.length + i]));
export const VOCAB = WORDS.length + SPECIAL.length;
export function tokenizer({ drop = [], extra = [] } = {}) {
  return JSON.stringify({
    model: { vocab: Object.fromEntries(WORDS.map((w, i) => [w, i])) },
    added_tokens: [...SPECIAL, ...extra]
      .filter((s) => !drop.includes(s))
      .map((content) => ({ id: ID[content] ?? 99, content })),
  });
}
export const GEN = JSON.stringify({ suppress_tokens: [8], begin_suppress_tokens: [0, ID["<|endoftext|>"]] });

// The stand-in onnxruntime: tensors that keep what they were made with, an encoder that answers
// a hidden state 128 wide (two heads), and a decoder that writes `script` then ends the text. Each
// window of a recording starts again with nothing cached, and the decoder with it; `windows`, when
// given, is what it writes in each window instead (`quiet` for one where nothing is said).
export class Tensor {
  constructor(type, data, dims) {
    Object.assign(this, { type, data, dims });
  }
}
export function decoder({
  script = [1, 0, 4],
  lang = "<|es|>",
  quiet = false,
  ends = true,
  layers = 2,
  windows = null,
} = {}) {
  const runs = [];
  let step = 0;
  let window = -1;
  return {
    runs,
    inputNames: [
      "input_ids",
      ...Array.from({ length: layers }, (_, l) => `past_key_values.${l}.decoder.key`),
    ],
    async run(feeds) {
      runs.push(feeds);
      if (feeds.use_cache_branch.data[0] === 0) {
        step = 0;
        window++;
      }
      const w = windows?.[window];
      const says = w === undefined || w === "quiet" ? script : w;
      const n = feeds.input_ids.dims[1];
      const logits = new Float32Array(n * VOCAB);
      const last = logits.subarray((n - 1) * VOCAB);
      if (step === 0) {
        last[ID[lang]] = 5;
        last[ID["<|nospeech|>"]] = quiet || w === "quiet" ? 50 : -50;
      } else {
        const want = says[step - 1];
        if (want === undefined) {
          last[choose(ends, ID["<|endoftext|>"], 7)] = 9;
        } else {
          last[want] = 9;
        }
      }
      const out = { logits: new Tensor("float32", logits, [1, n, VOCAB]) };
      for (let l = 0; l < layers; l++) {
        for (const part of ["decoder", "encoder"]) {
          out[`present.${l}.${part}.key`] = { step, part };
          out[`present.${l}.${part}.value`] = { step, part };
        }
      }
      step++;
      return out;
    },
  };
}
export function choose(c, a, b) {
  return c ? a : b;
}
export const encoder = {
  runs: [],
  async run(feeds) {
    this.runs.push(feeds);
    return { last_hidden_state: new Tensor("float32", new Float32Array(1500 * 128), [1, 1500, 128]) };
  },
};

