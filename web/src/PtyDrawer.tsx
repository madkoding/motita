// PtyDrawer is the interactive terminal: a real shell on the session's folder, for the commands
// that ask for input, in a drawer and a tab of its own beside the TTY (the history and its line
// prompt). Opening the drawer starts the shell when none is running and the agent is idle; the
// shell lives on while the drawer is closed (closing a window is not hanging up its shell) and
// ends with `exit`, the end button or a change of session.
import { useCallback, useEffect, useState } from "preact/hooks";
import { t } from "./i18n";
import { choose } from "./choose";
import { useApp } from "./appStore";
import { TermPty } from "./TermPty";
import { CrtFx } from "./Drawers";

export function PtyDrawer() {
  const { rs, drawers, machine, status, selectedSession } = useApp();
  const { ptyOpen } = drawers;
  const sid = rs.sessionId;
  const [live, setLive] = useState(false);
  // A terminal needs a session that can still be worked in, and waits while the agent works.
  const usable = !rs.noSession && !selectedSession?.merged;
  const canStart = usable && !rs.running;
  const start = useCallback(() => setLive(true), []);
  // Another session is another shell: the open one ends with the session it belonged to.
  useEffect(() => setLive(false), [sid, machine]);
  // Opening the drawer is asking for the terminal: it starts when it can.
  useEffect(() => {
    if (ptyOpen && canStart) {
      setLive(true);
    }
  }, [ptyOpen]);
  const end = useCallback((reason?: string) => {
    setLive(false);
    if (reason) {
      status.setToast({
        message: t("The terminal could not be opened"),
        type: "error",
        detail: reason,
      });
    }
  }, []);
  const tab = choose(ptyOpen, 0, -1);
  return (
    <div class={`term-drawer is-pty${choose(ptyOpen, " is-open", "")}`}>
      <button
        type="button"
        class={`term-tab${choose(live, " is-live", "")}`}
        aria-expanded={ptyOpen}
        aria-controls="pty-panel"
        title={choose(
          ptyOpen,
          t("Close the interactive terminal"),
          t("Open an interactive terminal, for commands that ask for input"),
        )}
        onClick={drawers.togglePty}
      >
        <span class="term-tab-label">PTY</span>
      </button>
      <section
        class="term-panel"
        id="pty-panel"
        aria-label={t("interactive terminal")}
        aria-hidden={!ptyOpen}
      >
        <div class="term-titlebar">
          <span>motita@pty — {t("interactive")}</span>
          {live && (
            <button
              type="button"
              class="term-pty-end"
              onClick={() => end()}
              title={t("End the interactive terminal")}
              tabIndex={tab}
            >
              {t("end")}
            </button>
          )}
          <button
            type="button"
            class="term-close"
            onClick={drawers.closePty}
            aria-label={t("Close the interactive terminal")}
            tabIndex={tab}
          >
            ×
          </button>
        </div>
        <div class="term-viewport">
          {live && (
            <TermPty machine={machine} sid={sid} open={ptyOpen} onEnd={end} />
          )}
          {!live && (
            <PtyIdle
              usable={usable}
              canStart={canStart}
              start={start}
              tab={tab}
            />
          )}
          <CrtFx full />
        </div>
      </section>
    </div>
  );
}

// PtyIdle is the drawer with no shell: the way to start one, or why it cannot start yet.
function PtyIdle({
  usable,
  canStart,
  start,
  tab,
}: {
  usable: boolean;
  canStart: boolean;
  start: () => void;
  tab: number;
}) {
  if (!usable) {
    return (
      <div class="term-screen">
        <div class="term-line term-locked">
          {t("open a session to use the interactive terminal")}
        </div>
      </div>
    );
  }
  return (
    <div class="term-screen">
      {!canStart && (
        <div class="term-line term-locked">
          {t(
            "the agent is working: the terminal is yours again when it finishes",
          )}
        </div>
      )}
      <button
        type="button"
        class="term-pty-open"
        onClick={start}
        disabled={!canStart}
        tabIndex={tab}
      >
        {"▸ " + t("start the interactive terminal")}
      </button>
    </div>
  );
}
