// LOCAL VOICES on the page's side: the catalog the gateway offers, its downloads, and a FetchClip
// that asks the worker (speechModel.worker.ts) to say a sentence.
import { MODELS_PATH, VOICE_CACHE, fileURL, type FromWorker, type ToWorker } from "./localVoiceCore";
import type { Api } from "./prApi";
import type { FetchClip } from "./speech";

// LocalModel is one voice of the gateway's catalog.
export interface LocalModel {
  id: string;
  name: string;
  lang: string;
  quality: string;
  size: number;
  installed: boolean;
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
  if (typeof caches !== "undefined") {
    const cache = await caches.open(VOICE_CACHE);
    await Promise.all(["model.onnx", "model.json"].map((n) => cache.delete(fileURL(id, n))));
  }
}

// The worker, made the first time a sentence is said.
let worker: Worker | null = null;
let seq = 0;
const pending = new Map<number, { ok: (b: Blob) => void; fail: (e: Error) => void }>();

function post(m: ToWorker): void {
  worker?.postMessage(m);
}

function getWorker(): Worker {
  if (!worker) {
    worker = new Worker(new URL("./speechModel.worker.ts", import.meta.url), { type: "module" });
    worker.onmessage = (e: MessageEvent<FromWorker>) => {
      const m = e.data;
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
    worker.onerror = () => {
      for (const p of pending.values()) {
        p.fail(new Error("the local voice stopped"));
      }
      pending.clear();
      worker = null;
    };
  }
  return worker;
}

// localClip is the FetchClip of a local voice. A sentence asked for and then aborted is still
// said by the worker (it cannot be cut mid-way), and its audio is dropped.
export function localClip(id: string): FetchClip {
  return (text, signal) =>
    new Promise<Blob>((ok, fail) => {
      seq++;
      const n = seq;
      pending.set(n, { ok, fail });
      signal.addEventListener("abort", () => {
        pending.delete(n);
        fail(new DOMException("aborted", "AbortError"));
      });
      getWorker().postMessage({ kind: "say", seq: n, id, text } satisfies ToWorker);
    });
}

// warmLocal loads a voice in the worker ahead of the first sentence.
export function warmLocal(id: string): void {
  getWorker().postMessage({ kind: "warm", id } satisfies ToWorker);
}

// localVoicesSupported says this browser can run a local voice at all.
export function localVoicesSupported(): boolean {
  return typeof Worker !== "undefined" && typeof WebAssembly !== "undefined" && typeof caches !== "undefined";
}
