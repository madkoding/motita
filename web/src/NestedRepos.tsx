// The git repositories found inside a project's folder, drawn as a tree under the project's header.
// The folder itself is not a repository: each one found is its own, with its own menu.
import { t, tf } from "./i18n";
import { choose } from "./choose";
import { useApp } from "./appStore";
import { RowMenu } from "./SessionRow";
import { IconDots, IconFolder } from "./icons";
import type { NestedRepo, ProjectInfo } from "./appTypes";

const ITEM_PLAIN =
  "row-menu-item w-full flex items-center gap-2 px-3 py-2 text-sm text-[#e8e8ea] hover:bg-white/5";

// menuKey tells the repositories' menus apart from the project's and from each other.
const menuKey = (p: ProjectInfo, r: NestedRepo) => `${p.id}\u0000${r.path}`;

function NestedRepoRow({ p, r }: { p: ProjectInfo; r: NestedRepo }) {
  const { menus, projects } = useApp();
  const key = menuKey(p, r);
  const open = menus.rowMenu?.id === key;
  return (
    <div
      class="flex items-center gap-1.5 pl-2 pr-1 py-0.5 text-xs text-[#c8c8d0] rounded-sm hover:bg-white/5"
      data-testid="nested-repo"
    >
      <IconFolder size={11} class="flex-none text-accent/60" />
      <span class="truncate" title={r.path}>
        {r.path}
      </span>
      {r.branch && (
        <span
          class="flex-none opacity-60 truncate"
          data-testid="nested-state"
          title={tf("Branch {branch}", { branch: r.branch })}
        >
          {r.branch}
          {choose(!!r.changes, ` · ${r.changes}±`, "")}
        </span>
      )}
      <div class="relative flex-none ml-auto">
        <button
          class="p-0.5 rounded-sm hover:bg-white/10"
          title={t("More actions")}
          data-row-menu-trigger
          data-testid="nested-menu"
          onClick={(e) => {
            e.stopPropagation();
            menus.toggleRowMenu(e.currentTarget as HTMLElement, {
              type: "project",
              id: key,
              title: r.path,
            });
          }}
        >
          <IconDots size={14} />
        </button>
        {open && (
          <RowMenu>
            <button
              class={ITEM_PLAIN}
              data-testid="refresh-nested"
              title={tf(
                "Brings {repo} up to date with its origin; nothing is forced.",
                { repo: r.path },
              )}
              onClick={(e) => {
                e.stopPropagation();
                void projects.refreshProjectMain(p.id, r.path);
                menus.closeRowMenu();
              }}
            >
              {tf("Update {repo}", { repo: r.path })}
            </button>
          </RowMenu>
        )}
      </div>
    </div>
  );
}

// NestedRepos is the tree: nothing at all for a folder that holds no repository.
export function NestedRepos({ p }: { p: ProjectInfo }) {
  if (!p.nested_repos?.length) {
    return null;
  }
  return (
    <div class="ml-2 mb-1 pl-1.5 border-l border-white/10 space-y-0.5">
      {p.nested_repos.map((r) => (
        <NestedRepoRow key={r.path} p={p} r={r} />
      ))}
    </div>
  );
}
