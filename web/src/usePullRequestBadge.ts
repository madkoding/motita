// How many pull requests have a failing CI, for the badge on the sidebar's button: read now and
// then while signed in, so a red CI shows without opening the overview.
import { useEffect, useState } from "preact/hooks";
import { PR_BADGE_POLL_MS } from "./appConst";
import { fetchPullRequests } from "./pullRequestsApi";
import type { Api } from "./prApi";

// usePullRequestBadge is the number of pull requests whose CI is failing, 0 until it is known.
export function usePullRequestBadge(api: Api | null | undefined): number {
  const [failing, setFailing] = useState(0);
  useEffect(() => {
    if (!api) {
      return undefined;
    }
    let live = true;
    const look = () =>
      fetchPullRequests(api)
        .then((l) => {
          if (live) {
            setFailing(l.failing ?? 0);
          }
        })
        .catch(() => undefined);
    void look();
    const id = setInterval(() => void look(), PR_BADGE_POLL_MS);
    return () => {
      live = false;
      clearInterval(id);
    };
  }, [api]);
  return failing;
}
