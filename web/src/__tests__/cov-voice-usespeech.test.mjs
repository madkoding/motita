// useSpeech loaded as the source itself (through tsx-hooks.mjs): every scenario of speechhook.test.mjs
// (a spoken turn read, Alt+L, stops, failures, subtitles, the natural style, the gateway's and a
// local voice, notices, conversation mode), then what that file leaves out: the sentence
// highlighted and kept in view, scrolling by hand, the voice unlocked by a first tap, a device
// without a voice, and the edges of the natural style.
import test from "node:test";
import assert from "node:assert/strict";
import { setupDom, source } from "./cov-voice-helpers.mjs";

// The stand-in voice gives up after this many utterances, so a loop cannot hang the test.
const MAX_UTTERANCES = 50;
// The id of a later message in a conversation.
const LATER_ID = 3;
const SYNTHESIS_FAILED = "synthesis-failed";
// Longer than the gap the natural style waits between progress lines.
const PAST_THE_GAP_MS = 20000;
const OPENING = "Dale, lo reviso.";
const ALL_DONE = "All done.";
const TESTS_PASS = "Tests pass.";
const LOCAL_VOICE = "es_MX-claude-high";
const CI_PASSED = "CI passed.";
const dom = setupDom("http://localhost/");

// The stand-in voice: it records each utterance and lets the test end it.
const synth = {
  said: [],
  cancels: 0,
  speaking: false,
  voices: [{ voiceURI: "ana", name: "Ana", lang: "en-US", localService: true }],
  speak(u) {
    if (u.text) {
      this.said.push(u);
    }
  },
  cancel() {
    this.cancels++;
  },
  getVoices() {
    return this.voices;
  },
  addEventListener() {},
  removeEventListener() {},
  finish() {
    // Ends every utterance in turn, as the voice would.
    for (let i = 0; i < MAX_UTTERANCES && this.said.length; i++) {
      const u = this.said.at(-1);
      if (!u || u.done) {
        break;
      }
      u.done = true;
      u.onstart?.({});
      u.onend?.({});
    }
  },
};
function Utterance(text) {
  this.text = text;
}
dom.window.speechSynthesis = synth;
dom.window.SpeechSynthesisUtterance = Utterance;
globalThis.speechSynthesis = synth;
globalThis.SpeechSynthesisUtterance = Utterance;

const { useSpeech } = await source("useSpeech.ts");
const {
  SPEECH_KEY,
  SPEECH_TURN_READ_EVENT,
  SUBTITLE_EVENT,
  SPEECH_PREFS_EVENT,
  stopSpeaking,
  sayNotice,
} = await import("../speech.ts");
const { h, render } = await import("preact");
const { act } = await import("preact/test-utils");

const user = (id) => ({ id, role: "user", text: "do it" });
const answer = (id, text = "All done. Tests pass.") => ({
  id,
  role: "agent",
  text,
});

// mount renders the hook with props the test changes, and hands back what it returned.
async function mount(mode, heard = false, extra = {}) {
  const {
    style = "literal",
    engine = "device",
    gatewayVoice = "",
    prefs = {},
    ...props0
  } = extra;
  localStorage.setItem(
    SPEECH_KEY,
    JSON.stringify({
      mode,
      style,
      engine,
      gatewayVoice,
      voiceURI: "",
      rate: 1,
      maxChars: 600,
      ...prefs,
    }),
  );
  synth.said = [];
  synth.cancels = 0;
  const root = document.createElement("div");
  document.body.appendChild(root);
  const state = { toasts: [], current: null, heard };
  let props = {
    messages: [user(1)],
    running: false,
    sessionId: "s1",
    ...props0,
  };
  function Host(p) {
    state.current = useSpeech({
      ...p,
      takeHeard: () => {
        const was = state.heard;
        state.heard = false;
        return was;
      },
      setToast: (t) => state.toasts.push(t),
    });
    return null;
  }
  const set = async (next) => {
    props = { ...props, ...next };
    await act(() => render(h(Host, props), root));
  };
  await set({});
  const done = async () => {
    await act(() => render(null, root));
    root.remove();
  };
  return { state, set, done };
}

// turn runs one turn: it starts, and ends with an answer.
async function turn(s, answerMsg) {
  await s.set({ running: true, messages: [user(1)] });
  await s.set({ running: false, messages: [user(1), answerMsg] });
}

test("when I spoke: the answer of a spoken turn is read, a typed one is not", async () => {
  let s = await mount("voice", true);
  await turn(s, answer(2));
  assert.deepEqual(
    synth.said.map((u) => u.text),
    [ALL_DONE],
    "one sentence at a time",
  );
  assert.equal(s.state.current.speakingId, 2);
  await act(() => synth.finish());
  assert.deepEqual(
    synth.said.map((u) => u.text),
    [ALL_DONE, TESTS_PASS],
  );
  assert.equal(s.state.current.speakingId, null);
  await s.done();

  s = await mount("voice", false);
  await turn(s, answer(2));
  assert.equal(synth.said.length, 0);
  await s.done();
});

test("always reads every answer; off reads none; the answer may arrive after the turn ends", async () => {
  let s = await mount("always");
  await s.set({ running: true });
  await s.set({ running: false });
  assert.equal(synth.said.length, 0, "no answer yet");
  await s.set({ messages: [user(1), answer(2, "Late.")] });
  assert.deepEqual(
    synth.said.map((u) => u.text),
    ["Late."],
  );
  await s.done();

  s = await mount("off", true);
  await turn(s, answer(2));
  assert.equal(synth.said.length, 0);
  await s.done();
});

test("a report reads its summary, and the voice says the language's terms", async () => {
  const s = await mount("always");
  await turn(s, {
    id: 2,
    role: "agent",
    text: "raw",
    report: { summary: "Opened the **PR**." },
  });
  assert.deepEqual(
    synth.said.map((u) => u.text),
    ["Opened the PR."],
  );
  assert.equal(synth.said[0].voice.voiceURI, "ana");
  await s.done();
});

test("the button reads an answer or stops it; Alt+L does the same for the last one", async () => {
  const s = await mount("off");
  await s.set({
    messages: [user(1), answer(2, "One."), user(LATER_ID), answer(4, "Two.")],
  });
  await act(() =>
    s.state.current.speak({ id: 2, role: "agent", text: "One." }),
  );
  assert.deepEqual(
    synth.said.map((u) => u.text),
    ["One."],
  );
  assert.equal(s.state.current.speakingId, 2);
  const cancels = synth.cancels;
  await act(() =>
    s.state.current.speak({ id: 2, role: "agent", text: "One." }),
  );
  assert.equal(s.state.current.speakingId, null, "the second press stops it");
  assert.ok(synth.cancels > cancels);

  const altL = () =>
    document.dispatchEvent(
      new KeyboardEvent("keydown", {
        code: "KeyL",
        altKey: true,
        bubbles: true,
      }),
    );
  await act(altL);
  assert.equal(synth.said.at(-1).text, "Two.", "the last answer");
  assert.equal(s.state.current.speakingId, 4);
  await act(altL);
  assert.equal(s.state.current.speakingId, null);
  // Another key, or Alt with Ctrl, is not the shortcut.
  const before = synth.said.length;
  await act(() =>
    document.dispatchEvent(
      new KeyboardEvent("keydown", {
        code: "KeyL",
        altKey: true,
        ctrlKey: true,
      }),
    ),
  );
  assert.equal(synth.said.length, before);

  await act(() => s.state.current.test());
  assert.match(synth.said.at(-1).text, /I am Motita/);
  await s.done();
});

test("the microphone and a change of conversation silence the voice", async () => {
  const s = await mount("off");
  await act(() => s.state.current.speak(answer(2)));
  assert.equal(s.state.current.speakingId, 2);
  await act(() => stopSpeaking());
  assert.equal(s.state.current.speakingId, null);
  await act(() => s.state.current.speak(answer(2)));
  await s.set({ sessionId: "s2" });
  assert.equal(s.state.current.speakingId, null);
  await s.done();
});

test("a failure of the voice is reported, and says what to do when the browser wants a tap", async () => {
  const s = await mount("off");
  await act(() => s.state.current.speak(answer(2)));
  await act(() => synth.said.at(-1).onerror({ error: "not-allowed" }));
  assert.equal(s.state.toasts.length, 1);
  assert.equal(s.state.toasts[0].type, "error");
  assert.match(s.state.toasts[0].detail, /tap first/);
  await act(() => s.state.current.speak(answer(LATER_ID)));
  await act(() => synth.said.at(-1).onerror({ error: "voice-unavailable" }));
  assert.match(s.state.toasts[1].detail, /no voice/);
  await act(() => s.state.current.speak(answer(4)));
  await act(() => synth.said.at(-1).onerror({ error: SYNTHESIS_FAILED }));
  assert.equal(s.state.toasts[2].detail, SYNTHESIS_FAILED);
  assert.deepEqual(
    s.state.current.voices.map((v) => v.voiceURI),
    ["ana"],
    "the voices of the interface language",
  );
  await s.done();
});

// subtitles records what the subtitles are told: each sentence the voice starts, "" when it ends.
function subtitles() {
  const seen = [];
  const on = (e) => seen.push(e.detail);
  window.addEventListener(SUBTITLE_EVENT, on);
  return { seen, off: () => window.removeEventListener(SUBTITLE_EVENT, on) };
}

test("subtitles: each sentence as the voice starts it, gone when it ends, is stopped or fails", async () => {
  const sub = subtitles();
  const s = await mount("off");
  await act(() => s.state.current.speak(answer(2)));
  assert.deepEqual(sub.seen, [], "nothing before the voice starts");
  await act(() => synth.said.at(-1).onstart({}));
  assert.deepEqual(sub.seen, [ALL_DONE]);
  await act(() => synth.said.at(-1).onend({}));
  await act(() => synth.said.at(-1).onstart({}));
  assert.deepEqual(sub.seen, [ALL_DONE, TESTS_PASS], "no gap between sentences");
  await act(() => synth.said.at(-1).onend({}));
  assert.deepEqual(sub.seen, [ALL_DONE, TESTS_PASS, ""], "gone at the end");

  sub.seen.length = 0;
  await act(() => s.state.current.speak(answer(LATER_ID)));
  await act(() => synth.said.at(-1).onstart({}));
  await act(() => s.state.current.stop());
  assert.deepEqual(sub.seen, [ALL_DONE, ""], "gone when stopped");

  sub.seen.length = 0;
  await act(() => s.state.current.speak(answer(4)));
  await act(() => synth.said.at(-1).onstart({}));
  await act(() => synth.said.at(-1).onerror({ error: SYNTHESIS_FAILED }));
  assert.deepEqual(sub.seen, [ALL_DONE, ""], "gone when the voice fails");
  sub.off();
  await s.done();
});

// fakeNarrator answers the narrate endpoint with one line per stage, and records what it was asked.
function fakeNarrator(lines, fail = false) {
  const asked = [];
  const api = async (path, init) => {
    const body = JSON.parse(init.body);
    asked.push({ path, ...body });
    if (fail) {
      return new Response("{}", { status: 502 });
    }
    return new Response(JSON.stringify({ text: lines[body.stage] ?? "" }), {
      status: 200,
    });
  };
  return { api, asked };
}

// flush lets the requests and their answers settle.
const flush = () => act(() => new Promise((r) => setTimeout(r, 0)));

test("natural: a line when the turn starts, one while it works, and the gist at the end", async (t) => {
  t.mock.timers.enable({ apis: ["setInterval", "Date"] });
  const n = fakeNarrator({
    start: OPENING,
    progress: "Estoy corriendo los tests.",
    answer: "Listo: todo pasa.",
  });
  const sub = subtitles();
  const s = await mount("always", false, { style: "natural", api: n.api });
  await s.set({ running: true, messages: [user(1)] });
  await flush();
  assert.deepEqual(
    n.asked.map((a) => a.stage),
    ["start"],
  );
  assert.equal(n.asked[0].request, "do it");
  assert.equal(n.asked[0].path, "/v1/sessions/s1/narrate");
  assert.deepEqual(
    synth.said.map((u) => u.text),
    [OPENING],
  );
  await act(() => synth.finish());
  assert.deepEqual(
    sub.seen,
    ["", OPENING, ""],
    "a new turn clears the subtitle; the line the model wrote is the next one",
  );
  sub.off();

  // Nothing new: no progress line, however long it waits.
  t.mock.timers.tick(PAST_THE_GAP_MS);
  await flush();
  assert.deepEqual(
    n.asked.map((a) => a.stage),
    ["start"],
  );

  await s.set({
    stepsByTurn: { 1: [{ kind: "command", text: "go test ./..." }] },
  });
  t.mock.timers.tick(PAST_THE_GAP_MS);
  await flush();
  assert.deepEqual(
    n.asked.map((a) => a.stage),
    ["start", "progress"],
  );
  assert.deepEqual(n.asked[1].notes, ["command: go test ./..."]);
  assert.deepEqual(n.asked[1].said, [OPENING], "what was said is not repeated");
  assert.equal(synth.said.at(-1).text, "Estoy corriendo los tests.");
  await act(() => synth.finish());

  await s.set({
    running: false,
    messages: [user(1), answer(2, "## Long\nAll done.")],
  });
  await flush();
  assert.equal(n.asked.at(-1).stage, "answer");
  assert.equal(n.asked.at(-1).answer, "## Long\nAll done.");
  assert.equal(synth.said.at(-1).text, "Listo: todo pasa.");
  assert.equal(
    s.state.current.speakingId,
    2,
    "the answer's button shows it is being told",
  );
  await s.done();
});

test("natural: a line that comes while another is said waits its turn", async () => {
  const n = fakeNarrator({ answer: "Gist." });
  const s = await mount("off", false, { style: "natural", api: n.api });
  await s.set({ messages: [user(1), answer(2)] });
  await act(() => s.state.current.speak(answer(2)));
  await flush();
  assert.equal(synth.said.at(-1).text, "Gist.");
  // A second line arriving during it is queued, then said when it ends.
  await act(() => s.state.current.speak(answer(LATER_ID, "x")));
  await flush();
  assert.equal(synth.said.at(-1).text, "Gist.");
  await s.done();
});

test("natural: when the model cannot be asked, the answer is read word for word", async () => {
  const n = fakeNarrator({}, true);
  const s = await mount("always", false, { style: "natural", api: n.api });
  await s.set({ running: true, messages: [user(1)] });
  await flush();
  assert.equal(synth.said.length, 0, "no start line");
  await s.set({ running: false, messages: [user(1), answer(2)] });
  await flush();
  assert.deepEqual(
    synth.said.map((u) => u.text),
    [ALL_DONE],
  );
  await s.done();
});

test("natural: the button tells the gist; pressing it again stops, and a late line is dropped", async () => {
  const n = fakeNarrator({ answer: "Todo bien." });
  const s = await mount("off", false, { style: "natural", api: n.api });
  await s.set({ messages: [user(1), answer(2)] });
  await act(() => s.state.current.speak(answer(2)));
  assert.equal(
    s.state.current.speakingId,
    2,
    "busy while the line is asked for",
  );
  await act(() => s.state.current.speak(answer(2)));
  assert.equal(s.state.current.speakingId, null);
  await flush();
  assert.equal(
    synth.said.length,
    0,
    "the line that came after the stop is not said",
  );
  assert.equal(
    n.asked[0].request,
    "do it",
    "the question the answer belongs to",
  );

  await act(() => s.state.current.speak(answer(2)));
  await flush();
  assert.deepEqual(
    synth.said.map((u) => u.text),
    ["Todo bien."],
  );
  await s.done();
});

// The gateway's voice: <audio> and blob URLs stand in, and the test ends each clip.
const played = [];
globalThis.Audio = function Audio() {
  const a = {
    src: "",
    playbackRate: 1,
    onplaying: null,
    onended: null,
    onerror: null,
    play() {
      played.push(a);
      queueMicrotask(() => a.onplaying?.());
      return Promise.resolve();
    },
    pause() {},
  };
  return a;
};
globalThis.URL.createObjectURL = () => "blob:x";
globalThis.URL.revokeObjectURL = () => {};
const TTS = { enabled: true, voices: ["alloy", "nova"], max_chars: 4000 };

function gatewayApi() {
  const asked = [];
  const api = async (path, init) => {
    asked.push({ path, ...JSON.parse(init.body) });
    return new Response("MP3", { status: 200 });
  };
  return { api, asked };
}

test("gateway: chosen and on, the gateway says each sentence with the voice chosen", async () => {
  played.length = 0;
  const g = gatewayApi();
  const sub = subtitles();
  const s = await mount("off", false, {
    engine: "gateway",
    gatewayVoice: "nova",
    api: g.api,
    tts: TTS,
  });
  assert.equal(s.state.current.supported, true);
  assert.deepEqual(s.state.current.gatewayVoices, ["alloy", "nova"]);
  await s.set({ messages: [user(1), answer(2)] });
  await act(() => s.state.current.speak(answer(2)));
  await flush();
  assert.deepEqual(
    g.asked.map((a) => [a.path, a.text, a.voice]),
    [
      ["/v1/voice/speak", ALL_DONE, "nova"],
      ["/v1/voice/speak", TESTS_PASS, "nova"],
    ],
  );
  assert.equal(synth.said.length, 0, "the device says nothing");
  assert.deepEqual(sub.seen, [ALL_DONE], "the subtitle waits for the clip to play");
  sub.off();
  assert.equal(s.state.current.speakingId, 2);
  await act(async () => {
    played[0].onended();
    await new Promise((r) => setTimeout(r, 0));
  });
  await act(async () => {
    played[1].onended();
    await new Promise((r) => setTimeout(r, 0));
  });
  assert.equal(
    s.state.current.speakingId,
    null,
    "done when the last clip ends",
  );
  await s.done();
});

test("gateway: chosen but off on this gateway, the device speaks", async () => {
  const g = gatewayApi();
  const s = await mount("off", false, { engine: "gateway", api: g.api });
  assert.deepEqual(s.state.current.gatewayVoices, []);
  await s.set({ messages: [user(1), answer(2)] });
  await act(() => s.state.current.speak(answer(2)));
  await flush();
  assert.equal(g.asked.length, 0);
  assert.deepEqual(
    synth.said.map((u) => u.text),
    [ALL_DONE],
  );
  await s.done();
});

test("local voice: chosen, on and downloaded, the worker says each sentence; without one the device speaks", async () => {
  played.length = 0;
  const asked = [];
  const warmed = [];
  globalThis.Worker = class {
    postMessage(m) {
      if (m.kind === "warm") {
        warmed.push(m.id);
        return;
      }
      asked.push([m.id, m.text]);
      queueMicrotask(() => this.onmessage({ data: { seq: m.seq, wav: new ArrayBuffer(4) } }));
    }
  };
  globalThis.caches = {};
  const g = gatewayApi();
  let s = await mount("off", false, {
    engine: "local",
    prefs: { localVoice: LOCAL_VOICE },
    api: g.api,
    local: true,
  });
  assert.equal(s.state.current.local, true);
  assert.deepEqual(warmed, [LOCAL_VOICE], "the voice is loaded with the page");
  await s.set({ messages: [user(1), answer(2)] });
  await act(() => s.state.current.speak(answer(2)));
  await flush();
  assert.deepEqual(asked, [
    [LOCAL_VOICE, ALL_DONE],
    [LOCAL_VOICE, TESTS_PASS],
  ]);
  assert.equal(g.asked.length, 0, "nothing goes to the gateway's voice");
  assert.equal(synth.said.length, 0, "the device says nothing");
  await s.done();
  // No voice downloaded yet: the device reads.
  s = await mount("off", false, { engine: "local", api: g.api, local: true });
  await s.set({ messages: [user(1), answer(2)] });
  await act(() => s.state.current.speak(answer(2)));
  await flush();
  assert.equal(asked.length, 2);
  assert.deepEqual(synth.said.map((u) => u.text), [ALL_DONE]);
  await s.done();
  delete globalThis.Worker;
  delete globalThis.caches;
});

test("notices: said when turned on and the tab is visible, after what is being said", async () => {
  // jsdom is a hidden page unless told otherwise.
  const hidden = (value) =>
    Object.defineProperty(document, "hidden", { value, configurable: true });
  hidden(false);
  let s = await mount("off", false, { prefs: { notices: true } });
  await act(() => sayNotice("CI passed. Pull request #3 is green."));
  assert.deepEqual(
    synth.said.map((u) => u.text),
    [CI_PASSED],
    "one sentence at a time",
  );
  // One that comes while another is said waits its turn.
  await act(() => sayNotice("CI failed."));
  await act(() => synth.finish());
  assert.ok(synth.said.some((u) => u.text === "CI failed."));
  // A new question of the agent is a notice too.
  synth.said = [];
  await s.set({ approval: { id: "a1", question: "Run rm -rf build?" } });
  await act(() => synth.finish());
  assert.match(
    synth.said.map((u) => u.text).join(" "),
    /Motita needs your answer\. Run rm -rf build\?/,
  );
  await s.done();

  s = await mount("off", false);
  await act(() => sayNotice(CI_PASSED));
  assert.equal(synth.said.length, 0, "off by default");
  await s.done();

  s = await mount("off", false, { prefs: { notices: true } });
  hidden(true);
  await act(() => sayNotice(CI_PASSED));
  delete document.hidden;
  assert.equal(synth.said.length, 0, "not for a tab nobody looks at");
  await s.done();
});

test("conversation: the page is told when the answer to a spoken turn was read to its end", async () => {
  let told = 0;
  const on = () => (told += 1);
  window.addEventListener(SPEECH_TURN_READ_EVENT, on);
  let s = await mount("voice", true);
  await turn(s, answer(2));
  await act(() => synth.finish());
  assert.equal(told, 1);
  await s.done();

  // A stopped reading is not an end: nothing listens again.
  s = await mount("voice", true);
  await turn(s, answer(2));
  await act(() => s.state.current.stop());
  await act(() => synth.finish());
  assert.equal(told, 1);
  await s.done();

  // A typed turn read in "always" mode is not a spoken one.
  s = await mount("always", false);
  await turn(s, answer(2));
  await act(() => synth.finish());
  assert.equal(told, 1);
  await s.done();
  window.removeEventListener(SPEECH_TURN_READ_EVENT, on);
});

// ---- What speechhook.test.mjs leaves out. ----

const SIX = "One. Two. Three. Four. Five. Six.";
// startNext ends the sentence being said and starts the next one.
const startNext = async () => {
  await act(() => synth.said.at(-1).onend({}));
  await act(() => synth.said.at(-1).onstart({}));
};

test("the sentence read is highlighted and brought into view, until the person scrolls by hand", async () => {
  const marks = new Map();
  globalThis.CSS = {
    highlights: { set: (n, v) => marks.set(n, v), delete: (n) => marks.delete(n) },
  };
  globalThis.Highlight = class {
    constructor(r) {
      this.range = r;
    }
  };
  const holder = document.createElement("div");
  holder.innerHTML = `<div role="log" id="log"><div data-msg-id="2"><div class="markdown-body"><p>${SIX}</p></div><input id="field"></div></div>`;
  document.body.appendChild(holder);
  const log = document.getElementById("log");
  const scrolls = [];
  log.scrollBy = (o) => scrolls.push(o);
  log.getBoundingClientRect = () => ({ top: 0, bottom: 100, height: 100 });
  let box = { top: 150, bottom: 170, height: 20 };
  const rangeBox = dom.window.Range.prototype.getBoundingClientRect;
  dom.window.Range.prototype.getBoundingClientRect = () => box;
  window.matchMedia = () => ({ matches: true });
  const s = await mount("off");
  await s.set({ messages: [user(1), answer(2, SIX)] });
  await act(() => s.state.current.speak(answer(2, SIX)));
  await act(() => synth.said.at(-1).onstart({}));
  assert.equal(marks.get("motita-reading").range.toString(), "One.");
  assert.deepEqual(scrolls, [{ top: 86, behavior: "auto" }], "below the view: down, at once for less motion");

  box = { top: -50, bottom: -30, height: 20 };
  window.matchMedia = () => {
    throw new Error("no matchMedia");
  };
  await startNext();
  assert.equal(marks.get("motita-reading").range.toString(), "Two.");
  assert.deepEqual(scrolls[1], { top: -66, behavior: "smooth" }, "above the view: up");

  box = { top: 10, bottom: 30, height: 20 };
  await startNext();
  assert.equal(scrolls.length, 2, "in view: no scroll");

  // A key typed in a field is not a scroll; a scroll key on the page is.
  box = { top: 150, bottom: 170, height: 20 };
  await act(() =>
    document.getElementById("field").dispatchEvent(new KeyboardEvent("keydown", { key: "PageDown", bubbles: true })),
  );
  await act(() => window.dispatchEvent(new KeyboardEvent("keydown", { key: "a" })));
  await startNext();
  assert.equal(scrolls.length, 3, "still following");
  await act(() => window.dispatchEvent(new KeyboardEvent("keydown", { key: "PageDown" })));
  await startNext();
  assert.equal(marks.get("motita-reading").range.toString(), "Five.");
  assert.equal(scrolls.length, 3, "the person scrolled: the view stays where they put it");
  await act(() => s.state.current.stop());
  assert.equal(marks.has("motita-reading"), false, "the highlight goes with the voice");

  // A press on the conversation's own bar stops the following; one on a message does not.
  const pointer = (target) =>
    act(() => target.dispatchEvent(new dom.window.Event("pointerdown", { bubbles: true })));
  await act(() => s.state.current.speak(answer(2, SIX)));
  await act(() => synth.said.at(-1).onstart({}));
  const before = scrolls.length;
  await pointer(holder.querySelector("p"));
  await startNext();
  assert.equal(scrolls.length, before + 1, "a press on a message is not a scroll");
  await pointer(log);
  await startNext();
  assert.equal(scrolls.length, before + 1, "a press on the conversation's bar is");
  await act(() => s.state.current.stop());

  // A finger or the wheel stop it too, and an editable element is a field.
  for (const type of ["touchmove", "wheel"]) {
    await act(() => s.state.current.speak(answer(2, SIX)));
    await act(() => synth.said.at(-1).onstart({}));
    const n = scrolls.length;
    await act(() => window.dispatchEvent(new dom.window.Event(type)));
    await startNext();
    assert.equal(scrolls.length, n, type + " stops the following");
    await act(() => s.state.current.stop());
  }
  const editor = document.createElement("div");
  Object.defineProperty(editor, "isContentEditable", { value: true });
  document.body.appendChild(editor);
  await act(() => s.state.current.speak(answer(2, SIX)));
  await act(() => synth.said.at(-1).onstart({}));
  const n = scrolls.length;
  await act(() => editor.dispatchEvent(new KeyboardEvent("keydown", { key: "End", bubbles: true })));
  await startNext();
  assert.equal(scrolls.length, n + 1, "End typed in an editor is not a scroll");
  await act(() => s.state.current.stop());

  // A box with no height (not laid out) is not scrolled to; a message outside the conversation
  // is highlighted only; a sentence that is not on screen (a pronunciation) has no highlight.
  box = { top: 150, bottom: 170, height: 0 };
  await act(() => s.state.current.speak(answer(2, SIX)));
  await act(() => synth.said.at(-1).onstart({}));
  assert.equal(scrolls.length, n + 1);
  await act(() => s.state.current.stop());
  const loose = document.createElement("div");
  loose.dataset.msgId = "9";
  loose.textContent = "Loose words.";
  document.body.appendChild(loose);
  box = { top: 150, bottom: 170, height: 20 };
  await act(() => s.state.current.speak(answer(9, "Loose words.")));
  await act(() => synth.said.at(-1).onstart({}));
  assert.equal(marks.get("motita-reading").range.toString(), "Loose words.");
  assert.equal(scrolls.length, n + 1);
  await act(() => s.state.current.stop());
  await act(() => s.state.current.speak(answer(9, "Not shown.")));
  await act(() => synth.said.at(-1).onstart({}));
  assert.equal(marks.has("motita-reading"), false);
  await act(() => s.state.current.stop());

  // Without the Highlight API nothing is drawn, and nothing breaks.
  delete globalThis.Highlight;
  await act(() => s.state.current.speak(answer(2, SIX)));
  await act(() => synth.said.at(-1).onstart({}));
  assert.equal(marks.has("motita-reading"), false);
  await act(() => s.state.current.stop());
  await s.done();
  delete globalThis.CSS;
  holder.remove();
  loose.remove();
  editor.remove();
  dom.window.Range.prototype.getBoundingClientRect = rangeBox;
  delete window.matchMedia;
});

test("the first tap or key unlocks the voice once; the settings change and are kept", async () => {
  const speak = synth.speak;
  let unlocks = 0;
  synth.speak = function (u) {
    if (!u.text) {
      unlocks++;
    }
    return speak.call(this, u);
  };
  let changed;
  synth.addEventListener = (type, f) => {
    if (type === "voiceschanged") {
      changed = f;
    }
  };
  const s = await mount("always");
  await act(() => window.dispatchEvent(new dom.window.Event("pointerdown")));
  await act(() => window.dispatchEvent(new KeyboardEvent("keydown", { key: "x" })));
  assert.equal(unlocks, 1, "one empty utterance, then the listeners go");
  synth.speak = speak;
  delete synth.addEventListener;

  // Chrome's voices arrive late.
  const voices = synth.voices;
  synth.voices = [...voices, { voiceURI: "bob", name: "Bob", lang: "en-GB" }];
  await act(() => changed());
  assert.deepEqual(s.state.current.allVoices.map((v) => v.voiceURI), ["ana", "bob"]);
  synth.voices = voices;

  const p = { ...s.state.current.prefs, rate: 1.5 };
  await act(() => s.state.current.setPrefs(p));
  assert.equal(s.state.current.prefs.rate, 1.5);
  assert.equal(JSON.parse(localStorage.getItem(SPEECH_KEY)).rate, 1.5);
  localStorage.setItem(SPEECH_KEY, JSON.stringify({ ...p, rate: 2 }));
  await act(() => window.dispatchEvent(new CustomEvent(SPEECH_PREFS_EVENT)));
  assert.equal(s.state.current.prefs.rate, 2, "a change made in the settings is read again");
  await s.done();

  // Off, the voice is not unlocked at all.
  const off = await mount("off");
  synth.speak = function (u) {
    if (!u.text) {
      unlocks++;
    }
    return speak.call(this, u);
  };
  await act(() => window.dispatchEvent(new dom.window.Event("pointerdown")));
  assert.equal(unlocks, 1);
  synth.speak = speak;
  await off.done();
});

test("a device without a voice: nothing is read, and a question with no words is still announced", async () => {
  const saved = [globalThis.speechSynthesis, globalThis.SpeechSynthesisUtterance];
  delete globalThis.speechSynthesis;
  delete globalThis.SpeechSynthesisUtterance;
  Object.defineProperty(document, "hidden", { value: false, configurable: true });
  const s = await mount("always", false, { prefs: { notices: true } });
  assert.equal(s.state.current.supported, false);
  assert.equal(s.state.current.device, false);
  await turn(s, answer(2));
  await act(() => s.state.current.speak(answer(2)));
  await act(() => s.state.current.test());
  await act(() => sayNotice("CI passed."));
  await act(() =>
    document.dispatchEvent(new KeyboardEvent("keydown", { code: "KeyL", altKey: true, bubbles: true })),
  );
  assert.equal(s.state.current.speakingId, null, "nothing is read");
  assert.equal(s.state.toasts.length, 0);
  // An approval without its question says only that Motita waits.
  const heard = [];
  const onNotice = (e) => heard.push(e.detail);
  window.addEventListener("motita-speech-notice", onNotice);
  await s.set({ approval: { id: "q1" } });
  assert.deepEqual(heard, ["Motita needs your answer."]);
  window.removeEventListener("motita-speech-notice", onNotice);
  await s.done();
  delete document.hidden;
  [globalThis.speechSynthesis, globalThis.SpeechSynthesisUtterance] = saved;
});

test("a device without a voice reads with the gateway's; one without audio has none", async () => {
  const saved = [globalThis.speechSynthesis, globalThis.SpeechSynthesisUtterance];
  delete globalThis.speechSynthesis;
  delete globalThis.SpeechSynthesisUtterance;
  played.length = 0;
  const g = gatewayApi();
  let s = await mount("off", false, { engine: "gateway", api: g.api, tts: TTS });
  assert.equal(s.state.current.supported, true);
  await act(() => s.state.current.speak(answer(2)));
  await flush();
  assert.equal(g.asked.length, 2);
  assert.equal(s.state.current.speakingId, 2);
  // Alt+L stops what plays.
  await act(() =>
    document.dispatchEvent(new KeyboardEvent("keydown", { code: "KeyL", altKey: true, bubbles: true })),
  );
  assert.equal(s.state.current.speakingId, null);
  await s.done();

  // The gateway's voice without an api to reach it, or without <audio>, is no voice.
  s = await mount("off", false, { engine: "gateway", tts: TTS });
  assert.equal(s.state.current.supported, false);
  await s.done();
  const audio = globalThis.Audio;
  delete globalThis.Audio;
  s = await mount("off", false, { engine: "gateway", api: g.api, tts: TTS, local: true });
  assert.equal(s.state.current.supported, false);
  assert.deepEqual(s.state.current.gatewayVoices, []);
  await s.done();
  globalThis.Audio = audio;
  [globalThis.speechSynthesis, globalThis.SpeechSynthesisUtterance] = saved;
});

test("errors are said in words a person can act on", async () => {
  const s = await mount("off");
  await act(() => s.state.current.speak(answer(2)));
  await act(() => synth.said.at(-1).onerror({ error: "language-unavailable" }));
  assert.match(s.state.toasts.at(-1).detail, /no voice for this language/);
  await act(() => s.state.current.speak(answer(2)));
  await act(() => synth.said.at(-1).onerror({ error: "this browser has no WebGPU in its workers" }));
  assert.match(s.state.toasts.at(-1).detail, /needs WebGPU/);
  await s.done();
});

test("natural without an api reads word for word, and a turn that starts on an empty conversation", async () => {
  let s = await mount("always", false, { style: "natural" });
  await turn(s, answer(2));
  await flush();
  assert.deepEqual(synth.said.map((u) => u.text), [ALL_DONE], "no opening line, the answer as it is");
  await s.done();
  s = await mount("always");
  await s.set({ running: true, messages: [] });
  await s.set({ running: false, messages: [answer(2)] });
  assert.deepEqual(synth.said.map((u) => u.text), [ALL_DONE], "the first message is the answer");
  await s.done();
});

test("natural: progress waits while the agent has done nothing new, the voice talks, or a line is asked", async (t) => {
  t.mock.timers.enable({ apis: ["setInterval", "Date"] });
  let release;
  const asked = [];
  const api = async (path, init) => {
    const body = JSON.parse(init.body);
    asked.push(body.stage);
    if (body.stage === "progress") {
      await new Promise((r) => (release = r));
      return new Response(JSON.stringify({ text: "Sigo." }), { status: 200 });
    }
    return new Response(JSON.stringify({ text: "" }), { status: 200 });
  };
  const s = await mount("always", false, { style: "natural", api });
  await s.set({ running: true, messages: [user(1)], stepsByTurn: { 1: undefined } });
  await flush();
  t.mock.timers.tick(PAST_THE_GAP_MS);
  await flush();
  assert.deepEqual(asked, ["start"], "a turn with no steps has nothing to tell");
  await s.set({ stepsByTurn: { 1: [{ kind: "", text: "edit" }] } });
  t.mock.timers.tick(PAST_THE_GAP_MS);
  await flush();
  assert.deepEqual(asked, ["start", "progress"]);
  // While the line is asked for, no other one is.
  t.mock.timers.tick(PAST_THE_GAP_MS);
  await flush();
  assert.deepEqual(asked, ["start", "progress"]);
  // The turn ends before the line comes back: it is not said.
  await s.set({ running: false, messages: [user(1), answer(2, "Done.")] });
  await flush();
  release();
  await flush();
  assert.ok(!synth.said.some((u) => u.text === "Sigo."));
  await s.done();

  // While the voice talks, progress waits.
  const n = fakeNarrator({ start: OPENING, progress: "Más." });
  const v = await mount("always", false, { style: "natural", api: n.api });
  await v.set({ running: true, messages: [user(1)], stepsByTurn: { 1: [{ kind: "", text: "a" }] } });
  await flush();
  assert.equal(synth.said.at(-1).text, OPENING);
  t.mock.timers.tick(PAST_THE_GAP_MS);
  await flush();
  assert.deepEqual(n.asked.map((a) => a.stage), ["start"], "the opening is still being said");
  await v.done();

  // A typed turn in "when I spoke" mode says no progress at all.
  const quiet = fakeNarrator({ progress: "Nope." });
  const w = await mount("voice", false, { style: "natural", api: quiet.api });
  await w.set({ running: true, messages: [user(1)], stepsByTurn: { 1: [{ kind: "", text: "a" }] } });
  t.mock.timers.tick(PAST_THE_GAP_MS);
  await flush();
  assert.deepEqual(quiet.asked, []);
  await w.done();
});
