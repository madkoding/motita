// When a talk is about a repository, the session may belong in a project: the repository named in
// the person's messages is read here, and what to offer follows from where the session is. Nothing
// is done by this file; the offer is a card the person answers (ProjectHintCard.tsx).
import type { Message, SessionInfo } from "./appTypes";
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
  | { kind: "start"; title: string };

// Marks a text puts around an address that are not part of it.
const EDGE = "<>()[]{}\"'`,;:!?*.";

// trimMarks takes the marks off the end of an address a word starts with.
function trimMarks(word: string): string {
  let end = word.length;
  while (end > 0 && EDGE.includes(word[end - 1])) {
    end--;
  }
  return word.slice(0, end);
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
  return trimMarks(word.slice(Math.min(...at)));
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

// projectHint is what to offer for this conversation, or null. A session in no project that names
// a repository is offered a project made from the first one; one with work and no repository, a
// project of its own.
export function projectHint(
  messages: Message[],
  session: SessionInfo | undefined,
): ProjectHint | null {
  if (!session) {
    return null;
  }
  const mentions = mentionedIn(messages);
  if (mentions.length === 0) {
    return startHint(session);
  }
  if (session.project_id) {
    return null;
  }
  return { kind: "create", repo: mentions[0] };
}

// hintKey names an offer, so one the person turned down is not made again for the same session.
export function hintKey(sessionId: string, hint: ProjectHint): string {
  if (hint.kind === "start") {
    return `${sessionId}|start`;
  }
  return `${sessionId}|${hint.kind}|${repoKey(hint.repo.url)}`;
}
