// THE BROWSER MODEL on the page's side: turning it on, getting its files from the gateway, loading
// it in a worker (browserLLM.worker.ts) and answering the gateway's requests while this tab is
// open. It is off until the person turns it on, and nothing is downloaded before that.
import { useEffect, useState } from "preact/hooks";
import {
  buildFor,
  type ChatMessage,
  type FromLLMWorker,
  type ToLLMWorker,
} from "./browserLLMCore";
import { t } from "./i18n";
import { gpuAllowed, workerName } from "./gpuPolicy";
import { fetchCatalog, installVoice } from "./localVoice";
import type { Api } from "./prApi";

export const BROWSER_MODEL_KEY = "motita.ui.browserModel";

const HTTP_OK = 200;
const HTTP_NO_CONTENT = 204;
const RETRY_MS = 5000;

function messageOf(e: unknown): string {
  if (e instanceof Error) {
    return e.message;
  }
  return String(e);
}

// percentOf is how much of the download is done, 0 while its size is not known.
function percentOf(
  d: { done: number; total: number } | null | undefined,
): number {
  if (d?.total) {
    return Math.round((100 * d.done) / d.total);
  }
  return 0;
}

// off: not asked for; unsupported: this browser or device cannot run it; downloading: the gateway
// is fetching the files; loading: the worker is starting the model; ready: it answers requests.
export type BrowserPhase =
  | "off"
  | "unsupported"
  | "downloading"
  | "loading"
  | "ready"
  | "failed";
export interface BrowserState {
  phase: BrowserPhase;
  pct: number;
  error: string;
  // build is the catalog id of the build in use, once known.
  build: string;
  // software says the browser's WebGPU is emulated on the processor, which is very slow.
  software: boolean;
}

const IDLE: BrowserState = {
  phase: "off",
  pct: 0,
  error: "",
  build: "",
  software: false,
};
let state: BrowserState = IDLE;
const listeners = new Set<(s: BrowserState) => void>();

function set(next: Partial<BrowserState>): void {
  state = { ...state, ...next };
  for (const l of listeners) {
    l(state);
  }
}

// show is set for a run that may have been stopped meanwhile: a stopped run (the person turned the
// model off while it was downloading or loading) must not paint over the state of the next one.
function show(signal: AbortSignal, next: Partial<BrowserState>): void {
  if (!signal.aborted) {
    set(next);
  }
}

export function browserState(): BrowserState {
  return state;
}

// browserModelOn says the person turned the model on in this browser.
export function browserModelOn(): boolean {
  try {
    return localStorage.getItem(BROWSER_MODEL_KEY) === "1";
  } catch {
    return false;
  }
}

export function setBrowserModelOn(on: boolean): void {
  try {
    let value = "0";
    if (on) {
      value = "1";
    }
    localStorage.setItem(BROWSER_MODEL_KEY, value);
  } catch {
    // Nothing to do: the choice is only a convenience.
  }
}

// browserGPU says this browser can run it at all: WebGPU in a window and in a worker, on a device
// the local models may use it on (not a phone, unless forced).
export function browserGPU(): boolean {
  return gpuAllowed() && "gpu" in navigator;
}

// The adapters that emulate WebGPU on the processor (SwiftShader, llvmpipe, Microsoft's WARP).
const SOFTWARE_ADAPTER = /swiftshader|llvmpipe|lavapipe|software|\bwarp\b/i;

// inspectCard says what the graphics card has: 16-bit floats in its shaders (so the lighter build
// runs) and whether it is only WebGPU emulated on the processor.
async function inspectCard(): Promise<{ f16: boolean; software: boolean }> {
  const gpu = (
    navigator as Navigator & {
      gpu: { requestAdapter(): Promise<GPUAdapter | null> };
    }
  ).gpu;
  const card = await gpu.requestAdapter();
  const info = card?.info;
  const named = [info?.vendor, info?.architecture, info?.description].join(" ");
  return {
    f16: card?.features.has("shader-f16") === true,
    software:
      (card as { isFallbackAdapter?: boolean } | null)?.isFallbackAdapter ===
        true || SOFTWARE_ADAPTER.test(named),
  };
}

// A running model: its worker, and the loop that serves the gateway's requests.
let run: { stop: () => void } | undefined;

// startBrowserModel downloads (once), loads and serves the model; it does nothing when it runs.
export function startBrowserModel(api: Api): void {
  if (run) {
    return;
  }
  if (!browserGPU()) {
    set({ phase: "unsupported", error: "" });
    return;
  }
  const ctl = new AbortController();
  run = {
    stop: () => {
      ctl.abort();
      run = undefined;
      set(IDLE);
    },
  };
  void serve(api, ctl.signal).catch((e: unknown) => {
    if (!ctl.signal.aborted) {
      // Aborting ends the worker a failed load left behind, so a retry does not stack another.
      ctl.abort();
      set({
        phase: "failed",
        error: messageOf(e),
      });
      run = undefined;
    }
  });
}

export function stopBrowserModel(): void {
  run?.stop();
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

// ensureFiles has the gateway download the build, following the progress in its catalog.
async function ensureFiles(
  api: Api,
  id: string,
  signal: AbortSignal,
): Promise<void> {
  for (let started = false; !signal.aborted; ) {
    const catalog = await fetchCatalog(api);
    const mine = catalog.models.find((m) => m.id === id);
    if (!mine) {
      throw new Error(t("this gateway does not offer the browser model"));
    }
    if (mine.installed) {
      return;
    }
    if (catalog.download?.id === id && catalog.download.error) {
      throw new Error(catalog.download.error);
    }
    if (!started) {
      await installVoice(api, id);
      started = true;
    }
    show(signal, { phase: "downloading", pct: percentOf(catalog.download) });
    await sleep(1000);
  }
}

async function serve(api: Api, signal: AbortSignal): Promise<void> {
  const { f16, software } = await inspectCard();
  const build = buildFor(f16);
  show(signal, { phase: "downloading", pct: 0, error: "", build, software });
  await ensureFiles(api, build, signal);
  if (signal.aborted) {
    return;
  }
  show(signal, { phase: "loading", pct: 0 });
  const worker = new Worker(
    new URL("./browserLLM.worker.ts", import.meta.url),
    { type: "module", name: workerName() },
  );
  const waiting = new Map<
    number,
    { ok: (t: string) => void; fail: (e: Error) => void }
  >();
  // A request the worker will never answer (it was stopped, or it died) fails at once, so the job
  // is told to the gateway as failed instead of left to its own timeout.
  const failAll = (err: Error) => {
    for (const w of waiting.values()) {
      w.fail(err);
    }
    waiting.clear();
  };
  signal.addEventListener("abort", () => {
    worker.terminate();
    failAll(new Error("the browser model was turned off"));
  });
  // dead settles when the worker script cannot load or throws: nothing else would end the wait.
  const dead = new Promise<never>((_, died) => {
    worker.onerror = (e: ErrorEvent) => {
      const err = new Error(e.message || "the browser model's worker stopped");
      failAll(err);
      died(err);
    };
  });
  dead.catch(() => undefined);
  const loaded = new Promise<void>((ok, fail) => {
    worker.onmessage = (e: MessageEvent<FromLLMWorker>) => {
      const m = e.data;
      if ("kind" in m) {
        if (m.kind === "progress") {
          show(signal, { pct: m.pct });
        } else if (m.kind === "ready") {
          ok();
        } else {
          fail(new Error(m.error));
        }
        return;
      }
      const w = waiting.get(m.seq);
      waiting.delete(m.seq);
      if ("text" in m) {
        w?.ok(m.text);
      } else {
        w?.fail(new Error(m.error));
      }
    };
  });
  const post = (m: ToLLMWorker) => worker.postMessage(m);
  post({ kind: "load" });
  await Promise.race([loaded, dead]);
  show(signal, { phase: "ready", pct: 100 });
  let seq = 0;
  const generate = (messages: ChatMessage[]) =>
    new Promise<string>((ok, fail) => {
      seq += 1;
      waiting.set(seq, { ok, fail });
      post({ kind: "generate", seq, messages });
    });
  await Promise.race([answerLoop(api, signal, generate), dead]);
}

// answerLoop asks the gateway for work and answers it, until stopped. A request the gateway
// cannot be reached for is asked again a few seconds later.
async function answerLoop(
  api: Api,
  signal: AbortSignal,
  generate: (m: ChatMessage[]) => Promise<string>,
): Promise<void> {
  while (!signal.aborted) {
    let job: { id: string; messages: ChatMessage[] } | undefined;
    try {
      const res = await api("/v1/browser-llm/next", { signal });
      if (res.status === HTTP_OK) {
        job = (await res.json()) as { id: string; messages: ChatMessage[] };
      } else if (res.status === HTTP_NO_CONTENT) {
        // No work yet: ask again.
      } else {
        await sleep(RETRY_MS);
      }
    } catch {
      await sleep(RETRY_MS);
    }
    if (!job) {
      continue;
    }
    const answer = await generate(job.messages).then(
      (text) => ({ text }),
      (e: Error) => ({ error: e.message }),
    );
    await api(`/v1/browser-llm/jobs/${job.id}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(answer),
    }).catch(() => undefined);
  }
}

// useBrowserModelState follows the model's state, for the settings.
export function useBrowserModelState(): BrowserState {
  const [s, setS] = useState(state);
  useEffect(() => {
    listeners.add(setS);
    setS(state);
    return () => {
      listeners.delete(setS);
    };
  }, []);
  return s;
}

// useBrowserModel starts the model once this tab is signed in, when the person turned it on.
export function useBrowserModel(api: Api, authed: boolean): void {
  useEffect(() => {
    if (!authed) {
      // Signed out: every request would be a 401, so the loop and the worker stop.
      stopBrowserModel();
      return;
    }
    if (browserModelOn()) {
      startBrowserModel(api);
    }
  }, [api, authed]);
}
