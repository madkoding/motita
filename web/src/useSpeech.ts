// useSpeech reads the agent's answers aloud: on request (the 🔊 button of an answer, or Alt+L for
// the last one), and on its own when a turn ends, as the settings say. The sentence being read is
// highlighted on screen. See speech.ts for the parts that are decided without a voice.
import { useCallback, useEffect, useMemo, useRef, useState } from "preact/hooks";
import { lang, t } from "./i18n";
import type { Message, SetToast } from "./appTypes";
import {
  type SpeechPrefs,
  type UtteranceLike,
  Reader,
  SPEECH_PREFS_EVENT,
  SPEECH_STOP_EVENT,
  loadSpeechPrefs,
  pickVoice,
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
}

// isAnswer says the message is an answer of the agent: what can be read aloud.
export function isAnswer(m: Message): boolean {
  return m.role === "agent" && !m.kind;
}

// spokenText is the part of an answer that is read: a report's summary, or the answer itself.
function spokenText(m: Message): string {
  return m.report?.summary || m.text;
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

  const stop = useCallback(() => {
    reader?.stop();
  }, [reader]);

  // follow says the view follows the sentence being read; scrolling by hand turns it off.
  const follow = useRef(true);

  // read says a text, highlighting each sentence inside `rootOf()` when there is one.
  const read = useCallback(
    (text: string, id: number | null, rootOf?: () => HTMLElement | null) => {
      if (!reader) {
        return;
      }
      const p = prefsRef.current;
      const l = lang();
      const list = script(text, id === null ? 0 : p.maxChars, l, {
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
            return;
          }
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

  // speak reads one answer, or stops it when it is the one being read.
  const speak = useCallback(
    (m: Message) => {
      if (reader?.reading && speakingId === m.id) {
        stop();
        return;
      }
      read(spokenText(m), m.id, () => messageRoot(m.id));
    },
    [read, reader, speakingId, stop],
  );

  // test reads a sample sentence with the settings as they are.
  const test = useCallback(() => {
    read(t("Hi, I am Motita. I opened the PR and the CI passed."), null);
  }, [read]);

  // Alt+L reads the last answer, or stops the reading.
  const messagesRef = useRef(messages);
  messagesRef.current = messages;
  useEffect(() => {
    if (!reader) {
      return undefined;
    }
    const onKey = (e: KeyboardEvent) => {
      if (!e.altKey || e.ctrlKey || e.metaKey || e.code !== "KeyL") {
        return;
      }
      e.preventDefault();
      if (reader.reading) {
        stop();
        return;
      }
      const m = lastAnswer(messagesRef.current);
      if (m) {
        read(spokenText(m), m.id, () => messageRoot(m.id));
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [reader, read, stop]);

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
  const pending = useRef<{ spoken: boolean; after: number } | null>(null);
  const wasRunning = useRef(running);
  useEffect(() => {
    if (running && !wasRunning.current) {
      const last = messages[messages.length - 1];
      pending.current = { spoken: takeHeard(), after: last ? last.id : -1 };
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
    const mode = prefsRef.current.mode;
    if (mode === "always" || (mode === "voice" && p.spoken)) {
      read(spokenText(answer), answer.id, () => messageRoot(answer.id));
    }
  }, [running, messages, takeHeard, read]);
  useEffect(() => {
    pending.current = null;
  }, [sessionId]);

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
