// LOCAL VOICES on the page's side: the catalog the gateway offers, its downloads, and a FetchClip
// that asks the worker (speechModel.worker.ts) to say a sentence.
import { workerName } from "./gpuPolicy";
import {
  ORT_GPU_WASM,
  MODELS_PATH,
  RUNTIME_ID,
  VOICE_CACHE,
  fileURL,
  type FromWorker,
  type ToWorker,
  type Tune,
} from "./localVoiceCore";
import { choose } from "./choose";
import { speechText } from "./speechText";
import type { Api } from "./prApi";
import type { FetchClip } from "./speech";
import { WHISPER_FILES, isWhisper } from "./localListenCore";
import { forgetListener } from "./localListen";

// LocalModel is one voice of the gateway's catalog.
export interface LocalModel {
  id: string;
  name: string;
  lang: string;
  quality: string;
  // engine runs it: "piper" for a voice (the listening models have their own).
  engine: string;
  // size is what downloading it takes now (with the runtime, while that is missing).
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
  const catalog = (await res.json()) as LocalCatalog;
  if (!pruned) {
    pruned = true;
    // A copy that cannot be dropped is only left behind: it is never read.
    void pruneCache(catalog).catch(() => undefined);
    if (catalog.models.some((m) => m.installed)) {
      void keepOnDisk();
    }
  }
  return catalog;
}

// keepOnDisk asks the browser to keep this page's copies of the models (hundreds of MB) when it
// runs short of room, instead of evicting them as it may evict any site's storage. Browsers grant
// it on their own terms (an installed app, a site used often) or ask; it is asked once a page,
// and answers whether the copies are kept.
let keepAsked: Promise<boolean> | undefined;
export function keepOnDisk(): Promise<boolean> {
  keepAsked ??= askToKeep(globalThis.navigator?.storage);
  return keepAsked;
}

// askToKeep asks storage to keep this page's data, unless it already does; false where it cannot
// be asked or refuses.
export async function askToKeep(
  storage: StorageManager | undefined,
): Promise<boolean> {
  if (!storage?.persist) {
    return false;
  }
  try {
    return (await storage.persisted()) || (await storage.persist());
  } catch {
    return false;
  }
}

// storageKept says whether the browser promised to keep this page's data, without asking it.
export async function storageKept(
  storage: StorageManager | undefined = globalThis.navigator?.storage,
): Promise<boolean> {
  try {
    return (await storage?.persisted?.()) === true;
  } catch {
    return false;
  }
}

// pruned says this page has dropped the copies the catalog no longer has, once.
let pruned = false;

// pruneCache drops this page's copies of files no voice of the catalog uses: those of voices
// taken out of it (Kokoro's, Supertonic's, Chatterbox's), which nothing would read again.
async function pruneCache(catalog: LocalCatalog): Promise<void> {
  if (typeof caches === "undefined") {
    return;
  }
  const keep = new Set([RUNTIME_ID, ...catalog.models.map((m) => m.id)]);
  const prefix = MODELS_PATH + "/";
  const cache = await caches.open(VOICE_CACHE);
  for (const req of await cache.keys()) {
    const at = req.url.indexOf(prefix);
    const id = decodeURIComponent(
      req.url.slice(at + prefix.length).split("/")[0],
    );
    if (at >= 0 && (!keep.has(id) || ownGPURuntime(req.url, id))) {
      await cache.delete(req);
    }
  }
}

// ownGPURuntime says a copy is onnxruntime's WebGPU build as an earlier catalog served it, with
// each model that used it: it is read from the runtime now, once for all of them.
function ownGPURuntime(url: string, id: string): boolean {
  return id !== RUNTIME_ID && url.endsWith("/" + ORT_GPU_WASM);
}

// installVoice asks the gateway to download a voice; the progress is in the catalog.
export async function installVoice(api: Api, id: string): Promise<void> {
  // Asked here, on the click that downloads it, where a browser that asks has a gesture to ask on.
  void keepOnDisk();
  const res = await api(MODELS_PATH + "/" + encodeURIComponent(id), {
    method: "POST",
  });
  if (!res.ok) {
    throw await failure(res);
  }
}

// deleteVoice removes a voice (or a model speech input listens with) from the gateway and from this
// page's copy.
export async function deleteVoice(api: Api, id: string): Promise<void> {
  const res = await api(MODELS_PATH + "/" + encodeURIComponent(id), {
    method: "DELETE",
  });
  if (!res.ok) {
    throw await failure(res);
  }
  post({ kind: "forget", id });
  setLoad(id, undefined);
  if (isWhisper(id)) {
    forgetListener(id);
  }
  if (typeof caches === "undefined") {
    return;
  }
  const cache = await caches.open(VOICE_CACHE);
  // A model to listen with keeps all its files apart; the runtime, WebGPU's too, stays.
  const own = choose(isWhisper(id), Object.values(WHISPER_FILES), [
    "model.onnx",
    "model.json",
  ]);
  const gone = own.map((n) => fileURL(id, n));
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

// THE WORKER says every voice and tells how far one is from loaded.
let worker: Worker | null = null;
let seq = 0;
const pending = new Map<
  number,
  { ok: (b: Blob) => void; fail: (e: Error) => void }
>();

function post(m: ToWorker): void {
  worker?.postMessage(m);
}

function getWorker(): Worker {
  let w = worker;
  if (!w) {
    w = new Worker(new URL("./speechModel.worker.ts", import.meta.url), {
      type: "module",
      name: workerName(),
    });
    worker = w;
    w.onmessage = (e: MessageEvent<FromWorker>) => {
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
      if ("wav" in m) {
        p.ok(new Blob([m.wav], { type: "audio/wav" }));
        return;
      }
      p.fail(new Error(m.error));
    };
    w.onerror = () => {
      for (const p of pending.values()) {
        p.fail(new Error("the local voice stopped"));
      }
      pending.clear();
      worker = null;
      // The voices it had loaded went with it: a new worker loads them again.
      for (const id of loads.keys()) {
        setLoad(id, undefined);
      }
    };
  }
  return w;
}

// voiceLang is the language a Piper voice speaks, from its id (es_MX-claude-high is Spanish): the
// text is prepared for it when the sentence does not say which one it is in.
export function voiceLang(id: string): string {
  return id.slice(0, 2).toLowerCase();
}

// localClip is the FetchClip of a local voice. The text is first prepared for a voice (see
// speechText.ts): numbers and abbreviations written out, pauses marked. A sentence asked for and
// then aborted is still said by the worker (it cannot be cut mid-way), and its audio is dropped.
export function localClip(id: string, tune?: Tune): FetchClip {
  return (text, signal, lang) =>
    new Promise<Blob>((ok, fail) => {
      seq++;
      const s = seq;
      pending.set(s, { ok, fail });
      signal.addEventListener("abort", () => {
        pending.delete(s);
        fail(new DOMException("aborted", "AbortError"));
      });
      getWorker().postMessage({
        kind: "say",
        seq: s,
        id,
        text: speechText(text, lang ?? voiceLang(id)),
        tune,
      } satisfies ToWorker);
    });
}

// LOADING a voice into the worker's memory: how far it is, for the settings to show.
export interface LoadState {
  // pct is 0 to 100; ready says the voice can speak; error says why it could not be loaded.
  pct: number;
  ready: boolean;
  error?: string;
  // gpu says a model that listens runs on WebGPU; gpuError why it could not (localListen.ts).
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
    setLoad(m.id, { pct: 100, ready: true });
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
  getWorker().postMessage({ kind: "warm", id } satisfies ToWorker);
}

// localVoicesSupported says this browser can run a local voice at all.
export function localVoicesSupported(): boolean {
  return (
    typeof Worker !== "undefined" &&
    typeof WebAssembly !== "undefined" &&
    typeof caches !== "undefined"
  );
}
