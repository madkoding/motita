// The sidebar's lists: the conversations and the projects, which conversations are running, and
// how the sidebar itself is laid out (open, and which projects are folded).
import { useCallback, useEffect, useRef, useState } from "preact/hooks";
import type { ProjectInfo, SessionInfo } from "./appTypes";
import type { Api } from "./prApi";
import type { RunState } from "./useRunState";
import {
  ANSWERED_HOLD_MS,
  PROJECT_COLLAPSE_KEY,
  SESSIONS_POLL_MS,
  SESSIONS_URL,
  SIDEBAR_KEY,
} from "./appConst";

// readCollapsed is the set of folded projects the browser remembers.
function readCollapsed(): Set<string> {
  try {
    const raw = localStorage.getItem(PROJECT_COLLAPSE_KEY);
    if (raw) {
      return new Set(JSON.parse(raw) as string[]);
    }
  } catch {
    /* ignore */
  }
  return new Set();
}

// withSession returns the list with one session's field changed.
export function withSession(
  list: SessionInfo[],
  id: string,
  patch: Partial<SessionInfo>,
): SessionInfo[] {
  return list.map((s) => {
    if (s.id === id) {
      return { ...s, ...patch };
    }
    return s;
  });
}

export function useSessionList(api: Api, rs: RunState) {
  const { runningRef, setRunning, sessionRef, running } = rs;
  const [sessions, setSessions] = useState<SessionInfo[]>([]);
  const [projects, setProjects] = useState<ProjectInfo[]>([]);
  const [sidebarOpen, setSidebarOpen] = useState<boolean>(() => {
    try {
      return localStorage.getItem(SIDEBAR_KEY) !== "false";
    } catch {
      return true;
    }
  });
  // Collapsed projects: a set of project IDs whose session list is hidden.
  // Persisted in localStorage so a user's choice survives a reload, like the
  // sidebar's own open/closed state.
  const [collapsedProjects, setCollapsedProjects] =
    useState<Set<string>>(readCollapsed);
  const toggleProject = useCallback((pid: string) => {
    setCollapsedProjects((prev) => {
      const next = new Set(prev);
      if (next.has(pid)) {
        next.delete(pid);
      } else {
        next.add(pid);
      }
      try {
        localStorage.setItem(PROJECT_COLLAPSE_KEY, JSON.stringify([...next]));
      } catch {
        /* ignore */
      }
      return next;
    });
  }, []);

  // Persist sidebar preference.
  useEffect(() => {
    try {
      localStorage.setItem(SIDEBAR_KEY, String(sidebarOpen));
    } catch {
      /* ignore */
    }
  }, [sidebarOpen]);

  // answeredRef is the conversation whose answer THIS TAB has already seen but
  // whose run the gateway still reports as in flight.
  //
  // The gateway is telling the TRUTH in that window: the turn's goroutine has
  // not returned yet. It generates the auto-title and saves the session BEFORE
  // its deferred releaseRunSlot, and that ordering is deliberate - the gateway's
  // own end-to-end test depends on the slot still being held at that point. So
  // a list read taken in that window really does say a run is in flight, and
  // the refresh that finish() issues (to pick up the new title) is exactly such
  // a read: it turned the spinner back ON over a conversation that had already
  // answered, for as long as the title took to generate.
  //
  // The row is masked for that session until the gateway's OWN flag goes false,
  // which is why this is not a timer: the release happens at a moment only the
  // gateway knows, and a fixed expiry would either un-mask too early (the
  // spinner flashes back) or too late. A new run cannot start in that
  // conversation before the release - the slot is what refuses it - so masking
  // until then can never hide real work.
  //
  // The mask is applied when RENDERING, never to the stored list: the gateway's
  // answer is kept verbatim so `anyRunning` below still counts it, and the poll
  // therefore keeps running exactly long enough to observe the release and
  // clear this.
  const answeredRef = useRef("");
  // answeredUntil is the backstop for a release that is never observed (a lost
  // fetch, a gateway that went away). Comfortably past the title generator's
  // own 15s ceiling, so it can only fire when something has genuinely gone
  // wrong - and then it un-masks rather than leaving a row dark forever.
  const answeredUntil = useRef(0);
  const answeredHere = (id: string) =>
    answeredRef.current === id && Date.now() < answeredUntil.current;

  const setRunningState = (r: boolean, sessionFor = sessionRef.current) => {
    runningRef.current = r;
    setRunning(r);
    // Fold this tab's own run into the SESSION LIST, so the row that owns it
    // paints its spinner from the same field as every other row.
    //
    // Both directions matter, and the list alone cannot carry either one in
    // time:
    //
    //   * true, at the instant the user sends - the gateway has not answered a
    //     single /v1/sessions yet, and polling would leave the spinner off for
    //     up to a tick while a turn is visibly running. It also clears any
    //     latch, because a new turn supersedes the old answer.
    //   * false, the moment the answer arrives, which LATCHES the session so
    //     the refresh finish() issues cannot light it back up.
    //
    // setSessions is used and not fetchSessions: the local answer is
    // authoritative in both directions, and a request issued here would be the
    // stale one it exists to correct.
    setSessions((prev) => withSession(prev, sessionFor, { running: r }));
    if (r) {
      answeredRef.current = "";
      answeredUntil.current = 0;
    } else {
      answeredRef.current = sessionFor;
      answeredUntil.current = Date.now() + ANSWERED_HOLD_MS;
    }
  };

  // fetchSessions loads the list of conversations from the gateway.
  const fetchSessions = useCallback(async (): Promise<SessionInfo[]> => {
    try {
      const res = await api(SESSIONS_URL);
      const data = await res.json();
      const list: SessionInfo[] = data.sessions || [];
      setSessions(list);
      // The gateway has now been asked, and it has answered. If it says the
      // conversation whose answer we already have is NOT running, the run slot
      // has been released - so the latch that was masking it has done its job
      // and is cleared HERE, on the gateway's own word, rather than on a timer.
      // (A stale read in the window is harmless by construction: it leaves the
      // latch exactly as it was.)
      if (
        answeredRef.current &&
        !list.some((s) => s.id === answeredRef.current && s.running)
      ) {
        answeredRef.current = "";
        answeredUntil.current = 0;
      }
      return list;
    } catch {
      return [];
    }
  }, []);

  // Poll session list while a run is in flight, so the sidebar shows a
  // spinner on whichever conversations the agent is working in.
  //
  // Two rules, and the earlier version got both wrong:
  //
  //  1. It polls while ANY conversation is running, not only the one this tab
  //     started. A run belongs to the SESSION, not to a connection: another
  //     client, a scheduled task, or a session resumed after a gateway restart
  //     can be the one working, and a list this tab never refreshed would show
  //     no sign of it.
  //
  //  2. It does NOT stop the instant this tab's own run ends. The gateway
  //     appends the `done` event and finishes the run BEFORE its deferred
  //     releaseRunSlot runs, so a list read taken right after the answer can
  //     still report `running: true`. That read used to be the last one - the
  //     interval was torn down in the same render, because it was keyed on the
  //     local run alone - and the result was a spinner left turning on a
  //     conversation that had already answered. Keying the interval on "is
  //     anything running" makes that final, stale read harmless: it is no
  //     longer the last one, and the next tick clears the flag the gateway has
  //     by then released.
  const anyRunning = sessions.some((s) => s.running);
  useEffect(() => {
    if (!running && !anyRunning) {
      return undefined;
    }
    const interval = setInterval(() => fetchSessions(), SESSIONS_POLL_MS);
    return () => clearInterval(interval);
  }, [running, anyRunning, fetchSessions]);

  // fetchProjects loads the list of projects from the gateway.
  const fetchProjects = useCallback(async (): Promise<ProjectInfo[]> => {
    try {
      const res = await api("/v1/projects");
      const data = await res.json();
      const list: ProjectInfo[] = data.projects || [];
      setProjects(list);
      return list;
    } catch {
      return [];
    }
  }, []);

  const openSidebar = useCallback(() => setSidebarOpen(true), []);
  const closeSidebar = useCallback(() => setSidebarOpen(false), []);

  return {
    sessions,
    setSessions,
    projects,
    setProjects,
    sidebarOpen,
    openSidebar,
    closeSidebar,
    collapsedProjects,
    toggleProject,
    answeredHere,
    setRunningState,
    fetchSessions,
    fetchProjects,
  };
}

export type SessionList = ReturnType<typeof useSessionList>;
