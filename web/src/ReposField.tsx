// The repositories of a project, as the New project and Edit project dialogs show them: the main
// one first (the project's own folder), then the others, each with a button to remove it, a field
// to add one more, and the repositories the workspace already has, to add without cloning again.
import { useState } from "preact/hooks";
import { t, tf } from "./i18n";
import { choose } from "./choose";
import {
  addRepoProblem,
  repoDraftOf,
  repoFolderOf,
  type MainRepo,
  type RepoDraft,
} from "./projectRepos";
import type { CatalogRepo } from "./appTypes";
import { IconSpinner, IconTrash } from "./icons";

const LABEL = "block text-sm text-[#9a9aaa] mb-1.5";
const HINT = "text-xs text-[#7a7a8c] mt-1";
const ROW = "flex items-start gap-2.5 px-3 py-2.5";

// RepoListItem is what one row of the list shows.
export interface RepoListItem {
  name: string;
  git_url: string;
  dir?: string;
  branch?: string;
  changes?: number;
}

// RepoConflict is a removal the gateway refused because the repository holds uncommitted work.
export interface RepoConflict {
  name: string;
  message: string;
}

// GitMark is the small branch icon in front of each repository.
function GitMark() {
  return (
    <svg
      class="flex-none mt-0.5 text-accent"
      width="16"
      height="16"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="2"
      stroke-linecap="round"
      stroke-linejoin="round"
      aria-hidden="true"
    >
      <circle cx="6" cy="6" r="2.5" />
      <circle cx="6" cy="18" r="2.5" />
      <circle cx="18" cy="9" r="2.5" />
      <path d="M6 8.5v7" />
      <path d="M18 11.5c0 4-12 1-12 4" />
    </svg>
  );
}

// stateOf is a repository's branch and uncommitted changes, as one short line.
function stateOf(r: RepoListItem): string {
  const parts = [r.branch || ""];
  if (r.changes) {
    parts.push(tf("{n} changes", { n: r.changes }));
  }
  return parts.filter(Boolean).join(" · ");
}

// OtherRepoRow is one of the other repositories, with its remove button, and the question the
// gateway asked when it holds work.
function OtherRepoRow({
  r,
  busy,
  conflict,
  alsoIn,
  onRemove,
}: {
  r: RepoListItem;
  busy: boolean;
  conflict?: RepoConflict | null;
  // The other projects that use the same clone of the repository.
  alsoIn: string[];
  onRemove: (name: string, force: boolean) => void;
}) {
  const refused = conflict?.name === r.name;
  return (
    <li class="border-t border-white/10" data-testid="repo-row">
      <div class={ROW}>
        <GitMark />
        <span class="flex-1 min-w-0">
          <span class="flex items-baseline gap-2">
            <span class="text-sm font-semibold text-[#e8e8ea] truncate">
              {r.name}
            </span>
            <span class="text-xs text-[#7a7a8c] font-mono truncate">
              {stateOf(r)}
            </span>
          </span>
          <span
            class="block text-xs text-[#7a7a8c] font-mono truncate"
            title={r.git_url}
          >
            {r.git_url}
          </span>
          {alsoIn.length > 0 && (
            <span
              class="block text-xs text-accent/80 truncate"
              data-testid="repo-also-in"
            >
              {tf("Also in {names}", { names: alsoIn.join(", ") })}
            </span>
          )}
        </span>
        <button
          type="button"
          class="flex-none p-1.5 -m-1 rounded-lg text-[#9a9aaa] hover:text-danger hover:bg-white/5 disabled:opacity-30"
          onClick={() => onRemove(r.name, false)}
          disabled={busy}
          aria-label={tf("Remove {name}", { name: r.name })}
          data-testid="repo-remove"
        >
          {choose(
            busy,
            <IconSpinner class="animate-spin" size={16} />,
            <IconTrash size={16} />,
          )}
        </button>
      </div>
      {refused && (
        <div
          class="px-3 pb-2.5 -mt-1 text-xs text-danger"
          data-testid="repo-conflict"
        >
          <p>{conflict?.message}</p>
          <button
            type="button"
            class="mt-1.5 px-2.5 min-h-[28px] rounded-lg border border-danger/50 text-danger font-semibold"
            onClick={() => onRemove(r.name, true)}
          >
            {t("Discard the changes and remove it")}
          </button>
        </div>
      )}
    </li>
  );
}

// MainRepoRow is the main repository. When it can be moved, it has a button that first asks, and
// then moves it into a folder of its own: the project's folder is left holding repositories only.
function MainRepoRow({
  main,
  busy,
  onMove,
}: {
  main: MainRepo;
  busy: boolean;
  onMove?: () => void;
}) {
  const [asking, setAsking] = useState(false);
  return (
    <li data-testid="repo-main">
      <div class={ROW}>
        <GitMark />
        <span class="flex-1 min-w-0">
          <span class="flex items-center gap-2">
            <span class="text-sm font-semibold text-[#e8e8ea] truncate">
              {main.name}
            </span>
            <span class="flex-none text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded-md bg-accent/15 text-accent">
              {t("main")}
            </span>
          </span>
          {main.source && (
            <span
              class="block text-xs text-[#7a7a8c] font-mono truncate"
              title={main.source}
            >
              {main.source}
            </span>
          )}
        </span>
        {onMove && (
          <button
            type="button"
            class="flex-none p-1.5 -m-1 rounded-lg text-[#9a9aaa] hover:text-danger hover:bg-white/5 disabled:opacity-30"
            onClick={() => setAsking(true)}
            disabled={busy || asking}
            aria-label={tf("Remove {name}", { name: main.name })}
            data-testid="repo-main-remove"
          >
            {choose(
              busy,
              <IconSpinner class="animate-spin" size={16} />,
              <IconTrash size={16} />,
            )}
          </button>
        )}
      </div>
      {onMove && asking && (
        <div
          class="px-3 pb-2.5 -mt-1 text-xs text-[#9a9aaa]"
          data-testid="repo-main-ask"
        >
          <p>
            {tf(
              "{name} moves into its own folder inside the project, and the project becomes a folder of repositories. Nothing is deleted.",
              { name: main.name },
            )}
          </p>
          <div class="mt-1.5 flex gap-2">
            <button
              type="button"
              class="px-2.5 min-h-[30px] rounded-lg bg-accent text-white font-semibold"
              onClick={() => {
                setAsking(false);
                onMove();
              }}
              data-testid="repo-main-move"
            >
              {t("Move it")}
            </button>
            <button
              type="button"
              class="px-2.5 min-h-[30px] rounded-lg border border-white/10 text-[#e8e8ea]"
              onClick={() => setAsking(false)}
            >
              {t("Cancel")}
            </button>
          </div>
        </div>
      )}
    </li>
  );
}

// alsoInOf names the other projects that use the catalog's clone at dir.
function alsoInOf(
  catalog: CatalogRepo[],
  dir: string | undefined,
  projectId: string,
): string[] {
  const entry = catalog.find((c) => c.dir === dir);
  return (entry?.projects ?? [])
    .filter((p) => p.id !== projectId)
    .map((p) => p.title);
}

// RepoList lists the main repository and the others.
export function RepoList({
  main,
  repos,
  busy,
  conflict,
  catalog = [],
  projectId = "",
  onRemove,
  onMoveMain,
}: {
  main: MainRepo | null;
  repos: RepoListItem[];
  busy: string;
  conflict?: RepoConflict | null;
  catalog?: CatalogRepo[];
  projectId?: string;
  onRemove: (name: string, force: boolean) => void;
  onMoveMain?: () => void;
}) {
  if (!main && repos.length === 0) {
    return null;
  }
  return (
    <ul
      class="rounded-xl border border-white/10 bg-black/20 overflow-hidden"
      data-testid="repo-list"
    >
      {main && (
        <MainRepoRow
          main={main}
          busy={busy === "*"}
          onMove={choose(main.movable, onMoveMain, undefined)}
        />
      )}
      {repos.map((r) => (
        <OtherRepoRow
          key={r.name}
          r={r}
          busy={busy === r.name}
          conflict={conflict}
          alsoIn={alsoInOf(catalog, r.dir, projectId)}
          onRemove={onRemove}
        />
      ))}
    </ul>
  );
}

// AddRepo is the field that adds one more repository. onAdd answers whether it was added, so the
// field empties only then.
export function AddRepo({
  taken,
  busy,
  onAdd,
}: {
  taken: string[];
  busy: boolean;
  onAdd: (draft: RepoDraft) => boolean | Promise<boolean>;
}) {
  const [url, setUrl] = useState("");
  const [problem, setProblem] = useState("");
  const add = async () => {
    const why = addRepoProblem(url, taken);
    setProblem(why);
    const draft = repoDraftOf(url);
    if (why || !draft) {
      return;
    }
    if (await onAdd(draft)) {
      setUrl("");
    }
  };
  return (
    <div>
      <div class="flex gap-2">
        <input
          class="flex-1 min-w-0 px-3 py-2 rounded-xl bg-black/30 border border-white/10 text-[#e8e8ea] font-mono text-sm focus:outline-hidden focus:border-accent"
          value={url}
          onInput={(e) => {
            setUrl((e.target as HTMLInputElement).value);
            setProblem("");
          }}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              void add();
            }
          }}
          placeholder={t("https://github.com/user/other-repo.git")}
          aria-label={t("Add a repository")}
          data-testid="repo-add-url"
          disabled={busy}
        />
        <button
          type="button"
          class="flex-none px-3 min-h-[38px] rounded-xl border border-accent/60 text-accent text-sm font-semibold disabled:opacity-30"
          onClick={() => void add()}
          disabled={busy || !url.trim()}
          data-testid="repo-add"
        >
          {choose(
            busy,
            <IconSpinner class="animate-spin" size={16} />,
            t("Add"),
          )}
        </button>
      </div>
      {problem && (
        <p class="text-xs text-danger mt-1" data-testid="repo-add-problem">
          {choose(
            problem === "taken",
            t("The project already has a repository with that name."),
            t("That is not a repository URL."),
          )}
        </p>
      )}
    </div>
  );
}

// sameRepo is the form two URLs of one repository share: no trailing slash or ".git", any case.
function sameRepo(url: string): string {
  return url
    .trim()
    .replace(/\/+$/, "")
    .replace(/\.git$/, "")
    .toLowerCase();
}

// CatalogPicks are the repositories the workspace already has that the list does not: one click
// adds one, and the project shares the clone instead of making another.
function CatalogPicks({
  catalog,
  repos,
  busy,
  onAdd,
}: {
  catalog: CatalogRepo[];
  repos: RepoListItem[];
  busy: boolean;
  onAdd: (draft: RepoDraft) => boolean | Promise<boolean>;
}) {
  const urls = repos.map((r) => sameRepo(r.git_url));
  const names = repos.map((r) => r.name);
  const picks = catalog
    .map((c) => ({
      name: repoFolderOf(c.git_url) || c.name,
      git_url: c.git_url,
    }))
    .filter(
      (c) => !urls.includes(sameRepo(c.git_url)) && !names.includes(c.name),
    );
  if (picks.length === 0) {
    return null;
  }
  return (
    <div data-testid="repo-catalog">
      <p class="text-xs text-[#7a7a8c] mb-1">
        {t("Already in your workspace")}
      </p>
      <div class="flex flex-wrap gap-1.5">
        {picks.map((c) => (
          <button
            key={c.git_url}
            type="button"
            class="px-2.5 min-h-[30px] rounded-lg border border-white/10 bg-black/20 text-sm text-[#e8e8ea] hover:border-accent/60 disabled:opacity-30"
            onClick={() => void onAdd(c)}
            disabled={busy}
            title={c.git_url}
            aria-label={tf("Add {name}", { name: c.name })}
            data-testid="repo-catalog-pick"
          >
            + {c.name}
          </button>
        ))}
      </div>
    </div>
  );
}

// ReposSection is the whole block: the title, the list, the field and what the repositories are.
export function ReposSection({
  main,
  repos,
  busy,
  conflict,
  onRemove,
  onAdd,
  onChoose,
  onMoveMain,
  catalog = [],
  projectId,
  hint,
}: {
  main: MainRepo | null;
  repos: RepoListItem[];
  busy: string;
  conflict?: RepoConflict | null;
  onRemove: (name: string, force: boolean) => void;
  onAdd: (draft: RepoDraft) => boolean | Promise<boolean>;
  // Opens the picker of the person's own repositories, when a git host is connected.
  onChoose?: () => void;
  // Moves the main repository into a folder of its own (the edit dialog only).
  onMoveMain?: () => void;
  // The repositories the workspace already has, and the project the dialog is about (none yet
  // in the New project one).
  catalog?: CatalogRepo[];
  projectId?: string;
  hint: string;
}) {
  return (
    <div data-testid="repos-section">
      <label class={LABEL}>{t("Repositories")}</label>
      <div class="space-y-2">
        <RepoList
          main={main}
          repos={repos}
          busy={busy}
          conflict={conflict}
          catalog={catalog}
          projectId={projectId}
          onRemove={onRemove}
          onMoveMain={onMoveMain}
        />
        <AddRepo
          taken={repos.map((r) => r.name)}
          busy={busy === "+"}
          onAdd={onAdd}
        />
        <CatalogPicks
          catalog={catalog}
          repos={repos}
          busy={busy === "+"}
          onAdd={onAdd}
        />
        {onChoose && (
          <button
            type="button"
            class="text-sm text-accent underline underline-offset-2"
            onClick={onChoose}
            data-testid="repo-choose"
          >
            {t("Add from my repositories")}
          </button>
        )}
      </div>
      <p class={HINT}>{hint}</p>
    </div>
  );
}
