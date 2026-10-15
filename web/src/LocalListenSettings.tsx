import { useCallback, useEffect, useState } from "preact/hooks";
import { lang, t, tf } from "./i18n";
import { choose } from "./choose";
import { errorMessage } from "./errors";
import { type LocalCatalog, type LocalModel, type LoadState, deleteVoice, fetchCatalog, installVoice } from "./localVoice";
import { isWhisper } from "./localListenCore";
import { forGPU, listenStateOf, onListenState, warmListener } from "./localListen";
import { DOWNLOAD_POLL_MS, megabytes, qualityLabel, useWebGPU } from "./LocalVoiceSettings";
import type { VoicePrefs } from "./voice";
import type { Api } from "./prApi";
import { Collapsed, Row, primaryBtn, selectCls, smallBtn } from "./SettingsControls";

// LISTEN_LANGUAGES are the languages the settings offer to say which one is spoken; Whisper
// knows many more, and tells them apart itself with "".
export const LISTEN_LANGUAGES = ["es", "en", "pt", "fr", "de", "it"];

// langName is a language's name in the interface's, or its code where the browser has none.
function langName(code: string): string {
  try {
    const name = new Intl.DisplayNames([lang()], { type: "language" }).of(code) as string;
    return name.charAt(0).toLocaleUpperCase(lang()) + name.slice(1);
  } catch {
    return code;
  }
}

// useListenState is how far a model is from being ready in this browser, kept current.
function useListenState(id: string): LoadState | undefined {
  const [, redraw] = useState(0);
  useEffect(() => onListenState(() => redraw((n) => n + 1)), []);
  return choose(id !== "", listenStateOf(id), undefined);
}

// recommendedFor is the model offered first: the small one where the graphics card runs it, the
// base one on the processor, where the small one takes several seconds a sentence.
export function recommendedFor(webGPU: boolean): string {
  return choose(webGPU, "whisper-small-gpu", "whisper-base");
}

// byPreference puts the models for the graphics card first where it can run them: they are the
// fastest there. On the processor, the smaller ones come first.
function byPreference(models: LocalModel[], webGPU: boolean): LocalModel[] {
  const gpu = models.filter((m) => forGPU(m.id));
  const cpu = models.filter((m) => !forGPU(m.id));
  return choose(webGPU, [...gpu, ...cpu], cpu);
}

// LocalListening lists the Whisper models the gateway can download to listen with in this
// browser: download one, follow its progress, choose it, or delete it, and the language spoken.
export function LocalListening({
  api,
  prefs,
  setPrefs,
}: {
  api: Api;
  prefs: VoicePrefs;
  setPrefs: (p: VoicePrefs) => void;
}) {
  const [catalog, setCatalog] = useState<LocalCatalog | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState("");
  const webGPU = useWebGPU();
  const chosen = prefs.listener;
  const pick = useCallback((listener: string) => setPrefs({ ...prefs, listener }), [prefs, setPrefs]);

  // load reads the catalog again, and answers why it could not ("" when it could).
  const load = useCallback(async (): Promise<string> => {
    let why = "";
    try {
      setCatalog(await fetchCatalog(api));
    } catch (e) {
      why = errorMessage(e);
    }
    setError(why);
    return why;
  }, [api]);
  useEffect(() => {
    void load();
  }, [load]);

  const dl = catalog?.download;
  const downloading = !!dl && !dl.error;
  // While a model downloads, the catalog is read again until it is done.
  useEffect(() => {
    if (!downloading) {
      return undefined;
    }
    const timer = setInterval(() => void load(), DOWNLOAD_POLL_MS);
    return () => clearInterval(timer);
  }, [downloading, load]);

  const models = byPreference(catalog?.models.filter((m) => isWhisper(m.id)) ?? [], webGPU);
  // The first model downloaded is the one used, when none was chosen.
  const firstReady = models.find((m) => m.installed)?.id ?? "";
  useEffect(() => {
    if (!chosen && firstReady) {
      pick(firstReady);
    }
  }, [chosen, firstReady, pick]);

  // The model in use is loaded into memory as soon as it is downloaded, and how far that is shows
  // in place of "In use" until it is ready.
  const chosenReady = models.some((m) => m.id === chosen && m.installed);
  useEffect(() => {
    if (chosenReady) {
      warmListener(chosen);
    }
  }, [chosen, chosenReady]);
  const loading = useListenState(chosen) ?? { pct: 0, ready: false };

  // act runs one request, then reads the catalog again; its failure is the one shown.
  const act = useCallback(
    async (id: string, run: () => Promise<void>) => {
      setBusy(id);
      let failure = "";
      try {
        await run();
      } catch (e) {
        failure = errorMessage(e);
      }
      setBusy("");
      const why = await load();
      setError(failure || why);
    },
    [load],
  );

  if (!catalog) {
    return (
      <div class="settings-card-note text-xs text-[#7a7a8c]" data-testid="local-listeners">
        {choose(error !== "", error, t("Loading…"))}
      </div>
    );
  }
  // The model in use and the one downloading stay in sight; the rest wait folded.
  const inSight = (m: LocalModel) => (m.id === chosen && m.installed) || (downloading && dl.id === m.id);
  const pinned = models.filter(inSight);
  const rest = models.filter((m) => !inSight(m));
  const row = (m: LocalModel) => (
    <ListenerRow
      key={m.id}
      m={m}
      chosen={chosen}
      pick={pick}
      loading={loading}
      dl={dl}
      busy={busy}
      act={act}
      api={api}
      recommended={m.id === recommendedFor(webGPU)}
    />
  );
  const alerts = [
    { kind: "download", text: choose(dl?.error, tf("The download failed: {error}", { error: dl?.error ?? "" }), "") },
    {
      kind: "load",
      text: choose(loading.error, tf("The model could not be loaded: {error}", { error: loading.error ?? "" }), ""),
    },
    { kind: "catalog", text: error },
  ];
  return (
    <>
      <Row
        id="settings-listen-models"
        label={t("Model to listen with")}
        stack
        hint={t(
          "Whisper runs in this browser, on WebGPU where there is one and on WebAssembly otherwise: what you say never leaves this device.",
        )}
      >
        <div class="flex flex-col gap-2 w-full" data-testid="local-listeners">
          {!catalog.runtime.installed && (
            <div class="text-xs text-[#7a7a8c]">
              {tf("The first download adds {size} for the engine.", { size: megabytes(catalog.runtime.size) })}
            </div>
          )}
          {pinned.map(row)}
          <Collapsed label={tf("Every model ({n})", { n: String(rest.length) })} testid="local-listener-list">
            {rest.map(row)}
          </Collapsed>
          {forGPU(chosen) && loading.gpuError && (
            <div class="text-xs text-[#7a7a8c]" data-testid="local-listener-gpu-note">
              {tf("WebGPU could not start here ({reason}): the model runs on the processor.", {
                reason: loading.gpuError,
              })}
            </div>
          )}
          {alerts.map(
            ({ kind, text }) =>
              text && (
                <div key={kind} class="text-xs text-[#f87171]" role="alert">
                  {text}
                </div>
              ),
          )}
        </div>
      </Row>
      <Row id="settings-listen-language" label={t("Language you speak")}>
        <select
          class={selectCls}
          aria-labelledby="settings-listen-language"
          data-testid="listen-language"
          value={prefs.language}
          onChange={(e) => setPrefs({ ...prefs, language: (e.target as HTMLSelectElement).value })}
        >
          <option value="">{t("Detect it")}</option>
          {LISTEN_LANGUAGES.map((code) => (
            <option key={code} value={code}>
              {langName(code)}
            </option>
          ))}
        </select>
      </Row>
    </>
  );
}

// The download the gateway is busy with, as the catalog reports it.
type Download = LocalCatalog["download"];

// ListenerRow is one model of the list: its name and details, and what can be done with it.
function ListenerRow({
  m,
  chosen,
  pick,
  loading,
  dl,
  busy,
  act,
  api,
  recommended,
}: {
  m: LocalModel;
  chosen: string;
  pick: (id: string) => void;
  loading: LoadState;
  dl: Download | undefined;
  busy: string;
  act: (id: string, run: () => Promise<void>) => Promise<void>;
  api: Api;
  recommended: boolean;
}) {
  const where = choose(forGPU(m.id), "WebGPU", t("Processor"));
  const onGPU = m.id === chosen && !!loading.gpu;
  return (
    <div class="flex items-center justify-between gap-3" data-testid={"local-listener-" + m.id}>
      <div class="min-w-0">
        <div class="text-sm text-[#e8e8ea] truncate">
          {m.name} · {where}
        </div>
        <div class="text-xs text-[#7a7a8c]">
          {qualityLabel(m.quality)} · {megabytes(m.size)}
          {choose(onGPU, " · " + t("on the graphics card"), "")}
          {choose(recommended, " · " + t("Recommended"), "")}
        </div>
      </div>
      <ListenerState m={m} chosen={chosen} pick={pick} loading={loading} dl={dl} busy={busy} act={act} api={api} />
    </div>
  );
}

// ListenerState is what can be done with a model: follow its download, use or delete it once it
// is downloaded, or download it.
function ListenerState({
  m,
  chosen,
  pick,
  loading,
  dl,
  busy,
  act,
  api,
}: {
  m: LocalModel;
  chosen: string;
  pick: (id: string) => void;
  loading: LoadState;
  dl: Download | undefined;
  busy: string;
  act: (id: string, run: () => Promise<void>) => Promise<void>;
  api: Api;
}) {
  const downloading = !!dl && !dl.error;
  if (downloading && dl.id === m.id) {
    const pct = Math.floor((100 * dl.done) / Math.max(dl.total, 1));
    return (
      <span class="flex items-center gap-2 shrink-0" data-testid={"local-listener-progress-" + m.id}>
        <progress class="settings-progress" max={100} value={pct} />
        <output class="text-xs text-[#a0a0b0]">{tf("Downloading… {pct}%", { pct: String(pct) })}</output>
      </span>
    );
  }
  if (!m.installed) {
    return (
      <button
        type="button"
        class={smallBtn}
        disabled={downloading || busy !== ""}
        data-testid={"local-listener-get-" + m.id}
        onClick={() => void act(m.id, () => installVoice(api, m.id))}
      >
        {t("Download")}
      </button>
    );
  }
  const inUse = chosen === m.id;
  return (
    <span class="flex items-center gap-2 shrink-0">
      <UseListener m={m} inUse={inUse} pick={pick} loading={loading} />
      <button
        type="button"
        class={smallBtn}
        disabled={busy !== ""}
        data-testid={"local-listener-delete-" + m.id}
        onClick={() =>
          // The choice goes once the catalog no longer has it: another one downloaded takes its place.
          void act(m.id, () => deleteVoice(api, m.id)).then(() => {
            if (inUse) {
              pick("");
            }
          })
        }
      >
        {t("Delete")}
      </button>
    </span>
  );
}

// UseListener chooses a downloaded model; for the model in use it is how far it is from being
// loaded into memory, or a way to try again when it could not be.
function UseListener({
  m,
  inUse,
  pick,
  loading,
}: {
  m: LocalModel;
  inUse: boolean;
  pick: (id: string) => void;
  loading: LoadState;
}) {
  if (inUse && loading.error) {
    return (
      <button
        type="button"
        class={smallBtn}
        title={loading.error}
        data-testid={"local-listener-retry-" + m.id}
        onClick={() => warmListener(m.id)}
      >
        {t("Try again")}
      </button>
    );
  }
  if (inUse && !loading.ready) {
    return (
      <span class="flex items-center gap-2" data-testid={"local-listener-loading-" + m.id}>
        <progress class="settings-progress" max={100} value={loading.pct} />
        <output class="text-xs text-[#a0a0b0]">
          {tf("Loading into memory… {pct}%", { pct: String(loading.pct) })}
        </output>
      </span>
    );
  }
  return (
    <button
      type="button"
      class={choose(inUse, primaryBtn, smallBtn)}
      aria-pressed={inUse}
      data-testid={"local-listener-use-" + m.id}
      onClick={() => pick(m.id)}
    >
      {choose(inUse, t("In use"), t("Use"))}
    </button>
  );
}
