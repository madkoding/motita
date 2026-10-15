// A conversation as it is drawn again when it is opened: the gateway's transcript, with each
// turn's answer as the live view showed it.
import { choose } from "./choose";
import { t } from "./i18n";
import { isApplying, type SuggestionAnswers } from "./proposals";
import { hasReport, type TaskReport } from "./Report";
import {
  answersLabel,
  hasQuestions,
  isAnswering,
  type AskItem,
  type QuestionAnswers,
} from "./questions";
import type { ChangeReport, Message, StepView } from "./appTypes";

// TurnOutcome is how a turn ended, as the gateway kept it with the turn's checkpoint.
export interface TurnOutcome {
  result?: string;
  error?: string;
  report?: TaskReport;
  files?: NonNullable<ChangeReport["files"]>;
  artifacts?: { name: string; type: string; size: number }[];
  questions?: AskItem[];
}

export interface Checkpoint {
  turn: number;
  task?: string;
  files: boolean;
  steps: StepView[];
  outcome?: TurnOutcome;
  // answers are how the suggestions of this turn's report were answered.
  answers?: SuggestionAnswers;
  // question_answers are how the questions this turn asked were answered.
  question_answers?: QuestionAnswers;
}

export interface TranscriptTurn {
  User?: string;
  Agent?: string;
}

// answerOf is the message a finished turn was shown with: the answer with its report, the
// files it changed and its artifacts. The transcript only keeps the agent's one-line account of
// a task, which is what the model reads back; drawing that instead is how a conversation opened
// again came back shorter than it was.
function answerOf(
  id: number,
  result: string,
  out: TurnOutcome,
  cp?: Checkpoint,
): Message {
  const asked = hasQuestions(out.questions);
  return {
    id,
    role: "agent",
    turn: cp?.turn,
    answers: cp?.answers,
    text: result,
    changes: choose(!!out.files?.length, { files: out.files }, undefined),
    report: choose(hasReport(out.report), out.report, undefined),
    artifacts: choose(!!out.artifacts?.length, out.artifacts, undefined),
    questions: choose(asked, out.questions, undefined),
    origin: choose(asked, cp?.task ?? "", undefined),
    questionAnswers: cp?.question_answers,
  };
}

// agentMessages is what the agent said in one turn: its answer, or the error the turn failed
// with when it has no answer to show.
function agentMessages(
  said: string | undefined,
  out: TurnOutcome | undefined,
  nextId: () => number,
  cp?: Checkpoint,
): Message[] {
  if (out?.result) {
    return [answerOf(nextId(), out.result, out, cp)];
  }
  const msgs: Message[] = [];
  if (said) {
    msgs.push({ id: nextId(), role: "agent", text: said });
  }
  if (out?.error) {
    msgs.push({ id: nextId(), role: "agent", text: out.error, kind: "error" });
  }
  return msgs;
}

// shownOf is the label a user turn is drawn under when it carries what was answered on the turn
// before it (`prev`): the accepted suggestions, or the answers to its questions. The prompt it
// carries is for the agent, not for the reader.
function shownOf(prev: Checkpoint | undefined, text: string): string | undefined {
  if (isApplying(prev?.answers, text)) {
    return t("Applying suggestions");
  }
  const qa = prev?.question_answers;
  if (isAnswering(qa, text)) {
    return answersLabel(prev?.outcome?.questions ?? [], (qa as QuestionAnswers).answers);
  }
  return undefined;
}

// transcriptMessages turns the gateway's transcript, and the outcomes kept with its checkpoints,
// into the messages drawn for it.
export function transcriptMessages(
  turns: TranscriptTurn[],
  cps: Checkpoint[],
  nextId: () => number,
): Message[] {
  const byTurn = new Map<number, Checkpoint>();
  for (const cp of cps) {
    byTurn.set(cp.turn, cp);
  }
  const out: Message[] = [];
  for (const [i, m] of turns.entries()) {
    if (m.User) {
      // The turn that applies the suggestions answered on the turn before it is drawn by name:
      // the prompt it carries is for the agent, not for the reader.
      const shown = shownOf(byTurn.get(i - 1), m.User);
      out.push({ id: nextId(), role: "user", text: m.User, turn: i, shown });
    }
    // A turn still open carries what the run had done before it was cut. The stream only
    // brings what happens FROM NOW ON, so this is the one place the reader can see where
    // it got to - it is drawn whether or not the run is live.
    const cp = byTurn.get(i);
    out.push(...agentMessages(m.Agent, cp?.outcome, nextId, cp));
  }
  // A run that failed before it left a turn still has its checkpoint: the input and why it
  // stopped are what the reader saw, so they are drawn after the turns.
  const last = cps.find((cp) => cp.turn === turns.length);
  if (last?.outcome?.error && last.task) {
    out.push(
      { id: nextId(), role: "user", text: last.task, turn: last.turn },
      ...agentMessages(undefined, last.outcome, nextId),
    );
  }
  return out;
}
