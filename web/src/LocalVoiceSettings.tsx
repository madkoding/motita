import { useCallback, useEffect, useRef, useState } from "preact/hooks";
import { lang, t, tf } from "./i18n";
import { choose } from "./choose";
import { errorMessage } from "./errors";
import {
  type LocalCatalog,
  type LocalModel,
  type LoadState,
  deleteVoice,
  fetchCatalog,
  installVoice,
  loadStateOf,
  localVoicesSupported,
  onLoadState,
  sampleOf,
  setKokoroGPU,
  warmLocal,
} from "./localVoice";
import { isKokoro } from "./localVoiceCore";
import type { Api } from "./prApi";
import { inputCls, primaryBtn, smallBtn } from "./SettingsControls";

// How often the catalog is read again while a voice downloads.
export const DOWNLOAD_POLL_MS = 1000;

// megabytes writes a size as people read it: 63 MB.
function megabytes(n: number): string {
  return Math.round(n / 1e6).toLocaleString(lang()) + " MB";
}

// qualityLabel names a voice's quality.
function qualityLabel(q: string): string {
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
    const name = new Intl.DisplayNames([lang()], { type: "language" }).of(code) ?? code;
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
      language: choose(typeof raw.language === "string", raw.language, none.language),
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

// useWebGPU says whether this browser has WebGPU and a graphics card it can use, which the
// Kokoro accelerator needs: false until the browser answers.
function useWebGPU(): boolean {
  const [has, setHas] = useState(false);
  useEffect(() => {
    const gpu = (navigator as Navigator & { gpu?: { requestAdapter(): Promise<unknown> } }).gpu;
    gpu
      ?.requestAdapter()
      .then((a) => setHas(!!a))
      .catch(() => undefined);
  }, []);
  return has;
}

// shown says whether a voice passes the filters ("" lets every one through).
function shown(m: LocalModel, language: string, quality: string): boolean {
  const q = choose(m.quality === "x-low", "low", m.quality);
  return (language === "" || baseLang(m) === language) && (quality === "" || q === quality);
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
        setError(tf("The sample could not be played: {error}", { error: errorMessage(e) }));
      }
    },
    [api, playing, stop],
  );
  return { playing, play, error };
}

// useLoadState is how far a voice is from being ready in this browser, kept current.
function useLoadState(id: string): LoadState | undefined {
  const [, redraw] = useState(0);
  useEffect(() => onLoadState(() => redraw((n) => n + 1)), []);
  return choose(id !== "", loadStateOf(id), undefined);
}

// Mix is another Kokoro voice blended into the chosen one: id "" is none, amount 0 to 1.
export interface Mix {
  id: string;
  amount: number;
}

// MIX_STEP is how far one step of the mix slider moves.
const MIX_STEP = 0.05;

// LocalVoices lists the voices the gateway can download to run in this browser: download one,
// follow its progress, choose it, or delete it. A Kokoro voice in use can be mixed with another.
export function LocalVoices({
  api,
  chosen,
  choose: pick,
  mix = { id: "", amount: 0.5 },
  setMix = () => undefined,
}: {
  api: Api;
  chosen: string;
  choose: (id: string) => void;
  mix?: Mix;
  setMix?: (m: Mix) => void;
}) {
  const [catalog, setCatalog] = useState<LocalCatalog | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState("");
  // The list opens on the filters chosen last time.
  const [filters, setFilters] = useState<Filters>(loadFilters);
  const { language, quality } = filters;
  const filter = (f: Partial<Filters>) => {
    const next = { ...filters, ...f };
    setFilters(next);
    saveFilters(next);
  };
  const sample = useSample(api);

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
  // While a voice downloads, the catalog is read again until it is done.
  useEffect(() => {
    if (!downloading) {
      return undefined;
    }
    const timer = setInterval(() => void load(), DOWNLOAD_POLL_MS);
    return () => clearInterval(timer);
  }, [downloading, load]);

  // The first voice downloaded is the one used, when none was chosen.
  useEffect(() => {
    if (chosen) {
      return;
    }
    const ready = catalog?.models.find((m) => m.installed && m.engine !== "accelerator");
    if (ready) {
      pick(ready.id);
    }
  }, [catalog, chosen, pick]);

  // The voice in use is loaded into memory as soon as it is downloaded, and how far that is shows
  // in place of "In use" until it is ready.
  const chosenReady = !!catalog?.models.some((m) => m.id === chosen && m.installed);
  // Before the first word from the worker, a voice that will load counts as loading.
  const pending: LoadState | undefined = choose(localVoicesSupported(), { pct: 0, ready: false }, undefined);
  const loading = useLoadState(chosen) ?? pending;
  // Kokoro runs on the graphics card once the accelerator is downloaded, on a browser with
  // WebGPU; the voice in use loads again when that changes.
  const webGPU = useWebGPU();
  const accel = catalog?.models.find((m) => m.engine === "accelerator");
  const gpu = !!accel?.installed && webGPU;
  useEffect(() => {
    setKokoroGPU(gpu);
  }, [gpu]);
  useEffect(() => {
    if (chosenReady && localVoicesSupported()) {
      warmLocal(chosen);
    }
  }, [chosen, chosenReady, gpu]);

  // act runs one request, then reads the catalog again; its failure is the one shown.
  const act = async (id: string, run: () => Promise<void>) => {
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
  };

  if (!catalog) {
    return (
      <div class="text-xs text-[#7a7a8c]" data-testid="local-voices">
        {choose(error !== "", error, t("Loading…"))}
      </div>
    );
  }
  const voices = catalog.models.filter((m) => m.engine !== "accelerator");
  const languages = [...new Set(byLanguage(voices).map(baseLang))];
  // The voice in use, and the one downloading, stay in the list whatever the filters say.
  const visible = byLanguage(voices).filter(
    (m) => m.id === chosen || m.id === dl?.id || shown(m, choose(languages.includes(language), language, ""), quality),
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
      {accel && webGPU && voices.some((m) => m.engine === "kokoro") && (
        <Accelerator
          model={accel}
          download={catalog.download}
          busy={busy !== ""}
          running={choose(isKokoro(chosen) && !!loading?.ready, choose(!!loading?.gpu, "gpu", "cpu"), "")}
          get={() => void act(accel.id, () => installVoice(api, accel.id))}
          remove={() => void act(accel.id, () => deleteVoice(api, accel.id))}
        />
      )}
      <div class="flex flex-wrap gap-2" data-testid="local-voice-filters">
        <select
          class={inputCls}
          aria-label={t("Language")}
          data-testid="local-voice-language"
          value={language}
          onChange={(e) => filter({ language: (e.target as HTMLSelectElement).value })}
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
          onChange={(e) => filter({ quality: (e.target as HTMLSelectElement).value })}
        >
          <option value="">{t("Every quality")}</option>
          {QUALITY_FILTERS.map((q) => (
            <option key={q} value={q}>
              {qualityLabel(q)}
            </option>
          ))}
        </select>
      </div>
      {visible.length === 0 && <div class="text-xs text-[#7a7a8c]">{t("No voice matches these filters.")}</div>}
      {visible.map((m) => {
        const here = dl?.id === m.id;
        let state;
        if (here && downloading) {
          const pct = Math.floor((100 * dl.done) / Math.max(dl.total, 1));
          state = (
            <output class="text-xs text-[#a0a0b0]" data-testid={"local-voice-progress-" + m.id}>
              {tf("Downloading… {pct}%", { pct: String(pct) })}
            </output>
          );
        } else if (m.installed) {
          const inUse = chosen === m.id;
          let use = (
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
          if (inUse && loading?.error) {
            use = (
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
          } else if (inUse && loading && !loading.ready) {
            use = (
              <span class="text-xs text-[#a0a0b0]" role="status" data-testid={"local-voice-loading-" + m.id}>
                {tf("Loading into memory… {pct}%", { pct: String(loading.pct) })}
              </span>
            );
          }
          state = (
            <span class="flex items-center gap-2">
              {use}
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
                    if (mix.id === m.id) {
                      setMix({ ...mix, id: "" });
                    }
                  })
                }
              >
                {t("Delete")}
              </button>
            </span>
          );
        } else {
          state = (
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
        const hearing = sample.playing === m.id;
        return (
          <div key={m.id} class="flex items-center justify-between gap-3" data-testid={"local-voice-" + m.id}>
            <div class="flex items-center gap-2 min-w-0">
              {!m.sample && <span class="w-[32px] shrink-0" aria-hidden="true" />}
              {m.sample && (
                <button
                  type="button"
                  class={smallBtn + " w-[32px] shrink-0 px-0"}
                  aria-label={choose(hearing, t("Stop the sample"), tf("Hear {name}", { name: m.name }))}
                  aria-pressed={hearing}
                  data-testid={"local-voice-sample-" + m.id}
                  onClick={() => void sample.play(m.id)}
                >
                  {choose(hearing, "■", "▶")}
                </button>
              )}
              <div class="min-w-0">
                <div class="text-sm text-[#e8e8ea] truncate">
                  {m.name} · {m.lang}
                </div>
                <div class="text-xs text-[#7a7a8c]">
                  {choose(m.engine === "kokoro", "Kokoro · ", "")}
                  {qualityLabel(m.quality)} · {megabytes(m.size)}
                </div>
              </div>
            </div>
            {state}
          </div>
        );
      })}
      {isKokoro(chosen) && (
        <KokoroMix
          voices={voices.filter((m) => isKokoro(m.id) && m.installed && m.id !== chosen)}
          mix={mix}
          setMix={setMix}
        />
      )}
      {sample.error && (
        <div class="text-xs text-[#f87171]" role="alert">
          {sample.error}
        </div>
      )}
      {dl?.error && (
        <div class="text-xs text-[#f87171]" role="alert">
          {tf("The download failed: {error}", { error: dl.error })}
        </div>
      )}
      {loading?.error && (
        <div class="text-xs text-[#f87171]" role="alert">
          {tf("The voice could not be loaded: {error}", { error: loading.error })}
        </div>
      )}
      {error && (
        <div class="text-xs text-[#f87171]" role="alert">
          {error}
        </div>
      )}
    </div>
  );
}

// Accelerator offers the files that run Kokoro on the graphics card: much faster to start
// speaking than on the processor, for a larger download.
function Accelerator({
  model,
  download,
  busy,
  running,
  get,
  remove,
}: {
  model: LocalModel;
  download: LocalCatalog["download"];
  busy: boolean;
  // running is where the Kokoro voice in use runs, once loaded: "gpu", "cpu", or "" before.
  running: string;
  get: () => void;
  remove: () => void;
}) {
  const downloading = !!download && !download.error;
  let state;
  if (download?.id === model.id && downloading) {
    const pct = Math.floor((100 * download.done) / Math.max(download.total, 1));
    state = (
      <output class="text-xs text-[#a0a0b0]" data-testid="local-voice-gpu-progress">
        {tf("Downloading… {pct}%", { pct: String(pct) })}
      </output>
    );
  } else if (model.installed) {
    state = (
      <button type="button" class={smallBtn} disabled={busy} data-testid="local-voice-gpu-delete" onClick={remove}>
        {t("Delete")}
      </button>
    );
  } else {
    state = (
      <button
        type="button"
        class={smallBtn}
        disabled={downloading || busy}
        data-testid="local-voice-gpu-get"
        onClick={get}
      >
        {t("Download")}
      </button>
    );
  }
  let note = tf("Kokoro starts speaking sooner on the graphics card. Adds {size}.", {
    size: megabytes(model.size),
  });
  if (model.installed) {
    const notes: Record<string, string> = {
      gpu: t("Kokoro runs on the graphics card."),
      cpu: t("WebGPU could not start here: Kokoro runs on the processor."),
    };
    note = notes[running] ?? t("Kokoro will use the graphics card.");
  }
  return (
    <div
      class="flex items-center justify-between gap-3 rounded border border-[#2a2a35] p-2"
      data-testid="local-voice-gpu"
    >
      <div class="min-w-0">
        <div class="text-sm text-[#e8e8ea]">{t("Graphics card (WebGPU)")}</div>
        <div class="text-xs text-[#7a7a8c]" data-testid="local-voice-gpu-note">
          {note}
        </div>
      </div>
      {state}
    </div>
  );
}

// KokoroMix blends the Kokoro voice in use with another downloaded one: a timbre of its own,
// between the two.
function KokoroMix({ voices, mix, setMix }: { voices: LocalModel[]; mix: Mix; setMix: (m: Mix) => void }) {
  if (voices.length === 0) {
    return (
      <div class="text-xs text-[#7a7a8c]" data-testid="local-voice-mix-none">
        {t("Download another Kokoro voice to mix it with this one.")}
      </div>
    );
  }
  const other = voices.some((m) => m.id === mix.id);
  return (
    <div class="flex flex-col gap-2" data-testid="local-voice-mix">
      <label class="flex items-center justify-between gap-3 text-sm text-[#e8e8ea]">
        {t("Mix with")}
        <select
          class={inputCls}
          data-testid="local-voice-mix-voice"
          value={choose(other, mix.id, "")}
          onChange={(e) => setMix({ ...mix, id: (e.target as HTMLSelectElement).value })}
        >
          <option value="">{t("No other voice")}</option>
          {voices.map((m) => (
            <option key={m.id} value={m.id}>
              {m.name} · {m.lang}
            </option>
          ))}
        </select>
      </label>
      {other && (
        <label class="flex items-center gap-3 text-xs text-[#a0a0b0]">
          {t("How much")}
          <input
            type="range"
            class="flex-1 accent-[var(--color-accent,#4cc2ff)]"
            data-testid="local-voice-mix-amount"
            min={0}
            max={1}
            step={MIX_STEP}
            value={mix.amount}
            onInput={(e) => setMix({ ...mix, amount: Number((e.target as HTMLInputElement).value) })}
          />
          <span class="w-10 text-right tabular-nums">{Math.round(mix.amount * 100)}%</span>
        </label>
      )}
    </div>
  );
}
