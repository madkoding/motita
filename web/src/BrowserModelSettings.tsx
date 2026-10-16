import { useEffect, useState } from "preact/hooks";
import { lang, t, tf } from "./i18n";
import { choose } from "./choose";
import {
  browserGPU,
  browserModelOn,
  setBrowserModelOn,
  startBrowserModel,
  stopBrowserModel,
  useBrowserModelState,
  type BrowserPhase,
  type BrowserState,
} from "./browserLLM";
import type { Api } from "./prApi";
import { Row, smallBtn } from "./SettingsControls";
import { errorMessage } from "./errors";

// What the lighter build (the one a card with 16-bit floats runs) takes to download, with the
// graphics runtime, in bytes (the gateway tells it in its catalog).
export const BROWSER_MODEL_BYTES = 516_825_018;

// After this many seconds the test says a slow answer is not a hang.
const SLOW_AFTER_S = 20;

// What each phase says under the switch.
function statusOf(phase: BrowserPhase, pct: number, error: string): string {
  switch (phase) {
    case "downloading":
      return tf("The gateway is downloading the model: {pct}%", {
        pct: String(pct),
      });
    case "loading":
      return tf("Loading the model into the graphics card: {pct}%", {
        pct: String(pct),
      });
    case "ready":
      return t("Ready: it answers while this tab stays open.");
    case "failed":
      return tf("The model could not start: {error}", { error });
    case "unsupported":
      return t(
        "This browser or device cannot run it: it needs WebGPU, and a phone is left out.",
      );
    default:
      return "";
  }
}

// tryIt asks the gateway to have the model write a title, and says what came back.
async function tryIt(api: Api): Promise<string> {
  const res = await api("/v1/browser-llm/test", { method: "POST" });
  // A proxy's error page is not JSON: the status still says what went wrong.
  const body = (await res.json().catch(() => ({}))) as {
    text?: string;
    ms?: number;
    error?: string;
  };
  if (!res.ok) {
    throw new Error(body.error || "HTTP " + res.status);
  }
  const seconds = ((body.ms ?? 0) / 1000).toFixed(1);
  if (!body.text?.trim()) {
    return tf("It ran in {seconds} s but answered nothing.", { seconds });
  }
  return tf("“{text}” in {seconds} s", { text: body.text, seconds });
}

// BrowserModelSettings turns on the small model this tab runs on its own graphics card. Nothing is
// downloaded until the switch is on, and it works only while a tab with it on stays open.
// CardLine says which graphics card the ready model runs on; the emulation warning covers the rest.
function CardLine({ s }: { s: BrowserState }) {
  if (s.phase !== "ready" || s.software) {
    return null;
  }
  return (
    <output
      class="block text-xs text-[#9a9aaa] px-3 pb-2"
      data-testid="llm-browser-card"
    >
      {tf("Running on the graphics card: {card}", {
        card: s.card || t("unnamed"),
      })}
    </output>
  );
}

export function BrowserModelSettings({ api }: { api: Api }) {
  const [on, setOn] = useState(browserModelOn());
  const s = useBrowserModelState();
  const supported = browserGPU();
  const toggle = (next: boolean) => {
    setOn(next);
    setBrowserModelOn(next);
    if (next) {
      startBrowserModel(api);
    } else {
      stopBrowserModel();
    }
  };
  const [tried, setTried] = useState({ text: "", ok: true });
  const [trying, setTrying] = useState(false);
  // waited counts the seconds of a test that is running, so a slow one shows it is alive.
  const [waited, setWaited] = useState(0);
  useEffect(() => {
    if (!trying) {
      return undefined;
    }
    const began = Date.now();
    setWaited(0);
    const timer = setInterval(
      () => setWaited(Math.floor((Date.now() - began) / 1000)),
      1000,
    );
    return () => clearInterval(timer);
  }, [trying]);
  const tryOut = () => {
    setTrying(true);
    setTried({ text: "", ok: true });
    tryIt(api)
      .then(
        (text) => setTried({ text, ok: true }),
        (e: unknown) => setTried({ text: errorMessage(e), ok: false }),
      )
      .finally(() => setTrying(false));
  };
  const status = statusOf(
    choose(supported, s.phase, "unsupported"),
    s.pct,
    s.error,
  );
  return (
    <section class="settings-group" aria-labelledby="llm-browser-h">
      <h4 id="llm-browser-h" class="settings-group-title">
        {t("Model in this browser")}
      </h4>
      <div class="settings-card" data-testid="llm-browser">
        <Row
          id="llm-browser-switch"
          label={t("Run a small model in this tab (WebGPU)")}
          hint={tf(
            "Qwen2.5 0.5B, free. Downloads about {size} once. Then choose it under Summaries below: titles and summaries use it while this tab is open.",
            {
              size:
                Math.round(BROWSER_MODEL_BYTES / 1e6).toLocaleString(lang()) +
                " MB",
            },
          )}
          warning={
            status && (
              <output
                class="block text-xs text-[#9a9aaa] mt-1"
                data-testid="llm-browser-status"
              >
                {status}
              </output>
            )
          }
        >
          <label class="settings-switch-hit">
            <input
              type="checkbox"
              role="switch"
              class="settings-switch"
              aria-labelledby="llm-browser-switch"
              checked={on}
              disabled={!supported}
              onChange={(e) => toggle((e.target as HTMLInputElement).checked)}
            />
          </label>
        </Row>
        {on && s.phase === "failed" && (
          <Row
            id="llm-browser-retry"
            label={t("Try again")}
            hint={t("The switch stays on: this starts the model once more.")}
          >
            <button
              type="button"
              class={smallBtn}
              onClick={() => startBrowserModel(api)}
            >
              {t("Try again")}
            </button>
          </Row>
        )}
        <CardLine s={s} />
        {s.software && (
          <output
            class="block text-xs text-[#e0b46a] px-3 pb-2"
            data-testid="llm-browser-software"
          >
            {t(
              "This browser runs WebGPU on the processor, not on a graphics card: the model works but is very slow.",
            )}
            {s.card && " (" + s.card + ")"}
          </output>
        )}
        {s.phase === "ready" && (
          <Row
            id="llm-browser-try"
            label={t("Try it")}
            hint={t(
              "Asks it for a title, through the gateway, the way the agent would.",
            )}
            warning={
              (trying || tried.text) && (
                <output
                  class={
                    "block text-sm mt-1 " +
                    choose(tried.ok, "text-[#7ee0a0]", "text-[#ff8a8a]")
                  }
                  data-testid="llm-browser-try-result"
                >
                  {choose(
                    trying,
                    tf("Trying it… {seconds} s", { seconds: String(waited) }) +
                      choose(
                        waited >= SLOW_AFTER_S,
                        " " +
                          t(
                            "Still working: a card that emulates WebGPU on the processor can take minutes.",
                          ),
                        "",
                      ),
                    tried.text,
                  )}
                </output>
              )
            }
          >
            <button
              type="button"
              class={smallBtn}
              disabled={trying}
              onClick={tryOut}
            >
              {choose(trying, t("Trying it…"), t("Try it"))}
            </button>
          </Row>
        )}
      </div>
    </section>
  );
}
