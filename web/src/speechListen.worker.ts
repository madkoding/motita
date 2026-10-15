/// <reference lib="webworker" />

// The listening worker: Whisper runs here, off the page's thread, on the processor or on the
// graphics card. Its files come from the gateway like the local voices' (see workerOrt.ts); the
// logic is in localListenCore.ts.
import { Listener, type FromListener, type ToListener } from "./localListenCore";
import { bytes, cpuOrt, gpuOrt, messageOf } from "./workerOrt";

declare const self: DedicatedWorkerGlobalScope;

const listener = new Listener({ bytes, ort: cpuOrt, gpu: gpuOrt });

self.onmessage = (e: MessageEvent<ToListener>) => {
  const m = e.data;
  if (m.kind === "forget") {
    listener.forget(m.id);
    return;
  }
  if (m.kind === "warm") {
    // The page shows how far the model is from being ready, and why it could not be.
    listener
      .warm(m.id, (pct) => self.postMessage({ kind: "progress", id: m.id, pct } satisfies FromListener))
      .then(
        (r) =>
          self.postMessage({
            kind: "ready",
            id: m.id,
            gpu: r.gpu,
            gpuError: r.gpuError || undefined,
          } satisfies FromListener),
        (err: unknown) => self.postMessage({ kind: "failed", id: m.id, error: messageOf(err) } satisfies FromListener),
      );
    return;
  }
  listener.hear(m.id, m.samples, m.lang).then(
    (h) => self.postMessage({ seq: m.seq, text: h.text, lang: h.lang } satisfies FromListener),
    (err: unknown) => self.postMessage({ seq: m.seq, error: messageOf(err) } satisfies FromListener),
  );
};
