// The parts of reading aloud that are decided without a voice: what of an answer is read, how it
// is cut and pronounced, which voice says it, the reader's order of sentences, and finding the
// sentence on screen.
import test from "node:test";
import assert from "node:assert/strict";
import { JSDOM } from "jsdom";

const dom = new JSDOM("<!doctype html><html><body></body></html>", {
  url: "http://localhost/",
});
globalThis.localStorage = dom.window.localStorage;
const m = await import("../speech.ts");

// The sizes the tests cut at, and how often a long text repeats.
const SHORT_LIMIT = 20;
const WORD_LIMIT = 12;
const WORDS_PER_CLAUSE = 30;
const CLAUSES = 3;
const MAX_PIECE = 220; // speech.ts: the longest piece a voice is given
const ONE_WORD = 500;
const QUICK = 1.25;
const NEW_NOTES = 3;
const SAID_NOTES = 2;

test("the settings: read when I spoke, the device's voice, normal speed, and they are kept", () => {
  localStorage.clear();
  assert.deepEqual(m.loadSpeechPrefs(), m.DEFAULT_SPEECH_PREFS);
  assert.equal(m.DEFAULT_SPEECH_PREFS.mode, "voice");
  assert.equal(
    m.DEFAULT_SPEECH_PREFS.style,
    "literal",
    "word for word unless asked",
  );
  let heard = 0;
  globalThis.window = dom.window;
  globalThis.CustomEvent = dom.window.CustomEvent;
  dom.window.addEventListener(m.SPEECH_PREFS_EVENT, () => {
    heard += 1;
  });
  const p = {
    mode: "always",
    style: "natural",
    voiceURI: "v1",
    rate: 1.5,
    maxChars: 0,
  };
  m.saveSpeechPrefs(p);
  assert.deepEqual(m.loadSpeechPrefs(), p);
  assert.equal(heard, 1, "the page is told the settings changed");
  // A value this version does not know falls back to the default, one field at a time.
  localStorage.setItem(
    m.SPEECH_KEY,
    JSON.stringify({
      mode: "loud",
      style: "poem",
      voiceURI: 3,
      rate: 9,
      maxChars: 600,
    }),
  );
  assert.deepEqual(m.loadSpeechPrefs(), {
    ...m.DEFAULT_SPEECH_PREFS,
    maxChars: 600,
  });
  localStorage.setItem(m.SPEECH_KEY, "{not json");
  assert.deepEqual(m.loadSpeechPrefs(), m.DEFAULT_SPEECH_PREFS);
});

test("the browser's support is read from the page", () => {
  assert.equal(m.speechSupported(), false);
  globalThis.speechSynthesis = {};
  globalThis.SpeechSynthesisUtterance = function () {};
  assert.equal(m.speechSupported(), true);
  delete globalThis.speechSynthesis;
  delete globalThis.SpeechSynthesisUtterance;
});

test("an answer is read as words: code, tables and formulas are named, links read their text", () => {
  const md = [
    "# Done",
    "",
    "I changed **two** files in `web/src/App.tsx` and _one_ test.",
    "See [the guide](https://example.com/guide) or https://example.com/x.",
    "",
    "```go",
    "func main() {}",
    "```",
    "",
    "| a | b |",
    "|---|---|",
    "| 1 | 2 |",
    "",
    "- [x] first item",
    "- second item!",
    "> quoted line",
    "---",
    "$$",
    "x^2",
    "$$",
    "![a cat](cat.png) <b>bold</b> ~~old~~ note[^1]",
  ].join("\n");
  assert.equal(
    m.toSpeakable(md),
    [
      "Done.",
      "I changed two files in App.tsx and one test. See the guide or link.",
      "Code block.",
      "Table.",
      "first item. second item! quoted line",
      "Formula.",
      "a cat bold old note",
    ].join("\n\n"),
  );
  assert.equal(m.toSpeakable("`npm run build` now"), "npm run build now");
  assert.equal(m.toSpeakable("~~~\nx\n~~~\nafter"), "Code block.\n\nafter");
});

test("a long answer is read up to its first paragraph, and says the rest is on screen", () => {
  const rest = "The rest is on screen.";
  assert.equal(m.limitLength("short", 0), "short");
  assert.equal(m.limitLength("short", 100), "short");
  assert.equal(
    m.limitLength("First part.\n\nSecond part that is long.", SHORT_LIMIT),
    "First part.\n\n" + rest,
  );
  assert.equal(
    m.limitLength("One sentence. Another sentence here.", SHORT_LIMIT),
    "One sentence.\n\n" + rest,
  );
  assert.equal(
    m.limitLength("words without a stop", WORD_LIMIT),
    "words…\n\n" + rest,
  );
  assert.equal(m.limitLength("abcdefghij", 4), "abcd…\n\n" + rest);
});

test("the text is cut in sentences, and a sentence too long at a comma or a space", () => {
  assert.deepEqual(m.sentences("One. Two? Three!\n\nFour"), [
    "One.",
    "Two?",
    "Three!",
    "Four",
  ]);
  const long = ("word ".repeat(WORDS_PER_CLAUSE) + ", ").repeat(CLAUSES).trim();
  const parts = m.sentences(long);
  assert.ok(parts.length > 1);
  for (const p of parts) {
    assert.ok(p.length <= MAX_PIECE, "each piece is short enough: " + p.length);
  }
  assert.equal(parts.join(" ").replace(/\s+/g, " "), long.replace(/\s+/g, " "));
  const word = "x".repeat(ONE_WORD);
  assert.deepEqual(
    m.sentences(word).map((p) => p.length),
    [MAX_PIECE, MAX_PIECE, ONE_WORD - 2 * MAX_PIECE],
  );
});

test("terms are said the way the language says them, as whole words", () => {
  assert.equal(
    m.pronounce("Abrí el PR y los PRs, y el CI pasó.", "es"),
    "Abrí el pe erre y los pe erres, y el ce i pasó.",
  );
  assert.equal(m.pronounce("PRINT CIA pr", "es"), "PRINT CIA pr");
  assert.equal(m.pronounce("the PR", "en"), "the PR");
  const s = m.script("Opened the **PR**.", 0, "es");
  assert.deepEqual(s, [
    { shown: "Opened the PR.", said: "Opened the pe erre." },
  ]);
});

test("the voices of the language, the device's first; the chosen one when it speaks it", () => {
  const all = [
    { voiceURI: "b", name: "Bea", lang: "es-MX", localService: false },
    { voiceURI: "a", name: "Ana", lang: "es_ES", localService: true },
    {
      voiceURI: "z",
      name: "Zoe",
      lang: "es-ES",
      localService: true,
      default: true,
    },
    { voiceURI: "e", name: "Eve", lang: "en-US", localService: true },
  ];
  assert.deepEqual(
    m.voicesFor(all, "es").map((v) => v.voiceURI),
    ["a", "z", "b"],
  );
  assert.equal(m.pickVoice(all, "b", "es").voiceURI, "b");
  assert.equal(
    m.pickVoice(all, "e", "es").voiceURI,
    "z",
    "a voice of another language is not used",
  );
  assert.equal(m.pickVoice(all, "", "en").voiceURI, "e");
  assert.equal(m.pickVoice(all.slice(0, 2), "", "es").voiceURI, "a");
  assert.equal(m.pickVoice([], "", "es"), null);
});

// fakeSynth stands in for speechSynthesis: it records what is said and lets the test end each
// utterance.
function fakeSynth() {
  const synth = {
    said: [],
    cancels: 0,
    current: null,
    speak(u) {
      this.said.push(u);
      this.current = u;
    },
    cancel() {
      this.cancels++;
    },
    start() {
      this.current.onstart?.({});
    },
    end() {
      this.current.onend?.({});
    },
    fail(error) {
      this.current.onerror?.({ error });
    },
  };
  const make = (text) => ({
    text,
    lang: "",
    rate: 1,
    voice: null,
    onstart: null,
    onend: null,
    onerror: null,
  });
  return { synth, reader: new m.Reader(synth, make) };
}

const two = [
  { shown: "One.", said: "One." },
  { shown: "PR.", said: "pe erre." },
];

test("the reader says the sentences one after another, and tells when it starts each", () => {
  const { synth, reader } = fakeSynth();
  const started = [];
  let ended = 0;
  const voice = { voiceURI: "a", name: "Ana", lang: "es-CL" };
  reader.read(two, {
    voice,
    lang: "es",
    rate: QUICK,
    onSentence: (i) => started.push(i),
    onEnd: (e) => {
      ended++;
      assert.equal(e, undefined);
    },
  });
  assert.equal(reader.reading, true);
  assert.equal(synth.said.length, 1, "one utterance at a time");
  assert.equal(synth.said[0].lang, "es-CL");
  assert.equal(synth.said[0].voice, voice);
  assert.equal(synth.said[0].rate, QUICK);
  synth.start();
  synth.end();
  assert.equal(synth.said[1].text, "pe erre.", "the said form is spoken");
  synth.start();
  synth.end();
  assert.deepEqual(started, [0, 1]);
  assert.equal(ended, 1);
  assert.equal(reader.reading, false);
});

test("stopping ends the reading once and ignores the voice's late events", () => {
  const { synth, reader } = fakeSynth();
  let ends = [];
  const started = [];
  reader.read(two, {
    voice: null,
    lang: "en",
    rate: 1,
    onSentence: (i) => started.push(i),
    onEnd: (e) => ends.push(e),
  });
  assert.equal(synth.said[0].lang, "en-US", "no voice: the language's locale");
  const first = synth.current;
  if (!first) {
    throw new Error("the first sentence is not being said");
  }
  reader.stop();
  assert.deepEqual(ends, [undefined]);
  first.onstart({});
  first.onend({});
  first.onerror({ error: "interrupted" });
  assert.equal(synth.said.length, 1, "nothing more is said");
  assert.deepEqual(started, []);
  assert.deepEqual(ends, [undefined]);
  // A new reading replaces the one in progress.
  ends = [];
  reader.read(two, {
    voice: null,
    lang: "en",
    rate: 1,
    onEnd: (e) => ends.push(e),
  });
  reader.read(two, {
    voice: null,
    lang: "en",
    rate: 1,
    onEnd: (e) => ends.push("second:" + e),
  });
  assert.deepEqual(ends, [undefined]);
  // Nothing to read ends at once.
  let empty = 0;
  reader.read([], {
    voice: null,
    lang: "en",
    rate: 1,
    onEnd: () => {
      empty += 1;
    },
  });
  assert.equal(empty, 1);
});

test("a cancel reported as an error is a stop; any other error is reported", () => {
  const { synth, reader } = fakeSynth();
  const ends = [];
  reader.read(two, {
    voice: null,
    lang: "en",
    rate: 1,
    onEnd: (e) => ends.push(e),
  });
  synth.fail("interrupted");
  reader.read(two, {
    voice: null,
    lang: "en",
    rate: 1,
    onEnd: (e) => ends.push(e),
  });
  synth.fail("not-allowed");
  reader.read(two, {
    voice: null,
    lang: "en",
    rate: 1,
    onEnd: (e) => ends.push(e),
  });
  synth.fail(undefined);
  assert.deepEqual(ends, [undefined, "not-allowed", "error"]);
  assert.equal(reader.reading, false);
});

test("the sentence is found on screen across tags and spacing, skipping the page's buttons", () => {
  const doc = dom.window.document;
  globalThis.document = doc;
  doc.body.innerHTML =
    '<div class="markdown-body"><button>copy</button><p>I changed <b>two</b>\n   files.</p><p>Next one.</p></div>';
  const root = doc.querySelector(".markdown-body");
  const r = m.textRange(root, "I changed two files.");
  assert.ok(r);
  assert.equal(r.toString().replace(/\s+/g, " "), "I changed two files.");
  assert.equal(
    m.textRange(root, "files. Next one.").toString().replace(/\s+/g, " "),
    "files.Next one.",
  );
  assert.equal(
    m.textRange(root, "copy"),
    null,
    "the button's text is not the answer's",
  );
  assert.equal(m.textRange(root, "Code block."), null);
  assert.equal(m.textRange(root, "   "), null);
});

test("the microphone asks for silence, and the device says whether it is speaking", () => {
  let asked = 0;
  dom.window.addEventListener(m.SPEECH_STOP_EVENT, () => {
    asked += 1;
  });
  m.stopSpeaking();
  assert.equal(asked, 1);
  assert.equal(m.voiceBusy(), false);
  globalThis.speechSynthesis = { speaking: true };
  assert.equal(m.voiceBusy(), true);
  delete globalThis.speechSynthesis;
});

test("natural style: the notes are what the steps did, without the mode line", () => {
  assert.deepEqual(
    m.notesOf([
      { kind: "step", text: "mode: plan (question)" },
      { kind: "command", text: " go test ./... " },
      { kind: "", text: "read the CI log" },
      { kind: "thought", text: "  " },
    ]),
    ["command: go test ./...", "read the CI log"],
  );
});

test("natural style: a progress line waits for something new and for the gap", () => {
  const gap = m.PROGRESS_GAP_MS;
  assert.equal(m.progressDue(gap, 0, NEW_NOTES, SAID_NOTES), true);
  assert.equal(
    m.progressDue(gap - 1, 0, NEW_NOTES, SAID_NOTES),
    false,
    "too soon",
  );
  assert.equal(m.progressDue(gap, 0, 2, 2), false, "nothing new");
});

test("natural style: the line is asked of the session, and a refusal throws", async () => {
  const calls = [];
  const api = async (path, init) => {
    calls.push({ path, init });
    if (path.includes("bad")) {
      return new Response("{}", { status: 502 });
    }
    return new Response(JSON.stringify({ text: "  Listo.  " }), {
      status: 200,
    });
  };
  const body = { stage: "start", lang: "es", request: "revisa" };
  assert.equal(await m.narrate(api, "s 1", body), "Listo.");
  assert.equal(calls[0].path, "/v1/sessions/s%201/narrate");
  assert.equal(calls[0].init.method, "POST");
  assert.equal(calls[0].init.headers["Content-Type"], "application/json");
  assert.deepEqual(JSON.parse(calls[0].init.body), body);
  await assert.rejects(m.narrate(api, "bad", body), /HTTP 502/);
  const empty = async () => new Response("{}", { status: 200 });
  assert.equal(await m.narrate(empty, "s", body), "");
});
