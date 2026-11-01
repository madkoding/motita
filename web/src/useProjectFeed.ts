// Changes under a project (a branch switched, a file edited, a repository that moved) reach an open
// page as they happen: the gateway sends an event when what a project shows differs, and the page
// reads the projects again. The feed also reads them when it (re)connects and when the tab comes
// back to the front, which is when a page may have missed something.
//
// The sessions have the same feed: a turn started or ended from another browser or computer, a
// session renamed or created there. Without it a page polled the list only while a run of its own
// was in flight, and a conversation open on two machines drifted apart until it was reloaded.
import { useEffect } from "preact/hooks";
import { PROJECT_FEED_RETRY_MS } from "./appConst";
import { readFrames } from "./useUpdates";
import type { Api } from "./prApi";

export const PROJECT_EVENTS_URL = "/v1/projects/events";
export const SESSION_EVENTS_URL = "/v1/sessions/events";

// useFeed follows one of the gateway's change feeds and calls refresh when it says `event`, when it
// (re)connects and when the tab is shown again.
export function useFeed(
  api: Api,
  url: string,
  event: string,
  refresh: () => Promise<unknown>,
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
        const res = await api(url, { signal: ctrl.signal });
        if (res.ok && res.body) {
          void refresh();
          await readFrames(res.body.getReader(), (frame) => {
            if (frame.includes("event: " + event)) {
              void refresh();
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
        void refresh();
      }
    };
    document.addEventListener("visibilitychange", onVisible);
    return () => {
      ctrl.abort();
      clearTimeout(retry);
      document.removeEventListener("visibilitychange", onVisible);
    };
  }, [authOk, refresh]);
}

export function useProjectFeed(
  api: Api,
  fetchProjects: () => Promise<unknown>,
  authOk: boolean,
) {
  useFeed(api, PROJECT_EVENTS_URL, "projects", fetchProjects, authOk);
}

export function useSessionFeed(
  api: Api,
  fetchSessions: () => Promise<unknown>,
  authOk: boolean,
) {
  useFeed(api, SESSION_EVENTS_URL, "sessions", fetchSessions, authOk);
}
