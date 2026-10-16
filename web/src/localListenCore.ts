// LOCAL SPEECH INPUT: Whisper run in the browser, on WebAssembly or WebGPU, with no transcription
// service. What can be decided without onnxruntime lives here, so it can be tested without one:
// the sound turned into what Whisper reads (a log-mel spectrogram), the tokens it writes turned
// back into text, and the decoding loop itself, run on whatever sessions it is handed.
//
// The model's files come from the gateway, like the local voices' (see localVoiceCore.ts): it
// downloads them once from a fixed catalog, checks their hashes, and serves them from this origin.
import { choose } from "./choose";
import {
  ORT_GPU_WASM,
  NO_WEBGPU,
  ORT_WASM,
  RUNTIME_ID,
  fileURL,
  noWebGPU,
  reasonOf,
  type OrtLike,
  type SessionLike,
  type TensorLike,
} from "./localVoiceCore";

// WHISPER_ID starts the id of every Whisper model of the catalog; WHISPER_GPU ends the one built
// for the graphics card.
export const WHISPER_ID = "whisper";
export const WHISPER_GPU = "-gpu";

// isWhisper says whether a catalog id is a speech input model.
export function isWhisper(id: string): boolean {
  return id.startsWith(WHISPER_ID + "-");
}

// The files of a Whisper model, under the same names whichever model it is.
export const WHISPER_FILES = {
  tokenizer: "tokenizer.json",
  generation: "generation_config.json",
  encoder: "encoder.onnx",
  decoder: "decoder.onnx",
};

// What Whisper hears: 16 kHz mono, in windows of 30 seconds, read as 80 mel bands of 25 ms frames
// every 10 ms.
export const WHISPER_RATE = 16000;
export const N_FFT = 400;
export const HOP = 160;
export const N_MELS = 80;
const CHUNK_SECONDS = 30;
export const CHUNK_SAMPLES = CHUNK_SECONDS * WHISPER_RATE;
export const N_FRAMES = CHUNK_SAMPLES / HOP;
// Every Whisper attention head is 64 wide: the number of heads is the model's width over that.
const HEAD_DIM = 64;

// The most the progress says while a model is still loading.
const ALMOST_DONE = 99;

// The share of loading each part takes, for the progress the page shows.
const SHARES = { runtime: 15, encoder: 25, decoder: 55, text: 5 };

// Slaney's mel scale: linear below 1 kHz, 3 mels every 200 Hz, then logarithmic, 27 mels for each
// factor of 6.4 in frequency.
const SLANEY_HZ = 200;
const SLANEY_MELS = 3;
const SLANEY_LOG_HZ = 1000;
const SLANEY_LOG_RATIO = 6.4;
const SLANEY_LOG_MELS = 27;

// melFilters is the mel filter bank Whisper was trained with (librosa's, Slaney's scale and
// normalisation): nMels rows of nFft / 2 + 1 weights, one per frequency of the transform.
export function melFilters(
  nMels = N_MELS,
  nFft = N_FFT,
  rate = WHISPER_RATE,
): Float32Array[] {
  const fSp = SLANEY_HZ / SLANEY_MELS;
  const minLogHz = SLANEY_LOG_HZ;
  const minLogMel = minLogHz / fSp;
  const logStep = Math.log(SLANEY_LOG_RATIO) / SLANEY_LOG_MELS;
  const toHz = (mel: number) => {
    if (mel < minLogMel) {
      return mel * fSp;
    }
    return minLogHz * Math.exp(logStep * (mel - minLogMel));
  };
  const bins = nFft / 2 + 1;
  // The highest frequency is far above 1 kHz, on the scale's logarithmic part.
  const top = minLogMel + Math.log(rate / 2 / minLogHz) / logStep;
  const hz = Array.from({ length: nMels + 2 }, (_, i) =>
    toHz((top * i) / (nMels + 1)),
  );
  const out: Float32Array[] = [];
  for (let m = 0; m < nMels; m++) {
    const row = new Float32Array(bins);
    const norm = 2 / (hz[m + 2] - hz[m]);
    for (let k = 0; k < bins; k++) {
      const f = (k * rate) / nFft;
      const lower = (f - hz[m]) / (hz[m + 1] - hz[m]);
      const upper = (hz[m + 2] - f) / (hz[m + 2] - hz[m + 1]);
      row[k] = Math.max(0, Math.min(lower, upper)) * norm;
    }
    out.push(row);
  }
  return out;
}

// The tables of the transform, made once: the window, and the cosines and sines of every bin.
let tables: {
  win: Float32Array;
  cos: Float32Array;
  sin: Float32Array;
  mel: Float32Array[];
} | null = null;
function transformTables() {
  if (!tables) {
    const bins = N_FFT / 2 + 1;
    const win = new Float32Array(N_FFT);
    for (let n = 0; n < N_FFT; n++) {
      win[n] = 0.5 - 0.5 * Math.cos((2 * Math.PI * n) / N_FFT);
    }
    const cos = new Float32Array(bins * N_FFT);
    const sin = new Float32Array(bins * N_FFT);
    for (let k = 0; k < bins; k++) {
      for (let n = 0; n < N_FFT; n++) {
        const a = (2 * Math.PI * ((k * n) % N_FFT)) / N_FFT;
        cos[k * N_FFT + n] = Math.cos(a);
        sin[k * N_FFT + n] = Math.sin(a);
      }
    }
    tables = { win, cos, sin, mel: melFilters() };
  }
  return tables;
}

// reflect is the index a centred frame reads at i of a signal n long, mirrored at both ends.
function reflect(i: number, n: number): number {
  if (i < 0) {
    return -i;
  }
  if (i >= n) {
    return 2 * (n - 1) - i;
  }
  return i;
}

// logMel is the spectrogram Whisper reads, N_MELS rows of N_FRAMES, from up to 30 seconds of
// 16 kHz samples (padded with silence, or cut: a longer recording is cut into windows first, see
// windowsOf). It follows Whisper's own: a centred transform with
// a Hann window, power in mel bands, log10, at most 8 below the loudest, then scaled.
export function logMel(samples: Float32Array): Float32Array {
  const { mel } = transformTables();
  const audio = new Float32Array(CHUNK_SAMPLES);
  audio.set(samples.subarray(0, CHUNK_SAMPLES));
  const out = new Float32Array(N_MELS * N_FRAMES);
  // Past the sound everything is silence: those frames are all the same floor.
  const heard = Math.min(
    N_FRAMES,
    Math.ceil((Math.min(samples.length, CHUNK_SAMPLES) + N_FFT / 2) / HOP) + 1,
  );
  const power = new Float32Array(N_FFT / 2 + 1);
  let loudest = -Infinity;
  for (let t = 0; t < N_FRAMES; t++) {
    if (t < heard) {
      framePower(audio, t, power);
    } else {
      power.fill(0);
    }
    for (let m = 0; m < N_MELS; m++) {
      const v = Math.log10(Math.max(dot(mel[m], power), 1e-10));
      out[m * N_FRAMES + t] = v;
      loudest = Math.max(loudest, v);
    }
  }
  const floor = loudest - 8;
  for (let i = 0; i < out.length; i++) {
    out[i] = (Math.max(out[i], floor) + 4) / 4;
  }
  return out;
}

// framePower writes into power the power of each frequency of frame t: its samples, centred on
// t * HOP and read past the ends mirrored, through the Hann window and the transform.
function framePower(audio: Float32Array, t: number, power: Float32Array): void {
  const { win, cos, sin } = transformTables();
  const frame = new Float32Array(N_FFT);
  for (let n = 0; n < N_FFT; n++) {
    frame[n] = audio[reflect(t * HOP + n - N_FFT / 2, CHUNK_SAMPLES)] * win[n];
  }
  for (let k = 0; k < power.length; k++) {
    const at = k * N_FFT;
    const re = dot(frame, cos, at);
    const im = dot(frame, sin, at);
    power[k] = re * re + im * im;
  }
}

// dot is the sum of a's values times b's, b read from its index at.
function dot(a: Float32Array, b: Float32Array, at = 0): number {
  let sum = 0;
  for (let i = 0, j = at; i < a.length; i++, j++) {
    sum += a[i] * b[j];
  }
  return sum;
}

// byteDecoder maps the characters of GPT-2's byte-level vocabulary back to the bytes they stand
// for: printable bytes stand for themselves, the rest were moved past 255.
function byteDecoder(): Map<string, number> {
  // GPT-2's printable bytes: "!" to "~", "¡" to "¬", and "®" to "ÿ".
  const ranges = ["!~", "¡¬", "®ÿ"].map((r) => [
    r.codePointAt(0) as number,
    r.codePointAt(1) as number,
  ]);
  const printable = (b: number) =>
    ranges.some(([lo, hi]) => b >= lo && b <= hi);
  const map = new Map<string, number>();
  let moved = 0;
  for (let b = 0; b < 256; b++) {
    if (printable(b)) {
      map.set(String.fromCodePoint(b), b);
    } else {
      map.set(String.fromCodePoint(256 + moved), b);
      moved++;
    }
  }
  return map;
}

// Vocabulary is what the decoding needs from Whisper's tokenizer: each id's text, and the ids of
// the tokens that steer it.
export interface Vocabulary {
  tokens: string[];
  eot: number;
  sot: number;
  transcribe: number;
  noTimestamps: number;
  noSpeech: number;
  // langs are the language tokens' ids by their code (es, en…).
  langs: Map<string, number>;
}

// vocabulary reads Whisper's tokenizer.json.
export function vocabulary(json: string): Vocabulary {
  const tk = JSON.parse(json) as {
    model: { vocab: Record<string, number> };
    added_tokens: { id: number; content: string }[];
  };
  const tokens: string[] = [];
  for (const [text, id] of Object.entries(tk.model.vocab)) {
    tokens[id] = text;
  }
  const named = new Map<string, number>();
  for (const a of tk.added_tokens) {
    tokens[a.id] = a.content;
    named.set(a.content, a.id);
  }
  const idOf = (name: string) => {
    const n = named.get(name);
    if (n === undefined) {
      throw new Error("the tokenizer has no " + name);
    }
    return n;
  };
  const langs = new Map<string, number>();
  for (const [name, n] of named) {
    const m = /^<\|([a-z]{2,3}|haw|yue)\|>$/.exec(name);
    if (m) {
      langs.set(m[1], n);
    }
  }
  return {
    tokens,
    eot: idOf("<|endoftext|>"),
    sot: idOf("<|startoftranscript|>"),
    transcribe: idOf("<|transcribe|>"),
    noTimestamps: idOf("<|notimestamps|>"),
    noSpeech: named.get("<|nospeech|>") ?? idOf("<|nocaptions|>"),
    langs,
  };
}

let bytesOf: Map<string, number> | null = null;

// textOf is what tokens say: the special ones (from the end of text on) are left out. Every id
// below them is a word of the vocabulary.
export function textOf(ids: number[], v: Vocabulary): string {
  bytesOf ??= byteDecoder();
  const bytes: number[] = [];
  for (const id of ids) {
    if (id >= v.eot) {
      continue;
    }
    for (const ch of v.tokens[id]) {
      bytes.push(bytesOf.get(ch) ?? 32);
    }
  }
  return new TextDecoder().decode(Uint8Array.from(bytes)).trim();
}

// Generation is what the model's generation_config.json says about the tokens it must not write.
export interface Generation {
  suppress: number[];
  beginSuppress: number[];
}

export function generation(json: string): Generation {
  const g = JSON.parse(json) as {
    suppress_tokens?: number[];
    begin_suppress_tokens?: number[];
  };
  return {
    suppress: g.suppress_tokens ?? [],
    beginSuppress: g.begin_suppress_tokens ?? [],
  };
}

// NO_SPEECH is how sure the model must be that nothing was said to answer nothing: it writes
// thanks for watching, or a subtitler's credit, over silence otherwise.
export const NO_SPEECH = 0.6;

// noSpeechOf is the chance the model gives to nothing being said, from its first logits.
export function noSpeechOf(logits: Float32Array, v: Vocabulary): number {
  let top = -Infinity;
  for (const x of logits) {
    top = Math.max(top, x);
  }
  let sum = 0;
  for (const x of logits) {
    sum += Math.exp(x - top);
  }
  return Math.exp(logits[v.noSpeech] - top) / sum;
}

// languageOf is the language the model hears, among the ones its tokenizer names.
export function languageOf(logits: Float32Array, v: Vocabulary): number {
  let best = -1;
  for (const id of v.langs.values()) {
    if (best < 0 || logits[id] > logits[best]) {
      best = id;
    }
  }
  return best;
}

// nextOf is the token the model writes next: the likeliest it may write. Only the end of text
// and words may be written (no timestamps or other steering tokens); first never ends the text
// nor opens with a bare space.
export function nextOf(
  logits: Float32Array,
  v: Vocabulary,
  g: Generation,
  first: boolean,
): number {
  const banned = new Set(g.suppress);
  if (first) {
    for (const id of g.beginSuppress) {
      banned.add(id);
    }
  }
  let best = -1;
  for (let id = 0; id <= v.eot; id++) {
    if (!banned.has(id) && (best < 0 || logits[id] > logits[best])) {
      best = id;
    }
  }
  return best;
}

// MAX_TOKENS is the most Whisper writes for one window: half its context of 448.
const MAX_TOKENS = 224;

// maxTokens is how many tokens a recording may be written in: speech is never more than a few per
// second, and a model stuck repeating itself stops there.
export function maxTokens(samples: number): number {
  return Math.min(MAX_TOKENS, Math.ceil((samples / WHISPER_RATE) * 8) + 16);
}

// windowsOf cuts a recording into the 30-second windows Whisper reads, one after the other; a
// recording with nothing in it is still one window, so it is answered like any other. What is left
// after the last whole window, when it is under MIN_TAIL_SAMPLES (half a second: a breath, or the
// end of a button press), is not a window of its own: it would cost a full pass of the encoder and
// the decoder to hear nothing.
export function windowsOf(samples: Float32Array): Float32Array[] {
  const out: Float32Array[] = [];
  for (let at = 0; at === 0 || at < samples.length; at += CHUNK_SAMPLES) {
    const part = samples.subarray(at, at + CHUNK_SAMPLES);
    if (at > 0 && part.length < MIN_TAIL_SAMPLES) {
      break;
    }
    out.push(part);
  }
  return out;
}

const MIN_TAIL_SAMPLES = WHISPER_RATE / 2;

// lastLogits is the row of a [1, n, vocab] output for its last position.
function lastLogits(t: TensorLike): Float32Array {
  const data = t.data as Float32Array;
  const [, n, vocab] = t.dims as readonly number[];
  return data.subarray((n - 1) * vocab, n * vocab);
}

// Hearing is what Whisper made of one recording: its text, and the language it heard.
export interface Hearing {
  text: string;
  lang: string;
}

// ListenerDeps are what a Listener reads files with and runs them on: as SynthDeps.
export interface ListenerDeps {
  bytes(
    url: string,
    progress?: (loaded: number, total: number) => void,
  ): Promise<ArrayBuffer>;
  ort(wasm: ArrayBuffer): Promise<OrtLike>;
  gpu?(wasm: ArrayBuffer): Promise<OrtLike>;
  webGPU?(): boolean;
}

// A model ready to listen.
interface LoadedModel {
  ort: OrtLike;
  encoder: SessionLike;
  decoder: SessionLike;
  layers: number;
  vocab: Vocabulary;
  gen: Generation;
  gpu: boolean;
  gpuError: string;
}

// layersOf is how many decoder layers a merged decoder has, from the cache inputs it names.
export function layersOf(inputNames: readonly string[]): number {
  return inputNames.filter((n) =>
    /^past_key_values\.\d+\.decoder\.key$/.test(n),
  ).length;
}

// Listener turns recordings into text with the Whisper models of the catalog: each is loaded the
// first time it is asked for, and kept. Requests run one at a time, in order.
export class Listener {
  private readonly deps: ListenerDeps;
  private readonly models = new Map<string, Promise<LoadedModel>>();
  private queue: Promise<unknown> = Promise.resolve();

  constructor(deps: ListenerDeps) {
    this.deps = deps;
  }

  // warm loads a model, telling progress how far it is (0 to 100).
  warm(
    id: string,
    progress?: (pct: number) => void,
  ): Promise<{ gpu: boolean; gpuError: string }> {
    return this.model(id, progress).then((m) => ({
      gpu: m.gpu,
      gpuError: m.gpuError,
    }));
  }

  // forget drops a model, so the next request loads it again (after it was deleted, say).
  forget(id: string): void {
    this.models.delete(id);
  }

  // hear writes down what a recording (16 kHz mono) says. lang is the language spoken, or "" to
  // let the model tell.
  hear(id: string, samples: Float32Array, lang: string): Promise<Hearing> {
    const job = this.queue.then(() => this.hearNow(id, samples, lang));
    this.queue = job.catch(() => undefined);
    return job;
  }

  private model(
    id: string,
    progress?: (pct: number) => void,
  ): Promise<LoadedModel> {
    let m = this.models.get(id);
    if (!m) {
      m = this.load(id, progress ?? (() => undefined));
      this.models.set(id, m);
      m.catch(() => this.models.delete(id));
    }
    return m;
  }

  // load reads a model's files and starts its sessions: on the graphics card for the model built
  // for it, where the browser has one, and on the processor otherwise or when that failed.
  private async load(
    id: string,
    progress: (pct: number) => void,
  ): Promise<LoadedModel> {
    let done = 0;
    const read = (url: string, share: number) => {
      let last = 0;
      return this.deps.bytes(url, (got, total) => {
        const now = (share * got) / Math.max(total, got, 1);
        done += now - last;
        last = now;
        // 100 % is said once everything is loaded, not when the last byte arrives.
        progress(Math.min(ALMOST_DONE, Math.floor(done)));
      });
    };
    progress(0);
    const text = Promise.all([
      read(fileURL(id, WHISPER_FILES.tokenizer), SHARES.text / 2),
      read(fileURL(id, WHISPER_FILES.generation), SHARES.text / 2),
    ]);
    // WebGPU's runtime is fetched only where the browser has WebGPU: the processor's then takes
    // its share of the progress.
    const loadGPU = choose(id.endsWith(WHISPER_GPU), this.deps.gpu, undefined);
    let ort: OrtLike | null = null;
    let gpuError = "";
    let tried = false;
    if (loadGPU && noWebGPU(this.deps)) {
      gpuError = NO_WEBGPU;
    } else if (loadGPU) {
      tried = true;
      try {
        ort = await loadGPU(
          await read(fileURL(RUNTIME_ID, ORT_GPU_WASM), SHARES.runtime),
        );
      } catch (e) {
        gpuError = reasonOf(e);
      }
    } else {
      // A model that does not run on WebGPU has no runtime of it to fetch.
    }
    const gpu = ort !== null;
    ort ??= await this.deps.ort(
      await read(
        fileURL(RUNTIME_ID, ORT_WASM),
        choose(tried, 0, SHARES.runtime),
      ),
    );
    // One at a time, so only one model is ever held twice in memory.
    const encoder = await ort.createSession(
      await read(fileURL(id, WHISPER_FILES.encoder), SHARES.encoder),
    );
    const decoder = await ort.createSession(
      await read(fileURL(id, WHISPER_FILES.decoder), SHARES.decoder),
    );
    const [tok, gen] = await text;
    const dec = new TextDecoder();
    const loaded: LoadedModel = {
      ort,
      encoder,
      decoder,
      layers: layersOf(decoder.inputNames ?? []),
      vocab: vocabulary(dec.decode(tok)),
      gen: generation(dec.decode(gen)),
      gpu,
      gpuError,
    };
    progress(100);
    return loaded;
  }

  // hearNow writes a recording down window by window: Whisper reads 30 seconds at a time, and a
  // dictation may last longer. The language heard in the first window that said anything is the
  // one the rest are written in.
  private async hearNow(
    id: string,
    samples: Float32Array,
    lang: string,
  ): Promise<Hearing> {
    const m = await this.model(id);
    const said: string[] = [];
    let heard = lang;
    for (const window of windowsOf(samples)) {
      const h = await this.hearWindow(m, window, heard);
      if (h.text) {
        said.push(h.text);
        heard = h.lang;
      }
    }
    return { text: said.join(" "), lang: heard };
  }

  // hearWindow writes down one window of up to 30 seconds.
  private async hearWindow(
    m: LoadedModel,
    samples: Float32Array,
    lang: string,
  ): Promise<Hearing> {
    const T = m.ort.Tensor;
    const v = m.vocab;
    const enc = await m.encoder.run({
      input_features: new T("float32", logMel(samples), [1, N_MELS, N_FRAMES]),
    });
    const hidden = enc.last_hidden_state;
    const width = (hidden.dims as readonly number[])[2];
    const heads = width / HEAD_DIM;
    // The first pass reads only the start of the transcript, with nothing in the cache: it says
    // whether anything was said, and in what language.
    const empty = new T("float32", new Float32Array(0), [
      1,
      heads,
      0,
      HEAD_DIM,
    ]);
    const past: Record<string, TensorLike> = {};
    for (let l = 0; l < m.layers; l++) {
      for (const part of ["decoder", "encoder"]) {
        past[`past_key_values.${l}.${part}.key`] = empty;
        past[`past_key_values.${l}.${part}.value`] = empty;
      }
    }
    const step = async (ids: number[], cached: boolean) => {
      const out = await m.decoder.run({
        input_ids: new T("int64", BigInt64Array.from(ids.map(BigInt)), [
          1,
          ids.length,
        ]),
        encoder_hidden_states: hidden,
        use_cache_branch: new T(
          "bool",
          Uint8Array.from([choose(cached, 1, 0)]),
          [1],
        ),
        ...past,
      });
      for (const [name, t] of Object.entries(out)) {
        // The encoder's keys and values are made once, by the first pass.
        if (
          name.startsWith("present.") &&
          !(cached && name.includes(".encoder."))
        ) {
          past["past_key_values." + name.slice("present.".length)] = t;
        }
      }
      return out.logits;
    };
    const first = lastLogits(await step([v.sot], false));
    if (noSpeechOf(first, v) > NO_SPEECH) {
      return { text: "", lang };
    }
    const langId = v.langs.get(lang) ?? languageOf(first, v);
    const heard = [...v.langs].find(([, n]) => n === langId)?.[0] ?? lang;
    let logits = lastLogits(
      await step([langId, v.transcribe, v.noTimestamps], true),
    );
    const said: number[] = [];
    const most = maxTokens(samples.length);
    for (;;) {
      const next = nextOf(logits, v, m.gen, said.length === 0);
      if (next === v.eot || said.length >= most) {
        break;
      }
      said.push(next);
      logits = lastLogits(await step([next], true));
    }
    return { text: textOf(said, v), lang: heard };
  }
}

// The messages between the page and the listening worker.
export type ToListener =
  | { kind: "warm"; id: string }
  | { kind: "forget"; id: string }
  | {
      kind: "hear";
      seq: number;
      id: string;
      samples: Float32Array;
      lang: string;
    };
export type FromListener =
  | { seq: number; text: string; lang: string }
  | { seq: number; error: string }
  | { kind: "progress"; id: string; pct: number }
  | { kind: "ready"; id: string; gpu?: boolean; gpuError?: string }
  | { kind: "failed"; id: string; error: string };
