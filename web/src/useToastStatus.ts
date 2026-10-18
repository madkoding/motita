// The two ways the page reports: the toast at the bottom (a message) and the pill in the header
// (a state).
import { useCallback, useEffect, useState } from "preact/hooks";
import { t } from "./i18n";
import { choose } from "./choose";
import { toastTone } from "./appFormat";
import type { SetStatus, Toast } from "./appTypes";
import {
  NOT_CONNECTED,
  TOAST_ERROR_MS,
  TOAST_MS,
  TOAST_UPDATE_MS,
  TOAST_WARNING_MS,
} from "./appConst";

// isConnectionState reports whether a failure message is really a STATE this
// tab is in - the websocket dropped, or the gateway is not answering at all.
// Those are drawn by the pill, not announced by a toast.
function isConnectionState(text: string) {
  const norm = (text || "").trim().toLowerCase();
  return (
    norm === "reconnecting" || norm === NOT_CONNECTED || norm === "connecting"
  );
}

// pillClass is the pill's class. `bad` is set for both severities, so the TEXT decides
// which of the two it is drawn as: the code sets "warning" for the recoverable
// case and "error" for the rest.
function pillClass(text: string, bad: boolean): string {
  if (bad) {
    return choose(text === "warning", "state-warning bad", "bad");
  }
  return choose(
    text === "working" || text === "reconnecting",
    "state-working",
    "",
  );
}

// ConnectionState is how the tab stands with the gateway, for the banner: "ok", "reconnecting"
// (the stream dropped and the tab is trying again) or "lost" (the gateway is not answering).
export type ConnectionState = "ok" | "reconnecting" | "lost";

export function useToastStatus() {
  const [connection, setConnection] = useState<ConnectionState>("ok");
  const [toast, setToast] = useState<Toast | null>(null);
  const [toastDetailsOpen, setToastDetailsOpen] = useState(false);
  const [stateText, setStateText] = useState("connecting");
  const [stateBad, setStateBad] = useState(false);

  // Auto-dismiss toast. One timer, one place: inline setTimeout calls used to
  // race each other and a stale timer could close a newer toast early.
  // Durations: an error needs reading, so it stays longest; the "new version"
  // toast is clickable and important, so it stays longer than a plain success.
  useEffect(() => {
    setToastDetailsOpen(false);
    if (!toast) {
      return undefined;
    }
    const isUpdate = toast.update === true;
    // An error needs reading, so it stays longest. A warning is between the two:
    // it says something is wrong but recovering, which is worth more than the
    // five seconds a success gets and less than an error.
    const ms = toastTone(
      toast.type,
      TOAST_ERROR_MS,
      TOAST_WARNING_MS,
      choose(isUpdate, TOAST_UPDATE_MS, TOAST_MS),
    );
    const timer = setTimeout(() => setToast(null), ms);
    return () => clearTimeout(timer);
  }, [toast]);

  // setState drives the header pill, which is a STATUS and not a message.
  //
  // It used to be handed a whole failure sentence - `setState('could not load the
  // skill library', true)` - so the header read like a log line while looking
  // exactly as calm as "ready": `.bad` was never defined in the stylesheet
  // (measured: no rule for it in index.css), so the two states were drawn
  // identically.
  //
  // The division now: the TOAST carries the message, and the pill carries the
  // severity and nothing else - "warning" or "error". `bad` stays as the second
  // argument because every existing call already passes it, and it is the only
  // thing that distinguishes a failure from a state.
  const setState: SetStatus = useCallback((text: string, bad = false) => {
    if (bad) {
      // A failure is announced once, in the toast, which is the surface built
      // for a message and already renders the gateway's own error bodies.
      //
      // A connection state is NOT announced: the websocket dropping and coming
      // back is something the user watches in the pill, and it happens while
      // they are waiting - a toast popping up would be an alarm about a
      // condition they can see.
      const isConnection = isConnectionState(text);
      setConnection(
        choose(
          isConnection,
          choose(
            text.trim().toLowerCase() === NOT_CONNECTED,
            "lost",
            "reconnecting",
          ),
          "ok",
        ),
      );
      if (!isConnection) {
        // Translated here, once, for every caller: their messages are the English keys.
        setToast({ message: t(text), type: "error" });
      }
      // "reconnecting" is the recoverable one: the tab is trying again on its
      // own, so the pill warns. Everything else - not connected, a request that
      // failed - is an error.
      const warning =
        isConnection && text.trim().toLowerCase() === "reconnecting";
      setStateText(choose(warning, "warning", "error"));
      setStateBad(true);
      return;
    }
    setConnection("ok");
    setStateText(text);
    setStateBad(false);
  }, []);

  const dismissToast = useCallback(() => setToast(null), []);
  const toggleToastDetails = useCallback(
    () => setToastDetailsOpen((v) => !v),
    [],
  );

  return {
    toast,
    setToast,
    dismissToast,
    toastDetailsOpen,
    toggleToastDetails,
    connection,
    stateText,
    stateClass: pillClass(stateText, stateBad),
    setState,
  };
}
