// The offer to give a conversation a project: made when the person talks about a repository (see
// projectHints.ts), done only when they accept it, with the same requests the project dialogs send.
import { useCallback, useMemo, useState } from "preact/hooks";
import { t, tf } from "./i18n";
import { slugOf } from "./appParse";
import { errorMessage } from "./errors";
import {
  hintKey,
  projectHint,
  type ProjectHint,
  type RepoMention,
} from "./projectHints";
import type { Api } from "./prApi";
import type { SetToast } from "./appTypes";
import type { RunState } from "./useRunState";
import type { SessionList } from "./useSessionList";
import {
  HTTP_CONFLICT,
  JSON_HEADERS,
  PROJECTS_PATH,
  SESSIONS_PATH,
} from "./appConst";

const TURNED_DOWN_KEY = "motita.ui.projectHintsTurnedDown";
const TURNED_DOWN_MAX = 300;
// How much of the session id names the folder of a project whose title makes none.
const FOLDER_ID_CHARS = 6;

interface Deps {
  api: Api;
  rs: RunState;
  lists: SessionList;
  setToast: SetToast;
}

// refusal is the gateway's reason for a refused request.
async function refusal(res: Response): Promise<string> {
  const err = await res.json().catch(() => ({}));
  return err.error || "HTTP " + res.status;
}

// send posts a JSON body and answers the project or session it returns, or throws the reason.
async function send(api: Api, path: string, body: object, method = "POST") {
  const res = await api(path, {
    method,
    headers: JSON_HEADERS,
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    throw new Error(await refusal(res));
  }
  return res.json();
}

// indexIn brings the project's code index up to date. It is a courtesy that can take a while, so
// nothing waits for it, and an index that is off or fails changes nothing.
function indexIn(api: Api, projectId: string, setToast: SetToast) {
  api(PROJECTS_PATH + encodeURIComponent(projectId) + "/code-index", {
    method: "POST",
  })
    .then((res) => {
      if (res.status === HTTP_CONFLICT) {
        setToast({
          message: t("The project is not indexed: the code index is off"),
          type: "warning",
          detail: t("Turn it on in Settings → Indexing."),
        });
      }
    })
    .catch(() => undefined);
}

// newProjectOf is the title a new project gets and the repository it is cloned from, if any.
function newProjectOf(
  hint:
    | { kind: "create"; repo: RepoMention }
    | { kind: "start"; title: string },
) {
  if (hint.kind === "start") {
    return { title: hint.title, gitUrl: undefined };
  }
  return { title: hint.repo.name, gitUrl: hint.repo.url };
}

// settle does what the hint offers and answers the toast to tell it with.
async function settle(
  api: Api,
  sessionId: string,
  hint: ProjectHint,
  setToast: SetToast,
): Promise<string> {
  const move = (projectId: string) =>
    send(api, SESSIONS_PATH + encodeURIComponent(sessionId) + "/project", {
      project_id: projectId,
    });
  const { title, gitUrl } = newProjectOf(hint);
  const created = await send(api, "/v1/projects", {
    title,
    dir: slugOf(title) || "project-" + sessionId.slice(0, FOLDER_ID_CHARS),
    git_url: gitUrl,
  });
  try {
    await move(created.id);
  } catch (e) {
    throw new Error(
      tf(
        'The project "{title}" was created, but the session was not moved: {err}',
        {
          title,
          err: errorMessage(e),
        },
      ),
    );
  }
  indexIn(api, created.id, setToast);
  return tf('Project "{title}" created and this session moved into it', {
    title,
  });
}

// readTurnedDown is the offers the person turned down, as this browser remembers them.
function readTurnedDown(): string[] {
  try {
    return JSON.parse(localStorage.getItem(TURNED_DOWN_KEY) ?? "[]");
  } catch {
    return [];
  }
}

// keepTurnedDown remembers them, the latest few hundred.
function keepTurnedDown(keys: string[]) {
  try {
    localStorage.setItem(
      TURNED_DOWN_KEY,
      JSON.stringify(keys.slice(-TURNED_DOWN_MAX)),
    );
  } catch {
    /* ignore */
  }
}

// useProjectHint is the offer for the conversation on screen, while it is idle and the person has
// not turned it down, and what accepting or turning it down does.
export function useProjectHint({ api, rs, lists, setToast }: Deps) {
  const { sessionId, running, messages, noSession } = rs;
  const [turnedDown, setTurnedDown] = useState<string[]>(readTurnedDown);
  const [busy, setBusy] = useState(false);
  const hint = useMemo(() => {
    const session = lists.sessions.find((s) => s.id === sessionId);
    const offer = projectHint(messages, session);
    if (
      !offer ||
      running ||
      noSession ||
      turnedDown.includes(hintKey(sessionId, offer))
    ) {
      return null;
    }
    return offer;
  }, [
    messages,
    sessionId,
    lists.sessions,
    lists.projects,
    running,
    noSession,
    turnedDown,
  ]);

  const dismiss = useCallback(() => {
    if (hint) {
      setTurnedDown((prev) => {
        const next = [...prev, hintKey(sessionId, hint)];
        keepTurnedDown(next);
        return next;
      });
    }
  }, [hint, sessionId]);

  const accept = useCallback(async () => {
    if (!hint || busy) {
      return;
    }
    setBusy(true);
    try {
      const message = await settle(api, sessionId, hint, setToast);
      setToast({ message, type: "success" });
    } catch (e) {
      setToast({
        message: t("Could not set up the project"),
        type: "error",
        detail: errorMessage(e),
      });
    } finally {
      setBusy(false);
      await Promise.all([lists.fetchProjects(), lists.fetchSessions()]);
    }
  }, [
    hint,
    busy,
    api,
    sessionId,
    setToast,
    lists.fetchProjects,
    lists.fetchSessions,
  ]);

  return { hint, busy, accept, dismiss };
}

export type ProjectHintState = ReturnType<typeof useProjectHint>;
