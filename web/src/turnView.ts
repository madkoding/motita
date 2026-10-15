// What the turns of the conversation read, in a context of its own.
//
// The store (`useApp`) is a new object on every render of the app, and the conversation streams:
// every token re-renders the app, so every component that reads the store re-renders with it. For
// the turns that meant the WHOLE conversation was diffed again on each token, a hundred turns to
// change the last one. The turns read this instead: a value that only changes when something a
// turn draws changes (a run starting, a menu opening, an answer being read aloud), with every
// action behind a stable function. A memoized turn then really is skipped while another streams.
import { createContext } from "preact";
import { useContext, useMemo, useRef } from "preact/hooks";
import type { Store } from "./appStore";
import type { CheckpointInfo, Message, StepView } from "./appTypes";
import type { SuggestionAnswers } from "./proposals";

export interface TurnView {
  running: boolean;
  activityKind: string;
  cpInfo: Record<number, CheckpointInfo>;
  cpMenu: number | null;
  cpArmed: boolean;
  speakingId: number | null;
  speechSupported: boolean;
  /** The id of the last message of the conversation: the one still streaming while a run works. */
  lastMsgId: number | null;
  /** The newest input of the user: suggestions of a report older than it are stale. */
  lastUserId: number;
  setCpMenu: (m: number | null) => void;
  setCpArmed: (a: boolean) => void;
  toggleCpMenu: (turn: number) => void;
  restoreCheckpoint: (turn: number, files: boolean) => void;
  stopRun: () => void;
  submit: (text: string) => void;
  answerSuggestions: (m: Message, a: SuggestionAnswers) => void;
  speak: (m: Message) => void;
  showRunArtifact: (a: { name: string; type: string }) => void;
}

export const TurnCtx = createContext<TurnView | null>(null);

/** The turn view, for a component inside the conversation. */
export function useTurn(): TurnView {
  return useContext(TurnCtx) as TurnView;
}

/** lastUserIdOf is the id of the newest input of the user, or -1 when there is none. */
export function lastUserIdOf(messages: Message[]): number {
  for (let i = messages.length - 1; i >= 0; i--) {
    if (messages[i].role === "user") {
      return messages[i].id;
    }
  }
  return -1;
}

/** useTurnView builds the turn view from the store, changing only when what turns draw does. */
export function useTurnView(store: Store): TurnView {
  const latest = useRef(store);
  latest.current = store;
  // The actions go through the latest store: their identity never changes, so they never make a
  // turn render again, and they always act on the state of now.
  const actions = useMemo(
    () => ({
      setCpMenu: (m: number | null) => latest.current.checkpoints.setCpMenu(m),
      setCpArmed: (a: boolean) => latest.current.checkpoints.setCpArmed(a),
      toggleCpMenu: (turn: number) =>
        latest.current.checkpoints.toggleCpMenu(turn),
      restoreCheckpoint: (turn: number, files: boolean) =>
        void latest.current.checkpoints.restoreCheckpoint(turn, files),
      stopRun: () => latest.current.stream.stopRun(),
      submit: (text: string) => void latest.current.stream.submit(text),
      answerSuggestions: (m: Message, a: SuggestionAnswers) =>
        void latest.current.answerSuggestions(m, a),
      speak: (m: Message) => latest.current.speech.speak(m),
      showRunArtifact: (a: { name: string; type: string }) =>
        latest.current.artifacts.showRunArtifact(a),
    }),
    [],
  );
  const { rs, checkpoints, speech } = store;
  const { messages } = rs;
  let lastMsgId: number | null = null;
  if (messages.length) {
    lastMsgId = messages[messages.length - 1].id;
  }
  const lastUserId = lastUserIdOf(messages);
  return useMemo(
    () => ({
      ...actions,
      running: rs.running,
      activityKind: rs.activityKind,
      cpInfo: rs.cpInfo,
      cpMenu: checkpoints.cpMenu,
      cpArmed: checkpoints.cpArmed,
      speakingId: speech.speakingId,
      speechSupported: speech.supported,
      lastMsgId,
      lastUserId,
    }),
    [
      actions,
      rs.running,
      rs.activityKind,
      rs.cpInfo,
      checkpoints.cpMenu,
      checkpoints.cpArmed,
      speech.speakingId,
      speech.supported,
      lastMsgId,
      lastUserId,
    ],
  );
}

/** What a memoized turn is drawn from: its group, and the run state that concerns it. */
export interface TurnInput {
  g: { key: number; turn?: number; items: Message[] };
  steps: StepView[] | undefined;
  liveHere: boolean;
  activity: string | null;
}

/**
 * sameTurn says two renders of a turn would draw the same thing. The groups are rebuilt on every
 * render, so they are compared by what they hold: the messages are kept immutably, so a message
 * that did not change is the same object, and so are the steps of a turn nothing was added to.
 */
export function sameTurn(a: TurnInput, b: TurnInput): boolean {
  if (!sameRunState(a, b) || !sameGroupShape(a.g, b.g)) {
    return false;
  }
  return a.g.items.every((m, i) => m === b.g.items[i]);
}

// sameRunState says the run state that concerns a turn is the same in both renders.
function sameRunState(a: TurnInput, b: TurnInput): boolean {
  return (
    a.steps === b.steps &&
    a.liveHere === b.liveHere &&
    a.activity === b.activity
  );
}

// sameGroupShape says two groups are the same turn holding as many messages.
function sameGroupShape(a: TurnInput["g"], b: TurnInput["g"]): boolean {
  return (
    a.key === b.key && a.turn === b.turn && a.items.length === b.items.length
  );
}
