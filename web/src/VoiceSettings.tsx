import { useCallback, useEffect, useState } from "preact/hooks";
import type { Dispatch, StateUpdater } from "preact/hooks";
import { InsecureWarning } from "./InsecureWarning";
import { t, tf } from "./i18n";
import { choose } from "./choose";
import {
  type ListenEngine,
  type MicPermission,
  type PermissionStatusLike,
  type VoiceChange,
  type VoiceInfo,
  type VoicePrefs,
  type VoiceState,
  VOICE_PRESETS,
  isPermissionRefused,
  listenEngine,
  micNamesHidden,
  micOptions,
  micPermissionOf,
  parsePhrases,
  putVoice,
  queryMicPermission,
} from "./voice";
import type { Api } from "./gitApi";
import { errorMessage } from "./errors";
import { gpuForced, isMobileDevice, setGpuForced } from "./gpuPolicy";
import { LocalListening } from "./LocalListenSettings";
import {
  Row,
  inputCls,
  primaryBtn,
  selectCls,
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

// voiceBlocker explains why speech input cannot be used, or "" when it can. An insecure page is
// not one of them: that one is the InsecureWarning pill (see voiceWarning).
function voiceBlocker(state: string, canSave: boolean): string {
  if (state === "unsupported") {
    return t("This browser cannot record audio.");
  }
  if (state === "no-model") {
    return t("Download a model to listen with above first.");
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

// MobileGpuRow is the switch to run the local models on WebGPU on a phone, where they use the
// processor by default; the workers already running keep their way until the page is reloaded.
export function MobileGpuRow({ id = "settings-gpu-mobile" }: { id?: string }) {
  const [on, setOn] = useState(gpuForced);
  const change = useCallback((v: boolean) => {
    setGpuForced(v);
    setOn(v);
  }, []);
  return (
    <Row
      id={id}
      label={t("Use WebGPU on this device")}
      hint={t(
        "On a phone the models that listen and speak run on the processor, as its graphics card is too weak for them. Turn this on to try WebGPU anyway; reload the page to apply it.",
      )}
    >
      <VoiceSwitch
        id={id}
        usable
        checked={on}
        onChange={change}
      />
    </Row>
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
function serviceChange(saved: SavedVoice, form: ServiceForm): VoiceChange {
  const change: VoiceChange = { enabled: true, confirm: true };
  const base = form.base_url.trim();
  const model = form.model.trim();
  const key = form.api_key.trim();
  if (base !== saved.base_url) {
    change.base_url = base;
  }
  if (model && model !== saved.model) {
    change.model = model;
  }
  if (key) {
    change.api_key = key;
  }
  return change;
}

type SavedVoice = NonNullable<VoiceInfo["saved"]>;

// gatewayHint says where speech input sends the recordings, or that it is off.
function gatewayHint(saved: SavedVoice): string {
  if (!saved.enabled) {
    return t("Off. Turn it on to dictate and to use the wake phrase.");
  }
  let hint = tf("Recordings go to {endpoint} ({model}).", {
    endpoint: saved.endpoint,
    model: saved.model,
  });
  if (saved.inherits) {
    hint += " " + t("With the language model's own key.");
  }
  return hint;
}

// VoiceGateway turns speech input on or off on the gateway, picks the transcription service and
// edits the wake phrases, all saved in its configuration. Turning it on, or moving it, first says
// where the recordings go and waits for a yes.
function VoiceGateway({
  api,
  info,
  onInfo,
  service,
}: {
  api: Api;
  info: VoiceInfo;
  onInfo: (i: VoiceInfo) => void;
  // service says the transcription service listens: its switch and form are shown.
  service: boolean;
}) {
  // Only drawn with a saved configuration (see gatewayEditor).
  const saved = info.saved as SavedVoice;
  const [editing, setEditing] = useState(false);
  const [form, setForm] = useState<ServiceForm>({
    base_url: "",
    model: "",
    api_key: "",
  });
  const savedPhrases = (saved.wake_phrases ?? []).join(", ");
  const [phrases, setPhrases] = useState(savedPhrases);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  useEffect(() => setPhrases(savedPhrases), [savedPhrases]);
  const save = useCallback(
    async (change: VoiceChange) => {
      setBusy(true);
      setErr("");
      try {
        onInfo(await putVoice(api, change));
        setEditing(false);
      } catch (e) {
        setErr(errorMessage(e));
      }
      setBusy(false);
    },
    [api, onInfo],
  );
  return (
    <>
      {service && (
        <VoiceGatewaySwitch
          saved={saved}
          editing={editing}
          busy={busy}
          setEditing={setEditing}
          setErr={setErr}
          setForm={setForm}
          save={save}
        />
      )}
      {service && editing && (
        <VoiceServiceForm
          saved={saved}
          form={form}
          setForm={setForm}
          busy={busy}
          setEditing={setEditing}
          save={save}
        />
      )}
      <WakePhrases
        enabled={saved.enabled}
        phrases={phrases}
        setPhrases={setPhrases}
        savedPhrases={savedPhrases}
        busy={busy}
        save={save}
      />
      {info.restart_needed && (
        <output
          class="settings-card-note block text-xs text-warning"
          data-testid="voice-restart"
        >
          {t(
            "Saved. Applies when the gateway restarts: run motita gateway stop, then motita gateway start.",
          )}
        </output>
      )}
      {err && (
        <p class="settings-card-note text-xs text-danger" role="alert">
          {err}
        </p>
      )}
    </>
  );
}

// VoiceGatewaySwitch is the switch that turns speech input on the gateway on or off, and the
// transcription service it uses while it is on. Turning it on opens the service form first.
function VoiceGatewaySwitch({
  saved,
  editing,
  busy,
  setEditing,
  setErr,
  setForm,
  save,
}: {
  saved: SavedVoice;
  editing: boolean;
  busy: boolean;
  setEditing: Dispatch<StateUpdater<boolean>>;
  setErr: Dispatch<StateUpdater<string>>;
  setForm: Dispatch<StateUpdater<ServiceForm>>;
  save: (change: VoiceChange) => Promise<void>;
}) {
  const openForm = () => {
    setErr("");
    setForm({ base_url: saved.base_url, model: saved.model, api_key: "" });
    setEditing(true);
  };
  return (
    <>
      <Row
        id="settings-voice-on"
        label={t("Speech input")}
        hint={gatewayHint(saved)}
      >
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
    </>
  );
}

// VoiceServiceForm is the transcription service to turn speech input on with, or to move it to,
// and where the recordings will go, waiting for a yes.
function VoiceServiceForm({
  saved,
  form,
  setForm,
  busy,
  setEditing,
  save,
}: {
  saved: SavedVoice;
  form: ServiceForm;
  setForm: Dispatch<StateUpdater<ServiceForm>>;
  busy: boolean;
  setEditing: Dispatch<StateUpdater<boolean>>;
  save: (change: VoiceChange) => Promise<void>;
}) {
  const target = form.base_url.trim() || saved.endpoint;
  const field = (k: keyof ServiceForm) => (e: Event) =>
    setForm({ ...form, [k]: (e.target as HTMLInputElement).value });
  return (
    <fieldset
      aria-labelledby="settings-voice-confirm"
      class="settings-confirm min-w-0"
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
    </fieldset>
  );
}

// WakePhrases edits the phrases hands-free listening starts a message with.
function WakePhrases({
  enabled,
  phrases,
  setPhrases,
  savedPhrases,
  busy,
  save,
}: {
  enabled: boolean;
  phrases: string;
  setPhrases: Dispatch<StateUpdater<string>>;
  savedPhrases: string;
  busy: boolean;
  save: (change: VoiceChange) => Promise<void>;
}) {
  return (
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
              enabled,
              confirm: enabled,
              wake_phrases: parsePhrases(phrases),
            })
          }
        >
          {t("Save")}
        </button>
      </div>
    </Row>
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
        class={selectCls}
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
    // Only listened to on the status itself.
    const onChange = () =>
      setPermission(micPermissionOf((status as PermissionStatusLike).state));
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

// gatewayEditor is what editing speech input on the gateway needs, or null where this page
// cannot save it.
function gatewayEditor(
  info: VoiceInfo | null | undefined,
  api: Api | undefined,
  onInfo: ((i: VoiceInfo) => void) | undefined,
): { info: VoiceInfo; api: Api; onInfo: (i: VoiceInfo) => void } | null {
  if (info?.can_save && info.saved && api && onInfo) {
    return { info, api, onInfo };
  }
  return null;
}

// MicPermissionRow is the row that asks the browser for the microphone, and says why the voice
// settings wait for it.
function MicPermissionRow({
  permission,
  missing,
  ask,
}: {
  permission: MicPermission;
  missing: boolean;
  ask: () => Promise<void>;
}) {
  return (
    <div
      class="settings-row settings-mic-permission"
      data-testid="mic-permission"
    >
      <div class="settings-row-text">
        <div id="settings-mic-permission" class="text-sm text-[#e8e8ea]">
          {t("Microphone permission")}
        </div>
        <output
          class={
            "block text-xs mt-0.5 " +
            choose(permission === "denied", "text-warning", "text-[#7a7a8c]")
          }
        >
          {permissionHint(permission, missing)}
        </output>
      </div>
      <div class="settings-row-control">
        <button
          type="button"
          class={primaryBtn}
          data-testid="mic-allow"
          aria-describedby="settings-mic-permission"
          onClick={() => void ask()}
        >
          {choose(
            permission === "denied",
            t("Try again"),
            t("Allow microphone"),
          )}
        </button>
      </div>
    </div>
  );
}

// ListenEngineRow chooses who turns speech into text: a model run in this browser (the first
// choice: nothing leaves the device) or the transcription service.
function ListenEngineRow({
  engine,
  onPick,
}: {
  engine: ListenEngine;
  onPick: (e: ListenEngine) => void;
}) {
  return (
    <Row
      id="settings-listen-engine"
      label={t("Who listens")}
      hint={choose(
        engine === "local",
        t("A model downloaded once, run in this browser."),
        t("A transcription service, through the gateway."),
      )}
    >
      <select
        class={selectCls}
        aria-labelledby="settings-listen-engine"
        data-testid="voice-engine"
        value={engine}
        onChange={(e) =>
          onPick((e.target as HTMLSelectElement).value as ListenEngine)
        }
      >
        <option value="local">{t("This browser")}</option>
        <option value="service">{t("Transcription service")}</option>
      </select>
    </Row>
  );
}

// listenHint is what listening for the wake phrase does with what it hears.
function listenHint(engine: ListenEngine, phrases: string[]): string {
  const said = phrases.map((p) => `“${p}”`).join(", ");
  if (engine === "local") {
    return tf(
      "Only while this tab is open. What you say is heard in this browser. Say: {phrases}",
      { phrases: said },
    );
  }
  return tf(
    "Only while this tab is open. What you say is sent to the transcription service. Say: {phrases}",
    { phrases: said },
  );
}

// ListenSource is who listens and what that one needs: the models to download for this browser,
// or the transcription service of the gateway (whose wake phrases count for either).
function ListenSource({
  engine,
  onPick,
  api,
  editor,
  prefs,
  setPrefs,
}: {
  engine: ListenEngine;
  onPick: (e: ListenEngine) => void;
  api: Api | undefined;
  editor: ReturnType<typeof gatewayEditor>;
  prefs: VoicePrefs;
  setPrefs: (p: VoicePrefs) => void;
}) {
  return (
    <>
      <ListenEngineRow engine={engine} onPick={onPick} />
      {engine === "local" && api && (
        <LocalListening api={api} prefs={prefs} setPrefs={setPrefs} />
      )}
      {engine === "local" && isMobileDevice() && <MobileGpuRow />}
      {editor && <VoiceGateway {...editor} service={engine === "service"} />}
    </>
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
  const editor = gatewayEditor(info, api, onInfo);
  const canSave = editor !== null;
  const setDevice = useCallback(
    (deviceId: string) => setPrefs({ ...prefs, deviceId }),
    [prefs, setPrefs],
  );
  const setMic = useCallback(
    (on: boolean) => setPrefs({ ...prefs, mic: on }),
    [prefs, setPrefs],
  );
  const setListen = useCallback(
    (listen: boolean) => setPrefs({ ...prefs, listen }),
    [prefs, setPrefs],
  );
  const engine = listenEngine(prefs, info ?? null);
  const setEngine = useCallback(
    (e: ListenEngine) => setPrefs({ ...prefs, engine: e }),
    [prefs, setPrefs],
  );
  const askable = state !== "insecure" && state !== "unsupported";
  const mic = useMicPermission(askable);
  const allowed = askable && mic.permission === "granted";
  return (
    <section class="settings-group" aria-labelledby="settings-voice-h">
      <h3 id="settings-voice-h" class="settings-group-title">
        {t("Voice input")}
      </h3>
      <div class="settings-card" data-testid="voice-setting">
        {askable && mic.permission && !allowed && (
          <MicPermissionRow
            permission={mic.permission}
            missing={mic.missing}
            ask={mic.ask}
          />
        )}
        <fieldset
          class="settings-voice-lock"
          disabled={!allowed}
          data-testid="voice-locked"
        >
          <ListenSource
            engine={engine}
            onPick={setEngine}
            api={api}
            editor={editor}
            prefs={prefs}
            setPrefs={setPrefs}
          />
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
            hint={voiceHint(hands, listenHint(engine, phrases), canSave)}
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
