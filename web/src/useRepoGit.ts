// The git tools of a repository inside a project's folder: the panel that shows what it holds, the
// actions done at once, and the ones handed to the agent, which holds the git logins and writes
// the message of a commit.
import { useCallback, useState } from "preact/hooks";
import { t, tf } from "./i18n";
import { beginGitActivity, endGitActivity } from "./gitActivity";
import { choose } from "./choose";
import { PROJECTS_PATH } from "./appConst";
import type { Api } from "./prApi";
import type { RepoStatus, SetStatus, SetToast } from "./appTypes";

interface Deps {
  api: Api;
  setState: SetStatus;
  setToast: SetToast;
  fetchProjects: () => Promise<unknown>;
  // createSession opens the project's conversation, saying which one it opened (nothing when it
  // could not), and submit sends the agent a request in the one on screen.
  createSession: (projectId: string) => Promise<string | undefined>;
  submit: (text: string) => unknown;
}

// RepoRef names a repository: its project and its folder inside it.
export interface RepoRef {
  projectId: string;
  repo: string;
}

// What an action says when it fails.
const ACTION_FAILED = "the action failed";

// What each action says when it is done.
const DONE: Record<string, string> = {
  pull: "{repo} is up to date",
  main: "{repo} is back on its main branch",
  branch: "Branch created in {repo}",
  stash: "The changes of {repo} are put aside",
  clean: "Merged branches of {repo} deleted",
  discard: "The changes of {repo} are discarded",
};

// What each action is called in the modal that follows it, and what git does for it.
const TITLE: Record<string, string> = {
  pull: "Pulling {repo}",
  main: "Going back to the main branch of {repo}",
  branch: "Creating a branch in {repo}",
  stash: "Putting the changes of {repo} aside",
  clean: "Deleting the merged branches of {repo}",
  discard: "Discarding the changes of {repo}",
};
const DETAIL: Record<string, string> = {
  pull: "git fetch, then a fast-forward of the current branch; never a merge.",
  main: "git checkout of the main branch, only from a clean checkout.",
  branch: "git switch -c: a new branch at the current work.",
  stash: "git stash push --include-untracked, labelled with the date.",
  clean:
    "git branch -d on the branches whose work is already in the main branch.",
  discard: "Restores the tracked files and removes the new ones.",
};

// The requests the agent is sent. They name the folder, so they work wherever the conversation is.
const ASK = {
  commit:
    "In the repository `{repo}` of this project, commit the pending changes of the current branch with a semantic commit message (type(scope): summary) that says what changed. Show me the files and the message first. Do not push.",
  push: "In the repository `{repo}` of this project, push the current branch to origin (git push -u origin <branch>). If it is the main branch, ask me before pushing.",
  pr: "In the repository `{repo}` of this project, open a pull request for the current branch with the pull-requests-and-ci skill: commit what is pending with a semantic message, push, and open it with a semantic title and a Before/After/How description. Do not merge it.",
  work: "We are going to work in the repository `{repo}` of this project. Show me its branch and git status, then wait for what I want done.",
};

export type AskKind = keyof typeof ASK;

export function useRepoGit({
  api,
  setState,
  setToast,
  fetchProjects,
  createSession,
  submit,
}: Deps) {
  const [open, setOpen] = useState<RepoRef | null>(null);
  const [status, setStatus] = useState<RepoStatus | null>(null);

  const urlOf = (r: RepoRef, tail: string) =>
    PROJECTS_PATH +
    r.projectId +
    "/repo" +
    tail +
    "?repo=" +
    encodeURIComponent(r.repo);

  // load reads what the repository holds now into the panel.
  const load = useCallback(
    async (r: RepoRef) => {
      try {
        const res = await api(urlOf(r, ""));
        if (!res.ok) {
          const err = await res.json().catch(() => ({}));
          setState(err.error || "could not read the repository", true);
          return;
        }
        setStatus(await res.json());
      } catch {
        setState("could not read the repository", true);
      }
    },
    [api, setState],
  );

  const openRepoGit = useCallback(
    (r: RepoRef) => {
      setOpen(r);
      setStatus(null);
      void load(r);
    },
    [load],
  );
  const closeRepoGit = useCallback(() => {
    setOpen(null);
    setStatus(null);
  }, []);

  // repoAction does one thing to the repository, then shows what it did. It says whether it worked.
  const repoAction = useCallback(
    async (r: RepoRef, action: string, body: object = {}) => {
      beginGitActivity(tf(TITLE[action], { repo: r.repo }), t(DETAIL[action]));
      try {
        const res = await api(urlOf(r, "/" + action), {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(body),
        });
        if (!res.ok) {
          const err = await res.json().catch(() => ({}));
          setState(err.error || ACTION_FAILED, true);
          endGitActivity(false, err.error || t(ACTION_FAILED), "");
          return false;
        }
        const done = await res.json().catch(() => ({}));
        const gone: string[] = done.deleted ?? [];
        const message = choose(
          action === "clean" && gone.length > 0,
          tf("Deleted: {branches}", { branches: gone.join(", ") }),
          tf(DONE[action], { repo: r.repo }),
        );
        setToast({ message, type: "success" });
        endGitActivity(true, message, String(done.output ?? ""));
        await fetchProjects();
        return true;
      } catch {
        setState(ACTION_FAILED, true);
        endGitActivity(false, t(ACTION_FAILED), "");
        return false;
      }
    },
    [api, setState, setToast, fetchProjects],
  );

  // act is repoAction for the panel: what changed is read again afterwards.
  const act = useCallback(
    async (r: RepoRef, action: string, body: object = {}) => {
      if (await repoAction(r, action, body)) {
        await load(r);
      }
    },
    [repoAction, load],
  );

  // askAgent hands the repository's work to the agent, in the conversation of its project.
  const askAgent = useCallback(
    async (r: RepoRef, kind: AskKind) => {
      if (await createSession(r.projectId)) {
        void submit(tf(ASK[kind], { repo: r.repo }));
      }
    },
    [createSession, submit],
  );

  return { open, status, openRepoGit, closeRepoGit, repoAction, act, askAgent };
}
