// The four drawers on the right edge: the terminal history, the interactive terminal, the model's
// thinking and the run's agents. Opening one closes the others: they share the edge.
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
  const [ptyOpen, setPtyOpen] = useState(false);
  const [agentCopied, setAgentCopied] = useState<string | null>(null);
  const [agentNow, setAgentNow] = useState(() => Date.now());
  const term = useFollowEnd(termOpen);
  const think = useFollowEnd(thinkOpen);
  const stickTerm = term.stick;

  // only opens or toggles one drawer and closes the other three.
  const only = useCallback(
    (
      set: (fn: (open: boolean) => boolean) => void,
      next: (open: boolean) => boolean,
    ) => {
      for (const other of [
        setTermOpen,
        setThinkOpen,
        setAgentsOpen,
        setPtyOpen,
      ]) {
        if (other !== set) {
          other(() => false);
        }
      }
      set(next);
    },
    [],
  );
  const showTerm = useCallback(() => only(setTermOpen, () => true), []);
  const showAgents = useCallback(() => only(setAgentsOpen, () => true), []);
  const toggleTerm = useCallback(() => only(setTermOpen, (o) => !o), []);
  const toggleThink = useCallback(() => only(setThinkOpen, (o) => !o), []);
  const toggleAgents = useCallback(() => only(setAgentsOpen, (o) => !o), []);
  const togglePty = useCallback(() => only(setPtyOpen, (o) => !o), []);
  const closeTerm = useCallback(() => setTermOpen(false), []);
  const closeThink = useCallback(() => setThinkOpen(false), []);
  const closeAgents = useCallback(() => setAgentsOpen(false), []);
  const closePty = useCallback(() => setPtyOpen(false), []);

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
    ptyOpen,
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
    togglePty,
    closeTerm,
    closeThink,
    closeAgents,
    closePty,
    toggleAgent,
    copyMerge,
    termEntryDone,
  };
}

export type Drawers = ReturnType<typeof useDrawers>;
