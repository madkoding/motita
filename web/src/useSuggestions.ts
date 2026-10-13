// Answering the suggestions of a report: the answers are kept with the turn in the gateway, so
// the card draws the same pressed buttons when the conversation is opened again, and the accepted
// ones are sent as the next turn under a short label instead of the prompt they carry.
import { useCallback } from "preact/hooks";
import { t } from "./i18n";
import type { Api } from "./prApi";
import type { RunState } from "./useRunState";
import type { Stream } from "./useStream";
import type { Message } from "./appTypes";
import type { SuggestionAnswers } from "./proposals";
import { JSON_HEADERS, SESSIONS_PATH } from "./appConst";

// answersPath is where the gateway keeps the answers of one turn's suggestions.
export function answersPath(session: string, turn: number): string {
  return (
    SESSIONS_PATH +
    encodeURIComponent(session) +
    "/checkpoints/" +
    turn +
    "/answers"
  );
}

export function useSuggestions(api: Api, rs: RunState, stream: Stream) {
  const answerSuggestions = useCallback(
    async (m: Message, answers: SuggestionAnswers) => {
      rs.setMessages((prev) =>
        prev.map((x) => (x.id === m.id ? { ...x, answers } : x)),
      );
      // Kept before the turn starts. A gateway that cannot keep them costs only the marks after
      // a reload, so the turn is sent anyway.
      if (m.turn != null) {
        try {
          await api(answersPath(rs.sessionRef.current, m.turn), {
            method: "PUT",
            headers: JSON_HEADERS,
            body: JSON.stringify(answers),
          });
        } catch {
          /* the marks stay on screen; only a reload loses them */
        }
      }
      if (answers.request) {
        await stream.submit(
          answers.request,
          undefined,
          t("Applying suggestions"),
        );
      }
    },
    [api, stream.submit],
  );
  return { answerSuggestions };
}
