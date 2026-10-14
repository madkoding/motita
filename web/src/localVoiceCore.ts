// LOCAL VOICES: a Piper model run in this browser, the parts that need no browser.
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
// VOICE_CACHE is the Cache Storage this page keeps the files in.
export const VOICE_CACHE = "motita-voices-v1";

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

// phonemeIds reads the phonemizer's output line: the ids the model takes.
export function phonemeIds(line: string): number[] {
  const out = JSON.parse(line) as { phoneme_ids?: number[] };
  if (!Array.isArray(out.phoneme_ids) || out.phoneme_ids.length === 0) {
    throw new Error("the phonemizer answered nothing");
  }
  return out.phoneme_ids;
}

// wavOf is mono 16-bit PCM audio, as a WAV file the <audio> element plays.
export function wavOf(samples: Float32Array, rate: number): ArrayBuffer {
  const header = 44;
  const view = new DataView(new ArrayBuffer(header + samples.length * 2));
  const text = (at: number, s: string) => {
    for (let i = 0; i < s.length; i++) {
      view.setUint8(at + i, s.charCodeAt(i));
    }
  };
  text(0, "RIFF");
  view.setUint32(4, view.byteLength - 8, true);
  text(8, "WAVE");
  text(12, "fmt ");
  view.setUint32(16, 16, true);
  view.setUint16(20, 1, true); // PCM
  view.setUint16(22, 1, true); // mono
  view.setUint32(24, rate, true);
  view.setUint32(28, rate * 2, true);
  view.setUint16(32, 2, true);
  view.setUint16(34, 16, true);
  text(36, "data");
  view.setUint32(40, samples.length * 2, true);
  for (let i = 0; i < samples.length; i++) {
    const s = Math.max(-1, Math.min(1, samples[i]));
    view.setInt16(header + i * 2, Math.round(s * 32767), true);
  }
  return view.buffer;
}

// What the synthesis needs from onnxruntime, so a test can stand in.
export interface TensorLike {
  data: unknown;
}
export interface SessionLike {
  run(feeds: Record<string, TensorLike>): Promise<Record<string, TensorLike>>;
}
export interface OrtLike {
  Tensor: new (type: "int64" | "float32", data: BigInt64Array | Float32Array, dims: number[]) => TensorLike;
  createSession(model: ArrayBuffer): Promise<SessionLike>;
}

// A phonemizer instance: callMain runs it once, and what it prints arrives through print.
export interface PhonemizerLike {
  callMain(args: string[]): void;
}

// SynthDeps are what a Synth reads files with and runs them on.
export interface SynthDeps {
  // bytes is a downloaded file, from the page's own copy when there is one.
  bytes(url: string): Promise<ArrayBuffer>;
  // ort loads the runtime the first time it is asked, given its WebAssembly.
  ort(wasm: ArrayBuffer): Promise<OrtLike>;
  // phonemizer builds one instance from its WebAssembly and data; print receives its output.
  phonemizer(wasm: ArrayBuffer, data: ArrayBuffer, print: (line: string) => void): Promise<PhonemizerLike>;
}

interface LoadedVoice {
  config: VoiceConfig;
  session: SessionLike;
}

// Synth says texts with the local voices: the runtime and the phonemizer are loaded once, and
// each voice the first time it speaks. Requests run one at a time, in order.
export class Synth {
  private readonly deps: SynthDeps;
  private ort: Promise<OrtLike> | null = null;
  private phon: Promise<PhonemizerLike> | null = null;
  private printed: string[] = [];
  private readonly voices = new Map<string, Promise<LoadedVoice>>();
  private queue: Promise<unknown> = Promise.resolve();

  constructor(deps: SynthDeps) {
    this.deps = deps;
  }

  // say is one text as WAV audio.
  say(id: string, text: string): Promise<ArrayBuffer> {
    const job = this.queue.then(() => this.sayNow(id, text));
    this.queue = job.catch(() => undefined);
    return job;
  }

  // warm loads a voice, the runtime and the phonemizer ahead of the first sentence, which then
  // starts as soon as it is synthesised.
  warm(id: string): Promise<void> {
    const job = this.queue.then(async () => {
      await this.voice(id);
      await this.phonemizer();
    });
    this.queue = job.catch(() => undefined);
    return job;
  }

  // forget drops a voice, so the next request loads it again (after it was deleted, say).
  forget(id: string): void {
    this.voices.delete(id);
  }

  private async sayNow(id: string, text: string): Promise<ArrayBuffer> {
    const v = await this.voice(id);
    const ids = await this.phonemize(v.config.espeak.voice, text);
    const ort = await this.runtime();
    const inf = v.config.inference;
    const feeds: Record<string, TensorLike> = {
      input: new ort.Tensor("int64", BigInt64Array.from(ids.map(BigInt)), [1, ids.length]),
      input_lengths: new ort.Tensor("int64", BigInt64Array.from([BigInt(ids.length)]), [1]),
      scales: new ort.Tensor("float32", Float32Array.from([inf.noise_scale, inf.length_scale, inf.noise_w]), [3]),
    };
    if ((v.config.num_speakers ?? 1) > 1) {
      feeds.sid = new ort.Tensor("int64", BigInt64Array.from([0n]), [1]);
    }
    const out = await v.session.run(feeds);
    return wavOf(out.output.data as Float32Array, v.config.audio.sample_rate);
  }

  private runtime(): Promise<OrtLike> {
    this.ort ??= this.deps.bytes(fileURL(RUNTIME_ID, ORT_WASM)).then((w) => this.deps.ort(w));
    this.ort.catch(() => {
      this.ort = null;
    });
    return this.ort;
  }

  private voice(id: string): Promise<LoadedVoice> {
    let v = this.voices.get(id);
    if (!v) {
      v = (async () => {
        const config = parseVoiceConfig(new TextDecoder().decode(await this.deps.bytes(fileURL(id, "model.json"))));
        const ort = await this.runtime();
        const session = await ort.createSession(await this.deps.bytes(fileURL(id, "model.onnx")));
        return { config, session };
      })();
      this.voices.set(id, v);
      // A voice that failed to load is tried again next time.
      v.catch(() => this.voices.delete(id));
    }
    return v;
  }

  private phonemizer(): Promise<PhonemizerLike> {
    this.phon ??= Promise.all([
      this.deps.bytes(fileURL(RUNTIME_ID, PHONEMIZE_WASM)),
      this.deps.bytes(fileURL(RUNTIME_ID, PHONEMIZE_DATA)),
    ]).then(([wasm, data]) => this.deps.phonemizer(wasm, data, (line) => this.printed.push(line)));
    this.phon.catch(() => {
      this.phon = null;
    });
    return this.phon;
  }

  private async phonemize(espeakVoice: string, text: string): Promise<number[]> {
    const p = await this.phonemizer();
    this.printed = [];
    p.callMain(phonemizeArgs(espeakVoice, text));
    const line = this.printed.find((l) => l.trim().startsWith("{"));
    if (!line) {
      throw new Error("the phonemizer answered nothing");
    }
    return phonemeIds(line);
  }
}

// The messages between the page and the worker.
export type ToWorker =
  | { kind: "say"; seq: number; id: string; text: string }
  | { kind: "warm"; id: string }
  | { kind: "forget"; id: string };
export type FromWorker = { seq: number; wav: ArrayBuffer } | { seq: number; error: string };
