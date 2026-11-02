// How many pull requests have a failing CI, for the badge on the sidebar's button: read now and
// then while signed in, so a red CI shows without opening the overview. Each read is shared with
// the project boxes (pullRequestsLive), which show their pull requests and CI from it; while a CI
// runs the page looks again sooner, so the boxes follow it.
import { useEffect, useState } from "preact/hooks";
import { PR_BADGE_POLL_MS, PR_LIST_POLL_MS } from "./appConst";
import { choose } from "./choose";
import { fetchPullRequests } from "./pullRequestsApi";
import { ciRunning, publishPullRequests } from "./pullRequestsLive";
import type { Api } from "./prApi";

// usePullRequestBadge is the number of pull requests whose CI is failing, 0 until it is known.
export function usePullRequestBadge(api: Api | null | undefined): number {
  const [failing, setFailing] = useState(0);
  useEffect(() => {
    if (!api) {
      publishPullRequests(null);
      return undefined;
    }
    let live = true;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const look = async () => {
      let delay = PR_BADGE_POLL_MS;
      try {
        const l = await fetchPullRequests(api);
        if (!live) {
          return;
        }
        setFailing(l.failing ?? 0);
        publishPullRequests(l);
        delay = choose(ciRunning(l), PR_LIST_POLL_MS, PR_BADGE_POLL_MS);
      } catch {
        // A read that fails leaves what was known; the next look tries again.
      }
      if (live) {
        timer = setTimeout(() => void look(), delay);
      }
    };
    void look();
    return () => {
      live = false;
      clearTimeout(timer);
    };
  }, [api]);
  return failing;
}
