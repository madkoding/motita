// When a talk is about a repository, the session may belong in a project: the repository named in
// the person's messages is read here, and what to offer follows from where the session is. Nothing
// is done by this file; the offer is a card the person answers (ProjectHintCard.tsx).
import type { Message, ProjectInfo, SessionInfo } from "./appTypes";
import { repoFolderOf } from "./projectRepos";

// RepoMention is a repository a message names: the address to clone and the name it goes by.
export interface RepoMention {
  url: string;
  name: string;
}

// ProjectHint is what to offer: a new project from the repository and this session moved into it,
// this session moved into the project that already has the repository, or the repository added to
// the project the session is in.
export type ProjectHint =
  | { kind: "create"; repo: RepoMention }
  | { kind: "move"; repo: RepoMention; projectId: string; projectTitle: string }
  | { kind: "add"; repo: RepoMention; projectId: string; projectTitle: string };

const HTTP_URL = /https?:\/\/[\w.-]+(?::\d+)?(?:\/[\w.~%+-]+)+/g;
const SSH_URL = /git@[\w.-]+:[\w.~-]+(?:\/[\w.~-]+)+/g;
// The first path element of a page of a repository on a host, after owner and name.
const REPO_PAGE = new Set([
  "pull",
  "pulls",
  "issues",
  "tree",
  "blob",
  "commit",
  "commits",
  "actions",
  "releases",
  "wiki",
  "merge_requests",
  "-",
]);

// addressOf cuts a page of a repository (an issue, a file) back to the repository, and drops the
// dots a sentence puts after an address.
function addressOf(raw: string): string {
  const parts = raw.replace(/\.+$/, "").split("/");
  const page = parts.findIndex((p, i) => i >= 2 && REPO_PAGE.has(p));
  if (page < 0) {
    return parts.join("/");
  }
  return parts.slice(0, page).join("/");
}

// repoKey is an address without what differs between spellings of it: the scheme, the user, ".git"
// and the case. https://github.com/a/b.git and git@github.com:a/b are the same repository.
export function repoKey(url: string): string {
  return url
    .trim()
    .toLowerCase()
    .replace(/^[a-z+]+:\/\//, "")
    .replace(/^git@/, "")
    .replace(":", "/")
    .replace(/\/+$/, "")
    .replace(/\.git$/, "");
}

// repoMentions reads the repositories a text names, in the order it names them. A web address
// needs an owner and a name after the host: a bare site is not a repository.
export function repoMentions(text: string): RepoMention[] {
  const found: RepoMention[] = [];
  const seen = new Set<string>();
  const addresses = [
    ...(text.match(HTTP_URL) ?? []).map(addressOf),
    ...(text.match(SSH_URL) ?? []),
  ];
  for (const url of addresses) {
    const name = repoFolderOf(url);
    const owned = repoKey(url).split("/").length > 2;
    if (!name || !owned || seen.has(repoKey(url))) {
      continue;
    }
    seen.add(repoKey(url));
    found.push({ url, name });
  }
  return found;
}

// mentionedIn is the repositories the person named in this conversation, latest message first.
export function mentionedIn(messages: Message[]): RepoMention[] {
  const found: RepoMention[] = [];
  for (const m of [...messages].reverse()) {
    if (m.role === "user" && !m.shown) {
      found.push(...repoMentions(m.text));
    }
  }
  return found;
}

// reposOfProject are the keys of every repository a project has: the main one and the others.
function reposOfProject(p: ProjectInfo): Set<string> {
  const keys = (p.repos ?? []).map((r) => repoKey(r.git_url));
  if (p.git_url) {
    keys.push(repoKey(p.git_url));
  }
  return new Set(keys);
}

// projectHint is what to offer for this conversation, or null. A session in a project is offered
// the first repository it names that the project lacks; a session in none, the first it names,
// with the project that has it when there is one.
export function projectHint(
  messages: Message[],
  session: SessionInfo | undefined,
  projects: ProjectInfo[],
): ProjectHint | null {
  const mentions = mentionedIn(messages);
  if (!session || mentions.length === 0) {
    return null;
  }
  if (!session.project_id) {
    const repo = mentions[0];
    const owner = projects.find((p) =>
      reposOfProject(p).has(repoKey(repo.url)),
    );
    if (owner) {
      return {
        kind: "move",
        repo,
        projectId: owner.id,
        projectTitle: owner.title,
      };
    }
    return { kind: "create", repo };
  }
  const project = projects.find((p) => p.id === session.project_id);
  if (!project) {
    return null;
  }
  const have = reposOfProject(project);
  const repo = mentions.find((m) => !have.has(repoKey(m.url)));
  if (!repo) {
    return null;
  }
  return {
    kind: "add",
    repo,
    projectId: project.id,
    projectTitle: project.title,
  };
}

// hintKey names an offer, so one the person turned down is not made again for the same session.
export function hintKey(sessionId: string, hint: ProjectHint): string {
  return `${sessionId}|${hint.kind}|${repoKey(hint.repo.url)}`;
}
