// localVoice.ts once more, in a browser without Cache Storage (see cov-voice-localvoice-lib.test.mjs).
globalThis.COV_VOICE_NO_CACHE = true;
await import("./cov-voice-localvoice-lib.test.mjs");
