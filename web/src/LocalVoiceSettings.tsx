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
  warmLocal,
} from "./localVoice";
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

// LocalVoices lists the voices the gateway can download to run in this browser: download one,
// follow its progress, choose it, or delete it.
export function LocalVoices({
  api,
  chosen,
  choose: pick,
}: {
  api: Api;
  chosen: string;
  choose: (id: string) => void;
}) {
  const [catalog, setCatalog] = useState<LocalCatalog | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState("");
  // The list opens on the interface's language; "" is every language, or every quality.
  const [language, setLanguage] = useState<string>(lang());
  const [quality, setQuality] = useState("");
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
    const ready = catalog?.models.find((m) => m.installed);
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
  useEffect(() => {
    if (chosenReady && localVoicesSupported()) {
      warmLocal(chosen);
    }
  }, [chosen, chosenReady]);

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
  const languages = [...new Set(byLanguage(catalog.models).map(baseLang))];
  // The voice in use, and the one downloading, stay in the list whatever the filters say.
  const visible = byLanguage(catalog.models).filter(
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
      <div class="flex flex-wrap gap-2" data-testid="local-voice-filters">
        <select
          class={inputCls}
          aria-label={t("Language")}
          data-testid="local-voice-language"
          value={language}
          onChange={(e) => setLanguage((e.target as HTMLSelectElement).value)}
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
          onChange={(e) => setQuality((e.target as HTMLSelectElement).value)}
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
