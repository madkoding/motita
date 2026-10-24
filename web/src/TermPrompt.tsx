// The terminal drawer's own prompt: while nothing runs in the session, the TTY is a terminal the
// person types into, not only the history of what the agent ran.
//
// Each line goes where `!ls` from the composer goes (shell.runShellLine): the session's workspace,
// the policy and sandbox of Settings → Security, the approval panel when the policy would ask, and
// a lone `cd` kept for the next line. It is a line prompt and not a full PTY on purpose: the policy
// judges whole command lines, and keystrokes sent to a live shell would go past it.
//
// A command that asks for input needs a real terminal: the drawer's title bar opens one (TermPty),
// and the drawer becomes that terminal until it ends.
//
// While the agent works the prompt gives way: the gateway refuses a typed line during a run (both
// would write in the same workspace), so the line says so instead of offering what would fail.
import { useEffect, useRef, useState } from "preact/hooks";
import { t } from "./i18n";
import { choose } from "./choose";
import { useApp } from "./appStore";

// How the arrow keys walk the lines typed here: up to an older one, down to a newer one.
const HISTORY_STEP: Record<string, number> = { ArrowUp: -1, ArrowDown: 1 };

export function TermPrompt() {
  const { rs, drawers, shell, selectedSession } = useApp();
  const [line, setLine] = useState("");
  const [busy, setBusy] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);
  // The lines typed here, oldest first, and where the arrows are in them: at the end is the line
  // being written.
  const historyRef = useRef<string[]>([]);
  const posRef = useRef(0);
  // A finished line gives the focus back, so the next one can be typed straight away: the input
  // lost it while it was disabled.
  const refocusRef = useRef(false);
  useEffect(() => {
    if (!busy && refocusRef.current) {
      refocusRef.current = false;
      (inputRef.current as HTMLInputElement).focus();
    }
  }, [busy]);

  // No session to type into, or one already integrated into its branch: nothing to offer.
  if (rs.noSession || selectedSession?.merged) {
    return null;
  }
  if (rs.running) {
    return (
      <div class="term-line term-locked">
        {t(
          "the agent is working: the terminal is yours again when it finishes",
        )}
      </div>
    );
  }

  const submit = async (e: Event) => {
    e.preventDefault();
    const command = line.trim();
    if (!command) {
      return;
    }
    const hist = historyRef.current;
    if (hist[hist.length - 1] !== command) {
      hist.push(command);
    }
    posRef.current = hist.length;
    setLine("");
    setBusy(true);
    refocusRef.current = true;
    drawers.stickTerm();
    await shell.runShellLine(command);
    setBusy(false);
  };

  const onKey = (e: KeyboardEvent) => {
    const step = HISTORY_STEP[e.key];
    if (step === undefined) {
      return;
    }
    const hist = historyRef.current;
    const next = Math.min(hist.length, Math.max(0, posRef.current + step));
    e.preventDefault();
    posRef.current = next;
    setLine(hist[next] ?? "");
  };

  return (
    <form class="term-line term-input-line" onSubmit={submit}>
      <span class="term-prompt" aria-hidden="true">
        $
      </span>
      <input
        ref={inputRef}
        class="term-input"
        type="text"
        value={line}
        onInput={(e) => setLine((e.target as HTMLInputElement).value)}
        onKeyDown={onKey}
        disabled={busy}
        placeholder={choose(busy, t("running…"), t("type a command"))}
        aria-label={t("Command to run in the session's folder")}
        autocomplete="off"
        autocapitalize="off"
        spellcheck={false}
        enterKeyHint="send"
        tabIndex={choose(drawers.termOpen, 0, -1)}
      />
    </form>
  );
}
