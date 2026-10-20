import { useCallback, useEffect, useState } from "preact/hooks";
import { lang, t } from "./i18n";
import type { Api } from "./gitApi";
import { errorMessage } from "./errors";
import { type FrictionView, clearFriction, getFriction } from "./frictionApi";
import { dangerBtn } from "./SettingsControls";
import { InlineConfirm } from "./NetworkSettings";

// useFriction reads the week, and keeps what it says current after the journal is cleared.
function useFriction(api: Api) {
  // undefined: not read yet. null: this gateway has no journal.
  const [view, setView] = useState<FrictionView | null | undefined>(undefined);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [asking, setAsking] = useState(false);
  useEffect(() => {
    let live = true;
    getFriction(api, lang())
      .then((v) => live && setView(v))
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
  const clear = useCallback(async () => {
    setBusy(true);
    setErr("");
    try {
      await clearFriction(api);
      setView(await getFriction(api, lang()));
    } catch (e) {
      setErr(errorMessage(e));
    }
    setAsking(false);
    setBusy(false);
  }, [api]);
  return { view, err, busy, asking, setAsking, clear };
}

// FrictionSettings is the journal tab: the last week of what got in the way, in plain words, and
// the way to forget it. The journal stays on the computer the gateway runs on and never changes
// how Motita works: it is there to be read.
export function FrictionSettings({ api }: { api: Api }) {
  const { view, err, busy, asking, setAsking, clear } = useFriction(api);
  if (view === undefined) {
    return <p class="settings-card-note text-xs">{t("Loading…")}</p>;
  }
  if (view === null) {
    return (
      <p class="settings-card-note text-xs" data-testid="friction-missing">
        {err || t("This gateway has no journal: it is older than this page.")}
      </p>
    );
  }
  return (
    <>
      <p class="settings-card-note text-xs">
        {t(
          "Motita writes down, on this computer only, the failures, retries and refused actions of your sessions, and the answers you mark as not good. Nothing leaves it, secrets are masked, and nothing changes by itself.",
        )}
      </p>
      <pre class="friction-summary" data-testid="friction-summary">
        {view.text}
      </pre>
      {view.total > 0 && (
        <p class="settings-card-note">
          <button
            type="button"
            class={dangerBtn}
            data-testid="friction-clear"
            disabled={busy}
            onClick={() => setAsking(true)}
          >
            {t("Clear the journal")}
          </button>
        </p>
      )}
      {asking && (
        <InlineConfirm
          id="settings-friction-confirm"
          testid="friction-confirm"
          title={t("Clear the whole journal?")}
          lines={[
            t("Everything written so far is deleted, not just this week."),
          ]}
          confirmLabel={t("Clear the journal")}
          danger
          busy={busy}
          onConfirm={() => void clear()}
          onCancel={() => setAsking(false)}
        />
      )}
      {err && (
        <p
          class="settings-card-note text-xs text-danger"
          role="alert"
          data-testid="friction-error"
        >
          {err}
        </p>
      )}
    </>
  );
}
