// The local voices' worker in a browser with WebGPU: the scenarios of cov-voice-worker-cases.mjs.
import test from "node:test";

const { cases } = await import("./cov-voice-worker-cases.mjs");

for (const args of cases) {
  test(...args);
}
