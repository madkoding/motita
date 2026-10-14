import { useCallback, useEffect, useState } from "preact/hooks";
import { InsecureWarning } from "./InsecureWarning";
import { lang, t, tf } from "./i18n";
import { choose } from "./choose";
import {
  type MicPermission,
  type PermissionStatusLike,
  type VoiceChange,
  type VoiceInfo,
  type VoicePrefs,
  type VoiceState,
  VOICE_PRESETS,
  isPermissionRefused,
  micNamesHidden,
  micOptions,
  micPermissionOf,
  parsePhrases,
  putVoice,
  queryMicPermission,
} from "./voice";
import type { Api } from "./gitApi";
import { errorMessage } from "./errors";
import {
  LENGTHS,
  MEDIUM_CHARS,
  RATES,
  SHORT_CHARS,
  type SpeakMode,
  type SpeakStyle,
  type SpeechEngine,
  type SpeechPrefs,
  type VoiceLike,
} from "./speech";
import {
  Row,
  Segmented,
  inputCls,
  primaryBtn,
  smallBtn,
} from "./SettingsControls";

export interface VoiceSettingsProps {
  state: VoiceState;
  hands: VoiceState | "no-phrases";
  phrases: string[];
  prefs: VoicePrefs;
  setPrefs: (p: VoicePrefs) => void;
  // The gateway's side, to turn speech input on from here. Absent on a page without the API.
  info?: VoiceInfo | null;
  api?: Api;
  onInfo?: (i: VoiceInfo) => void;
}

export interface SpeechSettingsProps {
  supported: boolean;
  prefs: SpeechPrefs;
  setPrefs: (p: SpeechPrefs) => void;
  voices: VoiceLike[];
  test: () => void;
  // device says this device has voices of its own; gatewayVoices are the gateway's (voice.tts),
  // empty when it has none.
  device?: boolean;
  gatewayVoices?: string[];
}

// rateLabel writes a speed as people say it: 1×, 1.25×.
function rateLabel(r: number): string {
  return r.toLocaleString(lang()) + "×";
}

// lengthLabel names the longest answer read whole.
function lengthLabel(n: number): string {
  if (n === 0) {
    return t("Whole");
  }
  if (n <= SHORT_CHARS) {
    return t("Short");
  }
  if (n <= MEDIUM_CHARS) {
    return t("Medium");
  }
  return t("Long");
}

// SpeechSettings is reading answers aloud: when, who speaks (the device, or the gateway's voice
// when it has one), which voice, how fast and how much. With the device's voices it needs neither
// the microphone nor the gateway, so it works on any address.
export function SpeechSettings({
  supported,
  prefs,
  setPrefs,
  voices,
  test,
  device = true,
  gatewayVoices = [],
}: SpeechSettingsProps) {
  const set = useCallback(
    (patch: Partial<SpeechPrefs>) => setPrefs({ ...prefs, ...patch }),
    [prefs, setPrefs],
  );
  // Handed to Segmented, so they keep their identity between renders.
  const setMode = useCallback((mode: SpeakMode) => set({ mode }), [set]);
  const setStyle = useCallback((style: SpeakStyle) => set({ style }), [set]);
  const setEngine = useCallback(
    (engine: SpeechEngine) => set({ engine }),
    [set],
  );
  const hasGateway = gatewayVoices.length > 0;
  // The gateway speaks when it is chosen, or when the device has no voices of its own.
  const gateway = hasGateway && (prefs.engine === "gateway" || !device);
  const setRate = useCallback((r: string) => set({ rate: Number(r) }), [set]);
  const setLength = useCallback(
    (n: string) => set({ maxChars: Number(n) }),
    [set],
  );
  const off = !supported;
  return (
    <section class="settings-group" aria-labelledby="settings-speech-h">
      <h3 id="settings-speech-h" class="settings-group-title">
        {t("Read aloud")}
      </h3>
      <div class="settings-card" data-testid="speech-setting">
        <Row
          id="settings-speak"
          label={t("Read answers aloud")}
          stack
          hint={choose(
            off,
            t("This browser cannot read aloud."),
            t("Alt+L or 🔊 on an answer reads it or stops."),
          )}
        >
          <Segmented<SpeakMode>
            name="settings-speak"
            labelledBy="settings-speak"
            value={prefs.mode}
            disabled={off}
            onChange={setMode}
            options={[
              { value: "off", label: t("Off") },
              { value: "voice", label: t("When I spoke") },
              { value: "always", label: t("Always") },
            ]}
          />
        </Row>
        <Row
          id="settings-speak-style"
          label={t("What it says")}
          stack
          hint={choose(
            prefs.style === "natural",
            t(
              "Natural: the session's model says what the agent is doing while it works, and the gist of the answer at the end. Each line is a short extra call to the model.",
            ),
            t("Word for word: the answer as it is written."),
          )}
        >
          <Segmented<SpeakStyle>
            name="settings-speak-style"
            labelledBy="settings-speak-style"
            value={prefs.style}
            disabled={off}
            onChange={setStyle}
            options={[
              { value: "literal", label: t("Word for word") },
              { value: "natural", label: t("Natural") },
            ]}
          />
        </Row>
        {hasGateway && device && (
          <Row
            id="settings-speech-engine"
            label={t("Who speaks")}
            stack
            hint={choose(
              gateway,
              t(
                "The gateway's voice: the same on every device. The text of each answer read goes to the speech service.",
              ),
              t("The voices of this device: nothing leaves the browser."),
            )}
          >
            <Segmented<SpeechEngine>
              name="settings-speech-engine"
              labelledBy="settings-speech-engine"
              value={prefs.engine}
              onChange={setEngine}
              options={[
                { value: "device", label: t("This device") },
                { value: "gateway", label: t("The gateway") },
              ]}
            />
          </Row>
        )}
        {gateway ? (
          <Row id="settings-speech-voice" label={t("Voice")}>
            <select
              class={inputCls + " max-w-[220px]"}
              aria-labelledby="settings-speech-voice"
              value={prefs.gatewayVoice}
              onChange={(e) =>
                set({ gatewayVoice: (e.target as HTMLSelectElement).value })
              }
            >
              <option value="">{t("Gateway default")}</option>
              {gatewayVoices.map((v) => (
                <option key={v} value={v}>
                  {v}
                </option>
              ))}
            </select>
          </Row>
        ) : (
          <Row
            id="settings-speech-voice"
            label={t("Voice")}
            hint={choose(
              !off && voices.length === 0,
              t("This device has no voice for the interface language."),
              undefined,
            )}
          >
            <select
              class={inputCls + " max-w-[220px]"}
              aria-labelledby="settings-speech-voice"
              disabled={off}
              value={prefs.voiceURI}
              onChange={(e) =>
                set({ voiceURI: (e.target as HTMLSelectElement).value })
              }
            >
              <option value="">{t("Device default")}</option>
              {voices.map((v) => (
                <option key={v.voiceURI} value={v.voiceURI}>
                  {v.name}
                </option>
              ))}
            </select>
          </Row>
        )}
        <Row id="settings-speech-rate" label={t("Speed")} stack>
          <Segmented<string>
            name="settings-speech-rate"
            labelledBy="settings-speech-rate"
            value={String(prefs.rate)}
            disabled={off}
            onChange={setRate}
            options={RATES.map((r) => ({
              value: String(r),
              label: rateLabel(r),
            }))}
          />
        </Row>
        <Row
          id="settings-speech-length"
          label={t("Length")}
          stack
          hint={t("A longer answer is read up to its first paragraph.")}
        >
          <Segmented<string>
            name="settings-speech-length"
            labelledBy="settings-speech-length"
            value={String(prefs.maxChars)}
            disabled={off}
            onChange={setLength}
            options={LENGTHS.map((n) => ({
              value: String(n),
              label: lengthLabel(n),
            }))}
          />
        </Row>
        <Row id="settings-speech-test" label={t("Try the voice")}>
          <button
            type="button"
            class={smallBtn}
            disabled={off}
            onClick={test}
            data-testid="speech-test"
          >
            {t("Try")}
          </button>
        </Row>
      </div>
    </section>
  );
}

// voiceBlocker explains why speech input cannot be used, or "" when it can. An insecure page is
// not one of them: that one is the InsecureWarning pill (see voiceWarning).
function voiceBlocker(state: string, canSave: boolean): string {
  if (state === "unsupported") {
    return t("This browser cannot record audio.");
  }
  if (state === "off") {
    return choose(
      canSave,
      t("Turn on speech input above first."),
      t("Off on the gateway: set voice.enabled in the configuration."),
    );
  }
  if (state === "no-phrases") {
    return choose(
      canSave,
      t("Add a wake phrase above first."),
      t("Add voice.wake_phrases to the configuration first."),
    );
  }
  return "";
}

// The guide section that says how to give the page a secure address for the microphone.
const VOICE_GUIDE =
  "https://github.com/madkoding/motita/blob/main/docs/REFERENCE.md#voice-input";

// voiceWarning is the pill for a voice switch the browser hides on an insecure page.
function voiceWarning(state: string) {
  if (state !== "insecure") {
    return undefined;
  }
  return (
    <InsecureWarning
      text={t(
        "Your browser blocks the microphone because Motita was opened without a secure connection.",
      )}
      guide={VOICE_GUIDE}
    />
  );
}

// voiceHint is the hint under a voice switch: none on an insecure page (the pill says it), why
// the switch cannot be used, or what it does.
function voiceHint(
  state: string,
  usable: string,
  canSave: boolean,
): string | undefined {
  if (state === "insecure") {
    return undefined;
  }
  return voiceBlocker(state, canSave) || usable;
}

// VoiceSwitch is one switch of the speech section, greyed out while it cannot be used.
function VoiceSwitch({
  id,
  usable,
  checked,
  onChange,
}: {
  id: string;
  usable: boolean;
  checked: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <label class="settings-switch-hit">
      <input
        type="checkbox"
        role="switch"
        class="settings-switch"
        aria-labelledby={id}
        disabled={!usable}
        checked={usable && checked}
        onChange={(e) => onChange((e.target as HTMLInputElement).checked)}
      />
    </label>
  );
}

// serviceLines say where the recordings will go, before speech input is turned on or moved.
function serviceLines(endpoint: string): string[] {
  const lines = [
    t("The recording is not stored, by the gateway or by this page."),
    t(
      "A key typed here is kept beside the configuration and never shown again.",
    ),
  ];
  if (!endpoint) {
    return [
      t("This provider has no transcription service of its own: name one."),
      ...lines,
    ];
  }
  return [
    tf(
      "What the microphone hears is sent to {endpoint} to be turned into text.",
      {
        endpoint,
      },
    ),
    ...lines,
  ];
}

interface ServiceForm {
  base_url: string;
  model: string;
  api_key: string;
}

// serviceChange is what the form changes: only the fields that differ from the file, and the key
// only when one was typed.
function serviceChange(
  saved: VoiceInfo["saved"],
  form: ServiceForm,
): VoiceChange {
  const change: VoiceChange = { enabled: true, confirm: true };
  const base = form.base_url.trim();
  const model = form.model.trim();
  const key = form.api_key.trim();
  if (base !== (saved?.base_url ?? "")) {
    change.base_url = base;
  }
  if (model && model !== saved?.model) {
    change.model = model;
  }
  if (key) {
    change.api_key = key;
  }
  return change;
}

// VoiceGateway turns speech input on or off on the gateway, picks the transcription service and
// edits the wake phrases, all saved in its configuration. Turning it on, or moving it, first says
// where the recordings go and waits for a yes.
function VoiceGateway({
  api,
  info,
  onInfo,
}: {
  api: Api;
  info: VoiceInfo;
  onInfo: (i: VoiceInfo) => void;
}) {
  const saved = info.saved;
  const [editing, setEditing] = useState(false);
  const [form, setForm] = useState<ServiceForm>({
    base_url: "",
    model: "",
    api_key: "",
  });
  const savedPhrases = (saved?.wake_phrases ?? []).join(", ");
  const [phrases, setPhrases] = useState(savedPhrases);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  useEffect(() => setPhrases(savedPhrases), [savedPhrases]);
  if (!saved) {
    return null;
  }
  const save = async (change: VoiceChange) => {
    setBusy(true);
    setErr("");
    try {
      onInfo(await putVoice(api, change));
      setEditing(false);
    } catch (e) {
      setErr(errorMessage(e));
    }
    setBusy(false);
  };
  const openForm = () => {
    setErr("");
    setForm({ base_url: saved.base_url, model: saved.model, api_key: "" });
    setEditing(true);
  };
  const target = form.base_url.trim() || saved.endpoint;
  const field = (k: keyof ServiceForm) => (e: Event) =>
    setForm({ ...form, [k]: (e.target as HTMLInputElement).value });
  let hint = t("Off. Turn it on to dictate and to use the wake phrase.");
  if (saved.enabled) {
    hint = tf("Recordings go to {endpoint} ({model}).", {
      endpoint: saved.endpoint,
      model: saved.model,
    });
    if (saved.inherits) {
      hint += " " + t("With the language model's own key.");
    }
  }
  return (
    <>
      <Row id="settings-voice-on" label={t("Speech input")} hint={hint}>
        <label class="settings-switch-hit">
          <input
            type="checkbox"
            role="switch"
            class="settings-switch"
            aria-labelledby="settings-voice-on"
            data-testid="voice-gateway-switch"
            disabled={busy}
            checked={saved.enabled || editing}
            onChange={(e) => {
              if ((e.target as HTMLInputElement).checked) {
                openForm();
              } else if (editing && !saved.enabled) {
                setEditing(false);
              } else {
                void save({ enabled: false });
              }
            }}
          />
        </label>
      </Row>
      {saved.enabled && !editing && (
        <Row
          id="settings-voice-service"
          label={t("Transcription service")}
          hint={saved.model}
        >
          <button
            type="button"
            class={smallBtn}
            data-testid="voice-service-change"
            onClick={openForm}
          >
            {t("Change")}
          </button>
        </Row>
      )}
      {editing && (
        <div
          role="group"
          aria-labelledby="settings-voice-confirm"
          class="settings-confirm"
          data-testid="voice-confirm"
        >
          <div
            id="settings-voice-confirm"
            class="text-sm font-semibold text-[#e8e8ea]"
          >
            {choose(
              saved.enabled,
              t("Change the transcription service?"),
              t("Turn on speech input?"),
            )}
          </div>
          <div class="flex flex-wrap gap-2">
            {VOICE_PRESETS.map((p) => (
              <button
                key={p.id}
                type="button"
                class={smallBtn}
                onClick={() =>
                  setForm({ ...form, base_url: p.base_url, model: p.model })
                }
              >
                {p.label}
              </button>
            ))}
          </div>
          <label class="llm-label">
            <span>{t("Endpoint")}</span>
            <input
              class={inputCls + " font-mono"}
              value={form.base_url}
              placeholder={choose(
                saved.endpoint,
                saved.endpoint,
                "https://api.openai.com/v1",
              )}
              autocapitalize="off"
              spellcheck={false}
              data-testid="voice-base-url"
              onInput={field("base_url")}
            />
          </label>
          <label class="llm-label">
            <span>{t("Model")}</span>
            <input
              class={inputCls + " font-mono"}
              value={form.model}
              placeholder="whisper-1"
              autocapitalize="off"
              spellcheck={false}
              data-testid="voice-model"
              onInput={field("model")}
            />
          </label>
          <label class="llm-label">
            <span>{t("API key")}</span>
            <input
              class={inputCls + " font-mono"}
              type="password"
              value={form.api_key}
              placeholder={choose(
                saved.has_key,
                t("Saved. Paste a new one to replace it"),
                t("Not needed for a local server"),
              )}
              autocomplete="off"
              autocapitalize="off"
              spellcheck={false}
              data-testid="voice-api-key"
              onInput={field("api_key")}
            />
          </label>
          <ul class="settings-confirm-lines">
            {serviceLines(target).map((l) => (
              <li key={l}>{l}</li>
            ))}
          </ul>
          <div class="flex justify-end gap-2">
            <button
              type="button"
              class={smallBtn}
              disabled={busy}
              onClick={() => setEditing(false)}
            >
              {t("Cancel")}
            </button>
            <button
              type="button"
              class={primaryBtn}
              disabled={busy || !target}
              data-testid="voice-confirm-yes"
              onClick={() => void save(serviceChange(saved, form))}
            >
              {choose(saved.enabled, t("Save"), t("Turn on"))}
            </button>
          </div>
        </div>
      )}
      <Row
        id="settings-voice-phrases"
        label={t("Wake phrases")}
        stack
        hint={t(
          "Separated by commas. Listening hands-free starts a message when it hears one.",
        )}
      >
        <div class="flex gap-2 w-full">
          <input
            class={inputCls + " flex-1"}
            value={phrases}
            placeholder={t("hey motita")}
            aria-labelledby="settings-voice-phrases"
            data-testid="voice-phrases"
            onInput={(e) => setPhrases((e.target as HTMLInputElement).value)}
          />
          <button
            type="button"
            class={smallBtn}
            disabled={busy || phrases === savedPhrases}
            data-testid="voice-phrases-save"
            onClick={() =>
              void save({
                enabled: saved.enabled,
                confirm: saved.enabled,
                wake_phrases: parsePhrases(phrases),
              })
            }
          >
            {t("Save")}
          </button>
        </div>
      </Row>
      {info.restart_needed && (
        <p
          class="settings-card-note text-xs text-warning"
          role="status"
          data-testid="voice-restart"
        >
          {t(
            "Saved. Applies when the gateway restarts: run motita gateway stop, then motita gateway start.",
          )}
        </p>
      )}
      {err && (
        <p class="settings-card-note text-xs text-danger" role="alert">
          {err}
        </p>
      )}
    </>
  );
}

type MediaDevicesLike = {
  enumerateDevices?: () => Promise<
    { kind: string; deviceId: string; label: string }[]
  >;
  getUserMedia?: (c: MediaStreamConstraints) => Promise<MediaStream>;
  addEventListener?: (t: string, f: () => void) => void;
  removeEventListener?: (t: string, f: () => void) => void;
};

// mediaDevices is the page's microphone API, or undefined where there is none (an insecure page).
function mediaDevices(): MediaDevicesLike | undefined {
  return (globalThis.navigator as { mediaDevices?: MediaDevicesLike })
    ?.mediaDevices;
}

// micHint explains the microphone picker for the state it is in.
function micHint(gone: boolean, hidden: boolean): string {
  if (gone) {
    return t(
      "The chosen microphone is not connected: the default one is used.",
    );
  }
  if (hidden) {
    return t("Allow the microphone to see the names of the ones connected.");
  }
  return t("Used for dictation and the wake phrase, on this device only.");
}

// MicPicker chooses the microphone this device records with. It is only usable once the browser
// allowed the microphone, which is also when it shows their names.
function MicPicker({
  usable,
  deviceId,
  onPick,
}: {
  usable: boolean;
  deviceId: string;
  onPick: (deviceId: string) => void;
}) {
  const [devices, setDevices] = useState<
    { kind: string; deviceId: string; label: string }[]
  >([]);
  const read = useCallback(async () => {
    const md = mediaDevices();
    if (!md?.enumerateDevices) {
      return;
    }
    try {
      setDevices(await md.enumerateDevices());
    } catch {
      setDevices([]);
    }
  }, []);
  useEffect(() => {
    if (!usable) {
      return undefined;
    }
    const md = mediaDevices();
    const onChange = () => void read();
    void read();
    md?.addEventListener?.("devicechange", onChange);
    return () => md?.removeEventListener?.("devicechange", onChange);
  }, [usable, read]);
  const options = micOptions(devices, (n) => tf("Microphone {n}", { n }));
  const hidden = micNamesHidden(devices);
  const gone =
    deviceId !== "" && !hidden && !options.some((o) => o.deviceId === deviceId);
  return (
    <Row
      id="settings-mic-device"
      label={t("Microphone")}
      hint={choose(usable, micHint(gone, hidden), undefined)}
    >
      <select
        class={inputCls + " max-w-[220px]"}
        aria-labelledby="settings-mic-device"
        data-testid="mic-device"
        disabled={!usable}
        value={deviceId}
        onChange={(e) => onPick((e.target as HTMLSelectElement).value)}
      >
        <option value="">{t("System default")}</option>
        {gone && <option value={deviceId}>{t("Not connected")}</option>}
        {options.map((o) => (
          <option key={o.deviceId} value={o.deviceId}>
            {o.label}
          </option>
        ))}
      </select>
    </Row>
  );
}

// useMicPermission follows the browser's answer about the microphone, and asks for it. It only
// asks the browser where the page can record at all (askable). null is before the browser said.
function useMicPermission(askable: boolean) {
  const [permission, setPermission] = useState<MicPermission | null>(null);
  const [missing, setMissing] = useState(false);
  useEffect(() => {
    if (!askable) {
      return undefined;
    }
    let live = true;
    let status: PermissionStatusLike | undefined;
    const onChange = () => {
      if (status) {
        setPermission(micPermissionOf(status.state));
      }
    };
    void queryMicPermission(globalThis.navigator as never).then((r) => {
      if (!live) {
        return;
      }
      status = r.status;
      setPermission(r.state);
      status?.addEventListener?.("change", onChange);
    });
    return () => {
      live = false;
      status?.removeEventListener?.("change", onChange);
    };
  }, [askable]);
  const ask = useCallback(async () => {
    const md = mediaDevices();
    if (!md?.getUserMedia) {
      return;
    }
    try {
      const stream = await md.getUserMedia({ audio: true });
      for (const track of stream.getTracks()) {
        track.stop();
      }
      setMissing(false);
      setPermission("granted");
    } catch (e) {
      if (isPermissionRefused(e)) {
        setPermission("denied");
      } else {
        setMissing(true);
      }
    }
  }, []);
  return { permission, missing, ask };
}

// permissionHint says why the voice settings wait for the microphone, and what to do.
function permissionHint(permission: MicPermission, missing: boolean): string {
  if (permission === "denied") {
    return t(
      "The browser was not allowed to use the microphone: allow it in this site's settings, then try again.",
    );
  }
  if (missing) {
    return t("No microphone was found on this device.");
  }
  return t(
    "Allow the microphone to use voice. The browser asks once, and you can take it back in this site's settings.",
  );
}

// VoiceSettings are speech input on the gateway, the microphone of this device and the two
// switches, which only work where the browser grants the microphone (https or localhost) and
// the gateway has speech input on. Until the browser allowed the microphone, all of it is greyed
// out under a row that asks for it.
export function VoiceSettings({
  state,
  hands,
  phrases,
  prefs,
  setPrefs,
  info,
  api,
  onInfo,
}: VoiceSettingsProps) {
  const canSave = Boolean(info?.can_save && info.saved && api && onInfo);
  const setDevice = useCallback(
    (deviceId: string) => setPrefs({ ...prefs, deviceId }),
    [prefs, setPrefs],
  );
  const setMic = useCallback(
    (mic: boolean) => setPrefs({ ...prefs, mic }),
    [prefs, setPrefs],
  );
  const setListen = useCallback(
    (listen: boolean) => setPrefs({ ...prefs, listen }),
    [prefs, setPrefs],
  );
  const askable = state !== "insecure" && state !== "unsupported";
  const mic = useMicPermission(askable);
  const allowed = askable && mic.permission === "granted";
  return (
    <section class="settings-group" aria-labelledby="settings-voice-h">
      <h3 id="settings-voice-h" class="settings-group-title">
        {t("Voice")}
      </h3>
      <div class="settings-card" data-testid="voice-setting">
        {askable && mic.permission && !allowed && (
          <div
            class="settings-row settings-mic-permission"
            data-testid="mic-permission"
          >
            <div class="settings-row-text">
              <div id="settings-mic-permission" class="text-sm text-[#e8e8ea]">
                {t("Microphone permission")}
              </div>
              <div
                class={
                  "text-xs mt-0.5 " +
                  choose(
                    mic.permission === "denied",
                    "text-warning",
                    "text-[#7a7a8c]",
                  )
                }
                role="status"
              >
                {permissionHint(mic.permission, mic.missing)}
              </div>
            </div>
            <div class="settings-row-control">
              <button
                type="button"
                class={primaryBtn}
                data-testid="mic-allow"
                aria-describedby="settings-mic-permission"
                onClick={() => void mic.ask()}
              >
                {choose(
                  mic.permission === "denied",
                  t("Try again"),
                  t("Allow microphone"),
                )}
              </button>
            </div>
          </div>
        )}
        <fieldset
          class="settings-voice-lock"
          disabled={!allowed}
          data-testid="voice-locked"
        >
          {canSave && info && api && onInfo && (
            <VoiceGateway api={api} info={info} onInfo={onInfo} />
          )}
          <MicPicker
            usable={allowed}
            deviceId={prefs.deviceId}
            onPick={setDevice}
          />
          <Row
            id="settings-mic"
            label={t("Microphone button")}
            warning={voiceWarning(state)}
            hint={voiceHint(
              state,
              t(
                "Dictate into the message box. You read and edit the text before sending it.",
              ),
              canSave,
            )}
          >
            <VoiceSwitch
              id="settings-mic"
              usable={allowed && state === "ready"}
              checked={prefs.mic}
              onChange={setMic}
            />
          </Row>
          <Row
            id="settings-listen"
            label={t("Listen for the wake phrase")}
            warning={voiceWarning(hands)}
            hint={voiceHint(
              hands,
              tf(
                "Only while this tab is open. What you say is sent to the transcription service. Say: {phrases}",
                { phrases: phrases.map((p) => `“${p}”`).join(", ") },
              ),
              canSave,
            )}
          >
            <VoiceSwitch
              id="settings-listen"
              usable={allowed && hands === "ready"}
              checked={prefs.listen}
              onChange={setListen}
            />
          </Row>
        </fieldset>
      </div>
    </section>
  );
}
