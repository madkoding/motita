// A project's repositories: the main one (the project's own folder) and the others the gateway
// clones inside it. The New project and Edit project dialogs both list them the same way.
import type { ProjectInfo, ProjectRepoInfo } from "./appTypes";

// RepoDraft is a repository the New project dialog will add once the project exists.
export interface RepoDraft {
  name: string;
  git_url: string;
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

// repoDraftOf reads a URL typed in the "add a repository" field: null when it names no repository.
export function repoDraftOf(url: string): RepoDraft | null {
  const gitUrl = url.trim();
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

// MainRepo is how the main repository is shown: its name and where it comes from.
export interface MainRepo {
  name: string;
  source: string;
}

// mainRepoOf describes a project's main repository: its URL when it was cloned, its folder otherwise.
export function mainRepoOf(p: Pick<ProjectInfo, "dir" | "git_url">): MainRepo {
  const source = p.git_url || p.dir;
  // The last part of the path that is not empty, so a trailing separator is skipped.
  const folder = /([^\\/]+)[\\/]*$/.exec(p.dir)?.[1] || p.dir;
  return { name: repoFolderOf(p.git_url || "") || folder, source };
}

// reposOf is a project's other repositories, never undefined: a project from before they existed
// has none.
export function reposOf(p: Pick<ProjectInfo, "repos">): ProjectRepoInfo[] {
  return p.repos ?? [];
}
