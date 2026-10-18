import { useCallback, useState } from "preact/hooks";
import { t } from "./i18n";
import type { Api } from "./gitApi";
import { choose } from "./choose";
import { errorMessage } from "./errors";
import { type SecurityView, setDesktopControl } from "./securityApi";
import { Row, Segmented } from "./SettingsControls";
import { InlineConfirm } from "./NetworkSettings";

// off: the agent never sees the screen nor uses the mouse or the keyboard (the default). ask: it
// may, asking before every step.
type DesktopChoice = "off" | "ask";

// desktopHint says what the choice shown means.
function desktopHint(c: DesktopChoice): string {
  if (c === "off") {
    return t(
      "Motita works with files and commands only: it never sees your screen nor uses your mouse or keyboard.",
    );
  }
  return t(
    "Motita can look at this computer's screen and use its mouse, keyboard and windows. It asks before every step, a picture of the screen included.",
  );
}

// desktopRisks is what the confirmation spells out before the desktop is handed over.
function desktopRisks(): string[] {
  return [
    t(
      "A picture of the screen shows the model whatever you have open: mail, chats, passwords on display.",
    ),
    t(
      "Clicks and keys act as you, outside the sandbox, in every program of your session.",
    ),
    t(
      "Allowing every command for a session also allows every step on the desktop.",
    ),
  ];
}

// DesktopSetting is whether the agent may see and drive this computer's desktop. Off is the
// default; turning it on is never saved without a confirmation, and turning it off needs none.
// Where the computer has no desktop the gateway says why, and the page shows it.
export function DesktopSetting({
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
  const current: DesktopChoice = choose(view.desktop_control, "ask", "off");

  const save = useCallback(
    async (on: boolean) => {
      setBusy(true);
      setErr("");
      try {
        onView(await setDesktopControl(api, on));
      } catch (e) {
        setErr(errorMessage(e));
      }
      setAsking(false);
      setBusy(false);
    },
    [api, onView],
  );
  const pick = useCallback(
    (c: DesktopChoice) => {
      setErr("");
      if (c === current) {
        setAsking(false);
        return;
      }
      if (c === "ask") {
        setAsking(true);
        return;
      }
      void save(false);
    },
    [current, save],
  );
  const confirm = useCallback(() => void save(true), [save]);
  const cancel = useCallback(() => setAsking(false), []);
  const shown: DesktopChoice = choose(asking, "ask", current);

  return (
    <>
      <Row
        id="settings-desktop"
        label={t("Control of this computer's desktop")}
        stack
        hint={desktopHint(shown)}
      >
        <Segmented<DesktopChoice>
          name="settings-desktop"
          labelledBy="settings-desktop"
          value={shown}
          onChange={pick}
          disabled={!view.can_save || busy}
          options={[
            { value: "off", label: t("Off") },
            { value: "ask", label: t("Allowed, asking each step") },
          ]}
        />
      </Row>
      {view.desktop_reason && (
        <p
          class="settings-card-note text-xs text-muted"
          data-testid="security-desktop-reason"
        >
          {view.desktop_reason}
        </p>
      )}
      {asking && (
        <InlineConfirm
          id="settings-desktop-confirm"
          testid="security-desktop-confirm"
          title={t("Let Motita control this computer's desktop?")}
          lines={desktopRisks()}
          confirmLabel={t("Allow desktop control")}
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
          data-testid="security-desktop-error"
        >
          {err}
        </p>
      )}
    </>
  );
}
