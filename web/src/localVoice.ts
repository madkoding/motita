// LOCAL VOICES on the page's side: the catalog the gateway offers, its downloads, and a FetchClip
// that asks the worker (speechModel.worker.ts) to say a sentence.
import {
  KOKORO_GPU_ID,
  KOKORO_ID,
  ORT_GPU_WASM,
  MODELS_PATH,
  VOICE_CACHE,
  fileURL,
  isKokoro,
  type FromWorker,
  type ToWorker,
  type Tune,
} from "./localVoiceCore";
import { choose } from "./choose";
import type { Api } from "./prApi";
import type { FetchClip } from "./speech";

// LocalModel is one voice of the gateway's catalog.
export interface LocalModel {
  id: string;
  name: string;
  lang: string;
  quality: string;
  // engine runs it: "piper" or "kokoro".
  engine: string;
  // size is what downloading it takes now (with its engine's model, while that is missing).
  size: number;
  installed: boolean;
  // sample says it can be heard before it is downloaded (sampleURL).
  sample: boolean;
}

// LocalCatalog is GET /v1/voice/models.
export interface LocalCatalog {
  runtime: { size: number; installed: boolean };
  models: LocalModel[];
  download: { id: string; done: number; total: number; error?: string } | null;
}

// failure is the gateway's message for a refused request.
async function failure(res: Response): Promise<Error> {
  let msg = "HTTP " + res.status;
  try {
    const body = (await res.json()) as { error?: string };
    msg = body.error || msg;
  } catch {
    /* not JSON */
  }
  return new Error(msg);
}

export async function fetchCatalog(api: Api): Promise<LocalCatalog> {
  const res = await api(MODELS_PATH);
  if (!res.ok) {
    throw await failure(res);
  }
  return (await res.json()) as LocalCatalog;
}

// installVoice asks the gateway to download a voice; the progress is in the catalog.
export async function installVoice(api: Api, id: string): Promise<void> {
  const res = await api(MODELS_PATH + "/" + encodeURIComponent(id), { method: "POST" });
  if (!res.ok) {
    throw await failure(res);
  }
}

// deleteVoice removes a voice from the gateway and from this page's copy.
export async function deleteVoice(api: Api, id: string): Promise<void> {
  const res = await api(MODELS_PATH + "/" + encodeURIComponent(id), { method: "DELETE" });
  if (!res.ok) {
    throw await failure(res);
  }
  post({ kind: "forget", id });
  setLoad(id, undefined);
  if (typeof caches === "undefined") {
    return;
  }
  const cache = await caches.open(VOICE_CACHE);
  // The accelerator's copy goes too: its model, and the build of the runtime that drives WebGPU.
  const names = ["model.onnx", "model.json", "voice.bin", ...choose(id === KOKORO_GPU_ID, [ORT_GPU_WASM], [])];
  const gone = names.map((n) => fileURL(id, n));
  // Kokoro's model goes with its last voice, on the gateway (see voice.Library.Remove) and here.
  if (isKokoro(id) && !(await fetchCatalog(api)).models.some((m) => isKokoro(m.id) && m.installed)) {
    gone.push(fileURL(KOKORO_ID, "model.onnx"), fileURL(KOKORO_ID, "tokenizer.json"));
  }
  await Promise.all(gone.map((u) => cache.delete(u)));
}

// sampleOf is a voice's sample, to hear it before downloading it: the gateway fetches it from the
// catalog's address the first time.
export async function sampleOf(api: Api, id: string): Promise<Blob> {
  const res = await api(fileURL(id, "sample"));
  if (!res.ok) {
    throw await failure(res);
  }
  return res.blob();
}

// THE WORKERS. The first says every voice and tells how far one is from loaded. A Kokoro voice
// on the CPU also has helpers, each with its own copy of the model, one per spare core up to
// MAX_LANES: Kokoro takes longer to say a sentence than to play it, so the next sentences are
// said at once, each by a worker of its own.
export const MAX_LANES = 3;
const workers: (Worker | null)[] = [];
// busy counts the sentences each worker has been asked for and not answered yet.
const busy: number[] = [];
let seq = 0;
const pending = new Map<number, { ok: (b: Blob) => void; fail: (e: Error) => void; w: number }>();
// kokoroGPU says Kokoro runs on WebGPU: its files are downloaded and the browser has it.
let kokoroGPU = false;

function post(m: ToWorker): void {
  for (const w of workers) {
    w?.postMessage(m);
  }
}

function getWorker(n = 0): Worker {
  let w = workers[n];
  if (!w) {
    w = new Worker(new URL("./speechModel.worker.ts", import.meta.url), { type: "module" });
    workers[n] = w;
    busy[n] = 0;
    w.onmessage = (e: MessageEvent<FromWorker>) => {
      const m = e.data;
      if ("kind" in m) {
        // Only the first worker's loading is shown; the helpers load quietly.
        if (n === 0) {
          loaded(m);
        }
        return;
      }
      busy[n] = Math.max(0, busy[n] - 1);
      const p = pending.get(m.seq);
      pending.delete(m.seq);
      if (!p) {
        return;
      }
      if ("wav" in m) {
        p.ok(new Blob([m.wav], { type: "audio/wav" }));
        return;
      }
      p.fail(new Error(m.error));
    };
    w.onerror = () => {
      for (const [k, p] of pending) {
        if (p.w === n) {
          p.fail(new Error("the local voice stopped"));
          pending.delete(k);
        }
      }
      workers[n] = null;
      // The voices it had loaded went with it: a new worker loads them again.
      if (n === 0) {
        for (const id of [...loads.keys()]) {
          setLoad(id, undefined);
        }
      }
    };
  }
  return w;
}

// lanes is how many workers say a voice's sentences at once: one, but for Kokoro on the CPU.
export function lanes(id: string): number {
  if (!isKokoro(id) || kokoroGPU) {
    return 1;
  }
  const cores = (typeof navigator !== "undefined" && navigator.hardwareConcurrency) || 1;
  return Math.max(1, Math.min(MAX_LANES, cores - 1));
}

// setKokoroGPU says whether Kokoro runs on WebGPU. A change loads the voices again.
export function setKokoroGPU(on: boolean): void {
  if (on === kokoroGPU) {
    return;
  }
  kokoroGPU = on;
  for (const id of [...loads.keys()]) {
    if (isKokoro(id)) {
      setLoad(id, undefined);
    }
  }
}

// kokoroOnGPU says whether Kokoro is meant to run on WebGPU.
export function kokoroOnGPU(): boolean {
  return kokoroGPU;
}

// localClip is the FetchClip of a local voice. A sentence asked for and then aborted is still
// said by the worker (it cannot be cut mid-way), and its audio is dropped. Each sentence goes to
// the worker with the fewest waiting, and the reader asks for as many ahead as there are.
export function localClip(id: string, tune?: Tune): FetchClip {
  const n = lanes(id);
  const say: FetchClip = (text, signal) =>
    new Promise<Blob>((ok, fail) => {
      let w = 0;
      for (let k = 0; k < n; k++) {
        getWorker(k);
        if (busy[k] < busy[w]) {
          w = k;
        }
      }
      seq++;
      const s = seq;
      pending.set(s, { ok, fail, w });
      signal.addEventListener("abort", () => {
        pending.delete(s);
        fail(new DOMException("aborted", "AbortError"));
      });
      busy[w]++;
      getWorker(w).postMessage({ kind: "say", seq: s, id, text, tune, gpu: kokoroGPU } satisfies ToWorker);
    });
  say.ahead = n;
  return say;
}

// LOADING a voice into the worker's memory: how far it is, for the settings to show.
export interface LoadState {
  // pct is 0 to 100; ready says the voice can speak; error says why it could not be loaded.
  pct: number;
  ready: boolean;
  error?: string;
  // gpu says a Kokoro voice runs on WebGPU; gpuError why it could not, when it was asked to.
  gpu?: boolean;
  gpuError?: string;
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

// loaded records what the worker said about a voice it is loading.
function loaded(m: Extract<FromWorker, { kind: string }>): void {
  if (m.kind === "progress") {
    setLoad(m.id, { pct: m.pct, ready: false });
    return;
  }
  if (m.kind === "ready") {
    setLoad(m.id, {
      pct: 100,
      ready: true,
      ...(m.gpu ? { gpu: true } : {}),
      ...(m.gpuError ? { gpuError: m.gpuError } : {}),
    });
    // The helpers load the voice too, once the first worker has it in its copy of the files.
    for (let k = 1; k < lanes(m.id); k++) {
      getWorker(k).postMessage({ kind: "warm", id: m.id, gpu: kokoroGPU } satisfies ToWorker);
    }
    return;
  }
  setLoad(m.id, { pct: 0, ready: false, error: m.error });
}

// loadStateOf is how far a voice is from being ready here; undefined before it was asked for.
export function loadStateOf(id: string): LoadState | undefined {
  return loads.get(id);
}

// onLoadState calls f whenever a voice's loading changes, until the returned function is called.
export function onLoadState(f: () => void): () => void {
  loadListeners.add(f);
  return () => loadListeners.delete(f);
}

// warmLocal loads a voice in the worker ahead of the first sentence. A voice loading or loaded
// is not asked for again; one that failed is.
export function warmLocal(id: string): void {
  const now = loads.get(id);
  if (now && !now.error) {
    return;
  }
  setLoad(id, { pct: 0, ready: false });
  getWorker().postMessage({ kind: "warm", id, gpu: kokoroGPU } satisfies ToWorker);
}

// localVoicesSupported says this browser can run a local voice at all.
export function localVoicesSupported(): boolean {
  return typeof Worker !== "undefined" && typeof WebAssembly !== "undefined" && typeof caches !== "undefined";
}
