// The repositories of a project, as the New project and Edit project dialogs show them: the main
// one first (the project's own folder, which cannot be taken out), then the others, each with a
// button to remove it, and a field to add one more.
import { useState } from "preact/hooks";
import { t, tf } from "./i18n";
import { choose } from "./choose";
import {
  addRepoProblem,
  repoDraftOf,
  type MainRepo,
  type RepoDraft,
} from "./projectRepos";
import { IconSpinner, IconTrash } from "./icons";

const LABEL = "block text-sm text-[#9a9aaa] mb-1.5";
const HINT = "text-xs text-[#7a7a8c] mt-1";
const ROW = "flex items-start gap-2.5 px-3 py-2.5";

// RepoListItem is what one row of the list shows.
export interface RepoListItem {
  name: string;
  git_url: string;
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
  onRemove,
}: {
  r: RepoListItem;
  busy: boolean;
  conflict?: RepoConflict | null;
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

// RepoList lists the main repository and the others.
export function RepoList({
  main,
  repos,
  busy,
  conflict,
  onRemove,
}: {
  main: MainRepo | null;
  repos: RepoListItem[];
  busy: string;
  conflict?: RepoConflict | null;
  onRemove: (name: string, force: boolean) => void;
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
        <li class={ROW}>
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
        </li>
      )}
      {repos.map((r) => (
        <OtherRepoRow
          key={r.name}
          r={r}
          busy={busy === r.name}
          conflict={conflict}
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

// ReposSection is the whole block: the title, the list, the field and what the repositories are.
export function ReposSection({
  main,
  repos,
  busy,
  conflict,
  onRemove,
  onAdd,
  onChoose,
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
          onRemove={onRemove}
        />
        <AddRepo
          taken={repos.map((r) => r.name)}
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
