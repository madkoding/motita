// Speech input: what can be decided without a microphone, so it can be tested without one.
//
// The browser records, the gateway forwards the recording to the configured transcription
// endpoint (POST /v1/voice/transcribe) and the text lands in the composer, editable, never sent on
// its own. Hands-free mode listens with a level detector (no model, no WebAssembly: the page's
// content policy allows neither) and sends each spoken phrase to the same endpoint, which says
// whether it opened with one of the configured wake phrases.
import type { Api } from "./prApi";

// The voice block as the gateway's configuration file has it. The key is never sent back: only
// whether there is one.
export interface VoiceSaved {
  enabled: boolean;
  // base_url as written ("" inherits the language model's); endpoint is where recordings go,
  // resolved, and "" when there is nowhere yet.
  base_url: string;
  endpoint: string;
  inherits: boolean;
  provider: string;
  model: string;
  has_key: boolean;
  wake_phrases: string[];
}

// What GET and PUT /v1/voice answer. An older gateway sends only the first three.
export interface VoiceInfo {
  enabled: boolean;
  wake_phrases: string[];
  max_audio_bytes: number;
  can_save?: boolean;
  saved?: VoiceSaved;
  restart_needed?: boolean;
  // tts is the gateway's voice, for reading answers aloud. An older gateway sends none.
  tts?: { enabled: boolean; voices: string[]; max_chars: number };
  // local says the gateway offers local voices to download and run in the browser.
  local?: boolean;
}

// What PUT /v1/voice takes. Turning it on, or a new base_url, needs confirm.
export interface VoiceChange {
  enabled: boolean;
  confirm?: boolean;
  base_url?: string;
  model?: string;
  api_key?: string;
  wake_phrases?: string[];
}

// The transcription services a person is most likely to have, to fill the form in one tap. A
// local whisper.cpp server is typed by hand: the page carries no plain-http address of its own.
export const VOICE_PRESETS = [
  {
    id: "openai",
    label: "OpenAI",
    base_url: "https://api.openai.com/v1",
    model: "whisper-1",
  },
  {
    id: "groq",
    label: "Groq",
    base_url: "https://api.groq.com/openai/v1",
    model: "whisper-large-v3-turbo",
  },
] as const;

// parsePhrases reads wake phrases typed one per comma or line, without blanks or repeats.
export function parsePhrases(text: string): string[] {
  const out: string[] = [];
  for (const p of text.split(/[,\n]/)) {
    const v = p.trim();
    if (v && !out.some((o) => o.toLowerCase() === v.toLowerCase())) {
      out.push(v);
    }
  }
  return out;
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
  // deviceId is the microphone chosen on this device; "" is the system's default.
  deviceId: string;
}
export const VOICE_KEY = "motita.ui.voice";
export const DEFAULT_VOICE_PREFS: VoicePrefs = {
  mic: true,
  listen: false,
  deviceId: "",
};
// VOICE_PREFS_EVENT tells the page the switches changed in the settings screen.
export const VOICE_PREFS_EVENT = "motita-voice-prefs";

export function loadVoicePrefs(): VoicePrefs {
  try {
    const raw = JSON.parse(localStorage.getItem(VOICE_KEY) || "{}");
    let deviceId = "";
    if (typeof raw.deviceId === "string") {
      deviceId = raw.deviceId;
    }
    return { mic: raw.mic !== false, listen: raw.listen === true, deviceId };
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

// audioConstraints asks for the chosen microphone, or the default one when none was chosen.
export function audioConstraints(deviceId: string): MediaStreamConstraints {
  if (!deviceId) {
    return { audio: true };
  }
  return { audio: { deviceId: { exact: deviceId } } };
}

// isMissingDevice says getUserMedia failed because the chosen microphone is not there any more
// (unplugged, or the browser renamed its id), which falls back to the default one.
export function isMissingDevice(e: unknown): boolean {
  const name = (e as { name?: string } | null)?.name;
  return name === "OverconstrainedError" || name === "NotFoundError";
}

// One microphone of the picker.
export interface MicOption {
  deviceId: string;
  label: string;
}

// micOptions are the microphones of a device list. Without permission the browser hides their
// names (and may list one entry with an empty id), so each one is numbered instead.
export function micOptions(
  devices: { kind: string; deviceId: string; label: string }[],
  unnamed: (n: number) => string,
): MicOption[] {
  const out: MicOption[] = [];
  for (const d of devices) {
    if (
      d.kind !== "audioinput" ||
      !d.deviceId ||
      d.deviceId === "default" ||
      out.some((o) => o.deviceId === d.deviceId)
    ) {
      continue;
    }
    out.push({
      deviceId: d.deviceId,
      label: d.label || unnamed(out.length + 1),
    });
  }
  return out;
}

// micNamesHidden says the browser listed the microphones without their names: it has not been
// allowed to use one yet.
export function micNamesHidden(
  devices: { kind: string; label: string }[],
): boolean {
  const mics = devices.filter((d) => d.kind === "audioinput");
  return mics.length === 0 || mics.every((d) => !d.label);
}

// What the browser answers about the microphone for this site.
//   granted  the page may record: the voice settings can be used
//   prompt   it has not been asked yet (or the browser does not say): the page offers to ask
//   denied   it was refused: only the site's settings in the browser can allow it again
export type MicPermission = "granted" | "prompt" | "denied";

// The parts of a PermissionStatus the settings listen to.
export interface PermissionStatusLike {
  state: string;
  addEventListener?: (t: string, f: () => void) => void;
  removeEventListener?: (t: string, f: () => void) => void;
}

export interface MicNavigatorLike {
  permissions?: {
    query?: (d: { name: string }) => Promise<PermissionStatusLike>;
  };
  mediaDevices?: {
    enumerateDevices?: () => Promise<{ kind: string; label: string }[]>;
  };
}

// micPermissionOf reads a PermissionStatus state; anything unknown counts as not asked yet.
export function micPermissionOf(state: string): MicPermission {
  if (state === "granted" || state === "denied") {
    return state;
  }
  return "prompt";
}

// queryMicPermission asks the Permissions API, and returns its status to listen for changes.
// Where the browser cannot say (older Firefox and Safari throw on "microphone"), the microphone
// names stand in: the browser shows them only once the page was allowed to record.
export async function queryMicPermission(
  nav: MicNavigatorLike | undefined,
): Promise<{ state: MicPermission; status?: PermissionStatusLike }> {
  try {
    const status = await nav?.permissions?.query?.({ name: "microphone" });
    if (status) {
      return { state: micPermissionOf(status.state), status };
    }
  } catch {
    // Not a permission this browser names: fall back to the device list.
  }
  try {
    const devices = (await nav?.mediaDevices?.enumerateDevices?.()) ?? [];
    return { state: micNamesHidden(devices) ? "prompt" : "granted" };
  } catch {
    return { state: "prompt" };
  }
}

// isPermissionRefused says getUserMedia failed because the person or the browser said no, not
// because there is no microphone.
export function isPermissionRefused(e: unknown): boolean {
  const name = (e as { name?: string } | null)?.name;
  return name === "NotAllowedError" || name === "SecurityError";
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

// putVoice changes speech input on the gateway, saved in its configuration.
export async function putVoice(
  api: Api,
  change: VoiceChange,
): Promise<VoiceInfo> {
  const res = await api("/v1/voice", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(change),
  });
  if (!res.ok) {
    throw new Error(await errorOf(res));
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
