// Runs conversation mode in jsdom with a stand-in microphone (bundled with esbuild, since node
// cannot import TSX): when an answer to a spoken turn has been read, one phrase is listened for,
// ended by the silence after it, transcribed and sent; an exit phrase, a tap or a silence ends it.
import test from "node:test";
import assert from "node:assert/strict";
import { fileURLToPath, pathToFileURL } from "node:url";
import { dirname, join } from "node:path";
import { mkdtempSync, rmSync } from "node:fs";
import { JSDOM } from "jsdom";
import { build } from "esbuild";

const here = dirname(fileURLToPath(import.meta.url));
const dom = new JSDOM("<!doctype html><html><body></body></html>", {
  url: "https://localhost/",
});
for (const k of [
  "document",
  "window",
  "Event",
  "CustomEvent",
  "HTMLElement",
  "Node",
  "localStorage",
]) {
  globalThis[k] = dom.window[k];
}
globalThis.isSecureContext = true;

// The stand-in microphone: loud says whether the person is speaking now.
const mic = { loud: false, opened: 0, stopped: 0 };
Object.defineProperty(globalThis, "navigator", {
  value: {
    mediaDevices: {
      getUserMedia: async () => {
        mic.opened += 1;
        return { getTracks: () => [{ stop: () => (mic.stopped += 1) }] };
      },
    },
  },
  configurable: true,
});
globalThis.MediaRecorder = class {
  static isTypeSupported(t) {
    return t.startsWith("audio/webm");
  }
  constructor() {
    this.mimeType = "audio/webm";
  }
  start() {}
  stop() {
    this.ondataavailable?.({
      data: new Blob(["voice"], { type: "audio/webm" }),
    });
    this.onstop?.();
  }
};
globalThis.AudioContext = class {
  createAnalyser() {
    return {
      fftSize: 0,
      getByteTimeDomainData(frame) {
        frame.fill(mic.loud ? 200 : 128);
      },
    };
  }
  createMediaStreamSource() {
    return { connect() {} };
  }
  close() {
    return Promise.resolve();
  }
};

const out = mkdtempSync(join(here, ".vc-"));
await build({
  sourcemap: "inline",
  stdin: {
    contents:
      'export { useVoice } from "./useVoice";\nexport { useVoiceConversation } from "./useVoiceConversation";\n',
    resolveDir: join(here, ".."),
    loader: "ts",
  },
  outfile: join(out, "vc.mjs"),
  bundle: true,
  format: "esm",
  platform: "node",
  external: ["preact", "preact/*"],
  logLevel: "silent",
});
const { useVoice, useVoiceConversation } = await import(
  pathToFileURL(join(out, "vc.mjs")).href
);
const { SPEECH_TURN_READ_EVENT } = await import("../speech.ts");
const { h, render } = await import("preact");
const { act } = await import("preact/test-utils");
rmSync(out, { recursive: true });

const settle = () => act(() => new Promise((r) => setImmediate(r)));

// mount runs both hooks; the gateway transcribes to `heard`.
async function mount(heard) {
  const state = { sent: [], toasts: [], current: null, asked: 0 };
  const api = async (path) => {
    if (path === "/v1/voice") {
      return new Response(
        JSON.stringify({
          enabled: true,
          wake_phrases: [],
          max_audio_bytes: 1e6,
        }),
        { status: 200 },
      );
    }
    state.asked += 1;
    return new Response(JSON.stringify({ text: heard }), { status: 200 });
  };
  const root = document.createElement("div");
  document.body.appendChild(root);
  function Host() {
    const voice = useVoice({
      api,
      authed: true,
      setInput: () => {},
      setToast: (t) => state.toasts.push(t),
    });
    state.current = voice;
    useVoiceConversation({
      on: true,
      ready: voice.state === "ready",
      listenOnce: voice.listenOnce,
      cancelListen: voice.cancelListen,
      send: (text) => state.sent.push(text),
      setToast: (t) => state.toasts.push(t),
    });
    return null;
  }
  await act(() => render(h(Host, {}), root));
  await settle();
  return {
    state,
    done: async () => {
      await act(() => render(null, root));
      root.remove();
    },
  };
}

const read = () =>
  act(() => window.dispatchEvent(new CustomEvent(SPEECH_TURN_READ_EVENT)));

// run advances the stand-in clock by ms, a frame at a time.
async function run(t, ms) {
  for (let i = 0; i < ms; i += 50) {
    t.mock.timers.tick(50);
    await settle();
  }
}

test("a phrase after the answer is heard to its end and sent, and it counts as spoken", async (t) => {
  t.mock.timers.enable({ apis: ["setInterval", "Date"] });
  const s = await mount("y ahora corre los tests");
  assert.equal(s.state.current.state, "ready");
  await read();
  await settle();
  assert.equal(mic.opened, 1, "the microphone opens on its own");
  assert.equal(s.state.current.phase, "recording");
  mic.loud = true;
  await run(t, 1000);
  mic.loud = false;
  await run(t, 1000);
  await settle();
  assert.deepEqual(s.state.sent, ["y ahora corre los tests"]);
  assert.equal(
    s.state.current.takeHeard(),
    true,
    "its answer is read as a spoken turn's",
  );
  assert.equal(mic.stopped, 1, "the microphone is let go");
  assert.equal(s.state.current.phase, "idle");
  await s.done();
});

test("an exit phrase ends the conversation without being sent", async (t) => {
  t.mock.timers.enable({ apis: ["setInterval", "Date"] });
  const s = await mount("Motita, basta.");
  await read();
  await settle();
  mic.loud = true;
  await run(t, 1000);
  mic.loud = false;
  await run(t, 1000);
  await settle();
  assert.deepEqual(s.state.sent, []);
  assert.match(s.state.toasts.at(-1).message, /Conversation ended/);
  await s.done();
});

test("a silence or a tap on the microphone ends it, and nothing is transcribed", async (t) => {
  t.mock.timers.enable({ apis: ["setInterval", "Date"] });
  const s = await mount("never");
  await read();
  await settle();
  await run(t, 8500);
  await settle();
  assert.equal(
    s.state.asked,
    0,
    "nobody spoke: nothing is sent to be transcribed",
  );
  assert.equal(s.state.current.phase, "idle");

  await read();
  await settle();
  assert.equal(s.state.current.phase, "recording");
  await act(() => s.state.current.toggleDictation());
  await settle();
  assert.equal(
    s.state.current.phase,
    "idle",
    "the tap ends the listening, it starts no dictation",
  );
  assert.equal(s.state.asked, 0);
  assert.deepEqual(s.state.sent, []);

  // Leaving the page while it listens lets the microphone go.
  await read();
  await settle();
  const before = mic.stopped;
  await s.done();
  await settle();
  assert.equal(mic.stopped, before + 1);
});
