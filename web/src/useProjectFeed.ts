// Changes under a project (a branch switched, a file edited, a repository that moved) reach an open
// page as they happen: the gateway sends an event when what a project shows differs, and the page
// reads the projects again. The feed also reads them when it (re)connects and when the tab comes
// back to the front, which is when a page may have missed something.
import { useEffect } from "preact/hooks";
import { PROJECT_FEED_RETRY_MS } from "./appConst";
import { readFrames } from "./useUpdates";
import type { Api } from "./prApi";

export const PROJECT_EVENTS_URL = "/v1/projects/events";

export function useProjectFeed(
  api: Api,
  fetchProjects: () => Promise<unknown>,
  authOk: boolean,
) {
  useEffect(() => {
    if (!authOk) {
      return undefined;
    }
    const ctrl = new AbortController();
    let retry: ReturnType<typeof setTimeout> | undefined;
    const follow = async () => {
      try {
        const res = await api(PROJECT_EVENTS_URL, { signal: ctrl.signal });
        if (res.ok && res.body) {
          void fetchProjects();
          await readFrames(res.body.getReader(), (frame) => {
            if (frame.includes("event: projects")) {
              void fetchProjects();
            }
          });
        }
      } catch {
        /* dropped: listen again below */
      }
      if (!ctrl.signal.aborted) {
        retry = setTimeout(follow, PROJECT_FEED_RETRY_MS);
      }
    };
    void follow();
    const onVisible = () => {
      if (document.visibilityState === "visible") {
        void fetchProjects();
      }
    };
    document.addEventListener("visibilitychange", onVisible);
    return () => {
      ctrl.abort();
      clearTimeout(retry);
      document.removeEventListener("visibilitychange", onVisible);
    };
  }, [authOk, fetchProjects]);
}
