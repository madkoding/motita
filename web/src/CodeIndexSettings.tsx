import { useCallback, useEffect, useState } from "preact/hooks";
import { t, tf } from "./i18n";
import type { Api } from "./gitApi";
import { choose } from "./choose";
import { errorMessage } from "./errors";
import {
  type CodeIndexChange,
  type CodeIndexView,
  clearCodeIndexCache,
  getCodeIndex,
  looksLocal,
  setCodeIndex,
} from "./codeIndexApi";
import { Row, Segmented, inputCls, primaryBtn } from "./SettingsControls";
import { InlineConfirm } from "./NetworkSettings";

type OnOff = "on" | "off";

// embeddingsStatus says where the vectors come from now.
function embeddingsStatus(v: CodeIndexView): string {
  if (v.embeddings === "endpoint") {
    const args = { endpoint: v.embeddings_url, model: v.embeddings_model };
    if (v.endpoint_local) {
      return tf(
        "Embeddings come from {endpoint} ({model}), on this machine or its network.",
        args,
      );
    }
    return tf(
      "Embeddings come from {endpoint} ({model}). The text of your files and every question go there.",
      args,
    );
  }
  if (v.embeddings === "auto") {
    return t(
      "Embeddings use a local Ollama with nomic-embed-text when there is one (ollama pull nomic-embed-text). The text stays on this machine.",
    );
  }
  return t(
    "No embeddings: passages are ranked by words and by meaning learned from your code.",
  );
}

// remoteRisks is what the confirmation spells out before the text leaves the machine.
function remoteRisks(url: string): string[] {
  return [
    tf(
      "The text of the indexed files, in pieces, is sent to {endpoint} once.",
      { endpoint: url },
    ),
    t("Every question searched is sent there too."),
    t("Use it only with a provider you would already show your code to."),
  ];
}

// useCodeIndex holds the section's state: what the gateway reported and the action that saves.
function useCodeIndex(api: Api) {
  const [view, setView] = useState<CodeIndexView | null | undefined>(undefined);
  const [loadErr, setLoadErr] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let live = true;
    getCodeIndex(api)
      .then((v) => live && setView(v))
      .catch((e) => live && setLoadErr(errorMessage(e)));
    return () => {
      live = false;
    };
  }, [api]);

  const save = useCallback(
    async (change: CodeIndexChange) => {
      setBusy(true);
      setErr("");
      try {
        setView(await setCodeIndex(api, change));
      } catch (e) {
        setErr(errorMessage(e));
      }
      setBusy(false);
    },
    [api],
  );
  const [cleared, setCleared] = useState("");
  const clear = useCallback(async () => {
    setBusy(true);
    setErr("");
    setCleared("");
    try {
      const done = await clearCodeIndexCache(api);
      setCleared(
        tf(
          "{n} index files deleted ({kb} KB). The next search builds it again.",
          {
            n: String(done.removed),
            kb: String(Math.ceil(done.bytes / 1024)),
          },
        ),
      );
    } catch (e) {
      setErr(errorMessage(e));
    }
    setBusy(false);
  }, [api]);
  return { view, loadErr, err, busy, save, clear, cleared };
}

// SwitchRow is an on/off setting.
function SwitchRow({
  id,
  label,
  hint,
  value,
  disabled,
  field,
  save,
}: {
  id: string;
  label: string;
  hint: string;
  value: boolean;
  disabled: boolean;
  field: "enabled" | "semantic" | "embeddings_auto";
  save: (c: CodeIndexChange) => Promise<void>;
}) {
  const onChange = useCallback(
    (v: OnOff) => void save({ [field]: v === "on" }),
    [field, save],
  );
  return (
    <Row id={id} label={label} hint={hint} stack>
      <Segmented<OnOff>
        name={id}
        labelledBy={id}
        value={choose(value, "on", "off")}
        onChange={onChange}
        disabled={disabled}
        options={[
          { value: "on", label: t("On") },
          { value: "off", label: t("Off") },
        ]}
      />
    </Row>
  );
}

// EndpointFields are the address, the model and the limits, saved together. An address outside this
// machine and its network is never saved without a confirmation that says the text leaves.
function EndpointFields({
  view,
  busy,
  save,
}: {
  view: CodeIndexView;
  busy: boolean;
  save: (c: CodeIndexChange) => Promise<void>;
}) {
  const [url, setUrl] = useState(view.embeddings_url);
  const [model, setModel] = useState(view.embeddings_model);
  const [kb, setKb] = useState(String(view.max_file_kb));
  const [files, setFiles] = useState(String(view.max_files));
  const [asking, setAsking] = useState(false);
  const change = (): CodeIndexChange => ({
    embeddings_url: url.trim(),
    embeddings_model: model.trim(),
    max_file_kb: Number(kb),
    max_files: Number(files),
  });
  const submit = (sure: boolean) => {
    setAsking(false);
    void save({ ...change(), confirm: sure });
  };
  const press = () => {
    if (url.trim() !== "" && !looksLocal(url)) {
      setAsking(true);
      return;
    }
    submit(false);
  };
  const off = !view.can_save || busy;
  const confirm = useCallback(() => submit(true), [submit]);
  const cancel = useCallback(() => setAsking(false), []);
  return (
    <>
      <Row
        id="settings-code-index-endpoint"
        label={t("Embeddings endpoint")}
        hint={t(
          "An OpenAI-compatible /embeddings address. Empty uses only the local Ollama, if any.",
        )}
        stack
      >
        <input
          class={inputCls}
          type="url"
          aria-labelledby="settings-code-index-endpoint"
          placeholder="https://api.openai.com/v1"
          value={url}
          disabled={off}
          onInput={(e) => setUrl(e.currentTarget.value)}
        />
        <input
          class={inputCls}
          type="text"
          aria-label={t("Embeddings model")}
          placeholder="text-embedding-3-small"
          value={model}
          disabled={off}
          onInput={(e) => setModel(e.currentTarget.value)}
        />
      </Row>
      <Row
        id="settings-code-index-limits"
        label={t("Limits")}
        hint={t(
          "Files larger than this many KB, or beyond this many files, are not indexed.",
        )}
        stack
      >
        <input
          class={inputCls}
          type="number"
          min="0"
          aria-label={t("Largest file (KB)")}
          value={kb}
          disabled={off}
          onInput={(e) => setKb(e.currentTarget.value)}
        />
        <input
          class={inputCls}
          type="number"
          min="0"
          aria-label={t("Most files")}
          value={files}
          disabled={off}
          onInput={(e) => setFiles(e.currentTarget.value)}
        />
        <button type="button" class={primaryBtn} disabled={off} onClick={press}>
          {t("Save")}
        </button>
      </Row>
      {asking && (
        <InlineConfirm
          id="settings-code-index-confirm"
          testid="code-index-confirm"
          title={t("Send your code to this endpoint?")}
          lines={remoteRisks(url.trim())}
          confirmLabel={t("Send my code there")}
          danger
          busy={busy}
          onConfirm={confirm}
          onCancel={cancel}
        />
      )}
    </>
  );
}

// CacheRow deletes the index kept on disk and says what was removed.
function CacheRow({
  busy,
  clear,
  cleared,
}: {
  busy: boolean;
  clear: () => Promise<void>;
  cleared: string;
}) {
  return (
    <>
      <Row
        id="settings-code-index-cache"
        label={t("Index cache")}
        hint={t(
          "The index is kept in ~/.motita/index and rebuilt when it is gone.",
        )}
        stack
      >
        <button
          type="button"
          class={primaryBtn}
          disabled={busy}
          onClick={clear}
          data-testid="code-index-clear"
        >
          {t("Clear index cache")}
        </button>
      </Row>
      {cleared && (
        <output
          class="settings-card-note block text-xs text-[#7a7a8c]"
          data-testid="code-index-cleared"
        >
          {cleared}
        </output>
      )}
    </>
  );
}

// CodeIndexSettings is the search_index tool's settings: whether it is on, the ranking by meaning,
// where real embeddings come from, and the limits. The section stays hidden on a gateway without
// the code index API.
export function CodeIndexSettings({ api }: { api: Api }) {
  const { view, loadErr, err, busy, save, clear, cleared } = useCodeIndex(api);
  if (view === null || (view === undefined && !loadErr)) {
    return null;
  }
  const off = !view?.can_save || busy;
  return (
    <section class="settings-group" aria-labelledby="settings-code-index-h">
      <h3 id="settings-code-index-h" class="settings-group-title">
        {t("Indexing")}
      </h3>
      <div class="settings-card" data-testid="code-index-setting">
        {loadErr && (
          <p class="settings-card-note text-xs text-danger" role="alert">
            {loadErr}
          </p>
        )}
        {view && (
          <>
            <SwitchRow
              id="settings-code-index-enabled"
              label={t("Search the workspace")}
              hint={t(
                "Motita reads the folder once and answers with passages instead of whole files.",
              )}
              value={view.enabled}
              disabled={off}
              field="enabled"
              save={save}
            />
            <SwitchRow
              id="settings-code-index-semantic"
              label={t("Ranking by meaning")}
              hint={t("Learned from your own code. No network.")}
              value={view.semantic}
              disabled={off}
              field="semantic"
              save={save}
            />
            <SwitchRow
              id="settings-code-index-auto"
              label={t("Find a local Ollama")}
              hint={t(
                "Looks on this machine for nomic-embed-text. The text does not leave it.",
              )}
              value={view.embeddings_auto}
              disabled={off}
              field="embeddings_auto"
              save={save}
            />
            <p
              class="settings-card-note text-xs text-[#7a7a8c]"
              data-testid="code-index-status"
            >
              {embeddingsStatus(view)}
            </p>
            <EndpointFields
              key={view.embeddings_url + view.embeddings_model}
              view={view}
              busy={busy}
              save={save}
            />
            <CacheRow busy={busy} clear={clear} cleared={cleared} />
            <p class="settings-card-note text-xs text-[#7a7a8c]">
              {t(
                "The embeddings key goes in MOTITA_CODE_INDEX_EMBEDDINGS_API_KEY. Changes apply to new sessions.",
              )}
              {view.embeddings_key && " " + t("A key is set.")}
              {!view.can_save &&
                " " +
                  t(
                    "The gateway runs on its built-in defaults, without a configuration file, so this cannot be changed here.",
                  )}
            </p>
          </>
        )}
        {err && (
          <p
            class="settings-card-note text-xs text-danger"
            role="alert"
            data-testid="code-index-error"
          >
            {err}
          </p>
        )}
      </div>
    </section>
  );
}
