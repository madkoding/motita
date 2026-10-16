import { useState } from "preact/hooks";
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
} from "./browserLLM";
import type { Api } from "./prApi";
import { Row, smallBtn } from "./SettingsControls";
import { errorMessage } from "./errors";

// What the lighter build (the one a card with 16-bit floats runs) takes to download, with the
// graphics runtime, in bytes (the gateway tells it in its catalog).
export const BROWSER_MODEL_BYTES = 516_825_018;

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
  const body = (await res.json()) as {
    text?: string;
    ms?: number;
    error?: string;
  };
  if (!res.ok) {
    throw new Error(body.error || "HTTP " + res.status);
  }
  return tf("“{text}” in {seconds} s", {
    text: body.text ?? "",
    seconds: ((body.ms ?? 0) / 1000).toFixed(1),
  });
}

// BrowserModelSettings turns on the small model this tab runs on its own graphics card. Nothing is
// downloaded until the switch is on, and it works only while a tab with it on stays open.
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
  const [tried, setTried] = useState("");
  const [trying, setTrying] = useState(false);
  const tryOut = () => {
    setTrying(true);
    setTried("");
    tryIt(api)
      .then(setTried, (e: unknown) => setTried(errorMessage(e)))
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
        {s.phase === "ready" && (
          <Row
            id="llm-browser-try"
            label={t("Try it")}
            hint={
              tried ||
              t(
                "Asks it for a title, through the gateway, the way the agent would.",
              )
            }
          >
            <button
              type="button"
              class={smallBtn}
              disabled={trying}
              onClick={tryOut}
            >
              {t("Try it")}
            </button>
          </Row>
        )}
      </div>
    </section>
  );
}
