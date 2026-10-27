import { useCallback, useEffect, useState } from "preact/hooks";
import { t, tf } from "./i18n";
import type { Api } from "./gitApi";
import { choose } from "./choose";
import { errorMessage } from "./errors";
import {
  NAME_PATTERN,
  type SecretsView,
  deleteSecret,
  getSecrets,
  saveSecret,
} from "./secretsApi";
import { dangerBtn, inputCls, primaryBtn } from "./SettingsControls";

// useSecrets holds the section's state: what the gateway reported and the actions that change it.
function useSecrets(api: Api) {
  const [view, setView] = useState<SecretsView | null | undefined>(undefined);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let live = true;
    getSecrets(api)
      .then((v) => {
        if (live) {
          setView(v);
        }
      })
      .catch((e) => {
        if (live) {
          setErr(errorMessage(e));
          setView(null);
        }
      });
    return () => {
      live = false;
    };
  }, [api]);

  // change runs a write and shows what the gateway answers or why it refused; true when it worked.
  const change = useCallback(async (write: () => Promise<SecretsView>) => {
    setBusy(true);
    setErr("");
    let ok = true;
    try {
      setView(await write());
    } catch (e) {
      setErr(errorMessage(e));
      ok = false;
    }
    setBusy(false);
    return ok;
  }, []);

  return { view, err, busy, change };
}

// SecretForm adds a variable, or replaces the one of the same name.
function SecretForm({
  api,
  canSave,
  busy,
  change,
}: {
  api: Api;
  canSave: boolean;
  busy: boolean;
  change: ReturnType<typeof useSecrets>["change"];
}) {
  const [name, setName] = useState("");
  const [value, setValue] = useState("");
  const valid = NAME_PATTERN.test(name) && value !== "";
  const submit = async (e: Event) => {
    e.preventDefault();
    if (await change(() => saveSecret(api, name, value))) {
      setName("");
      setValue("");
    }
  };
  return (
    <form class="settings-row settings-row-stack" onSubmit={submit}>
      <div class="flex flex-wrap gap-2 w-full">
        <input
          class={inputCls + " font-mono flex-1 basis-40"}
          data-testid="secret-name"
          aria-label={t("Variable name")}
          placeholder="DEPLOY_TOKEN"
          autocomplete="off"
          spellcheck={false}
          maxLength={64}
          value={name}
          disabled={!canSave}
          onInput={(e) => setName((e.target as HTMLInputElement).value)}
        />
        <input
          class={inputCls + " font-mono flex-[2] basis-48"}
          data-testid="secret-value"
          type="password"
          aria-label={t("Value")}
          placeholder={t("Paste the value")}
          autocomplete="off"
          value={value}
          disabled={!canSave}
          onInput={(e) => setValue((e.target as HTMLInputElement).value)}
        />
        <button
          type="submit"
          class={primaryBtn}
          data-testid="secret-save"
          disabled={!valid || busy || !canSave}
        >
          {t("Save")}
        </button>
      </div>
    </form>
  );
}

// SecretList names the variables kept, each with a button to remove it.
function SecretList({
  api,
  view,
  busy,
  change,
}: {
  api: Api;
  view: SecretsView;
  busy: boolean;
  change: ReturnType<typeof useSecrets>["change"];
}) {
  if (view.secrets.length === 0) {
    return (
      <p
        class="settings-card-note text-xs text-[#7a7a8c]"
        data-testid="secrets-empty"
      >
        {t("No variables yet.")}
      </p>
    );
  }
  return (
    <ul data-testid="secrets-list">
      {view.secrets.map((s) => (
        <li key={s.name} class="settings-row">
          <div class="settings-row-text">
            <code class="text-sm">{s.name}</code>
            <div class="text-xs text-[#7a7a8c] mt-0.5">
              {choose(
                !!s.hint,
                tf("Saved (…{hint})", { hint: s.hint ?? "" }),
                t("Saved"),
              )}
            </div>
          </div>
          <div class="settings-row-control">
            <button
              type="button"
              class={dangerBtn}
              data-testid={"secret-remove-" + s.name}
              aria-label={tf("Remove {name}", { name: s.name })}
              disabled={busy || !view.can_save}
              onClick={() => void change(() => deleteSecret(api, s.name))}
            >
              {t("Remove")}
            </button>
          </div>
        </li>
      ))}
    </ul>
  );
}

// SecretsSettings is where the person keeps variables (tokens, keys, URLs) for the agent: every
// command it runs gets them in its environment, the agent is told their names but never their
// values, and a value is masked in what a command prints. The section stays hidden on a gateway
// without the secrets API.
export function SecretsSettings({ api }: { api: Api }) {
  const { view, err, busy, change } = useSecrets(api);
  if (view === undefined || (view === null && !err)) {
    return null;
  }
  return (
    <section class="settings-group" aria-labelledby="settings-secrets-h">
      <h3 id="settings-secrets-h" class="settings-group-title">
        {t("Variables for the agent")}
      </h3>
      <div class="settings-card" data-testid="secrets-setting">
        <p class="settings-card-note text-xs text-[#7a7a8c]">
          {t(
            "Tokens, keys or URLs the agent can use. Each one reaches every command it runs as an environment variable. The agent is told the names, never the values, and a value is masked in what a command prints.",
          )}
        </p>
        {view && (
          <>
            <SecretList api={api} view={view} busy={busy} change={change} />
            <SecretForm
              api={api}
              canSave={view.can_save}
              busy={busy}
              change={change}
            />
            <p class="settings-card-note text-xs text-[#7a7a8c]">
              {choose(
                view.can_save,
                tf(
                  "Stored in a file only you can read, beside the configuration, and kept out of reach of the agent's commands. Up to {max} variables; saving a name that exists replaces it.",
                  { max: String(view.max) },
                ),
                t(
                  "The gateway runs on its built-in defaults, without a configuration file, so variables cannot be saved here.",
                ),
              )}
            </p>
          </>
        )}
        {err && (
          <p
            class="settings-card-note text-xs text-danger"
            role="alert"
            data-testid="secrets-error"
          >
            {err}
          </p>
        )}
      </div>
    </section>
  );
}
