// useSpeech reads the agent's answers aloud: on request (the 🔊 button of an answer, or Alt+L for
// the last one), and on its own when a turn ends, as the settings say. The sentence being read is
// highlighted on screen, and shown as a subtitle when the settings ask. In the natural style it says short lines the session's model writes
// instead: when the turn starts and the gist of the answer. What the agent does in between (its
// steps, decisions, thoughts) is never read aloud.
// See speech.ts for the parts that are decided without a voice.
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "preact/hooks";
import { choose } from "./choose";
import { scrollKey } from "./scrollIntent";
import { localClip, localVoicesSupported, warmLocal } from "./localVoice";
import { lang, t } from "./i18n";
import type { Message, SetToast } from "./appTypes";
import type { Api } from "./prApi";
import {
  type AudioLike,
  ClipReader,
  type SpeechPrefs,
  type UtteranceLike,
  Reader,
  SPEECH_PREFS_EVENT,
  SPEECH_NOTICE_EVENT,
  SPEECH_STOP_EVENT,
  SPEECH_TURN_READ_EVENT,
  sayNotice,
  type NarrateBody,
  loadSpeechPrefs,
  narrate,
  pickVoice,
  saveSpeechPrefs,
  script,
  showSubtitle,
  speakClip,
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
  // api serves the natural style; without it the answer is read word for word.
  api?: Api;
  // tts is the gateway's voice, as GET /v1/voice describes it; absent or off, the device speaks.
  tts?: TTSInfo;
  // local says the gateway offers local voices (GET /v1/voice); the page runs them itself.
  local?: boolean;
  // approval is the question the agent waits on, said aloud as a notice when it arrives.
  approval?: { id: string; question?: string } | null;
}

export interface TTSInfo {
  enabled: boolean;
  voices: string[];
  max_chars: number;
}

// AnyReader is the part of a reader the hook uses: the device's (Reader) and the gateway's
// (ClipReader) alike.
interface AnyReader {
  readonly reading: boolean;
  read(
    list: Parameters<Reader["read"]>[0],
    opts: Parameters<Reader["read"]>[1],
  ): void;
  stop(): void;
}

// isAnswer says the message is an answer of the agent: what can be read aloud.
export function isAnswer(m: Message): boolean {
  return m.role === "agent" && !m.kind && !m.draft;
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

// How far past the edge of the conversation a sentence brought into view stays.
const SCROLL_MARGIN = 16;

// scrollDelta is how far the conversation scrolls to show a sentence: none while it is in view.
function scrollDelta(box: DOMRect, view: DOMRect): number {
  if (box.top < view.top) {
    return box.top - view.top - SCROLL_MARGIN;
  }
  if (box.bottom > view.bottom) {
    return box.bottom - view.bottom + SCROLL_MARGIN;
  }
  return 0;
}

// scrollBehavior moves the view at once for a reader who asked for less motion.
function scrollBehavior(): ScrollBehavior {
  if (reducedMotion()) {
    return "auto";
  }
  return "smooth";
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
  const log =
    range.startContainer.parentElement?.closest<HTMLElement>("[role=log]");
  if (!log || box.height === 0) {
    return;
  }
  const delta = scrollDelta(box, log.getBoundingClientRect());
  if (delta !== 0) {
    log.scrollBy({ top: delta, behavior: scrollBehavior() });
  }
}

// messageRoot is where the text of an answer is drawn.
function messageRoot(id: number): HTMLElement | null {
  const el = document.querySelector<HTMLElement>(`[data-msg-id="${id}"]`);
  return el?.querySelector<HTMLElement>(".markdown-body") || el;
}

// readLimit is how much of a text is read: all of a sample or a line of the natural style, an
// answer up to the length the settings say.
function readLimit(
  id: number | null,
  whole: boolean,
  maxChars: number,
): number {
  if (id === null || whole) {
    return 0;
  }
  return maxChars;
}

// The words said in place of what is not read, in the interface language.
function speechWords() {
  return {
    link: t("link"),
    code: t("Code block."),
    formula: t("Formula."),
    table: t("Table."),
    rest: t("The rest is on screen."),
  };
}

// turnRead tells the page the answer to a spoken turn was read to its end (conversation mode).
function turnRead() {
  window.dispatchEvent(new CustomEvent(SPEECH_TURN_READ_EVENT));
}

// A turn being watched: whether its answer is read on its own, the last message before it, and
// what the person asked.
interface PendingTurn {
  auto: boolean;
  // spoken says the person spoke the turn: in conversation mode, the microphone listens again
  // when its answer has been read.
  spoken: boolean;
  after: number;
  request: string;
}

// turnStart is what is noted when a turn starts.
function turnStart(
  messages: Message[],
  mode: SpeechPrefs["mode"],
  spoken: boolean,
): PendingTurn {
  const last = messages.at(-1);
  const request = [...messages].reverse().find((m) => m.role === "user");
  let asked = "";
  if (request) {
    asked = request.shown || request.text;
  }
  return {
    auto: mode === "always" || (mode === "voice" && spoken),
    spoken,
    after: last?.id ?? -1,
    request: asked,
  };
}

// useSpeechPrefs are the read-aloud settings, read again when the settings screen changes them.
function useSpeechPrefs() {
  const [prefs, setPrefsState] = useState<SpeechPrefs>(() =>
    loadSpeechPrefs(lang()),
  );
  const prefsRef = useRef(prefs);
  prefsRef.current = prefs;
  useEffect(() => {
    const reload = () => setPrefsState(loadSpeechPrefs(lang()));
    window.addEventListener(SPEECH_PREFS_EVENT, reload);
    return () => window.removeEventListener(SPEECH_PREFS_EVENT, reload);
  }, []);
  const setPrefs = useCallback((p: SpeechPrefs) => {
    setPrefsState(p);
    saveSpeechPrefs(p);
  }, []);
  return { prefs, prefsRef, setPrefs };
}

// useDeviceVoices are the voices of the device. They arrive late in Chrome (voiceschanged) and at
// once in Safari.
function useDeviceVoices(supported: boolean) {
  const [voices, setVoices] = useState<SpeechSynthesisVoice[]>([]);
  const voicesRef = useRef(voices);
  voicesRef.current = voices;
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
  return { voices, voicesRef };
}

// useDeviceReader is the device's reader, where the browser can speak.
function useDeviceReader(device: boolean): Reader | null {
  return useMemo(() => {
    if (!device) {
      return null;
    }
    return new Reader(
      window.speechSynthesis as unknown as ConstructorParameters<
        typeof Reader
      >[0],
      (text) => new SpeechSynthesisUtterance(text) as unknown as UtteranceLike,
    );
  }, [device]);
}

// useClipReader is the gateway's reader, where the gateway has a voice: each sentence is said with
// the voice chosen in the settings at the time it is read.
function useClipReader(
  on: boolean,
  api: Api | undefined,
  prefsRef: { current: SpeechPrefs },
): ClipReader | null {
  const apiRef = useRef(api);
  apiRef.current = api;
  return useMemo(() => {
    if (!on) {
      return null;
    }
    return new ClipReader(
      () => {
        const p = prefsRef.current;
        if (p.engine === "local") {
          return localClip(p.localVoice, {
            noise: p.expressiveness,
            noiseW: p.variation,
            length: 1 / p.rate,
          });
        }
        return speakClip(apiRef.current as Api, p.gatewayVoice);
      },
      () => new Audio() as unknown as AudioLike,
      {
        create: (b) => URL.createObjectURL(b),
        revoke: (u) => URL.revokeObjectURL(u),
      },
    );
  }, [on, prefsRef]);
}

// clipSpeaks says the clip reader is the one to speak: the gateway or a local voice is chosen,
// and on.
function clipSpeaks(
  p: SpeechPrefs,
  gatewayOn: boolean,
  localOn: boolean,
): boolean {
  if (p.engine === "gateway") {
    return gatewayOn;
  }
  return p.engine === "local" && localOn && p.localVoice !== "";
}

// useReader is whichever speaks: the gateway or a local voice when it is chosen and on, the
// device otherwise.
// Stopping stops both, so a change of engine mid-sentence leaves nothing talking.
function useReader(
  device: Reader | null,
  clip: ClipReader | null,
  prefsRef: { current: SpeechPrefs },
  gatewayOn: boolean,
  localOn: boolean,
): AnyReader | null {
  return useMemo(() => {
    const first = device || clip;
    if (!first) {
      return null;
    }
    const pick = (): AnyReader => {
      if (clip && clipSpeaks(prefsRef.current, gatewayOn, localOn)) {
        return clip;
      }
      return first;
    };
    return {
      get reading() {
        return !!(device?.reading || clip?.reading);
      },
      read: (list, opts) => {
        const r = pick();
        for (const other of [device, clip]) {
          if (other && other !== r) {
            other.stop();
          }
        }
        r.read(list, opts);
      },
      stop: () => {
        device?.stop();
        clip?.stop();
      },
    };
  }, [device, clip, prefsRef, gatewayOn, localOn]);
}

// useFollow says the view follows the sentence being read: scrolling by hand while reading (the
// wheel, a finger, the scroll keys, a drag of the conversation's bar) turns it off, and leaves the
// view where the person put it.
function useFollow(speakingId: number | null) {
  const follow = useRef(true);
  useEffect(() => {
    if (speakingId === null) {
      return undefined;
    }
    const off = () => {
      follow.current = false;
    };
    const onKey = (e: KeyboardEvent) => {
      if (scrollKey(e.key, editable(e.target))) {
        off();
      }
    };
    // A press on the conversation itself, not on a message in it, is a press on its scroll bar.
    const onPointerDown = (e: PointerEvent) => {
      if ((e.target as Element | null)?.getAttribute?.("role") === "log") {
        off();
      }
    };
    window.addEventListener("wheel", off, { passive: true });
    window.addEventListener("touchmove", off, { passive: true });
    window.addEventListener("keydown", onKey);
    window.addEventListener("pointerdown", onPointerDown);
    return () => {
      window.removeEventListener("wheel", off);
      window.removeEventListener("touchmove", off);
      window.removeEventListener("keydown", onKey);
      window.removeEventListener("pointerdown", onPointerDown);
    };
  }, [speakingId]);
  return follow;
}

// editable says a key lands in a field, where it types instead of scrolling.
function editable(target: EventTarget | null): boolean {
  const el = target as HTMLElement | null;
  if (!el?.tagName) {
    return false;
  }
  return (
    /^(INPUT|TEXTAREA|SELECT)$/.test(el.tagName) ||
    el.isContentEditable === true
  );
}

// useStopOnLeave ends the reading when the microphone asks for silence, and on leaving the
// conversation or the page.
function useStopOnLeave(stop: () => void, sessionId: string) {
  useEffect(() => {
    window.addEventListener(SPEECH_STOP_EVENT, stop);
    window.addEventListener("pagehide", stop);
    return () => {
      window.removeEventListener(SPEECH_STOP_EVENT, stop);
      window.removeEventListener("pagehide", stop);
    };
  }, [stop]);
  useEffect(() => stop, [sessionId, stop]);
}

// useVoiceUnlock: Safari speaks only after a tap or a key, so the first one unlocks the voice with
// an empty utterance, and an answer can later be read on its own.
function useVoiceUnlock(supported: boolean, mode: SpeechPrefs["mode"]) {
  useEffect(() => {
    if (!supported || mode === "off") {
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
  }, [supported, mode]);
}

// useReadShortcut: Alt+L reads the last answer, or stops the reading.
function useReadShortcut(
  reader: AnyReader | null,
  speakingId: number | null,
  messagesRef: { current: Message[] },
  tellAnswer: (m: Message) => void,
  stop: () => void,
) {
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
  }, [reader, messagesRef, tellAnswer, stop]);
}

type Ask = (body: Omit<NarrateBody, "lang">) => Promise<string | null>;
type Say = (text: string, id: number | null) => void;

// useAutoRead is the AUTOMATIC READING: when a turn starts, note whether it was spoken and which
// messages were there; when it ends, read the answer it produced if the mode asks for it. In the
// natural style a line is also said when the turn starts. It returns the turn being watched.
function useAutoRead(o: {
  running: boolean;
  messages: Message[];
  sessionId: string;
  takeHeard: () => boolean;
  prefsRef: { current: SpeechPrefs };
  said: { current: string[] };
  tellAnswer: (m: Message, after?: () => void) => void;
  ask: Ask;
  say: Say;
  stop: () => void;
}) {
  const { running, messages, takeHeard, tellAnswer, ask, say, stop } = o;
  const { prefsRef, said } = o;
  const pending = useRef<PendingTurn | null>(null);
  const wasRunning = useRef(running);
  useEffect(() => {
    const begin = () => {
      const p = turnStart(messages, prefsRef.current.mode, takeHeard());
      pending.current = p;
      said.current = [];
      if (p.auto && prefsRef.current.style === "natural" && p.request) {
        stop();
        void ask({ stage: "start", request: p.request }).then(
          (text) => text && say(text, null),
        );
      }
    };
    const end = () => {
      const p = pending.current;
      const answer = lastAnswer(messages);
      if (!p || !answer || answer.id <= p.after) {
        return;
      }
      pending.current = null;
      if (p.auto) {
        tellAnswer(answer, choose(p.spoken, turnRead, undefined));
      }
    };
    const started = running && !wasRunning.current;
    wasRunning.current = running;
    if (started) {
      begin();
    } else if (!running) {
      end();
    } else {
      // Still running: no turn starts or ends here.
    }
  }, [
    running,
    messages,
    takeHeard,
    tellAnswer,
    ask,
    say,
    stop,
    prefsRef,
    said,
  ]);
  useEffect(() => {
    pending.current = null;
  }, [o.sessionId]);
  return pending;
}

// useNotices says the page's notices aloud, when the person turned voice notices on and the tab is
// visible: a notice for a tab nobody looks at is noise in the room. A notice that comes while
// something is said waits its turn. A new approval is one of them.
function useNotices(
  prefsRef: { current: SpeechPrefs },
  say: Say,
  approval: { id: string; question?: string } | null | undefined,
) {
  useEffect(() => {
    const onNotice = (e: Event) => {
      const text = (e as CustomEvent<string>).detail;
      if (prefsRef.current.notices && !document.hidden && text) {
        say(text, null);
      }
    };
    window.addEventListener(SPEECH_NOTICE_EVENT, onNotice);
    return () => window.removeEventListener(SPEECH_NOTICE_EVENT, onNotice);
  }, [prefsRef, say]);
  const id = approval?.id;
  const question = approval?.question;
  useEffect(() => {
    if (id) {
      sayNotice(t("Motita needs your answer.") + " " + (question || ""));
    }
  }, [id, question]);
}

type ReadText = (
  text: string,
  id: number | null,
  rootOf?: () => HTMLElement | null,
  whole?: boolean,
  after?: () => void,
) => void;

// useReadText is the function that says a text, highlighting each sentence inside `rootOf()`
// when there is one; when it ends, the next line waiting in the queue is said.
function useReadText(o: {
  reader: AnyReader | null;
  prefsRef: { current: SpeechPrefs };
  voicesRef: { current: SpeechSynthesisVoice[] };
  follow: { current: boolean };
  queue: { current: { text: string; id: number | null }[] };
  setSpeakingId: (id: number | null) => void;
  setToast: SetToast;
}): ReadText {
  const {
    reader,
    prefsRef,
    voicesRef,
    follow,
    queue,
    setSpeakingId,
    setToast,
  } = o;
  const readRef = useRef<typeof read | null>(null);
  const read = useCallback(
    (
      text: string,
      id: number | null,
      rootOf?: () => HTMLElement | null,
      whole = false,
      after?: () => void,
    ) => {
      if (!reader) {
        return;
      }
      const p = prefsRef.current;
      const l = lang();
      const list = script(
        text,
        readLimit(id, whole, p.maxChars),
        l,
        speechWords(),
        { byLanguage: p.byLanguage, pronunciations: p.pronunciations },
      );
      follow.current = true;
      setSpeakingId(id);
      reader.read(list, {
        voice: pickVoice(voicesRef.current, p.voiceURI, l),
        voiceFor: (other) =>
          pickVoice(voicesRef.current, p.langVoices[other] || "", other),
        lang: l,
        // A local voice already speaks at the speed (its length_scale); playing it faster again
        // would double it.
        rate: choose(p.engine === "local", 1, p.rate),
        pitch: p.pitch,
        onSentence: (i) => {
          showSubtitle(list[i].shown);
          const root = rootOf?.();
          const range = root && textRange(root, list[i].shown);
          if (range) {
            showRange(range, follow.current);
          } else {
            clearHighlight();
          }
        },
        onEnd: (error, stopped) => {
          // A stop belongs to whoever stopped it: a new reading that takes over, or stop(), which
          // clears the state itself.
          if (stopped) {
            return;
          }
          showSubtitle("");
          clearHighlight();
          setSpeakingId(null);
          if (!error) {
            const next = queue.current.shift();
            if (next) {
              readRef.current?.(next.text, next.id, undefined, true, after);
            } else {
              after?.();
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
    [reader, setToast, prefsRef, voicesRef, follow, queue, setSpeakingId],
  );
  readRef.current = read;
  return read;
}

// speechEngines says which voices can read aloud: the gateway's, the local ones the page runs,
// and whether any voice at all (the device's included) can.
function speechEngines(
  device: boolean,
  tts: TTSInfo | undefined,
  local: boolean,
  api: Api | undefined,
) {
  const audio = typeof Audio !== "undefined";
  const gatewayOn = !!(tts?.enabled && api && audio);
  const localOn = !!(local && api && audio && localVoicesSupported());
  return { gatewayOn, localOn, supported: device || gatewayOn || localOn };
}

// gatewayVoicesOf are the gateway's voices, when it reads aloud.
function gatewayVoicesOf(
  gatewayOn: boolean,
  tts: TTSInfo | undefined,
): string[] {
  return choose(gatewayOn, tts?.voices ?? [], []);
}

export function useSpeech({
  messages,
  running,
  sessionId,
  takeHeard,
  setToast,
  api,
  tts,
  local,
  approval,
}: Deps) {
  const device = useMemo(speechSupported, []);
  // A gateway that says nothing about local voices offers none.
  const { gatewayOn, localOn, supported } = speechEngines(
    device,
    tts,
    !!local,
    api,
  );
  const { prefs, prefsRef, setPrefs } = useSpeechPrefs();
  const { voices, voicesRef } = useDeviceVoices(device);
  const [speakingId, setSpeakingId] = useState<number | null>(null);
  // A local voice in use is loaded with the page, so the first answer does not wait for it.
  const warmId = choose(
    localOn && prefs.engine === "local",
    prefs.localVoice,
    "",
  );
  useEffect(() => {
    if (warmId) {
      warmLocal(warmId);
    }
  }, [warmId]);
  const reader = useReader(
    useDeviceReader(device),
    useClipReader(gatewayOn || localOn, api, prefsRef),
    prefsRef,
    gatewayOn,
    localOn,
  );

  // NATURAL STYLE state: the lines waiting for the voice, the lines said this turn, and a counter
  // that a stop or a new turn bumps, so an answer from the model that comes back late is dropped.
  const queue = useRef<{ text: string; id: number | null }[]>([]);
  const said = useRef<string[]>([]);
  const generation = useRef(0);

  const stop = useCallback(() => {
    queue.current = [];
    generation.current++;
    reader?.stop();
    clearHighlight();
    showSubtitle("");
    setSpeakingId(null);
  }, [reader]);

  const follow = useFollow(speakingId);

  const read = useReadText({
    reader,
    prefsRef,
    voicesRef,
    follow,
    queue,
    setSpeakingId,
    setToast,
  });

  // say speaks one line of the natural style, after the line being said if there is one.
  const say = useCallback(
    (text: string, id: number | null) => {
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
        if (mine !== generation.current) {
          return null;
        }
        return text;
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
    (m: Message, after?: () => void) => {
      const root = () => messageRoot(m.id);
      if (prefsRef.current.style !== "natural" || !api) {
        read(spokenText(m), m.id, root, false, after);
        return;
      }
      queue.current = [];
      generation.current++;
      const mine = generation.current;
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
          read(text, m.id, undefined, true, after);
          return;
        }
        read(spokenText(m), m.id, root, false, after);
      });
    },
    [api, ask, read, reader, prefsRef],
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

  useReadShortcut(reader, speakingId, messagesRef, tellAnswer, stop);
  useStopOnLeave(stop, sessionId);
  useVoiceUnlock(device, prefs.mode);
  useNotices(prefsRef, say, approval);
  useAutoRead({
    running,
    messages,
    sessionId,
    takeHeard,
    prefsRef,
    said,
    tellAnswer,
    ask,
    say,
    stop,
  });

  return {
    supported,
    // device says the device has voices of its own; gatewayVoices are the gateway's, when it has one.
    device,
    gatewayVoices: gatewayVoicesOf(gatewayOn, tts),
    // local says the gateway offers local voices and this browser can run them.
    local: localOn,
    prefs,
    setPrefs,
    voices: voicesFor(voices, lang()),
    allVoices: voices,
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
