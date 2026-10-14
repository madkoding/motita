// LOCAL VOICES: a Piper, Supertonic or Chatterbox model run in this browser, the parts
// that need no browser.
//
// The worker (speechModel.worker.ts) wires these to onnxruntime and the phonemizer; keeping the
// logic here, with every dependency passed in, lets the node tests drive it with fakes. Like
// speech.ts this file imports nothing at run time.

// Where the gateway serves the downloaded files (see internal/gateway/voicemodels.go).
export const MODELS_PATH = "/v1/voice/models";
export const RUNTIME_ID = "runtime";
export const ORT_WASM = "ort-wasm-simd-threaded.wasm";
export const PHONEMIZE_WASM = "piper_phonemize.wasm";
export const PHONEMIZE_DATA = "piper_phonemize.data";
// ORT_GPU_WASM is onnxruntime's build that drives WebGPU, served with the model that uses it.
export const ORT_GPU_WASM = "ort-wasm-simd-threaded.asyncify.wasm";
// SUPERTONIC_ID is where Supertonic's model is served; a Supertonic voice's id is it, a dash, and
// the voice's name there.
export const SUPERTONIC_ID = "supertonic";
export const SUPERTONIC_RATE = 44100;
// The parts of Supertonic's model, in the order they run.
export const SUPERTONIC_PARTS = ["duration_predictor.onnx", "text_encoder.onnx", "vector_estimator.onnx", "vocoder.onnx"];
// SUPERTONIC_LANGS are the languages Supertonic speaks; "na" is its own for any other.
export const SUPERTONIC_LANGS = new Set(
  "en ko ja ar bg cs da de el es et fi fr hi hr hu id it lt lv nl pl pt ro ru sk sl sv tr uk vi".split(" "),
);
// Supertonic's sound is made of chunks of SUPERTONIC_CHUNK samples, each SUPERTONIC_LATENT
// numbers wide (its tts.json: base_chunk_size 512 and latent_dim 24, both times
// chunk_compress_factor 6).
const SUPERTONIC_CHUNK = 512 * 6;
const SUPERTONIC_LATENT = 24 * 6;
// SUPERTONIC_SPEED is the pace the model speaks at when none is asked for (its own default):
// it changes how long each sound lasts, not the pitch, unlike the page's playback rate.
export const SUPERTONIC_SPEED = 1.05;
// SUPERTONIC_STEPS are the steps the sound is refined in when none are asked for: its default
// on the graphics card, and fewer on the processor, where each step takes about a second of a
// sentence's.
export const SUPERTONIC_STEPS = { gpu: 8, cpu: 5 };
// CHATTERBOX_ID is where Chatterbox's model is served; a Chatterbox voice's id is it, a dash, and
// the voice's name.
export const CHATTERBOX_ID = "chatterbox";
export const CHATTERBOX_RATE = 24000;
// The parts of Chatterbox's model; each keeps its weights in a file of the same name plus "_data".
export const CHATTERBOX_PARTS = ["embed_tokens.onnx", "language_model_q4.onnx", "conditional_decoder.onnx"];
// CHATTERBOX_LANGS are the languages Chatterbox speaks without help (Chinese and Japanese need
// converters the page does not have, and are read as English).
export const CHATTERBOX_LANGS = new Set("ar da de el en es fi fr he hi it ko ms nl no pl pt ru sv sw tr".split(" "));
// The shape of Chatterbox's language model: its layers, and each one's keys and values.
const CHATTERBOX_LAYERS = 30;
const CHATTERBOX_HEADS = 16;
const CHATTERBOX_HEAD = 64;
const CHATTERBOX_WIDTH = 1024;
// CHATTERBOX_MAX_TOKENS is the most sound tokens a sentence gets: 25 a second, so 32 seconds.
export const CHATTERBOX_MAX_TOKENS = 800;
// CHATTERBOX_PENALTY makes a sound token already said less likely, as the model's own settings.
const CHATTERBOX_PENALTY = 1.2;
// CHATTERBOX_EMOTION is the emotion setting a sentence gets when none is given (its default).
export const CHATTERBOX_EMOTION = 0.5;
// A warm's progress, out of 100: reading the files makes LOAD_SHARE, starting the runtime
// RUNTIME_SHARE and the phonemizer PHONEMIZER_SHARE; preparing the model (onnxruntime's session,
// the longest step once the files are on this device) is the rest.
export const LOAD_SHARE = 50;
export const RUNTIME_SHARE = 15;
export const PHONEMIZER_SHARE = 5;
// VOICE_CACHE is the Cache Storage this page keeps the files in.
export const VOICE_CACHE = "motita-voices-v1";

// choose is a when b, c otherwise (this file imports nothing, so it has its own).
function choose<T>(a: boolean, b: T, c: T): T {
  if (a) {
    return b;
  }
  return c;
}

// fileURL is the address of one downloaded file.
export function fileURL(id: string, name: string): string {
  return MODELS_PATH + "/" + encodeURIComponent(id) + "/" + encodeURIComponent(name);
}

// VoiceConfig is the part of a Piper voice's .onnx.json the synthesis reads.
export interface VoiceConfig {
  espeak: { voice: string };
  audio: { sample_rate: number };
  inference: { noise_scale: number; length_scale: number; noise_w: number };
  num_speakers?: number;
}

// parseVoiceConfig reads a voice's settings, or throws when they are not a Piper voice's.
export function parseVoiceConfig(text: string): VoiceConfig {
  const raw = JSON.parse(text) as Partial<VoiceConfig>;
  if (!raw.espeak?.voice || !raw.audio?.sample_rate || !raw.inference) {
    throw new Error("not a Piper voice");
  }
  return raw as VoiceConfig;
}

// phonemizeArgs are the phonemizer's command line for one text.
export function phonemizeArgs(espeakVoice: string, text: string): string[] {
  return ["-l", espeakVoice, "--input", JSON.stringify([{ text }]), "--espeak_data", "/espeak-ng-data"];
}

// Phonemized is the phonemizer's output for one text: the ids a Piper model takes.
export interface Phonemized {
  ids: number[];
}

// phonemized reads the phonemizer's output line.
export function phonemized(line: string): Phonemized {
  const out = JSON.parse(line) as { phoneme_ids?: number[] };
  if (!Array.isArray(out.phoneme_ids) || out.phoneme_ids.length === 0) {
    throw new Error("the phonemizer answered nothing");
  }
  return { ids: out.phoneme_ids };
}

// MAX_REASON is the longest reason for a failure the page shows.
const MAX_REASON = 200;

// reasonOf is why something failed, short enough to show.
export function reasonOf(e: unknown): string {
  const text = (choose(e instanceof Error, (e as Error)?.message, String(e)) || String(e)).trim();
  return choose(text.length > MAX_REASON, text.slice(0, MAX_REASON - 1) + "…", text);
}

// isSupertonic says whether a voice runs on Supertonic.
export function isSupertonic(id: string): boolean {
  return id.startsWith(SUPERTONIC_ID + "-");
}

// SUPERTONIC_SWAPS are the characters Supertonic's own pipeline writes otherwise before reading.
const SUPERTONIC_SWAPS: [RegExp, string][] = [
  [/[–‑—]/g, "-"],
  [/[_[\]|/#→←]/g, " "],
  [/[“”]/g, '"'],
  [/[‘’´`]/g, "'"],
  [/[♥☆♡©\\]/g, ""],
  [/@/g, " at "],
  [/ ([,.!?;:'])/g, "$1"],
  [/""+/g, '"'],
  [/''+/g, "'"],
  [/\s+/g, " "],
];

// supertonicText is a sentence as Supertonic reads it, tagged with its language: decomposed,
// without emoji, its punctuation written as the model knows it, ending in a mark.
export function supertonicText(text: string, lang: string): string {
  let t = text.normalize("NFKD").replace(/\p{Extended_Pictographic}/gu, "");
  for (const [from, to] of SUPERTONIC_SWAPS) {
    t = t.replace(from, to);
  }
  t = t.trim();
  if (!/[.!?;:,'"')\]}…。」』】〉》›»]$/.test(t)) {
    t += ".";
  }
  const base = lang.toLowerCase().split("-")[0];
  const l = choose(SUPERTONIC_LANGS.has(base), base, "na");
  return "<" + l + ">" + t + "</" + l + ">";
}

// supertonicIds are the ids Supertonic takes for a tagged text, from its table by code point
// (-1 for one it lacks).
export function supertonicIds(text: string, indexer: number[]): number[] {
  return [...text].map((c) => indexer[c.codePointAt(0) as number] ?? -1);
}

// SupertonicStyle is a Supertonic voice: its two style tensors, flat, and their shapes.
export interface SupertonicStyle {
  ttl: Float32Array;
  ttlDims: number[];
  dp: Float32Array;
  dpDims: number[];
}

// supertonicStyle reads a Supertonic voice's JSON file.
export function supertonicStyle(text: string): SupertonicStyle {
  type Part = { data?: unknown; dims?: number[] };
  const raw = JSON.parse(text) as { style_ttl?: Part; style_dp?: Part };
  const flat = (p?: Part) => {
    if (!Array.isArray(p?.data) || !Array.isArray(p?.dims)) {
      throw new Error("not a Supertonic voice");
    }
    return Float32Array.from((p.data as unknown[]).flat(Infinity) as number[]);
  };
  return {
    ttl: flat(raw.style_ttl),
    ttlDims: raw.style_ttl?.dims as number[],
    dp: flat(raw.style_dp),
    dpDims: raw.style_dp?.dims as number[],
  };
}

// supertonicLatent is how many chunks a sentence of this many seconds takes.
export function supertonicLatent(seconds: number): number {
  return Math.max(1, Math.ceil(Math.floor(seconds * SUPERTONIC_RATE) / SUPERTONIC_CHUNK));
}

// noise is n numbers drawn from the normal distribution, for the steps to start from.
export function noise(n: number, random: () => number = Math.random): Float32Array {
  const out = new Float32Array(n);
  for (let i = 0; i < n; i++) {
    const u = Math.max(1e-4, random());
    out[i] = Math.sqrt(-2 * Math.log(u)) * Math.cos(2 * Math.PI * random());
  }
  return out;
}

// readsText says a voice's engine reads the text itself, with no phonemizer.
function readsText(id: string): boolean {
  return isSupertonic(id) || isChatterbox(id);
}

// chatterboxFiles are the files Chatterbox's model is loaded from.
function chatterboxFiles(): string[] {
  const parts = CHATTERBOX_PARTS.flatMap((p) => [p, p + "_data"]);
  return ["tokenizer.json", ORT_GPU_WASM, ...parts].map((n) => fileURL(CHATTERBOX_ID, n));
}

// isChatterbox says whether a voice runs on Chatterbox.
export function isChatterbox(id: string): boolean {
  return id.startsWith(CHATTERBOX_ID + "-");
}

// CHATTERBOX_SWAPS are the marks Chatterbox's own pipeline writes otherwise before reading.
const CHATTERBOX_SWAPS: [RegExp, string][] = [
  [/\.\.\.|…/g, ", "],
  [/:/g, ","],
  [/ - /g, ", "],
  [/;/g, ", "],
  [/[—–]/g, "-"],
  [/ ,/g, ","],
  [/[“”]/g, '"'],
  [/[‘’]/g, "'"],
];

// chatterboxText is a sentence as Chatterbox reads it: its marks normalised and an end added the
// way its own pipeline does, then lowercased and decomposed as its tokenizer was trained, after
// its language's tag.
export function chatterboxText(text: string, lang: string): string {
  let t = text.split(/\s+/).filter(Boolean).join(" ");
  for (const [from, to] of CHATTERBOX_SWAPS) {
    t = t.replace(from, to);
  }
  t = t.trimEnd();
  if (!/[.!?,-]$/.test(t)) {
    t += ".";
  }
  const base = lang.toLowerCase().split("-")[0];
  const l = choose(CHATTERBOX_LANGS.has(base), base, "en");
  return "[" + l + "]" + t.toLowerCase().normalize("NFKD");
}

// ChatterboxTokenizer is Chatterbox's table from text to ids: a BPE vocabulary, its merges by
// rank, the tokens taken whole (language tags, sounds such as [laughter]), and the ids that
// frame a text.
export interface ChatterboxTokenizer {
  vocab: Map<string, number>;
  ranks: Map<string, number>;
  // whole splits a text around the tokens taken whole, which wholes lists.
  whole: RegExp;
  wholes: Set<string>;
  frame: { exaggeration: number; bos: number; eos: number; start: number };
}

// chatterboxTokenizer reads Chatterbox's tokenizer.json.
export function chatterboxTokenizer(text: string): ChatterboxTokenizer {
  type Special = Record<string, { ids?: number[] }>;
  const raw = JSON.parse(text) as {
    model?: { vocab?: Record<string, number>; merges?: (string | string[])[] };
    added_tokens?: { id: number; content: string }[];
    post_processor?: { special_tokens?: Special };
  };
  const special = raw.post_processor?.special_tokens;
  const id = (name: string) => special?.[name]?.ids?.[0];
  const frame = { exaggeration: id("EXAGGERATION"), bos: id("BOS"), eos: id("EOS"), start: id("START_SPEECH") };
  if (!raw.model?.vocab || !raw.model.merges || !raw.added_tokens || Object.values(frame).includes(undefined)) {
    throw new Error("not Chatterbox's tokenizer");
  }
  const vocab = new Map(Object.entries(raw.model.vocab));
  for (const a of raw.added_tokens) {
    vocab.set(a.content, a.id);
  }
  const ranks = new Map(raw.model.merges.map((m, i) => [choose(typeof m === "string", m as string, ([] as string[]).concat(m).join(" ")), i]));
  // The longest first, so a tag is never cut by a shorter one inside it; a space is its own.
  const wholes = raw.added_tokens.map((a) => a.content).sort((a, b) => b.length - a.length);
  const escaped = wholes.map((c) => c.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"));
  return {
    vocab,
    ranks,
    whole: new RegExp("(" + [...escaped, " "].join("|") + ")"),
    wholes: new Set(wholes),
    frame: frame as ChatterboxTokenizer["frame"],
  };
}

// bpe splits a word into its vocabulary's pieces: the pair with the lowest rank is joined until
// none is left.
function bpe(word: string, ranks: Map<string, number>): string[] {
  let parts = [...word];
  for (;;) {
    let best = -1;
    let at = -1;
    for (let i = 0; i + 1 < parts.length; i++) {
      const r = ranks.get(parts[i] + " " + parts[i + 1]);
      if (r !== undefined && (best < 0 || r < best)) {
        best = r;
        at = i;
      }
    }
    if (at < 0) {
      return parts;
    }
    parts = [...parts.slice(0, at), parts[at] + parts[at + 1], ...parts.slice(at + 2)];
  }
}

// chatterboxTokens are the ids Chatterbox takes for a text, framed as its tokenizer does: the
// emotion's slot, the start, the text, the end, and the start of the sound twice. Its spaces are
// [SPACE], its tags whole, and the rest split into words and marks and then by BPE.
export function chatterboxTokens(tk: ChatterboxTokenizer, text: string): number[] {
  const unknown = tk.vocab.get("[UNK]") ?? 1;
  const ids: number[] = [];
  for (const piece of text.split(tk.whole)) {
    if (piece === " ") {
      ids.push(tk.vocab.get("[SPACE]") ?? unknown);
    } else if (tk.wholes.has(piece)) {
      ids.push(tk.vocab.get(piece) as number);
    } else {
      for (const w of piece.match(/[\p{L}\p{N}\p{M}_]+|[^\p{L}\p{N}\p{M}_\s]+/gu) ?? []) {
        ids.push(...bpe(w, tk.ranks).map((p) => tk.vocab.get(p) ?? unknown));
      }
    }
  }
  const f = tk.frame;
  return [f.exaggeration, f.bos, ...ids, f.eos, f.start, f.start];
}

// ChatterboxVoice is what Chatterbox's speech encoder made of a voice's recording, as voice.bin
// keeps it (three little-endian int32 lengths, then float32s): what the language model reads
// before the text, the recording's own sound tokens, and the speaker as the waveform part takes it.
export interface ChatterboxVoice {
  features: Float32Array;
  featureLen: number;
  tokens: number[];
  embedding: Float32Array;
  speaker: Float32Array;
  speakerLen: number;
}

// The widths in voice.bin: the speaker's embedding, and each frame of its features.
const CHATTERBOX_EMBEDDING = 192;
const CHATTERBOX_MELS = 80;

// chatterboxVoice reads a Chatterbox voice.bin.
export function chatterboxVoice(buf: ArrayBuffer): ChatterboxVoice {
  const head = new Int32Array(buf.slice(0, 12));
  const [featureLen, tokenLen, speakerLen] = [head[0] ?? 0, head[1] ?? 0, head[2] ?? 0];
  const all = new Float32Array(buf.slice(12));
  const sizes = [featureLen * CHATTERBOX_WIDTH, tokenLen, CHATTERBOX_EMBEDDING, speakerLen * CHATTERBOX_MELS];
  if (buf.byteLength < 12 || all.length !== sizes.reduce((a, b) => a + b, 0) || !featureLen || !tokenLen) {
    throw new Error("not a Chatterbox voice");
  }
  const parts: Float32Array[] = [];
  let at = 0;
  for (const n of sizes) {
    parts.push(all.subarray(at, at + n));
    at += n;
  }
  return {
    features: parts[0],
    featureLen,
    tokens: Array.from(parts[1]),
    embedding: parts[2],
    speaker: parts[3],
    speakerLen,
  };
}

// nextToken is the most likely sound token after the penalty on those already said (the
// model's own repetition penalty: a positive score is divided by it, a negative multiplied).
export function nextToken(logits: Float32Array, said: Iterable<number>): number {
  const scores = Float32Array.from(logits);
  for (const t of new Set(said)) {
    scores[t] = choose(scores[t] < 0, scores[t] * CHATTERBOX_PENALTY, scores[t] / CHATTERBOX_PENALTY);
  }
  let best = 0;
  for (let k = 1; k < scores.length; k++) {
    if (scores[k] > scores[best]) {
      best = k;
    }
  }
  return best;
}

// The layout of a canonical 44-byte WAV header: byte offsets of its fields, in order.
const WAV_RIFF = 0;
const WAV_RIFF_SIZE = 4;
const WAV_WAVE = 8;
const WAV_FMT = 12;
const WAV_FMT_SIZE = 16;
const WAV_FORMAT = 20;
const WAV_CHANNELS = 22;
const WAV_RATE = 24;
const WAV_BYTE_RATE = 28;
const WAV_BLOCK_ALIGN = 32;
const WAV_BITS = 34;
const WAV_DATA = 36;
const WAV_DATA_SIZE = 40;
const WAV_HEADER = 44;
// The bytes of the header the RIFF size leaves out: the "RIFF" tag and the size itself.
const WAV_RIFF_PREFIX = 8;
// The size of the PCM fmt chunk's body.
const WAV_FMT_BODY = 16;
const WAV_PCM = 1;
const WAV_MONO = 1;
// Every sample is 16 bits, two bytes.
const SAMPLE_BYTES = 2;
const SAMPLE_BITS = 16;
// The largest 16-bit sample, which a full-scale float sample becomes.
const SAMPLE_MAX = 32767;

// wavOf is mono 16-bit PCM audio, as a WAV file the <audio> element plays.
export function wavOf(samples: Float32Array, rate: number): ArrayBuffer {
  const view = new DataView(new ArrayBuffer(WAV_HEADER + samples.length * SAMPLE_BYTES));
  const text = (at: number, s: string) => {
    for (let i = 0; i < s.length; i++) {
      view.setUint8(at + i, s.codePointAt(i) ?? 0);
    }
  };
  text(WAV_RIFF, "RIFF");
  view.setUint32(WAV_RIFF_SIZE, view.byteLength - WAV_RIFF_PREFIX, true);
  text(WAV_WAVE, "WAVE");
  text(WAV_FMT, "fmt ");
  view.setUint32(WAV_FMT_SIZE, WAV_FMT_BODY, true);
  view.setUint16(WAV_FORMAT, WAV_PCM, true);
  view.setUint16(WAV_CHANNELS, WAV_MONO, true);
  view.setUint32(WAV_RATE, rate, true);
  view.setUint32(WAV_BYTE_RATE, rate * SAMPLE_BYTES, true);
  view.setUint16(WAV_BLOCK_ALIGN, SAMPLE_BYTES, true);
  view.setUint16(WAV_BITS, SAMPLE_BITS, true);
  text(WAV_DATA, "data");
  view.setUint32(WAV_DATA_SIZE, samples.length * SAMPLE_BYTES, true);
  for (const [i, sample] of samples.entries()) {
    const s = Math.max(-1, Math.min(1, sample));
    view.setInt16(WAV_HEADER + i * SAMPLE_BYTES, Math.round(s * SAMPLE_MAX), true);
  }
  return view.buffer;
}

// readAll reads a response's body, telling progress the bytes read and the size it announced
// (0 when it named none): first when the reading starts, then after every chunk.
export async function readAll(
  res: Response,
  progress?: (loaded: number, total: number) => void,
): Promise<ArrayBuffer> {
  const total = Number(res.headers.get("Content-Length")) || 0;
  progress?.(0, total);
  if (!res.body) {
    const whole = await res.arrayBuffer();
    progress?.(whole.byteLength, total);
    return whole;
  }
  const reader = res.body.getReader();
  const chunks: Uint8Array[] = [];
  let loaded = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) {
      break;
    }
    chunks.push(value);
    loaded += value.byteLength;
    progress?.(loaded, total);
  }
  const out = new Uint8Array(loaded);
  let at = 0;
  for (const c of chunks) {
    out.set(c, at);
    at += c.byteLength;
  }
  return out.buffer;
}

// What the synthesis needs from onnxruntime, so a test can stand in.
export interface TensorLike {
  data: unknown;
  dims?: readonly number[];
}
export interface SessionLike {
  run(feeds: Record<string, TensorLike>): Promise<Record<string, TensorLike>>;
}
export interface OrtLike {
  Tensor: new (type: "int64" | "float32", data: BigInt64Array | Float32Array, dims: number[]) => TensorLike;
  // weights, when given, is the file the model keeps its weights in, by the name it gives it.
  createSession(model: ArrayBuffer, weights?: { path: string; data: ArrayBuffer }): Promise<SessionLike>;
}

// A phonemizer instance: callMain runs it once, and what it prints arrives through print.
export interface PhonemizerLike {
  callMain(args: string[]): void;
}

// SynthDeps are what a Synth reads files with and runs them on.
export interface SynthDeps {
  // bytes is a downloaded file, from the page's own copy when there is one. progress, when
  // given, is told the bytes read so far and the file's size (0 when it is not known).
  bytes(url: string, progress?: (loaded: number, total: number) => void): Promise<ArrayBuffer>;
  // ort loads the runtime the first time it is asked, given its WebAssembly.
  ort(wasm: ArrayBuffer): Promise<OrtLike>;
  // phonemizer builds one instance from its WebAssembly and data; print receives its output.
  phonemizer(wasm: ArrayBuffer, data: ArrayBuffer, print: (line: string) => void): Promise<PhonemizerLike>;
  // gpu loads onnxruntime's WebGPU build, given its WebAssembly; its sessions run on WebGPU,
  // and it throws where the browser has none.
  gpu?(wasm: ArrayBuffer): Promise<OrtLike>;
}

// A voice ready to speak: a Piper model, or a Supertonic or Chatterbox voice on its engine's
// shared model.
type LoadedVoice =
  | { kind: "piper"; config: VoiceConfig; session: SessionLike }
  | { kind: "supertonic"; style: SupertonicStyle }
  | { kind: "chatterbox"; voice: ChatterboxVoice };

// Chatterbox's model, loaded once for its voices, on WebGPU.
interface ChatterboxModel {
  ort: OrtLike;
  embed: SessionLike;
  lm: SessionLike;
  decoder: SessionLike;
  tokenizer: ChatterboxTokenizer;
  gpu: true;
}

// Supertonic's model, loaded once for all its voices: its four parts in order, and its table.
interface SupertonicModel {
  ort: OrtLike;
  parts: SessionLike[];
  indexer: number[];
  gpu: boolean;
}

// Synth says texts with the local voices: the runtime and the phonemizer are loaded once, and
// each voice the first time it speaks. Requests run one at a time, in order.
export class Synth {
  private readonly deps: SynthDeps;
  private ort: Promise<OrtLike> | null = null;
  private phon: Promise<PhonemizerLike> | null = null;
  private supertonic: Promise<SupertonicModel> | null = null;
  private chatterbox: Promise<ChatterboxModel> | null = null;
  // failed is why Supertonic could not run on WebGPU here ("" when it did not fail), so the
  // processor runs it instead.
  private failed = "";
  private printed: string[] = [];
  private readonly voices = new Map<string, Promise<LoadedVoice>>();
  private queue: Promise<unknown> = Promise.resolve();
  // watch follows the files a warm is reading, for its progress.
  private watch: {
    files: Map<string, [number, number]>;
    // steps is the share of the steps done besides reading.
    steps: number;
    shown: number;
    report: (pct: number) => void;
  } | null = null;

  constructor(deps: SynthDeps) {
    this.deps = deps;
  }

  // say is one text as WAV audio; tune, when given, scales the voice's own noise settings.
  say(id: string, text: string, tune?: Tune): Promise<ArrayBuffer> {
    const job = this.queue.then(() => this.sayNow(id, text, tune));
    this.queue = job.catch(() => undefined);
    return job;
  }

  // warm loads a voice, the runtime and the phonemizer ahead of the first sentence, which then
  // starts as soon as it is synthesised. progress is told how far it is, 0 to 100: the files
  // read make up to LOAD_SHARE, and preparing the model the rest.
  warm(id: string, progress?: (pct: number) => void): Promise<void> {
    const job = this.queue.then(async () => {
      const files: string[] = [];
      // Supertonic and Chatterbox read the text itself: they need no phonemizer, nor the
      // processor's runtime where the graphics card runs them (Chatterbox runs nowhere else).
      if (!this.ort && !isChatterbox(id) && !(isSupertonic(id) && this.deps.gpu)) {
        files.push(fileURL(RUNTIME_ID, ORT_WASM));
      }
      if (!this.phon && !readsText(id)) {
        files.push(fileURL(RUNTIME_ID, PHONEMIZE_WASM), fileURL(RUNTIME_ID, PHONEMIZE_DATA));
      }
      const supertonic = isSupertonic(id);
      if (supertonic && !this.supertonic) {
        files.push(...this.supertonicFiles());
      }
      if (isChatterbox(id) && !this.chatterbox) {
        files.push(...chatterboxFiles());
      }
      if (!this.voices.has(id)) {
        files.push(...this.voiceFiles(id));
      }
      this.watch = {
        files: new Map(files.map((f) => [f, [-1, 0]])),
        // A step already done counts from the start.
        steps: choose(!!this.ort, RUNTIME_SHARE, 0) + choose(!!this.phon, PHONEMIZER_SHARE, 0),
        shown: 0,
        report: progress ?? (() => undefined),
      };
      try {
        const loads: Promise<unknown>[] = [this.voice(id)];
        if (!readsText(id)) {
          loads.push(this.phonemizer());
        }
        await Promise.all(loads);
      } finally {
        this.watch = null;
      }
      progress?.(100);
    });
    this.queue = job.catch(() => undefined);
    return job;
  }

  // forget drops a voice, so the next request loads it again (after it was deleted, say).
  forget(id: string): void {
    this.voices.delete(id);
  }

  // gpuError is why Supertonic could not run on WebGPU here, "" when it did not fail.
  gpuError(): string {
    return this.failed;
  }

  // onGPU says whether a voice's engine runs on WebGPU now: Supertonic wherever it worked, and
  // Chatterbox, which runs nowhere else. Piper runs on the processor.
  async onGPU(id: string): Promise<boolean> {
    let model: Promise<{ gpu: boolean }> | null = null;
    if (isSupertonic(id)) {
      model = this.supertonic;
    } else if (isChatterbox(id)) {
      model = this.chatterbox;
    }
    return (await model?.catch(() => null))?.gpu ?? false;
  }

  // voiceFiles are a voice's own files.
  private voiceFiles(id: string): string[] {
    if (isSupertonic(id)) {
      return [fileURL(id, "voice.json")];
    }
    if (isChatterbox(id)) {
      return [fileURL(id, "voice.bin")];
    }
    return [fileURL(id, "model.json"), fileURL(id, "model.onnx")];
  }

  // supertonicFiles are the files Supertonic's model is loaded from, here.
  private supertonicFiles(): string[] {
    const files = [...SUPERTONIC_PARTS, "unicode_indexer.json"].map((n) => fileURL(SUPERTONIC_ID, n));
    if (this.deps.gpu) {
      files.push(fileURL(SUPERTONIC_ID, ORT_GPU_WASM));
    }
    return files;
  }

  private async sayNow(id: string, text: string, tune: Tune = { noise: 1, noiseW: 1 }): Promise<ArrayBuffer> {
    const v = await this.voice(id);
    if (v.kind === "supertonic") {
      return this.saySupertonic(v.style, text, tune);
    }
    if (v.kind === "chatterbox") {
      return this.sayChatterbox(v.voice, text, tune);
    }
    const ort = await this.runtime();
    const { ids } = await this.phonemize(v.config.espeak.voice, text);
    const inf = v.config.inference;
    const scales = [inf.noise_scale * tune.noise, inf.length_scale, inf.noise_w * tune.noiseW];
    const feeds: Record<string, TensorLike> = {
      input: new ort.Tensor("int64", BigInt64Array.from(ids.map(BigInt)), [1, ids.length]),
      input_lengths: new ort.Tensor("int64", BigInt64Array.from([BigInt(ids.length)]), [1]),
      scales: new ort.Tensor("float32", Float32Array.from(scales), [scales.length]),
    };
    if ((v.config.num_speakers ?? 1) > 1) {
      feeds.sid = new ort.Tensor("int64", BigInt64Array.from([0n]), [1]);
    }
    const out = await v.session.run(feeds);
    return wavOf(out.output.data as Float32Array, v.config.audio.sample_rate);
  }

  // saySupertonic says a sentence the way Supertonic's own pipeline does: the length of each
  // sound, the text, then the sound refined from noise step by step, and the waveform.
  // Its pace and its steps are the ones asked for, or its own.
  private async saySupertonic(style: SupertonicStyle, text: string, tune: Tune): Promise<ArrayBuffer> {
    const m = await this.supertonicModel();
    const [dp, te, ve, voc] = m.parts;
    const T = m.ort.Tensor;
    const ids = supertonicIds(supertonicText(text, tune.lang ?? "en"), m.indexer);
    const textIds = new T("int64", BigInt64Array.from(ids.map(BigInt)), [1, ids.length]);
    const textMask = new T("float32", new Float32Array(ids.length).fill(1), [1, 1, ids.length]);
    const ttl = new T("float32", style.ttl, style.ttlDims);
    const dpStyle = new T("float32", style.dp, style.dpDims);
    const seconds = ((await dp.run({ text_ids: textIds, style_dp: dpStyle, text_mask: textMask })).duration
      .data as Float32Array)[0];
    const textEmb = (await te.run({ text_ids: textIds, style_ttl: ttl, text_mask: textMask })).text_emb;
    const len = supertonicLatent(seconds / (tune.speed ?? SUPERTONIC_SPEED));
    const latentMask = new T("float32", new Float32Array(len).fill(1), [1, 1, len]);
    const steps = tune.steps || choose(m.gpu, SUPERTONIC_STEPS.gpu, SUPERTONIC_STEPS.cpu);
    const total = new T("float32", Float32Array.from([steps]), [1]);
    let x = noise(SUPERTONIC_LATENT * len);
    for (let step = 0; step < steps; step++) {
      const out = await ve.run({
        noisy_latent: new T("float32", x, [1, SUPERTONIC_LATENT, len]),
        text_emb: textEmb,
        style_ttl: ttl,
        latent_mask: latentMask,
        text_mask: textMask,
        current_step: new T("float32", Float32Array.from([step]), [1]),
        total_step: total,
      });
      x = out.denoised_latent.data as Float32Array;
    }
    const wav = (await voc.run({ latent: new T("float32", x, [1, SUPERTONIC_LATENT, len]) })).wav_tts;
    return wavOf(wav.data as Float32Array, SUPERTONIC_RATE);
  }

  // sayChatterbox says a sentence the way Chatterbox's own pipeline does: the language model,
  // after the voice's features and the text, writes sound tokens one at a time (each one
  // remembering the rest through its keys and values) until it says it is done, and the waveform
  // part turns them, after the voice's own, into sound.
  private async sayChatterbox(voice: ChatterboxVoice, text: string, tune: Tune): Promise<ArrayBuffer> {
    const m = await this.chatterboxModel();
    const T = m.ort.Tensor;
    const longs = (a: number[]) => BigInt64Array.from(a.map(BigInt));
    const ids = chatterboxTokens(m.tokenizer, chatterboxText(text, tune.lang ?? "en"));
    const start = m.tokenizer.frame.start;
    const emotion = new T("float32", Float32Array.from([tune.exaggeration ?? CHATTERBOX_EMOTION]), [1]);
    // The text's positions count from its start; the frame's speech tokens are at 0.
    let feed: Record<string, TensorLike> = {
      input_ids: new T("int64", longs(ids), [1, ids.length]),
      position_ids: new T("int64", longs(ids.map((id, i) => choose(id >= start, 0, i - 1))), [1, ids.length]),
      exaggeration: emotion,
    };
    const past: Record<string, TensorLike> = {};
    for (let l = 0; l < CHATTERBOX_LAYERS; l++) {
      for (const kv of ["key", "value"]) {
        past["past_key_values." + l + "." + kv] = new T("float32", new Float32Array(0), [1, CHATTERBOX_HEADS, 0, CHATTERBOX_HEAD]);
      }
    }
    const said = [start];
    let seen = 0;
    for (let i = 0; i < CHATTERBOX_MAX_TOKENS; i++) {
      let embeds = (await m.embed.run(feed)).inputs_embeds;
      if (i === 0) {
        // The voice comes before the text.
        const text = embeds.data as Float32Array;
        const both = new Float32Array(voice.features.length + text.length);
        both.set(voice.features);
        both.set(text, voice.features.length);
        embeds = new T("float32", both, [1, voice.featureLen + ids.length, CHATTERBOX_WIDTH]);
      }
      seen += (embeds.dims as number[])[1];
      const out = await m.lm.run({
        inputs_embeds: embeds,
        attention_mask: new T("int64", new BigInt64Array(seen).fill(1n), [1, seen]),
        ...past,
      });
      const [, steps, width] = out.logits.dims as number[];
      const token = nextToken((out.logits.data as Float32Array).subarray((steps - 1) * width), said);
      if (token === start + 1) {
        break;
      }
      said.push(token);
      feed = {
        input_ids: new T("int64", longs([token]), [1, 1]),
        position_ids: new T("int64", longs([i + 1]), [1, 1]),
        exaggeration: emotion,
      };
      for (let l = 0; l < CHATTERBOX_LAYERS; l++) {
        for (const kv of ["key", "value"]) {
          past["past_key_values." + l + "." + kv] = out["present." + l + "." + kv];
        }
      }
    }
    const tokens = [...voice.tokens, ...said.slice(1)];
    const wav = await m.decoder.run({
      speech_tokens: new T("int64", longs(tokens), [1, tokens.length]),
      speaker_embeddings: new T("float32", voice.embedding, [1, voice.embedding.length]),
      speaker_features: new T("float32", voice.speaker, [1, voice.speakerLen, CHATTERBOX_MELS]),
    });
    return wavOf(wav.waveform.data as Float32Array, CHATTERBOX_RATE);
  }

  // read is a file, its progress counted toward the warm in course.
  private read(url: string): Promise<ArrayBuffer> {
    return this.deps.bytes(url, (loaded, total) => this.counted(url, loaded, total));
  }

  // counted records one file's progress, and reports the whole once every file has started:
  // before that the total is not known, and the share would go backwards.
  private counted(url: string, loaded: number, total: number): void {
    const w = this.watch;
    if (!w?.files.has(url)) {
      return;
    }
    w.files.set(url, [loaded, Math.max(total, loaded)]);
    this.show();
  }

  // stepDone counts a step besides reading toward the warm in course.
  private stepDone(share: number): void {
    if (this.watch) {
      this.watch.steps += share;
      this.show();
    }
  }

  // show reports the warm's progress when it moved forward.
  private show(): void {
    const w = this.watch as NonNullable<Synth["watch"]>;
    let done = 0;
    let all = 0;
    for (const [l, t] of w.files.values()) {
      if (l < 0) {
        return;
      }
      done += l;
      all += t;
    }
    const pct = Math.floor((LOAD_SHARE * done) / Math.max(all, 1)) + w.steps;
    if (pct > w.shown) {
      w.shown = pct;
      w.report(pct);
    }
  }

  private runtime(): Promise<OrtLike> {
    this.ort ??= this.read(fileURL(RUNTIME_ID, ORT_WASM))
      .then((w) => this.deps.ort(w))
      .then((o) => {
        this.stepDone(RUNTIME_SHARE);
        return o;
      });
    this.ort.catch(() => {
      this.ort = null;
    });
    return this.ort;
  }

  private voice(id: string): Promise<LoadedVoice> {
    let v = this.voices.get(id);
    if (!v) {
      let load = () => this.loadPiper(id);
      if (isSupertonic(id)) {
        load = () => this.loadSupertonic(id);
      } else if (isChatterbox(id)) {
        load = () => this.loadChatterbox(id);
      }
      v = load();
      this.voices.set(id, v);
      // A voice that failed to load is tried again next time.
      v.catch(() => this.voices.delete(id));
    }
    return v;
  }

  private async loadPiper(id: string): Promise<LoadedVoice> {
    // The three are read at once, so a warm knows every size early.
    const [json, ort, model] = await Promise.all([
      this.read(fileURL(id, "model.json")),
      this.runtime(),
      this.read(fileURL(id, "model.onnx")),
    ]);
    const config = parseVoiceConfig(new TextDecoder().decode(json));
    const session = await ort.createSession(model);
    return { kind: "piper", config, session };
  }

  private async loadSupertonic(id: string): Promise<LoadedVoice> {
    const [style] = await Promise.all([this.read(fileURL(id, "voice.json")), this.supertonicModel()]);
    return { kind: "supertonic", style: supertonicStyle(new TextDecoder().decode(style)) };
  }

  // supertonicModel is Supertonic's model, loaded with the first of its voices: on WebGPU where
  // the browser has it, on the CPU otherwise or when WebGPU failed.
  private supertonicModel(): Promise<SupertonicModel> {
    this.supertonic ??= (async () => {
      const read = (n: string) => this.read(fileURL(SUPERTONIC_ID, n));
      const files = Promise.all(SUPERTONIC_PARTS.map(read));
      const indexer = read("unicode_indexer.json").then((b) => JSON.parse(new TextDecoder().decode(b)) as number[]);
      const start = async (ort: OrtLike, gpu: boolean): Promise<SupertonicModel> => {
        const parts: SessionLike[] = [];
        // One at a time: four sessions at once would hold every model twice in memory.
        for (const f of await files) {
          parts.push(await ort.createSession(f));
        }
        return { ort, parts, indexer: await indexer, gpu };
      };
      if (this.deps.gpu) {
        try {
          const ort = await read(ORT_GPU_WASM).then((w) => (this.deps.gpu as NonNullable<SynthDeps["gpu"]>)(w));
          return await start(ort, true);
        } catch (e) {
          // The processor's runtime says it instead, and the page is told why.
          this.failed = reasonOf(e);
        }
      }
      return start(await this.runtime(), false);
    })();
    this.supertonic.catch(() => {
      this.supertonic = null;
    });
    return this.supertonic;
  }

  private async loadChatterbox(id: string): Promise<LoadedVoice> {
    const [voice] = await Promise.all([this.read(fileURL(id, "voice.bin")), this.chatterboxModel()]);
    return { kind: "chatterbox", voice: chatterboxVoice(voice) };
  }

  // chatterboxModel is Chatterbox's model, loaded with its first voice. It runs only on WebGPU:
  // on the processor a sentence would take minutes.
  private chatterboxModel(): Promise<ChatterboxModel> {
    this.chatterbox ??= (async () => {
      const gpu = this.deps.gpu;
      if (!gpu) {
        throw new Error("Chatterbox needs WebGPU");
      }
      const read = (n: string) => this.read(fileURL(CHATTERBOX_ID, n));
      const tokenizer = read("tokenizer.json").then((b) => chatterboxTokenizer(new TextDecoder().decode(b)));
      const ort = await read(ORT_GPU_WASM).then((w) => gpu(w));
      const sessions: SessionLike[] = [];
      // One at a time, each with its weights, so only one is ever held twice in memory.
      for (const part of CHATTERBOX_PARTS) {
        const [model, data] = await Promise.all([read(part), read(part + "_data")]);
        sessions.push(await ort.createSession(model, { path: part + "_data", data }));
      }
      const [embed, lm, decoder] = sessions;
      return { ort, embed, lm, decoder, tokenizer: await tokenizer, gpu: true as const };
    })();
    this.chatterbox.catch(() => {
      this.chatterbox = null;
    });
    return this.chatterbox;
  }

  private phonemizer(): Promise<PhonemizerLike> {
    this.phon ??= Promise.all([
      this.read(fileURL(RUNTIME_ID, PHONEMIZE_WASM)),
      this.read(fileURL(RUNTIME_ID, PHONEMIZE_DATA)),
    ])
      .then(([wasm, data]) => this.deps.phonemizer(wasm, data, (line) => this.printed.push(line)))
      .then((p) => {
        this.stepDone(PHONEMIZER_SHARE);
        return p;
      });
    this.phon.catch(() => {
      this.phon = null;
    });
    return this.phon;
  }

  private async phonemize(espeakVoice: string, text: string): Promise<Phonemized> {
    const p = await this.phonemizer();
    this.printed = [];
    p.callMain(phonemizeArgs(espeakVoice, text));
    const line = this.printed.find((l) => l.trim().startsWith("{"));
    if (!line) {
      throw new Error("the phonemizer answered nothing");
    }
    return phonemized(line);
  }
}

// Tune scales a Piper voice's noise settings: noise its intonation (noise_scale), noiseW its
// rhythm (noise_w). 1 and 1 are the voice as it was trained. The other engines have neither.
export interface Tune {
  noise: number;
  noiseW: number;
  // lang is the sentence's language, which a Supertonic or Chatterbox voice is told.
  lang?: string;
  // exaggeration is how much emotion a Chatterbox voice puts in (0.25 to 2; 0.5 its default).
  exaggeration?: number;
  // speed is the pace a Supertonic voice speaks at (SUPERTONIC_SPEED its own), and steps how
  // many times its sound is refined (0 or none: SUPERTONIC_STEPS, by where it runs).
  speed?: number;
  steps?: number;
}

// The messages between the page and the worker.
export type ToWorker =
  | { kind: "say"; seq: number; id: string; text: string; tune?: Tune }
  | { kind: "warm"; id: string }
  | { kind: "forget"; id: string };
export type FromWorker =
  | { seq: number; wav: ArrayBuffer }
  | { seq: number; error: string }
  | { kind: "progress"; id: string; pct: number }
  | { kind: "ready"; id: string; gpu?: boolean; gpuError?: string }
  | { kind: "failed"; id: string; error: string };
