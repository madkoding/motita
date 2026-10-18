// When a talk is about a repository, the session may belong in a project: the repository named in
// the person's messages is read here, and what to offer follows from where the session is. Nothing
// is done by this file; the offer is a card the person answers (ProjectHintCard.tsx).
import type { Message, ProjectInfo, SessionInfo } from "./appTypes";
import { repoFolderOf } from "./projectRepos";
import { NEW_SESSION_TITLE } from "./appConst";

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
  | { kind: "add"; repo: RepoMention; projectId: string; projectTitle: string }
  | { kind: "start"; title: string };

// Marks a text puts around an address that are not part of it.
const EDGE = "<>()[]{}\"'`,;:!?*.";
const NAME_PART = /^[\w.-]+$/;

// trimEdges takes the marks off both ends of a word, and any trailing run of c after that.
function trimEdges(word: string): string {
  let start = 0;
  let end = word.length;
  while (start < end && EDGE.includes(word[start])) {
    start++;
  }
  while (end > start && EDGE.includes(word[end - 1])) {
    end--;
  }
  return word.slice(start, end);
}

// trimEnd takes every c off the end of a text.
function trimEnd(text: string, c: string): string {
  let end = text.length;
  while (end > 0 && text[end - 1] === c) {
    end--;
  }
  return text.slice(0, end);
}

// addressIn is the repository address a word holds, from its scheme (or "git@") on, "" for none.
function addressIn(word: string): string {
  const at = ["https://", "http://", "git@"]
    .map((scheme) => word.indexOf(scheme))
    .filter((i) => i >= 0);
  if (at.length === 0) {
    return "";
  }
  return trimEdges(word.slice(Math.min(...at)));
}

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
  const parts = raw.split("/");
  const page = parts.findIndex((p, i) => i >= 2 && REPO_PAGE.has(p));
  if (page < 0) {
    return parts.join("/");
  }
  return parts.slice(0, page).join("/");
}

// repoKey is an address without what differs between spellings of it: the scheme, the user, ".git"
// and the case. https://github.com/a/b.git and git@github.com:a/b are the same repository.
export function repoKey(url: string): string {
  return trimEnd(url.trim().toLowerCase(), "/")
    .replace(/^[a-z+]+:\/\//, "")
    .replace(/^git@/, "")
    .replace(":", "/")
    .replace(/\.git$/, "");
}

// repoMentions reads the repositories a text names, in the order it names them. A web address
// needs an owner and a name after the host: a bare site is not a repository.
export function repoMentions(text: string): RepoMention[] {
  const found: RepoMention[] = [];
  const seen = new Set<string>();
  const addresses = text
    .split(/\s+/)
    .map(addressIn)
    .filter((a) => a !== "")
    .map(addressOf);
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

// shorthandMentions reads `owner/name` and `name` (in backticks) as the known repositories they
// say. Only a repository some project already has is read this way: any other pair of words with
// a slash is a path, not a repository.
function shorthandMentions(text: string, known: RepoMention[]): RepoMention[] {
  const spoken = text.split(/\s+/).filter((w) => addressIn(w) === "");
  const pairs = spoken
    .map(trimEdges)
    .filter((w) => w.split("/").length === 2)
    .filter((w) => w.split("/").every((part) => NAME_PART.test(part)));
  const quoted = text
    .split("`")
    .filter((_, i) => i % 2 === 1)
    .filter((w) => NAME_PART.test(w));
  const words = [...pairs, ...quoted].map((w) => w.toLowerCase());
  return known.filter((k) => {
    const key = repoKey(k.url);
    return words.some(
      (w) => key.endsWith("/" + w) || k.name.toLowerCase() === w,
    );
  });
}

// mentionedIn is the repositories the person named in this conversation, latest message first:
// by address, or by the short name of one of the known repositories.
export function mentionedIn(
  messages: Message[],
  known: RepoMention[] = [],
): RepoMention[] {
  const found: RepoMention[] = [];
  for (const m of [...messages].reverse()) {
    if (m.role === "user" && !m.shown) {
      found.push(...repoMentions(m.text), ...shorthandMentions(m.text, known));
    }
  }
  return found;
}

// knownRepos is every repository the projects have, as the mentions they would be.
export function knownRepos(projects: ProjectInfo[]): RepoMention[] {
  return projects.flatMap((p) => {
    const urls = [p.git_url ?? "", ...(p.repos ?? []).map((r) => r.git_url)];
    return urls
      .filter((url) => url !== "")
      .map((url) => ({ url, name: repoFolderOf(url) }));
  });
}

// reposOfProject are the keys of every repository a project has: the main one and the others.
function reposOfProject(p: ProjectInfo): Set<string> {
  const keys = (p.repos ?? []).map((r) => repoKey(r.git_url));
  if (p.git_url) {
    keys.push(repoKey(p.git_url));
  }
  return new Set(keys);
}

// startHint is the offer for a session with no project and no repository that has work in it:
// a project of its own, named after the session. A session with no real title has no name to give.
function startHint(session: SessionInfo): ProjectHint | null {
  if (
    session.project_id ||
    !session.changes ||
    session.title === NEW_SESSION_TITLE
  ) {
    return null;
  }
  return { kind: "start", title: session.title };
}

// projectHint is what to offer for this conversation, or null. A session in a project is offered
// the first repository it names that the project lacks; a session in none, the first it names,
// with the project that has it when there is one.
export function projectHint(
  messages: Message[],
  session: SessionInfo | undefined,
  projects: ProjectInfo[],
): ProjectHint | null {
  if (!session) {
    return null;
  }
  const mentions = mentionedIn(messages, knownRepos(projects));
  if (mentions.length === 0) {
    return startHint(session);
  }
  if (!session.project_id) {
    const first = mentions[0];
    const owner = projects.find((p) =>
      reposOfProject(p).has(repoKey(first.url)),
    );
    if (owner) {
      return {
        kind: "move",
        repo: first,
        projectId: owner.id,
        projectTitle: owner.title,
      };
    }
    return { kind: "create", repo: first };
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
  if (hint.kind === "start") {
    return `${sessionId}|start`;
  }
  return `${sessionId}|${hint.kind}|${repoKey(hint.repo.url)}`;
}
