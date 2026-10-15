// The local voices' worker once more, in a browser without WebGPU where Supertonic's runtime
// cannot be had at first (see cov-voice-worker.test.mjs).
globalThis.COV_VOICE_WORKER_CPU = true;
await import("./cov-voice-worker.test.mjs");
