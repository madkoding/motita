// Speech input: what can be decided without a microphone, so it can be tested without one.
//
// The browser records, the gateway forwards the recording to the configured transcription
// endpoint (POST /v1/voice/transcribe) and the text lands in the composer, editable, never sent on
// its own. Hands-free mode listens with a level detector (no model, no WebAssembly: the page's
// content policy allows neither) and sends each spoken phrase to the same endpoint, which says
// whether it opened with one of the configured wake phrases.
import type { Api } from "./prApi";

// What GET /v1/voice answers.
export interface VoiceInfo {
  enabled: boolean;
  wake_phrases: string[];
  max_audio_bytes: number;
}

// What POST /v1/voice/transcribe answers.
export interface Transcript {
  text: string;
  wake?: { phrase: string; rest: string };
}

// The two switches of the settings screen, kept in this browser only: a microphone is a property
// of the device, not of the gateway.
export interface VoicePrefs {
  mic: boolean;
  listen: boolean;
}
export const VOICE_KEY = "motita.ui.voice";
export const DEFAULT_VOICE_PREFS: VoicePrefs = { mic: true, listen: false };
// VOICE_PREFS_EVENT tells the page the switches changed in the settings screen.
export const VOICE_PREFS_EVENT = "motita-voice-prefs";

export function loadVoicePrefs(): VoicePrefs {
  try {
    const raw = JSON.parse(localStorage.getItem(VOICE_KEY) || "{}");
    return {
      mic: raw.mic !== false,
      listen: raw.listen === true,
    };
  } catch {
    return { ...DEFAULT_VOICE_PREFS };
  }
}

export function saveVoicePrefs(p: VoicePrefs): void {
  try {
    localStorage.setItem(VOICE_KEY, JSON.stringify(p));
  } catch {
    /* ignore */
  }
  if (typeof window !== "undefined") {
    window.dispatchEvent(new CustomEvent(VOICE_PREFS_EVENT));
  }
}

// Why speech input can or cannot be used here, in the order a person can fix it.
//   insecure     the page is not https or localhost: the browser hides the microphone
//   unsupported  the browser has no microphone API or no MediaRecorder
//   off          the gateway has speech input off (voice.enabled)
//   ready        it can be used
export type VoiceState = "insecure" | "unsupported" | "off" | "ready";

export function voiceState(
  secure: boolean,
  supported: boolean,
  info: VoiceInfo | null,
): VoiceState {
  if (!secure) {
    return "insecure";
  }
  if (!supported) {
    return "unsupported";
  }
  if (!info?.enabled) {
    return "off";
  }
  return "ready";
}

// listenState is the same answer for hands-free mode, which also needs a phrase to listen for.
export function listenState(
  state: VoiceState,
  info: VoiceInfo | null,
): VoiceState | "no-phrases" {
  if (state !== "ready") {
    return state;
  }
  if (!info?.wake_phrases.length) {
    return "no-phrases";
  }
  return "ready";
}

// browserSupport reads what the current page may use.
export function browserSupport(): { secure: boolean; supported: boolean } {
  const g = globalThis as {
    isSecureContext?: boolean;
    navigator?: { mediaDevices?: { getUserMedia?: unknown } };
    MediaRecorder?: unknown;
  };
  return {
    secure: g.isSecureContext === true,
    supported:
      typeof g.navigator?.mediaDevices?.getUserMedia === "function" &&
      typeof g.MediaRecorder === "function",
  };
}

// The recording types, best first. Chrome and Firefox record webm/opus, Safari only mp4; every
// one of them is a format the transcription endpoints read.
const MIME_TYPES = [
  "audio/webm;codecs=opus",
  "audio/webm",
  "audio/mp4",
  "audio/ogg;codecs=opus",
];

// pickMimeType is the first type the recorder supports, or "" to let it choose.
export function pickMimeType(isSupported: (t: string) => boolean): string {
  return MIME_TYPES.find((t) => isSupported(t)) || "";
}

// level is the loudness of one frame of time-domain samples (bytes centred on 128), from 0 to 1.
export function level(samples: Uint8Array): number {
  if (samples.length === 0) {
    return 0;
  }
  let sum = 0;
  for (const s of samples) {
    const v = (s - 128) / 128;
    sum += v * v;
  }
  return Math.sqrt(sum / samples.length);
}

// The thresholds of the level detector. Tuned for a laptop microphone in a quiet room; a phrase is
// what lies between the first loud frame and SILENCE_MS of quiet.
export const GATE = {
  threshold: 0.04,
  silenceMs: 900,
  minMs: 400,
  maxMs: 15000,
};

export type GateEvent = "start" | "end" | "discard" | null;

// SpeechGate turns a stream of levels into phrases: "start" when speech begins, "end" when it is
// followed by enough silence (or runs too long), "discard" when it was too short to be words.
export class SpeechGate {
  private startedAt = -1;
  private lastLoud = -1;
  private readonly g: typeof GATE;
  constructor(g: typeof GATE = GATE) {
    this.g = g;
  }

  get speaking(): boolean {
    return this.startedAt >= 0;
  }

  feed(lvl: number, now: number): GateEvent {
    const loud = lvl >= this.g.threshold;
    if (!this.speaking) {
      if (!loud) {
        return null;
      }
      this.startedAt = now;
      this.lastLoud = now;
      return "start";
    }
    if (loud) {
      this.lastLoud = now;
    }
    if (now - this.startedAt >= this.g.maxMs) {
      return this.close("end");
    }
    if (now - this.lastLoud < this.g.silenceMs) {
      return null;
    }
    if (this.lastLoud - this.startedAt < this.g.minMs) {
      return this.close("discard");
    }
    return this.close("end");
  }

  private close(e: GateEvent): GateEvent {
    this.startedAt = -1;
    this.lastLoud = -1;
    return e;
  }
}

// appendText adds dictated text to what is already in the composer, with one space between.
export function appendText(current: string, addition: string): string {
  const add = addition.trim();
  if (!add) {
    return current;
  }
  if (!current.trim()) {
    return add;
  }
  return current.trimEnd() + " " + add;
}

// errorOf is the message of a refused request: the gateway's {"error": ...} when there is one.
async function errorOf(res: Response): Promise<string> {
  try {
    const body = (await res.json()) as { error?: string };
    if (body.error) {
      return body.error;
    }
  } catch {
    /* not JSON */
  }
  return `HTTP ${res.status}`;
}

export async function fetchVoiceInfo(api: Api): Promise<VoiceInfo | null> {
  const res = await api("/v1/voice");
  if (!res.ok) {
    return null;
  }
  return (await res.json()) as VoiceInfo;
}

// transcribe sends one recording. With wake, the answer also names the wake phrase it opened with.
export async function transcribe(
  api: Api,
  audio: Blob,
  wake: boolean,
): Promise<Transcript> {
  let path = "/v1/voice/transcribe";
  if (wake) {
    path += "?wake=1";
  }
  const res = await api(path, {
    method: "POST",
    headers: { "Content-Type": audio.type || "audio/webm" },
    body: audio,
  });
  if (!res.ok) {
    throw new Error(await errorOf(res));
  }
  return (await res.json()) as Transcript;
}
