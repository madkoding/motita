// Where the local voices may use WebGPU (gpuPolicy.ts): never on a mobile device unless forced.
import test from "node:test";
import assert from "node:assert/strict";
import { setupDom, source } from "./cov-voice-helpers.mjs";

setupDom("https://localhost/");
const P = await source("gpuPolicy.ts");
const ua = (v) =>
  Object.defineProperty(navigator, "userAgent", {
    value: v,
    configurable: true,
  });
const media = (fn) => (window.matchMedia = fn);

test("a desktop may use WebGPU; a phone, by viewport, touch or user agent, may not", () => {
  delete window.matchMedia;
  ua("Mozilla/5.0 (X11; Linux x86_64) Firefox");
  assert.equal(P.isMobileDevice(), false, "no matchMedia, desktop agent");
  assert.equal(P.gpuAllowed(), true);
  assert.equal(P.workerName(), "");

  media((q) => ({ matches: q.startsWith("(max-width") }));
  assert.equal(P.isMobileDevice(), true, "narrow viewport");
  media((q) => ({ matches: q.startsWith("(pointer") }));
  assert.equal(P.isMobileDevice(), true, "touch only");
  media(() => ({ matches: false }));
  assert.equal(P.isMobileDevice(), false);
  ua("Mozilla/5.0 (iPhone; CPU iPhone OS 17) Mobile");
  assert.equal(P.isMobileDevice(), true, "user agent");
  assert.equal(P.gpuAllowed(), false);
  assert.equal(P.workerName(), P.CPU_ONLY);

  P.setGpuForced(true);
  assert.equal(P.gpuForced(), true);
  assert.equal(P.gpuAllowed(), true, "forced on");
  assert.equal(P.workerName(), "");
  P.setGpuForced(false);
  assert.equal(P.gpuForced(), false);
  localStorage.clear();
});

test("a browser that cannot store the choice forgets it", () => {
  const store = globalThis.localStorage;
  globalThis.localStorage = {
    getItem() {
      throw new Error("blocked");
    },
    setItem() {
      throw new Error("blocked");
    },
  };
  try {
    P.setGpuForced(true);
    assert.equal(P.gpuForced(), false);
  } finally {
    globalThis.localStorage = store;
  }
});

test("a worker named for the processor has no WebGPU", async () => {
  globalThis.self = globalThis;
  self.navigator = { gpu: {} };
  const O = await source("workerOrt.ts");
  self.name = "";
  assert.equal(O.webGPU(), true);
  self.name = P.CPU_ONLY;
  assert.equal(O.webGPU(), false);
  await assert.rejects(
    O.gpuOrt(new ArrayBuffer(0)),
    /no WebGPU in its workers/,
  );
});
