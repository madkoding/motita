// localVoice.ts once more, in a browser without Cache Storage (see cov-voice-localvoice-cases.mjs).
import test from "node:test";

// Set before the scenarios load: their set-up reads it.
globalThis.COV_VOICE_NO_CACHE = true;
const { cases } = await import("./cov-voice-localvoice-cases.mjs");

for (const args of cases) {
  test(...args);
}
