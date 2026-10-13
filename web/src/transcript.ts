// A conversation as it is drawn again when it is opened: the gateway's transcript, with each
// turn's answer as the live view showed it.
import { choose } from "./choose";
import { hasReport, type TaskReport } from "./Report";
import type { ChangeReport, Message, StepView } from "./appTypes";

// TurnOutcome is how a turn ended, as the gateway kept it with the turn's checkpoint.
export interface TurnOutcome {
  result?: string;
  error?: string;
  report?: TaskReport;
  files?: ChangeReport["files"];
  artifacts?: { name: string; type: string; size: number }[];
}

export interface Checkpoint {
  turn: number;
  task?: string;
  files: boolean;
  steps: StepView[];
  outcome?: TurnOutcome;
}

export interface TranscriptTurn {
  User?: string;
  Agent?: string;
}

// answerOf is the message a finished turn was shown with: the answer with its report, the
// files it changed and its artifacts. The transcript only keeps the agent's one-line account of
// a task, which is what the model reads back; drawing that instead is how a conversation opened
// again came back shorter than it was.
function answerOf(id: number, said: string, out: TurnOutcome): Message {
  return {
    id,
    role: "agent",
    text: out.result || said,
    changes: choose(!!out.files?.length, { files: out.files }, undefined),
    report: choose(hasReport(out.report), out.report, undefined),
    artifacts: choose(!!out.artifacts?.length, out.artifacts, undefined),
  };
}

// agentMessages is what the agent said in one turn: its answer, or the error the turn failed
// with when it has no answer to show.
function agentMessages(
  said: string | undefined,
  out: TurnOutcome | undefined,
  nextId: () => number,
): Message[] {
  if (out?.result) {
    return [answerOf(nextId(), said ?? "", out)];
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

// transcriptMessages turns the gateway's transcript, and the outcomes kept with its checkpoints,
// into the messages drawn for it.
export function transcriptMessages(
  turns: TranscriptTurn[],
  cps: Checkpoint[],
  nextId: () => number,
): Message[] {
  const outcomes = new Map<number, TurnOutcome>();
  for (const cp of cps) {
    if (cp.outcome) {
      outcomes.set(cp.turn, cp.outcome);
    }
  }
  const out: Message[] = [];
  for (const [i, m] of turns.entries()) {
    if (m.User) {
      out.push({ id: nextId(), role: "user", text: m.User, turn: i });
    }
    // A turn still open carries what the run had done before it was cut. The stream only
    // brings what happens FROM NOW ON, so this is the one place the reader can see where
    // it got to - it is drawn whether or not the run is live.
    out.push(...agentMessages(m.Agent, outcomes.get(i), nextId));
  }
  // A run that failed before it left a turn still has its checkpoint: the input and why it
  // stopped are what the reader saw, so they are drawn after the turns.
  const last = cps.find((cp) => cp.turn === turns.length);
  if (last?.outcome?.error && last.task) {
    out.push({ id: nextId(), role: "user", text: last.task, turn: last.turn });
    out.push(...agentMessages(undefined, last.outcome, nextId));
  }
  return out;
}
