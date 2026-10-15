/// <reference lib="webworker" />

// The local voices' worker: onnxruntime and the phonemizer run here, off the page's thread.
//
// Every file comes from the gateway (GET /v1/voice/models/...), which downloaded it from a fixed
// catalog and checked its hash; the page keeps its own copy in Cache Storage so a voice loads
// without the network after the first time. The logic is in localVoiceCore.ts.
import createPiperPhonemize from "@diffusionstudio/piper-wasm/build/piper_phonemize.js";
import { Synth, isSupertonic, type FromWorker, type ToWorker } from "./localVoiceCore";
import { bytes, cpuOrt, gpuOrt, messageOf } from "./workerOrt";

declare const self: DedicatedWorkerGlobalScope;

const synth = new Synth({
  bytes,
  ort: cpuOrt,
  // WebGPU's build runs Supertonic and Chatterbox on a browser that has WebGPU. Each way it can
  // fail says so, and the page shows it.
  gpu: gpuOrt,
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
    // The page shows how far the voice is from being ready, and why it could not be.
    synth
      .warm(m.id, (pct) => self.postMessage({ kind: "progress", id: m.id, pct } satisfies FromWorker))
      .then(
        async () =>
          self.postMessage({
            kind: "ready",
            id: m.id,
            gpu: await synth.onGPU(m.id),
            gpuError: (isSupertonic(m.id) && synth.gpuError()) || undefined,
          } satisfies FromWorker),
        (err: unknown) => self.postMessage({ kind: "failed", id: m.id, error: messageOf(err) } satisfies FromWorker),
      );
    return;
  }
  synth.say(m.id, m.text, m.tune).then(
    (wav) => self.postMessage({ seq: m.seq, wav } satisfies FromWorker, [wav]),
    (err: unknown) => self.postMessage({ seq: m.seq, error: messageOf(err) } satisfies FromWorker),
  );
};
