import { useCallback, useEffect, useRef, useState } from "preact/hooks";
import { lang, t, tf } from "./i18n";
import { choose } from "./choose";
import { errorMessage, workerError } from "./errors";
import {
  type LocalCatalog,
  type LocalModel,
  type LoadState,
  deleteVoice,
  fetchCatalog,
  installVoice,
  loadStateOf,
  localVoicesSupported,
  storageKept,
  onLoadState,
  sampleOf,
  warmLocal,
} from "./localVoice";
import { gpuAllowed } from "./gpuPolicy";
import { isChatterbox, isSupertonic } from "./localVoiceCore";
import { isBrowserLLM } from "./browserLLMCore";
import { isWhisper } from "./localListenCore";
import type { Api } from "./prApi";
import { Collapsed, inputCls, primaryBtn, smallBtn } from "./SettingsControls";

// How often the catalog is read again while a voice downloads.
export const DOWNLOAD_POLL_MS = 1000;

// megabytes writes a size as people read it: 63 MB, and 1 MB for anything smaller.
export function megabytes(n: number): string {
  return Math.max(1, Math.round(n / 1e6)).toLocaleString(lang()) + " MB";
}

// MANY_LANGS is the language of a voice that speaks many (Supertonic's).
const MANY_LANGS = "mul";

// ENGINES name the engines besides Piper, as a voice's details start.
const ENGINES: Record<string, string> = {
  supertonic: "Supertonic · ",
  chatterbox: "Chatterbox · ",
};

// LANGUAGES_OF is how many languages a voice that speaks many speaks here, by its engine.
const LANGUAGES_OF: Record<string, number> = { supertonic: 31, chatterbox: 21 };

// qualityLabel names a voice's quality.
export function qualityLabel(q: string): string {
  const labels: Record<string, string> = {
    high: t("High quality"),
    medium: t("Medium quality"),
    low: t("Low quality"),
    "x-low": t("Very low quality"),
  };
  return labels[q] ?? q;
}

// baseLang is a voice's language without its region: es for es-MX.
function baseLang(m: LocalModel): string {
  return m.lang.toLowerCase().split("-")[0];
}

// langName is a language's name in the interface's, capitalised as a list item, or its code
// where the browser has none.
function langName(code: string): string {
  try {
    // With its default fallback, of() answers the code itself for a language it cannot name.
    const name = new Intl.DisplayNames([lang()], { type: "language" }).of(
      code,
    ) as string;
    return name.charAt(0).toLocaleUpperCase(lang()) + name.slice(1);
  } catch {
    return code;
  }
}

// byLanguage puts the voices of the interface language first.
function byLanguage(models: LocalModel[]): LocalModel[] {
  const mine = (m: LocalModel) => baseLang(m) === lang();
  return [...models.filter(mine), ...models.filter((m) => !mine(m))];
}

// QUALITY_FILTERS are the qualities the list can be narrowed to; low counts the very low too.
const QUALITY_FILTERS = ["high", "medium", "low"];

// FILTERS_KEY keeps the list's filters from one visit to the next.
export const FILTERS_KEY = "motita.ui.localVoiceFilters";

// Filters narrow the list: "" is every language, or every quality.
export interface Filters {
  language: string;
  quality: string;
}

// loadFilters reads the filters kept last time; the list opens on the interface's language
// when there are none.
export function loadFilters(): Filters {
  const none = { language: lang(), quality: "" };
  try {
    const raw = JSON.parse(localStorage.getItem(FILTERS_KEY) || "{}");
    return {
      language: choose(
        typeof raw.language === "string",
        raw.language,
        none.language,
      ),
      quality: choose(QUALITY_FILTERS.includes(raw.quality), raw.quality, ""),
    };
  } catch {
    return none;
  }
}

// saveFilters keeps the filters; a browser that cannot store them forgets them.
export function saveFilters(f: Filters): void {
  try {
    localStorage.setItem(FILTERS_KEY, JSON.stringify(f));
  } catch {
    // Nothing to do: the filters are only a convenience.
  }
}

// useWebGPU says whether this browser has WebGPU and a graphics card it can use, which Chatterbox
// needs: false until the browser answers.
export function useWebGPU(): boolean {
  const [has, setHas] = useState(false);
  useEffect(() => {
    const gpu = (
      navigator as Navigator & { gpu?: { requestAdapter(): Promise<unknown> } }
    ).gpu;
    // A phone does not offer WebGPU to the voices, unless the reader forced it.
    choose(gpuAllowed(), gpu, undefined)
      ?.requestAdapter()
      .then((a) => setHas(!!a))
      .catch(() => undefined);
  }, []);
  return has;
}

// shown says whether a voice passes the filters ("" lets every one through).
function shown(m: LocalModel, language: string, quality: string): boolean {
  const q = choose(m.quality === "x-low", "low", m.quality);
  // A voice that speaks many languages speaks the one chosen too.
  const speaks =
    language === "" || baseLang(m) === language || m.lang === MANY_LANGS;
  return speaks && (quality === "" || q === quality);
}

// useSample plays one voice's sample at a time: play starts one (stopping any other), or stops
// the one playing. playing is the voice heard now, or loading.
function useSample(api: Api) {
  const [playing, setPlaying] = useState("");
  const [error, setError] = useState("");
  const audio = useRef<HTMLAudioElement | null>(null);
  const stop = useCallback(() => {
    audio.current?.pause();
    audio.current = null;
    setPlaying("");
  }, []);
  useEffect(() => stop, [stop]);
  const play = useCallback(
    async (id: string) => {
      const again = playing === id;
      stop();
      if (again) {
        return;
      }
      setError("");
      setPlaying(id);
      try {
        const url = URL.createObjectURL(await sampleOf(api, id));
        const a = new Audio(url);
        audio.current = a;
        a.onended = () => {
          URL.revokeObjectURL(url);
          if (audio.current === a) {
            stop();
          }
        };
        await a.play();
      } catch (e) {
        setPlaying("");
        setError(
          tf("The sample could not be played: {error}", {
            error: errorMessage(e),
          }),
        );
      }
    },
    [api, playing, stop],
  );
  return { playing, play, error };
}

// voicesOf is the catalog without the models speech input listens with (LocalListenSettings.tsx).
function voicesOf(catalog: LocalCatalog): LocalCatalog {
  return { ...catalog, models: catalog.models.filter((m) => !isWhisper(m.id) && !isBrowserLLM(m.id)) };
}

// useCatalog is the gateway's catalog of local models as shape keeps them, read again on its own
// while one downloads, with the request about a model that is running (busy) and the last failure.
// shape must keep its identity between renders: a function declared at the top of a module.
export function useCatalog(api: Api, shape: (c: LocalCatalog) => LocalCatalog) {
  const [catalog, setCatalog] = useState<LocalCatalog | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState("");

  // load reads the catalog again, and answers why it could not ("" when it could).
  const load = useCallback(async (): Promise<string> => {
    let why = "";
    try {
      setCatalog(shape(await fetchCatalog(api)));
    } catch (e) {
      why = errorMessage(e);
    }
    setError(why);
    return why;
  }, [api, shape]);
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

  // act runs one request about a model, then reads the catalog again; its failure is the one shown.
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
  return { catalog, error, busy, downloading, act };
}

// useLoadState is how far a voice is from being ready in this browser, kept current.
function useLoadState(id: string): LoadState | undefined {
  const [, redraw] = useState(0);
  useEffect(() => onLoadState(() => redraw((n) => n + 1)), []);
  return choose(id !== "", loadStateOf(id), undefined);
}

// KeptNote says, once a model is downloaded, whether the browser will keep it: when it will not,
// it may delete the copy when the disk runs short, and the model downloads again.
export function KeptNote({ catalog }: { catalog: LocalCatalog }) {
  const [kept, setKept] = useState(true);
  const any = catalog.models.some((m) => m.installed);
  useEffect(() => {
    if (any) {
      void storageKept().then(setKept);
    }
  }, [any]);
  if (!any) {
    return null;
  }
  return (
    <div class="text-xs text-[#7a7a8c]" data-testid="local-kept-note">
      {choose(
        kept,
        t(
          "This browser keeps the downloaded models, also when you update Motita.",
        ),
        t(
          "This browser may delete the downloaded models if the disk runs short; they download again. Installing Motita as an app helps it keep them.",
        ),
      )}
    </div>
  );
}

// LocalVoices lists the voices the gateway can download to run in this browser: download one,
// follow its progress, choose it, or delete it. A Chatterbox voice in use has its emotion setting.
export function LocalVoices({
  api,
  chosen,
  choose: pick,
  emotion = 0.5,
  setEmotion = () => undefined,
}: {
  api: Api;
  chosen: string;
  choose: (id: string) => void;
  // emotion is how much emotion a Chatterbox voice puts in.
  emotion?: number;
  setEmotion?: (n: number) => void;
}) {
  const { catalog, error, busy, downloading, act } = useCatalog(api, voicesOf);
  // The list opens on the filters chosen last time.
  const [filters, setFilters] = useState<Filters>(loadFilters);
  const { language, quality } = filters;
  const filter = (f: Partial<Filters>) => {
    const next = { ...filters, ...f };
    setFilters(next);
    saveFilters(next);
  };
  const sample = useSample(api);

  const dl = catalog?.download;

  // The first voice downloaded is the one used, when none was chosen, or when the one chosen
  // is Chatterbox on a device that does not give it WebGPU (a phone, say): it would only fail.
  useEffect(() => {
    if (chosen && !(isChatterbox(chosen) && !gpuAllowed())) {
      return;
    }
    const ready = catalog?.models.find(
      (m) => m.installed && !isChatterbox(m.id),
    );
    if (ready) {
      pick(ready.id);
    }
  }, [catalog, chosen, pick]);

  // The voice in use is loaded into memory as soon as it is downloaded, and how far that is shows
  // in place of "In use" until it is ready.
  const chosenReady = !!catalog?.models.some(
    (m) => m.id === chosen && m.installed,
  );
  // Before the first word from the worker, a voice that will load counts as loading.
  const pending: LoadState | undefined = choose(
    localVoicesSupported(),
    { pct: 0, ready: false },
    undefined,
  );
  const loading = useLoadState(chosen) ?? pending;
  const webGPU = useWebGPU();
  useEffect(() => {
    if (chosenReady && localVoicesSupported()) {
      warmLocal(chosen);
    }
  }, [chosen, chosenReady]);

  if (!catalog) {
    return (
      <div class="text-xs text-[#7a7a8c]" data-testid="local-voices">
        {choose(error !== "", error, t("Loading…"))}
      </div>
    );
  }
  // Chatterbox runs only on the graphics card: it is offered where the browser has one.
  const voices = catalog.models.filter(
    (m) => m.engine !== "chatterbox" || webGPU,
  );
  const languages = [...new Set(byLanguage(voices).map(baseLang))];
  const visible = listed(voices, languages, filters, [chosen, dl?.id]);
  // The voice in use and the one downloading stay in sight; the rest wait folded, with the filters.
  const inSight = (m: LocalModel) =>
    (m.id === chosen && m.installed) || (downloading && dl?.id === m.id);
  const pinned = visible.filter(inSight);
  const rest = visible.filter((m) => !inSight(m));
  const row = (m: LocalModel) => (
    <VoiceRow
      key={m.id}
      m={m}
      chosen={chosen}
      pick={pick}
      loading={loading}
      dl={dl}
      busy={busy}
      sample={sample}
      act={act}
      api={api}
    />
  );
  return (
    <div class="flex flex-col gap-2 w-full" data-testid="local-voices">
      {!catalog.runtime.installed && (
        <div class="text-xs text-[#7a7a8c]">
          {tf("The first download adds {size} for the voice engine.", {
            size: megabytes(catalog.runtime.size),
          })}
        </div>
      )}
      {pinned.map(row)}
      <KeptNote catalog={catalog} />
      <Collapsed
        label={tf("Every voice ({n})", { n: String(voices.length) })}
        testid="local-voice-list"
      >
        <div class="flex flex-wrap gap-2" data-testid="local-voice-filters">
          <select
            class={inputCls}
            aria-label={t("Language")}
            data-testid="local-voice-language"
            value={language}
            onChange={(e) =>
              filter({ language: (e.target as HTMLSelectElement).value })
            }
          >
            <option value="">{t("Every language")}</option>
            {languages.map((code) => (
              <option key={code} value={code}>
                {langName(code)}
              </option>
            ))}
          </select>
          <select
            class={inputCls}
            aria-label={t("Quality")}
            data-testid="local-voice-quality"
            value={quality}
            onChange={(e) =>
              filter({ quality: (e.target as HTMLSelectElement).value })
            }
          >
            <option value="">{t("Every quality")}</option>
            {QUALITY_FILTERS.map((q) => (
              <option key={q} value={q}>
                {qualityLabel(q)}
              </option>
            ))}
          </select>
        </div>
        {rest.length === 0 && (
          <div class="text-xs text-[#7a7a8c]">
            {t("No voice matches these filters.")}
          </div>
        )}
        {rest.map(row)}
      </Collapsed>
      {isSupertonic(chosen) && loading?.gpuError && (
        <div class="text-xs text-[#7a7a8c]" data-testid="local-voice-gpu-note">
          {tf(
            "WebGPU could not start here ({reason}): Supertonic runs on the processor.",
            { reason: workerError(loading.gpuError) },
          )}
        </div>
      )}
      {isChatterbox(chosen) && <Emotion value={emotion} set={setEmotion} />}
      <Failures
        sample={sample.error}
        download={dl?.error}
        load={loading?.error}
        catalog={error}
      />
    </div>
  );
}

// listed are the voices the list shows, in the order it shows them: those the filters let
// through, and the ones kept whatever the filters say (the voice in use, the one downloading).
function listed(
  voices: LocalModel[],
  languages: string[],
  filters: Filters,
  kept: (string | undefined)[],
): LocalModel[] {
  const language = choose(
    languages.includes(filters.language),
    filters.language,
    "",
  );
  return byLanguage(voices).filter(
    (m) => kept.includes(m.id) || shown(m, language, filters.quality),
  );
}

// The download the gateway is busy with, as the catalog reports it.
type Download = LocalCatalog["download"];
// act runs one request about a voice, then reads the catalog again.
type Act = (id: string, run: () => Promise<void>) => Promise<void>;

// VoiceRow is one voice of the list: its sample, its name and details, and what can be done with it.
function VoiceRow({
  m,
  chosen,
  pick,
  loading,
  dl,
  busy,
  sample,
  act,
  api,
}: {
  m: LocalModel;
  chosen: string;
  pick: (id: string) => void;
  loading: LoadState | undefined;
  dl: Download | undefined;
  busy: string;
  sample: ReturnType<typeof useSample>;
  act: Act;
  api: Api;
}) {
  const hearing = sample.playing === m.id;
  return (
    <div
      class="flex items-center justify-between gap-3"
      data-testid={"local-voice-" + m.id}
    >
      <div class="flex items-center gap-2 min-w-0">
        {!m.sample && <span class="w-[32px] shrink-0" aria-hidden="true" />}
        {m.sample && (
          <button
            type="button"
            class={smallBtn + " w-[32px] shrink-0 px-0"}
            aria-label={choose(
              hearing,
              t("Stop the sample"),
              tf("Hear {name}", { name: m.name }),
            )}
            aria-pressed={hearing}
            data-testid={"local-voice-sample-" + m.id}
            onClick={() => void sample.play(m.id)}
          >
            {choose(hearing, "■", "▶")}
          </button>
        )}
        <div class="min-w-0">
          <div class="text-sm text-[#e8e8ea] truncate">
            {m.name} ·{" "}
            {choose(
              m.lang === MANY_LANGS,
              tf("{n} languages", { n: String(LANGUAGES_OF[m.engine] ?? "") }),
              m.lang,
            )}
          </div>
          <div class="text-xs text-[#7a7a8c]">
            {ENGINES[m.engine] ?? ""}
            {qualityLabel(m.quality)} · {megabytes(m.size)}
            {choose(m.id === chosen && !!loading?.gpu, " · WebGPU", "")}
          </div>
        </div>
      </div>
      <VoiceState
        m={m}
        chosen={chosen}
        pick={pick}
        loading={loading}
        dl={dl}
        busy={busy}
        act={act}
        api={api}
      />
    </div>
  );
}

// VoiceState is what can be done with a voice: follow its download, use or delete it once it is
// downloaded, or download it.
function VoiceState({
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
  loading: LoadState | undefined;
  dl: Download | undefined;
  busy: string;
  act: Act;
  api: Api;
}) {
  const downloading = !!dl && !dl.error;
  if (downloading && dl.id === m.id) {
    const pct = Math.floor((100 * dl.done) / Math.max(dl.total, 1));
    return (
      <output
        class="text-xs text-[#a0a0b0]"
        data-testid={"local-voice-progress-" + m.id}
      >
        {tf("Downloading… {pct}%", { pct: String(pct) })}
      </output>
    );
  }
  if (!m.installed) {
    return (
      <button
        type="button"
        class={smallBtn}
        disabled={downloading || busy !== ""}
        data-testid={"local-voice-get-" + m.id}
        onClick={() => void act(m.id, () => installVoice(api, m.id))}
      >
        {t("Download")}
      </button>
    );
  }
  return (
    <span class="flex items-center gap-2">
      <UseVoice m={m} inUse={chosen === m.id} pick={pick} loading={loading} />
      <button
        type="button"
        class={smallBtn}
        disabled={busy !== ""}
        data-testid={"local-voice-delete-" + m.id}
        onClick={() =>
          void act(m.id, async () => {
            await deleteVoice(api, m.id);
            if (chosen === m.id) {
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

// UseVoice chooses a downloaded voice; for the voice in use it is how far it is from being loaded
// into memory, or a way to try again when it could not be.
function UseVoice({
  m,
  inUse,
  pick,
  loading,
}: {
  m: LocalModel;
  inUse: boolean;
  pick: (id: string) => void;
  loading: LoadState | undefined;
}) {
  if (inUse && loading?.error) {
    return (
      <button
        type="button"
        class={smallBtn}
        title={loading.error}
        data-testid={"local-voice-retry-" + m.id}
        onClick={() => warmLocal(m.id)}
      >
        {t("Try again")}
      </button>
    );
  }
  if (inUse && loading && !loading.ready) {
    return (
      <output
        class="text-xs text-[#a0a0b0]"
        data-testid={"local-voice-loading-" + m.id}
      >
        {tf("Loading into memory… {pct}%", { pct: String(loading.pct) })}
      </output>
    );
  }
  return (
    <button
      type="button"
      class={choose(inUse, primaryBtn, smallBtn)}
      aria-pressed={inUse}
      data-testid={"local-voice-use-" + m.id}
      onClick={() => pick(m.id)}
    >
      {choose(inUse, t("In use"), t("Use"))}
    </button>
  );
}

// Failures are the alerts below the list: why a sample could not play, the download failed, the
// voice in use could not be loaded, or the catalog could not be read.
function Failures({
  sample,
  download,
  load,
  catalog,
}: {
  sample: string;
  download: string | undefined;
  load: string | undefined;
  catalog: string;
}) {
  const alerts = [
    { kind: "sample", text: sample },
    {
      kind: "download",
      text: choose(
        download,
        tf("The download failed: {error}", { error: download ?? "" }),
        "",
      ),
    },
    {
      kind: "load",
      text: choose(
        load,
        tf("The voice could not be loaded: {error}", {
          error: workerError(load ?? ""),
        }),
        "",
      ),
    },
    { kind: "catalog", text: catalog },
  ];
  return (
    <>
      {alerts.map(
        ({ kind, text }) =>
          text && (
            <div key={kind} class="text-xs text-[#f87171]" role="alert">
              {text}
            </div>
          ),
      )}
    </>
  );
}

// EMOTION_STEP is how far one step of the emotion slider moves.
const EMOTION_STEP = 0.05;

// Emotion sets how much emotion a Chatterbox voice puts in: its own default is 0.5; more is more
// expressive (and a little faster).
function Emotion({ value, set }: { value: number; set: (n: number) => void }) {
  return (
    <div class="flex flex-col gap-1" data-testid="local-voice-emotion">
      <label class="flex items-center gap-3 text-sm text-[#e8e8ea]">
        {t("Emotion")}
        <input
          type="range"
          class="flex-1 accent-[var(--color-accent,#4cc2ff)]"
          data-testid="local-voice-emotion-amount"
          min={0.25}
          max={2}
          step={EMOTION_STEP}
          value={value}
          onInput={(e) => set(Number((e.target as HTMLInputElement).value))}
        />
        <span class="w-10 text-right text-xs text-[#a0a0b0] tabular-nums">
          {value.toLocaleString(lang())}
        </span>
      </label>
      <div class="text-xs text-[#7a7a8c]">
        {t(
          "How much emotion the voice puts in: 0.5 is its own; more is livelier and a little faster.",
        )}
      </div>
    </div>
  );
}
