// useVoice is speech input for the composer: a dictation button, and the hands-free mode that
// listens for a wake phrase while this tab is open. See voice.ts for the parts that are decided
// without a microphone.
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
import {
  type VoiceInfo,
  type VoicePrefs,
  VOICE_PREFS_EVENT,
  SpeechGate,
  appendText,
  browserSupport,
  fetchVoiceInfo,
  listenState,
  level,
  loadVoicePrefs,
  pickMimeType,
  saveVoicePrefs,
  transcribe,
  voiceState,
} from "./voice";

// The longest dictation, after which it stops on its own: a button left on by mistake must not
// record a meeting.
const MAX_DICTATION_MS = 120_000;
// How often the hands-free mode reads the level, and how long it records silence before it
// starts a fresh recording (so a phrase is never sent with minutes of quiet in front of it).
const FRAME_MS = 50;
const IDLE_RESTART_MS = 4000;

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
    mimeType ? { mimeType } : undefined,
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

function focusComposer() {
  document.getElementById(TASK_INPUT_ID)?.focus();
}

export function useVoice({ api, authed, setInput, setToast }: Deps) {
  const [info, setInfo] = useState<VoiceInfo | null>(null);
  const [prefs, setPrefsState] = useState<VoicePrefs>(loadVoicePrefs);
  const [phase, setPhase] = useState<VoicePhase>("idle");
  const [listening, setListening] = useState(false);
  const support = useMemo(browserSupport, []);
  const state = voiceState(support.secure, support.supported, info);
  const hands = listenState(state, info);
  const phaseRef = useRef<VoicePhase>("idle");
  phaseRef.current = phase;

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
  const toggleDictation = useCallback(async () => {
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
    let stream: MediaStream;
    try {
      stream = await navigator.mediaDevices.getUserMedia({ audio: true });
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
      const r = await transcribe(api, audio, false);
      setInput((cur) => appendText(cur, r.text));
      focusComposer();
    } catch (e) {
      fail(e);
    } finally {
      setPhase("idle");
    }
  }, [api, fail, setInput]);

  // HANDS-FREE: while the switch is on and this tab is open, every spoken phrase is transcribed,
  // and only one that opens with a wake phrase reaches the composer.
  const active = prefs.listen && hands === "ready" && authed;
  useEffect(() => {
    if (!active) {
      return;
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
      current = stream
        ? { ...recorderFor(stream), at: performance.now() }
        : null;
    };
    const send = async (audio: Promise<Blob>) => {
      try {
        const r = await transcribe(api, await audio, true);
        if (!stopped && r.wake) {
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

    void (async () => {
      try {
        stream = await navigator.mediaDevices.getUserMedia({ audio: true });
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
      const gate = new SpeechGate();
      restart();
      setListening(true);
      timer = window.setInterval(() => {
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
  }, [active, api, fail, setInput, setToast]);

  return {
    info,
    state,
    hands,
    prefs,
    setPrefs,
    phase,
    listening,
    toggleDictation,
    // The dictation button is shown when it can be used and the person has not turned it off.
    showMic: state === "ready" && prefs.mic,
  };
}

export type Voice = ReturnType<typeof useVoice>;
