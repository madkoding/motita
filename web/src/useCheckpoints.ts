// The ⋯ menus beside each input of the conversation: going back to the checkpoint it made, and
// (on the input being worked on) stopping the agent.
import { useCallback, useEffect, useState } from "preact/hooks";
import { t, tf } from "./i18n";
import { choose } from "./choose";
import type { RunState } from "./useRunState";
import type { Conversation } from "./useConversation";
import type { Api } from "./prApi";
import type { SetToast } from "./appTypes";
import { JSON_HEADERS, SESSIONS_PATH } from "./appConst";

// RUN_MENU is the menu of the input being worked on, which has no checkpoint yet.
export const RUN_MENU = -1;

interface Deps {
  api: Api;
  rs: RunState;
  conv: Conversation;
  setInput: (text: string) => void;
  setToast: SetToast;
}

export function useCheckpoints({ api, rs, conv, setInput, setToast }: Deps) {
  const { sessionRef, runningRef } = rs;
  // The checkpoint whose ⋯ menu is open, and whether the destructive item was armed.
  const [cpMenu, setCpMenu] = useState<number | null>(null);
  const [cpArmed, setCpArmed] = useState(false);
  useEffect(() => {
    if (cpMenu == null) {
      return undefined;
    }
    const close = () => {
      setCpMenu(null);
      setCpArmed(false);
    };
    // Deferred one tick: the click that opened the menu must not be the one that closes it.
    const timer = setTimeout(
      () => document.addEventListener("click", close),
      0,
    );
    return () => {
      clearTimeout(timer);
      document.removeEventListener("click", close);
    };
  }, [cpMenu]);

  // toggleCpMenu opens the menu of one input, or closes it when it is the one already open.
  const toggleCpMenu = useCallback((turn: number) => {
    setCpArmed(false);
    setCpMenu((m) => {
      if (m === turn) {
        return null;
      }
      return turn;
    });
  }, []);

  // restoreCheckpoint takes the conversation back to before an input; with `files` it also puts
  // the files back. The input's text returns to the composer, so it can be edited and sent again.
  const restoreCheckpoint = useCallback(
    async (turn: number, files: boolean) => {
      setCpMenu(null);
      setCpArmed(false);
      if (runningRef.current) {
        return;
      }
      const sid = sessionRef.current;
      try {
        const res = await api(
          SESSIONS_PATH +
            encodeURIComponent(sid) +
            "/checkpoints/" +
            turn +
            "/restore",
          {
            method: "POST",
            headers: JSON_HEADERS,
            body: JSON.stringify({ files }),
          },
        );
        const body = await res.json().catch(() => ({}));
        if (!res.ok) {
          setToast({
            message: t("Could not go back to that checkpoint"),
            type: "error",
            detail: body.error,
          });
          return;
        }
        if (sessionRef.current !== sid) {
          return;
        }
        setInput(body.task ?? "");
        await conv.switchSessionRef.current?.(sid, "none");
        setToast({
          message: choose(
            body.files_restored,
            t("Back at the checkpoint — conversation and files"),
            t("Back at the checkpoint — conversation only"),
          ),
          type: "success",
          detail: choose(
            body.files_restored && body.undo_kept,
            tf("What the files were before is kept in git as {ref}", {
              ref: "refs/motita/undo/" + sid,
            }),
            undefined,
          ),
        });
      } catch {
        setToast({
          message: t("Could not go back to that checkpoint"),
          type: "error",
        });
      }
    },
    [],
  );

  return {
    cpMenu,
    setCpMenu,
    cpArmed,
    setCpArmed,
    toggleCpMenu,
    restoreCheckpoint,
  };
}
