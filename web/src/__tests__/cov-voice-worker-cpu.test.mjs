// The local voices' worker once more, in a browser without WebGPU where Supertonic's runtime
// cannot be had at first (see cov-voice-worker-cases.mjs).
import test from "node:test";

// Set before the scenarios load: their set-up reads it.
globalThis.COV_VOICE_WORKER_CPU = true;
const { cases } = await import("./cov-voice-worker-cases.mjs");

for (const args of cases) {
  test(...args);
}
