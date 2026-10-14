// Runs useSpeech in jsdom with a stand-in voice (bundled with esbuild, since node cannot import
// TSX): an answer is read on its own when the turn was spoken or the mode says always, Alt+L reads
// the last answer or stops, the microphone and a change of conversation silence it, and a failure
// is reported.
import test from "node:test";
import assert from "node:assert/strict";
import { fileURLToPath, pathToFileURL } from "node:url";
import { dirname, join } from "node:path";
import { mkdtempSync, rmSync } from "node:fs";
import { JSDOM } from "jsdom";
import { build } from "esbuild";

const here = dirname(fileURLToPath(import.meta.url));
// The stand-in voice gives up after this many utterances, so a loop cannot hang the test.
const MAX_UTTERANCES = 50;
// The id of a later message in a conversation.
const LATER_ID = 3;
// Longer than the gap the natural style waits between progress lines.
const PAST_THE_GAP_MS = 20000;
const OPENING = "Dale, lo reviso.";
const dom = new JSDOM("<!doctype html><html><body></body></html>", {
  url: "http://localhost/",
});
for (const k of [
  "document",
  "window",
  "KeyboardEvent",
  "Event",
  "CustomEvent",
  "HTMLElement",
  "Node",
  "localStorage",
]) {
  globalThis[k] = dom.window[k];
}

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

const out = mkdtempSync(join(here, ".sp-"));
await build({
  entryPoints: [join(here, "..", "useSpeech.ts")],
  outfile: join(out, "sp.mjs"),
  bundle: true,
  format: "esm",
  platform: "node",
  external: ["preact", "preact/*"],
  logLevel: "silent",
});
const { useSpeech } = await import(pathToFileURL(join(out, "sp.mjs")).href);
const { SPEECH_KEY, stopSpeaking } = await import("../speech.ts");
const { h, render } = await import("preact");
const { act } = await import("preact/test-utils");
rmSync(out, { recursive: true });

const user = (id) => ({ id, role: "user", text: "do it" });
const answer = (id, text = "All done. Tests pass.") => ({
  id,
  role: "agent",
  text,
});

// mount renders the hook with props the test changes, and hands back what it returned.
async function mount(mode, heard = false, extra = {}) {
  const { style = "literal", ...props0 } = extra;
  localStorage.setItem(
    SPEECH_KEY,
    JSON.stringify({ mode, style, voiceURI: "", rate: 1, maxChars: 600 }),
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
    ["All done."],
    "one sentence at a time",
  );
  assert.equal(s.state.current.speakingId, 2);
  await act(() => synth.finish());
  assert.deepEqual(
    synth.said.map((u) => u.text),
    ["All done.", "Tests pass."],
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
  await act(() => synth.said.at(-1).onerror({ error: "synthesis-failed" }));
  assert.equal(s.state.toasts[2].detail, "synthesis-failed");
  assert.deepEqual(
    s.state.current.voices.map((v) => v.voiceURI),
    ["ana"],
    "the voices of the interface language",
  );
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
    ["All done."],
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
