// The commands a person types (`!ls`, `/shell ls`) and the approval panel: the question the agent
// or a typed command is waiting on, and the "allow every command" switch of a session.
import { useCallback, useEffect, useRef } from "preact/hooks";
import { t } from "./i18n";
import { choose } from "./choose";
import { withSession, type SessionList } from "./useSessionList";
import type { RunState } from "./useRunState";
import type { Drawers } from "./useDrawers";
import type { Api } from "./prApi";
import type { SetStatus, SetToast } from "./appTypes";
import {
  APPROVAL_ALL,
  APPROVAL_NO,
  APPROVAL_OPTIONS,
  JSON_HEADERS,
  NEEDS_APPROVAL,
  NO_RESULT,
  SESSIONS_PATH,
  SHELL_LOG_MAX,
} from "./appConst";

interface Deps {
  api: Api;
  rs: RunState;
  lists: SessionList;
  drawers: Drawers;
  setState: SetStatus;
  setToast: SetToast;
}

// approvalMove is how far an arrow key moves the highlighted option, or null for any other key.
function approvalMove(key: string): number | null {
  if (key === "ArrowRight" || key === "ArrowDown") {
    return 1;
  }
  if (key === "ArrowLeft" || key === "ArrowUp") {
    return APPROVAL_OPTIONS - 1;
  }
  return null;
}

export function useShellApproval({
  api,
  rs,
  lists,
  drawers,
  setState,
  setToast,
}: Deps) {
  const {
    sessionRef,
    nextId,
    setShellLog,
    setApproval,
    approval,
    approvalSel,
    setApprovalSel,
    approvalRef,
  } = rs;
  const { setSessions } = lists;

  // runShellLine runs a command the person typed (`!ls`, `/shell ls`) in the session's workspace,
  // under the same policy as the agent's commands. Its output goes to the terminal drawer, where
  // the agent's commands are. When the policy would ask, the question is the approval panel's,
  // and the answer comes back through answerApproval with a "shell:" id.
  const shellPendingRef = useRef<string | null>(null);
  // `always` keeps commands of this shape allowed in the project, as the panel's second answer
  // does for the agent's own commands.
  const runShellLine = useCallback(
    async (command: string, approved = false, always = false) => {
      const sid = sessionRef.current;
      const entryId = nextId();
      setShellLog((prev) =>
        [...prev, { id: entryId, cmd: command, exit: null, out: "" }].slice(
          -SHELL_LOG_MAX,
        ),
      );
      drawers.showTerm();
      const settle = (exit: number, out: string) =>
        setShellLog((prev) =>
          prev.map((e) => {
            if (e.id === entryId) {
              return { ...e, exit, out };
            }
            return e;
          }),
        );
      try {
        const res = await api(
          SESSIONS_PATH + encodeURIComponent(sid) + "/shell",
          {
            method: "POST",
            headers: JSON_HEADERS,
            body: JSON.stringify(
              choose(always, { command, approved, scope: "project" }, { command, approved }),
            ),
          },
        );
        const body = await res.json().catch(() => ({}));
        if (!res.ok) {
          settle(NO_RESULT, body.error ?? "");
          setToast({
            message: t("The command could not be run"),
            type: "error",
            detail: body.error,
          });
          return;
        }
        if (body.needs_approval) {
          // Nothing ran: the line leaves the terminal and waits for the person's yes.
          setShellLog((prev) => prev.filter((e) => e.id !== entryId));
          shellPendingRef.current = command;
          setApproval({
            id: "shell:" + entryId,
            question: body.reason || t(NEEDS_APPROVAL),
            command,
          });
          return;
        }
        const out = [body.output, body.error].filter(Boolean).join("\n");
        settle(
          choose(body.error && !body.output, NO_RESULT, body.exit ?? 0),
          out,
        );
      } catch {
        settle(NO_RESULT, "");
        setToast({ message: t("The command could not be run"), type: "error" });
      }
    },
    [],
  );

  // answerShell answers a typed command's question: it has no run to answer, so it is asked again
  // with the yes in hand.
  const answerShell = (approve: boolean, always: boolean) => {
    const command = shellPendingRef.current;
    shellPendingRef.current = null;
    if (!approve || !command) {
      return;
    }
    void runShellLine(command, true, always);
  };

  // answerApproval sends the approval response.
  //
  // `always` is "always allow this here": the gateway keeps the shape of the command (its
  // rule and program) allowed in the project, so later sessions are not asked about it either.
  const answerApproval = useCallback(
    async (id: string, approve: boolean, always = false) => {
      setApproval(null);
      if (id.startsWith("shell:")) {
        answerShell(approve, always);
        return;
      }
      // The session is pinned now: the answer is for the question asked in it.
      const sid = sessionRef.current;
      try {
        const res = await api(SESSIONS_PATH + sid + "/runs/approval", {
          method: "POST",
          headers: JSON_HEADERS,
          body: JSON.stringify(
            choose(always, { id, approve, scope: "project" }, { id, approve }),
          ),
        });
        // A refusal (the question was already answered, or the run is gone) is a failure too:
        // the panel is closed, so the user must hear that the answer did not land.
        if (!res.ok) {
          throw new Error(String(res.status));
        }
      } catch {
        setState("could not answer the approval", true);
        setToast({
          message: t("The approval could not be answered"),
          type: "error",
        });
      }
    },
    [runShellLine],
  );

  const approvalId = approval?.id;
  useEffect(() => {
    if (!approvalId) {
      return;
    }
    setApprovalSel(0);
    approvalRef.current?.focus();
  }, [approvalId]);

  // onApprovalKey: arrows move between the options, Enter takes the highlighted one.
  const onApprovalKey = (e: KeyboardEvent) => {
    if (!approval) {
      return;
    }
    const move = approvalMove(e.key);
    if (move !== null) {
      e.preventDefault();
      setApprovalSel((i) => (i + move) % APPROVAL_OPTIONS);
    } else if (e.key === "Enter") {
      e.preventDefault();
      answerApproval(
        approval.id,
        approvalSel !== APPROVAL_NO,
        approvalSel === APPROVAL_ALL,
      );
    } else {
      // Any other key is not the approval's: the browser handles it.
    }
  };

  // stopAutoApprove takes back "allow all commands": the next command is asked again.
  const stopAutoApprove = useCallback(async () => {
    const sid = sessionRef.current;
    try {
      const res = await api(SESSIONS_PATH + sid + "/auto-approve", {
        method: "POST",
        headers: JSON_HEADERS,
        body: JSON.stringify({ enabled: false }),
      });
      if (!res.ok) {
        throw new Error("HTTP " + res.status);
      }
      setSessions((prev) => withSession(prev, sid, { auto_approve: false }));
    } catch {
      setState("could not stop allowing all commands", true);
    }
  }, []);

  return { runShellLine, answerApproval, onApprovalKey, stopAutoApprove };
}
