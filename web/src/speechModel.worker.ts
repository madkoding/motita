/// <reference lib="webworker" />

// The local voices' worker: onnxruntime and the phonemizer run here, off the page's thread.
//
// Every file comes from the gateway (GET /v1/voice/models/...), which downloaded it from a fixed
// catalog and checked its hash; the page keeps its own copy in Cache Storage so a voice loads
// without the network after the first time. The logic is in localVoiceCore.ts.
import { env, InferenceSession, Tensor } from "onnxruntime-web/wasm";
import createPiperPhonemize from "@diffusionstudio/piper-wasm/build/piper_phonemize.js";
import { Synth, VOICE_CACHE, readAll, type FromWorker, type OrtLike, type ToWorker } from "./localVoiceCore";

declare const self: DedicatedWorkerGlobalScope;

// bytes is a downloaded file, from the page's copy when there is one.
async function bytes(url: string, progress?: (loaded: number, total: number) => void): Promise<ArrayBuffer> {
  const cache = await caches.open(VOICE_CACHE);
  const hit = await cache.match(url);
  if (hit) {
    return readAll(hit, progress);
  }
  const res = await fetch(url, { credentials: "same-origin" });
  if (!res.ok) {
    throw new Error("HTTP " + res.status + " " + url);
  }
  // A copy the browser cannot keep (out of room for a large model) is read from the gateway
  // again next time; the voice still loads.
  const [, body] = await Promise.all([cache.put(url, res.clone()).catch(() => undefined), readAll(res, progress)]);
  return body;
}

const synth = new Synth({
  bytes,
  ort: async (wasm) => {
    // One thread: several need a cross-origin isolated page, which this one is not.
    env.wasm.numThreads = 1;
    env.wasm.proxy = false;
    env.wasm.wasmBinary = wasm;
    return {
      Tensor: Tensor as unknown as OrtLike["Tensor"],
      createSession: (model) => InferenceSession.create(model, { executionProviders: ["wasm"] }),
    } as OrtLike;
  },
  // WebGPU's build is loaded only for Kokoro, on a browser that has WebGPU.
  gpu: async (wasm) => {
    const gpu = (self.navigator as Navigator & { gpu?: { requestAdapter(): Promise<unknown> } }).gpu;
    if (!gpu || !(await gpu.requestAdapter())) {
      throw new Error("no WebGPU here");
    }
    const ortGPU = await import("onnxruntime-web/webgpu");
    ortGPU.env.wasm.numThreads = 1;
    ortGPU.env.wasm.proxy = false;
    ortGPU.env.wasm.wasmBinary = wasm;
    // Its notes on which nodes stay on the CPU are expected, not errors.
    ortGPU.env.logLevel = "error";
    return {
      Tensor: ortGPU.Tensor as unknown as OrtLike["Tensor"],
      createSession: (model) => ortGPU.InferenceSession.create(model, { executionProviders: ["webgpu"] }),
    } as OrtLike;
  },
  phonemizer: (wasm, data, print) =>
    createPiperPhonemize({
      print,
      printErr: () => undefined,
      wasmBinary: wasm,
      // The espeak-ng data comes from the same copy, never from an address the module builds.
      getPreloadedPackage: () => data,
      locateFile: (name) => name,
    }),
});

// messageOf is what failed, as the page shows it.
function messageOf(err: unknown): string {
  if (err instanceof Error) {
    return err.message;
  }
  return String(err);
}

self.onmessage = (e: MessageEvent<ToWorker>) => {
  const m = e.data;
  if (m.kind === "forget") {
    synth.forget(m.id);
    return;
  }
  if (m.gpu !== undefined) {
    synth.setGPU(m.gpu);
  }
  if (m.kind === "warm") {
    // The page shows how far the voice is from being ready, and why it could not be.
    synth
      .warm(m.id, (pct) => self.postMessage({ kind: "progress", id: m.id, pct } satisfies FromWorker))
      .then(
        async () => self.postMessage({ kind: "ready", id: m.id, gpu: await synth.onGPU() } satisfies FromWorker),
        (err: unknown) => self.postMessage({ kind: "failed", id: m.id, error: messageOf(err) } satisfies FromWorker),
      );
    return;
  }
  synth.say(m.id, m.text, m.tune).then(
    (wav) => self.postMessage({ seq: m.seq, wav } satisfies FromWorker, [wav]),
    (err: unknown) => self.postMessage({ seq: m.seq, error: messageOf(err) } satisfies FromWorker),
  );
};
