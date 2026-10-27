import { useEffect, useState } from "preact/hooks";
import { t, tf } from "./i18n";
import { choose } from "./choose";
import {
  type Api,
  type LLMView,
  customBody,
  customDraft,
  customProblem,
  sameCustom,
  saveCustomModel,
} from "./llmApi";
import {
  type ProviderSettingsProps,
  failureText,
  inputCls,
  primaryBtn,
} from "./ProviderSettings";

// The fields of the form: the key of the draft, its label, the example the placeholder shows and
// what it means.
const FIELDS = [
  {
    key: "model",
    label: "Model",
    example: "llama-3.3-70b-instruct",
    hint: "The name your endpoint knows the model by.",
    mode: "text",
  },
  {
    key: "temperature",
    label: "Temperature",
    example: "0.2",
    hint: "From 0 (precise) to 2 (creative). Empty keeps the endpoint's own.",
    mode: "decimal",
  },
  {
    key: "max_tokens",
    label: "Max tokens",
    example: "8192",
    hint: "The most tokens one answer may have. Empty keeps the general limit.",
    mode: "numeric",
  },
  {
    key: "context_window",
    label: "Context",
    example: "128000",
    hint: "The model's window in tokens, so long conversations are summarised in time. Empty asks the model.",
    mode: "numeric",
  },
] as const;

// CustomModelSettings describes the model of the custom provider: its name and the limits it runs
// with. They apply to that model only; connect the provider itself (URL and token) in Providers.
export function CustomModelSettings({
  api,
  view,
  onSaved,
  onNotice,
}: {
  api: Api;
  view: LLMView;
  onSaved: (v: LLMView) => void;
  onNotice: ProviderSettingsProps["onNotice"];
}) {
  const [draft, setDraft] = useState(customDraft(view.custom_model));
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  useEffect(
    () => setDraft(customDraft(view.custom_model)),
    [view.custom_model],
  );
  const problem = customProblem(draft);
  const changed = !sameCustom(draft, view.custom_model);
  const save = async (e: Event) => {
    e.preventDefault();
    setBusy(true);
    setErr("");
    try {
      const v = await saveCustomModel(api, customBody(draft));
      onSaved(v);
      onNotice(t("Custom model saved"));
    } catch (error_) {
      setErr(failureText(error_));
    }
    setBusy(false);
  };
  return (
    <section class="settings-group" aria-labelledby="llm-custom-h">
      <h4 id="llm-custom-h" class="settings-group-title">
        {t("Custom model")}
      </h4>
      <form class="settings-card" onSubmit={save} data-testid="llm-custom">
        {FIELDS.map((f) => (
          <div class="settings-row" key={f.key}>
            <div class="settings-row-text">
              <label for={`llm-custom-${f.key}`} class="text-sm text-[#e8e8ea]">
                {t(f.label)}
              </label>
              <p class="text-xs text-[#7a7a8c]">{t(f.hint)}</p>
            </div>
            <div class="settings-row-control llm-field">
              <input
                id={`llm-custom-${f.key}`}
                class={inputCls + " font-mono"}
                type="text"
                inputMode={f.mode}
                value={draft[f.key]}
                placeholder={tf("e.g. {example}", { example: f.example })}
                aria-invalid={problem === f.key}
                autocapitalize="off"
                autocomplete="off"
                spellcheck={false}
                disabled={!view.writable}
                onInput={(e) => {
                  const value = (e.target as HTMLInputElement).value;
                  setDraft((d) => ({ ...d, [f.key]: value }));
                }}
              />
            </div>
          </div>
        ))}
        <div class="settings-add llm-actions">
          <p class="text-xs text-[#7a7a8c] flex-1 min-w-0">
            {t(
              "Works with any OpenAI-compatible endpoint. Connect it first in Providers (URL and token), then pick it as a model here or per kind of work.",
            )}
          </p>
          <button
            type="submit"
            class={primaryBtn}
            disabled={!view.writable || !changed || !!problem || busy}
          >
            {choose(busy, t("Saving…"), t("Save"))}
          </button>
        </div>
        {problem && (
          <p class="settings-add text-xs text-danger" role="alert">
            {choose(
              problem === "temperature",
              t("The temperature is a number from 0 to 2."),
              t("Max tokens and context are whole numbers above zero."),
            )}
          </p>
        )}
        {err && (
          <p class="settings-add text-xs text-danger" role="alert">
            {err}
          </p>
        )}
      </form>
    </section>
  );
}
