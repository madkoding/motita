// The workspace catalog, in the Git tab of the settings: every repository cloned once for the
// projects that share it, which projects use each one, and a way to delete the ones none uses.
import { useEffect, useState } from "preact/hooks";
import { t, tf } from "./i18n";
import type { Api } from "./gitApi";
import type { CatalogRepo } from "./appTypes";
import { choose } from "./choose";
import { errorMessage } from "./errors";
import { dangerBtn, smallBtn } from "./SettingsControls";
import { HTTP_CONFLICT } from "./appConst";

// Asking is the repository whose deletion waits for an answer: the first question, or the
// gateway's own when the clone holds uncommitted work.
interface Asking {
  name: string;
  refusal?: string;
}

// deleteRepo asks the gateway to delete one clone. It answers "" when it is gone, the gateway's
// reason when it was refused because of uncommitted work (409), and throws on anything else.
async function deleteRepo(
  api: Api,
  name: string,
  force: boolean,
): Promise<string> {
  const res = await api(
    "/v1/repos/" + encodeURIComponent(name) + choose(force, "?force=1", ""),
    { method: "DELETE" },
  );
  if (res.ok) {
    return "";
  }
  const body = await res.json().catch(() => ({}));
  const reason: string = body.error || t("Could not delete the repository");
  if (res.status === HTTP_CONFLICT && !force) {
    return reason;
  }
  throw new Error(reason);
}

export function RepoCatalogSettings({
  api,
  onNotice,
}: {
  api: Api;
  onNotice: (message: string, error?: boolean) => void;
}) {
  const [repos, setRepos] = useState<CatalogRepo[] | null>(null);
  const [reload, setReload] = useState(0);
  const [asking, setAsking] = useState<Asking | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let live = true;
    api("/v1/repos")
      .then(async (res) => {
        const d: { repos?: CatalogRepo[] } = res.ok ? await res.json() : {};
        if (live) {
          setRepos(d.repos ?? []);
        }
      })
      .catch(() => {
        if (live) {
          setRepos([]);
        }
      });
    return () => {
      live = false;
    };
  }, [reload]);

  const remove = async (name: string, force: boolean) => {
    setBusy(true);
    try {
      const refusal = await deleteRepo(api, name, force);
      if (refusal) {
        setAsking({ name, refusal });
        return;
      }
      setAsking(null);
      onNotice(tf("Deleted {name}", { name }));
      setReload((n) => n + 1);
    } catch (e) {
      setAsking(null);
      onNotice(errorMessage(e), true);
    } finally {
      setBusy(false);
    }
  };

  return (
    <section
      class="settings-group mt-6"
      aria-labelledby="repo-catalog-h"
      data-testid="repo-catalog-settings"
    >
      <h3 id="repo-catalog-h" class="text-sm font-semibold text-[#e8e8ea] mb-1">
        {t("Repositories in your workspace")}
      </h3>
      <p class="text-xs text-[#7a7a8c] mb-3">
        {t(
          "Each repository is cloned once and shared by the projects that use it. One that no project uses can be deleted.",
        )}
      </p>
      {repos === null && (
        <output class="block text-xs text-[#9a9aaa]">{t("Loading…")}</output>
      )}
      {repos?.length === 0 && (
        <p class="text-xs text-[#9a9aaa]">
          {t("No repositories in your workspace yet.")}
        </p>
      )}
      {!!repos?.length && (
        <ul class="settings-card">
          {repos.map((r) => (
            <li key={r.name} class="settings-row" data-testid="catalog-repo">
              <div class="settings-row-text min-w-0">
                <div class="text-sm text-[#e8e8ea] truncate">{r.name}</div>
                <div
                  class="text-xs text-[#7a7a8c] font-mono truncate"
                  title={r.git_url}
                >
                  {r.git_url}
                </div>
                <div class="text-xs text-[#9a9aaa]">
                  {choose(
                    r.projects.length > 0,
                    tf("Used by {names}", {
                      names: r.projects.map((p) => p.title).join(", "),
                    }),
                    t("No project uses it"),
                  )}
                </div>
                {asking?.name === r.name && (
                  <p class="text-xs text-danger mt-1" data-testid="catalog-ask">
                    {asking.refusal ||
                      t(
                        "Its clone and its local branches leave your workspace. A project can clone it again later.",
                      )}
                  </p>
                )}
              </div>
              {r.projects.length === 0 && (
                <div class="settings-row-control flex gap-2">
                  {asking?.name === r.name ? (
                    <>
                      <button
                        class={dangerBtn}
                        disabled={busy}
                        onClick={() => remove(r.name, !!asking.refusal)}
                        data-testid="catalog-confirm"
                      >
                        {choose(
                          asking.refusal,
                          t("Discard the changes and delete it"),
                          t("Delete"),
                        )}
                      </button>
                      <button class={smallBtn} onClick={() => setAsking(null)}>
                        {t("Cancel")}
                      </button>
                    </>
                  ) : (
                    <button
                      class={smallBtn}
                      aria-label={tf("Delete {name}", { name: r.name })}
                      onClick={() => setAsking({ name: r.name })}
                      data-testid="catalog-delete"
                    >
                      {t("Delete")}
                    </button>
                  )}
                </div>
              )}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
