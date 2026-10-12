// A run: sending a task, reading the stream of what the agent does, following it again when the
// connection drops, and the messages sent to a run that is already in flight.
import { useCallback, useEffect } from "preact/hooks";
import { t } from "./i18n";
import { choose } from "./choose";
import { parseFrame } from "./appParse";
import { handleStreamEvent, type StreamPayload } from "./streamEvents";
import type { RunState } from "./useRunState";
import type { SessionList } from "./useSessionList";
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
  } = rs;
  const { setRunningState, fetchSessions } = lists;
  const {
    loadCheckpoints,
    loadAgents,
    switchSessionRef,
    transcript,
    followRef,
  } = conv;

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
  const dispatchEvent = useCallback(
    (name: string, data: string | null, id: string | null) => {
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
    async (res: Response) => {
      const reader = res.body!.getReader();
      const decoder = new TextDecoder();
      let buffer = "";
      const dispatchFrame = (raw: string) => {
        const frame = parseFrame(raw);
        if (frame.data !== null) {
          dispatchEvent(frame.event || "message", frame.data, frame.id);
        }
      };
      for (;;) {
        const { done, value } = await reader.read();
        if (done) {
          break;
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
  const followReconnect = useCallback(async () => {
    const retry = () => {
      if (runningRef.current) {
        setState("reconnecting", true);
        reconnectTimer.current = setTimeout(followReconnect, RECONNECT_MS);
      }
    };
    let res: Response;
    try {
      res = await api(
        SESSIONS_PATH +
          sessionRef.current +
          "/events?from=" +
          lastIdRef.current,
      );
    } catch {
      retry();
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
      await readStream(res);
    } catch {
      // Stream broke mid-read.
    }
    retry();
  }, [finish, transcript, readStream]);

  useEffect(() => {
    followRef.current = followReconnect;
  }, [followReconnect]);

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

  // submit sends a task and reads the SSE response.
  const submit = useCallback(
    async (text: string, endpoint?: string) => {
      rs.setMessages((prev) => [
        ...prev,
        { id: rs.nextId(), role: "user", text },
      ]);
      rs.setLiveThought(null);
      // A new run numbers its events from 1 again: a cursor left over from the last one would
      // ask a reconnection for events that do not exist yet.
      lastIdRef.current = 0;
      // Pinned BEFORE the run starts, so the spinner and its clearing are both
      // addressed to the conversation this turn belongs to.
      runSessionRef.current = sessionRef.current;
      setRunningState(true);
      setState("working");
      let res: Response;
      try {
        res = await api(
          endpoint || SESSIONS_PATH + sessionRef.current + "/task",
          {
            method: "POST",
            headers: JSON_HEADERS,
            body: JSON.stringify({ task: text }),
          },
        );
      } catch {
        setState(TURN_REFUSED, true);
        finish();
        return;
      }
      if (!res.ok) {
        await refused(res);
        return;
      }
      try {
        await readStream(res);
      } catch {
        // Stream broke mid-flight — try to reconnect.
        if (runningRef.current) {
          setState("reconnecting", true);
          reconnectTimer.current = setTimeout(followReconnect, RECONNECT_MS);
        }
        return;
      }
      // Stream ended normally. If finish() was already called by the done/error
      // event, runningRef is false and this is a no-op. If the server closed
      // the connection without a terminal event, we must still finish.
      if (runningRef.current) {
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
    submit,
    sendWhileRunning,
    stopRun,
  };
}

export type Stream = ReturnType<typeof useStream>;
