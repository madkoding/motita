// A project has one main session, and the work done in parallel inside it happens in threads: sessions
// that name the main one as their parent. The person never opens a thread; the main session's agent
// does, and the sidebar only has to show them under it.
import type { SessionInfo } from "./appTypes";

// mainSessionOf is the project's main session: the oldest one that is not a thread of another.
export function mainSessionOf(
  sessions: SessionInfo[],
  projectId: string,
): SessionInfo | undefined {
  return sessions
    .filter((s) => s.project_id === projectId && !s.parent_id)
    .sort((a, b) => a.created.localeCompare(b.created))[0];
}

// groupThreads orders sessions for the sidebar: each main session followed by its threads, oldest
// first. A thread whose main session is not in the list is shown on its own, after the others.
export function groupThreads(sessions: SessionInfo[]): SessionInfo[] {
  const byAge = (a: SessionInfo, b: SessionInfo) =>
    a.created.localeCompare(b.created);
  const ids = new Set(sessions.map((s) => s.id));
  const isMain = (s: SessionInfo) => !s.parent_id || !ids.has(s.parent_id);
  const mains = sessions.filter(isMain);
  const threads = sessions.filter((s) => !isMain(s)).sort(byAge);
  return mains.flatMap((m) => [
    m,
    ...threads.filter((t) => t.parent_id === m.id),
  ]);
}
