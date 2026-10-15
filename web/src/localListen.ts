// LOCAL SPEECH INPUT on the page's side: the worker that runs Whisper (speechListen.worker.ts),
// how far a model is from loaded, and a recording turned into the sound Whisper reads.
//
// The models are downloaded, listed and deleted like the local voices (localVoice.ts): they are
// part of the same catalog, with engine "whisper".
import { WHISPER_GPU, WHISPER_RATE, type FromListener, type ToListener } from "./localListenCore";
import { choose } from "./choose";
import type { LoadState } from "./localVoice";

let worker: Worker | null = null;
let seq = 0;
const pending = new Map<number, { ok: (text: string) => void; fail: (e: Error) => void }>();

function getWorker(): Worker {
  if (!worker) {
    const w = new Worker(new URL("./speechListen.worker.ts", import.meta.url), { type: "module" });
    worker = w;
    w.onmessage = (e: MessageEvent<FromListener>) => {
      const m = e.data;
      if ("kind" in m) {
        loaded(m);
        return;
      }
      const p = pending.get(m.seq);
      pending.delete(m.seq);
      if (!p) {
        return;
      }
      if ("text" in m) {
        p.ok(m.text);
        return;
      }
      p.fail(new Error(m.error));
    };
    w.onerror = () => {
      for (const p of pending.values()) {
        p.fail(new Error("the local listener stopped"));
      }
      pending.clear();
      worker = null;
      // The models it had loaded went with it: a new worker loads them again.
      for (const id of loads.keys()) {
        setLoad(id, undefined);
      }
    };
  }
  return worker;
}

const loads = new Map<string, LoadState>();
const loadListeners = new Set<() => void>();

function setLoad(id: string, state: LoadState | undefined): void {
  if (state) {
    loads.set(id, state);
  } else {
    loads.delete(id);
  }
  for (const f of loadListeners) {
    f();
  }
}

// loaded records what the worker said about a model it is loading.
function loaded(m: Extract<FromListener, { kind: string }>): void {
  if (m.kind === "progress") {
    setLoad(m.id, { pct: m.pct, ready: false });
    return;
  }
  if (m.kind === "ready") {
    setLoad(m.id, {
      pct: 100,
      ready: true,
      ...choose(m.gpu, { gpu: true }, {}),
      ...choose(m.gpuError, { gpuError: m.gpuError }, {}),
    });
    return;
  }
  setLoad(m.id, { pct: 0, ready: false, error: m.error });
}

// listenStateOf is how far a model is from being ready here; undefined before it was asked for.
export function listenStateOf(id: string): LoadState | undefined {
  return loads.get(id);
}

// onListenState calls f whenever a model's loading changes, until the returned function is called.
export function onListenState(f: () => void): () => void {
  loadListeners.add(f);
  return () => loadListeners.delete(f);
}

// warmListener loads a model in the worker ahead of the first recording. A model loading or
// loaded is not asked for again; one that failed is.
export function warmListener(id: string): void {
  const now = loads.get(id);
  if (now && !now.error) {
    return;
  }
  setLoad(id, { pct: 0, ready: false });
  getWorker().postMessage({ kind: "warm", id } satisfies ToListener);
}

// forgetListener drops a model from the worker (it was deleted), so it is never used from memory.
export function forgetListener(id: string): void {
  worker?.postMessage({ kind: "forget", id } satisfies ToListener);
  setLoad(id, undefined);
}

// samplesOf is a recording as Whisper hears it: 16 kHz, its channels mixed into one. The browser
// decodes whatever it recorded (webm, mp4, ogg) and resamples it on the way.
export async function samplesOf(audio: Blob): Promise<Float32Array> {
  const ctx = new OfflineAudioContext(1, 1, WHISPER_RATE);
  const buf = await ctx.decodeAudioData(await audio.arrayBuffer());
  const out = new Float32Array(buf.length);
  for (let c = 0; c < buf.numberOfChannels; c++) {
    const ch = buf.getChannelData(c);
    for (let i = 0; i < out.length; i++) {
      out[i] += ch[i] / buf.numberOfChannels;
    }
  }
  return out;
}

// hearLocal writes down what a recording says, with a model of the catalog run in this browser.
// lang is the language spoken, or "" to let the model tell.
export async function hearLocal(id: string, audio: Blob, lang: string): Promise<string> {
  const samples = await samplesOf(audio);
  warmListener(id);
  return new Promise<string>((ok, fail) => {
    seq++;
    pending.set(seq, { ok, fail });
    getWorker().postMessage({ kind: "hear", seq, id, samples, lang } satisfies ToListener, [samples.buffer]);
  });
}

// localListenSupported says this browser can run a model to listen with at all.
export function localListenSupported(): boolean {
  return (
    typeof Worker !== "undefined" &&
    typeof WebAssembly !== "undefined" &&
    typeof caches !== "undefined" &&
    typeof OfflineAudioContext !== "undefined"
  );
}

// forGPU says a model of the catalog is the one built for the graphics card.
export function forGPU(id: string): boolean {
  return id.endsWith(WHISPER_GPU);
}
