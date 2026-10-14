import { useCallback, useEffect, useState } from "preact/hooks";
import { t, tf } from "./i18n";
import { choose } from "./choose";
import {
  type Api,
  type LLMView,
  type ModelChoice,
  isConnected,
  loadLLM,
  saveDefault,
  saveTasks,
  sameTasks,
  taskChoices,
} from "./llmApi";
import {
  type ProviderSettingsProps,
  failureText,
  inputCls,
  primaryBtn,
} from "./ProviderSettings";
import { ModelPicker } from "./ModelPicker";

// The kinds of work a model can be assigned to, with what each one covers. The gateway names the
// kinds; one it names that is not here is still shown, under its own name.
const KINDS: Record<string, { label: string; hint: string }> = {
  plan: {
    label: "Planning",
    hint: "Plan mode: explores and proposes, changes nothing.",
  },
  code: {
    label: "Coding",
    hint: "Task mode, and the background agents that write.",
  },
  review: {
    label: "Review",
    hint: "Background agents that review the work and change nothing.",
  },
  research: {
    label: "Research",
    hint: "Background agents that investigate and change nothing.",
  },
  summary: {
    label: "Summaries",
    hint: "Titles, conversation summaries and auto mode's guess.",
  },
};

// ModelSettings is the models tab: the model new sessions start with, and the model each kind of
// work runs on instead of it. The background agents of a session run on the models of their role,
// inside that session.
export function ModelSettings({ api, onNotice }: ProviderSettingsProps) {
  const [view, setView] = useState<LLMView | null>(null);
  const [err, setErr] = useState("");
  const reload = useCallback(() => {
    loadLLM(api)
      .then((v) => {
        setView(v);
        setErr("");
      })
      .catch((e) => setErr(failureText(e)));
  }, [api]);
  useEffect(reload, [reload]);
  return (
    <div data-testid="model-settings">
      <h3 class="sr-only">{t("Models")}</h3>
      <p class="text-xs text-[#7a7a8c] mb-3">
        {t(
          "Pick the model new sessions start with, and a model for each kind of work if you want one.",
        )}
      </p>
      {err && (
        <p class="text-xs text-danger mb-2" role="alert">
          {err}
        </p>
      )}
      {!view && !err && (
        <output class="block text-xs text-[#9a9aaa]">{t("Loading…")}</output>
      )}
      {view && !view.writable && (
        <p class="text-xs text-warning mb-3" data-testid="llm-readonly">
          {t(
            "This gateway runs without a configuration file, so nothing can be saved here. Run motita config on its machine first.",
          )}
        </p>
      )}
      {view && (
        <>
          <DefaultModel
            api={api}
            view={view}
            onSaved={setView}
            onNotice={onNotice}
          />
          <TaskModels
            api={api}
            view={view}
            onSaved={setView}
            onNotice={onNotice}
          />
        </>
      )}
    </div>
  );
}

// DefaultModel picks the provider and model new sessions start with, among the connected ones.
function DefaultModel({
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
  const [provider, setProvider] = useState(view.provider);
  const [model, setModel] = useState(view.model);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  useEffect(() => {
    setProvider(view.provider);
    setModel(view.model);
  }, [view.provider, view.model]);
  const connected = view.providers.filter(isConnected);
  const p = view.providers.find((x) => x.id === provider);
  const changed = provider !== view.provider || model.trim() !== view.model;
  const pick = (id: string) => {
    setProvider(id);
    setErr("");
    const next = view.providers.find((x) => x.id === id);
    setModel(
      choose(
        id === view.provider,
        view.model,
        next?.listed_models?.[0] || next?.models[0]?.id || "",
      ),
    );
  };
  const save = async (e: Event) => {
    e.preventDefault();
    setBusy(true);
    setErr("");
    try {
      const v = await saveDefault(api, provider, model.trim());
      onSaved(v);
      onNotice(tf("New sessions will start with {model}", { model: v.model }));
    } catch (error_) {
      setErr(failureText(error_));
    }
    setBusy(false);
  };
  return (
    <section class="settings-group" aria-labelledby="llm-default-h">
      <h4 id="llm-default-h" class="settings-group-title">
        {t("Default model")}
      </h4>
      <form class="settings-card" onSubmit={save} data-testid="llm-default">
        <div class="settings-row">
          <label for="llm-default-provider" class="text-sm text-[#e8e8ea]">
            {t("Provider")}
          </label>
          <div class="settings-row-control llm-field">
            <select
              id="llm-default-provider"
              class={inputCls}
              value={provider}
              disabled={!view.writable}
              onChange={(e) => pick((e.target as HTMLSelectElement).value)}
            >
              {/* The one in force is listed even when its key has gone, so it is not mistaken for another. */}
              {view.providers
                .filter((x) => isConnected(x) || x.id === view.provider)
                .map((x) => (
                  <option key={x.id} value={x.id}>
                    {x.name}
                  </option>
                ))}
            </select>
          </div>
        </div>
        <div class="settings-row">
          <label for="llm-default-model" class="text-sm text-[#e8e8ea]">
            {t("Model")}
          </label>
          <div class="settings-row-control llm-field llm-task-field">
            <ModelPicker
              key={provider}
              id="llm-default-model"
              provider={p}
              value={model}
              disabled={!view.writable}
              onChange={setModel}
            />
          </div>
        </div>
        <div class="settings-add llm-actions">
          <p class="text-xs text-[#7a7a8c] flex-1 min-w-0">
            {choose(
              connected.length > 0,
              t("Sessions already open keep their own model."),
              t("Connect a provider in Providers first."),
            )}
          </p>
          <button
            type="submit"
            class={primaryBtn}
            disabled={!view.writable || !changed || !model.trim() || busy}
          >
            {choose(busy, t("Saving…"), t("Save"))}
          </button>
        </div>
        {err && (
          <p class="settings-add text-xs text-danger" role="alert">
            {err}
          </p>
        )}
      </form>
    </section>
  );
}

// TaskModels assigns a model to each kind of work. "Same as default" leaves the kind on the
// session's model.
function TaskModels({
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
  const saved = view.tasks || {};
  const [draft, setDraft] = useState<Record<string, ModelChoice>>(saved);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  useEffect(() => setDraft(view.tasks || {}), [view.tasks]);
  const kinds = view.task_kinds || Object.keys(KINDS);
  const usable = view.providers.filter(isConnected);
  const pick = (kind: string, provider: string) => {
    setErr("");
    const p = view.providers.find((x) => x.id === provider);
    setDraft((d) => {
      const next = { ...d };
      if (provider) {
        next[kind] = {
          provider,
          model: choose(
            saved[kind]?.provider === provider,
            saved[kind]?.model || "",
            p?.listed_models?.[0] || p?.models[0]?.id || "",
          ),
        };
      } else {
        delete next[kind];
      }
      return next;
    });
  };
  const setModel = (kind: string, model: string) =>
    setDraft((d) => ({ ...d, [kind]: { ...d[kind], model } }));
  const incomplete = Object.values(draft).some(
    (c) => c.provider && !c.model.trim(),
  );
  const changed = !sameTasks(draft, saved);
  const save = async (e: Event) => {
    e.preventDefault();
    setBusy(true);
    setErr("");
    try {
      const v = await saveTasks(api, taskChoices(draft));
      onSaved(v);
      onNotice(t("Models per kind of work saved"));
    } catch (error_) {
      setErr(failureText(error_));
    }
    setBusy(false);
  };
  return (
    <section class="settings-group" aria-labelledby="llm-tasks-h">
      <h4 id="llm-tasks-h" class="settings-group-title">
        {t("Model per kind of work")}
      </h4>
      <form class="settings-card" onSubmit={save} data-testid="llm-tasks">
        {kinds.map((kind) => {
          const c = draft[kind];
          const p = view.providers.find((x) => x.id === c?.provider);
          const info = KINDS[kind];
          return (
            <div class="settings-row" key={kind} data-kind={kind}>
              <div class="settings-row-text">
                <label
                  for={`llm-task-${kind}-provider`}
                  class="text-sm text-[#e8e8ea]"
                >
                  {info ? t(info.label) : kind}
                </label>
                {info && (
                  <p class="text-xs text-[#7a7a8c]">{t(info.hint)}</p>
                )}
              </div>
              <div class="settings-row-control llm-field llm-task-field">
                <select
                  id={`llm-task-${kind}-provider`}
                  class={inputCls}
                  value={c?.provider || ""}
                  disabled={!view.writable}
                  onChange={(e) =>
                    pick(kind, (e.target as HTMLSelectElement).value)
                  }
                >
                  <option value="">{t("Same as default")}</option>
                  {view.providers
                    .filter((x) => isConnected(x) || x.id === c?.provider)
                    .map((x) => (
                      <option key={x.id} value={x.id}>
                        {x.name}
                      </option>
                    ))}
                </select>
                {c?.provider && (
                  <ModelPicker
                    key={c.provider}
                    id={`llm-task-${kind}-model`}
                    provider={p}
                    value={c.model}
                    ariaLabel={tf("Model for {kind}", {
                      kind: info ? t(info.label) : kind,
                    })}
                    disabled={!view.writable}
                    onChange={(m) => setModel(kind, m)}
                  />
                )}
              </div>
            </div>
          );
        })}
        <div class="settings-add llm-actions">
          <p class="text-xs text-[#7a7a8c] flex-1 min-w-0">
            {choose(
              usable.length > 0,
              t(
                "Each session's agent and its background agents use these models, and work as a team only inside their own session.",
              ),
              t("Connect a provider in Providers first."),
            )}
          </p>
          <button
            type="submit"
            class={primaryBtn}
            disabled={!view.writable || !changed || incomplete || busy}
          >
            {choose(busy, t("Saving…"), t("Save"))}
          </button>
        </div>
        {err && (
          <p class="settings-add text-xs text-danger" role="alert">
            {err}
          </p>
        )}
      </form>
    </section>
  );
}
