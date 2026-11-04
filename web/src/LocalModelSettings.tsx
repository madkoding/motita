import { useCallback, useEffect, useRef, useState } from "preact/hooks";
import { t, tf } from "./i18n";
import { choose } from "./choose";
import type { Api } from "./llmApi";
import {
  type LocalStatus,
  busy,
  gigabytes,
  installed,
  localCancel,
  localDelete,
  localInstall,
  localStart,
  localStatus,
  localStop,
  percent,
  removable,
  serverProblem,
  vram,
} from "./localModelApi";

const btn =
  "btn-outline px-3 min-h-[32px] rounded-lg border border-white/10 text-xs text-[#e8e8ea] hover:bg-white/5 active:scale-95 transition-transform disabled:opacity-40 disabled:cursor-not-allowed";
const primary = btn + " bg-accent text-white border-transparent";

// POLL_MS is how often the status is read while it changes on its own.
const POLL_MS = 1000;

export interface LocalModelSettingsProps {
  api: Api;
  writable: boolean;
  onChanged: () => void;
  onNotice: (message: string, error?: boolean) => void;
}

// reasonText says why this computer cannot run the local model.
function reasonText(s: LocalStatus): string {
  if (s.reason === "low_vram" && s.gpu) {
    return tf(
      "This GPU has {have} of memory; the local model needs at least {need}.",
      { have: vram(s.gpu.total_mb), need: "4 GB" },
    );
  }
  return t(
    "No GPU with readable memory was found: the local model needs at least 4 GB of GPU memory.",
  );
}

// gpuText is the GPU line.
function gpuText(s: LocalStatus): string {
  if (!s.gpu) {
    return t("No GPU found");
  }
  return tf("{name} · {size} of GPU memory", {
    name: s.gpu.name,
    size: vram(s.gpu.total_mb),
  });
}

// phaseText says what is being downloaded.
function phaseText(s: LocalStatus): string {
  const what = choose(
    s.task.phase === "model",
    t("Downloading the model…"),
    t("Downloading the runtime (llama.cpp)…"),
  );
  return tf("{what} {pct}%", { what, pct: String(percent(s.task)) });
}

// serverText is the server's state once installed.
function serverText(s: LocalStatus): string {
  if (s.server.running) {
    return tf("Running · {ctx} tokens of context · port {port}", {
      ctx: String(s.server.context),
      port: String(s.server.port),
    });
  }
  return choose(
    s.server.starting,
    t("Loading the model into the GPU…"),
    t("Installed · stopped"),
  );
}

// serverUp is a server that runs or is loading.
function serverUp(s: LocalStatus): boolean {
  return !!(s.server.running || s.server.starting);
}

// LocalFacts is what the card says: the GPU, why not, the download, the server.
function LocalFacts({ st }: { st: LocalStatus }) {
  return (
    <>
      <p>
        {tf(
          "Runs {model} on this computer's GPU, as one more provider. Nothing is downloaded or started until you press a button.",
          { model: st.model_name },
        )}
      </p>
      <p class="text-[#e8e8ea]" data-testid="llm-local-gpu">
        {gpuText(st)}
      </p>
      {!st.eligible && (
        <p class="text-warning" data-testid="llm-local-reason">
          {reasonText(st)}
        </p>
      )}
      {st.task.running && (
        <div data-testid="llm-local-progress">
          <p>{phaseText(st)}</p>
          <progress
            class="settings-progress"
            max={100}
            value={percent(st.task)}
          />
        </div>
      )}
      {st.task.error && (
        <p class="text-danger" role="alert">
          {st.task.error}
        </p>
      )}
      {installed(st) && (
        <p class="text-[#e8e8ea]" data-testid="llm-local-server">
          {serverText(st)}
        </p>
      )}
      {serverProblem(st.server) && (
        <pre class="text-danger whitespace-pre-wrap" role="alert">
          {serverProblem(st.server)}
        </pre>
      )}
    </>
  );
}

type Act = (
  fn: (api: Api) => Promise<LocalStatus>,
  done?: string,
) => () => void;

// LocalSteps are the buttons that move the model along: download, cancel, start, stop.
function LocalSteps({
  st,
  writable,
  act,
}: {
  st: LocalStatus;
  writable: boolean;
  act: Act;
}) {
  const blocked = !st.eligible || !writable;
  if (st.task.running) {
    return (
      <button type="button" class={btn} onClick={act(localCancel)}>
        {t("Cancel")}
      </button>
    );
  }
  if (!installed(st)) {
    return (
      <button
        type="button"
        class={primary}
        disabled={blocked}
        onClick={act(localInstall)}
      >
        {tf("Download ({size})", { size: gigabytes(st.size) })}
      </button>
    );
  }
  if (serverUp(st)) {
    return (
      <button type="button" class={btn} onClick={act(localStop)}>
        {t("Stop")}
      </button>
    );
  }
  return (
    <button
      type="button"
      class={primary}
      disabled={blocked}
      onClick={act(localStart)}
    >
      {t("Start")}
    </button>
  );
}

// LocalDelete asks once, then deletes the model and its runtime.
function LocalDelete({
  st,
  confirming,
  onAsk,
  onDelete,
}: {
  st: LocalStatus;
  confirming: boolean;
  onAsk: () => void;
  onDelete: () => void;
}) {
  if (confirming) {
    return (
      <button type="button" class={btn + " text-danger"} onClick={onDelete}>
        {tf("Delete {size} from this computer", { size: gigabytes(st.size) })}
      </button>
    );
  }
  if (st.task.running || !removable(st)) {
    return null;
  }
  return (
    <button type="button" class={btn} onClick={onAsk}>
      {t("Delete")}
    </button>
  );
}

// LocalModelSettings is Motita's own local model: one model on this computer's GPU, downloaded,
// started, stopped and deleted from here. Nothing happens until a button is pressed.
export function LocalModelSettings({
  api,
  writable,
  onChanged,
  onNotice,
}: LocalModelSettingsProps) {
  const [st, setSt] = useState<LocalStatus | null>(null);
  const [err, setErr] = useState("");
  const [confirming, setConfirming] = useState(false);
  const wasRunning = useRef(false);
  const take = useCallback(
    (s: LocalStatus) => {
      setSt(s);
      setErr("");
      // The provider connects when the server starts answering: the list reads it again.
      if (s.server.running !== wasRunning.current) {
        wasRunning.current = s.server.running;
        onChanged();
      }
    },
    [onChanged],
  );
  const read = useCallback(() => {
    localStatus(api)
      .then(take)
      .catch((e: Error) => setErr(e.message));
  }, [api, take]);
  useEffect(read, [read]);
  const polling = !!st && busy(st);
  useEffect(() => {
    const id = polling ? setInterval(read, POLL_MS) : undefined;
    return () => clearInterval(id);
  }, [polling, read]);
  const act = useCallback<Act>(
    (fn, done) => () => {
      setConfirming(false);
      fn(api)
        .then((s) => {
          take(s);
          if (done) {
            onNotice(done);
          }
        })
        .catch((e: Error) => setErr(e.message));
    },
    [api, take, onNotice],
  );
  const ask = useCallback(() => setConfirming(true), []);
  const remove = act(localDelete, t("Local model deleted"));
  return (
    <section
      class="settings-group"
      aria-labelledby="llm-local-h"
      data-testid="llm-local"
    >
      <h4 id="llm-local-h" class="settings-group-title">
        {t("Local model")}
      </h4>
      <div class="settings-card p-3 text-xs text-[#9a9aaa]">
        {!st && !err && <output class="block">{t("Loading…")}</output>}
        {err && (
          <p class="text-danger mb-2" role="alert">
            {err}
          </p>
        )}
        {st && (
          <div class="flex flex-col gap-2">
            <LocalFacts st={st} />
            <div class="flex flex-wrap gap-2">
              <LocalSteps st={st} writable={writable} act={act} />
              <LocalDelete
                st={st}
                confirming={confirming}
                onAsk={ask}
                onDelete={remove}
              />
            </div>
            <p class="text-[#7a7a8c]">
              {tf("Saved in {dir}", { dir: st.dir })}
            </p>
          </div>
        )}
      </div>
    </section>
  );
}
