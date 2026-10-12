// The state of the conversation on screen: its messages, the steps of every turn, what the agent
// is doing right now, the commands it ran, the question it is waiting on and its agents.
import { useCallback, useRef, useState } from "preact/hooks";
import type { RunLink } from "./runLink";
import type {
  AgentInfo,
  CheckpointInfo,
  Message,
  PendingApproval,
  ShellEntry,
  StepView,
} from "./appTypes";

// stepSeq numbers the steps as they arrive, so a trail can tell them apart when it draws them.
let stepSeq = 0;

// keyed gives a step the key it is drawn with.
export function keyed(step: StepView): StepView {
  stepSeq += 1;
  return { ...step, key: stepSeq };
}

export function useRunState() {
  const [messages, setMessages] = useState<Message[]>([]);
  const [running, setRunning] = useState(false);
  const [activity, setActivity] = useState<string | null>(null);
  const [activityKind, setActivityKind] = useState<string>("");
  // The steps of every turn, by turn number: the history of the work, kept with the session.
  const [stepsByTurn, setStepsByTurn] = useState<Record<number, StepView[]>>(
    {},
  );
  const [cpInfo, setCpInfo] = useState<Record<number, CheckpointInfo>>({});
  // The turn the run in flight belongs to, once the gateway has said which one it is.
  const liveTurnRef = useRef<number | null>(null);
  // What the model is writing RIGHT NOW, while a phase is being generated. Each
  // 'thinking' event replaces the last, and the next ordinary step clears it: by
  // then the phase's finished reasoning has arrived as a step of its own.
  const [liveThought, setLiveThought] = useState<string | null>(null);
  // Every command the agent ran in this conversation, with its output: what the terminal
  // drawer replays. The drawer is closed by default; the tab on the right edge opens it.
  const [shellLog, setShellLog] = useState<ShellEntry[]>([]);
  // The highest history id the drawer has finished playing. Everything at or below it is
  // drawn whole; the first entry above it is the one being typed; the rest wait their turn.
  // An id and not a position, because the log is capped and its oldest entries fall off.
  const [termSeenId, setTermSeenId] = useState(0);
  // The run's agents (the main one and the background ones it started), for the AGENTS drawer.
  const [agents, setAgents] = useState<AgentInfo[]>([]);
  const [agentOpenId, setAgentOpenId] = useState<string | null>(null);
  const [approval, setApproval] = useState<PendingApproval | null>(null);
  // The option the keyboard has highlighted in the approval panel (0 run, 1 allow all, 2 no), and
  // the panel itself, which takes focus when a question arrives so the arrows and Enter work.
  const [approvalSel, setApprovalSel] = useState(0);
  const approvalRef = useRef<HTMLFieldSetElement>(null);
  const [sessionId, setSessionId] = useState<string>("");

  const lastIdRef = useRef(0);
  const runningRef = useRef(false);
  const reconnectTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const msgIdRef = useRef(0);
  const sessionRef = useRef("");
  // runSessionRef is the conversation the run in flight was started in. A run
  // belongs to its SESSION, not to the tab, and the user is free to walk away
  // from it: without pinning the id, an answer arriving after a session switch
  // would clear the flag on whichever conversation happened to be on screen -
  // leaving the one that actually finished looking busy and the one that is
  // idle looking busy too.
  const runSessionRef = useRef("");
  // handoffRef is set when a message was sent to a run in flight (queued, or an interruption
  // that carries one): the gateway starts the next run on its own when this one ends, and this
  // tab never opened that stream, so finish() asks the gateway for it and follows it.
  const handoffRef = useRef(false);
  // linkRef is the stream this tab is reading, and the session it was opened for. See runLink.ts.
  const linkRef = useRef<RunLink | null>(null);

  const nextId = useCallback(() => {
    msgIdRef.current += 1;
    return msgIdRef.current;
  }, []);

  // clearConversation empties what is drawn for a conversation, before another one is loaded.
  const clearConversation = useCallback(() => {
    setMessages([]);
    setActivity(null);
    setStepsByTurn({});
    setCpInfo({});
    liveTurnRef.current = null;
    setShellLog([]);
    setTermSeenId(0);
    setLiveThought(null);
    setApproval(null);
    setAgents([]);
    setAgentOpenId(null);
  }, []);

  return {
    messages,
    setMessages,
    running,
    setRunning,
    activity,
    setActivity,
    activityKind,
    setActivityKind,
    stepsByTurn,
    setStepsByTurn,
    cpInfo,
    setCpInfo,
    liveTurnRef,
    liveThought,
    setLiveThought,
    shellLog,
    setShellLog,
    termSeenId,
    setTermSeenId,
    agents,
    setAgents,
    agentOpenId,
    setAgentOpenId,
    approval,
    setApproval,
    approvalSel,
    setApprovalSel,
    approvalRef,
    sessionId,
    setSessionId,
    lastIdRef,
    runningRef,
    reconnectTimer,
    sessionRef,
    runSessionRef,
    handoffRef,
    linkRef,
    nextId,
    clearConversation,
  };
}

export type RunState = ReturnType<typeof useRunState>;
