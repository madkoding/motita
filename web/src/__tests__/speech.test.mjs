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
const FAST = 1.5;
const MAX_TERM = 80; // speech.ts: the longest term or saying a pronunciation keeps
const NOT_ALLOWED = "not-allowed";

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
    engine: "local",
    gatewayVoice: "nova",
    localVoice: "es_MX-claude-high",
    voiceURI: "v1",
    rate: 1.5,
    maxChars: 0,
    pitch: 1.5,
    expressiveness: 0.4,
    variation: 2,
    kokoroMix: "kokoro-af_bella",
    kokoroMixAmount: 0.3,
    chatterboxEmotion: 1.2,
    conversation: true,
    notices: true,
    byLanguage: true,
    langVoices: { en: "eve" },
    pronunciations: [{ term: "kubectl", say: "cube control" }],
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
      engine: "robot",
      gatewayVoice: 7,
      localVoice: 8,
      voiceURI: 3,
      rate: 9,
      maxChars: 600,
      pitch: 9,
      expressiveness: -1,
      variation: "x",
      kokoroMix: 4,
      kokoroMixAmount: 1.5,
      chatterboxEmotion: 0.1,
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
    { shown: "Opened the PR.", said: "Opened the pe erre.", lang: "es" },
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
    pitch: 1.5,
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
  assert.equal(synth.said[0].pitch, 1.5);
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
  synth.fail(NOT_ALLOWED);
  reader.read(two, {
    voice: null,
    lang: "en",
    rate: 1,
    onEnd: (e) => ends.push(e),
  });
  synth.fail(undefined);
  assert.deepEqual(ends, [undefined, NOT_ALLOWED, "error"]);
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

// FakeAudio stands in for <audio>: the test plays, ends or fails it.
function audioFactory(made, { refuse = false } = {}) {
  return () => {
    const a = {
      src: "",
      playbackRate: 1,
      onplaying: null,
      onended: null,
      onerror: null,
      paused: false,
      play() {
        if (refuse) {
          return Promise.reject(
            Object.assign(new Error("no"), { name: "NotAllowedError" }),
          );
        }
        queueMicrotask(() => a.onplaying?.());
        return Promise.resolve();
      },
      pause() {
        a.paused = true;
      },
    };
    made.push(a);
    return a;
  };
}

const settle = () => new Promise((r) => setTimeout(r, 0));

function urls() {
  const live = new Set();
  let n = 0;
  return {
    live,
    create: () => {
      n += 1;
      const u = "blob:" + n;
      live.add(u);
      return u;
    },
    revoke: (u) => live.delete(u),
  };
}

const LIST = [
  { said: "Uno.", shown: "Uno." },
  { said: "Dos.", shown: "Dos." },
];

test("gateway voice: each sentence is asked for, the next one while one plays, and played in order", async () => {
  const asked = [];
  const made = [];
  const u = urls();
  const r = new m.ClipReader(
    () => async (text) => {
      asked.push(text);
      return new Blob([text]);
    },
    audioFactory(made),
    u,
  );
  const seen = [];
  let ended = "never";
  r.read(LIST, {
    voice: null,
    lang: "es",
    rate: FAST,
    onSentence: (i) => seen.push(i),
    onEnd: (e) => {
      ended = e;
    },
  });
  assert.equal(r.reading, true);
  await settle();
  assert.deepEqual(
    asked,
    ["Uno.", "Dos."],
    "the second is asked for while the first plays",
  );
  assert.equal(made.length, 1);
  assert.equal(made[0].src, "blob:1");
  assert.equal(made[0].playbackRate, FAST);
  assert.deepEqual(seen, [0]);
  assert.equal(
    m.voiceBusy(),
    true,
    "hands-free listening waits while the gateway speaks",
  );
  made[0].onended();
  await settle();
  assert.deepEqual(seen, [0, 1]);
  assert.equal(made[1].src, "blob:2");
  made[1].onended();
  await settle();
  assert.equal(ended, undefined, "finished without an error");
  assert.equal(m.voiceBusy(), false, "listening resumes when it ends");
  assert.equal(r.reading, false);
  assert.equal(u.live.size, 0, "the audio is let go");
  assert.deepEqual(asked, ["Uno.", "Dos."], "nothing is asked twice");
});

test("a voice that says several sentences at once is asked for as many ahead", async () => {
  const asked = [];
  const made = [];
  const said = async (text) => {
    asked.push(text);
    return new Blob([text]);
  };
  said.ahead = 3;
  const r = new m.ClipReader(() => said, audioFactory(made), urls());
  const list = ["1.", "2.", "3.", "4.", "5."].map((t) => ({ said: t, shown: t }));
  r.read(list, { voice: null, lang: "es", rate: 1 });
  await settle();
  assert.deepEqual(asked, ["1.", "2.", "3.", "4."], "three ahead of the one playing");
  made[0].onended();
  await settle();
  assert.deepEqual(asked, ["1.", "2.", "3.", "4.", "5."], "nothing past the end, nothing twice");
  for (let i = 1; i < list.length; i++) {
    made[i].onended();
    await settle();
  }
  assert.equal(r.reading, false);
});

test("gateway voice: a stop silences it and drops what comes back late", async () => {
  const made = [];
  let release;
  const r = new m.ClipReader(
    () => (text, signal) =>
      new Promise((resolve, reject) => {
        release = () => resolve(new Blob([text]));
        signal.addEventListener("abort", () => reject(new Error("aborted")));
      }),
    audioFactory(made),
    urls(),
  );
  let ends = 0;
  r.read(LIST, { voice: null, lang: "es", rate: 1, onEnd: () => (ends += 1) });
  r.stop();
  assert.equal(ends, 1);
  assert.equal(r.reading, false);
  release();
  await settle();
  assert.equal(made.length, 0, "nothing is played after the stop");

  // Stopping while a clip plays pauses it.
  const r2 = new m.ClipReader(
    () => async (t) => new Blob([t]),
    audioFactory(made),
    urls(),
  );
  r2.read(LIST, { voice: null, lang: "es", rate: 1 });
  await settle();
  r2.stop();
  assert.equal(made[0].paused, true);
  assert.equal(m.voiceBusy(), false);
  r2.read([], { voice: null, lang: "es", rate: 1, onEnd: () => (ends += 1) });
  assert.equal(ends, 2, "an empty reading ends at once");
});

test("gateway voice: a refusal, a broken clip or a blocked play end the reading with the reason", async () => {
  const run = async (fetchClip, opts = {}) => {
    const made = [];
    let ended = "never";
    const r = new m.ClipReader(
      () => fetchClip,
      audioFactory(made, opts),
      urls(),
    );
    r.read(LIST, {
      voice: null,
      lang: "es",
      rate: 1,
      onEnd: (e) => (ended = e),
    });
    await settle();
    return { made, ended: () => ended };
  };
  const refused = await run(async () => {
    throw new Error("the gateway has no voice");
  });
  assert.equal(refused.ended(), "the gateway has no voice");
  // Something that is not an Error, as some code throws, is reported as a plain "error".
  const notAnError = "?";
  const odd = await run(async () => {
    throw notAnError;
  });
  assert.equal(odd.ended(), "error");
  const blocked = await run(async (t) => new Blob([t]), { refuse: true });
  assert.equal(blocked.ended(), NOT_ALLOWED, "the browser wants a tap first");
  const broken = await run(async (t) => new Blob([t]));
  broken.made[0].onerror();
  assert.equal(broken.ended(), "audio");
});

test("gateway voice: the clip is asked of the gateway with the voice chosen", async () => {
  const calls = [];
  const api = async (path, init) => {
    calls.push({ path, init });
    if (init.body.includes("bad")) {
      return new Response(JSON.stringify({ error: "quota" }), { status: 502 });
    }
    if (init.body.includes("html")) {
      return new Response("<p>", { status: 500 });
    }
    return new Response("MP3", { status: 200 });
  };
  const ctl = new AbortController();
  const blob = await m.speakClip(api, "nova")("Hola.", ctl.signal);
  assert.equal(await blob.text(), "MP3");
  assert.equal(calls[0].path, "/v1/voice/speak");
  assert.equal(calls[0].init.method, "POST");
  assert.equal(calls[0].init.signal, ctl.signal);
  assert.deepEqual(JSON.parse(calls[0].init.body), {
    text: "Hola.",
    voice: "nova",
  });
  await assert.rejects(m.speakClip(api, "")("bad", ctl.signal), /quota/);
  await assert.rejects(m.speakClip(api, "")("html", ctl.signal), /HTTP 500/);
});

test("pronunciations: the person's own list is typed one a line, kept clean, and said first", () => {
  const list = m.parsePronunciations(
    "kubectl = cube control\nno equals sign\n = nothing\nnginx =\nKubectl = kube control\n motita=mo ti ta ",
  );
  assert.deepEqual(list, [
    { term: "Kubectl", say: "kube control" },
    { term: "motita", say: "mo ti ta" },
  ]);
  assert.equal(
    m.formatPronunciations(list),
    "Kubectl = kube control\nmotita = mo ti ta",
  );
  assert.equal(
    m.pronounce("Run KUBECTL get pods, not kubectl2.", "en", list),
    "Run kube control get pods, not kubectl2.",
    "any case, whole words only",
  );
  assert.equal(
    m.pronounce("El PR de motita", "es", list),
    "El pe erre de mo ti ta",
    "the built-in ones still apply",
  );
  // A saved list that is not one, or has broken entries, keeps only what is whole.
  localStorage.setItem(
    m.SPEECH_KEY,
    JSON.stringify({
      pronunciations: [
        { term: "a", say: "b" },
        { term: 3 },
        null,
        { term: "x".repeat(MAX_TERM + 1), say: "y" },
      ],
      langVoices: { es: "ana", en: 7, fr: "x" },
    }),
  );
  const p = m.loadSpeechPrefs();
  assert.deepEqual(p.pronunciations, [{ term: "a", say: "b" }]);
  assert.deepEqual(p.langVoices, { es: "ana" });
  localStorage.setItem(
    m.SPEECH_KEY,
    JSON.stringify({ pronunciations: "a=b", langVoices: null }),
  );
  assert.deepEqual(m.loadSpeechPrefs().pronunciations, []);
  assert.deepEqual(m.loadSpeechPrefs().langVoices, {});
});

test("a voice per language: each sentence is told apart by its common words", () => {
  assert.equal(
    m.detectLang("The tests pass and the build is green.", "es"),
    "en",
  );
  assert.equal(
    m.detectLang("Los tests pasan y el build está verde.", "en"),
    "es",
  );
  assert.equal(
    m.detectLang("Kubernetes.", "es"),
    "es",
    "a lone word changes nothing",
  );
  assert.equal(
    m.detectLang("the el", "es"),
    "es",
    "a tie keeps the interface's",
  );
  const list = m.script(
    "Abrí el PR y el CI pasó. The tests pass and the build is green.",
    0,
    "es",
    m.ENGLISH_WORDS,
    { byLanguage: true },
  );
  assert.deepEqual(
    list.map((s) => [s.lang, s.said]),
    [
      ["es", "Abrí el pe erre y el ce i pasó."],
      ["en", "The tests pass and the build is green."],
    ],
    "Spanish acronyms are said the Spanish way only in Spanish",
  );
  // The reader gives each sentence the voice of its language.
  const { synth, reader } = fakeSynth();
  const ana = { voiceURI: "a", name: "Ana", lang: "es-CL" };
  const eve = { voiceURI: "e", name: "Eve", lang: "en-US" };
  reader.read(list, {
    voice: ana,
    voiceFor: (l) => {
      if (l === "en") {
        return eve;
      }
      return null;
    },
    lang: "es",
    rate: 1,
  });
  synth.start();
  synth.end();
  assert.equal(synth.said[1].voice, eve);
  assert.equal(synth.said[1].lang, "en-US");
  reader.stop();
  // Without a voice for the language, the browser is asked for its locale.
  reader.read(list, { voice: ana, voiceFor: () => null, lang: "es", rate: 1 });
  synth.start();
  synth.end();
  assert.equal(synth.said.at(-1).lang, "en-US");
  assert.equal(synth.said.at(-1).voice, null);
});

test("a stop is told apart from an end, for whoever must go on after a reading", () => {
  const { synth, reader } = fakeSynth();
  const ends = [];
  reader.read(two, {
    voice: null,
    lang: "es",
    rate: 1,
    onEnd: (e, stopped) => ends.push([e, stopped]),
  });
  reader.stop();
  reader.read(two, {
    voice: null,
    lang: "es",
    rate: 1,
    onEnd: (e, stopped) => ends.push([e, stopped]),
  });
  synth.start();
  synth.end();
  synth.start();
  synth.end();
  assert.deepEqual(ends, [
    [undefined, true],
    [undefined, undefined],
  ]);
});

test("conversation: the exit phrases end it, with or without Motita's name and punctuation", () => {
  for (const s of [
    "Basta.",
    "Motita, basta",
    "¡Eso es todo!",
    "That's all.",
    "stop",
    "gracias, eso es todo",
  ]) {
    assert.equal(m.isExitPhrase(s), true, s);
  }
  for (const s of [
    "basta de errores en el CI",
    "stop the build",
    "",
    "para qué sirve esto",
  ]) {
    assert.equal(m.isExitPhrase(s), false, s);
  }
});

test("notices are handed to the page to be said, and an empty one is not", () => {
  const heard = [];
  const on = (e) => heard.push(e.detail);
  dom.window.addEventListener(m.SPEECH_NOTICE_EVENT, on);
  m.sayNotice("  CI passed  ");
  m.sayNotice("   ");
  dom.window.removeEventListener(m.SPEECH_NOTICE_EVENT, on);
  assert.deepEqual(heard, ["CI passed"]);
});
