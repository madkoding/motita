// The three drawers on the right edge: the terminal history, the model's thinking and the run's
// agents. Opening one closes the others: they share the edge.
import { useCallback, useEffect, useState } from "preact/hooks";
import { useFollowEnd } from "./useFollowEnd";
import { copyText } from "./clipboard";
import { choose } from "./choose";
import type { RunState } from "./useRunState";
import { CLOCK_TICK_MS, COPIED_MS, TERM_CAP, TERM_KEEP } from "./appConst";

export function useDrawers(rs: RunState) {
  const { shellLog, termSeenId, setTermSeenId, agents, setAgentOpenId } = rs;
  const [termOpen, setTermOpen] = useState(false);
  // The thinking drawer: what the model reasoned, in the same retro screen but orange. It is
  // closed by default, and opening one drawer closes the other: they share the edge.
  const [thinkOpen, setThinkOpen] = useState(false);
  const [agentsOpen, setAgentsOpen] = useState(false);
  const [agentCopied, setAgentCopied] = useState<string | null>(null);
  const [agentNow, setAgentNow] = useState(() => Date.now());
  const term = useFollowEnd(termOpen);
  const think = useFollowEnd(thinkOpen);
  const stickTerm = term.stick;

  const showTerm = useCallback(() => {
    setTermOpen(true);
    setAgentsOpen(false);
    setThinkOpen(false);
  }, []);
  const showAgents = useCallback(() => {
    setAgentsOpen(true);
    setTermOpen(false);
    setThinkOpen(false);
  }, []);
  const toggleTerm = useCallback(() => {
    setTermOpen((o) => !o);
    setThinkOpen(false);
    setAgentsOpen(false);
  }, []);
  const toggleThink = useCallback(() => {
    setThinkOpen((o) => !o);
    setTermOpen(false);
    setAgentsOpen(false);
  }, []);
  const toggleAgents = useCallback(() => {
    setAgentsOpen((o) => !o);
    setTermOpen(false);
    setThinkOpen(false);
  }, []);
  const closeTerm = useCallback(() => setTermOpen(false), []);
  const closeThink = useCallback(() => setThinkOpen(false), []);
  const closeAgents = useCallback(() => setAgentsOpen(false), []);

  // A running agent's clock ticks while the drawer shows it, and only then.
  const agentsTicking = agentsOpen && agents.some((a) => a.state === "running");
  useEffect(() => {
    if (!agentsTicking) {
      return undefined;
    }
    setAgentNow(Date.now());
    const timer = setInterval(() => setAgentNow(Date.now()), CLOCK_TICK_MS);
    return () => clearInterval(timer);
  }, [agentsTicking]);

  useEffect(() => {
    stickTerm();
  }, [shellLog, stickTerm]);

  // The queue the drawer plays: one entry at a time, in order, only while it is open. When
  // commands arrive faster than they can be typed the terminal catches up by typing FASTER
  // (see termBoost), and never by drawing an entry whole - that read as "everything at once".
  // The only cut is a sanity cap: a history this far behind is not being watched.
  useEffect(() => {
    if (!termOpen) {
      return;
    }
    const pending = shellLog.filter((e) => e.id > termSeenId);
    if (pending.length > TERM_CAP) {
      setTermSeenId(pending[pending.length - TERM_KEEP].id);
    }
  }, [termOpen, shellLog, termSeenId]);

  // toggleAgent unfolds a finished agent's summary, or folds it back.
  const toggleAgent = useCallback((id: string) => {
    setAgentOpenId((cur) => choose(cur === id, null, id));
  }, []);

  // copyMerge copies the command that merges an agent's branch, and says so for a moment.
  const copyMerge = useCallback((id: string, merge: string) => {
    void copyText(merge).then((ok) => {
      if (!ok) {
        return;
      }
      setAgentCopied(id);
      setTimeout(
        () => setAgentCopied((c) => choose(c === id, null, c)),
        COPIED_MS,
      );
    });
  }, []);

  // termEntryDone marks a history entry as played, so the next one starts.
  const termEntryDone = useCallback(
    (id: number) => setTermSeenId((n) => Math.max(n, id)),
    [],
  );

  return {
    termOpen,
    thinkOpen,
    agentsOpen,
    agentCopied,
    agentNow,
    term,
    think,
    stickTerm,
    showTerm,
    showAgents,
    toggleTerm,
    toggleThink,
    toggleAgents,
    closeTerm,
    closeThink,
    closeAgents,
    toggleAgent,
    copyMerge,
    termEntryDone,
  };
}

export type Drawers = ReturnType<typeof useDrawers>;
