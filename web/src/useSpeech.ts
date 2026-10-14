// useSpeech reads the agent's answers aloud: on request (the 🔊 button of an answer, or Alt+L for
// the last one), and on its own when a turn ends, as the settings say. The sentence being read is
// highlighted on screen. In the natural style it says short lines the session's model writes
// instead: when the turn starts, now and then while the agent works, and the gist of the answer.
// See speech.ts for the parts that are decided without a voice.
import { useCallback, useEffect, useMemo, useRef, useState } from "preact/hooks";
import { lang, t } from "./i18n";
import type { Message, SetToast, StepView } from "./appTypes";
import type { Api } from "./prApi";
import {
  type SpeechPrefs,
  type UtteranceLike,
  Reader,
  SPEECH_PREFS_EVENT,
  SPEECH_STOP_EVENT,
  type NarrateBody,
  loadSpeechPrefs,
  narrate,
  notesOf,
  pickVoice,
  progressDue,
  saveSpeechPrefs,
  script,
  speechSupported,
  textRange,
  voicesFor,
} from "./speech";

// The name of the highlight the sentence being read is drawn with (::highlight(motita-reading)).
const HIGHLIGHT = "motita-reading";

interface Deps {
  messages: Message[];
  running: boolean;
  sessionId: string;
  // takeHeard says whether the person spoke the turn that just started, and forgets it.
  takeHeard: () => boolean;
  setToast: SetToast;
  // api and stepsByTurn serve the natural style; without api it reads word for word.
  api?: Api;
  stepsByTurn?: Record<number, StepView[]>;
}

// How often the natural style looks whether the agent did something worth a line.
const PROGRESS_TICK_MS = 4000;

// isAnswer says the message is an answer of the agent: what can be read aloud.
export function isAnswer(m: Message): boolean {
  return m.role === "agent" && !m.kind;
}

// spokenText is the part of an answer that is read: a report's summary, or the answer itself.
function spokenText(m: Message): string {
  return m.report?.summary || m.text;
}

// requestOf is what the person asked for an answer: the closest message of theirs before it.
function requestOf(messages: Message[], id: number): string {
  let asked = "";
  for (const m of messages) {
    if (m.id >= id) {
      break;
    }
    if (m.role === "user") {
      asked = m.shown || m.text;
    }
  }
  return asked;
}

// currentSteps are the steps of the newest turn: the one a running agent is adding to.
function currentSteps(stepsByTurn: Record<number, StepView[]> | undefined): StepView[] {
  const turns = Object.keys(stepsByTurn || {}).map(Number);
  if (!stepsByTurn || turns.length === 0) {
    return [];
  }
  return stepsByTurn[Math.max(...turns)] || [];
}

// lastAnswer is the newest answer of the conversation, if there is one.
function lastAnswer(messages: Message[]): Message | undefined {
  for (let i = messages.length - 1; i >= 0; i--) {
    if (isAnswer(messages[i])) {
      return messages[i];
    }
  }
  return undefined;
}

type HighlightRegistry = {
  set(name: string, h: unknown): void;
  delete(name: string): void;
};

// highlights is the page's CSS highlight registry, where the browser has one.
function highlights(): {
  registry: HighlightRegistry;
  make: (r: Range) => unknown;
} | null {
  const g = globalThis as {
    CSS?: { highlights?: HighlightRegistry };
    Highlight?: new (...r: Range[]) => unknown;
  };
  const registry = g.CSS?.highlights;
  const H = g.Highlight;
  if (!registry || !H) {
    return null;
  }
  return { registry, make: (r) => new H(r) };
}

function clearHighlight() {
  highlights()?.registry.delete(HIGHLIGHT);
}

function reducedMotion(): boolean {
  try {
    return window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  } catch {
    return false;
  }
}

// showRange highlights the sentence and, while the reader has not scrolled on their own, brings it
// into the conversation's view.
function showRange(range: Range, follow: boolean) {
  const h = highlights();
  h?.registry.set(HIGHLIGHT, h.make(range));
  if (!follow) {
    return;
  }
  const box = range.getBoundingClientRect();
  const log = (range.startContainer.parentElement || null)?.closest<HTMLElement>(
    "[role=log]",
  );
  if (!log || box.height === 0) {
    return;
  }
  const view = log.getBoundingClientRect();
  let delta = 0;
  if (box.top < view.top) {
    delta = box.top - view.top - 16;
  } else if (box.bottom > view.bottom) {
    delta = box.bottom - view.bottom + 16;
  }
  if (delta !== 0) {
    log.scrollBy({ top: delta, behavior: reducedMotion() ? "auto" : "smooth" });
  }
}

// messageRoot is where the text of an answer is drawn.
function messageRoot(id: number): HTMLElement | null {
  const el = document.querySelector<HTMLElement>(`[data-msg-id="${id}"]`);
  return el?.querySelector<HTMLElement>(".markdown-body") || el;
}

export function useSpeech({
  messages,
  running,
  sessionId,
  takeHeard,
  setToast,
  api,
  stepsByTurn,
}: Deps) {
  const supported = useMemo(speechSupported, []);
  const [prefs, setPrefsState] = useState<SpeechPrefs>(loadSpeechPrefs);
  const [voices, setVoices] = useState<SpeechSynthesisVoice[]>([]);
  const [speakingId, setSpeakingId] = useState<number | null>(null);
  const prefsRef = useRef(prefs);
  prefsRef.current = prefs;
  const voicesRef = useRef(voices);
  voicesRef.current = voices;

  const reader = useMemo(() => {
    if (!supported) {
      return null;
    }
    return new Reader(
      window.speechSynthesis as unknown as ConstructorParameters<typeof Reader>[0],
      (text) => new SpeechSynthesisUtterance(text) as unknown as UtteranceLike,
    );
  }, [supported]);

  // The voices arrive late in Chrome (voiceschanged) and at once in Safari.
  useEffect(() => {
    if (!supported) {
      return undefined;
    }
    const synth = window.speechSynthesis;
    const load = () => setVoices(synth.getVoices());
    load();
    synth.addEventListener?.("voiceschanged", load);
    return () => synth.removeEventListener?.("voiceschanged", load);
  }, [supported]);

  useEffect(() => {
    const reload = () => setPrefsState(loadSpeechPrefs());
    window.addEventListener(SPEECH_PREFS_EVENT, reload);
    return () => window.removeEventListener(SPEECH_PREFS_EVENT, reload);
  }, []);

  const setPrefs = useCallback((p: SpeechPrefs) => {
    setPrefsState(p);
    saveSpeechPrefs(p);
  }, []);

  // NATURAL STYLE state: the lines waiting for the voice, the lines said this turn, and a counter
  // that a stop or a new turn bumps, so an answer from the model that comes back late is dropped.
  const queue = useRef<{ text: string; id: number | null }[]>([]);
  const said = useRef<string[]>([]);
  const generation = useRef(0);

  const stop = useCallback(() => {
    queue.current = [];
    generation.current++;
    reader?.stop();
    setSpeakingId(null);
  }, [reader]);

  // follow says the view follows the sentence being read; scrolling by hand turns it off.
  const follow = useRef(true);

  // read says a text, highlighting each sentence inside `rootOf()` when there is one.
  const readRef = useRef<typeof read | null>(null);
  const read = useCallback(
    (
      text: string,
      id: number | null,
      rootOf?: () => HTMLElement | null,
      whole = false,
    ) => {
      if (!reader) {
        return;
      }
      const p = prefsRef.current;
      const l = lang();
      const list = script(text, id === null || whole ? 0 : p.maxChars, l, {
        link: t("link"),
        code: t("Code block."),
        formula: t("Formula."),
        table: t("Table."),
        rest: t("The rest is on screen."),
      });
      follow.current = true;
      setSpeakingId(id);
      reader.read(list, {
        voice: pickVoice(voicesRef.current, p.voiceURI, l),
        lang: l,
        rate: p.rate,
        onSentence: (i) => {
          const root = rootOf?.();
          const range = root && textRange(root, list[i].shown);
          if (range) {
            showRange(range, follow.current);
          } else {
            clearHighlight();
          }
        },
        onEnd: (error) => {
          clearHighlight();
          setSpeakingId(null);
          if (!error) {
            const next = queue.current.shift();
            if (next) {
              readRef.current?.(next.text, next.id, undefined, true);
            }
            return;
          }
          queue.current = [];
          setToast({
            message: t("Reading aloud failed"),
            detail: errorHint(error),
            type: "error",
          });
        },
      });
    },
    [reader, setToast],
  );
  readRef.current = read;

  // say speaks one line of the natural style, after the line being said if there is one.
  const say = useCallback(
    (text: string, id: number | null) => {
      if (!text) {
        return;
      }
      said.current.push(text);
      if (reader?.reading) {
        queue.current.push({ text, id });
        return;
      }
      read(text, id, undefined, true);
    },
    [read, reader],
  );

  // ask asks the model for a line of this turn; null when it failed or the turn moved on.
  const ask = useCallback(
    async (body: Omit<NarrateBody, "lang">): Promise<string | null> => {
      if (!api) {
        return null;
      }
      const mine = generation.current;
      try {
        const text = await narrate(api, sessionId, { ...body, lang: lang() });
        return mine === generation.current ? text : null;
      } catch {
        return null;
      }
    },
    [api, sessionId],
  );

  // tellAnswer says an answer: its gist in the natural style, falling back to the answer itself
  // when the model cannot be asked; word for word otherwise.
  const messagesRef = useRef(messages);
  messagesRef.current = messages;
  const tellAnswer = useCallback(
    (m: Message) => {
      const root = () => messageRoot(m.id);
      if (prefsRef.current.style !== "natural" || !api) {
        read(spokenText(m), m.id, root);
        return;
      }
      queue.current = [];
      const mine = ++generation.current;
      setSpeakingId(m.id);
      void ask({
        stage: "answer",
        request: requestOf(messagesRef.current, m.id),
        answer: spokenText(m),
      }).then((text) => {
        if (mine !== generation.current) {
          return;
        }
        if (text) {
          reader?.stop();
          said.current.push(text);
          read(text, m.id, undefined, true);
          return;
        }
        read(spokenText(m), m.id, root);
      });
    },
    [api, ask, read, reader],
  );

  // speak reads one answer, or stops it when it is the one being read.
  const speak = useCallback(
    (m: Message) => {
      if (speakingId === m.id) {
        stop();
        return;
      }
      tellAnswer(m);
    },
    [tellAnswer, speakingId, stop],
  );

  // test reads a sample sentence with the settings as they are.
  const test = useCallback(() => {
    read(t("Hi, I am Motita. I opened the PR and the CI passed."), null);
  }, [read]);

  // Alt+L reads the last answer, or stops the reading.
  const speakingRef = useRef(speakingId);
  speakingRef.current = speakingId;
  useEffect(() => {
    if (!reader) {
      return undefined;
    }
    const onKey = (e: KeyboardEvent) => {
      if (!e.altKey || e.ctrlKey || e.metaKey || e.code !== "KeyL") {
        return;
      }
      e.preventDefault();
      if (reader.reading || speakingRef.current !== null) {
        stop();
        return;
      }
      const m = lastAnswer(messagesRef.current);
      if (m) {
        tellAnswer(m);
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [reader, tellAnswer, stop]);

  // Scrolling by hand while reading leaves the view where the person put it.
  useEffect(() => {
    if (speakingId === null) {
      return undefined;
    }
    const off = () => {
      follow.current = false;
    };
    window.addEventListener("wheel", off, { passive: true });
    window.addEventListener("touchmove", off, { passive: true });
    return () => {
      window.removeEventListener("wheel", off);
      window.removeEventListener("touchmove", off);
    };
  }, [speakingId]);

  // The microphone asks for silence; leaving the conversation or the page ends the reading.
  useEffect(() => {
    window.addEventListener(SPEECH_STOP_EVENT, stop);
    window.addEventListener("pagehide", stop);
    return () => {
      window.removeEventListener(SPEECH_STOP_EVENT, stop);
      window.removeEventListener("pagehide", stop);
    };
  }, [stop]);
  useEffect(() => stop, [sessionId, stop]);

  // Safari speaks only after a tap or a key: the first one unlocks the voice with an empty
  // utterance, so an answer can later be read on its own.
  useEffect(() => {
    if (!supported || prefs.mode === "off") {
      return undefined;
    }
    const unlock = () => {
      window.speechSynthesis.speak(new SpeechSynthesisUtterance(""));
      remove();
    };
    const remove = () => {
      window.removeEventListener("pointerdown", unlock);
      window.removeEventListener("keydown", unlock);
    };
    window.addEventListener("pointerdown", unlock, { once: true });
    window.addEventListener("keydown", unlock, { once: true });
    return remove;
  }, [supported, prefs.mode]);

  // AUTOMATIC READING: when a turn starts, note whether it was spoken and which messages were
  // there; when it ends, read the answer it produced if the mode asks for it.
  // In the natural style the turn is also talked about while it runs: a line when it starts,
  // and a line now and then about what the agent is doing.
  const pending = useRef<{
    auto: boolean;
    after: number;
    request: string;
  } | null>(null);
  const wasRunning = useRef(running);
  useEffect(() => {
    if (running && !wasRunning.current) {
      const last = messages[messages.length - 1];
      const mode = prefsRef.current.mode;
      const spoken = takeHeard();
      const auto = mode === "always" || (mode === "voice" && spoken);
      const request = [...messages].reverse().find((m) => m.role === "user");
      pending.current = {
        auto,
        after: last ? last.id : -1,
        request: request ? request.shown || request.text : "",
      };
      said.current = [];
      if (auto && prefsRef.current.style === "natural" && pending.current.request) {
        stop();
        void ask({ stage: "start", request: pending.current.request }).then(
          (text) => text && say(text, null),
        );
      }
    }
    wasRunning.current = running;
    const p = pending.current;
    if (running || !p) {
      return;
    }
    const answer = lastAnswer(messages);
    if (!answer || answer.id <= p.after) {
      return;
    }
    pending.current = null;
    if (p.auto) {
      tellAnswer(answer);
    }
  }, [running, messages, takeHeard, tellAnswer, ask, say, stop]);
  useEffect(() => {
    pending.current = null;
  }, [sessionId]);

  // PROGRESS LINES: while a natural turn runs, every few seconds, when the agent did something new
  // since the last line and nothing is being said or asked, the model is asked for one more.
  const stepsRef = useRef(stepsByTurn);
  stepsRef.current = stepsByTurn;
  const natural = prefs.style === "natural" && !!api;
  useEffect(() => {
    if (!running || !natural) {
      return undefined;
    }
    let lastAt = Date.now();
    let lastNotes = 0;
    let asking = false;
    const timer = setInterval(() => {
      const p = pending.current;
      const notes = notesOf(currentSteps(stepsRef.current));
      if (
        !p?.auto ||
        asking ||
        reader?.reading ||
        !progressDue(Date.now(), lastAt, notes.length, lastNotes)
      ) {
        return;
      }
      asking = true;
      lastNotes = notes.length;
      void ask({
        stage: "progress",
        request: p.request,
        notes,
        said: said.current,
      }).then((text) => {
        asking = false;
        lastAt = Date.now();
        if (text && pending.current === p) {
          say(text, null);
        }
      });
    }, PROGRESS_TICK_MS);
    return () => clearInterval(timer);
  }, [running, natural, ask, say, reader]);

  return {
    supported,
    prefs,
    setPrefs,
    voices: voicesFor(voices, lang()),
    speakingId,
    speak,
    stop,
    test,
  };
}

// errorHint says why the voice failed, in words a person can act on.
function errorHint(error: string): string {
  if (error === "not-allowed") {
    return t("The browser wants a tap first: press 🔊 on an answer once.");
  }
  if (error === "language-unavailable" || error === "voice-unavailable") {
    return t("This device has no voice for this language.");
  }
  return error;
}

export type Speech = ReturnType<typeof useSpeech>;
