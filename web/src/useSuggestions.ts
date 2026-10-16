// Answering the suggestions of a report: the answers are kept with the turn in the gateway, so
// the card draws the same pressed buttons when the conversation is opened again, and the accepted
// ones are sent as the next turn under a short label instead of the prompt they carry.
import { useCallback } from "preact/hooks";
import { t } from "./i18n";
import { choose } from "./choose";
import type { Api } from "./prApi";
import type { RunState } from "./useRunState";
import type { Stream } from "./useStream";
import type { Message, SetToast } from "./appTypes";
import type { SuggestionAnswers } from "./proposals";
import type { QuestionAnswers } from "./questions";
import { HTTP_CONFLICT, JSON_HEADERS, SESSIONS_PATH } from "./appConst";

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

// taskPath is where a session takes a task.
function taskPath(session: string): string {
  return SESSIONS_PATH + encodeURIComponent(session) + "/task";
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
// Undefined takes them off again, which unlocks the card.
function withQuestionAnswers(
  x: Message,
  m: Message,
  answers: QuestionAnswers | undefined,
): Message {
  if (x.id === m.id) {
    return { ...x, questionAnswers: answers };
  }
  return x;
}

// SuggestionsUi is what answering needs from the page: a way to read the conversation again when
// another tab answered first, and the toast that says the answers were not sent.
export interface SuggestionsUi {
  reload: () => Promise<void>;
  setToast: SetToast;
}

export function useSuggestions(
  api: Api,
  rs: RunState,
  stream: Stream,
  ui: SuggestionsUi,
) {
  const notSent = () =>
    ui.setToast({ message: t("Could not send the answers"), type: "error" });

  // sendTask starts `text` as the next turn of session `sid`. When the user is still there it is
  // sent through the stream like any turn. When they moved to another conversation while the
  // answers were being kept, the turn still belongs to the session that asked: it is started there
  // without being followed, since the stream draws into the conversation on screen, and the run
  // goes on in the gateway as any run left behind does. It says whether the gateway took the turn:
  // one that answered 409 or a failure did not, and the user is told.
  const sendTask = async (
    sid: string,
    text: string,
    shown?: string,
  ): Promise<boolean> => {
    if (rs.sessionRef.current === sid) {
      await stream.submit(text, taskPath(sid), shown);
      return true;
    }
    try {
      const res = await api(taskPath(sid), {
        method: "POST",
        headers: JSON_HEADERS,
        body: JSON.stringify({ task: text }),
      });
      void res.body?.cancel();
      if (res.ok) {
        return true;
      }
    } catch {
      /* told below, like a refusal */
    }
    notSent();
    return false;
  };

  const answerSuggestions = useCallback(
    async (m: Message, answers: SuggestionAnswers) => {
      // Read before any await: a switch while the answers are kept must not send them elsewhere.
      const sid = rs.sessionRef.current;
      rs.setMessages((prev) => prev.map((x) => withAnswers(x, m, answers)));
      // Kept before the turn starts. A gateway that cannot keep them costs only the marks after
      // a reload, so the turn is sent anyway.
      if (m.turn != null) {
        try {
          await api(answersPath(sid, m.turn), {
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
        await sendTask(sid, answers.request, t("Applying suggestions"));
      }
    },
    [api, stream.submit],
  );

  // keepQuestionAnswers stores the answers to the questions of m in session `sid`, and says
  // whether the turn that carries them may be sent. It may not when another tab answered first
  // (409: the conversation is read again, so the card shows those answers and the turn they sent),
  // nor when the gateway could not keep them (the card is unlocked, to be sent once more).
  const keepQuestionAnswers = async (
    sid: string,
    m: Message,
    turn: number,
    answers: QuestionAnswers,
  ): Promise<boolean> => {
    let status = 0;
    try {
      const res = await api(questionAnswersPath(sid, turn), {
        method: "PUT",
        headers: JSON_HEADERS,
        body: JSON.stringify(answers),
      });
      if (res.ok) {
        return true;
      }
      status = res.status;
    } catch {
      /* offline: told below, like any other failure */
    }
    const here = rs.sessionRef.current === sid;
    if (status === HTTP_CONFLICT) {
      if (here) {
        await ui.reload();
      }
      return false;
    }
    if (here) {
      rs.setMessages((prev) =>
        prev.map((x) => withQuestionAnswers(x, m, undefined)),
      );
    }
    notSent();
    return false;
  };

  // answerQuestions keeps the answers to the questions of m and sends them as the next turn, drawn
  // as `shown` (the answers themselves) instead of the prompt that carries them.
  const answerQuestions = useCallback(
    async (m: Message, answers: QuestionAnswers, shown: string) => {
      // Read before any await: a switch while the answers are kept must not send them elsewhere.
      const sid = rs.sessionRef.current;
      rs.setMessages((prev) =>
        prev.map((x) => withQuestionAnswers(x, m, answers)),
      );
      if (
        m.turn != null &&
        !(await keepQuestionAnswers(sid, m, m.turn, answers))
      ) {
        return;
      }
      // Sent as a task: the agent asked before doing work, so the answers are that work's request.
      const sent = await sendTask(
        sid,
        answers.request,
        choose(shown, shown, undefined),
      );
      if (!sent && rs.sessionRef.current === sid) {
        // The card is unlocked, to be answered once more.
        rs.setMessages((prev) =>
          prev.map((x) => withQuestionAnswers(x, m, undefined)),
        );
      }
    },
    [api, stream.submit],
  );
  return { answerSuggestions, answerQuestions };
}
