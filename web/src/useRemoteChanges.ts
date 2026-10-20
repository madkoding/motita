// News on origin: the gateway looks at each project's remote every few minutes (a fetch, nothing is
// updated by itself); this keeps an open page current and says, once per change, what to do.
import { useEffect, useRef } from "preact/hooks";
import { tf } from "./i18n";
import { choose } from "./choose";
import { REMOTE_POLL_MS } from "./appConst";
import type { ProjectInfo, SetToast } from "./appTypes";

interface Fresh {
  name: string;
  count: number;
}

// freshChanges are the changes of projects not told yet, and remembers them in seen (project and
// repository -> the newest commit told), so the same state is shown once. A repository that caught
// up is forgotten, and its next news is news again.
export function freshChanges(
  projects: ProjectInfo[],
  seen: Map<string, string>,
): Fresh[] {
  const fresh: Fresh[] = [];
  const now = new Set<string>();
  for (const p of projects) {
    for (const c of p.remote_changes ?? []) {
      const key = p.id + "/" + (c.repo ?? "");
      now.add(key);
      if (seen.get(key) !== c.commit) {
        seen.set(key, c.commit);
        fresh.push({
          name: choose(c.repo, p.title + " / " + c.repo, p.title),
          count: c.behind,
        });
      }
    }
  }
  for (const key of seen.keys()) {
    if (!now.has(key)) {
      seen.delete(key);
    }
  }
  return fresh;
}

// remoteMessage tells what arrived and what to do about it.
export function remoteMessage(fresh: Fresh[]): string {
  if (fresh.length === 1) {
    return tf(
      "{name} has {count} new commit(s) on origin. Choose “Update main branch” in its menu to bring them in, or review them first.",
      { name: fresh[0].name, count: String(fresh[0].count) },
    );
  }
  return tf(
    "{count} repositories have new changes on origin. Look for the “new” tag in the sidebar, then update or review them.",
    { count: String(fresh.length) },
  );
}

// useRemoteChanges re-reads the projects every few minutes while signed in, and toasts the news.
export function useRemoteChanges(
  lists: { projects: ProjectInfo[]; fetchProjects: () => Promise<unknown> },
  setToast: SetToast,
  authOk: boolean,
) {
  const { projects, fetchProjects } = lists;
  const seen = useRef(new Map<string, string>());
  useEffect(() => {
    if (!authOk) {
      return undefined;
    }
    const id = setInterval(() => void fetchProjects(), REMOTE_POLL_MS);
    return () => clearInterval(id);
  }, [authOk, fetchProjects]);
  useEffect(() => {
    const fresh = freshChanges(projects, seen.current);
    if (fresh.length > 0) {
      setToast({ message: remoteMessage(fresh), type: "success" });
    }
  }, [projects]);
}
