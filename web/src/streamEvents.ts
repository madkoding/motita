// What each event of a run's stream does to the conversation on screen.
import { tf } from "./i18n";
import { choose } from "./choose";
import { hasReport, type TaskReport } from "./Report";
import { hasQuestions, turnTask, type AskItem } from "./questions";
import { sortAgents } from "./appFormat";
import {
  classifyProgress,
  commandOf,
  isPhaseLine,
  parseSub,
  splitDoing,
  splitOutput,
  subTag,
} from "./appParse";
import { keyed } from "./useRunState";
import type { RunState } from "./useRunState";
import type {
  ChangeReport,
  Message,
  PendingApproval,
  ShellEntry,
  StepView,
} from "./appTypes";
import {
  AGENTS_PREFIX,
  SHELL_LOG_MAX,
  STEPS_MAX,
  SUB_PREFIX,
} from "./appConst";

// StreamPayload is the data of one event, with every field an event of the stream can carry.
export interface StreamPayload {
  // attached: the id of the run the stream follows.
  run_id?: string;
  turn?: number;
  dropped?: number;
  pending_approval?: PendingApproval;
  agents?: unknown;
  text?: string;
  result?: string;
  changes?: ChangeReport;
  artifacts?: { name: string; type: string; size: number }[];
  report?: TaskReport;
  questions?: AskItem[];
  error?: string;
  // approval_denied: what was refused, and why it needed asking.
  command?: string;
  reason?: string;
}

// StreamContext is what the handlers act on.
export interface StreamContext {
  rs: RunState;
  finish: () => void;
}

// markTurn gives the last input of the user that has no checkpoint yet the one the run became.
function markTurn(rs: RunState, turn: number) {
  rs.setMessages((prev) => {
    for (let i = prev.length - 1; i >= 0; i--) {
      if (prev[i].role !== "user") {
        continue;
      }
      if (prev[i].turn != null) {
        break;
      }
      const next = prev.slice();
      next[i] = { ...prev[i], turn };
      return next;
    }
    return prev;
  });
}

// onAttached: which checkpoint this run is. A run that is replayed from the start rebuilds its
// steps from that replay, so what was loaded for it is dropped first: it would be doubled.
function onAttached({ rs }: StreamContext, payload: StreamPayload) {
  if (payload.run_id) {
    rs.runIdRef.current = payload.run_id;
  }
  if (typeof payload.turn === "number") {
    const turn: number = payload.turn;
    rs.liveTurnRef.current = turn;
    markTurn(rs, turn);
    if (rs.lastIdRef.current === 0) {
      rs.setStepsByTurn((prev) => ({ ...prev, [turn]: [] }));
    }
  }
  const dropped = payload.dropped ?? 0;
  if (dropped > 0) {
    rs.setMessages((prev) => [
      ...prev,
      {
        id: rs.nextId(),
        role: "agent",
        kind: "kind",
        text: tf("({n} event(s) were not kept while nothing was listening)", {
          n: dropped,
        }),
      },
    ]);
  }
  // The gateway names the question still waiting, if any: one answered elsewhere or cut off goes.
  rs.setApproval((a) => payload.pending_approval ?? typedCommandOnly(a));
  if (Array.isArray(payload.agents)) {
    rs.setAgents(sortAgents(payload.agents));
  }
}

// settleLastCommand puts a command's result on the last command of the list still waiting for one.
function settleLastCommand<T extends { exit?: number | null }>(
  list: T[],
  isCommand: (x: T) => boolean,
  patch: Partial<T>,
): T[] | null {
  for (let i = list.length - 1; i >= 0; i--) {
    if (isCommand(list[i]) && list[i].exit == null) {
      const next = list.slice();
      next[i] = { ...list[i], ...patch };
      return next;
    }
  }
  return null;
}

// onOutput: a command's output is NOT a step of the conversation: the trail shows the
// command and whether it worked, and the terminal drawer keeps what it printed.
// The result lands on the command it belongs to.
function onOutput({ rs }: StreamContext, text: string) {
  const { exit, body } = splitOutput(text);
  const result = { exit: exit ?? 0, out: body };
  const liveTurn = rs.liveTurnRef.current;
  if (liveTurn != null) {
    rs.setStepsByTurn((prev) => {
      const next = settleLastCommand<StepView>(
        prev[liveTurn] ?? [],
        (st) => st.kind === "command",
        result,
      );
      if (!next) {
        return prev;
      }
      return { ...prev, [liveTurn]: next };
    });
  }
  rs.setShellLog(
    (prev) => settleLastCommand<ShellEntry>(prev, () => true, result) ?? prev,
  );
  rs.setLiveThought(null);
}

// stepKind is how a progress line is kept among the turn's steps.
function stepKind(kind: string): string {
  if (kind === "command") {
    return "command";
  }
  return choose(kind === "thought", "thought", "step");
}

// onStep: a progress line that is a step of the turn (a command, a thought, a note), or one
// that only names the phase the pipeline is in. The second kind moves the activity line and is
// filed nowhere: the trail is a timeline of what the agent did, not of the labels around it.
//
// label is what the action is for, in the words it announced itself with. The conversation shows it
// in place of the command, which only the terminal drawer keeps.
function onStep(
  { rs }: StreamContext,
  text: string,
  kind: string,
  label?: string,
) {
  // A finished thought belongs to the thinking drawer, not to the chat: it is filed with
  // the turn's steps (where it is kept) and the live snapshot it grew from is done.
  if (kind !== "thought") {
    rs.setActivity(label ?? text);
    rs.setActivityKind(choose(label, "doing", kind));
  }
  const liveTurn = rs.liveTurnRef.current;
  if (text && liveTurn != null && !isPhaseLine(text)) {
    const step = keyed({
      kind: stepKind(kind),
      text,
      ...choose(kind === "command", { exit: null }, {}),
      ...choose(label, { label }, {}),
    });
    rs.setStepsByTurn((prev) => ({
      ...prev,
      [liveTurn]: [...(prev[liveTurn] ?? []), step].slice(-STEPS_MAX),
    }));
  }
  if (kind === "command") {
    const cmd = commandOf(text);
    rs.setShellLog((prev) =>
      [...prev, { id: rs.nextId(), cmd, exit: null, out: "" }].slice(
        -SHELL_LOG_MAX,
      ),
    );
  }
  rs.setLiveThought(null);
}

// onSub: a command a background agent ran, or a thought it finished. They go to the terminal and
// thinking drawers like the main agent's, named by the agent in a tag in front of the text; the
// command arrives with its result, so it never waits to be settled by an output line that another
// agent's command could take.
function onSub({ rs }: StreamContext, body: string) {
  const sub = parseSub(body);
  const liveTurn = rs.liveTurnRef.current;
  if (!sub || liveTurn == null) {
    return;
  }
  const tag = "[" + subTag(sub) + "] ";
  const isThought = sub.kind === "thought";
  // The line leaves out a success and an empty output.
  const exit = sub.exit ?? 0;
  const out = sub.out ?? "";
  const step = keyed(
    choose(
      isThought,
      { kind: "thought", text: "thinking: " + tag + sub.text },
      {
        kind: "command",
        text: "running: " + tag + sub.text,
        exit,
        out,
      },
    ),
  );
  rs.setStepsByTurn((prev) => ({
    ...prev,
    [liveTurn]: [...(prev[liveTurn] ?? []), step].slice(-STEPS_MAX),
  }));
  if (!isThought) {
    rs.setShellLog((prev) =>
      [
        ...prev,
        {
          id: rs.nextId(),
          cmd: tag + sub.text,
          exit,
          out,
        },
      ].slice(-SHELL_LOG_MAX),
    );
  }
}

function onProgress(ctx: StreamContext, payload: StreamPayload) {
  const text: string = payload.text || "";
  if (text.startsWith(SUB_PREFIX)) {
    onSub(ctx, text.slice(SUB_PREFIX.length));
    return;
  }
  if (text.startsWith(AGENTS_PREFIX)) {
    // The agent list sent as a plain line: a snapshot, never a step of the conversation.
    try {
      ctx.rs.setAgents(
        sortAgents(JSON.parse(text.slice(AGENTS_PREFIX.length))),
      );
    } catch {
      /* not a list */
    }
    return;
  }
  const words = splitDoing(text);
  if (words?.rest) {
    onStep(ctx, words.rest, classifyProgress(words.rest) ?? "", words.doing);
    return;
  }
  const kind = classifyProgress(text) ?? "";
  if (kind === "output") {
    onOutput(ctx, text);
    return;
  }
  onStep(ctx, text, kind);
}

// typedCommandOnly is the question on screen once the run has none open: a typed command's
// question ("shell:") belongs to no run, so it stays; the run's goes.
export function typedCommandOnly(
  a: PendingApproval | null,
): PendingApproval | null {
  return choose(a?.id.startsWith("shell:"), a, null);
}

// withoutDraft is the conversation without the answer being written, if its last message is one.
export function withoutDraft(prev: Message[]): Message[] {
  if (prev.length > 0 && prev[prev.length - 1].draft) {
    return prev.slice(0, -1);
  }
  return prev;
}

// draftIdOr is the id of the draft that rest was taken from prev without, or a new one.
function draftIdOr(
  prev: Message[],
  rest: Message[],
  next: () => number,
): number {
  if (rest !== prev) {
    return prev[prev.length - 1].id;
  }
  return next();
}

// onAnswer: a snapshot of the answer being written, everything so far. It is shown where the
// answer will be, growing as it arrives; an empty one clears it.
function onAnswer({ rs }: StreamContext, payload: StreamPayload) {
  const text: string = payload.text || "";
  rs.setMessages((prev) => {
    const rest = withoutDraft(prev);
    if (!text) {
      return rest;
    }
    const id = draftIdOr(prev, rest, rs.nextId);
    return [
      ...rest,
      {
        id,
        role: "agent",
        text,
        turn: rs.liveTurnRef.current ?? undefined,
        draft: true,
      },
    ];
  });
}

function onDone({ rs, finish }: StreamContext, payload: StreamPayload) {
  rs.setActivity(null);
  const result = payload.result;
  rs.setMessages((prev) => {
    const rest = withoutDraft(prev);
    if (!result) {
      return rest;
    }
    // The answer takes the place of its draft, as the same message, so it does not blink.
    const id = draftIdOr(prev, rest, rs.nextId);
    return [
      ...rest,
      {
        id,
        role: "agent",
        text: result,
        turn: rs.liveTurnRef.current ?? undefined,
        changes: payload.changes,
        artifacts: choose(
          Array.isArray(payload.artifacts) && payload.artifacts.length,
          payload.artifacts,
          undefined,
        ),
        report: choose(hasReport(payload.report), payload.report, undefined),
        ...questionsOf(payload.questions, rest),
      },
    ];
  });
  finish();
}

// onApprovalDenied: a run with nobody to ask (a scheduled one) needed an approval, and the
// gateway refused it on the user's behalf. The run goes on without that action, so the
// conversation says what was not done and the gateway's reason, which names the setting that
// changes it: without this the turn just ends without explaining what it skipped.
function onApprovalDenied({ rs }: StreamContext, payload: StreamPayload) {
  const what = payload.command || payload.reason || "";
  const note = tf("Refused, nobody was there to approve it: {what}", { what });
  rs.setMessages((prev) => [
    ...prev,
    {
      id: rs.nextId(),
      role: "agent",
      kind: "warning",
      text: choose(payload.error, note + " — " + payload.error, note),
    },
  ]);
}

// questionsOf is what an answer that asked carries: its questions, and the request they are about
// (the user's newest input, as the gateway keeps it for the turn: the same request a reload reads
// from the checkpoint), which goes back with the answers. Nothing when it asked nothing.
function questionsOf(
  questions: AskItem[] | undefined,
  prev: Message[],
): Pick<Message, "questions" | "origin"> {
  if (!hasQuestions(questions)) {
    return {};
  }
  // The last user message, found by hand: findLast is newer than the ES2020 the build targets.
  let origin = "";
  for (const m of prev) {
    if (m.role === "user") {
      origin = turnTask(m.text);
    }
  }
  return { questions, origin };
}

function onError({ rs, finish }: StreamContext, payload: StreamPayload) {
  rs.setActivity(null);
  const error = payload.error;
  rs.setMessages((prev) => {
    const rest = withoutDraft(prev);
    if (!error) {
      return rest;
    }
    return [
      ...rest,
      { id: rs.nextId(), role: "agent", text: error, kind: "error" },
    ];
  });
  finish();
}

const HANDLERS: Record<
  string,
  (ctx: StreamContext, payload: StreamPayload) => void
> = {
  attached: onAttached,
  // A snapshot of the run's agents: it replaces the one before it.
  agents: ({ rs }, payload) => rs.setAgents(sortAgents(payload.agents)),
  progress: onProgress,
  // Ephemeral: never replayed, never kept. See EventThinking in the gateway.
  thinking: ({ rs }, payload) => rs.setLiveThought(payload.text || null),
  // Ephemeral too: the answer while it is being written.
  answer: onAnswer,
  approval: ({ rs }, payload) => {
    rs.setActivity(null);
    rs.setApproval(payload as PendingApproval);
  },
  approval_denied: onApprovalDenied,
  done: onDone,
  error: onError,
};

// handleStreamEvent applies one event. Unknown events are ignored: a future server may add one.
export function handleStreamEvent(
  ctx: StreamContext,
  name: string,
  payload: StreamPayload,
) {
  const handler = HANDLERS[name];
  if (handler) {
    handler(ctx, payload);
  }
}
