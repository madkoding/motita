import { useCallback, useState } from "preact/hooks";
import { t } from "./i18n";
import type { Api } from "./gitApi";
import { choose } from "./choose";
import { errorMessage } from "./errors";
import { type SecurityView, setAskUnconfinedRuns } from "./securityApi";
import { Row, Segmented } from "./SettingsControls";
import { InlineConfirm } from "./NetworkSettings";

// ask: Motita asks before the project's own code runs where nothing confines its commands (the
// default). run: it runs that code without asking.
type RunsChoice = "ask" | "run";

// runsHint says what the choice shown means.
function runsHint(c: RunsChoice): string {
  if (c === "ask") {
    return t(
      "Where nothing confines its commands (Windows, macOS, Linux without Landlock), Motita asks before it runs the project's own code: a script, a build, a test.",
    );
  }
  return t(
    "Motita runs the project's own scripts, builds and tests without asking, even where nothing confines them. Reads outside the workspace are still asked about.",
  );
}

// runsRisks is what the confirmation spells out before the question is turned off.
function runsRisks(): string[] {
  return [
    t(
      "On a system that does not confine it, that code can read your keys and Motita's own logins.",
    ),
    t(
      "A script written from instructions hidden in a web page or a repository would run before you see it.",
    ),
    t("It applies at once to every open session, from its next command."),
  ];
}

// UnconfinedRunsSetting is whether Motita asks before the project's own code runs where nothing
// confines its commands. Asking is the default; turning it off is never saved without a
// confirmation, and turning it back on needs none. onView takes the view the gateway answers.
export function UnconfinedRunsSetting({
  api,
  view,
  onView,
}: {
  api: Api;
  view: SecurityView;
  onView: (v: SecurityView) => void;
}) {
  const [asking, setAsking] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const current: RunsChoice = choose(view.ask_unconfined_runs, "ask", "run");

  const save = useCallback(
    async (ask: boolean) => {
      setBusy(true);
      setErr("");
      try {
        onView(await setAskUnconfinedRuns(api, ask));
      } catch (e) {
        setErr(errorMessage(e));
      }
      setAsking(false);
      setBusy(false);
    },
    [api, onView],
  );
  const pick = useCallback(
    (c: RunsChoice) => {
      setErr("");
      if (c === current) {
        setAsking(false);
        return;
      }
      if (c === "run") {
        setAsking(true);
        return;
      }
      void save(true);
    },
    [current, save],
  );
  const shown: RunsChoice = choose(asking, "run", current);
  const confined = choose(
    view.enforced,
    " " + t("This system confines Motita's commands, so this changes nothing here."),
    "",
  );

  return (
    <>
      <Row
        id="settings-unconfined-runs"
        label={t("Running the project's code")}
        stack
        hint={runsHint(shown) + confined}
      >
        <Segmented<RunsChoice>
          name="settings-unconfined-runs"
          labelledBy="settings-unconfined-runs"
          value={shown}
          onChange={pick}
          disabled={!view.can_save || busy}
          options={[
            { value: "ask", label: t("Ask first") },
            { value: "run", label: t("Run without asking") },
          ]}
        />
      </Row>
      {asking && (
        <InlineConfirm
          id="settings-unconfined-runs-confirm"
          testid="security-runs-confirm"
          title={t("Run the project's code without asking?")}
          lines={runsRisks()}
          confirmLabel={t("Run without asking")}
          danger
          busy={busy}
          onConfirm={() => void save(false)}
          onCancel={() => setAsking(false)}
        />
      )}
      {err && (
        <p
          class="settings-card-note text-xs text-danger"
          role="alert"
          data-testid="security-runs-error"
        >
          {err}
        </p>
      )}
    </>
  );
}
