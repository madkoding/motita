// A repository in the sidebar: a container with the sessions that work on it directly. Its pills sit
// on the top border of the box like a project's: the name on the left, "+" and "⋯" on the right.
import { useCallback } from "preact/hooks";
import { t, tf } from "./i18n";
import { choose } from "./choose";
import { useApp } from "./appStore";
import { NewSessionButton } from "./NewSessionButton";
import { RowMenu, SessionRow } from "./SessionRow";
import { IconDots, IconRepo } from "./icons";
import { repoScope } from "./useRepositories";
import type { RepositoryInfo } from "./appTypes";

const ITEM_PLAIN =
  "row-menu-item w-full flex items-center gap-2 px-3 py-2 text-sm text-[#e8e8ea] hover:bg-white/5";
const ITEM_DANGER =
  "row-menu-item w-full flex items-center gap-2 px-3 py-2 text-sm text-danger hover:bg-danger/10";

// repoTooltip says what a repository is: where it lives, its branch and its changes.
export function repoTooltip(r: RepositoryInfo): string {
  const facts = [r.git_url || r.dir];
  if (r.branch) {
    facts.push(tf("branch {branch}", { branch: r.branch }));
  }
  if (r.changes) {
    facts.push(tf("{n} changes", { n: r.changes }));
  }
  return facts.join(" · ");
}

// RepositoryMenu is the "⋯" menu of a repository.
function RepositoryMenu({ r }: { r: RepositoryInfo }) {
  const { menus, actions, repositories } = useApp();
  return (
    <RowMenu>
      <button
        class={ITEM_PLAIN}
        data-testid="new-isolated-repo-session"
        title={t(
          "Works in a worktree of its own, so it can run in parallel with the others.",
        )}
        onClick={(e) => {
          e.stopPropagation();
          void actions.createSession(repoScope(r.id), true);
          menus.closeRowMenu();
        }}
      >
        {t("New isolated session")}
      </button>
      <button
        class={ITEM_DANGER}
        data-testid="delete-repository"
        onClick={(e) => {
          e.stopPropagation();
          void repositories.deleteRepository(r);
          menus.closeRowMenu();
        }}
      >
        {t("Delete repository")}
      </button>
    </RowMenu>
  );
}

// RepositoryActions is the header's "+" and "⋯" buttons.
function RepositoryActions({ r }: { r: RepositoryInfo }) {
  const { menus, actions } = useApp();
  const { createSession } = actions;
  const scope = repoScope(r.id);
  const open = menus.rowMenu?.id === scope;
  const newSession = useCallback(
    (e: MouseEvent) => {
      e.stopPropagation();
      createSession(scope);
    },
    [createSession, scope],
  );
  return (
    <>
      <NewSessionButton
        compact
        loading={actions.creatingSession === scope}
        onClick={newSession}
        title={t("New session in repository")}
      />
      <div
        class={`row-actions relative flex-none${choose(open, " row-actions-open", "")}`}
      >
        <button
          class="p-0.5 rounded-sm hover:bg-white/10"
          title={t("More actions")}
          data-row-menu-trigger
          onClick={(e) => {
            e.stopPropagation();
            menus.toggleRowMenu(e.currentTarget as HTMLElement, {
              type: "project",
              id: scope,
              title: r.name,
            });
          }}
        >
          <IconDots size={14} />
        </button>
        {open && <RepositoryMenu r={r} />}
      </div>
    </>
  );
}

// RepositoryGroup is a repository and the sessions that work on it.
export function RepositoryGroup({ r }: { r: RepositoryInfo }) {
  const { lists } = useApp();
  const scope = repoScope(r.id);
  const sessions = lists.sessions.filter((s) => s.project_id === scope);
  return (
    <div
      class="mt-3.5 rounded-xl bg-white/2 border border-white/6"
      data-testid="repository-group"
    >
      <div class="project-header group select-none">
        <div class="project-bar">
          <div class="project-pill">
            <IconRepo size={12} class="flex-none text-accent/60" />
            <span class="project-name truncate" title={repoTooltip(r)}>
              {r.name}
            </span>
            {r.local && (
              <span class="flex-none px-1 py-px rounded-sm bg-white/5 text-[#9a9aaa] uppercase tracking-wide text-[9px]">
                {t("local")}
              </span>
            )}
          </div>
          <RepositoryActions r={r} />
        </div>
      </div>
      <div class="px-1.5 pb-1.5 space-y-0.5">
        {sessions.map((s) => (
          <SessionRow key={s.id} s={s} />
        ))}
        {sessions.length === 0 && (
          <div class="px-3 py-1.5 text-xs text-[#7a7a8c] italic">
            {t("No sessions yet")}
          </div>
        )}
      </div>
    </div>
  );
}
