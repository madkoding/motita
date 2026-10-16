import { useCallback, useState } from "preact/hooks";
import { t, tf } from "./i18n";
import type { Api } from "./gitApi";
import { choose } from "./choose";
import { errorMessage } from "./errors";
import { type SecurityView, setIsolateNetwork } from "./securityApi";
import { Row, Segmented } from "./SettingsControls";
import { InlineConfirm } from "./NetworkSettings";

// isolate: Motita's commands run without a network (the default). open: they reach it.
type NetChoice = "isolate" | "open";

// netHint says what the choice shown means.
function netHint(c: NetChoice): string {
  if (c === "isolate") {
    return t(
      "Motita's commands run without a network: only this machine's loopback, so tests on 127.0.0.1 still work. A command you approve keeps the network.",
    );
  }
  return t(
    "Motita's commands can reach the network. A command that read your files could send them anywhere.",
  );
}

// netRisks is what the confirmation spells out before the commands get the network.
function netRisks(): string[] {
  return [
    t(
      "A command that has read something, your code or a key in a file, could send it out.",
    ),
    t(
      "Instructions hidden in a web page or a repository could make it do that before you see it.",
    ),
    t("It applies at once to every open session, from its next command."),
  ];
}

// NetworkIsolationSetting is whether Motita's commands run without a network. Isolating is the
// default; giving them the network is never saved without a confirmation, and taking it back
// needs none. onView takes the view the gateway answers.
export function NetworkIsolationSetting({
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
  const current: NetChoice = choose(view.isolate_network, "isolate", "open");

  const save = useCallback(
    async (on: boolean) => {
      setBusy(true);
      setErr("");
      try {
        onView(await setIsolateNetwork(api, on));
      } catch (e) {
        setErr(errorMessage(e));
      }
      setAsking(false);
      setBusy(false);
    },
    [api, onView],
  );
  const pick = useCallback(
    (c: NetChoice) => {
      setErr("");
      if (c === current) {
        setAsking(false);
        return;
      }
      if (c === "open") {
        setAsking(true);
        return;
      }
      void save(true);
    },
    [current, save],
  );
  const confirm = useCallback(() => void save(false), [save]);
  const cancel = useCallback(() => setAsking(false), []);
  const shown: NetChoice = choose(asking, "open", current);
  // What the machine said, when it said: the hint is the AppArmor profile (translated here), the
  // reason is the system's own words; with neither, the only thing known is that it needs Linux.
  const why = choose(
    view.network_hint,
    t(
      "This system blocks it for programs without permission (Ubuntu 24.04 and later). To allow it, run once on the machine that runs Motita: sudo motita sandbox apparmor install",
    ),
    choose(
      view.network_reason,
      tf(
        "This system cannot give a command a network of its own ({reason}), so this changes nothing here.",
        { reason: view.network_reason ?? "" },
      ),
      t(
        "This system cannot take the network away from a command (it needs Linux), so this changes nothing here.",
      ),
    ),
  );
  const unenforced = choose(view.network_enforced === false, " " + why, "");

  return (
    <>
      <Row
        id="settings-isolate-network"
        label={t("Network for commands")}
        stack
        hint={netHint(shown) + unenforced}
      >
        <Segmented<NetChoice>
          name="settings-isolate-network"
          labelledBy="settings-isolate-network"
          value={shown}
          onChange={pick}
          disabled={!view.can_save || busy}
          options={[
            { value: "isolate", label: t("No network") },
            { value: "open", label: t("Allow the network") },
          ]}
        />
      </Row>
      {asking && (
        <InlineConfirm
          id="settings-isolate-network-confirm"
          testid="security-network-confirm"
          title={t("Give Motita's commands the network?")}
          lines={netRisks()}
          confirmLabel={t("Allow the network")}
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
          data-testid="security-network-error"
        >
          {err}
        </p>
      )}
    </>
  );
}
