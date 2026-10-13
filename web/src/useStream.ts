// A run: sending a task, reading the stream of what the agent does, following it again when the
// connection drops, and the messages sent to a run that is already in flight.
import { useCallback, useEffect } from "preact/hooks";
import { t } from "./i18n";
import { choose } from "./choose";
import { parseFrame } from "./appParse";
import { handleStreamEvent, type StreamPayload } from "./streamEvents";
import { cutLink, isLive, openLink, type RunLink } from "./runLink";
import type { RunState } from "./useRunState";
import { withSession, type SessionList } from "./useSessionList";
import type { Conversation } from "./useConversation";
import type { Api } from "./prApi";
import type { SetStatus, SetToast } from "./appTypes";
import {
  HANDOFF_RELOAD_MS,
  HTTP_NOT_FOUND,
  JSON_HEADERS,
  NO_RESULT,
  RECONNECT_MS,
  SESSIONS_PATH,
  SSE_FRAME_END,
  TURN_REFUSED,
} from "./appConst";

interface Deps {
  api: Api;
  rs: RunState;
  lists: SessionList;
  conv: Conversation;
  setState: SetStatus;
  setToast: SetToast;
}

// refusalOf reads why the gateway refused a turn, and whether it was because the session was
// already integrated.
async function refusalOf(
  res: Response,
): Promise<{ msg: string; merged: boolean }> {
  let msg = TURN_REFUSED;
  let merged = false;
  try {
    const err = await res.json();
    if (err.error) {
      msg = err.error;
    }
    merged = err.error?.includes("already been integrated");
  } catch {
    /* keep generic */
  }
  return { msg, merged };
}

export function useStream({ api, rs, lists, conv, setState, setToast }: Deps) {
  const {
    sessionRef,
    runSessionRef,
    runningRef,
    lastIdRef,
    reconnectTimer,
    handoffRef,
    liveTurnRef,
    linkRef,
  } = rs;
  const { setRunningState, fetchSessions, setSessions } = lists;
  const {
    loadCheckpoints,
    loadAgents,
    switchSessionRef,
    transcript,
    followRef,
    detachRef,
  } = conv;

  // live says whether a stream may still draw: see runLink.ts.
  const live = (link: RunLink) => isLive(link, linkRef, sessionRef.current);

  // finish marks the run as done.
  const finish = useCallback(() => {
    // Cleared against the session the run was STARTED in, not against whatever
    // is on screen now: a user who switched away mid-turn must not have the
    // finished run's flag land on the conversation they switched to.
    setRunningState(false, runSessionRef.current || sessionRef.current);
    runSessionRef.current = "";
    rs.setActivity(null);
    // The turn is over: its steps stay where they are, and the gateway is asked for its side
    // (whether the files were checkpointed) now that the run has ended.
    liveTurnRef.current = null;
    void loadCheckpoints(sessionRef.current);
    // A command that never reported a result (refused, declined, or cut off by a cancel)
    // must not read "running" for ever: it is closed as "no result".
    rs.setShellLog((prev) => {
      if (!prev.some((e) => e.exit == null)) {
        return prev;
      }
      return prev.map((e) => {
        if (e.exit == null) {
          return { ...e, exit: NO_RESULT };
        }
        return e;
      });
    });
    rs.setLiveThought(null);
    setState("ready");
    // The agents' final states and costs, as the gateway has them now that the run is over.
    void loadAgents(sessionRef.current);
    // After a run finishes, refresh the session list so the auto-title shows up.
    fetchSessions();
    // A message sent mid-run starts the next run in the gateway, not through this tab: reload
    // the conversation and follow it. Asked twice because the gateway starts it a beat later.
    if (handoffRef.current) {
      handoffRef.current = false;
      const sid = sessionRef.current;
      const reload = () => {
        if (sessionRef.current === sid && !runningRef.current) {
          void switchSessionRef.current?.(sid, "none", true);
        }
      };
      for (const delay of HANDOFF_RELOAD_MS) {
        setTimeout(reload, delay);
      }
    }
  }, [fetchSessions, loadCheckpoints]);

  // dispatchEvent handles one parsed SSE event.
  // An event of a stream that is no longer the one on screen is dropped: it belongs to another
  // conversation, and drawing it here is how two sessions ended up mixed in one view.
  const dispatchEvent = useCallback(
    (link: RunLink, name: string, data: string | null, id: string | null) => {
      if (!live(link)) {
        return;
      }
      if (id) {
        lastIdRef.current = Number.parseInt(id, 10) || lastIdRef.current;
      }
      if (data === null) {
        return;
      }
      let payload: StreamPayload;
      try {
        payload = JSON.parse(data);
      } catch {
        rs.setMessages((prev) => [
          ...prev,
          { id: rs.nextId(), role: "agent", text: data },
        ]);
        return;
      }
      handleStreamEvent({ rs, finish }, name, payload);
    },
    [finish],
  );

  // readStream consumes an SSE response body and dispatches each frame.
  const readStream = useCallback(
    async (res: Response, link: RunLink) => {
      const reader = res.body!.getReader();
      const decoder = new TextDecoder();
      let buffer = "";
      const dispatchFrame = (raw: string) => {
        const frame = parseFrame(raw);
        if (frame.data !== null) {
          dispatchEvent(link, frame.event || "message", frame.data, frame.id);
        }
      };
      for (;;) {
        const { done, value } = await reader.read();
        if (done) {
          break;
        }
        if (!live(link)) {
          void reader.cancel().catch(() => {});
          return;
        }
        buffer += decoder.decode(value, { stream: true });
        let idx: number;
        while ((idx = buffer.indexOf(SSE_FRAME_END)) !== -1) {
          const raw = buffer.slice(0, idx);
          buffer = buffer.slice(idx + SSE_FRAME_END.length);
          dispatchFrame(raw);
        }
      }
      if (buffer.trim()) {
        dispatchFrame(buffer);
      }
    },
    [dispatchEvent],
  );

  // followReconnect reattaches to a dropped stream.
  //
  // It follows the session ON SCREEN, so a timer left over from a session the reader has left must
  // not fire into the one they are on now: detach() clears it, and a call with no run in flight is
  // a no-op.
  const followReconnect = useCallback(async () => {
    if (!runningRef.current) {
      return;
    }
    const link = openLink(linkRef, sessionRef.current);
    const retry = () => {
      if (live(link) && runningRef.current) {
        setState("reconnecting", true);
        reconnectTimer.current = setTimeout(followReconnect, RECONNECT_MS);
      }
    };
    let res: Response;
    try {
      res = await api(
        SESSIONS_PATH + link.sid + "/events?from=" + lastIdRef.current,
        { signal: link.ctrl.signal },
      );
    } catch {
      retry();
      return;
    }
    if (!live(link)) {
      return;
    }
    if (res.status === HTTP_NOT_FOUND) {
      if (runningRef.current) {
        finish();
        await transcript();
      }
      return;
    }
    if (!res.ok) {
      retry();
      return;
    }
    try {
      await readStream(res, link);
    } catch {
      // Stream broke mid-read.
    }
    retry();
  }, [finish, transcript, readStream]);

  useEffect(() => {
    followRef.current = followReconnect;
  }, [followReconnect]);

  // detach lets go of the run this tab is following, because the reader is leaving its session.
  //
  // The run is NOT stopped: it belongs to the gateway, and it keeps working while the reader looks
  // at another conversation. What is dropped is this tab's view of it - the stream, the reconnect
  // timer and the `running` flag - so the conversation the reader opens next starts idle and its
  // composer can send. The row of the session that is still working keeps its spinner until the
  // gateway's own list says the run is over, and opening that session again follows it from the
  // start (showTranscript asks the gateway whether a run is in flight).
  const detach = useCallback(() => {
    const linked = cutLink(linkRef);
    if (reconnectTimer.current) {
      clearTimeout(reconnectTimer.current);
      reconnectTimer.current = null;
    }
    if (!runningRef.current) {
      return;
    }
    const runSid = runSessionRef.current || linked;
    runningRef.current = false;
    rs.setRunning(false);
    runSessionRef.current = "";
    handoffRef.current = false;
    liveTurnRef.current = null;
    rs.setActivity(null);
    rs.setLiveThought(null);
    if (runSid) {
      setSessions((prev) => withSession(prev, runSid, { running: true }));
    }
  }, []);

  useEffect(() => {
    detachRef.current = detach;
  }, [detach]);

  // refused tells the user why the gateway would not take the turn.
  const refused = async (res: Response) => {
    const { msg, merged } = await refusalOf(res);
    // Read from the list this run was started with, as it always was.
    const s = lists.sessions.find((x) => x.id === sessionRef.current);
    if (merged && s?.continuable) {
      setToast({
        message: t("This session is already integrated"),
        type: "warning",
        detail: t(
          "Click Continue in the session menu to keep working from the latest project branch.",
        ),
      });
    } else {
      setToast({ message: msg, type: "error" });
    }
    setState(msg, true);
    finish();
  };

  // submit sends a message in auto mode and reads the SSE response: the gateway picks plan or task
  // from the wording (or a leading /plan or /task) and says which in the first progress line.
  // `shown`, when given, is what the conversation draws for the message instead of its text.
  const submit = useCallback(
    async (text: string, endpoint?: string, shown?: string) => {
      const sid = sessionRef.current;
      const link = openLink(linkRef, sid);
      rs.setMessages((prev) => [
        ...prev,
        { id: rs.nextId(), role: "user", text, shown },
      ]);
      rs.setLiveThought(null);
      // A new run numbers its events from 1 again: a cursor left over from the last one would
      // ask a reconnection for events that do not exist yet.
      lastIdRef.current = 0;
      // Pinned BEFORE the run starts, so the spinner and its clearing are both
      // addressed to the conversation this turn belongs to.
      runSessionRef.current = sid;
      setRunningState(true);
      setState("working");
      let res: Response;
      try {
        res = await api(endpoint || SESSIONS_PATH + sid + "/run", {
          method: "POST",
          headers: JSON_HEADERS,
          body: JSON.stringify({ task: text }),
          signal: link.ctrl.signal,
        });
      } catch {
        // Cut because the reader left the session: the run goes on in the gateway, nothing failed.
        if (!live(link)) {
          return;
        }
        setState(TURN_REFUSED, true);
        finish();
        return;
      }
      if (!live(link)) {
        return;
      }
      if (!res.ok) {
        await refused(res);
        return;
      }
      try {
        await readStream(res, link);
      } catch {
        // Stream broke mid-flight — try to reconnect.
        if (live(link) && runningRef.current) {
          setState("reconnecting", true);
          reconnectTimer.current = setTimeout(followReconnect, RECONNECT_MS);
        }
        return;
      }
      // Stream ended normally. If finish() was already called by the done/error
      // event, runningRef is false and this is a no-op. If the server closed
      // the connection without a terminal event, we must still finish.
      if (live(link) && runningRef.current) {
        finish();
      }
    },
    [finish, readStream, followReconnect],
  );

  // sendWhileRunning sends a message to the session on screen while a run is in flight.
  // `interrupt` stops the run first and puts the message at the front, so the agent reads the
  // new context right away; without it the message waits for the end of the turn.
  const sendWhileRunning = (text: string, interrupt: boolean) => {
    const sid = sessionRef.current;
    handoffRef.current = true;
    const failed = () => {
      handoffRef.current = false;
      setToast({ message: t(TURN_REFUSED), type: "error" });
    };
    void api(
      SESSIONS_PATH +
        encodeURIComponent(sid) +
        choose(interrupt, "/interrupt", "/queue"),
      {
        method: "POST",
        headers: JSON_HEADERS,
        body: JSON.stringify({ task: text }),
      },
    )
      .then((res) => {
        if (!res.ok) {
          failed();
        }
      })
      .catch(failed);
  };

  // stopRun stops the current turn of the session on screen, with nothing queued behind it.
  const stopRun = () => {
    void api(
      SESSIONS_PATH + encodeURIComponent(sessionRef.current) + "/interrupt",
      {
        method: "POST",
        headers: JSON_HEADERS,
        body: "{}",
      },
    ).catch(() => {});
  };

  return {
    finish,
    readStream,
    followReconnect,
    detach,
    submit,
    sendWhileRunning,
    stopRun,
  };
}

export type Stream = ReturnType<typeof useStream>;
