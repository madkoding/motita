// The offer to give a conversation a project: made when the person talks about a repository (see
// projectHints.ts), done only when they accept it, with the same requests the project dialogs send.
import { useCallback, useMemo, useState } from "preact/hooks";
import { t, tf } from "./i18n";
import { slugOf } from "./appParse";
import { errorMessage } from "./errors";
import { hintKey, projectHint, type ProjectHint } from "./projectHints";
import type { Api } from "./prApi";
import type { SetToast } from "./appTypes";
import type { RunState } from "./useRunState";
import type { SessionList } from "./useSessionList";
import { JSON_HEADERS, PROJECTS_PATH, SESSIONS_PATH } from "./appConst";

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
function indexIn(api: Api, projectId: string) {
  api(PROJECTS_PATH + encodeURIComponent(projectId) + "/code-index", {
    method: "POST",
  }).catch(() => undefined);
}

// settle does what the hint offers and answers the toast to tell it with.
async function settle(
  api: Api,
  sessionId: string,
  hint: ProjectHint,
): Promise<string> {
  const move = (projectId: string) =>
    send(api, SESSIONS_PATH + encodeURIComponent(sessionId) + "/project", {
      project_id: projectId,
    });
  if (hint.kind === "add") {
    await send(
      api,
      PROJECTS_PATH + encodeURIComponent(hint.projectId) + "/repos",
      {
        name: hint.repo.name,
        git_url: hint.repo.url,
      },
    );
    indexIn(api, hint.projectId);
    return tf('{name} added to the project "{title}"', {
      name: hint.repo.name,
      title: hint.projectTitle,
    });
  }
  if (hint.kind === "move") {
    await move(hint.projectId);
    return tf('This session moved to the project "{title}"', {
      title: hint.projectTitle,
    });
  }
  const created = await send(api, "/v1/projects", {
    title: hint.repo.name,
    dir: slugOf(hint.repo.name),
    git_url: hint.repo.url,
  });
  try {
    await move(created.id);
  } catch (e) {
    throw new Error(
      tf(
        'The project "{title}" was created, but the session was not moved: {err}',
        {
          title: hint.repo.name,
          err: errorMessage(e),
        },
      ),
    );
  }
  indexIn(api, created.id);
  return tf('Project "{title}" created and this session moved into it', {
    title: hint.repo.name,
  });
}

// useProjectHint is the offer for the conversation on screen, while it is idle and the person has
// not turned it down, and what accepting or turning it down does.
export function useProjectHint({ api, rs, lists, setToast }: Deps) {
  const { sessionId, running, messages, noSession } = rs;
  const [turnedDown, setTurnedDown] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const hint = useMemo(() => {
    const session = lists.sessions.find((s) => s.id === sessionId);
    const offer = projectHint(messages, session, lists.projects);
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
      setTurnedDown((prev) => [...prev, hintKey(sessionId, hint)]);
    }
  }, [hint, sessionId]);

  const accept = useCallback(async () => {
    if (!hint || busy) {
      return;
    }
    setBusy(true);
    try {
      const message = await settle(api, sessionId, hint);
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
