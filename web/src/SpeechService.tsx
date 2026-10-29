import { useCallback, useEffect, useState } from "preact/hooks";
import type { Dispatch, StateUpdater } from "preact/hooks";
import { t, tf } from "./i18n";
import { choose } from "./choose";
import {
  type SpeechChange,
  type TtsSaved,
  type VoiceInfo,
  type FoundSpeech,
  DASHSCOPE_BASE,
  SPEECH_PRESETS,
  putSpeech,
  detectSpeech,
} from "./voice";
import type { Api } from "./gitApi";
import { errorMessage } from "./errors";
import { Row, inputCls, primaryBtn, smallBtn } from "./SettingsControls";

interface SpeechForm {
  api: string;
  base_url: string;
  model: string;
  voice: string;
  api_key: string;
}

// speechTarget is where the texts will go with this form: the address typed, the default of the
// api, or the endpoint the file already resolves. Empty when there is nowhere yet.
function speechTarget(saved: TtsSaved, form: SpeechForm): string {
  const base = form.base_url.trim();
  if (base) {
    return base;
  }
  if (form.api === "dashscope") {
    return DASHSCOPE_BASE;
  }
  return choose(saved.api === form.api, saved.endpoint, "");
}

// speechLines say where the text of every answer read will go, before the voice is turned on or moved.
function speechLines(target: string): string[] {
  const lines = [
    t("The text and the audio are not stored, by the gateway or by this page."),
    t(
      "A key typed here is kept beside the configuration and never shown again.",
    ),
  ];
  if (!target) {
    return [t("Name a speech service to read aloud with."), ...lines];
  }
  return [
    tf("The text of every answer read aloud is sent to {endpoint}.", {
      endpoint: target,
    }),
    ...lines,
  ];
}

// foundForm is the form with a server found on the gateway's computer filled in: its address, and
// its first model when it lists one.
function foundForm(form: SpeechForm, f: FoundSpeech): SpeechForm {
  return {
    ...form,
    api: "openai",
    base_url: f.base_url,
    model: f.models[0] ?? form.model,
  };
}

// speechChange is what the form changes: only the fields that differ from the file, and the key
// only when one was typed.
function speechChange(saved: TtsSaved, form: SpeechForm): SpeechChange {
  const change: SpeechChange = { enabled: true, confirm: true };
  const base = form.base_url.trim();
  const model = form.model.trim();
  const voice = form.voice.trim();
  const key = form.api_key.trim();
  if (form.api !== saved.api) {
    change.api = form.api;
  }
  if (base !== saved.base_url) {
    change.base_url = base;
  }
  if (model && model !== saved.model) {
    change.model = model;
  }
  if (voice && voice !== saved.voice) {
    change.voice = voice;
  }
  if (key) {
    change.api_key = key;
  }
  return change;
}

// speechHint says where the gateway's voice sends the text, or that it is off.
function speechHint(saved: TtsSaved): string {
  if (!saved.enabled) {
    return t("Off. Turn it on to read answers aloud with a voice service.");
  }
  return tf("Texts go to {endpoint} ({model}).", {
    endpoint: saved.endpoint,
    model: saved.model,
  });
}

// SpeechService turns the gateway's voice on or off, picks the speech service (OpenAI, or Qwen3-TTS
// on DashScope) and its model, voice and key, all saved in its configuration. Turning it on, or
// moving it, first says where the text goes and waits for a yes.
export function SpeechService({
  info,
  api,
  onInfo,
}: {
  info: VoiceInfo;
  api: Api;
  onInfo: (i: VoiceInfo) => void;
}) {
  const saved = info.saved?.tts;
  const [editing, setEditing] = useState(false);
  const [form, setForm] = useState<SpeechForm>({
    api: "openai",
    base_url: "",
    model: "",
    voice: "",
    api_key: "",
  });
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const save = useCallback(
    async (change: SpeechChange) => {
      setBusy(true);
      setErr("");
      try {
        onInfo(await putSpeech(api, change));
        setEditing(false);
      } catch (e) {
        setErr(errorMessage(e));
      }
      setBusy(false);
    },
    [api, onInfo],
  );
  if (!info.can_save || !saved) {
    return null;
  }
  const openForm = () => {
    setErr("");
    setForm({
      api: saved.api,
      base_url: saved.base_url,
      model: saved.model,
      voice: saved.voice,
      api_key: "",
    });
    setEditing(true);
  };
  return (
    <>
      <Row
        id="settings-speech-service"
        label={t("Voice service")}
        hint={speechHint(saved)}
      >
        <label class="settings-switch-hit">
          <input
            type="checkbox"
            role="switch"
            class="settings-switch"
            aria-labelledby="settings-speech-service"
            data-testid="speech-service-switch"
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
          id="settings-speech-service-change"
          label={t("Speech service")}
          hint={saved.model}
        >
          <button
            type="button"
            class={smallBtn}
            data-testid="speech-service-change"
            onClick={openForm}
          >
            {t("Change")}
          </button>
        </Row>
      )}
      {editing && (
        <SpeechServiceForm
          saved={saved}
          form={form}
          setForm={setForm}
          busy={busy}
          setEditing={setEditing}
          save={save}
          api={api}
        />
      )}
      {info.restart_needed && (
        <output
          class="settings-card-note block text-xs text-warning"
          data-testid="speech-service-restart"
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

// SpeechServiceForm is the speech service to turn the voice on with, or to move it to, and where
// the text will go, waiting for a yes.
function SpeechServiceForm({
  saved,
  form,
  setForm,
  busy,
  setEditing,
  save,
  api,
}: {
  api: Api;
  saved: TtsSaved;
  form: SpeechForm;
  setForm: Dispatch<StateUpdater<SpeechForm>>;
  busy: boolean;
  setEditing: Dispatch<StateUpdater<boolean>>;
  save: (change: SpeechChange) => Promise<void>;
}) {
  const target = speechTarget(saved, form);
  const [found, setFound] = useState<FoundSpeech[] | null>(null);
  const [looking, setLooking] = useState(false);
  const [lookErr, setLookErr] = useState("");
  const look = async (prefill: boolean) => {
    setLooking(true);
    setLookErr("");
    try {
      const servers = await detectSpeech(api);
      setFound(servers);
      // A single server on the machine and nothing typed yet: the form starts on it. The yes is
      // still asked, with its address in the lines above the button.
      if (prefill && servers.length === 1 && !saved.base_url) {
        setForm((f) => foundForm(f, servers[0]));
      }
    } catch (e) {
      setLookErr(errorMessage(e));
    }
    setLooking(false);
  };
  // The form looks once when it opens, on the gateway's loopback only: no text is sent.
  useEffect(() => {
    void look(true);
  }, []);
  const field = (k: keyof SpeechForm) => (e: Event) =>
    setForm({ ...form, [k]: (e.target as HTMLInputElement).value });
  return (
    <fieldset
      aria-labelledby="settings-speech-confirm"
      class="settings-confirm min-w-0"
      data-testid="speech-service-confirm"
    >
      <div
        id="settings-speech-confirm"
        class="text-sm font-semibold text-[#e8e8ea]"
      >
        {choose(
          saved.enabled,
          t("Change the voice service?"),
          t("Turn on the voice service?"),
        )}
      </div>
      <div class="flex flex-wrap gap-2">
        {SPEECH_PRESETS.map((p) => (
          <button
            key={p.id}
            type="button"
            class={smallBtn}
            data-testid={"speech-preset-" + p.id}
            onClick={() =>
              setForm({
                ...form,
                api: p.api,
                base_url: p.base_url,
                model: p.model,
                voice: p.voice,
              })
            }
          >
            {p.label}
          </button>
        ))}
      </div>
      <div class="flex flex-wrap items-center gap-2">
        <button
          type="button"
          class={smallBtn}
          disabled={looking}
          data-testid="speech-detect"
          onClick={() => void look(false)}
        >
          {t("Find one on this computer")}
        </button>
        {found?.length === 0 && (
          <span class="text-xs" data-testid="speech-detect-none">
            {t("No speech server found on the gateway's computer.")}
          </span>
        )}
        {lookErr && (
          <span class="text-xs text-danger" role="alert">
            {lookErr}
          </span>
        )}
      </div>
      {found && found.length > 0 && (
        <div class="flex flex-wrap gap-2">
          {found.map((f) => (
            <button
              key={f.base_url}
              type="button"
              class={smallBtn}
              data-testid="speech-found"
              onClick={() => setForm(foundForm(form, f))}
            >
              {f.base_url}
            </button>
          ))}
        </div>
      )}
      <label class="llm-label">
        <span>{t("Endpoint")}</span>
        <input
          class={inputCls + " font-mono"}
          value={form.base_url}
          placeholder={target}
          autocapitalize="off"
          spellcheck={false}
          data-testid="speech-base-url"
          onInput={field("base_url")}
        />
      </label>
      <label class="llm-label">
        <span>{t("Model")}</span>
        <input
          class={inputCls + " font-mono"}
          value={form.model}
          autocapitalize="off"
          spellcheck={false}
          data-testid="speech-model"
          onInput={field("model")}
        />
      </label>
      <label class="llm-label">
        <span>{t("Voice")}</span>
        <input
          class={inputCls + " font-mono"}
          value={form.voice}
          autocapitalize="off"
          spellcheck={false}
          data-testid="speech-voice"
          onInput={field("voice")}
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
          data-testid="speech-api-key"
          onInput={field("api_key")}
        />
      </label>
      <ul class="settings-confirm-lines">
        {speechLines(target).map((l) => (
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
          data-testid="speech-confirm-yes"
          onClick={() => void save(speechChange(saved, form))}
        >
          {choose(saved.enabled, t("Save"), t("Turn on"))}
        </button>
      </div>
    </fieldset>
  );
}
