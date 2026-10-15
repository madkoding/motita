// useVoice and useVoiceConversation with a stand-in microphone and a mocked clock: the dictation
// button, conversation mode's one phrase, the hands-free mode that listens for a wake phrase, and
// what each one does when the microphone, the gateway or the person does not cooperate.
import test, { mock } from "node:test";
import assert from "node:assert/strict";
import { setupDom, source, settle as settleWith } from "./cov-voice-helpers.mjs";

setupDom();
globalThis.isSecureContext = true;

// The stand-in microphone. loud says whether the person speaks now; refuse holds errors the next
// getUserMedia calls throw; mime is the one type the recorder supports ("" none).
const mic = { loud: false, refuse: [], asked: [], stopped: 0, recorders: [], mime: "audio/webm" };
const mediaDevices = {
  async getUserMedia(c) {
    mic.asked.push(c);
    const e = mic.refuse.shift();
    if (e) {
      throw e;
    }
    return { getTracks: () => [{ stop: () => mic.stopped++ }] };
  },
};
Object.defineProperty(navigator, "mediaDevices", { value: mediaDevices, configurable: true });
class FakeRecorder {
  static isTypeSupported(t) {
    return t === mic.mime;
  }
  constructor(stream, opts) {
    this.opts = opts;
    this.mimeType = opts?.mimeType ?? "";
    this.state = "inactive";
    mic.recorders.push(this);
  }
  start() {
    this.state = "recording";
  }
  stop() {
    this.state = "inactive";
    this.ondataavailable?.({ data: new Blob([]) });
    this.ondataavailable?.({ data: new Blob(["voice"]) });
    this.onstop?.();
  }
}
globalThis.MediaRecorder = FakeRecorder;
const ctxs = { made: 0, closed: 0, emptyFrames: false };
globalThis.AudioContext = class {
  constructor() {
    ctxs.made++;
  }
  createAnalyser() {
    // A stand-in analyser can be told to hand back no samples at all.
    const a = {
      size: 0,
      get fftSize() {
        return ctxs.emptyFrames ? 0 : this.size;
      },
      set fftSize(v) {
        this.size = v;
      },
      getByteTimeDomainData(frame) {
        frame.fill(mic.loud ? 200 : 128);
      },
    };
    return a;
  }
  createMediaStreamSource() {
    return { connect() {} };
  }
  close() {
    ctxs.closed++;
    return Promise.resolve();
  }
};
// The browser runs a model to listen with: its worker answers each recording with the next of
// `heard.texts`, and decoding a recording gives one sample.
const heard = { texts: [], sent: [] };
globalThis.Worker = class {
  postMessage(m) {
    heard.sent.push(m);
    if (m.kind === "hear") {
      void Promise.resolve().then(() => this.onmessage({ data: { seq: m.seq, text: heard.texts.shift() ?? "", lang: "es" } }));
    }
  }
};
globalThis.OfflineAudioContext = class {
  async decodeAudioData() {
    return { length: 1, numberOfChannels: 1, getChannelData: () => Float32Array.of(0) };
  }
};
globalThis.caches = {};
// The device's own voice, which hands-free listening must not take for the person.
globalThis.speechSynthesis = { speaking: false };

const { useVoice } = await source("useVoice.ts");
const { useVoiceConversation } = await source("useVoiceConversation.ts");
const { SPEECH_TURN_READ_EVENT, SPEECH_STOP_EVENT } = await import("../speech.ts");
const { VOICE_KEY, VOICE_PREFS_EVENT } = await import("../voice.ts");
const { h, render } = await import("preact");
const { act } = await import("preact/test-utils");

const settle = () => settleWith(act, 4);
// The clock: timers and Date are mocked, and performance.now follows Date.
mock.timers.enable({ apis: ["setInterval", "setTimeout", "Date"], now: 1_000_000 });
Object.defineProperty(performance, "now", { value: () => Date.now(), configurable: true });
async function advance(ms, step = 50) {
  for (let t = 0; t < ms; t += step) {
    await act(() => mock.timers.tick(step));
    await settle();
  }
}

const INFO = { enabled: true, wake_phrases: ["hey motita"], max_audio_bytes: 1e6 };

// mount renders both hooks. replies are the gateway's answers to transcriptions, in order (an
// Error rejects); info is GET /v1/voice's answer (or an Error to throw).
async function mount({ info = INFO, replies = [], authed = true, conversation = false } = {}) {
  const s = { input: "", toasts: [], sent: [], asked: [], voice: null };
  const api = async (path, init) => {
    s.asked.push([path, init?.headers?.["Content-Type"]]);
    if (path === "/v1/voice") {
      if (info instanceof Error) {
        throw info;
      }
      if (info === null) {
        return new Response("{}", { status: 404 });
      }
      return new Response(JSON.stringify(info), { status: 200 });
    }
    const r = await replies.shift();
    if (r instanceof Error) {
      return new Response(JSON.stringify({ error: r.message }), { status: 502 });
    }
    return new Response(JSON.stringify(r ?? { text: "" }), { status: 200 });
  };
  const setInput = (f) => {
    s.input = typeof f === "function" ? f(s.input) : f;
  };
  const setToast = (t) => s.toasts.push(t);
  const root = document.createElement("div");
  document.body.appendChild(root);
  let props = { conversation, authed };
  function Host(p) {
    const voice = useVoice({ api, authed: p.authed, setInput, setToast });
    s.voice = voice;
    useVoiceConversation({
      on: p.conversation,
      ready: voice.state === "ready",
      listenOnce: voice.listenOnce,
      cancelListen: voice.cancelListen,
      send: (text) => s.sent.push(text),
      setToast,
    });
    return null;
  }
  const set = async (next) => {
    props = { ...props, ...next };
    await act(() => render(h(Host, props), root));
    await settle();
  };
  await set({});
  return {
    s,
    set,
    done: async () => {
      await act(() => render(null, root));
      root.remove();
      await settle();
    },
  };
}
// tap presses the dictation button; its promise runs until the recording is transcribed.
const tap = (v) =>
  act(() => {
    void v.s.voice.toggleDictation();
  });
const named = (name) => Object.assign(new Error(name), { name });
const reset = () => {
  localStorage.clear();
  mic.loud = false;
  mic.refuse = [];
  mic.asked = [];
  mic.mime = "audio/webm";
  speechSynthesis.speaking = false;
  ctxs.emptyFrames = false;
};

// speak says one phrase: loud for `ms`, then silence until the gate closes it.
async function speak(ms = 600) {
  mic.loud = true;
  await advance(ms);
  mic.loud = false;
  await advance(1000);
}

test("what the page says before it can listen: not signed in, a refused gateway, an older one", async () => {
  reset();
  // With no service on, the browser is the one to listen: it needs a model first.
  let v = await mount({ authed: false });
  assert.equal(v.s.asked.length, 0, "nothing is asked before signing in");
  assert.equal(v.s.voice.state, "no-model");
  assert.equal(v.s.voice.engine, "local");
  assert.equal(v.s.voice.showMic, false);
  await v.done();

  v = await mount({ info: new Error("offline") });
  assert.equal(v.s.voice.info, null);
  assert.equal(v.s.voice.state, "no-model");
  await v.done();

  // The service chosen, but off (or a gateway too old to say).
  localStorage.setItem(VOICE_KEY, JSON.stringify({ engine: "service" }));
  v = await mount({ info: null });
  assert.equal(v.s.voice.state, "off", "an older gateway answers 404");
  await v.done();
  localStorage.clear();

  v = await mount({ info: { ...INFO, wake_phrases: [] } });
  assert.equal(v.s.voice.state, "ready");
  assert.equal(v.s.voice.hands, "no-phrases");
  assert.equal(v.s.voice.showMic, true);
  await v.done();
});

test("the switches are saved, and the page follows a change made elsewhere", async () => {
  reset();
  localStorage.setItem(VOICE_KEY, JSON.stringify({ mic: false, deviceId: 3 }));
  const v = await mount();
  assert.deepEqual(v.s.voice.prefs, { mic: false, listen: false, deviceId: "", engine: "", listener: "", language: "" });
  assert.equal(v.s.voice.showMic, false);
  await act(() => v.s.voice.setPrefs({ mic: true, listen: false, deviceId: "usb" }));
  assert.equal(v.s.voice.prefs.deviceId, "usb");
  assert.equal(JSON.parse(localStorage.getItem(VOICE_KEY)).deviceId, "usb");
  localStorage.setItem(VOICE_KEY, JSON.stringify({ mic: true, deviceId: "other" }));
  await act(() => window.dispatchEvent(new CustomEvent(VOICE_PREFS_EVENT)));
  assert.equal(v.s.voice.prefs.deviceId, "other");
  await act(() => v.s.voice.setInfo({ ...INFO, enabled: false }));
  assert.equal(v.s.voice.state, "no-model", "the settings' answer applies at once: the browser listens now");
  await v.done();
});

test("dictation: a tap records, the next one transcribes into the composer and focuses it", async () => {
  reset();
  const box = document.createElement("textarea");
  box.id = "task";
  document.body.appendChild(box);
  const v = await mount({ replies: [{ text: "fix the tests" }, { text: "  " }] });
  let stops = 0;
  const onStop = () => stops++;
  window.addEventListener(SPEECH_STOP_EVENT, onStop);
  v.s.input = "please";
  await tap(v);
  await settle();
  assert.equal(stops, 1, "Motita stops talking over the person");
  assert.equal(v.s.voice.phase, "recording");
  assert.deepEqual(mic.asked.at(-1), { audio: true });
  assert.deepEqual(mic.recorders.at(-1).opts, { mimeType: "audio/webm" });
  await tap(v);
  await settle();
  assert.equal(v.s.voice.phase, "idle");
  assert.equal(v.s.input, "please fix the tests");
  assert.equal(document.activeElement, box);
  assert.deepEqual(v.s.asked.at(-1), ["/v1/voice/transcribe", "audio/webm"]);
  assert.equal(v.s.voice.takeHeard(), true);
  assert.equal(v.s.voice.takeHeard(), false, "it is forgotten once taken");

  // Silence transcribed to nothing: the composer keeps its text, and nothing was heard.
  box.remove();
  mic.mime = "";
  await tap(v);
  await settle();
  assert.equal(mic.recorders.at(-1).opts, undefined, "the recorder chooses its own type");
  await tap(v);
  await settle();
  assert.equal(v.s.input, "please fix the tests");
  assert.equal(v.s.voice.takeHeard(), false);
  assert.deepEqual(v.s.asked.at(-1), ["/v1/voice/transcribe", "audio/webm"], "a recording with no type");
  window.removeEventListener(SPEECH_STOP_EVENT, onStop);
  await v.done();
});

test("dictation stops on its own after two minutes", async () => {
  reset();
  const v = await mount({ replies: [{ text: "a long one" }] });
  await tap(v);
  await settle();
  assert.equal(v.s.voice.phase, "recording");
  await act(() => mock.timers.tick(120_000));
  await settle();
  assert.equal(v.s.voice.phase, "idle", "the recording was cut and transcribed");
  assert.equal(v.s.input, "a long one");
  await v.done();
});

test("dictation: the chosen microphone, the default one when it is gone, and the failures said", async () => {
  reset();
  localStorage.setItem(VOICE_KEY, JSON.stringify({ deviceId: "usb-1" }));
  const v = await mount({ replies: [new Error("bad key")] });
  mic.refuse = [named("OverconstrainedError")];
  await tap(v);
  await settle();
  assert.deepEqual(mic.asked.slice(-2), [{ audio: { deviceId: { exact: "usb-1" } } }, { audio: true }]);
  assert.equal(v.s.voice.phase, "recording");
  await tap(v);
  await settle();
  assert.deepEqual(v.s.toasts.at(-1), {
    message: "Speech input failed",
    detail: "bad key",
    type: "error",
  });
  assert.equal(v.s.voice.phase, "idle");

  mic.refuse = [named("NotAllowedError")];
  await tap(v);
  await settle();
  assert.equal(v.s.toasts.at(-1).detail, "NotAllowedError", "a refusal is not a missing microphone");
  assert.equal(v.s.voice.phase, "idle");
  await v.done();

  reset();
  const w = await mount();
  mic.refuse = [named("NotFoundError")];
  await tap(w);
  await settle();
  assert.equal(mic.asked.length, 1, "no other microphone to fall back on");
  assert.equal(w.s.toasts.at(-1).detail, "NotFoundError");
  await w.done();
});

test("a tap while the transcription runs is ignored", async () => {
  reset();
  let answer;
  const v = await mount({ replies: [new Promise((r) => (answer = r))] });
  await tap(v);
  await settle();
  await tap(v);
  await settle();
  assert.equal(v.s.voice.phase, "transcribing");
  const recorders = mic.recorders.length;
  await tap(v);
  await settle();
  assert.equal(mic.recorders.length, recorders, "no new recording");
  assert.equal(await v.s.voice.listenOnce(), null, "nor a conversation phrase");
  answer({ text: "done" });
  await settle();
  assert.equal(v.s.voice.phase, "idle");
  assert.equal(v.s.input, "done");
  await v.done();
});

test("conversation: after a spoken answer was read, one phrase is heard and sent", async () => {
  reset();
  const v = await mount({ conversation: true, replies: [{ text: "run the tests" }] });
  await act(() => window.dispatchEvent(new CustomEvent(SPEECH_TURN_READ_EVENT)));
  await settle();
  assert.equal(v.s.voice.phase, "recording");
  await speak();
  assert.deepEqual(v.s.sent, ["run the tests"]);
  assert.equal(v.s.voice.phase, "idle");
  assert.equal(ctxs.closed, ctxs.made, "the audio context is closed");
  assert.equal(v.s.voice.takeHeard(), true);
  await v.done();
});

test("conversation: an exit phrase ends it, silence ends it, and nothing heard is not sent", async () => {
  reset();
  const v = await mount({
    conversation: true,
    replies: [{ text: "Basta, Motita." }, { text: "  " }, new Error("down")],
  });
  const read = async () => {
    await act(() => window.dispatchEvent(new CustomEvent(SPEECH_TURN_READ_EVENT)));
    await settle();
  };
  await read();
  await speak();
  assert.deepEqual(v.s.sent, []);
  assert.deepEqual(v.s.toasts.at(-1), { message: "Conversation ended", type: "success" });
  assert.equal(v.s.voice.takeHeard(), true, "the exit phrase was heard");

  await read();
  await speak();
  assert.deepEqual(v.s.sent, [], "an empty transcription is nobody speaking");
  assert.equal(v.s.voice.takeHeard(), false);

  await read();
  await speak();
  assert.equal(v.s.toasts.at(-1).detail, "down");
  assert.deepEqual(v.s.sent, []);

  await read();
  await advance(8200);
  assert.equal(v.s.voice.phase, "idle", "nobody started speaking: it stops listening");
  assert.deepEqual(v.s.sent, []);

  // An analyser that gives no samples is silence too.
  ctxs.emptyFrames = true;
  await read();
  await advance(8200);
  assert.equal(v.s.voice.phase, "idle");
  ctxs.emptyFrames = false;

  mic.refuse = [named("NotAllowedError")];
  await read();
  assert.equal(v.s.toasts.at(-1).detail, "NotAllowedError");
  await v.done();
});

test("conversation: a tap or leaving ends the listening, and a busy microphone is not shared", async () => {
  reset();
  const v = await mount({ conversation: true });
  const read = async () => {
    await act(() => window.dispatchEvent(new CustomEvent(SPEECH_TURN_READ_EVENT)));
    await settle();
  };
  await read();
  assert.equal(v.s.voice.phase, "recording");
  // A second read while listening: the microphone is already taken.
  assert.equal(await v.s.voice.listenOnce(), null);
  await tap(v);
  await settle();
  assert.equal(v.s.voice.phase, "idle", "the tap ended the conversation, not a dictation");
  assert.deepEqual(v.s.sent, []);

  // While a dictation records, conversation mode does not listen.
  await tap(v);
  await settle();
  assert.equal(await v.s.voice.listenOnce(), null);
  await tap(v);
  await settle();

  // Turned off while it listens: the listening is cancelled.
  await read();
  assert.equal(v.s.voice.phase, "recording");
  await v.set({ conversation: false });
  assert.equal(v.s.voice.phase, "idle");
  assert.deepEqual(v.s.sent, []);
  v.s.voice.cancelListen();

  // The answer of a phrase that comes back once conversation mode was left is dropped.
  await v.set({ conversation: true });
  v.s.asked.length = 0;
  await read();
  mic.loud = true;
  await advance(600);
  mic.loud = false;
  await advance(1000);
  await v.set({ conversation: false });
  await settle();
  assert.deepEqual(v.s.sent, []);
  await v.done();
});

test("hands-free: a phrase that opens with a wake phrase reaches the composer", async () => {
  reset();
  localStorage.setItem(VOICE_KEY, JSON.stringify({ listen: true }));
  const box = document.createElement("textarea");
  box.id = "task";
  document.body.appendChild(box);
  const v = await mount({
    replies: [
      { text: "hey motita open a pr", wake: { phrase: "hey motita", rest: "open a pr" } },
      { text: "just talking" },
      { text: "hey motita", wake: { phrase: "hey motita", rest: "" } },
      new Error("gateway down"),
    ],
  });
  assert.equal(v.s.voice.listening, true);
  await speak();
  assert.equal(v.s.input, "open a pr");
  assert.deepEqual(v.s.toasts.at(-1), { message: "Heard “hey motita”", type: "success" });
  assert.equal(document.activeElement, box);
  assert.deepEqual(v.s.asked.at(-1), ["/v1/voice/transcribe?wake=1", "audio/webm"]);
  assert.equal(v.s.voice.takeHeard(), true);

  await speak();
  assert.equal(v.s.input, "open a pr", "without the wake phrase nothing is written");
  await speak();
  assert.equal(v.s.input, "open a pr", "the wake phrase alone adds nothing");
  await speak();
  assert.equal(v.s.toasts.at(-1).detail, "gateway down");

  // Too short to be words: thrown away. Long silence: a fresh recording.
  const before = mic.recorders.length;
  mic.loud = true;
  await advance(100);
  mic.loud = false;
  await advance(1000);
  assert.ok(mic.recorders.length > before, "the short sound was dropped for a new recording");
  const quiet = mic.recorders.length;
  await advance(4200);
  assert.ok(mic.recorders.length > quiet, "silence starts over");

  // Motita's own voice is never taken for the person's.
  speechSynthesis.speaking = true;
  mic.loud = true;
  await advance(600);
  const sentBefore = v.s.asked.length;
  speechSynthesis.speaking = false;
  mic.loud = false;
  await advance(1200);
  assert.equal(v.s.asked.length, sentBefore, "what was recorded while it spoke is not sent");

  // A phrase that ends while a dictation records is not sent.
  await tap(v);
  await settle();
  const asked = v.s.asked.length;
  mic.loud = true;
  await advance(600);
  mic.loud = false;
  await advance(1000);
  assert.equal(v.s.asked.length, asked);
  await tap(v);
  await settle();

  const closed = ctxs.closed;
  await v.done();
  assert.equal(ctxs.closed, closed + 1, "leaving closes the microphone");
  box.remove();
});

test("listening in the browser: neither dictation nor the wake phrase leaves it", async () => {
  reset();
  localStorage.setItem(VOICE_KEY, JSON.stringify({ engine: "local", listener: "whisper-base", language: "es" }));
  heard.texts = ["arregla los tests"];
  heard.sent.length = 0;
  let v = await mount({ info: null });
  assert.equal(v.s.voice.state, "ready");
  await tap(v);
  await settle();
  assert.deepEqual(heard.sent[0], { kind: "warm", id: "whisper-base" }, "the model loads while the person speaks");
  await tap(v);
  await settle();
  assert.equal(v.s.input, "arregla los tests");
  const hear = heard.sent.find((m) => m.kind === "hear");
  assert.equal(hear.id, "whisper-base");
  assert.equal(hear.lang, "es");
  assert.ok(!v.s.asked.some(([p]) => p.startsWith("/v1/voice/transcribe")), "no recording reaches the gateway");
  await v.done();

  // Hands-free: the wake phrase is looked for here, among the ones the gateway listed; what it
  // heard is never sent anywhere.
  localStorage.setItem(
    VOICE_KEY,
    JSON.stringify({ engine: "local", listener: "whisper-base", listen: true }),
  );
  heard.texts = ["Eh, hey Mótita: abre un pr", "", "hola a todos"];
  v = await mount();
  assert.equal(v.s.voice.listening, true);
  await speak();
  assert.equal(v.s.input, "abre un pr");
  assert.equal(v.s.toasts.at(-1).message, "Heard “hey motita”");
  await speak();
  await speak();
  assert.equal(v.s.input, "abre un pr", "nothing heard, or no wake phrase: nothing written");
  assert.deepEqual(
    v.s.asked.map(([p]) => p),
    ["/v1/voice"],
    "the gateway is only asked for its settings",
  );
  await v.done();

  // Before the gateway lists its phrases, nothing wakes it.
  heard.texts = ["hey motita go"];
  v = await mount({ info: null });
  await speak();
  assert.equal(v.s.input, "");
  await v.done();
});

test("hands-free: an answer that comes after it was turned off is dropped", async () => {
  reset();
  localStorage.setItem(VOICE_KEY, JSON.stringify({ listen: true }));
  const v = await mount({ replies: [{ text: "hey motita go", wake: { phrase: "hey motita", rest: "go" } }] });
  mic.loud = true;
  await advance(600);
  mic.loud = false;
  // The phrase closes on this frame; it is turned off before the answer is read.
  await act(() => mock.timers.tick(950));
  await v.done();
  await settle();
  assert.equal(v.s.input, "", "nothing written after it was turned off");
});

test("hands-free: a microphone that cannot open, and one that opens after leaving", async () => {
  reset();
  localStorage.setItem(VOICE_KEY, JSON.stringify({ listen: true }));
  mic.refuse = [named("NotAllowedError")];
  let v = await mount();
  assert.equal(v.s.toasts.at(-1).detail, "NotAllowedError");
  assert.equal(v.s.voice.listening, false);
  await v.done();

  // The page leaves before the browser hands the microphone over: it is closed at once.
  let open;
  const realGet = mediaDevices.getUserMedia;
  mediaDevices.getUserMedia = () =>
    new Promise((r) => {
      open = () => r({ getTracks: () => [{ stop: () => mic.stopped++ }] });
    });
  const root = document.createElement("div");
  const { h: hh } = await import("preact");
  function Host() {
    useVoice({ api: async () => new Response(JSON.stringify(INFO)), authed: true, setInput() {}, setToast() {} });
    return null;
  }
  await act(() => render(hh(Host, {}), root));
  await settle();
  const stopped = mic.stopped;
  await act(() => render(null, root));
  open();
  await settle();
  assert.equal(mic.stopped, stopped + 1);
  mediaDevices.getUserMedia = realGet;
});
