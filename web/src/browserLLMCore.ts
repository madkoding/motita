// THE BROWSER MODEL: Qwen2.5 0.5B Instruct run in this browser on WebGPU, for the cheap jobs the
// gateway has (titles, summaries). The worker (browserLLM.worker.ts) wires these to onnxruntime;
// keeping the logic here, with every dependency passed in, lets the node tests drive it with
// fakes. Like speech.ts this file imports nothing at run time.

// The files the gateway serves for the model (see internal/voice/llm.go): a build for a card with
// 16-bit floats in its shaders, and a larger one for a card without them.
export const LLM_F16 = "llm-qwen25-0.5b";
export const LLM_F32 = "llm-qwen25-0.5b-f32";
export const LLM_FILES = [
  "tokenizer.json",
  "tokenizer_config.json",
  "model.onnx",
];

// The shape of Qwen2.5 0.5B (its config.json): 24 layers, each keeping 2 heads of 64 numbers for
// every token seen so far.
const LAYERS = 24;
const KV_HEADS = 2;
const HEAD_DIM = 64;
// The end of a turn and the end of the text: the model stops on either.
const IM_END = 151645;
const END_OF_TEXT = 151643;
const STOPS = new Set([IM_END, END_OF_TEXT]);
const DEFAULT_SYSTEM =
  "You are Qwen, created by Alibaba Cloud. You are a helpful assistant.";
// A title or a summary is short: the answer is cut at this many tokens.
export const MAX_NEW_TOKENS = 64;

export interface ChatMessage {
  role: string;
  content: string;
}

// chatPrompt writes the conversation as the model was trained to read it (ChatML), ending where
// the model's own turn begins. A conversation without a system turn gets the model's usual one.
export function chatPrompt(messages: ChatMessage[]): string {
  const turns = messages.filter(
    (m) => m.role === "system" || m.role === "user" || m.role === "assistant",
  );
  let withSystem = turns;
  if (turns[0]?.role !== "system") {
    withSystem = [{ role: "system", content: DEFAULT_SYSTEM }, ...turns];
  }
  return (
    withSystem
      .map((m) => `<|im_start|>${m.role}\n${m.content}<|im_end|>\n`)
      .join("") + "<|im_start|>assistant\n"
  );
}

// halfToFloat reads a 16-bit float (the bits of one).
const HALF_SIGN = 0x8000;
const HALF_EXP_SHIFT = 10;
const HALF_EXP_ALL = 0x1f;
const HALF_FRAC_MASK = 0x3ff;
const HALF_FRAC_SCALE = 1024;
const HALF_BIAS = 15;

export function halfToFloat(h: number): number {
  let sign = 1;
  if (h & HALF_SIGN) {
    sign = -1;
  }
  const exp = (h >> HALF_EXP_SHIFT) & HALF_EXP_ALL;
  const frac = h & HALF_FRAC_MASK;
  if (exp === 0) {
    return sign * 2 ** (1 - HALF_BIAS) * (frac / HALF_FRAC_SCALE);
  }
  if (exp === HALF_EXP_ALL) {
    if (frac) {
      return Number.NaN;
    }
    return sign * Infinity;
  }
  return sign * 2 ** (exp - HALF_BIAS) * (1 + frac / HALF_FRAC_SCALE);
}

// argmax is the position of the greatest number of the last `width` ones of logits (the scores of
// the last token of the prompt, or of the one token a step adds).
export function argmax(
  logits: ArrayLike<number>,
  width: number,
  half: boolean,
): number {
  const start = logits.length - width;
  let best = 0;
  let bestScore = -Infinity;
  for (let i = 0; i < width; i++) {
    const raw = logits[start + i] as number;
    let score = raw;
    if (half) {
      score = halfToFloat(raw);
    }
    if (score > bestScore) {
      best = i;
      bestScore = score;
    }
  }
  return best;
}

export interface LLMTensor {
  type: string;
  dims: readonly number[];
  getData(): Promise<ArrayLike<number>>;
}
export interface LLMSession {
  inputNames: readonly string[];
  run(feeds: Record<string, LLMTensor>): Promise<Record<string, LLMTensor>>;
}
export interface LLMOrt {
  Tensor: new (
    type: string,
    data: BigInt64Array | Uint16Array | Float32Array,
    dims: number[],
  ) => LLMTensor;
  createSession(model: ArrayBuffer): Promise<LLMSession>;
}
export interface TokenizerLike {
  encode(text: string): { ids: number[] };
  decode(ids: number[]): string;
}

export interface LLMDeps {
  bytes(
    url: string,
    progress?: (loaded: number, total: number) => void,
  ): Promise<ArrayBuffer>;
  // gpu loads onnxruntime's WebGPU build, given its WebAssembly; it throws where there is no
  // WebGPU. f16 says the card has 16-bit floats.
  gpu(wasm: ArrayBuffer): Promise<LLMOrt>;
  tokenizer(json: unknown, config: unknown): TokenizerLike;
  // f16 says whether the card can run the 16-bit build.
  f16(): Promise<boolean>;
}

const fileURL = (id: string, name: string) => `/v1/voice/models/${id}/${name}`;
const WASM_URL =
  "/v1/voice/models/runtime/ort-wasm-simd-threaded.asyncify.wasm";

// How far the load is, out of 100, when the model's weights are the bulk of it.
const WASM_SHARE = 5;

// BrowserModel is the model loaded once, then asked for text.
export class BrowserModel {
  private ready?: {
    session: LLMSession;
    ort: LLMOrt;
    tok: TokenizerLike;
    half: boolean;
  };

  private readonly deps: LLMDeps;

  constructor(deps: LLMDeps) {
    this.deps = deps;
  }

  // load downloads (from the gateway, or the page's copy) and starts the model.
  async load(progress: (pct: number) => void): Promise<{ build: string }> {
    const half = await this.deps.f16();
    const id = buildFor(half);
    const wasm = await this.deps.bytes(WASM_URL);
    progress(WASM_SHARE);
    const [json, config] = await Promise.all(
      LLM_FILES.slice(0, 2).map(async (n) =>
        JSON.parse(
          new TextDecoder().decode(await this.deps.bytes(fileURL(id, n))),
        ),
      ),
    );
    const weights = await this.deps.bytes(
      fileURL(id, "model.onnx"),
      (loaded, total) =>
        progress(
          WASM_SHARE +
            Math.round(((100 - 2 * WASM_SHARE) * loaded) / Math.max(total, 1)),
        ),
    );
    const ort = await this.deps.gpu(wasm);
    const session = await ort.createSession(weights);
    this.ready = { session, ort, tok: this.deps.tokenizer(json, config), half };
    progress(100);
    return { build: id };
  }

  // generate is the model's answer to the conversation, greedy, at most `max` tokens.
  async generate(
    messages: ChatMessage[],
    max = MAX_NEW_TOKENS,
  ): Promise<string> {
    const m = this.ready;
    if (!m) {
      throw new Error("the browser model is not loaded");
    }
    const { session, ort, tok, half } = m;
    let kind = "float32";
    let empty = (): Uint16Array | Float32Array => new Float32Array(0);
    if (half) {
      kind = "float16";
      empty = () => new Uint16Array(0);
    }
    let past: Record<string, LLMTensor> = {};
    for (let i = 0; i < LAYERS; i++) {
      for (const part of ["key", "value"]) {
        past[`past_key_values.${i}.${part}`] = new ort.Tensor(kind, empty(), [
          1,
          KV_HEADS,
          0,
          HEAD_DIM,
        ]);
      }
    }
    let ids = tok.encode(chatPrompt(messages)).ids;
    let seen = 0;
    const out: number[] = [];
    for (let step = 0; step < max; step++) {
      const total = seen + ids.length;
      const feeds: Record<string, LLMTensor> = {
        input_ids: new ort.Tensor("int64", BigInt64Array.from(ids, BigInt), [
          1,
          ids.length,
        ]),
        attention_mask: new ort.Tensor(
          "int64",
          new BigInt64Array(total).fill(1n),
          [1, total],
        ),
        position_ids: new ort.Tensor(
          "int64",
          BigInt64Array.from(ids, (_, i) => BigInt(seen + i)),
          [1, ids.length],
        ),
        ...past,
      };
      const res = await session.run(feeds);
      const logits = res.logits as LLMTensor;
      const vocab = logits.dims[2] as number;
      const next = argmax(
        await logits.getData(),
        vocab,
        logits.type === "float16",
      );
      if (STOPS.has(next)) {
        break;
      }
      out.push(next);
      past = {};
      for (let i = 0; i < LAYERS; i++) {
        for (const part of ["key", "value"]) {
          past[`past_key_values.${i}.${part}`] = res[
            `present.${i}.${part}`
          ] as LLMTensor;
        }
      }
      seen = total;
      ids = [next];
    }
    return tok.decode(out).trim();
  }
}

// What the page and the worker say to each other.
export type ToLLMWorker =
  | { kind: "load" }
  | { kind: "generate"; seq: number; messages: ChatMessage[] };
export type FromLLMWorker =
  | { kind: "progress"; pct: number }
  | { kind: "ready"; build: string }
  | { kind: "failed"; error: string }
  | { seq: number; text: string }
  | { seq: number; error: string };

// isBrowserLLM says a catalog entry is the browser model, not a voice.
export function isBrowserLLM(id: string): boolean {
  return id === LLM_F16 || id === LLM_F32;
}

// buildFor is the catalog id of the build a card runs: the 16-bit one where the card has 16-bit
// floats in its shaders.
export function buildFor(f16: boolean): string {
  if (f16) {
    return LLM_F16;
  }
  return LLM_F32;
}
