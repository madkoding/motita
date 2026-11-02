// The last pull request overview the page read, shared: the sidebar's badge reads it now and then
// and every project box shows its own pull requests from it, so one read serves them all.
import { useEffect, useState } from "preact/hooks";
import type { PRItem, PRList } from "./pullRequestsApi";

let latest: PRList | null = null;
const listeners = new Set<() => void>();

// publishPullRequests shares a fresh overview; null forgets it (signed out, no gateway).
export function publishPullRequests(list: PRList | null) {
  latest = list;
  listeners.forEach((l) => l());
}

// ciRunning: some pull request's CI is still running, so the page should look again sooner.
export function ciRunning(list: PRList): boolean {
  return list.pull_requests.some((i) => i.ci?.state === "pending");
}

// useProjectPullRequests are the open pull requests of one project, as last read.
export function useProjectPullRequests(projectId: string): PRItem[] {
  const pick = () =>
    (latest?.pull_requests ?? []).filter((i) => i.project_id === projectId);
  const [items, setItems] = useState(pick);
  useEffect(() => {
    const on = () => setItems(pick());
    listeners.add(on);
    on();
    return () => {
      listeners.delete(on);
    };
  }, [projectId]);
  return items;
}
