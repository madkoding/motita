import { useCallback, useEffect, useState } from "preact/hooks";
import { t, tf } from "./i18n";
import type { Api } from "./gitApi";
import { errorMessage } from "./errors";
import { type BoxesView, getBoxes, removeBox, stopBoxes } from "./boxesApi";
import { Row, dangerBtn, smallBtn } from "./SettingsControls";

// useBoxes reads the boxes once, and again on demand. A gateway without the API, a machine without
// a container tool and a failed first read all draw nothing: this is a convenience, not a setting.
function useBoxes(api: Api) {
  const [view, setView] = useState<BoxesView | null>(null);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    try {
      setView(await getBoxes(api));
    } catch {
      setView(null);
    }
  }, [api]);
  useEffect(() => {
    void load();
  }, [load]);

  // act runs one change with the buttons off meanwhile, reads the list again after, and shows what
  // went wrong.
  const act = useCallback(
    async (change: () => Promise<unknown>) => {
      setBusy(true);
      setErr("");
      try {
        await change();
      } catch (e) {
        setErr(errorMessage(e));
      }
      await load();
      setBusy(false);
    },
    [load],
  );
  return { view, err, busy, load, act };
}

// BoxesSetting lists the service containers Motita started (to test a project and photograph it)
// and stops them: each run removes its own when it ends, so what is here is running now, or was
// left by a run that died.
export function BoxesSetting({ api }: { api: Api }) {
  const { view, err, busy, load, act } = useBoxes(api);
  if (!view?.runtime) {
    return null;
  }
  return (
    <>
      <Row
        id="settings-boxes"
        label={t("Test containers")}
        stack
        hint={t(
          "Services Motita starts in a container only it can reach, to test a project and take screenshots. They are not where its commands run (that is the sandbox above). A run removes its own when it ends; stop here any that stay.",
        )}
      >
        <div class="flex gap-2">
          <button
            class={smallBtn}
            disabled={busy}
            onClick={() => void load()}
            data-testid="boxes-refresh"
          >
            {t("Refresh")}
          </button>
          {view.boxes.length > 0 && (
            <button
              class={dangerBtn}
              disabled={busy}
              onClick={() => void act(() => stopBoxes(api))}
              data-testid="boxes-stop-all"
            >
              {t("Stop all")}
            </button>
          )}
        </div>
      </Row>
      {view.boxes.length === 0 && (
        <p
          class="settings-card-note text-xs text-[#7a7a8c]"
          data-testid="boxes-empty"
        >
          {t("No boxes are running.")}
        </p>
      )}
      {view.boxes.length > 0 && (
        <ul class="settings-card-note text-xs" data-testid="boxes-list">
          {view.boxes.map((b) => (
            <li
              key={b.id}
              class="flex items-center gap-2 py-1"
              data-testid="box"
            >
              <span class="flex-1 min-w-0 truncate">
                <code>{b.name}</code>{" "}
                <span class="text-[#7a7a8c]">
                  {b.image} · {b.status}
                </span>
              </span>
              <button
                class={dangerBtn}
                disabled={busy}
                aria-label={tf("Stop {name}", { name: b.name })}
                onClick={() => void act(() => removeBox(api, b.id))}
                data-testid="box-stop"
              >
                {t("Stop")}
              </button>
            </li>
          ))}
        </ul>
      )}
      {err && (
        <p
          class="settings-card-note text-xs text-danger"
          role="alert"
          data-testid="boxes-error"
        >
          {err}
        </p>
      )}
    </>
  );
}
