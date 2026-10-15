// voice.ts: the switches kept in the browser, why speech input can or cannot
// be used, the microphones listed, the permission read, the recording types and the requests.
import test from "node:test";
import assert from "node:assert/strict";
import { setupDom, json } from "./cov-voice-helpers.mjs";

setupDom();
// voice.ts needs no compiling: node imports it as it is, as voice.test.mjs does.
const copies = [["native", await import("../voice.ts")]];
for (const [how, m] of copies) {

test(how + ": the switches are read, kept, and fall back when the storage cannot be used", () => {
  localStorage.removeItem(m.VOICE_KEY);
  assert.deepEqual(m.loadVoicePrefs(), { mic: true, listen: false, deviceId: "" });
  localStorage.setItem(m.VOICE_KEY, JSON.stringify({ mic: false, listen: true, deviceId: 7 }));
  assert.deepEqual(m.loadVoicePrefs(), { mic: false, listen: true, deviceId: "" });
  let told = 0;
  const onPrefs = () => told++;
  window.addEventListener(m.VOICE_PREFS_EVENT, onPrefs);
  m.saveVoicePrefs({ mic: true, listen: false, deviceId: "usb-1" });
  assert.equal(told, 1);
  assert.equal(m.loadVoicePrefs().deviceId, "usb-1");
  localStorage.setItem(m.VOICE_KEY, "{broken");
  assert.deepEqual(m.loadVoicePrefs(), m.DEFAULT_VOICE_PREFS);

  // A storage that refuses (a private window) and a page with no window still work.
  const store = globalThis.localStorage;
  const win = globalThis.window;
  globalThis.localStorage = {
    getItem() {
      throw new Error("denied");
    },
    setItem() {
      throw new Error("denied");
    },
  };
  delete globalThis.window;
  try {
    assert.doesNotThrow(() => m.saveVoicePrefs({ mic: false, listen: false, deviceId: "" }));
    assert.deepEqual(m.loadVoicePrefs(), m.DEFAULT_VOICE_PREFS);
  } finally {
    globalThis.localStorage = store;
    globalThis.window = win;
  }
  assert.equal(told, 1, "no window, no event");
  window.removeEventListener(m.VOICE_PREFS_EVENT, onPrefs);
});

test(how + ": the state says what to fix first, and hands-free needs a phrase", () => {
  const on = { enabled: true, wake_phrases: ["hey"], max_audio_bytes: 1 };
  assert.equal(m.voiceState(false, true, on), "insecure");
  assert.equal(m.voiceState(true, false, on), "unsupported");
  assert.equal(m.voiceState(true, true, null), "off");
  assert.equal(m.voiceState(true, true, on), "ready");
  assert.equal(m.listenState("off", on), "off");
  assert.equal(m.listenState("ready", null), "no-phrases");
  assert.equal(m.listenState("ready", { ...on, wake_phrases: [] }), "no-phrases");
  assert.equal(m.listenState("ready", on), "ready");
  assert.deepEqual(m.parsePhrases("hey, Hey\n\n ok "), ["hey", "ok"]);
});

test(how + ": the microphones are listed once each, numbered when the browser hides their names", () => {
  const list = m.micOptions(
    [
      { kind: "videoinput", deviceId: "cam", label: "Cam" },
      { kind: "audioinput", deviceId: "", label: "" },
      { kind: "audioinput", deviceId: "default", label: "Default" },
      { kind: "audioinput", deviceId: "a", label: "" },
      { kind: "audioinput", deviceId: "a", label: "again" },
      { kind: "audioinput", deviceId: "b", label: "USB" },
    ],
    (n) => "Mic " + n,
  );
  assert.deepEqual(list, [
    { deviceId: "a", label: "Mic 1" },
    { deviceId: "b", label: "USB" },
  ]);
  assert.equal(m.micNamesHidden([{ kind: "audioinput", label: "" }]), true);
  assert.equal(m.micNamesHidden([{ kind: "audioinput", label: "x" }]), false);
  assert.deepEqual(m.audioConstraints(""), { audio: true });
  assert.deepEqual(m.audioConstraints("x"), { audio: { deviceId: { exact: "x" } } });
  assert.equal(m.isMissingDevice({ name: "OverconstrainedError" }), true);
  assert.equal(m.isMissingDevice({ name: "NotFoundError" }), true);
  assert.equal(m.isMissingDevice(null), false);
  assert.equal(m.isPermissionRefused({ name: "NotAllowedError" }), true);
  assert.equal(m.isPermissionRefused({ name: "SecurityError" }), true);
  assert.equal(m.isPermissionRefused({ name: "NotFoundError" }), false);
  assert.equal(m.isPermissionRefused(null), false);
});

test(how + ": the microphone permission: the Permissions API, else the device names, else not asked", async () => {
  const status = { state: "denied" };
  assert.deepEqual(await m.queryMicPermission({ permissions: { query: async () => status } }), {
    state: "denied",
    status,
  });
  assert.equal(m.micPermissionOf("granted"), "granted");
  assert.equal(m.micPermissionOf("weird"), "prompt");
  const named = { mediaDevices: { enumerateDevices: async () => [{ kind: "audioinput", label: "Mic" }] } };
  assert.deepEqual(
    await m.queryMicPermission({
      permissions: {
        query: async () => {
          throw new TypeError("microphone");
        },
      },
      ...named,
    }),
    { state: "granted" },
  );
  assert.deepEqual(
    await m.queryMicPermission({ permissions: { query: async () => undefined }, ...named }),
    { state: "granted" },
  );
  assert.deepEqual(
    await m.queryMicPermission({
      mediaDevices: { enumerateDevices: async () => [{ kind: "audioinput", label: "" }] },
    }),
    { state: "prompt" },
  );
  assert.deepEqual(await m.queryMicPermission(undefined), { state: "prompt" }, "nothing to tell from");
  assert.deepEqual(
    await m.queryMicPermission({
      mediaDevices: {
        enumerateDevices: async () => {
          throw new Error("no");
        },
      },
    }),
    { state: "prompt" },
  );
});

test(how + ": the recording type, the level and the gate", () => {
  assert.equal(m.pickMimeType((t) => t === "audio/mp4"), "audio/mp4");
  assert.equal(m.pickMimeType(() => false), "");
  assert.equal(m.level(new Uint8Array()), 0);
  assert.equal(m.level(new Uint8Array([0, 0])), 1);
  const g = new m.SpeechGate({ threshold: 0.5, silenceMs: 100, minMs: 50, maxMs: 1000 });
  assert.equal(g.feed(0, 0), null);
  assert.equal(g.feed(1, 0), "start");
  assert.equal(g.speaking, true);
  assert.equal(g.feed(0, 50), null);
  assert.equal(g.feed(0, 200), "discard");
  assert.equal(g.feed(1, 300), "start");
  assert.equal(g.feed(1, 400), null);
  assert.equal(g.feed(0, 600), "end");
  assert.equal(g.feed(1, 700), "start");
  assert.equal(g.feed(1, 1800), "end", "too long");
  assert.equal(m.appendText("a", " "), "a");
  assert.equal(m.appendText(" ", "b"), "b");
  assert.equal(m.appendText("a ", "b"), "a b");
});

test(how + ": the requests send what the gateway reads and report its refusal", async () => {
  const calls = [];
  const api = (replies) => async (path, init) => {
    calls.push([path, init?.method, init?.headers?.["Content-Type"]]);
    return replies.shift();
  };
  const info = { enabled: true, wake_phrases: [], max_audio_bytes: 1 };
  assert.deepEqual(await m.fetchVoiceInfo(api([json(info)])), info);
  assert.equal(await m.fetchVoiceInfo(api([json({}, 404)])), null);
  assert.deepEqual(await m.putVoice(api([json(info)]), { enabled: true }), info);
  await assert.rejects(m.putVoice(api([json({ error: "nope" }, 400)]), { enabled: true }), /nope/);
  await assert.rejects(m.putVoice(api([json({}, 500)]), { enabled: true }), /HTTP 500/);
  await assert.rejects(
    m.putVoice(api([new Response("<html>", { status: 502 })]), { enabled: true }),
    /HTTP 502/,
  );
  calls.length = 0;
  assert.deepEqual(await m.transcribe(api([json({ text: "hi" })]), new Blob(["x"]), true), { text: "hi" });
  await m.transcribe(api([json({ text: "" })]), new Blob(["x"], { type: "audio/mp4" }), false);
  assert.deepEqual(calls, [
    ["/v1/voice/transcribe?wake=1", "POST", "audio/webm"],
    ["/v1/voice/transcribe", "POST", "audio/mp4"],
  ]);
  await assert.rejects(m.transcribe(api([json({ error: "bad" }, 502)]), new Blob(["x"]), false), /bad/);
});

test(how + ": the browser support is read from the page", () => {
  const before = globalThis.isSecureContext;
  globalThis.isSecureContext = true;
  const rec = globalThis.MediaRecorder;
  globalThis.MediaRecorder = function () {};
  const nav = globalThis.navigator;
  Object.defineProperty(globalThis, "navigator", {
    value: { mediaDevices: { getUserMedia() {} } },
    configurable: true,
  });
  assert.deepEqual(m.browserSupport(), { secure: true, supported: true });
  Object.defineProperty(globalThis, "navigator", { value: undefined, configurable: true });
  globalThis.isSecureContext = false;
  assert.deepEqual(m.browserSupport(), { secure: false, supported: false });
  Object.defineProperty(globalThis, "navigator", { value: nav, configurable: true, writable: true });
  globalThis.MediaRecorder = rec;
  globalThis.isSecureContext = before;
  assert.equal(m.VOICE_PRESETS.length, 2);
});
}
