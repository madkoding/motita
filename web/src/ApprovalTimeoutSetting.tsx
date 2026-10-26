import { useCallback, useState } from "preact/hooks";
import { t, tf } from "./i18n";
import type { Api } from "./gitApi";
import { choose } from "./choose";
import { errorMessage } from "./errors";
import { type SecurityView, setApprovalTimeout } from "./securityApi";
import { Row, Segmented } from "./SettingsControls";

// The waits offered, in minutes: 0 is for as long as it takes (the default).
const WAITS = [0, 5, 15, 60];

// timeoutHint says what the wait shown means.
function timeoutHint(minutes: number): string {
  if (minutes === 0) {
    return t(
      "A question about a command waits until you answer it. A run you left alone stays stopped until you come back.",
    );
  }
  return tf(
    "A question nobody answers within {minutes} minutes is refused and the run goes on without that command.",
    { minutes: String(minutes) },
  );
}

// ApprovalTimeoutSetting is how long a question about a command waits for an answer. Waiting is the
// default; a limit only makes the agent do less unasked, so it is saved at once, with no confirmation.
export function ApprovalTimeoutSetting({
  api,
  view,
  onView,
}: {
  api: Api;
  view: SecurityView;
  onView: (v: SecurityView) => void;
}) {
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const current = view.approval_timeout_minutes ?? 0;
  // A limit set in the file that the page does not offer is still shown, so it is not misread as 0.
  const waits = choose(WAITS.includes(current), WAITS, [...WAITS, current]);

  const pick = useCallback(
    async (value: string) => {
      setBusy(true);
      setErr("");
      try {
        onView(await setApprovalTimeout(api, Number(value)));
      } catch (e) {
        setErr(errorMessage(e));
      }
      setBusy(false);
    },
    [api, onView],
  );

  return (
    <>
      <Row
        id="settings-approval-timeout"
        label={t("If nobody answers")}
        stack
        hint={timeoutHint(current)}
      >
        <Segmented<string>
          name="settings-approval-timeout"
          labelledBy="settings-approval-timeout"
          value={String(current)}
          onChange={(v) => void pick(v)}
          disabled={!view.can_save || busy}
          options={waits.map((m) => ({
            value: String(m),
            label: choose(
              m === 0,
              t("Waiting for an answer"),
              tf("{n} min", { n: String(m) }),
            ),
          }))}
        />
      </Row>
      {err && (
        <p
          class="settings-card-note text-xs text-danger"
          role="alert"
          data-testid="security-timeout-error"
        >
          {err}
        </p>
      )}
    </>
  );
}
