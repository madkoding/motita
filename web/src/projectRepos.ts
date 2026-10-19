// A project's repositories: the main one (the project's own folder) and the others the gateway
// clones inside it. The New project and Edit project dialogs both list them the same way.
import type { CatalogRepo, ProjectInfo, ProjectRepoInfo } from "./appTypes";

// RepoDraft is a repository the New project dialog will add once the project exists.
export interface RepoDraft {
  name: string;
  git_url: string;
  // path marks a repository already on this machine: the gateway uses that folder where it is,
  // with worktrees of its own, instead of cloning it.
  path?: string;
}

// The folder name the gateway accepts for a repository: a simple name that is not hidden.
const REPO_NAME = /^[A-Za-z0-9][A-Za-z0-9._-]*$/;

// repoFolderOf is the folder the gateway gives a repository: the URL's last element, without a
// trailing slash or ".git". The same rule as the gateway's, so the list shows the name it will use.
export function repoFolderOf(url: string): string {
  let u = url.trim();
  while (u.endsWith("/")) {
    u = u.slice(0, -1);
  }
  if (u.endsWith(".git")) {
    u = u.slice(0, -".git".length);
  }
  return u.slice(
    Math.max(u.lastIndexOf("/"), u.lastIndexOf(":"), u.lastIndexOf("\\")) + 1,
  );
}

// isLocalFolder says whether text is the absolute path of a folder on this machine, as opposed to
// a repository address: a path that ends in ".git" is a bare repository to clone from.
export function isLocalFolder(text: string): boolean {
  const path = text.trim();
  return /^(\/|[A-Za-z]:[\\/]|\\\\)/.test(path) && !/\.git[\\/]*$/.test(path);
}

// localRepoDraft is a repository this machine already has, by its folder: null when the folder's
// name cannot be a repository's.
export function localRepoDraft(folder: string): RepoDraft | null {
  const path = folder.trim().replace(/[\\/]+$/, "");
  const name = repoFolderOf(path);
  if (!REPO_NAME.test(name) || name.includes("..")) {
    return null;
  }
  return { name, git_url: path, path };
}

// repoDraftOf reads a URL typed in the "add a repository" field: null when it names no repository.
// The path of a folder is a repository the machine already has, which is used where it is.
export function repoDraftOf(url: string): RepoDraft | null {
  const gitUrl = url.trim();
  if (isLocalFolder(gitUrl)) {
    return localRepoDraft(gitUrl);
  }
  const name = repoFolderOf(gitUrl);
  // A URL or a path: a bare word names no repository anywhere.
  const located = /[/:\\]/.test(gitUrl);
  if (!located || !REPO_NAME.test(name) || name.includes("..")) {
    return null;
  }
  return { name, git_url: gitUrl };
}

// addRepoProblem says why the URL cannot join the list, '' when it can: it names no repository,
// or the list already has one by that name (each gets its own folder).
export function addRepoProblem(url: string, taken: string[]): string {
  const draft = repoDraftOf(url);
  if (!draft) {
    return "not-a-repo";
  }
  if (taken.includes(draft.name)) {
    return "taken";
  }
  return "";
}

// lastPathPart is the last part of a path that is not empty, so a trailing separator is skipped;
// "" when the path has none.
function lastPathPart(path: string): string {
  let end = path.length;
  while (end > 0 && (path[end - 1] === "/" || path[end - 1] === "\\")) {
    end -= 1;
  }
  const rest = path.slice(0, end);
  return rest.slice(
    Math.max(rest.lastIndexOf("/"), rest.lastIndexOf("\\")) + 1,
  );
}

// MainRepo is how the main repository is shown: its name and where it comes from.
export interface MainRepo {
  name: string;
  source: string;
  // The repository can be moved out of the project's folder into one of its own.
  movable?: boolean;
}

// mainRepoOf describes a project's main repository: its URL when it was cloned, its folder otherwise.
export function mainRepoOf(p: Pick<ProjectInfo, "dir" | "git_url">): MainRepo {
  const source = p.git_url || p.dir;
  const folder = lastPathPart(p.dir) || p.dir;
  return { name: repoFolderOf(p.git_url || "") || folder, source };
}

// shownMainOf is the main repository the edit dialog lists: none once the project's folder only
// holds repositories (no URL of its own, and others inside it), because then the folder is not one.
export function shownMainOf(
  p: Pick<ProjectInfo, "dir" | "git_url" | "repos">,
): MainRepo | null {
  if (!p.git_url && reposOf(p).length > 0) {
    return null;
  }
  return { ...mainRepoOf(p), movable: !!p.git_url };
}

// reposOf is a project's other repositories, never undefined: a project from before they existed
// has none.
export function reposOf(p: Pick<ProjectInfo, "repos">): ProjectRepoInfo[] {
  return p.repos ?? [];
}

// sameRepoUrl is the form two URLs of one repository share: no trailing slash or ".git", any case.
export function sameRepoUrl(url: string): string {
  let u = url.trim().toLowerCase();
  while (u.endsWith("/")) {
    u = u.slice(0, -1);
  }
  return u.replace(/\.git$/, "");
}

// knownRepos is the workspace catalog as a set of sameRepoUrl addresses, for a field or picker
// that marks the repositories it would not clone again.
export function knownRepos(catalog: CatalogRepo[]): ReadonlySet<string> {
  return new Set(catalog.map((c) => sameRepoUrl(c.git_url)));
}
