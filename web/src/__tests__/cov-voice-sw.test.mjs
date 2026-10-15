// The service worker (sw.ts) with its precache manifest: the scenarios of cov-voice-sw-cases.mjs.
import test from "node:test";

const { cases } = await import("./cov-voice-sw-cases.mjs");

for (const args of cases) {
  test(...args);
}
