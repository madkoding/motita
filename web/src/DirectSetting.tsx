import { useCallback, useState } from "preact/hooks";
import { t } from "./i18n";
import type { Api } from "./gitApi";
import { choose } from "./choose";
import { errorMessage } from "./errors";
import { type SecurityView, setDirect } from "./securityApi";
import { Row, Segmented } from "./SettingsControls";

// phased: a task runs through analysis, JSON rounds and a synthesis (the default). direct: one
// conversation with native tools, the way a coding agent works.
type RunChoice = "phased" | "direct";

// runHint says what the choice shown means.
function runHint(c: RunChoice): string {
  if (c === "direct") {
    return t(
      "A task is one conversation with native tools (a shell, write and edit files, memory), like Claude Code: no analysis or plan before it, no summary after it. The sandbox and the approvals are the same, and the project's checks run once at the end.",
    );
  }
  return t(
    "A task is analysed and planned first, carried out in rounds the agent reports on, checked at every claim of done, and summarised at the end.",
  );
}

// DirectSetting is how the agent carries out a task: the phased loop or direct mode. It changes how
// the agent works, not what it may do, so neither way asks for a confirmation.
export function DirectSetting({
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
  const current: RunChoice = choose(!!view.direct, "direct", "phased");
  const pick = useCallback(
    async (c: RunChoice) => {
      if (c === current) {
        return;
      }
      setBusy(true);
      setErr("");
      try {
        onView(await setDirect(api, c === "direct"));
      } catch (e) {
        setErr(errorMessage(e));
      }
      setBusy(false);
    },
    [api, onView, current],
  );
  const onPick = useCallback((c: RunChoice) => void pick(c), [pick]);
  return (
    <>
      <Row
        id="settings-direct"
        label={t("How a task runs")}
        stack
        hint={runHint(current)}
      >
        <Segmented<RunChoice>
          name="settings-direct"
          labelledBy="settings-direct"
          value={current}
          onChange={onPick}
          disabled={!view.can_save || busy}
          options={[
            { value: "phased", label: t("In phases") },
            { value: "direct", label: t("Direct") },
          ]}
        />
      </Row>
      {err && (
        <p
          class="settings-card-note text-xs text-danger"
          role="alert"
          data-testid="security-direct-error"
        >
          {err}
        </p>
      )}
    </>
  );
}
