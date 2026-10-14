import { useEffect, useState } from "preact/hooks";
import { t, tf } from "./i18n";
import {
  type Api,
  type GitAccount,
  listAccounts,
  disconnect,
  trimTrailingSlashes,
} from "./gitApi";
import { choose } from "./choose";
import { errorMessage } from "./errors";
import { IconBranch } from "./SettingsIcons";
import { dangerBtn, inputCls, primaryBtn, smallBtn } from "./SettingsControls";

// GitSettingsProps is the git host connections section.
export interface GitSettingsProps {
  api: Api;
  // Bumped by the app after a host was connected, so the list is read again.
  rev: number;
  onConnect: (
    service?: string,
    selfHosted?: { kind: string; host: string },
  ) => void;
  onNotice: (message: string, error?: boolean) => void;
}

// The logo of each git host, from Simple Icons (CC0), in the host's own colour. Gitea, Forgejo and
// any host the page does not know get the generic branch icon.
const FORGE_LOGOS: Record<string, { color: string; d: string }> = {
  github: {
    color: "#e8e8ea",
    d: "M12 .297c-6.63 0-12 5.373-12 12 0 5.303 3.438 9.8 8.205 11.385.6.113.82-.258.82-.577 0-.285-.01-1.04-.015-2.04-3.338.724-4.042-1.61-4.042-1.61C4.422 18.07 3.633 17.7 3.633 17.7c-1.087-.744.084-.729.084-.729 1.205.084 1.838 1.236 1.838 1.236 1.07 1.835 2.809 1.305 3.495.998.108-.776.417-1.305.76-1.605-2.665-.3-5.466-1.332-5.466-5.93 0-1.31.465-2.38 1.235-3.22-.135-.303-.54-1.523.105-3.176 0 0 1.005-.322 3.3 1.23.96-.267 1.98-.399 3-.405 1.02.006 2.04.138 3 .405 2.28-1.552 3.285-1.23 3.285-1.23.645 1.653.24 2.873.12 3.176.765.84 1.23 1.91 1.23 3.22 0 4.61-2.805 5.625-5.475 5.92.42.36.81 1.096.81 2.22 0 1.606-.015 2.896-.015 3.286 0 .315.21.69.825.57C20.565 22.092 24 17.592 24 12.297c0-6.627-5.373-12-12-12",
  },
  gitlab: {
    color: "#fc6d26",
    d: "m23.6 9.593-.033-.086L20.3.98a.851.851 0 0 0-.336-.405.875.875 0 0 0-1 .054.875.875 0 0 0-.29.44L16.47 7.818H7.537L5.333 1.07a.857.857 0 0 0-.29-.441.875.875 0 0 0-1-.054.859.859 0 0 0-.336.405L.433 9.502l-.032.086a6.066 6.066 0 0 0 2.012 7.01l.01.009.03.021 4.977 3.727 2.462 1.863 1.5 1.132a1.008 1.008 0 0 0 1.22 0l1.499-1.132 2.461-1.863 5.006-3.75.013-.01a6.068 6.068 0 0 0 2.01-7.002z",
  },
  bitbucket: {
    color: "#2684ff",
    d: "M.778 1.213a.768.768 0 00-.768.892l3.263 19.81c.084.5.515.868 1.022.873H19.95a.772.772 0 00.77-.646l3.27-20.03a.768.768 0 00-.768-.891zM14.52 15.53H9.522L8.17 8.466h7.561z",
  },
};

// ForgeLogo is the logo of a known git host, or the generic branch icon.
function ForgeLogo({ kind }: { kind: string }) {
  const logo = FORGE_LOGOS[kind];
  if (!logo) {
    return <IconBranch />;
  }
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill={logo.color}>
      <path d={logo.d} />
    </svg>
  );
}

// ForgeIcon is the logo of an account's git host, on a small tile at the start of its row.
function ForgeIcon({ kind }: { kind: string }) {
  return (
    <span class="git-account-icon" aria-hidden="true" data-kind={kind}>
      <ForgeLogo kind={kind} />
    </span>
  );
}

// GitConnections lists every git host with its state, and connects, disconnects or adds one.
export function GitConnections({
  api,
  rev,
  onConnect,
  onNotice,
}: GitSettingsProps) {
  const [accounts, setAccounts] = useState<GitAccount[] | null>(null);
  const [err, setErr] = useState("");
  const [confirm, setConfirm] = useState("");
  const [adding, setAdding] = useState(false);
  const [kind, setKind] = useState("gitlab");
  const [host, setHost] = useState("");
  const [reload, setReload] = useState(0);

  useEffect(() => {
    let live = true;
    listAccounts(api)
      .then((a) => {
        if (live) {
          setAccounts(a);
          setErr("");
        }
      })
      .catch((e) => {
        if (live) {
          setErr(errorMessage(e));
          setAccounts([]);
        }
      });
    return () => {
      live = false;
    };
  }, [rev, reload]);

  const doDisconnect = async (a: GitAccount) => {
    try {
      await disconnect(api, a.id);
      onNotice(tf("Disconnected from {host}", { host: a.name }));
    } catch (e) {
      onNotice(errorMessage(e), true);
    }
    setConfirm("");
    setReload((n) => n + 1);
  };

  const cleanHost = trimTrailingSlashes(
    host.trim().replace(/^https?:\/\//, ""),
  );
  return (
    <section
      class="settings-group"
      aria-labelledby="git-conn-h"
      data-testid="git-connections"
    >
      {/* The tab already names the section on screen; the heading stays for screen readers. */}
      <h3 id="git-conn-h" class="sr-only">
        {t("Git connections")}
      </h3>
      <p class="text-xs text-[#7a7a8c] mb-3">
        {t(
          "Connect a git host to pick your repositories and clone private ones.",
        )}
      </p>
      {accounts === null && (
        <output class="block text-xs text-[#9a9aaa]">{t("Loading…")}</output>
      )}
      {err && (
        <p class="text-xs text-danger mb-2" role="alert">
          {err}
        </p>
      )}
      {!!accounts?.length && (
        <ul class="settings-card">
          {accounts.map((a) => (
            <li key={a.id} class="settings-row git-account">
              <ForgeIcon kind={a.kind} />
              <div class="settings-row-text">
                <div class="text-sm text-[#e8e8ea] truncate">
                  {a.name} <span class="text-xs text-[#7a7a8c]">{a.host}</span>
                </div>
                <div
                  class={
                    "git-account-state text-xs " +
                    choose(a.connected, "text-[#5fd08a]", "text-[#7a7a8c]")
                  }
                >
                  {choose(
                    a.connected,
                    tf("Connected as {user}", { user: a.username || "…" }),
                    t("Not connected"),
                  )}
                </div>
              </div>
              <div class="settings-row-control git-account-actions">
                {confirm === a.id && (
                  <>
                    <button class={dangerBtn} onClick={() => doDisconnect(a)}>
                      {t("Confirm")}
                    </button>
                    <button class={smallBtn} onClick={() => setConfirm("")}>
                      {t("Cancel")}
                    </button>
                  </>
                )}
                {confirm !== a.id && a.connected && (
                  <>
                    <button
                      class={smallBtn}
                      aria-label={tf("Reconnect {host}", { host: a.name })}
                      onClick={() => onConnect(a.id)}
                    >
                      {t("Reconnect")}
                    </button>
                    <button
                      class={smallBtn}
                      aria-label={tf("Disconnect {host}", { host: a.name })}
                      onClick={() => setConfirm(a.id)}
                    >
                      {t("Disconnect")}
                    </button>
                  </>
                )}
                {confirm !== a.id && !a.connected && (
                  <button
                    class={primaryBtn}
                    aria-label={tf("Connect {host}", { host: a.name })}
                    onClick={() => onConnect(a.id)}
                  >
                    {t("Connect")}
                  </button>
                )}
              </div>
            </li>
          ))}
        </ul>
      )}
      {adding && (
        <form
          class="settings-card settings-add space-y-2 mt-3"
          onSubmit={(e) => {
            e.preventDefault();
            if (cleanHost) {
              onConnect(undefined, { kind, host: cleanHost });
              setAdding(false);
              setHost("");
            }
          }}
        >
          <div class="settings-add-fields">
            <select
              class={inputCls}
              value={kind}
              aria-label={t("Server type")}
              onChange={(e) => setKind((e.target as HTMLSelectElement).value)}
            >
              <option value="gitlab">GitLab</option>
              <option value="github">GitHub Enterprise</option>
              <option value="gitea">Gitea / Forgejo</option>
            </select>
            <input
              class={inputCls + " flex-1"}
              value={host}
              placeholder="git.example.com"
              aria-label={t("Server address")}
              autocapitalize="off"
              spellcheck={false}
              onInput={(e) => setHost((e.target as HTMLInputElement).value)}
            />
          </div>
          <div class="flex justify-end gap-2">
            <button
              type="button"
              class={smallBtn}
              onClick={() => setAdding(false)}
            >
              {t("Cancel")}
            </button>
            <button type="submit" class={primaryBtn} disabled={!cleanHost}>
              {t("Continue")}
            </button>
          </div>
        </form>
      )}
      {!adding && (
        <button class={smallBtn + " mt-3"} onClick={() => setAdding(true)}>
          {t("Add self-hosted server")}
        </button>
      )}
    </section>
  );
}
