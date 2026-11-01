// Changes under a project (a branch switched, a file edited, a repository that moved) reach an open
// page as they happen: the gateway sends an event when what a project shows differs, and the page
// reads the projects again. The feed also reads them when it (re)connects and when the tab comes
// back to the front, which is when a page may have missed something.
//
// The sessions have the same feed: a turn started or ended from another browser or computer, a
// session renamed or created there. Without it a page polled the list only while a run of its own
// was in flight, and a conversation open on two machines drifted apart until it was reloaded.
//
// Both feeds come down one stream (/v1/events), so a tab holds one connection, not one per feed. A
// gateway from before that stream (a remote computer not yet updated) answers 404, and the page
// falls back to the projects' own stream there.
import { useEffect } from "preact/hooks";
import { PROJECT_FEED_RETRY_MS } from "./appConst";
import { readFrames } from "./useUpdates";
import type { Api } from "./prApi";

export const EVENTS_URL = "/v1/events";
export const PROJECT_EVENTS_URL = "/v1/projects/events";
const HTTP_NOT_FOUND = 404;

// nextUrl is where to listen after an answer that was not a stream: a gateway without the
// combined one (404) is followed on the projects' own.
function nextUrl(status: number, url: string): string {
  if (status === HTTP_NOT_FOUND) {
    return PROJECT_EVENTS_URL;
  }
  return url;
}

type Refresh = () => Promise<unknown>;

// useChangeFeed follows the gateway's change stream: "projects" reads the projects again,
// "sessions" the sessions, and both are read when it (re)connects and when the tab is shown again.
export function useChangeFeed(
  api: Api,
  fetchProjects: Refresh,
  fetchSessions: Refresh,
  authOk: boolean,
) {
  useEffect(() => {
    if (!authOk) {
      return undefined;
    }
    const ctrl = new AbortController();
    let retry: ReturnType<typeof setTimeout> | undefined;
    let url = EVENTS_URL;
    const both = () => {
      void fetchProjects();
      void fetchSessions();
    };
    const onFrame = (frame: string) => {
      if (frame.includes("event: projects")) {
        void fetchProjects();
      }
      if (frame.includes("event: sessions")) {
        void fetchSessions();
      }
    };
    const follow = async () => {
      try {
        const res = await api(url, { signal: ctrl.signal });
        if (res.ok && res.body) {
          both();
          await readFrames(res.body.getReader(), onFrame);
        } else {
          url = nextUrl(res.status, url);
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
        both();
      }
    };
    document.addEventListener("visibilitychange", onVisible);
    return () => {
      ctrl.abort();
      clearTimeout(retry);
      document.removeEventListener("visibilitychange", onVisible);
    };
  }, [authOk, fetchProjects, fetchSessions]);
}
