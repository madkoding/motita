// What the tab says about sessions it is not showing, and about a model switch in the one it is.
//
// The tab draws one session at a time while others can be working in the gateway. A run left
// behind in another session used to finish in silence: the only sign was its spinner stopping in
// the sidebar. finishedElsewhere spots that moment in the list the sidebar already polls, and the
// tab says so in a toast.
import { useEffect, useRef } from "preact/hooks";
import { t, tf } from "./i18n";
import { sessionTitle } from "./appFormat";
import type { ConfigView, SessionInfo, SetToast, Toast } from "./appTypes";

// finishedElsewhere returns the sessions that were running in `before` and are not any more,
// leaving out the one on screen (its own answer is already in front of the reader) and any that
// left the list (deleted, not finished). `before` maps a session id to its last running flag.
export function finishedElsewhere(
  before: Map<string, boolean>,
  list: SessionInfo[],
  onScreen: string,
): SessionInfo[] {
  return list.filter(
    (s) => s.id !== onScreen && before.get(s.id) === true && !s.running,
  );
}

// finishedToast is the toast for the sessions that just finished.
export function finishedToast(done: SessionInfo[]): Toast | null {
  if (done.length === 0) {
    return null;
  }
  if (done.length === 1) {
    return {
      message: tf("“{title}” finished", { title: sessionTitle(done[0]) }),
      type: "success",
    };
  }
  return {
    message: tf("{n} sessions finished", { n: done.length }),
    type: "success",
    detail: done.map(sessionTitle).join("\n"),
  };
}

// useFinishedElsewhere watches the session list and announces a session that finished while
// another one was on screen.
export function useFinishedElsewhere(
  sessions: SessionInfo[],
  onScreen: string,
  setToast: SetToast,
) {
  const before = useRef(new Map<string, boolean>());
  useEffect(() => {
    const toast = finishedToast(
      finishedElsewhere(before.current, sessions, onScreen),
    );
    before.current = new Map(sessions.map((s) => [s.id, s.running]));
    if (toast) {
      setToast(toast);
    }
  }, [sessions]);
}

// modelChangeToast is the warning a model switch earns in this session, or null when it earns
// none. The model belongs to the session, so switching it is allowed; what deserves a word is
// switching it in a conversation that already talked, or while a turn is running.
export function modelChangeToast(v: ConfigView): Toast | null {
  const w = v.warnings || [];
  if (w.length === 0) {
    return null;
  }
  const lines: string[] = [];
  if (w.includes("history")) {
    lines.push(
      tf(
        "The conversation so far was written with {model}; {next} takes it over from the next turn and may answer differently. Open a new session to keep each model on its own.",
        { model: v.previous_model || "?", next: v.model },
      ),
    );
  }
  if (w.includes("running")) {
    lines.push(t("The turn in progress finishes with the previous model."));
  }
  return {
    message: tf("Model switched to {model} in this session", {
      model: v.model,
    }),
    type: "warning",
    detail: lines.join("\n"),
  };
}

// switchWarning is what the model picker says BEFORE the switch is applied, from what the tab
// knows: whether the conversation has messages and whether a turn is running.
export function switchWarning(
  changed: boolean,
  hasMessages: boolean,
  running: boolean,
): string {
  if (!changed) {
    return "";
  }
  if (running) {
    return t(
      "A turn is running: it finishes with the current model, and the new one answers from the next turn.",
    );
  }
  if (hasMessages) {
    return t(
      "This conversation already has messages: the new model takes them over and may answer differently. A new session keeps each model on its own.",
    );
  }
  return "";
}
