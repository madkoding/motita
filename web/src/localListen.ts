// LOCAL SPEECH INPUT on the page's side: the worker that runs Whisper (speechListen.worker.ts),
// how far a model is from loaded, and a recording turned into the sound Whisper reads.
//
// The models are downloaded, listed and deleted like the local voices (localVoice.ts): they are
// part of the same catalog, with engine "whisper".
import { workerName } from "./gpuPolicy";
import { WHISPER_GPU, WHISPER_RATE, type FromListener, type ToListener } from "./localListenCore";
import { choose } from "./choose";
import type { LoadState } from "./localVoice";
import { t } from "./i18n";

// How long a recording may take to be written down before the worker is given up on: a worker
// that ran out of memory (on a phone, say) never answers, and "transcribing" must not last
// forever. A slow processor writes down several times slower than real time, so the time grows
// with the recording; it starts again whenever the model being loaded makes progress.
export const HEAR_BASE_MS = 120_000;
export const HEAR_PER_SECOND_MS = 5_000;

// A recording being written down: how it is answered, the model it waits on, and its clock.
interface Pending {
  ok: (text: string) => void;
  fail: (e: Error) => void;
  id: string;
  ms: number;
  timer?: ReturnType<typeof setTimeout>;
}

let worker: Worker | null = null;
let seq = 0;
const pending = new Map<number, Pending>();

// settle takes a request out of the waiting ones, its clock stopped.
function settle(n: number): Pending | undefined {
  const p = pending.get(n);
  pending.delete(n);
  if (p) {
    clearTimeout(p.timer);
  }
  return p;
}

// drop gives up on the worker: what it was asked fails with reason, and the models it had loaded
// went with it, so a new worker loads them again.
function drop(reason: string): void {
  for (const n of pending.keys()) {
    settle(n)?.fail(new Error(reason));
  }
  worker = null;
  for (const id of loads.keys()) {
    setLoad(id, undefined);
  }
}

// arm (re)starts the clock of request n.
function arm(n: number, p: Pending): void {
  clearTimeout(p.timer);
  p.timer = setTimeout(() => {
    worker?.terminate();
    drop(t("The local listener took too long and was stopped."));
  }, p.ms);
  pending.set(n, p);
}

function getWorker(): Worker {
  if (!worker) {
    const w = new Worker(new URL("./speechListen.worker.ts", import.meta.url), { type: "module", name: workerName() });
    worker = w;
    w.onmessage = (e: MessageEvent<FromListener>) => {
      const m = e.data;
      if ("kind" in m) {
        loaded(m);
        return;
      }
      const p = settle(m.seq);
      if (!p) {
        return;
      }
      if ("text" in m) {
        p.ok(m.text);
        return;
      }
      p.fail(new Error(m.error));
    };
    w.onerror = () => drop("the local listener stopped");
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
    // A model still on its way is not a worker that stopped answering.
    for (const [n, p] of pending) {
      if (p.id === m.id) {
        arm(n, p);
      }
    }
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
// lang is the language spoken, or "" to let the model tell. It fails, and the worker is stopped,
// when no answer comes in time (HEAR_BASE_MS, and HEAR_PER_SECOND_MS for each second recorded).
export async function hearLocal(id: string, audio: Blob, lang: string): Promise<string> {
  const samples = await samplesOf(audio);
  warmListener(id);
  const ms = HEAR_BASE_MS + Math.ceil(samples.length / WHISPER_RATE) * HEAR_PER_SECOND_MS;
  return new Promise<string>((ok, fail) => {
    seq++;
    arm(seq, { ok, fail, id, ms });
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
