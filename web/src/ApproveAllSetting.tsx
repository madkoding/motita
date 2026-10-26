import { useCallback, useState } from "preact/hooks";
import { t } from "./i18n";
import type { Api } from "./gitApi";
import { choose } from "./choose";
import { errorMessage } from "./errors";
import { type SecurityView, setApproveAllCommands } from "./securityApi";
import { Row, Segmented } from "./SettingsControls";
import { InlineConfirm } from "./NetworkSettings";

// ask: a command that could change things beyond the workspace waits for the user's answer (the
// default). all: it runs without asking.
type AskChoice = "ask" | "all";

// askHint says what the choice shown means.
function askHint(c: AskChoice): string {
  if (c === "ask") {
    return t(
      "Motita asks before a command that installs software, uses the network, runs as administrator or touches files outside its workspace.",
    );
  }
  return t(
    "Motita runs every command without asking. The sandbox still limits what a command can reach, and a few destructive commands (erasing a disk, powering off) stay refused.",
  );
}

// askRisks is what the confirmation spells out before suggestions are applied unasked.
function askRisks(): string[] {
  return [
    t(
      "Commands you never saw will run on your computer, one after another, with nobody to stop them.",
    ),
    t(
      "A command shaped by instructions hidden in a web page or a repository would run before you see it.",
    ),
    t("It applies at once to every open session, from its next command."),
  ];
}

// ApproveAllSetting is whether Motita runs every command without asking first. Asking is the
// default; allowing all is never saved without a confirmation, and going back needs none. onView takes the view the gateway answers.
export function ApproveAllSetting({
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
  const current: AskChoice = choose(view.approve_all_commands, "all", "ask");

  const save = useCallback(
    async (on: boolean) => {
      setBusy(true);
      setErr("");
      try {
        onView(await setApproveAllCommands(api, on));
      } catch (e) {
        setErr(errorMessage(e));
      }
      setAsking(false);
      setBusy(false);
    },
    [api, onView],
  );
  const pick = useCallback(
    (c: AskChoice) => {
      setErr("");
      if (c === current) {
        setAsking(false);
        return;
      }
      if (c === "all") {
        setAsking(true);
        return;
      }
      void save(false);
    },
    [current, save],
  );
  const confirm = useCallback(() => void save(true), [save]);
  const cancel = useCallback(() => setAsking(false), []);
  const shown: AskChoice = choose(asking, "all", current);

  return (
    <>
      <Row
        id="settings-approve-all"
        label={t("Approving commands")}
        stack
        hint={askHint(shown)}
      >
        <Segmented<AskChoice>
          name="settings-approve-all"
          labelledBy="settings-approve-all"
          value={shown}
          onChange={pick}
          disabled={!view.can_save || busy}
          options={[
            { value: "ask", label: t("Ask me") },
            { value: "all", label: t("Allow all") },
          ]}
        />
      </Row>
      {view.no_new_privs && (
        <p
          class="settings-card-note text-xs text-danger"
          role="alert"
          data-testid="security-nnp"
        >
          {t(
            "sudo cannot work here: Motita was started with NO_NEW_PRIVS (NoNewPrivs: 1), which no command can take off. To fix it, remove NoNewPrivileges=yes from Motita's systemd unit (or run the container without no-new-privileges) and restart it.",
          )}
        </p>
      )}
      {asking && (
        <InlineConfirm
          id="settings-approve-all-confirm"
          testid="security-approve-confirm"
          title={t("Run every command without asking?")}
          lines={askRisks()}
          confirmLabel={t("Allow all commands")}
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
          data-testid="security-approve-error"
        >
          {err}
        </p>
      )}
    </>
  );
}
