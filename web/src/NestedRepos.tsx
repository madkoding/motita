// The git repositories found inside a project's folder, drawn as a tree under the project's header.
// The folder itself is not a repository: each one found is its own, with its own menu.
import type { VNode } from "preact";
import { t, tf } from "./i18n";
import { choose } from "./choose";
import { useApp } from "./appStore";
import { RowMenu } from "./SessionRow";
import {
  IconArchive,
  IconArrowDown,
  IconArrowUp,
  IconBack,
  IconChat,
  IconCommit,
  IconDots,
  IconEye,
  IconFolder,
  IconPullRequest,
  IconRefresh,
  IconTrash,
} from "./icons";
import type { NestedRepo, ProjectInfo } from "./appTypes";
import type { AskKind, RepoRef } from "./useRepoGit";

const ITEM_PLAIN =
  "row-menu-item w-full flex items-center gap-2 px-3 py-2 text-sm text-[#e8e8ea] hover:bg-white/5";

// menuKey tells the repositories' menus apart from the project's and from each other.
const menuKey = (p: ProjectInfo, r: NestedRepo) => `${p.id}\u0000${r.path}`;

interface RepoItem {
  id: string;
  icon: (p: { size: number; class?: string }) => VNode;
  label: string;
  hint?: string;
  run: () => void;
}

// repoItems are the entries of a repository's menu: what is read, what is done at once to the
// repository, and what is asked of the agent, which holds the git logins.
function repoItems(
  repo: string,
  details: () => void,
  update: () => void,
  act: (action: string) => void,
  ask: (kind: AskKind) => void,
): RepoItem[] {
  const hint = (text: string) => tf(text, { repo });
  return [
    {
      id: "repo-details",
      icon: IconEye,
      label: t("Changes, history and branches…"),
      run: details,
    },
    {
      id: "refresh-nested",
      icon: IconRefresh,
      label: tf("Update {repo}", { repo }),
      hint: hint(
        "Brings {repo} up to date with its origin; nothing is forced.",
      ),
      run: update,
    },
    {
      id: "repo-pull",
      icon: IconArrowDown,
      label: t("Pull this branch"),
      hint: t("Brings in what origin has, only when nothing is uncommitted."),
      run: () => act("pull"),
    },
    {
      id: "repo-main",
      icon: IconBack,
      label: t("Back to the main branch"),
      hint: t("Only when nothing is uncommitted."),
      run: () => act("main"),
    },
    {
      id: "repo-stash",
      icon: IconArchive,
      label: t("Put changes aside"),
      hint: t("Keeps them in the stash, new files included."),
      run: () => act("stash"),
    },
    {
      id: "repo-clean",
      icon: IconTrash,
      label: t("Delete merged branches"),
      hint: t("Only the ones whose work is already in the main branch."),
      run: () => act("clean"),
    },
    {
      id: "repo-commit",
      icon: IconCommit,
      label: t("Save changes (commit)…"),
      hint: t("The agent writes the message and shows it to you first."),
      run: () => ask("commit"),
    },
    {
      id: "repo-push",
      icon: IconArrowUp,
      label: t("Push my branch…"),
      run: () => ask("push"),
    },
    {
      id: "repo-pr",
      icon: IconPullRequest,
      label: t("Open a pull request…"),
      run: () => ask("pr"),
    },
    {
      id: "repo-work",
      icon: IconChat,
      label: t("Work on it with the agent…"),
      run: () => ask("work"),
    },
  ];
}

// MAIN_BRANCHES are the names taken as a repository's main line when it does not say its own.
const MAIN_BRANCHES = ["main", "master", "develop"];

// isOffMain says the repository is on a branch that is not its main line; with no branch known
// there is nothing to flag.
const isOffMain = (r: NestedRepo) =>
  !!r.branch && r.branch !== r.main_branch && !MAIN_BRANCHES.includes(r.branch);

// repoTooltip names the branch (and the changes) on the repository's icon, so a long name never
// takes the room of the row's menu.
const repoTooltip = (r: NestedRepo) => {
  if (!r.branch) {
    return r.path;
  }
  return (
    tf("Branch {branch}", { branch: r.branch }) +
    choose(!!r.changes, ` · ${r.changes}±`, "")
  );
};

function NestedRepoRow({ p, r }: { p: ProjectInfo; r: NestedRepo }) {
  const { menus, projects, repoGit } = useApp();
  const key = menuKey(p, r);
  const ref: RepoRef = { projectId: p.id, repo: r.path };
  const items = repoItems(
    r.path,
    () => repoGit.openRepoGit(ref),
    () => void projects.refreshProjectMain(p.id, r.path),
    (action) => void repoGit.repoAction(ref, action),
    (kind) => void repoGit.askAgent(ref, kind),
  );
  const open = menus.rowMenu?.id === key;
  const offMain = isOffMain(r);
  return (
    <div
      class="flex items-center gap-1.5 pl-2 pr-1 py-0.5 text-xs text-[#c8c8d0] rounded-sm hover:bg-white/5"
      data-testid="nested-repo"
    >
      <span
        class={`flex-none inline-flex ${choose(offMain, "text-amber-400", "text-accent/60")}`}
        data-testid="nested-state"
        data-off-main={offMain ? "true" : undefined}
        title={repoTooltip(r)}
      >
        <IconFolder size={11} />
      </span>
      <span class="truncate" title={r.path}>
        {r.path}
      </span>
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
            {items.map((i) => (
              <button
                key={i.id}
                class={ITEM_PLAIN}
                data-testid={i.id}
                title={i.hint}
                onClick={(e) => {
                  e.stopPropagation();
                  i.run();
                  menus.closeRowMenu();
                }}
              >
                <i.icon size={14} class="flex-none" />
                {i.label}
              </button>
            ))}
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
