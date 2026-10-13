// The parts of speech input that are decided without a microphone: the switches, why the feature
// is or is not usable, the recording type, the level detector, and the two requests.
import test from "node:test";
import assert from "node:assert/strict";
import { JSDOM } from "jsdom";

const dom = new JSDOM("<!doctype html><html></html>", {
  url: "http://localhost/",
});
globalThis.localStorage = dom.window.localStorage;
const m = await import("../voice.ts");

const HTTP_OK = 200;
const HTTP_NOT_FOUND = 404;
const HTTP_SERVER_ERROR = 500;
const HTTP_BAD_GATEWAY = 502;

test("the switches: the microphone on and hands-free off by default, and both are kept", () => {
  localStorage.clear();
  assert.deepEqual(m.loadVoicePrefs(), { mic: true, listen: false });
  let heard = 0;
  globalThis.window = dom.window;
  globalThis.CustomEvent = dom.window.CustomEvent;
  dom.window.addEventListener(m.VOICE_PREFS_EVENT, () => {
    heard += 1;
  });
  m.saveVoicePrefs({ mic: false, listen: true });
  assert.deepEqual(m.loadVoicePrefs(), { mic: false, listen: true });
  assert.equal(heard, 1, "the page is told the switches changed");
  localStorage.setItem(m.VOICE_KEY, "{not json");
  assert.deepEqual(m.loadVoicePrefs(), m.DEFAULT_VOICE_PREFS);
});

test("why speech input can or cannot be used, in the order a person can fix it", () => {
  const on = { enabled: true, wake_phrases: ["motita"], max_audio_bytes: 1 };
  assert.equal(m.voiceState(false, true, on), "insecure");
  assert.equal(m.voiceState(true, false, on), "unsupported");
  assert.equal(m.voiceState(true, true, null), "off");
  assert.equal(m.voiceState(true, true, { ...on, enabled: false }), "off");
  assert.equal(m.voiceState(true, true, on), "ready");
  assert.equal(m.listenState("ready", on), "ready");
  assert.equal(
    m.listenState("ready", { ...on, wake_phrases: [] }),
    "no-phrases",
  );
  assert.equal(m.listenState("insecure", on), "insecure");
});

test("the browser support is read from the page", () => {
  const before = {
    isSecureContext: globalThis.isSecureContext,
    MediaRecorder: globalThis.MediaRecorder,
  };
  globalThis.isSecureContext = true;
  globalThis.MediaRecorder = function () {};
  Object.defineProperty(globalThis, "navigator", {
    value: { mediaDevices: { getUserMedia: () => {} } },
    configurable: true,
  });
  assert.deepEqual(m.browserSupport(), { secure: true, supported: true });
  Object.defineProperty(globalThis, "navigator", {
    value: {},
    configurable: true,
  });
  globalThis.isSecureContext = false;
  assert.deepEqual(m.browserSupport(), { secure: false, supported: false });
  Object.assign(globalThis, before);
});

test("the recording type is the best one the browser records", () => {
  assert.equal(
    m.pickMimeType(() => true),
    "audio/webm;codecs=opus",
  );
  assert.equal(
    m.pickMimeType((t) => t === "audio/mp4"),
    "audio/mp4",
  );
  assert.equal(
    m.pickMimeType(() => false),
    "",
  );
});

test("the level of a frame", () => {
  assert.equal(m.level(new Uint8Array()), 0);
  assert.equal(m.level(new Uint8Array([128, 128])), 0);
  assert.equal(m.level(new Uint8Array([0, 0])), 1);
});

test("the gate cuts speech into phrases and drops what is too short", () => {
  const SILENCE_MS = 100;
  const MIN_MS = 50;
  const MAX_MS = 1000;
  const g = new m.SpeechGate({
    threshold: 0.1,
    silenceMs: SILENCE_MS,
    minMs: MIN_MS,
    maxMs: MAX_MS,
  });
  const loud = 0.5;
  // The times, in ms, the gate is fed at: a phrase with a short pause inside it, a click too
  // short to be words, and a phrase that runs past maxMs.
  const PHRASE_START_MS = 10;
  const LAST_LOUD_MS = SILENCE_MS;
  const PAUSE_MS = LAST_LOUD_MS + MIN_MS;
  const PHRASE_END_MS = LAST_LOUD_MS + SILENCE_MS + 1;
  const CLICK_START_MS = 300;
  const CLICK_END_MS = 420;
  const LONG_START_MS = 500;
  assert.equal(g.feed(0, 0), null, "silence is nothing");
  assert.equal(g.feed(loud, PHRASE_START_MS), "start");
  assert.equal(g.speaking, true);
  assert.equal(g.feed(loud, LAST_LOUD_MS), null);
  assert.equal(
    g.feed(0, PAUSE_MS),
    null,
    "a short pause is part of the phrase",
  );
  assert.equal(g.feed(0, PHRASE_END_MS), "end");
  assert.equal(g.speaking, false);

  assert.equal(g.feed(loud, CLICK_START_MS), "start");
  assert.equal(g.feed(0, CLICK_END_MS), "discard", "a click is not words");

  assert.equal(g.feed(loud, LONG_START_MS), "start");
  assert.equal(
    g.feed(loud, LONG_START_MS + MAX_MS),
    "end",
    "a phrase that runs on is cut",
  );
  assert.equal(new m.SpeechGate().feed(0, 0), null, "the default thresholds");
});

test("dictated text joins what is already written", () => {
  assert.equal(m.appendText("", " hello "), "hello");
  assert.equal(m.appendText("fix it  ", "now"), "fix it now");
  assert.equal(m.appendText("keep", "  "), "keep");
});

test("the requests: the info, the recording as its own type, and the gateway's refusal", async () => {
  const calls = [];
  const reply = (status, body) => async (path, init) => {
    calls.push({ path, init });
    let text = body;
    if (typeof body !== "string") {
      text = JSON.stringify(body);
    }
    return new Response(text, { status });
  };
  const info = { enabled: true, wake_phrases: [], max_audio_bytes: 5 };
  assert.deepEqual(await m.fetchVoiceInfo(reply(HTTP_OK, info)), info);
  assert.equal(await m.fetchVoiceInfo(reply(HTTP_NOT_FOUND, {})), null);

  const audio = new Blob(["x"], { type: "audio/mp4" });
  const got = await m.transcribe(
    reply(HTTP_OK, { text: "hi", wake: { phrase: "motita", rest: "hi" } }),
    audio,
    true,
  );
  assert.equal(got.wake.rest, "hi");
  const last = calls.at(-1);
  assert.equal(last.path, "/v1/voice/transcribe?wake=1");
  assert.equal(last.init.headers["Content-Type"], "audio/mp4");
  await m.transcribe(reply(HTTP_OK, { text: "" }), new Blob(["x"]), false);
  assert.equal(calls.at(-1).path, "/v1/voice/transcribe");
  assert.equal(calls.at(-1).init.headers["Content-Type"], "audio/webm");

  await assert.rejects(
    m.transcribe(reply(HTTP_BAD_GATEWAY, { error: "bad key" }), audio, false),
    /bad key/,
  );
  await assert.rejects(
    m.transcribe(reply(HTTP_SERVER_ERROR, {}), audio, false),
    /HTTP 500/,
  );
  await assert.rejects(
    m.transcribe(reply(HTTP_SERVER_ERROR, "<html>"), audio, false),
    /HTTP 500/,
  );
});
