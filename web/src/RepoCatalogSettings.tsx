// The workspace catalog, in the Git tab of the settings: every repository cloned once for the
// projects that share it, which projects use each one and what it takes on disk, a way to bring
// one up to date with its origin, and a way to delete the ones none uses, one by one or all at once.
import { useEffect, useState } from "preact/hooks";
import { t, tf } from "./i18n";
import type { Api } from "./gitApi";
import type { CatalogRepo } from "./appTypes";
import { choose } from "./choose";
import { errorMessage } from "./errors";
import { dangerBtn, smallBtn } from "./SettingsControls";
import { HTTP_CONFLICT, KB } from "./appConst";

// ALL is what `asking` holds while the deletion of every unused clone waits for an answer.
const ALL = "*";

// Asking is the repository whose deletion waits for an answer: the first question, or the
// gateway's own when the clone holds uncommitted work.
interface Asking {
  name: string;
  refusal?: string;
}

// sizeText writes a size on disk the way a person reads it.
export function sizeText(bytes: number): string {
  const mb = bytes / KB / KB;
  if (mb < 1) {
    return Math.max(1, Math.round(bytes / KB)) + " KB";
  }
  if (mb < KB) {
    return mb.toFixed(1) + " MB";
  }
  return (mb / KB).toFixed(1) + " GB";
}

// sortedCatalog puts the clones no project uses first, then each group by name: the ones that
// can be deleted are the ones a person comes here for.
export function sortedCatalog(repos: CatalogRepo[]): CatalogRepo[] {
  return [...repos].sort(
    (a, b) =>
      Number(a.projects.length > 0) - Number(b.projects.length > 0) ||
      a.name.localeCompare(b.name),
  );
}

// reasonOf is the gateway's reason for a refused request, or the fallback.
async function reasonOf(res: Response, fallback: string): Promise<string> {
  const body = await res.json().catch(() => ({}));
  return body.error || fallback;
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
  const reason = await reasonOf(res, t("Could not delete the repository"));
  if (res.status === HTTP_CONFLICT && !force) {
    return reason;
  }
  throw new Error(reason);
}

// useCatalog reads the catalog, again each time reload changes.
function useCatalog(api: Api, reload: number) {
  const [repos, setRepos] = useState<CatalogRepo[] | null>(null);
  useEffect(() => {
    let live = true;
    api("/v1/repos")
      .then(async (res) => {
        let d: { repos?: CatalogRepo[] } = {};
        if (res.ok) {
          d = await res.json();
        }
        if (live) {
          setRepos(sortedCatalog(d.repos ?? []));
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
  return repos;
}

export function RepoCatalogSettings({
  api,
  onNotice,
}: {
  api: Api;
  onNotice: (message: string, error?: boolean) => void;
}) {
  const [reload, setReload] = useState(0);
  const repos = useCatalog(api, reload);
  const [asking, setAsking] = useState<Asking | null>(null);
  const [busy, setBusy] = useState(false);
  const unused = (repos ?? []).filter((r) => r.projects.length === 0);

  // run does one action on the catalog, with the buttons off meanwhile, and reads it again after.
  const run = async (action: () => Promise<void>) => {
    setBusy(true);
    try {
      await action();
    } catch (e) {
      setAsking(null);
      onNotice(errorMessage(e), true);
    } finally {
      setBusy(false);
      setReload((n) => n + 1);
    }
  };

  const remove = (name: string, force: boolean) =>
    run(async () => {
      const refusal = await deleteRepo(api, name, force);
      if (refusal) {
        setAsking({ name, refusal });
        return;
      }
      setAsking(null);
      onNotice(tf("Deleted {name}", { name }));
    });

  // removeUnused deletes every clone no project uses. One that holds uncommitted work is kept:
  // discarding work is a question asked one repository at a time.
  const removeUnused = () =>
    run(async () => {
      let done = 0;
      for (const r of unused) {
        if (!(await deleteRepo(api, r.name, false))) {
          done += 1;
        }
      }
      setAsking(null);
      onNotice(
        tf("Deleted {done} of {total} unused repositories", {
          done,
          total: unused.length,
        }),
      );
    });

  const update = (name: string) =>
    run(async () => {
      const res = await api(
        "/v1/repos/" + encodeURIComponent(name) + "/update",
        {
          method: "POST",
        },
      );
      if (!res.ok) {
        throw new Error(
          await reasonOf(res, t("Could not update the repository")),
        );
      }
      onNotice(tf("{name} is up to date", { name }));
    });

  return (
    <section
      class="settings-group mt-6"
      aria-labelledby="repo-catalog-h"
      data-testid="repo-catalog-settings"
    >
      <h3 id="repo-catalog-h" class="text-sm font-semibold text-[#e8e8ea] mb-1">
        {t("Repositories in your workspace")}
        {!!repos?.length && (
          <span class="ml-1.5 text-xs font-normal text-[#7a7a8c] tabular-nums">
            {repos.length}
          </span>
        )}
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
      {unused.length > 1 && (
        <div class="flex items-center gap-2 mb-2" data-testid="catalog-unused">
          {asking?.name === ALL && (
            <>
              <span class="text-xs text-danger flex-1">
                {tf(
                  "The {n} repositories no project uses leave your workspace, with their local branches.",
                  { n: unused.length },
                )}
              </span>
              <button
                class={dangerBtn}
                disabled={busy}
                onClick={removeUnused}
                data-testid="catalog-unused-confirm"
              >
                {t("Delete")}
              </button>
              <button class={smallBtn} onClick={() => setAsking(null)}>
                {t("Cancel")}
              </button>
            </>
          )}
          {asking?.name !== ALL && (
            <button
              class={smallBtn}
              onClick={() => setAsking({ name: ALL })}
              data-testid="catalog-unused-delete"
            >
              {tf("Delete the {n} unused", { n: unused.length })}
            </button>
          )}
        </div>
      )}
      {!!repos?.length && (
        <ul class="settings-card">
          {repos.map((r) => (
            <li key={r.name} class="settings-row" data-testid="catalog-repo">
              <div class="settings-row-text min-w-0">
                <div class="text-sm text-[#e8e8ea] truncate">
                  {r.name}{" "}
                  <span class="text-xs text-[#7a7a8c] tabular-nums">
                    {sizeText(r.size ?? 0)}
                  </span>
                </div>
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
              <div class="settings-row-control flex gap-2">
                {asking?.name === r.name && (
                  <>
                    <button
                      class={dangerBtn}
                      disabled={busy}
                      onClick={() => remove(r.name, !!asking?.refusal)}
                      data-testid="catalog-confirm"
                    >
                      {choose(
                        asking?.refusal,
                        t("Discard the changes and delete it"),
                        t("Delete"),
                      )}
                    </button>
                    <button class={smallBtn} onClick={() => setAsking(null)}>
                      {t("Cancel")}
                    </button>
                  </>
                )}
                {asking?.name !== r.name && (
                  <button
                    class={smallBtn}
                    disabled={busy}
                    aria-label={tf("Update {name}", { name: r.name })}
                    onClick={() => update(r.name)}
                    data-testid="catalog-update"
                  >
                    {t("Update")}
                  </button>
                )}
                {asking?.name !== r.name && r.projects.length === 0 && (
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
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
