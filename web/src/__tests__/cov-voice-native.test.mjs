// The voice group's plain modules imported as they are (as speech.test.mjs, push.test.mjs and
// notify.test.mjs do): what those tests leave out. No bundle is loaded in this file, so the
// coverage of these modules stays the module's own.
import test from "node:test";
import assert from "node:assert/strict";
import { JSDOM } from "jsdom";

const dom = new JSDOM("<!doctype html><html><body></body></html>", {
  url: "https://localhost/",
});
for (const k of ["document", "window", "CustomEvent", "localStorage"]) {
  globalThis[k] = dom.window[k];
}

const speech = await import("../speech.ts");
const speakable = await import("../speakable.ts");
const push = await import("../pushApi.ts");
const { showNotification } = await import("../notify.ts");

const settle = () => new Promise((r) => setImmediate(r));

// A stand-in <audio>: it plays at once and lets the test end it.
function audioFactory(made, { refuse } = {}) {
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
          return Promise.reject(refuse);
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
const urls = () => {
  let n = 0;
  return { create: () => "blob:" + ++n, revoke() {} };
};
const LIST = [
  { said: "Uno.", shown: "Uno." },
  { said: "Dos.", shown: "Dos." },
];

test("the settings fall back on what cannot be read, and a refused storage is not an error", () => {
  localStorage.setItem(
    speech.SPEECH_KEY,
    JSON.stringify({
      mode: "always",
      gatewayVoice: 5,
      pronunciations: "x",
      langVoices: null,
    }),
  );
  const p = speech.loadSpeechPrefs();
  assert.equal(p.mode, "always");
  assert.equal(
    p.gatewayVoice,
    speech.DEFAULT_SPEECH_PREFS.gatewayVoice,
    "a number is no voice",
  );
  assert.deepEqual(p.pronunciations, []);
  assert.deepEqual(p.langVoices, {});

  let told = 0;
  const onPrefs = () => told++;
  window.addEventListener(speech.SPEECH_PREFS_EVENT, onPrefs);
  const store = globalThis.localStorage;
  globalThis.localStorage = {
    setItem() {
      throw new Error("quota");
    },
  };
  try {
    speech.saveSpeechPrefs(p);
  } finally {
    globalThis.localStorage = store;
  }
  assert.equal(told, 1, "the page is still told");
  const win = globalThis.window;
  delete globalThis.window;
  try {
    speech.saveSpeechPrefs(p);
  } finally {
    globalThis.window = win;
  }
  assert.equal(told, 1, "without a page nobody is told");
  window.removeEventListener(speech.SPEECH_PREFS_EVENT, onPrefs);
});

test("a text without words keeps the fallback language", () => {
  assert.equal(speech.detectLang("1234 !!", "es"), "es");
  assert.equal(speech.detectLang("", "en"), "en");
});

test("a sentence is found when the whole document is searched", () => {
  document.body.innerHTML = "<p>Hello there.</p>";
  const r = speech.textRange(document, "Hello there.");
  assert.equal(r.toString(), "Hello there.");
});

test("the gateway's voice: its refusal without a message, and audio the browser cannot play", async () => {
  const api = async () => new Response(JSON.stringify({}), { status: 429 });
  await assert.rejects(
    speech.speakClip(api, "nova")("Hi.", new AbortController().signal),
    /HTTP 429/,
  );

  const made = [];
  const r = new speech.ClipReader(
    () => async (t) => new Blob([t]),
    audioFactory(made, {
      refuse: Object.assign(new Error("decode"), { name: "NotSupportedError" }),
    }),
    urls(),
  );
  let ended;
  r.read(LIST, { voice: null, lang: "es", rate: 1, onEnd: (e) => (ended = e) });
  await settle();
  await settle();
  assert.equal(ended, "audio");
  assert.equal(r.reading, false);
});

test("a clip that arrives after a stop is not played, nor is a late end of an old sentence", async () => {
  // The voice ignores the abort and answers anyway.
  const made = [];
  let release;
  const asked = [];
  const r = new speech.ClipReader(
    () => (t) => {
      asked.push(t);
      return new Promise((resolve) => {
        release = () => resolve(new Blob([t]));
      });
    },
    audioFactory(made),
    urls(),
  );
  let ends = 0;
  r.read([LIST[0]], { voice: null, lang: "es", rate: 1, onEnd: () => ends++ });
  r.stop();
  release();
  await settle();
  assert.equal(made.length, 0, "nothing played after the stop");
  assert.equal(ends, 1);

  // An audio element that reports its end a second time, once the reading was stopped.
  const made2 = [];
  const r2 = new speech.ClipReader(
    () => async (t) => new Blob([t]),
    audioFactory(made2),
    urls(),
  );
  const seen = [];
  r2.read(LIST, {
    voice: null,
    lang: "es",
    rate: 1,
    onSentence: (i) => seen.push(i),
  });
  await settle();
  const first = made2[0];
  const endedFirst = first.onended;
  endedFirst();
  await settle();
  assert.deepEqual(seen, [0, 1]);
  r2.stop();
  endedFirst();
  await settle();
  assert.equal(made2.length, 2, "the stopped reading does not go on");
  assert.equal(r2.reading, false);
});

test("what is read: a bare slash, an unclosed bracket and a heading's closing hashes", () => {
  assert.equal(speakable.toSpeakable("Run `/` now."), "Run / now.");
  assert.equal(
    speakable.toSpeakable("See [the note and more"),
    "See [the note and more",
  );
  assert.equal(speakable.toSpeakable("## Title ##"), "Title.");
});

test("phone notifications: the switch kept on this browser, and whether it can receive push", () => {
  push.savePushOn(true);
  assert.equal(push.loadPushOn(), true);
  const store = globalThis.localStorage;
  globalThis.localStorage = {
    setItem() {
      throw new Error("denied");
    },
    getItem() {
      throw new Error("denied");
    },
  };
  try {
    assert.doesNotThrow(() => push.savePushOn(true));
    assert.equal(push.loadPushOn(), false);
  } finally {
    globalThis.localStorage = store;
  }
  push.savePushOn(false);
  assert.equal(push.loadPushOn(), false);

  const nav = Object.getOwnPropertyDescriptor(globalThis, "navigator");
  const win = globalThis.window;
  try {
    Object.defineProperty(globalThis, "navigator", {
      value: { serviceWorker: {} },
      configurable: true,
    });
    globalThis.window = { PushManager: function () {} };
    assert.equal(push.pushSupported(), true);
    globalThis.window = {};
    assert.equal(push.pushSupported(), false, "no PushManager");
    delete globalThis.window;
    assert.equal(push.pushSupported(), false, "no window");
    Object.defineProperty(globalThis, "navigator", {
      value: {},
      configurable: true,
    });
    assert.equal(push.pushSupported(), false, "no service worker");
    Object.defineProperty(globalThis, "navigator", {
      value: undefined,
      configurable: true,
    });
    assert.equal(push.pushSupported(), false, "no navigator");
  } finally {
    Object.defineProperty(globalThis, "navigator", nav);
    globalThis.window = win;
  }
});

test("a notification falls back on the constructor when the worker cannot be asked", async () => {
  const nav = Object.getOwnPropertyDescriptor(globalThis, "navigator");
  const made = [];
  globalThis.Notification = function (t) {
    made.push(t);
  };
  Object.defineProperty(globalThis, "navigator", {
    value: {
      serviceWorker: {
        getRegistration: async () => {
          throw new Error("no worker");
        },
      },
    },
    configurable: true,
  });
  try {
    assert.equal(await showNotification("CI passed", {}), true);
  } finally {
    Object.defineProperty(globalThis, "navigator", nav);
    delete globalThis.Notification;
  }
  assert.deepEqual(made, ["CI passed"]);
});
