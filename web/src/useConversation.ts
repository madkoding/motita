// Opening a conversation: its transcript, the steps the gateway kept for each input, its agents and
// its configuration; and following the address bar when the reader goes back and forward.
import { useCallback, useEffect, useRef } from "preact/hooks";
import { choose } from "./choose";
import { sessionFromUrl, sortAgents, writeSessionUrl } from "./appFormat";
import { commandOf } from "./appParse";
import { keyed } from "./useRunState";
import type { RunState } from "./useRunState";
import type { SessionList } from "./useSessionList";
import type { Api } from "./prApi";
import type {
  ConfigView,
  Message,
  SetStatus,
  ShellEntry,
  StepView,
  UrlMode,
} from "./appTypes";
import {
  NO_RESULT,
  NOTHING_YET,
  SESSIONS_PATH,
  SHELL_LOG_MAX,
  STORAGE_KEY,
} from "./appConst";

interface Checkpoint {
  turn: number;
  files: boolean;
  steps: StepView[];
}

// transcriptMessages turns the gateway's transcript into the messages drawn for it.
function transcriptMessages(
  msgs: { User?: string; Agent?: string }[],
  nextId: () => number,
): Message[] {
  const out: Message[] = [];
  for (const [i, m] of msgs.entries()) {
    if (m.User) {
      out.push({ id: nextId(), role: "user", text: m.User, turn: i });
    }
    // A turn still open carries what the run had done before it was cut. The stream only
    // brings what happens FROM NOW ON, so this is the one place the reader can see where
    // it got to - it is drawn whether or not the run is live.
    if (m.Agent) {
      out.push({ id: nextId(), role: "agent", text: m.Agent });
    }
  }
  return out;
}

// placeholder is what a conversation with nothing in it yet shows.
function placeholder(nextId: () => number): Message {
  return { id: nextId(), role: "agent", text: NOTHING_YET, kind: "kind" };
}

// shellEntriesOf rebuilds the terminal history from the commands the checkpoints kept.
function shellEntriesOf(cps: Checkpoint[], nextId: () => number): ShellEntry[] {
  const entries: ShellEntry[] = [];
  for (const cp of cps) {
    const commands = (cp.steps ?? []).filter((st) => st.kind === "command");
    for (const st of commands) {
      entries.push({
        id: nextId(),
        cmd: commandOf(st.text),
        exit: st.exit ?? NO_RESULT,
        out: st.out ?? "",
      });
    }
  }
  return entries.slice(-SHELL_LOG_MAX);
}

// mergeSteps puts the checkpoints' steps in place of the ones on screen, keeping the live turn's.
function mergeSteps(
  prev: Record<number, StepView[]>,
  cps: Checkpoint[],
  live: number | null,
  rebuild: boolean,
) {
  const next: Record<number, StepView[]> = choose(rebuild, {}, { ...prev });
  for (const cp of cps) {
    if (cp.turn !== live) {
      next[cp.turn] = (cp.steps ?? []).map(keyed);
    }
  }
  if (live != null && prev[live]) {
    next[live] = prev[live];
  }
  return next;
}

interface Deps {
  api: Api;
  rs: RunState;
  lists: SessionList;
  setState: SetStatus;
  setConfig: (c: ConfigView) => void;
  requestModal: (open: boolean) => void;
}

export function useConversation({
  api,
  rs,
  lists,
  setState,
  setConfig,
  requestModal,
}: Deps) {
  const {
    sessionRef,
    liveTurnRef,
    nextId,
    setStepsByTurn,
    setCpInfo,
    setShellLog,
    setTermSeenId,
    setMessages,
    setAgents,
  } = rs;
  const followRef = useRef<() => void | Promise<void>>(() => {});
  const switchSessionRef = useRef<
    ((id: string, urlMode?: UrlMode, soft?: boolean) => Promise<void>) | null
  >(null);

  // loadCheckpoints reads the steps the gateway kept for every input of a conversation.
  //
  // `rebuildTerm` also rebuilds the terminal drawer from them: the commands of a conversation
  // that was closed, reloaded or restarted are still there, because the gateway kept them.
  // A refresh at the end of a run leaves the terminal alone - it already has those commands,
  // and rebuilding it would type them all again.
  const loadCheckpoints = useCallback(
    async (id: string, rebuildTerm = false) => {
      let cps: Checkpoint[];
      try {
        const res = await api(
          SESSIONS_PATH + encodeURIComponent(id) + "/checkpoints",
        );
        if (!res.ok) {
          return;
        }
        cps = (await res.json()).checkpoints ?? [];
      } catch {
        return;
      }
      // A late answer must not paint over the conversation now on screen.
      if (sessionRef.current !== id) {
        return;
      }
      const live = liveTurnRef.current;
      setStepsByTurn((prev) => mergeSteps(prev, cps, live, rebuildTerm));
      setCpInfo(
        Object.fromEntries(cps.map((cp) => [cp.turn, { files: cp.files }])),
      );
      if (rebuildTerm) {
        const kept = shellEntriesOf(cps, nextId);
        setShellLog(kept);
        // What was already there is drawn whole, not typed again.
        setTermSeenId(kept[kept.length - 1]?.id ?? 0);
      }
    },
    [],
  );

  // loadAgents asks the gateway for a session's agents. A gateway that predates the endpoint
  // answers 404, read as "no agents".
  const loadAgents = useCallback(async (id: string) => {
    if (!id) {
      return;
    }
    try {
      const res = await api(SESSIONS_PATH + encodeURIComponent(id) + "/agents");
      if (sessionRef.current !== id) {
        return;
      }
      if (!res.ok) {
        setAgents([]);
        return;
      }
      const data = await res.json();
      if (sessionRef.current !== id) {
        return;
      }
      setAgents(sortAgents(data.agents));
    } catch {
      /* no agents to show */
    }
  }, []);

  // runIsLive asks whether a run is in flight in a conversation. The gateway relaunches an
  // interrupted run on its own after a restart, and this tab knows nothing of it: it never started
  // the stream. Asking is what lets the reader see it and follow it.
  const runIsLive = async (id: string) => {
    try {
      const rs2 = await api(SESSIONS_PATH + encodeURIComponent(id) + "/run");
      if (rs2.ok) {
        const st = await rs2.json();
        return !st.outcome;
      }
    } catch {
      /* no run to follow */
    }
    return false;
  };

  // loadConfig reads the session's configuration so the header shows the current provider/model.
  const loadConfig = async (id: string) => {
    try {
      const cfgRes = await api(
        SESSIONS_PATH + encodeURIComponent(id) + "/config",
      );
      const cfgData: ConfigView = await cfgRes.json();
      if (sessionRef.current === id) {
        setConfig(cfgData);
      }
    } catch {
      /* non-fatal */
    }
  };

  // adoptSession makes a conversation the one on screen, in the state and in the address bar.
  const adoptSession = (id: string, urlMode: UrlMode) => {
    rs.setSessionId(id);
    sessionRef.current = id;
    try {
      localStorage.setItem(STORAGE_KEY, id);
    } catch {
      /* ignore */
    }
    if (urlMode !== "none") {
      writeSessionUrl(id, urlMode);
    }
  };

  // showTranscript loads a conversation's transcript and its steps, and follows its run when one
  // is in flight. It says false when the reader moved on to another conversation meanwhile.
  const showTranscript = async (id: string) => {
    const res = await api(SESSIONS_PATH + encodeURIComponent(id) + "/messages");
    const data = await res.json();
    // The user may have clicked another conversation while this one was loading: a
    // late answer must not paint over the one now on screen.
    if (sessionRef.current !== id) {
      return false;
    }
    const msgs = data.messages || [];
    const live = await runIsLive(id);
    if (sessionRef.current !== id) {
      return false;
    }
    const out = transcriptMessages(msgs, nextId);
    if (out.length === 0) {
      out.push(placeholder(nextId));
    }
    setMessages(out);
    await loadCheckpoints(id, true);
    setState("ready");
    if (live && !rs.runningRef.current) {
      rs.runSessionRef.current = id;
      lists.setRunningState(true, id);
      setState("working");
      rs.lastIdRef.current = 0;
      followRef.current();
    }
    return true;
  };

  // switchSession loads the transcript for a given session id and adopts it.
  // `urlMode` says how the address bar follows: 'push' for a click, 'replace' for the
  // initial load, 'none' when the URL already changed (back/forward).
  // `soft` re-reads the conversation already on screen (the handoff to a run this tab did not
  // start): nothing is blanked and no loading modal opens, the new messages just replace the old.
  const switchSession = useCallback(
    async (id: string, urlMode: UrlMode = "push", soft = false) => {
      adoptSession(id, urlMode);
      if (!soft) {
        rs.clearConversation();
      }
      void loadAgents(id);
      rs.lastIdRef.current = 0;
      // The transcript is being fetched: the message area is empty until it arrives, and an
      // empty area is not the same message as "this conversation has nothing in it yet". The
      // spinner covers the fetch AND the deferred rendering that follows, and is cleared below
      // once the messages are on screen and their formulas and diagrams are built.
      if (!soft) {
        requestModal(true);
      }
      // This tab's run belongs to ONE conversation, and it keeps running while
      // the user looks elsewhere. Carry its flag into the row it belongs to, so
      // coming back to that conversation shows the spinner its own turn earned -
      // the gateway does not know about this tab's view of the list, and the
      // next fetchSessions would otherwise paint the row idle for a tick.
      const liveRun = rs.runSessionRef.current;
      if (liveRun) {
        lists.setRunningState(true, liveRun);
      }
      // Load transcript for the new session.
      try {
        if (!(await showTranscript(id))) {
          return;
        }
        await loadConfig(id);
      } catch {
        setState("could not load the conversation", true);
        // Nothing will be rendered, so nothing will announce itself as hydrated either.
        requestModal(false);
      }
    },
    [],
  );

  useEffect(() => {
    switchSessionRef.current = switchSession;
  }, [switchSession]);

  // Back/forward: the URL already names the conversation, so only adopt it.
  useEffect(() => {
    const onPop = () => {
      const id = sessionFromUrl();
      if (id && id !== sessionRef.current) {
        switchSession(id, "none");
      }
    };
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  }, [switchSession]);

  // transcript paints the conversation that already exists.
  const transcript = useCallback(async () => {
    const res = await api(SESSIONS_PATH + sessionRef.current + "/messages");
    const data = await res.json();
    const msgs = data.messages || [];
    if (msgs.length === 0) {
      setMessages([placeholder(nextId)]);
      return;
    }
    setMessages(transcriptMessages(msgs, nextId));
    void loadCheckpoints(sessionRef.current, true);
  }, []);

  return {
    switchSession,
    switchSessionRef,
    followRef,
    loadCheckpoints,
    loadAgents,
    transcript,
  };
}

export type Conversation = ReturnType<typeof useConversation>;
