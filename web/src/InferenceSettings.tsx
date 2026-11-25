import { useCallback, useEffect, useState } from "preact/hooks";
import { t, tf } from "./i18n";
import type { Api } from "./gitApi";
import { choose } from "./choose";
import { errorMessage } from "./errors";
import {
  type InferenceBody,
  type InferenceKind,
  type InferenceServer,
  type InferenceView,
  type ProbeResult,
  addInference,
  editInference,
  getInference,
  probeInference,
  removeInference,
} from "./inferenceApi";
import {
  dangerBtn,
  inputCls,
  primaryBtn,
  selectCls,
  smallBtn,
} from "./SettingsControls";

// useInference holds the section's state: what the gateway reported and the actions that change it.
function useInference(api: Api) {
  const [view, setView] = useState<InferenceView | null | undefined>(undefined);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let live = true;
    getInference(api)
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
  const change = useCallback(async (write: () => Promise<InferenceView>) => {
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

type Change = ReturnType<typeof useInference>["change"];

// Draft is what the form holds besides the key.
interface Draft {
  name: string;
  kind: string;
  base: string;
  model: string;
}

// draftOf starts the form empty, or from the server being edited.
function draftOf(editing: InferenceServer | null, firstKind: string): Draft {
  return {
    name: editing?.name ?? "",
    kind: editing?.kind ?? firstKind,
    base: editing?.base_url ?? "",
    model: editing?.model ?? "",
  };
}

// usualAddress is where a kind listens by default, "" when it has none to guess.
function usualAddress(kinds: InferenceKind[], kind: string): string {
  return kinds.find((k) => k.id === kind)?.default_base_url ?? "";
}

// canSubmit says whether the draft names a server and an address, typed or usual.
function canSubmit(d: Draft, usual: string): boolean {
  return d.name.trim() !== "" && (d.base.trim() !== "" || usual !== "");
}

// bodyOf is what the form sends; the key only when one was typed, so an edit keeps the saved one.
function bodyOf(d: Draft, key: string): InferenceBody {
  const body: InferenceBody = {
    name: d.name,
    kind: d.kind,
    base_url: d.base,
    model: d.model,
  };
  if (key !== "") {
    body.api_key = key;
  }
  return body;
}

// ServerForm adds a server, or edits the one it is given.
function ServerForm({
  api,
  view,
  busy,
  change,
  editing,
  onDone,
}: {
  api: Api;
  view: InferenceView;
  busy: boolean;
  change: Change;
  editing: InferenceServer | null;
  onDone: () => void;
}) {
  const [draft, setDraft] = useState(() => draftOf(editing, view.kinds[0].id));
  const [key, setKey] = useState("");
  const usual = usualAddress(view.kinds, draft.kind);
  // field is the handler that keeps one text field of the draft.
  const field = (k: keyof Draft) => (e: Event) => {
    const value = (e.target as HTMLInputElement | HTMLSelectElement).value;
    setDraft((d) => ({ ...d, [k]: value }));
  };
  const submit = async (e: Event) => {
    e.preventDefault();
    const body = bodyOf(draft, key);
    const write = editing
      ? () => editInference(api, editing.id, body)
      : () => addInference(api, body);
    if (await change(write)) {
      onDone();
    }
  };
  return (
    <form class="settings-row settings-row-stack" onSubmit={submit}>
      <div class="flex flex-wrap gap-2 w-full">
        <input
          class={inputCls + " flex-1 basis-40"}
          data-testid="inference-name"
          aria-label={t("Server name")}
          placeholder={t("My GPU box")}
          maxLength={80}
          value={draft.name}
          disabled={!view.can_save}
          onInput={field("name")}
        />
        <select
          class={selectCls}
          data-testid="inference-kind"
          aria-label={t("Kind")}
          value={draft.kind}
          disabled={!view.can_save}
          onChange={field("kind")}
        >
          {view.kinds.map((k) => (
            <option key={k.id} value={k.id}>
              {k.name}
            </option>
          ))}
        </select>
      </div>
      <div class="flex flex-wrap gap-2 w-full">
        <input
          class={inputCls + " font-mono flex-[2] basis-56"}
          data-testid="inference-base"
          aria-label={t("Address")}
          placeholder={usual || "https://host:port/v1"}
          autocomplete="off"
          spellcheck={false}
          value={draft.base}
          disabled={!view.can_save}
          onInput={field("base")}
        />
        <input
          class={inputCls + " font-mono flex-1 basis-40"}
          data-testid="inference-model"
          aria-label={t("Model (optional)")}
          placeholder={t("Model (optional)")}
          autocomplete="off"
          spellcheck={false}
          value={draft.model}
          disabled={!view.can_save}
          onInput={field("model")}
        />
      </div>
      <div class="flex flex-wrap gap-2 w-full">
        <input
          class={inputCls + " font-mono flex-1 basis-48"}
          data-testid="inference-key"
          type="password"
          aria-label={t("Key (optional)")}
          placeholder={choose(
            !!editing,
            t("Leave empty to keep the saved key"),
            t("Key (optional)"),
          )}
          autocomplete="off"
          value={key}
          disabled={!view.can_save}
          onInput={(e) => setKey((e.target as HTMLInputElement).value)}
        />
        <button
          type="submit"
          class={primaryBtn}
          data-testid="inference-save"
          disabled={!canSubmit(draft, usual) || busy || !view.can_save}
        >
          {choose(!!editing, t("Save"), t("Add"))}
        </button>
        {editing && (
          <button
            type="button"
            class={smallBtn}
            data-testid="inference-cancel"
            onClick={onDone}
          >
            {t("Cancel")}
          </button>
        )}
      </div>
    </form>
  );
}

// probeLine is what a test of a server says.
function probeLine(r: ProbeResult): string {
  if (r.ok) {
    return tf("{count} models in {ms} ms", {
      count: String(r.models?.length ?? 0),
      ms: String(r.latency_ms),
    });
  }
  return tf("Not reachable: {error}", { error: r.error ?? "" });
}

// ServerRow is one server with its buttons and the last answer of its test.
function ServerRow({
  api,
  server,
  busy,
  change,
  onEdit,
  canSave,
}: {
  api: Api;
  server: InferenceServer;
  busy: boolean;
  change: Change;
  onEdit: (s: InferenceServer) => void;
  canSave: boolean;
}) {
  const [probe, setProbe] = useState<ProbeResult | "running" | null>(null);
  const test = async () => {
    setProbe("running");
    try {
      setProbe(await probeInference(api, server.id));
    } catch (e) {
      setProbe({ ok: false, latency_ms: 0, error: errorMessage(e) });
    }
  };
  const key = choose(
    server.has_key,
    choose(
      !!server.key_hint,
      tf("Key saved (…{hint})", { hint: (server.key_hint ?? "").slice(-4) }),
      t("Key saved"),
    ),
    t("No key"),
  );
  return (
    <li class="settings-row" data-testid={"inference-row-" + server.id}>
      <div class="settings-row-text">
        <div class="text-sm">{server.name}</div>
        <div class="text-xs text-[#7a7a8c] mt-0.5 font-mono break-all">
          {server.base_url}
          {server.model ? " · " + server.model : ""}
        </div>
        <div class="text-xs text-[#7a7a8c] mt-0.5">{key}</div>
        {probe && (
          <output
            class={
              "block text-xs mt-0.5 " +
              choose(probe !== "running" && !probe.ok, "text-danger", "")
            }
            data-testid={"inference-probe-" + server.id}
          >
            {choose(probe === "running", t("Testing") + "…", "")}
            {probe !== "running" && probeLine(probe)}
          </output>
        )}
      </div>
      <div class="settings-row-control flex gap-2">
        <button
          type="button"
          class={smallBtn}
          data-testid={"inference-test-" + server.id}
          aria-label={tf("Test {name}", { name: server.name })}
          disabled={probe === "running"}
          onClick={() => void test()}
        >
          {t("Test")}
        </button>
        <button
          type="button"
          class={smallBtn}
          data-testid={"inference-edit-" + server.id}
          aria-label={tf("Edit {name}", { name: server.name })}
          disabled={busy || !canSave}
          onClick={() => onEdit(server)}
        >
          {t("Edit")}
        </button>
        <button
          type="button"
          class={dangerBtn}
          data-testid={"inference-remove-" + server.id}
          aria-label={tf("Remove {name}", { name: server.name })}
          disabled={busy || !canSave}
          onClick={() => void change(() => removeInference(api, server.id))}
        >
          {t("Remove")}
        </button>
      </div>
    </li>
  );
}

// InferenceSettings is where the person keeps the servers that run models: llama.cpp, LM Studio,
// Ollama or any OpenAI-compatible endpoint. The section stays hidden on a gateway without the API.
export function InferenceSettings({ api }: { api: Api }) {
  const { view, err, busy, change } = useInference(api);
  const [editing, setEditing] = useState<InferenceServer | null>(null);
  // formRun changes after each save, so the form starts over empty.
  const [formRun, setFormRun] = useState(0);
  const done = useCallback(() => {
    setEditing(null);
    setFormRun((n) => n + 1);
  }, []);
  if (view === undefined || (view === null && !err)) {
    return null;
  }
  return (
    <section class="settings-group" aria-labelledby="settings-inference-h">
      <h3 id="settings-inference-h" class="settings-group-title">
        {t("Inference servers")}
      </h3>
      <div class="settings-card" data-testid="inference-setting">
        <p class="settings-card-note text-xs text-[#7a7a8c]">
          {t(
            "Servers that run models for you: llama.cpp, LM Studio, Ollama or any OpenAI-compatible endpoint. Keys stay in a file only you can read and are never shown again.",
          )}
        </p>
        {view && (
          <>
            {choose(
              view.servers.length === 0,
              <p
                class="settings-card-note text-xs text-[#7a7a8c]"
                data-testid="inference-empty"
              >
                {t("No servers yet.")}
              </p>,
              <ul data-testid="inference-list">
                {view.servers.map((s) => (
                  <ServerRow
                    key={s.id}
                    api={api}
                    server={s}
                    busy={busy}
                    change={change}
                    canSave={view.can_save}
                    onEdit={setEditing}
                  />
                ))}
              </ul>,
            )}
            <ServerForm
              key={(editing?.id ?? "new") + formRun}
              api={api}
              view={view}
              busy={busy}
              change={change}
              editing={editing}
              onDone={done}
            />
            <p class="settings-card-note text-xs text-[#7a7a8c]">
              {choose(
                view.can_save,
                tf("Up to {max} servers.", { max: String(view.max) }),
                t(
                  "The gateway runs on its built-in defaults, without a configuration file, so servers cannot be saved here.",
                ),
              )}
            </p>
          </>
        )}
        {err && (
          <p
            class="settings-card-note text-xs text-danger"
            role="alert"
            data-testid="inference-error"
          >
            {err}
          </p>
        )}
      </div>
    </section>
  );
}
