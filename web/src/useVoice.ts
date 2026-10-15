// useVoice is speech input for the composer: a dictation button, and the hands-free mode that
// listens for a wake phrase while this tab is open. See voice.ts for the parts that are decided
// without a microphone.
import type { RefObject } from "preact";
import type { Dispatch, StateUpdater } from "preact/hooks";
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "preact/hooks";
import { t, tf } from "./i18n";
import type { Api } from "./prApi";
import type { SetToast } from "./appTypes";
import { TASK_INPUT_ID } from "./appConst";
import { errorMessage } from "./errors";
import { choose } from "./choose";
import {
  type Transcript,
  type VoiceInfo,
  type VoicePrefs,
  VOICE_PREFS_EVENT,
  SpeechGate,
  appendText,
  audioConstraints,
  browserSupport,
  fetchVoiceInfo,
  isMissingDevice,
  listenEngine,
  listenState,
  level,
  loadVoicePrefs,
  pickMimeType,
  saveVoicePrefs,
  transcribe,
  voiceState,
} from "./voice";
import { matchWake } from "./wakeMatch";
import { hearLocal, localListenSupported, warmListener } from "./localListen";
import { stopSpeaking, voiceBusy } from "./speech";

// The longest dictation, after which it stops on its own: a button left on by mistake must not
// record a meeting.
const MAX_DICTATION_MS = 120_000;
// How often the hands-free mode reads the level, and how long it records silence before it
// starts a fresh recording (so a phrase is never sent with minutes of quiet in front of it).
const FRAME_MS = 50;
const IDLE_RESTART_MS = 4000;
// How long conversation mode waits for the person to start speaking before it stops listening.
const NOTHING_HEARD_MS = 8000;

// NO_PHRASES stands for the wake phrases before the gateway listed them: one array, so the
// listener is not made again on every render.
const NO_PHRASES: readonly string[] = [];

export type VoicePhase = "idle" | "recording" | "transcribing";

interface Deps {
  api: Api;
  authed: boolean;
  setInput: Dispatch<StateUpdater<string>>;
  setToast: SetToast;
}

// recorderFor starts a recorder on a stream and resolves the recording when it stops.
function recorderFor(stream: MediaStream): {
  recorder: MediaRecorder;
  done: Promise<Blob>;
} {
  const mimeType = pickMimeType((m) => MediaRecorder.isTypeSupported(m));
  const recorder = new MediaRecorder(
    stream,
    choose(mimeType, { mimeType }, undefined),
  );
  const chunks: Blob[] = [];
  const done = new Promise<Blob>((resolve) => {
    recorder.ondataavailable = (e) => {
      if (e.data.size > 0) {
        chunks.push(e.data);
      }
    };
    recorder.onstop = () =>
      resolve(new Blob(chunks, { type: recorder.mimeType || mimeType }));
  });
  recorder.start();
  return { recorder, done };
}

function stopTracks(stream: MediaStream) {
  for (const track of stream.getTracks()) {
    track.stop();
  }
}

// openMic opens the microphone chosen in the settings. One that is not there any more (unplugged,
// or its id changed) falls back to the default one rather than leaving the person without voice.
export async function openMic(deviceId: string): Promise<MediaStream> {
  try {
    return await navigator.mediaDevices.getUserMedia(
      audioConstraints(deviceId),
    );
  } catch (e) {
    if (!deviceId || !isMissingDevice(e)) {
      throw e;
    }
    return navigator.mediaDevices.getUserMedia(audioConstraints(""));
  }
}

function focusComposer() {
  document.getElementById(TASK_INPUT_ID)?.focus();
}

// Hear turns one recording into text, with the listener chosen; with wake, the answer also names
// the wake phrase it opened with. ready, called as the microphone opens, loads a local model while
// the person speaks.
interface Hear {
  (audio: Blob, wake: boolean): Promise<Transcript>;
  ready(): void;
}

// What the microphone hooks share: the state of the composer's voice, whether the person spoke,
// the dictation running and the phrase conversation mode listens for.
interface MicRefs {
  hear: Hear;
  deviceId: string;
  fail: (e: unknown) => void;
  setPhase: Dispatch<StateUpdater<VoicePhase>>;
  phaseRef: RefObject<VoicePhase>;
  heard: RefObject<boolean>;
  dictation: RefObject<{ recorder: MediaRecorder; timer: number } | null>;
  // listening1 ends the one phrase conversation mode is listening for, when there is one.
  listening1: RefObject<(() => void) | null>;
}

// useDictation is the dictation button: one tap starts, the next one stops and transcribes into
// the composer.
function useDictation({
  hear,
  deviceId,
  fail,
  setInput,
  setPhase,
  phaseRef,
  heard,
  dictation,
  listening1,
}: MicRefs & { setInput: Deps["setInput"] }) {
  return useCallback(async () => {
    // A tap while the conversation listens ends the conversation, not a dictation.
    if (listening1.current) {
      listening1.current();
      return;
    }
    const live = dictation.current;
    if (live) {
      clearTimeout(live.timer);
      live.recorder.stop();
      dictation.current = null;
      return;
    }
    if (phaseRef.current !== "idle") {
      return;
    }
    // The person is about to speak: Motita stops talking over them.
    stopSpeaking();
    hear.ready();
    let stream: MediaStream;
    try {
      stream = await openMic(deviceId);
    } catch (e) {
      fail(e);
      return;
    }
    const { recorder, done } = recorderFor(stream);
    const timer = window.setTimeout(() => {
      if (dictation.current?.recorder === recorder) {
        dictation.current = null;
        recorder.stop();
      }
    }, MAX_DICTATION_MS);
    dictation.current = { recorder, timer };
    setPhase("recording");
    const audio = await done;
    stopTracks(stream);
    setPhase("transcribing");
    try {
      const r = await hear(audio, false);
      setInput((cur) => appendText(cur, r.text));
      heard.current = heard.current || r.text.trim() !== "";
      focusComposer();
    } catch (e) {
      fail(e);
    } finally {
      setPhase("idle");
    }
  }, [hear, deviceId, fail, setInput]);
}

// useListenOnce is conversation mode's listening: one phrase, ended by the silence after it,
// transcribed and handed back (null when nobody spoke, the listening was cancelled, or it
// failed). What it hears counts as spoken.
function useListenOnce({
  hear,
  deviceId,
  fail,
  setPhase,
  phaseRef,
  heard,
  dictation,
  listening1,
}: MicRefs) {
  const listenOnce = useCallback(async (): Promise<string | null> => {
    if (
      phaseRef.current !== "idle" ||
      dictation.current ||
      listening1.current
    ) {
      return null;
    }
    hear.ready();
    let stream: MediaStream;
    try {
      stream = await openMic(deviceId);
    } catch (e) {
      fail(e);
      return null;
    }
    const ctx = new AudioContext();
    const analyser = ctx.createAnalyser();
    analyser.fftSize = 1024;
    ctx.createMediaStreamSource(stream).connect(analyser);
    const frame = new Uint8Array(analyser.fftSize);
    const { recorder, done } = recorderFor(stream);
    setPhase("recording");
    const spoke = await new Promise<boolean>((resolve) => {
      const gate = new SpeechGate();
      const from = Date.now();
      let timer: ReturnType<typeof setInterval> | undefined;
      const finish = (ok: boolean) => {
        clearInterval(timer);
        listening1.current = null;
        resolve(ok);
      };
      listening1.current = () => finish(false);
      timer = setInterval(() => {
        analyser.getByteTimeDomainData(frame);
        const now = Date.now();
        const ev = gate.feed(level(frame), now);
        if (ev === "end") {
          finish(true);
          return;
        }
        if (!gate.speaking && now - from > NOTHING_HEARD_MS) {
          finish(false);
        }
      }, FRAME_MS);
    });
    recorder.stop();
    const audio = await done;
    stopTracks(stream);
    void ctx.close();
    if (!spoke) {
      setPhase("idle");
      return null;
    }
    setPhase("transcribing");
    let text: string | null = null;
    try {
      const said = (await hear(audio, false)).text.trim();
      heard.current = heard.current || said !== "";
      text = said || null;
    } catch (e) {
      fail(e);
    }
    setPhase("idle");
    return text;
  }, [hear, deviceId, fail]);
  const cancelListen = useCallback(() => listening1.current?.(), []);
  return { listenOnce, cancelListen };
}

// useHandsFree listens while the hands-free mode is active: every spoken phrase is transcribed,
// and only one that opens with a wake phrase reaches the composer.
function useHandsFree({
  active,
  hear,
  deviceId,
  fail,
  setInput,
  setToast,
  setListening,
  phaseRef,
  heard,
}: Pick<MicRefs, "hear" | "deviceId" | "fail" | "phaseRef" | "heard"> & {
  active: boolean;
  setInput: Deps["setInput"];
  setToast: SetToast;
  setListening: Dispatch<StateUpdater<boolean>>;
}) {
  useEffect(() => {
    if (!active) {
      return undefined;
    }
    let stopped = false;
    let stream: MediaStream | null = null;
    let ctx: AudioContext | null = null;
    let timer = 0;
    let current: {
      recorder: MediaRecorder;
      done: Promise<Blob>;
      at: number;
    } | null = null;

    const restart = () => {
      if (current && current.recorder.state !== "inactive") {
        current.recorder.stop();
      }
      current = null;
      if (stream) {
        current = { ...recorderFor(stream), at: performance.now() };
      }
    };
    const send = async (audio: Promise<Blob>) => {
      try {
        const r = await hear(await audio, true);
        if (!stopped && r.wake) {
          heard.current = true;
          setInput((cur) => appendText(cur, r.wake?.rest || ""));
          setToast({
            message: tf("Heard “{phrase}”", { phrase: r.wake.phrase }),
            type: "success",
          });
          focusComposer();
        }
      } catch (e) {
        fail(e);
      }
    };

    hear.ready();
    void (async () => {
      try {
        stream = await openMic(deviceId);
      } catch (e) {
        fail(e);
        return;
      }
      if (stopped) {
        stopTracks(stream);
        return;
      }
      ctx = new AudioContext();
      const analyser = ctx.createAnalyser();
      analyser.fftSize = 1024;
      ctx.createMediaStreamSource(stream).connect(analyser);
      const frame = new Uint8Array(analyser.fftSize);
      let gate = new SpeechGate();
      // overheard says the recording holds Motita's own voice: it is thrown away, never sent.
      let overheard = false;
      restart();
      setListening(true);
      timer = window.setInterval(() => {
        if (voiceBusy()) {
          overheard = true;
          return;
        }
        if (overheard) {
          overheard = false;
          gate = new SpeechGate();
          restart();
          return;
        }
        analyser.getByteTimeDomainData(frame);
        const now = performance.now();
        const ev = gate.feed(level(frame), now);
        if (ev === "end" && current && phaseRef.current === "idle") {
          const { done } = current;
          restart();
          void send(done);
          return;
        }
        if (
          ev === "discard" ||
          (!gate.speaking && current && now - current.at > IDLE_RESTART_MS)
        ) {
          restart();
        }
      }, FRAME_MS);
    })();

    return () => {
      stopped = true;
      clearInterval(timer);
      if (current && current.recorder.state !== "inactive") {
        current.recorder.stop();
      }
      if (stream) {
        stopTracks(stream);
      }
      void ctx?.close();
      setListening(false);
    };
  }, [active, hear, deviceId, fail, setInput, setToast]);
}

// useHear is the listener chosen: a model run in this browser, or the transcription service. What
// a local model hears never leaves this device: its wake phrase is looked for here too, among the
// phrases the gateway listed.
function useHear(
  api: Api,
  engine: string,
  listener: string,
  language: string,
  phrases: readonly string[],
): Hear {
  return useMemo(() => {
    if (engine !== "local") {
      return Object.assign((audio: Blob, wake: boolean) => transcribe(api, audio, wake), {
        ready: () => undefined,
      });
    }
    const local = async (audio: Blob, wake: boolean): Promise<Transcript> => {
      const text = await hearLocal(listener, audio, language);
      if (!wake || !text) {
        return { text };
      }
      const found = matchWake(text, phrases);
      return choose(found, { text, wake: found }, { text });
    };
    return Object.assign(local, { ready: () => warmListener(listener) });
  }, [api, engine, listener, language, phrases]);
}

export function useVoice({ api, authed, setInput, setToast }: Deps) {
  const [info, setInfo] = useState<VoiceInfo | null>(null);
  const [prefs, setPrefsState] = useState<VoicePrefs>(loadVoicePrefs);
  const [phase, setPhase] = useState<VoicePhase>("idle");
  const [listening, setListening] = useState(false);
  const support = useMemo(browserSupport, []);
  const engine = listenEngine(prefs, info);
  const runs = useMemo(localListenSupported, []);
  const state = voiceState(support.secure, support.supported, info, {
    engine,
    listener: prefs.listener,
    runs,
  });
  const hands = listenState(state, info);
  const phaseRef = useRef<VoicePhase>("idle");
  phaseRef.current = phase;
  // heard says text spoken by the person reached the composer since the last turn started: the
  // answer to that turn is read aloud in the "when I spoke" mode.
  const heard = useRef(false);
  const takeHeard = useCallback(() => {
    const h = heard.current;
    heard.current = false;
    return h;
  }, []);

  useEffect(() => {
    if (!authed) {
      return;
    }
    fetchVoiceInfo(api)
      .then(setInfo)
      .catch(() => setInfo(null));
  }, [api, authed]);

  useEffect(() => {
    const reload = () => setPrefsState(loadVoicePrefs());
    window.addEventListener(VOICE_PREFS_EVENT, reload);
    return () => window.removeEventListener(VOICE_PREFS_EVENT, reload);
  }, []);

  const setPrefs = useCallback((p: VoicePrefs) => {
    setPrefsState(p);
    saveVoicePrefs(p);
  }, []);

  const fail = useCallback(
    (e: unknown) =>
      setToast({
        message: t("Speech input failed"),
        detail: errorMessage(e),
        type: "error",
      }),
    [setToast],
  );

  // DICTATION: one tap starts, the next one stops and transcribes into the composer.
  const dictation = useRef<{ recorder: MediaRecorder; timer: number } | null>(
    null,
  );
  const deviceId = prefs.deviceId;
  const hear = useHear(api, engine, prefs.listener, prefs.language, info?.wake_phrases ?? NO_PHRASES);
  // listening ends the one phrase conversation mode is listening for, when there is one.
  const listening1 = useRef<(() => void) | null>(null);
  const toggleDictation = useDictation({
    hear,
    deviceId,
    fail,
    setInput,
    setPhase,
    phaseRef,
    heard,
    dictation,
    listening1,
  });

  // CONVERSATION: one phrase, ended by the silence after it, transcribed and handed back.
  const { listenOnce, cancelListen } = useListenOnce({
    hear,
    deviceId,
    fail,
    setPhase,
    phaseRef,
    heard,
    dictation,
    listening1,
  });

  // HANDS-FREE: while the switch is on and this tab is open, every spoken phrase is transcribed,
  // and only one that opens with a wake phrase reaches the composer.
  const active = prefs.listen && hands === "ready" && authed;
  useHandsFree({
    active,
    hear,
    deviceId,
    fail,
    setInput,
    setToast,
    setListening,
    phaseRef,
    heard,
  });

  return {
    info,
    engine,
    // setInfo takes what the settings saved, so the composer follows a change at once.
    setInfo,
    state,
    hands,
    prefs,
    setPrefs,
    phase,
    listening,
    toggleDictation,
    takeHeard,
    listenOnce,
    cancelListen,
    // The dictation button is shown when it can be used and the person has not turned it off.
    showMic: state === "ready" && prefs.mic,
  };
}

export type Voice = ReturnType<typeof useVoice>;
