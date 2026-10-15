// localVoice.ts in a browser with Cache Storage: the scenarios of cov-voice-localvoice-cases.mjs.
import test from "node:test";

const { cases } = await import("./cov-voice-localvoice-cases.mjs");

for (const args of cases) {
  test(...args);
}
