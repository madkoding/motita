import { useCallback, useEffect, useState } from "preact/hooks";
import { t } from "./i18n";
import type { Api } from "./gitApi";
import { choose } from "./choose";
import { errorMessage } from "./errors";
import {
  type DiskAccess,
  type SecurityView,
  type SessionScope,
  getSecurity,
  getSessionScope,
  setDiskAccess,
} from "./securityApi";
import { Row, Segmented } from "./SettingsControls";
import { InlineConfirm } from "./NetworkSettings";
import { UnconfinedRunsSetting } from "./UnconfinedRunsSetting";
import { NetworkIsolationSetting } from "./NetworkIsolationSetting";
import { AutoApplySetting } from "./AutoApplySetting";
import { BoxesSetting } from "./BoxesSetting";

// accessHint says what the choice shown lets the agent reach.
function accessHint(m: DiskAccess): string {
  if (m === "workspace") {
    return t(
      "Safest: Motita reads and writes only inside the folder of the session it works in. Your other files are out of its reach.",
    );
  }
  return t(
    "Motita can read and write any file your user account can: documents, other projects, settings.",
  );
}

// fullDiskRisks is what the confirmation spells out before the agent gets the whole disk.
function fullDiskRisks(): string[] {
  return [
    t(
      "Motita will be able to read, change and delete any file your user account can, outside its workspace too.",
    ),
    t(
      "A mistake, or instructions hidden in a web page or a repository it reads, could damage or leak your files.",
    ),
    t("It applies at once to every open session, from its next command."),
  ];
}

// useSecurity holds the section's state: what the gateway reported, the change waiting for a
// confirmation, and the action that saves one.
function useSecurity(api: Api) {
  const [view, setView] = useState<SecurityView | null | undefined>(undefined);
  const [loadErr, setLoadErr] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [asking, setAsking] = useState(false);

  useEffect(() => {
    let live = true;
    getSecurity(api)
      .then((v) => {
        if (live) {
          setView(v);
        }
      })
      .catch((e) => {
        if (live) {
          setLoadErr(errorMessage(e));
        }
      });
    return () => {
      live = false;
    };
  }, [api]);

  const save = useCallback(
    async (m: DiskAccess) => {
      setBusy(true);
      setErr("");
      try {
        setView(await setDiskAccess(api, m));
      } catch (e) {
        setErr(errorMessage(e));
      }
      setAsking(false);
      setBusy(false);
    },
    [api],
  );

  const current = view?.disk_access;
  // pick asks before the whole disk, and narrows back to the workspace at once.
  const pick = useCallback(
    (m: DiskAccess) => {
      setErr("");
      if (m === current) {
        setAsking(false);
        return;
      }
      if (m === "full") {
        setAsking(true);
        return;
      }
      void save(m);
    },
    [current, save],
  );
  const confirm = useCallback(() => void save("full"), [save]);
  const cancel = useCallback(() => setAsking(false), []);

  return { view, setView, loadErr, err, busy, asking, pick, confirm, cancel };
}

// DiskAccessControls is the disk access choice once the gateway reported it: the switch, the
// confirmation the whole disk asks for, and what holds either way.
function DiskAccessControls({
  view,
  shown,
  busy,
  asking,
  sec,
}: {
  view: SecurityView;
  shown: DiskAccess;
  busy: boolean;
  asking: boolean;
  sec: ReturnType<typeof useSecurity>;
}) {
  return (
    <>
      <Row
        id="settings-disk"
        label={t("What Motita can reach")}
        stack
        hint={
          accessHint(shown) +
          choose(
            view.can_save,
            "",
            " " +
              t(
                "The gateway runs on its built-in defaults, without a configuration file, so this cannot be changed here.",
              ),
          )
        }
        warning={
          !view.enforced && (
            <div
              class="text-xs text-warning mt-1"
              data-testid="security-not-enforced"
            >
              {t(
                "On this system only Motita's own file edits are limited: its commands are not, because that needs Linux with Landlock and write confinement on.",
              )}
            </div>
          )
        }
      >
        <Segmented<DiskAccess>
          name="settings-disk"
          labelledBy="settings-disk"
          value={shown}
          onChange={sec.pick}
          disabled={!view.can_save || busy}
          options={[
            { value: "workspace", label: t("Workspace only") },
            { value: "full", label: t("Whole disk") },
          ]}
        />
      </Row>
      {asking && (
        <InlineConfirm
          id="settings-disk-confirm"
          testid="security-confirm"
          title={t("Give Motita access to the whole disk?")}
          lines={fullDiskRisks()}
          confirmLabel={t("Allow the whole disk")}
          danger
          busy={busy}
          onConfirm={sec.confirm}
          onCancel={sec.cancel}
        />
      )}
      <p class="settings-card-note text-xs text-[#7a7a8c]">
        {t(
          "A command you approve one by one can write where you approved it, and sudo runs unconfined.",
        )}
        {view.enforced &&
          " " +
            t(
              "Motita's own logins and keys stay hidden from its commands either way, approved ones included.",
            )}
      </p>
    </>
  );
}

// useSessionScope reads where the open session works, refreshed when the session changes.
function useSessionScope(api: Api, sessionId: string) {
  const [scope, setScope] = useState<SessionScope | null>(null);
  useEffect(() => {
    let live = true;
    getSessionScope(api, sessionId)
      .then((v) => {
        if (live) {
          setScope(v);
        }
      })
      .catch(() => {
        if (live) {
          setScope(null);
        }
      });
    return () => {
      live = false;
    };
  }, [api, sessionId]);
  return scope;
}

// SessionScopeNote says, for the open session, where its commands work, which HOME they get and
// which folders are kept from them: the limits above, made concrete.
function SessionScopeNote({ scope }: { scope: SessionScope }) {
  return (
    <ul
      class="settings-card-note text-xs text-[#7a7a8c]"
      data-testid="security-session-scope"
    >
      <li>
        {t("This session works in")} <code>{scope.workspace}</code>
      </li>
      {scope.project_dir && (
        <li>
          {t("Its project is")} <code>{scope.project_dir}</code>
        </li>
      )}
      {scope.home && (
        <li>
          {t("Its commands' HOME is")} <code>{scope.home}</code>
        </li>
      )}
      {scope.private_roots.map((p) => (
        <li key={p}>
          {t("Kept from its commands:")} <code>{p}</code>
        </li>
      ))}
    </ul>
  );
}

// CommandSettings are the toggles for what a command may do, each shown only when the gateway
// reports it.
function CommandSettings({
  api,
  view,
  onView,
}: {
  api: Api;
  view: SecurityView;
  onView: (v: SecurityView) => void;
}) {
  return (
    <>
      {view.ask_unconfined_runs !== undefined && (
        <UnconfinedRunsSetting api={api} view={view} onView={onView} />
      )}
      {view.auto_apply_suggestions !== undefined && (
        <AutoApplySetting api={api} view={view} onView={onView} />
      )}
      {view.isolate_network !== undefined && (
        <NetworkIsolationSetting api={api} view={view} onView={onView} />
      )}
    </>
  );
}

// SecuritySettings is how much of the disk Motita reaches: its workspace only (the default) or
// the whole disk, which is never given without a confirmation that says what it implies. The
// section stays hidden on a gateway without the security API.
export function SecuritySettings({
  api,
  sessionId = "",
}: {
  api: Api;
  sessionId?: string;
}) {
  const sec = useSecurity(api);
  const scope = useSessionScope(api, sessionId);
  const { view, loadErr, err, busy, asking } = sec;
  if (view === null || (view === undefined && !loadErr)) {
    return null;
  }
  const shown: DiskAccess = choose(
    asking,
    "full",
    view?.disk_access ?? "workspace",
  );
  return (
    <section class="settings-group" aria-labelledby="settings-security-h">
      <h3 id="settings-security-h" class="settings-group-title">
        {t("Disk access")}
      </h3>
      <div class="settings-card" data-testid="security-setting">
        {loadErr && (
          <p class="settings-card-note text-xs text-danger" role="alert">
            {loadErr}
          </p>
        )}
        {view && (
          <DiskAccessControls
            view={view}
            shown={shown}
            busy={busy}
            asking={asking}
            sec={sec}
          />
        )}
        {view && <CommandSettings api={api} view={view} onView={sec.setView} />}
        {view && <BoxesSetting api={api} />}
        {scope && <SessionScopeNote scope={scope} />}
        {err && (
          <p
            class="settings-card-note text-xs text-danger"
            role="alert"
            data-testid="security-error"
          >
            {err}
          </p>
        )}
      </div>
    </section>
  );
}
