import { useCallback, useState } from "preact/hooks";
import { t } from "./i18n";
import type { Api } from "./gitApi";
import { choose } from "./choose";
import { errorMessage } from "./errors";
import { type SecurityView, setAutoApplySuggestions } from "./securityApi";
import { Row, Segmented } from "./SettingsControls";
import { InlineConfirm } from "./NetworkSettings";

// ask: the suggestions of a finished task wait for the user's answer (the default). apply: they
// are applied as soon as the task ends.
type ApplyChoice = "ask" | "apply";

// applyHint says what the choice shown means.
function applyHint(c: ApplyChoice): string {
  if (c === "ask") {
    return t(
      "After a task, Motita asks which of the changes it suggests to apply: this one, this one and the following, or none.",
    );
  }
  return t(
    "After a task, Motita applies every change it suggests without asking. It still follows the sandbox and the policy, and each applied change shows in the conversation.",
  );
}

// applyRisks is what the confirmation spells out before suggestions are applied unasked.
function applyRisks(): string[] {
  return [
    t(
      "Changes you never reviewed are written to your files right after each task.",
    ),
    t(
      "A suggestion shaped by instructions hidden in a web page or a repository would be applied before you see it.",
    ),
    t("It applies at once to every open session, from its next report."),
  ];
}

// AutoApplySetting is whether Motita applies the suggestions of a finished task without asking.
// Asking is the default; turning applying on is never saved without a confirmation, and turning it
// off needs none. onView takes the view the gateway answers.
export function AutoApplySetting({
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
  const current: ApplyChoice = choose(
    view.auto_apply_suggestions,
    "apply",
    "ask",
  );

  const save = useCallback(
    async (on: boolean) => {
      setBusy(true);
      setErr("");
      try {
        onView(await setAutoApplySuggestions(api, on));
      } catch (e) {
        setErr(errorMessage(e));
      }
      setAsking(false);
      setBusy(false);
    },
    [api, onView],
  );
  const pick = useCallback(
    (c: ApplyChoice) => {
      setErr("");
      if (c === current) {
        setAsking(false);
        return;
      }
      if (c === "apply") {
        setAsking(true);
        return;
      }
      void save(false);
    },
    [current, save],
  );
  const confirm = useCallback(() => void save(true), [save]);
  const cancel = useCallback(() => setAsking(false), []);
  const shown: ApplyChoice = choose(asking, "apply", current);

  return (
    <>
      <Row
        id="settings-auto-apply"
        label={t("Suggestions after a task")}
        stack
        hint={applyHint(shown)}
      >
        <Segmented<ApplyChoice>
          name="settings-auto-apply"
          labelledBy="settings-auto-apply"
          value={shown}
          onChange={pick}
          disabled={!view.can_save || busy}
          options={[
            { value: "ask", label: t("Ask me") },
            { value: "apply", label: t("Apply all") },
          ]}
        />
      </Row>
      {asking && (
        <InlineConfirm
          id="settings-auto-apply-confirm"
          testid="security-apply-confirm"
          title={t("Apply every suggestion without asking?")}
          lines={applyRisks()}
          confirmLabel={t("Apply without asking")}
          danger
          busy={busy}
          onConfirm={confirm}
          onCancel={cancel}
        />
      )}
      {err && (
        <p
          class="settings-card-note text-xs text-danger"
          role="alert"
          data-testid="security-apply-error"
        >
          {err}
        </p>
      )}
    </>
  );
}
