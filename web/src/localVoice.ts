// LOCAL VOICES on the page's side: the catalog the gateway offers, its downloads, and a FetchClip
// that asks the worker (speechModel.worker.ts) to say a sentence.
import { workerName } from "./gpuPolicy";
import {
  ORT_GPU_WASM,
  MODELS_PATH,
  RUNTIME_ID,
  VOICE_CACHE,
  fileURL,
  isSupertonic,
  isChatterbox,
  CHATTERBOX_ID,
  CHATTERBOX_PARTS,
  SUPERTONIC_ID,
  SUPERTONIC_PARTS,
  type FromWorker,
  type ToWorker,
  type Tune,
} from "./localVoiceCore";
import { choose } from "./choose";
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
  // engine runs it: "piper", "supertonic" or "chatterbox".
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

// SHARED_SETS are the ids of the files voices share, which the catalog does not list as voices.
const SHARED_SETS = [RUNTIME_ID, SUPERTONIC_ID, CHATTERBOX_ID];
// pruned says this page has dropped the copies the catalog no longer has, once.
let pruned = false;

// pruneCache drops this page's copies of files no voice of the catalog uses: those of voices
// taken out of it (Kokoro's), which nothing would read again.
async function pruneCache(catalog: LocalCatalog): Promise<void> {
  if (typeof caches === "undefined") {
    return;
  }
  const keep = new Set([...SHARED_SETS, ...catalog.models.map((m) => m.id)]);
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
    "voice.bin",
    "voice.json",
  ]);
  const gone = own.map((n) => fileURL(id, n));
  // An engine's model goes with its last voice, on the gateway (see voice.Library.Remove) and here.
  const engines = [
    {
      of: isSupertonic,
      files: [...SUPERTONIC_PARTS, "unicode_indexer.json"].map((n) =>
        fileURL(SUPERTONIC_ID, n),
      ),
    },
    {
      of: isChatterbox,
      files: [
        "tokenizer.json",
        ...CHATTERBOX_PARTS.flatMap((p) => [p, p + "_data"]),
      ].map((n) => fileURL(CHATTERBOX_ID, n)),
    },
  ];
  const engine = engines.find((e) => e.of(id));
  if (
    engine &&
    !(await fetchCatalog(api)).models.some(
      (m) => engine.of(m.id) && m.installed,
    )
  ) {
    gone.push(...engine.files);
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

// THE WORKERS. The first says every voice and tells how far one is from loaded. A Supertonic
// voice on the CPU also has helpers, each with its own copy of the model, one per spare core up
// to SUPERTONIC_LANES: it can take longer to say a sentence than to play it, so the next
// sentences are said at once, each by a worker of its own.
const workers: (Worker | null)[] = [];
// busy counts the sentences each worker has been asked for and not answered yet.
const busy: number[] = [];
let seq = 0;
const pending = new Map<
  number,
  { ok: (b: Blob) => void; fail: (e: Error) => void; w: number }
>();

function post(m: ToWorker): void {
  for (const w of workers) {
    w?.postMessage(m);
  }
}

function getWorker(n = 0): Worker {
  let w = workers[n];
  if (!w) {
    w = new Worker(new URL("./speechModel.worker.ts", import.meta.url), {
      type: "module",
      name: workerName(),
    });
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
        for (const id of loads.keys()) {
          setLoad(id, undefined);
        }
      }
    };
  }
  return w;
}

// SUPERTONIC_LANES is Supertonic's most workers: each holds its 400 MB model in memory.
export const SUPERTONIC_LANES = 2;

// lanes is how many workers say a voice's sentences at once: one, but for Supertonic on the CPU
// (it runs on the graphics card wherever the browser lets it).
export function lanes(id: string): number {
  const most = choose(
    isSupertonic(id) && !loads.get(id)?.gpu,
    SUPERTONIC_LANES,
    1,
  );
  const cores =
    (typeof navigator !== "undefined" && navigator.hardwareConcurrency) || 1;
  return Math.max(1, Math.min(most, cores - 1));
}

// localClip is the FetchClip of a local voice. A sentence asked for and then aborted is still
// said by the worker (it cannot be cut mid-way), and its audio is dropped. Each sentence goes to
// the worker with the fewest waiting, and the reader asks for as many ahead as there are.
export function localClip(id: string, tune?: Tune): FetchClip {
  const n = lanes(id);
  const say: FetchClip = (text, signal, lang) =>
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
      // The sentence's language goes with it, for a voice that speaks several.
      const told = choose(lang === undefined, tune, {
        noise: 1,
        noiseW: 1,
        ...tune,
        lang,
      });
      getWorker(w).postMessage({
        kind: "say",
        seq: s,
        id,
        text,
        tune: told,
      } satisfies ToWorker);
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
  // gpu says the voice runs on WebGPU; gpuError why a Supertonic voice could not.
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
      ...choose(m.gpu, { gpu: true }, {}),
      ...choose(m.gpuError, { gpuError: m.gpuError }, {}),
    });
    // The helpers load the voice too, once the first worker has it in its copy of the files.
    for (let k = 1; k < lanes(m.id); k++) {
      getWorker(k).postMessage({ kind: "warm", id: m.id } satisfies ToWorker);
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
