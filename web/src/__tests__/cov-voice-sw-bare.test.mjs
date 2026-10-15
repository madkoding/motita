// The service worker built without a precache manifest: it installs with nothing to precache.
globalThis.COV_VOICE_SW_BARE = true;
await import("./cov-voice-sw.test.mjs");
