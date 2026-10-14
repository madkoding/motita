/// <reference lib="webworker" />

// The local voices' worker: onnxruntime and the phonemizer run here, off the page's thread.
//
// Every file comes from the gateway (GET /v1/voice/models/...), which downloaded it from a fixed
// catalog and checked its hash; the page keeps its own copy in Cache Storage so a voice loads
// without the network after the first time. The logic is in localVoiceCore.ts.
import * as ort from "onnxruntime-web/wasm";
import createPiperPhonemize from "@diffusionstudio/piper-wasm/build/piper_phonemize.js";
import { Synth, VOICE_CACHE, type FromWorker, type OrtLike, type ToWorker } from "./localVoiceCore";

declare const self: DedicatedWorkerGlobalScope;

// bytes is a downloaded file, from the page's copy when there is one.
async function bytes(url: string): Promise<ArrayBuffer> {
  const cache = await caches.open(VOICE_CACHE);
  const hit = await cache.match(url);
  if (hit) {
    return hit.arrayBuffer();
  }
  const res = await fetch(url, { credentials: "same-origin" });
  if (!res.ok) {
    throw new Error("HTTP " + res.status + " " + url);
  }
  await cache.put(url, res.clone());
  return res.arrayBuffer();
}

const synth = new Synth({
  bytes,
  ort: async (wasm) => {
    // One thread: several need a cross-origin isolated page, which this one is not.
    ort.env.wasm.numThreads = 1;
    ort.env.wasm.proxy = false;
    ort.env.wasm.wasmBinary = wasm;
    return {
      Tensor: ort.Tensor as unknown as OrtLike["Tensor"],
      createSession: (model) => ort.InferenceSession.create(model, { executionProviders: ["wasm"] }),
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

self.onmessage = (e: MessageEvent<ToWorker>) => {
  const m = e.data;
  if (m.kind === "forget") {
    synth.forget(m.id);
    return;
  }
  if (m.kind === "warm") {
    // A failure here is reported by the first sentence, which tries again.
    synth.warm(m.id).catch(() => undefined);
    return;
  }
  synth.say(m.id, m.text).then(
    (wav) => self.postMessage({ seq: m.seq, wav } satisfies FromWorker, [wav]),
    (err: unknown) => {
      let msg = String(err);
      if (err instanceof Error) {
        msg = err.message;
      }
      self.postMessage({ seq: m.seq, error: msg } satisfies FromWorker);
    },
  );
};
