// Answering the suggestions of a report: the answers are kept with the turn in the gateway, so
// the card draws the same pressed buttons when the conversation is opened again, and the accepted
// ones are sent as the next turn under a short label instead of the prompt they carry.
import { useCallback } from "preact/hooks";
import { t } from "./i18n";
import { choose } from "./choose";
import type { Api } from "./prApi";
import type { RunState } from "./useRunState";
import type { Stream } from "./useStream";
import type { Message } from "./appTypes";
import type { SuggestionAnswers } from "./proposals";
import type { QuestionAnswers } from "./questions";
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

// questionAnswersPath is where the gateway keeps the answers to one turn's questions.
export function questionAnswersPath(session: string, turn: number): string {
  return (
    SESSIONS_PATH +
    encodeURIComponent(session) +
    "/checkpoints/" +
    turn +
    "/question-answers"
  );
}

// withAnswers is message x with the answers kept on it when it is m, and as it was otherwise.
function withAnswers(
  x: Message,
  m: Message,
  answers: SuggestionAnswers,
): Message {
  if (x.id === m.id) {
    return { ...x, answers };
  }
  return x;
}

// withQuestionAnswers is message x with the answers to its questions kept on it when it is m.
function withQuestionAnswers(
  x: Message,
  m: Message,
  answers: QuestionAnswers,
): Message {
  if (x.id === m.id) {
    return { ...x, questionAnswers: answers };
  }
  return x;
}

export function useSuggestions(api: Api, rs: RunState, stream: Stream) {
  const answerSuggestions = useCallback(
    async (m: Message, answers: SuggestionAnswers) => {
      rs.setMessages((prev) =>
        prev.map((x) => withAnswers(x, m, answers)),
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
      // Sent as a task, not through auto mode: applying accepted changes is work by definition,
      // and the wording of the request must not route it to the read-only planner.
      if (answers.request) {
        await stream.submit(
          answers.request,
          SESSIONS_PATH +
            encodeURIComponent(rs.sessionRef.current) +
            "/task",
          t("Applying suggestions"),
        );
      }
    },
    [api, stream.submit],
  );
  // answerQuestions keeps the answers to the questions of m and sends them as the next turn, drawn
  // as `shown` (the answers themselves) instead of the prompt that carries them.
  const answerQuestions = useCallback(
    async (m: Message, answers: QuestionAnswers, shown: string) => {
      rs.setMessages((prev) =>
        prev.map((x) => withQuestionAnswers(x, m, answers)),
      );
      if (m.turn != null) {
        try {
          await api(questionAnswersPath(rs.sessionRef.current, m.turn), {
            method: "PUT",
            headers: JSON_HEADERS,
            body: JSON.stringify(answers),
          });
        } catch {
          /* the answers stay on screen; only a reload loses them */
        }
      }
      // Sent as a task: the agent asked before doing work, so the answers are that work's request.
      await stream.submit(
        answers.request,
        SESSIONS_PATH + encodeURIComponent(rs.sessionRef.current) + "/task",
        choose(shown, shown, undefined),
      );
    },
    [api, stream.submit],
  );
  return { answerSuggestions, answerQuestions };
}
