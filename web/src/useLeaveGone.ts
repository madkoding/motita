// Taking the person off conversations the gateway just removed along with something else.
import { useCallback } from "preact/hooks";
import { mostRecent } from "./appFormat";
import type { SessionList } from "./useSessionList";
import type { Conversation } from "./useConversation";
import type { RunState } from "./useRunState";

interface Deps {
  sessionRef: RunState["sessionRef"];
  lists: SessionList;
  conv: Conversation;
}

// useLeaveGone returns leaveGone: when the conversation on screen is one of `gone` (a project's
// main session or a thread of it), open the most recent one left, or show that none is open.
// Nothing is done when the person was looking at another conversation. A refetch that fails
// leaves the list already on screen, minus what is gone, to decide.
export function useLeaveGone({ sessionRef, lists, conv }: Deps) {
  const { sessions, loadSessions } = lists;
  const { switchSession, showNoSession } = conv;
  return useCallback(
    async (gone: string[]) => {
      if (!gone.includes(sessionRef.current)) {
        return;
      }
      const list = ((await loadSessions()) ?? sessions).filter(
        (s) => !gone.includes(s.id),
      );
      const next = mostRecent(list)?.id;
      if (next) {
        await switchSession(next);
      } else {
        showNoSession();
      }
    },
    [sessions, loadSessions, switchSession, showNoSession],
  );
}
