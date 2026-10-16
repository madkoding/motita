/// <reference lib="webworker" />

// The browser model's worker: Qwen2.5 0.5B runs here, off the page's thread, on the graphics card.
// Its files come from the gateway like the local voices' (see workerOrt.ts); the logic is in
// browserLLMCore.ts.
import { Tokenizer } from "@huggingface/tokenizers";
import {
  BrowserModel,
  type FromLLMWorker,
  type LLMOrt,
  type ToLLMWorker,
} from "./browserLLMCore";
import { bytes, gpuOrt, messageOf, webGPU } from "./workerOrt";

declare const self: DedicatedWorkerGlobalScope;

// hasF16 says the card has 16-bit floats in its shaders, which the lighter build needs.
async function hasF16(): Promise<boolean> {
  if (!webGPU()) {
    return false;
  }
  const gpu = (
    self.navigator as Navigator & {
      gpu: { requestAdapter(): Promise<GPUAdapter | null> };
    }
  ).gpu;
  return (await gpu.requestAdapter())?.features.has("shader-f16") === true;
}

const model = new BrowserModel({
  bytes,
  gpu: (wasm) => gpuOrt(wasm) as unknown as Promise<LLMOrt>,
  tokenizer: (json, config) => new Tokenizer(json as object, config as object),
  f16: hasF16,
});

const send = (m: FromLLMWorker) => self.postMessage(m);

self.onmessage = (e: MessageEvent<ToLLMWorker>) => {
  const m = e.data;
  if (m.kind === "load") {
    model
      .load((pct) => send({ kind: "progress", pct }))
      .then(
        (r) => send({ kind: "ready", build: r.build }),
        (err: unknown) => send({ kind: "failed", error: messageOf(err) }),
      );
    return;
  }
  model.generate(m.messages).then(
    (text) => send({ seq: m.seq, text }),
    (err: unknown) => send({ seq: m.seq, error: messageOf(err) }),
  );
};
