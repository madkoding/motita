import { useCallback, useEffect, useRef, useState } from "preact/hooks";
import { t, tf } from "./i18n";
import { choose } from "./choose";
import { errorMessage } from "./errors";
import { InsecureWarning } from "./InsecureWarning";
import { ProviderLogo } from "./ProviderPicker";
import {
  type Api,
  type LLMProvider,
  type LLMView,
  type LoginView,
  type TestResult,
  LLMError,
  canTypeKey,
  cancelLogin,
  forgetProvider,
  isConnected,
  loadLLM,
  loginStatus,
  modelChoices,
  pasteLogin,
  saveDefault,
  saveProvider,
  startLogin,
  testProvider,
  trialOf,
} from "./llmApi";

export interface ProviderSettingsProps {
  api: Api;
  onNotice: (message: string, error?: boolean) => void;
}

const inputCls =
  "w-full min-w-0 px-2 py-1.5 rounded-lg bg-black/30 border border-white/10 text-xs text-[#e8e8ea] focus:outline-hidden focus:border-accent";
const smallBtn =
  "btn-outline px-3 min-h-[32px] rounded-lg border border-white/10 text-xs text-[#e8e8ea] hover:bg-white/5 active:scale-95 transition-transform disabled:opacity-40 disabled:cursor-not-allowed";
const primaryBtn = smallBtn + " bg-accent text-white border-transparent";

const PROVIDERS_GUIDE =
  "https://github.com/madkoding/motita/blob/main/docs/REFERENCE.md#llm-providers-from-the-browser";

// failureText is what a refused request is shown as: the codes the gateway names get a sentence
// in the reader's language, anything else its own message.
function failureText(e: unknown): string {
  if (e instanceof LLMError) {
    if (e.code === "env_key") {
      return t(
        "The key comes from an environment variable on the gateway's machine, which wins over one saved here.",
      );
    }
    if (e.code === "cleartext") {
      return t(
        "That address is plain http outside your network: the key would travel unencrypted. Use https.",
      );
    }
    if (e.code === "key_for_new_address") {
      return t(
        "Type the key again to try it on a new address: a saved key is only sent where it was saved for.",
      );
    }
  }
  return errorMessage(e);
}

// stateText is the line under a provider's name: whether it can run, and on what. A key's last
// four characters follow, so two saved keys can be told apart without showing either.
export function stateText(p: LLMProvider): string {
  const s = keyState(p);
  return choose(!!p.key_hint && isConnected(p), s + " " + p.key_hint, s);
}

function keyState(p: LLMProvider): string {
  switch (p.key) {
    case "stored":
      return t("Connected · key saved");
    case "env":
      return tf("Connected · key from {name}", {
        name: p.key_variable || "",
      });
    case "config":
      return t("Connected · key in the configuration file");
    case "login":
      return t("Connected · signed in");
    case "not_needed":
      return t("Ready · no key needed");
    default:
      return t("Not connected");
  }
}

// ProviderIcon is the provider's logo, or its initial, on the same tile the git hosts use.
function ProviderIcon({ p }: { p: LLMProvider }) {
  return <ProviderLogo id={p.id} name={p.name} />;
}

// ProviderSettings is the providers tab: the default model new sessions start with, and every
// provider with its connection.
export function ProviderSettings({ api, onNotice }: ProviderSettingsProps) {
  const [view, setView] = useState<LLMView | null>(null);
  const [err, setErr] = useState("");
  const [open, setOpen] = useState("");
  const [listed, setListed] = useState<Record<string, string[]>>({});
  const reload = useCallback(() => {
    loadLLM(api)
      .then((v) => {
        setView(v);
        setErr("");
      })
      .catch((e) => setErr(failureText(e)));
  }, [api]);
  useEffect(reload, [reload]);
  const onListed = useCallback(
    (id: string, models: string[]) =>
      setListed((l) => ({ ...l, [id]: models })),
    [],
  );
  const insecure =
    typeof window !== "undefined" && window.isSecureContext === false;
  return (
    <div data-testid="provider-settings">
      <h3 class="sr-only">{t("Providers")}</h3>
      <p class="text-xs text-[#7a7a8c] mb-3">
        {t(
          "Connect the AI providers Motita can use, and pick the model new sessions start with.",
        )}
      </p>
      {insecure && (
        <div class="mb-3">
          <InsecureWarning
            text={t(
              "A key you type here travels to Motita unencrypted, because it was opened without a secure connection.",
            )}
            why={t(
              "Only an address that starts with https:// (or localhost on this same computer) encrypts what you send. Open Motita through https before pasting a key.",
            )}
            guide={PROVIDERS_GUIDE}
          />
        </div>
      )}
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
            listed={listed}
            onSaved={setView}
            onNotice={onNotice}
          />
          <section class="settings-group" aria-labelledby="llm-providers-h">
            <h4 id="llm-providers-h" class="settings-group-title">
              {t("Providers")}
            </h4>
            <ul class="settings-card" data-testid="llm-providers">
              {view.providers.map((p) => (
                <ProviderRow
                  key={p.id}
                  api={api}
                  p={p}
                  writable={view.writable}
                  open={open === p.id}
                  onToggle={() => setOpen(choose(open === p.id, "", p.id))}
                  onChanged={reload}
                  onListed={onListed}
                  onNotice={onNotice}
                />
              ))}
            </ul>
          </section>
        </>
      )}
    </div>
  );
}

// DefaultModel picks the provider and model new sessions start with, among the connected ones.
function DefaultModel({
  api,
  view,
  listed,
  onSaved,
  onNotice,
}: {
  api: Api;
  view: LLMView;
  listed: Record<string, string[]>;
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
  const choices = modelChoices(
    p,
    listed[provider],
    choose(provider === view.provider, view.model, ""),
  );
  const changed = provider !== view.provider || model.trim() !== view.model;
  const pick = (id: string) => {
    setProvider(id);
    setErr("");
    const next = view.providers.find((x) => x.id === id);
    setModel(
      choose(id === view.provider, view.model, next?.models[0]?.id || ""),
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
          <div class="settings-row-control llm-field">
            <input
              id="llm-default-model"
              class={inputCls + " font-mono"}
              value={model}
              list="llm-default-models"
              disabled={!view.writable}
              autocapitalize="off"
              spellcheck={false}
              onInput={(e) => setModel((e.target as HTMLInputElement).value)}
            />
            <datalist id="llm-default-models">
              {choices.map((id) => (
                <option key={id} value={id} />
              ))}
            </datalist>
          </div>
        </div>
        <div class="settings-add llm-actions">
          <p class="text-xs text-[#7a7a8c] flex-1 min-w-0">
            {choose(
              connected.length > 0,
              t("Sessions already open keep their own model."),
              t("Connect a provider below first."),
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

interface RowProps {
  api: Api;
  p: LLMProvider;
  writable: boolean;
  open: boolean;
  onToggle: () => void;
  onChanged: () => void;
  onListed: (id: string, models: string[]) => void;
  onNotice: ProviderSettingsProps["onNotice"];
}

// ProviderRow is one provider: its state, and its connection form when it is open.
function ProviderRow(props: RowProps) {
  const { p, open, onToggle } = props;
  const connected = isConnected(p);
  return (
    <li class="settings-row git-account" data-provider={p.id}>
      <ProviderIcon p={p} />
      <div class="settings-row-text">
        <div class="text-sm text-[#e8e8ea] llm-name">
          {p.name}
          {p.default && <span class="llm-badge">{t("Default")}</span>}
        </div>
        <div
          class={
            "git-account-state text-xs " +
            choose(connected, "text-[#5fd08a]", "text-[#7a7a8c]")
          }
        >
          {stateText(p)}
        </div>
      </div>
      <div class="settings-row-control git-account-actions">
        <button
          type="button"
          class={smallBtn}
          aria-expanded={open}
          aria-label={tf("Set up {name}", { name: p.name })}
          onClick={onToggle}
        >
          {choose(
            open,
            t("Close"),
            choose(connected, t("Manage"), t("Connect")),
          )}
        </button>
      </div>
      {open && <ProviderForm {...props} />}
    </li>
  );
}

// explanation says how a provider connects, for the source its key comes from.
function explanation(p: LLMProvider): string {
  if (p.key === "not_needed" && p.login) {
    return tf(
      "It uses the login of its own command: run {cmd} on the gateway's machine.",
      { cmd: p.login },
    );
  }
  if (p.key === "not_needed") {
    return t("Your own server needs no key.");
  }
  if (p.key === "env") {
    return tf(
      "The key comes from {name} on the gateway's machine, and it wins over one saved here.",
      { name: p.key_variable || "" },
    );
  }
  if (p.key === "config") {
    return t(
      "The key is written in the configuration file (llm.api_key). Remove it from there to manage the key here.",
    );
  }
  // The catalogue's own words, translated where the dictionary has them.
  return t(p.access);
}

// ProviderForm connects one provider: a key, the endpoint where it can change, a test and a save.
function ProviderForm({
  api,
  p,
  writable,
  onToggle,
  onChanged,
  onListed,
  onNotice,
}: RowProps) {
  const [key, setKey] = useState("");
  const [base, setBase] = useState(p.base_url || "");
  const [busy, setBusy] = useState("");
  const [result, setResult] = useState<TestResult | null>(null);
  const [err, setErr] = useState("");
  const [confirm, setConfirm] = useState(false);
  const typing = canTypeKey(p);
  const trial = trialOf(p, key, base);
  const run = async (what: string, fn: () => Promise<void>) => {
    setBusy(what);
    setErr("");
    try {
      await fn();
    } catch (e) {
      setErr(failureText(e));
    }
    setBusy("");
  };
  const test = () =>
    run("test", async () => {
      setResult(null);
      const r = await testProvider(api, p.id, trial);
      setResult(r);
      if (r.ok && r.models?.length) {
        onListed(p.id, r.models);
      }
    });
  const save = (e: Event) => {
    e.preventDefault();
    void run("save", async () => {
      await saveProvider(api, p.id, trial);
      onNotice(tf("{name} saved", { name: p.name }));
      onChanged();
      onToggle();
    });
  };
  const forget = () =>
    run("forget", async () => {
      await forgetProvider(api, p.id);
      onNotice(tf("Disconnected from {host}", { host: p.name }));
      onChanged();
      onToggle();
    });
  const canForget =
    writable &&
    (p.key === "stored" ||
      p.key === "login" ||
      (!!p.ask_base_url && !!p.base_url && p.base_url !== p.default_base_url));
  return (
    <form class="llm-form" onSubmit={save} data-testid="llm-form">
      <p class="text-xs text-[#9a9aaa]">
        {explanation(p)}
        {p.console_url && typing && (
          <>
            {" "}
            <a
              class="text-accent underline underline-offset-2"
              href={p.console_url}
              target="_blank"
              rel="noopener noreferrer"
            >
              {t("Get a key")} ↗
            </a>
          </>
        )}
      </p>
      {p.supports_login && writable && (
        <AccountLogin
          api={api}
          p={p}
          onDone={() => {
            onNotice(tf("Signed in to {name}", { name: p.name }));
            onChanged();
            onToggle();
          }}
        />
      )}
      {typing && (
        <label class="llm-label">
          <span>{t("API key")}</span>
          <input
            class={inputCls + " font-mono"}
            type="password"
            value={key}
            placeholder={choose(
              p.key === "stored",
              tf("Saved ({hint}). Paste a new one to replace it", {
                hint: p.key_hint || "…",
              }),
              t("Paste the key"),
            )}
            autocomplete="off"
            autocapitalize="off"
            spellcheck={false}
            data-autofocus
            onInput={(e) => setKey((e.target as HTMLInputElement).value)}
          />
        </label>
      )}
      {p.ask_base_url && (
        <label class="llm-label">
          <span>{t("Endpoint")}</span>
          <input
            class={inputCls + " font-mono"}
            type="url"
            value={base}
            placeholder={p.default_base_url}
            autocapitalize="off"
            spellcheck={false}
            onInput={(e) => setBase((e.target as HTMLInputElement).value)}
          />
        </label>
      )}
      {result?.ok && (
        <output class="block text-xs text-[#5fd08a]">
          {tf("It works: {n} models available.", {
            n: result.models?.length || 0,
          })}
        </output>
      )}
      {result && !result.ok && (
        <p class="text-xs text-danger" role="alert">
          {tf("It did not connect: {err}", { err: result.error || "" })}
        </p>
      )}
      {err && (
        <p class="text-xs text-danger" role="alert">
          {err}
        </p>
      )}
      <div class="llm-actions">
        {canForget && !confirm && (
          <button
            type="button"
            class={smallBtn + " llm-forget"}
            onClick={() => setConfirm(true)}
          >
            {t("Disconnect")}
          </button>
        )}
        {confirm && (
          <>
            <button
              type="button"
              class={smallBtn + " text-danger"}
              disabled={!!busy}
              onClick={forget}
            >
              {t("Confirm")}
            </button>
            <button
              type="button"
              class={smallBtn}
              onClick={() => setConfirm(false)}
            >
              {t("Cancel")}
            </button>
          </>
        )}
        <span class="flex-1" />
        <button
          type="button"
          class={smallBtn}
          disabled={!!busy}
          onClick={() => void test()}
        >
          {choose(busy === "test", t("Testing…"), t("Test connection"))}
        </button>
        {writable && (typing || p.ask_base_url) && (
          <button
            type="submit"
            class={primaryBtn}
            disabled={!!busy || Object.keys(trial).length === 0}
          >
            {choose(busy === "save", t("Saving…"), t("Save"))}
          </button>
        )}
      </div>
    </form>
  );
}

// LOGIN_POLL is how often a pending sign-in asks the gateway whether the provider answered.
export const LOGIN_POLL = 2000;

// AccountLogin signs in with the provider's account. The gateway runs the login: the page shows
// the link and the code, or takes the address the browser ended on, and waits for the end.
function AccountLogin({
  api,
  p,
  onDone,
}: {
  api: Api;
  p: LLMProvider;
  onDone: () => void;
}) {
  const [login, setLogin] = useState<LoginView | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [pasted, setPasted] = useState("");
  const [sent, setSent] = useState(false);
  const flow = useRef("");
  const done = useRef(onDone);
  done.current = onDone;
  // A login left pending when the form closes is stopped, so the gateway forgets it.
  useEffect(
    () => () => {
      if (flow.current) {
        void cancelLogin(api, flow.current);
      }
    },
    [api],
  );
  const settle = useCallback((l: LoginView) => {
    setLogin(l);
    if (l.status === "pending") {
      return;
    }
    flow.current = "";
    if (l.status === "connected") {
      done.current();
    }
  }, []);
  useEffect(() => {
    if (login?.status !== "pending") {
      return;
    }
    const id = setTimeout(() => {
      loginStatus(api, login.id)
        .then(settle)
        .catch((e) => {
          flow.current = "";
          setLogin(null);
          setErr(failureText(e));
        });
    }, LOGIN_POLL);
    return () => clearTimeout(id);
  }, [api, login, settle]);
  const start = async () => {
    setBusy(true);
    setErr("");
    setSent(false);
    setPasted("");
    try {
      const l = await startLogin(api, p.id);
      flow.current = choose(l.status === "pending", l.id, "");
      settle(l);
    } catch (e) {
      setErr(failureText(e));
    }
    setBusy(false);
  };
  const stop = () => {
    void cancelLogin(api, flow.current);
    flow.current = "";
    setLogin(null);
  };
  const paste = async () => {
    setErr("");
    try {
      await pasteLogin(api, login?.id || "", pasted.trim());
      setSent(true);
    } catch (e) {
      setErr(failureText(e));
    }
  };
  const pending = login?.status === "pending";
  return (
    <div class="llm-login" data-testid="llm-login">
      {!pending && (
        <div class="llm-actions">
          <button
            type="button"
            class={smallBtn}
            disabled={busy}
            onClick={() => void start()}
          >
            {choose(
              busy,
              t("Starting…"),
              choose(
                p.key === "login",
                t("Sign in again"),
                t("Sign in with your account"),
              ),
            )}
          </button>
        </div>
      )}
      {pending && login.method === "device" && (
        <div class="llm-login-step">
          <p class="text-xs text-[#9a9aaa]">
            {t("Open the link and type this code:")}
          </p>
          <code class="llm-login-code" data-testid="llm-login-code">
            {login.code}
          </code>
          <a
            class="text-xs text-accent underline underline-offset-2 break-all"
            href={login.url}
            target="_blank"
            rel="noopener noreferrer"
          >
            {login.url} ↗
          </a>
        </div>
      )}
      {pending && login.method === "code" && (
        <div class="llm-login-step">
          <a
            class="text-xs text-accent underline underline-offset-2"
            href={login.url}
            target="_blank"
            rel="noopener noreferrer"
          >
            {t("Open the sign-in page")} ↗
          </a>
          <p class="text-xs text-[#9a9aaa]">
            {t(
              "If the page ends on an address that does not load, paste that address here.",
            )}
          </p>
          <div class="llm-actions">
            <input
              class={inputCls + " font-mono flex-1"}
              value={pasted}
              placeholder="localhost:1455/auth/callback?code=…"
              aria-label={t("Address the browser ended on")}
              autocapitalize="off"
              spellcheck={false}
              onInput={(e) => setPasted((e.target as HTMLInputElement).value)}
            />
            <button
              type="button"
              class={smallBtn}
              disabled={!pasted.trim() || sent}
              onClick={() => void paste()}
            >
              {t("Send")}
            </button>
          </div>
        </div>
      )}
      {pending && (
        <div class="llm-actions">
          <output class="text-xs text-[#9a9aaa] flex-1">
            {t("Waiting for the provider…")}
          </output>
          <button type="button" class={smallBtn} onClick={stop}>
            {t("Cancel")}
          </button>
        </div>
      )}
      {login?.status === "failed" && (
        <p class="text-xs text-danger" role="alert">
          {tf("The sign-in did not finish: {err}", { err: login.error || "" })}
        </p>
      )}
      {err && (
        <p class="text-xs text-danger" role="alert">
          {err}
        </p>
      )}
    </div>
  );
}
