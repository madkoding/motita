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
import { cardOf } from "./gpuPolicy";
import { bytes, gpuOrt, messageOf, workerCard } from "./workerOrt";

declare const self: DedicatedWorkerGlobalScope;

// The card the model runs on, found once: the build is chosen on it and the page is told its name.
let found: Promise<GPUAdapter | null> | undefined;
function card(): Promise<GPUAdapter | null> {
  found ??= workerCard();
  return found;
}

// hasF16 says the card has 16-bit floats in its shaders, which the lighter build needs.
async function hasF16(): Promise<boolean> {
  return (await card())?.features.has("shader-f16") === true;
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
    found = undefined;
    model
      .load((pct) => send({ kind: "progress", pct }))
      .then(
        async (r) =>
          send({ kind: "ready", build: r.build, ...cardOf(await card()) }),
        (err: unknown) => send({ kind: "failed", error: messageOf(err) }),
      );
    return;
  }
  model.generate(m.messages).then(
    (text) => send({ seq: m.seq, text }),
    (err: unknown) => send({ seq: m.seq, error: messageOf(err) }),
  );
};
