// The drawers on the right edge of the conversation, drawn as retro screens: the terminal history,
// the model's thinking and the run's agents. Each tab sits on the right edge and its drawer opens to
// the LEFT from it; opening one closes the others. They are siblings of the scroll container, for
// the reason the session modal is: they must stay put while the conversation scrolls.
import { useCallback, useRef } from "preact/hooks";
import { t, plural } from "./i18n";
import { choose } from "./choose";
import { useApp } from "./appStore";
import { useStopClicks } from "./domHooks";
import {
  TermEntry,
  TermEmpty,
  type EntryPhase,
  type ShellStatusView,
} from "./Term";
import {
  agentElapsed,
  agentMark,
  formatElapsed,
  formatTokens,
  sumTokens,
  tokenBreakdown,
  tokenTotal,
} from "./appFormat";
import type { AgentInfo, ShellEntry } from "./appTypes";
import { NO_RESULT, TERM_BOOST_MAX } from "./appConst";

// CrtFx are the CRT effects: layers ABOVE the text and never inside it. They carry no pointer
// events, so selecting and scrolling the history work as usual.
function CrtFx({ full }: { full: boolean }) {
  return (
    <>
      <div class="term-fx term-fx-scan" aria-hidden="true" />
      {full && <div class="term-fx term-fx-sweep" aria-hidden="true" />}
      {full && <div class="term-fx term-fx-noise" aria-hidden="true" />}
      <div class="term-fx term-fx-vignette" aria-hidden="true" />
    </>
  );
}

// exitLabel is how a command's result reads in the terminal.
function exitLabel(exit: number): string {
  if (exit === NO_RESULT) {
    return t("[no result]");
  }
  return choose(exit, `[exit ${exit}]`, "[ok]");
}

// statusOf is a history entry's status line.
function statusOf(e: ShellEntry): ShellStatusView {
  if (e.exit == null) {
    return { label: t("running…"), kind: "running" };
  }
  return {
    label: exitLabel(e.exit),
    kind: choose(e.exit && e.exit !== NO_RESULT, "bad" as const, "" as const),
  };
}

// TermRow is one entry of the terminal history.
function TermRow({
  e,
  phase,
  boost,
}: {
  e: ShellEntry;
  phase: EntryPhase;
  boost: number;
}) {
  const { drawers } = useApp();
  const { termEntryDone } = drawers;
  const id = e.id;
  const onFinished = useCallback(() => termEntryDone(id), [termEntryDone, id]);
  return (
    <TermEntry
      cmd={e.cmd}
      out={e.out}
      onTick={drawers.stickTerm}
      boost={boost}
      phase={phase}
      onFinished={onFinished}
      status={statusOf(e)}
    />
  );
}

// TermHistory is what the terminal drawer plays: one entry at a time, faster when it is behind.
function TermHistory() {
  const { rs, drawers } = useApp();
  const { shellLog, termSeenId } = rs;
  const firstPending = shellLog.find((e) => e.id > termSeenId);
  const behind = shellLog.filter((e) => e.id > termSeenId).length - 1;
  // 1x when caught up, 3x with one waiting, 5x with two, and so on up to 9x.
  const termBoost = Math.min(TERM_BOOST_MAX, 1 + 2 * Math.max(0, behind));
  const phaseOf = (e: ShellEntry): EntryPhase => {
    if (e.id <= termSeenId) {
      return "done";
    }
    return choose(drawers.termOpen && e === firstPending, "live", "wait");
  };
  return (
    <>
      {shellLog.length === 0 && <TermEmpty onTick={drawers.stickTerm} />}
      {shellLog.map((e) => (
        <TermRow key={e.id} e={e} phase={phaseOf(e)} boost={termBoost} />
      ))}
    </>
  );
}

// TermDrawer is everything the agent ran in this conversation, input and output.
export function TermDrawer() {
  const { rs, drawers } = useApp();
  const { termOpen, term } = drawers;
  const count = rs.shellLog.length;
  return (
    <div class={`term-drawer${choose(termOpen, " is-open", "")}`}>
      <button
        type="button"
        class="term-tab"
        aria-expanded={termOpen}
        aria-controls="term-panel"
        title={choose(
          termOpen,
          t("Close the terminal"),
          t("Open the terminal history"),
        )}
        onClick={drawers.toggleTerm}
      >
        <span class="term-tab-label">TTY</span>
        {count > 0 && <span class="term-tab-count">{count}</span>}
      </button>
      <section
        class="term-panel"
        id="term-panel"
        aria-label={t("terminal history")}
        aria-hidden={!termOpen}
      >
        <div class="term-titlebar">
          <span>
            motita@shell — {plural(count, "1 command", "{n} commands")}
          </span>
          <button
            type="button"
            class="term-close"
            onClick={drawers.closeTerm}
            aria-label={t("Close the terminal")}
            tabIndex={choose(termOpen, 0, -1)}
          >
            ×
          </button>
        </div>
        <div class="term-viewport">
          <div class="term-screen" ref={term.ref} {...term.handlers}>
            <TermHistory />
          </div>
          <CrtFx full />
        </div>
      </section>
    </div>
  );
}

// thoughtsOf is everything the model reasoned in this conversation, in the order of its turns,
// and what it is writing right now.
function thoughtsOf(
  stepsByTurn: Record<number, { kind: string; text: string }[]>,
  live: string | null,
) {
  const thoughts: { key: string; text: string; live?: boolean }[] = [];
  for (const turn of Object.keys(stepsByTurn)
    .map(Number)
    .sort((a, b) => a - b)) {
    stepsByTurn[turn].forEach((st, i) => {
      if (st.kind === "thought") {
        thoughts.push({
          key: turn + ":" + i,
          text: st.text.replace(/^thinking:\s*/, ""),
        });
      }
    });
  }
  if (live) {
    thoughts.push({ key: "live", text: live, live: true });
  }
  return thoughts;
}

// ThinkDrawer is the model's reasoning, on the same retro screen as the terminal but orange. The
// reasoning does not go into the chat any more - the chat keeps the steps and the actions - so this
// is where it is written live and where it stays afterwards. Its tab sits above the terminal's.
export function ThinkDrawer() {
  const { rs, drawers } = useApp();
  const { thinkOpen, think } = drawers;
  const live = choose(rs.running, rs.liveThought, null);
  const thoughts = thoughtsOf(rs.stepsByTurn, live);
  return (
    <div class={`term-drawer is-think${choose(thinkOpen, " is-open", "")}`}>
      <button
        type="button"
        class={`term-tab${choose(live, " is-live", "")}`}
        aria-expanded={thinkOpen}
        aria-controls="think-panel"
        title={choose(
          thinkOpen,
          t("Close the thinking"),
          t("Open the thinking"),
        )}
        onClick={drawers.toggleThink}
      >
        <span class="term-tab-label">THINK</span>
        {thoughts.length > 0 && (
          <span class="term-tab-count">{thoughts.length}</span>
        )}
      </button>
      <section
        class="term-panel"
        id="think-panel"
        aria-label={t("thinking")}
        aria-hidden={!thinkOpen}
      >
        <div class="term-titlebar">
          <span>
            motita@mind — {plural(thoughts.length, "1 thought", "{n} thoughts")}
          </span>
          <button
            type="button"
            class="term-close"
            onClick={drawers.closeThink}
            aria-label={t("Close the thinking")}
            tabIndex={choose(thinkOpen, 0, -1)}
          >
            ×
          </button>
        </div>
        <div class="term-viewport">
          <div class="term-screen" ref={think.ref} {...think.handlers}>
            {thoughts.length === 0 && (
              <div class="term-empty">
                {t("no thoughts yet")}
                <span class="term-caret" />
              </div>
            )}
            {thoughts.map((th) => (
              <div
                key={th.key}
                class={`think-entry${choose(th.live, " is-live", "")}`}
              >
                <pre class="think-text">{th.text}</pre>
              </div>
            ))}
          </div>
          <CrtFx full />
        </div>
      </section>
    </div>
  );
}

// AgentDetail is a finished agent's summary, and the command that merges its branch. Its clicks
// stay inside it, so reading or copying does not fold the row.
function AgentDetail({ a }: { a: AgentInfo }) {
  const { drawers } = useApp();
  const ref = useRef<HTMLDivElement>(null);
  useStopClicks(ref);
  const merge = choose(a.branch, "git merge --no-edit " + a.branch, "");
  const canCopy = typeof document !== "undefined";
  return (
    <div class="agent-detail" ref={ref}>
      {!!a.summary && <pre class="agent-summary">{a.summary}</pre>}
      {!a.summary && <div class="agent-activity">{t("no summary")}</div>}
      {a.branch && (
        <div class="agent-branch">
          <code>{merge}</code>
          {canCopy && (
            <button
              type="button"
              class="agent-copy"
              onClick={() => drawers.copyMerge(a.id, merge)}
            >
              {choose(drawers.agentCopied === a.id, t("copied"), t("copy"))}
            </button>
          )}
        </div>
      )}
    </div>
  );
}

// AgentHead is an agent's purpose, with how long it has run and what it has cost.
function AgentHead({ a }: { a: AgentInfo }) {
  const { agentNow } = useApp().drawers;
  return (
    <div class="agent-head">
      <span class="agent-purpose" title={a.purpose}>
        {!a.parent && <span class="agent-tag">{t("main")}</span>}
        {a.purpose || a.id}
      </span>
      <span
        class="agent-meta"
        title={tokenBreakdown(a.tokens || { input: 0, output: 0 })}
      >
        {formatElapsed(agentElapsed(a, agentNow))} ·{" "}
        {formatTokens(tokenTotal(a.tokens))} tok
        {choose(a.round, " · r" + a.round, "")}
      </span>
    </div>
  );
}

// AgentRow is one agent: what it is for, how long it has run and what it has cost. A finished one
// unfolds its summary on a click.
function AgentRow({ a }: { a: AgentInfo }) {
  const { rs, drawers } = useApp();
  const done = a.state !== "running";
  const open = done && rs.agentOpenId === a.id;
  const mark = agentMark(a.state);
  return (
    <div class={`agent-row is-${a.state}${choose(done, " is-done", "")}`}>
      {done && (
        <button
          type="button"
          class="row-hit"
          aria-label={a.purpose || a.id}
          aria-expanded={open}
          onClick={() => drawers.toggleAgent(a.id)}
        />
      )}
      <span class="agent-state" aria-label={t(a.state)}>
        {mark ?? <span class="agent-spin" aria-hidden="true" />}
      </span>
      <div class="agent-body">
        <AgentHead a={a} />
        {a.activity && !open && (
          <div class="agent-activity" title={a.activity}>
            {a.activity}
          </div>
        )}
        {open && <AgentDetail a={a} />}
      </div>
    </div>
  );
}

// AgentsDrawer is the run's main agent and the background agents it started. It is not there at
// all until a run has something to show. Its tab sits below the terminal's.
export function AgentsDrawer() {
  const { rs, drawers } = useApp();
  const { agents } = rs;
  const { agentsOpen } = drawers;
  const subagents = agents.filter((a) => a.parent);
  const runningSubs = subagents.filter((a) => a.state === "running").length;
  if (subagents.length === 0 && !agents.some((a) => tokenTotal(a.tokens) > 0)) {
    return null;
  }
  const total = sumTokens(agents);
  return (
    <div class={`term-drawer is-agents${choose(agentsOpen, " is-open", "")}`}>
      <button
        type="button"
        class={`term-tab${choose(runningSubs > 0, " is-live", "")}`}
        aria-expanded={agentsOpen}
        aria-controls="agents-panel"
        title={choose(agentsOpen, t("Close the agents"), t("Open the agents"))}
        onClick={drawers.toggleAgents}
      >
        <span class="term-tab-label">AGENTS</span>
        {runningSubs > 0 && <span class="term-tab-count">{runningSubs}</span>}
      </button>
      <section
        class="term-panel"
        id="agents-panel"
        aria-label={t("agents")}
        aria-hidden={!agentsOpen}
      >
        <div class="term-titlebar">
          <span title={tokenBreakdown(total)}>
            motita@fleet — {plural(agents.length, "1 agent", "{n} agents")} ·{" "}
            {formatTokens(tokenTotal(total))} tok
          </span>
          <button
            type="button"
            class="term-close"
            onClick={drawers.closeAgents}
            aria-label={t("Close the agents")}
            tabIndex={choose(agentsOpen, 0, -1)}
          >
            ×
          </button>
        </div>
        <div class="term-viewport">
          <div class="term-screen">
            {agents.map((a) => (
              <AgentRow key={a.id} a={a} />
            ))}
          </div>
          <CrtFx full={false} />
        </div>
      </section>
    </div>
  );
}
