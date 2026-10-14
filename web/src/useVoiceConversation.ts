// useVoiceConversation is CONVERSATION MODE: when the answer to a spoken turn has been read to its end,
// the microphone listens for the next phrase and sends it, so a conversation goes on without
// touching the page. It ends with an exit phrase ("basta", "that's all"), a tap on the microphone,
// or a silence; a phrase that is only an exit is never sent.
import { useEffect, useRef } from "preact/hooks";
import { t } from "./i18n";
import type { SetToast } from "./appTypes";
import { SPEECH_TURN_READ_EVENT, isExitPhrase } from "./speech";

interface Deps {
  // on is the person's setting, and ready says speech input can be used here.
  on: boolean;
  ready: boolean;
  listenOnce: () => Promise<string | null>;
  cancelListen: () => void;
  // send starts a turn with what was heard.
  send: (text: string) => void;
  setToast: SetToast;
}

export function useVoiceConversation(deps: Deps) {
  // The functions are read when they are called: the page re-renders while it listens, and a
  // listening must not be cut short because a callback was made again.
  const ref = useRef(deps);
  ref.current = deps;
  const { on, ready } = deps;
  useEffect(() => {
    if (!on || !ready) {
      return undefined;
    }
    let live = true;
    const next = async () => {
      const text = await ref.current.listenOnce();
      if (!live || text === null) {
        return;
      }
      if (isExitPhrase(text)) {
        ref.current.setToast({
          message: t("Conversation ended"),
          type: "success",
        });
        return;
      }
      ref.current.send(text);
    };
    const onRead = () => void next();
    window.addEventListener(SPEECH_TURN_READ_EVENT, onRead);
    return () => {
      live = false;
      window.removeEventListener(SPEECH_TURN_READ_EVENT, onRead);
      ref.current.cancelListen();
    };
  }, [on, ready]);
}
