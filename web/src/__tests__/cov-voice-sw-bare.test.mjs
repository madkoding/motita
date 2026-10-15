// The service worker built without a precache manifest: it installs with nothing to precache.
import test from "node:test";

// Set before the scenarios load: their set-up reads it.
globalThis.COV_VOICE_SW_BARE = true;
const { cases } = await import("./cov-voice-sw-cases.mjs");

for (const args of cases) {
  test(...args);
}
