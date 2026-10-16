// The ⋯ menus of the conversation's inputs (going back to a checkpoint), the commands a person
// types into the shell, and the approval panel that answers the agent's questions.
// Run with: node --test web/src/__tests__/cov-chat-run.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import {
  act,
  fakeApi,
  fakeRunState,
  json,
  load,
  renderHook,
  tick,
} from "./cov-chat-helpers.mjs";

const { useCheckpoints, RUN_MENU } = await load("useCheckpoints.ts");
const { useShellApproval } = await load("useShellApproval.ts");

const badJson = (status) => new Response("not json", { status });

// ── Checkpoints ──────────────────────────────────────────────────────────────

async function checkpoints(
  route,
  { running = false, sid = "s1", switchTo } = {},
) {
  const api = fakeApi(route);
  const rs = fakeRunState();
  rs.sessionRef.current = sid;
  rs.runningRef.current = running;
  const toasts = [];
  const inputs = [];
  const switched = [];
  const conv = {
    switchSessionRef: {
      current:
        switchTo === null
          ? null
          : async (id, mode) => {
              switched.push([id, mode]);
            },
    },
  };
  const hook = await renderHook(() =>
    useCheckpoints({
      api,
      rs,
      conv,
      setInput: (t) => inputs.push(t),
      setToast: (t) => toasts.push(t),
    }),
  );
  return {
    hook,
    api,
    rs,
    toasts,
    inputs,
    switched,
    c: () => hook.result.current,
  };
}

test("a checkpoint menu opens, closes on a second press or a click elsewhere, and disarms", async () => {
  const { hook, c } = await checkpoints(() => json({}));
  assert.equal(RUN_MENU, -1);
  await act(() => c().toggleCpMenu(2));
  assert.equal(c().cpMenu, 2);
  await act(() => c().setCpArmed(true));
  await act(() => c().toggleCpMenu(2));
  assert.equal(c().cpMenu, null, "the same menu again closes it");
  assert.equal(c().cpArmed, false);
  await act(() => c().toggleCpMenu(3));
  // The click that opened it does not close it; a later one does.
  document.dispatchEvent(new window.MouseEvent("click"));
  assert.equal(c().cpMenu, 3);
  await act(() => tick(5));
  await act(() => c().setCpArmed(true));
  await act(() => document.dispatchEvent(new window.MouseEvent("click")));
  assert.equal(c().cpMenu, null);
  assert.equal(c().cpArmed, false);
  // Another menu replacing an open one, and an unmount while open, leave no listener behind.
  await act(() => c().toggleCpMenu(4));
  await act(() => c().setCpMenu(5));
  assert.equal(c().cpMenu, 5);
  await hook.unmount();
});

test("going back to a checkpoint restores it, refills the composer and says what was put back", async () => {
  const { hook, c, api, toasts, inputs, switched } = await checkpoints(
    (path, init) => {
      const { files } = JSON.parse(init.body);
      if (path.includes("/checkpoints/1/")) {
        return json({
          task: "first input",
          files_restored: files,
          undo_kept: files,
        });
      }
      return json({ files_restored: true, undo_kept: false });
    },
    { sid: "a b" },
  );
  await act(() => c().toggleCpMenu(1));
  await act(() => c().restoreCheckpoint(1, true));
  assert.equal(c().cpMenu, null);
  assert.deepEqual(
    api.calls.map((x) => [x.path, x.init.method, x.body]),
    [["/v1/sessions/a%20b/checkpoints/1/restore", "POST", { files: true }]],
  );
  assert.deepEqual(inputs, ["first input"]);
  assert.deepEqual(switched, [["a b", "none"]]);
  assert.deepEqual(toasts.pop(), {
    message: "Back at the checkpoint — conversation and files",
    type: "success",
    detail: "What the files were before is kept in git as refs/motita/undo/a b",
  });
  await act(() => c().restoreCheckpoint(1, false));
  assert.deepEqual(toasts.pop(), {
    message: "Back at the checkpoint — conversation only",
    type: "success",
    detail: undefined,
  });
  // No task kept: the composer is emptied. Files back but no undo ref: no detail.
  await act(() => c().restoreCheckpoint(2, true));
  assert.equal(inputs.at(-1), "");
  assert.equal(toasts.pop().detail, undefined);
  await hook.unmount();
});

test("a checkpoint that cannot be restored says so, and nothing happens during a run", async () => {
  let mode = "refuse";
  const { hook, c, toasts, inputs } = await checkpoints(() => {
    if (mode === "refuse") {
      return json({ error: "dirty tree" }, 409);
    }
    if (mode === "garbled") {
      return badJson(500);
    }
    throw new TypeError("offline");
  });
  await act(() => c().restoreCheckpoint(1, true));
  assert.deepEqual(toasts.pop(), {
    message: "Could not go back to that checkpoint",
    type: "error",
    detail: "dirty tree",
  });
  mode = "garbled";
  await act(() => c().restoreCheckpoint(1, true));
  assert.equal(
    toasts.pop().detail,
    undefined,
    "an unreadable answer has no detail",
  );
  mode = "offline";
  await act(() => c().restoreCheckpoint(1, true));
  assert.deepEqual(toasts.pop(), {
    message: "Could not go back to that checkpoint",
    type: "error",
  });
  assert.equal(inputs.length, 0);
  await hook.unmount();
  const busy = await checkpoints(() => json({}), { running: true });
  await act(() => busy.c().restoreCheckpoint(1, false));
  assert.equal(busy.api.calls.length, 0);
  await busy.hook.unmount();
});

test("an answer that arrives after the reader left the conversation is dropped", async () => {
  let s;
  const { hook, c, inputs, toasts, rs } = await checkpoints(async () => {
    s.rs.sessionRef.current = "elsewhere";
    return json({ task: "x" });
  });
  s = { rs };
  await act(() => c().restoreCheckpoint(1, false));
  assert.deepEqual(inputs, []);
  assert.deepEqual(toasts, []);
  await hook.unmount();
  // With no conversation loader wired, the restore still reports.
  const bare = await checkpoints(() => json({ task: "t" }), { switchTo: null });
  await act(() => bare.c().restoreCheckpoint(1, false));
  assert.equal(bare.toasts.pop().type, "success");
  await bare.hook.unmount();
});

// ── Shell lines and approvals ────────────────────────────────────────────────

async function shell(route, rsInit = {}) {
  const api = fakeApi(route);
  const rs = fakeRunState(rsInit);
  rs.sessionRef.current = "s1";
  const toasts = [];
  const states = [];
  let terms = 0;
  const lists = {
    sessions: [
      { id: "s1", auto_approve: false },
      { id: "s2", auto_approve: false },
    ],
    setSessions(fn) {
      lists.sessions = fn(lists.sessions);
    },
  };
  const deps = {
    api,
    rs,
    lists,
    drawers: { showTerm: () => (terms += 1) },
    setState: (...a) => states.push(a),
    setToast: (t) => toasts.push(t),
  };
  const hook = await renderHook(() => useShellApproval(deps));
  return {
    hook,
    api,
    rs,
    lists,
    toasts,
    states,
    terms: () => terms,
    c: () => hook.result.current,
    rerender: () => hook.rerender(),
  };
}

const log = (rs) => rs.shellLog.map((e) => [e.cmd, e.exit, e.out]);

test("a typed command runs in the shell and its result lands in the terminal", async () => {
  let reply;
  const s = await shell(() => reply());
  reply = () => json({ output: "a\nb", exit: 0 });
  await s.c().runShellLine("ls");
  assert.equal(s.terms(), 1, "the terminal opens");
  assert.deepEqual(s.api.calls[0].body, { command: "ls", approved: false });
  assert.equal(s.api.calls[0].path, "/v1/sessions/s1/shell");
  reply = () => json({ output: "partial", error: "warn", exit: 3 });
  await s.c().runShellLine("make");
  reply = () => json({ error: "no such command" });
  await s.c().runShellLine("nope");
  reply = () => json({});
  await s.c().runShellLine("true");
  assert.deepEqual(log(s.rs), [
    ["ls", 0, "a\nb"],
    ["make", 3, "partial\nwarn"],
    ["nope", -999, "no such command"],
    ["true", 0, ""],
  ]);
  // A refusal, an unreadable answer and a network failure.
  reply = () => json({ error: "forbidden" }, 403);
  await s.c().runShellLine("rm -rf /");
  assert.deepEqual(s.toasts.pop(), {
    message: "The command could not be run",
    type: "error",
    detail: "forbidden",
  });
  reply = () => badJson(500);
  await s.c().runShellLine("odd");
  assert.equal(s.toasts.pop().detail, undefined);
  reply = () => {
    throw new TypeError("offline");
  };
  await s.c().runShellLine("ping");
  assert.deepEqual(s.toasts.pop(), {
    message: "The command could not be run",
    type: "error",
  });
  assert.deepEqual(log(s.rs).slice(4), [
    ["rm -rf /", -999, "forbidden"],
    ["odd", -999, ""],
    ["ping", -999, ""],
  ]);
  await s.hook.unmount();
});

test("the terminal keeps the last 500 commands", async () => {
  const old = Array.from({ length: 500 }, (_, i) => ({
    id: 10_000 + i,
    cmd: "c" + i,
    exit: 0,
    out: "",
  }));
  const s = await shell(() => json({ output: "x" }), { shellLog: old });
  await s.c().runShellLine("new");
  assert.equal(s.rs.shellLog.length, 500);
  assert.equal(s.rs.shellLog[0].cmd, "c1");
  assert.equal(s.rs.shellLog.at(-1).cmd, "new");
  await s.hook.unmount();
});

test("a command the policy asks about waits for a yes, then runs approved", async () => {
  let reply = () =>
    json({ needs_approval: true, reason: "writes outside the workspace" });
  const s = await shell((path, init) => {
    if (path.endsWith("/auto-approve")) {
      return json({});
    }
    return reply(path, init);
  });
  await s.c().runShellLine("touch /x");
  assert.deepEqual(
    s.rs.shellLog,
    [],
    "nothing ran, nothing is in the terminal",
  );
  assert.equal(s.rs.approval.question, "writes outside the workspace");
  assert.equal(s.rs.approval.command, "touch /x");
  assert.match(s.rs.approval.id, /^shell:\d+$/);
  // A no: nothing runs.
  await s.c().answerApproval(s.rs.approval.id, false);
  assert.equal(s.rs.approval, null);
  assert.equal(s.api.calls.length, 1);
  // A second no, with the question already answered: still nothing.
  await s.c().answerApproval("shell:1", true);
  assert.equal(s.api.calls.length, 1);
  // A yes for every command: allowed for the session first, then run approved.
  reply = () => json({ needs_approval: true });
  await s.c().runShellLine("touch /y");
  assert.equal(s.rs.approval.question, "This needs your approval");
  reply = () => json({ output: "done" });
  await s.c().answerApproval(s.rs.approval.id, true, true);
  await tick();
  assert.deepEqual(
    s.api.calls.slice(2).map((c) => [c.path, c.body]),
    [
      ["/v1/sessions/s1/auto-approve", { enabled: true }],
      ["/v1/sessions/s1/shell", { command: "touch /y", approved: true }],
    ],
  );
  assert.equal(s.lists.sessions[0].auto_approve, true);
  assert.equal(s.lists.sessions[1].auto_approve, false);
  assert.deepEqual(log(s.rs), [["touch /y", 0, "done"]]);
  await s.hook.unmount();
});

test("a yes to a typed command still runs it when allowing all fails", async () => {
  let fail = "reject";
  const s = await shell((path) => {
    if (path.endsWith("/auto-approve")) {
      if (fail === "reject") {
        throw new TypeError("offline");
      }
      return json({}, 500);
    }
    if (path.endsWith("/shell") && s2.pending) {
      s2.pending = false;
      return json({ needs_approval: true });
    }
    return json({ output: "ok" });
  });
  const s2 = { pending: true };
  await s.c().runShellLine("a");
  await s.c().answerApproval(s.rs.approval.id, true, true);
  await tick();
  s2.pending = true;
  fail = "status";
  await s.c().runShellLine("b");
  await s.c().answerApproval(s.rs.approval.id, true, true);
  await tick();
  // A plain yes.
  s2.pending = true;
  await s.c().runShellLine("c");
  await s.c().answerApproval(s.rs.approval.id, true);
  await tick();
  assert.deepEqual(log(s.rs), [
    ["a", 0, "ok"],
    ["b", 0, "ok"],
    ["c", 0, "ok"],
  ]);
  assert.equal(s.lists.sessions[0].auto_approve, false, "never allowed");
  await s.hook.unmount();
});

test("the agent's questions are answered once, or for the whole session", async () => {
  let fail = false;
  let refuse = false;
  const s = await shell(() => {
    if (fail) {
      throw new TypeError("offline");
    }
    if (refuse) {
      return json({ error: "no question is waiting" }, 409);
    }
    return json({});
  });
  // A refused "allow every command" does not turn the switch on, and the user is told.
  refuse = true;
  await s.c().answerApproval("q0", true, true);
  assert.equal(s.lists.sessions[0].auto_approve, false);
  assert.deepEqual(s.states, [["could not answer the approval", true]]);
  assert.deepEqual(s.toasts, [
    { message: "The approval could not be answered", type: "error" },
  ]);
  s.states.length = 0;
  s.toasts.length = 0;
  s.api.calls.length = 0;
  refuse = false;
  await s.c().answerApproval("q1", true);
  await s.c().answerApproval("q2", false);
  await s.c().answerApproval("q3", true, true);
  assert.deepEqual(
    s.api.calls.map((c) => [c.path, c.body]),
    [
      ["/v1/sessions/s1/runs/approval", { id: "q1", approve: true }],
      ["/v1/sessions/s1/runs/approval", { id: "q2", approve: false }],
      [
        "/v1/sessions/s1/runs/approval",
        { id: "q3", approve: true, scope: "session" },
      ],
    ],
  );
  assert.equal(s.lists.sessions[0].auto_approve, true);
  fail = true;
  await s.c().answerApproval("q4", true);
  assert.deepEqual(s.states, [["could not answer the approval", true]]);
  await s.hook.unmount();
});

test("the approval panel takes focus, and the keyboard moves and answers it", async () => {
  const s = await shell(() => json({}));
  // No question: the keys are not the panel's.
  const e0 = {
    key: "Enter",
    preventDefault() {
      this.prevented = true;
    },
  };
  s.c().onApprovalKey(e0);
  assert.equal(e0.prevented, undefined);
  // A question arrives: the highlight resets to the first option and the panel is focused.
  const panel = document.createElement("fieldset");
  panel.tabIndex = 0;
  document.body.appendChild(panel);
  s.rs.approvalRef.current = panel;
  s.rs.approval = { id: "q1", question: "?" };
  s.rs.approvalSel = 2;
  await s.rerender();
  assert.equal(s.rs.approvalSel, 0);
  assert.equal(document.activeElement, panel);
  const press = async (k) => {
    const e = {
      key: k,
      prevented: false,
      preventDefault() {
        this.prevented = true;
      },
    };
    await act(() => s.c().onApprovalKey(e));
    await s.rerender();
    return e;
  };
  for (const [k, sel] of [
    ["ArrowRight", 1],
    ["ArrowDown", 2],
    ["ArrowDown", 0],
    ["ArrowLeft", 2],
    ["ArrowUp", 1],
  ]) {
    const e = await press(k);
    assert.equal(e.prevented, true);
    assert.equal(s.rs.approvalSel, sel, k);
  }
  assert.equal(
    (await press("x")).prevented,
    false,
    "any other key is the browser's",
  );
  // Enter on "allow all" answers yes for the session.
  await press("Enter");
  await tick();
  assert.deepEqual(s.api.calls.at(-1).body, {
    id: "q1",
    approve: true,
    scope: "session",
  });
  // Enter on "no".
  s.rs.approval = { id: "q2", question: "?" };
  s.rs.approvalRef.current = null;
  await s.rerender();
  s.rs.approvalSel = 2;
  await s.rerender();
  await press("Enter");
  await tick();
  assert.deepEqual(s.api.calls.at(-1).body, { id: "q2", approve: false });
  // Enter on "run".
  s.rs.approval = { id: "q3", question: "?" };
  await s.rerender();
  assert.equal(
    s.rs.approvalSel,
    0,
    "a new question starts on the first option",
  );
  await s.rerender();
  await press("Enter");
  await tick();
  assert.deepEqual(s.api.calls.at(-1).body, { id: "q3", approve: true });
  panel.remove();
  await s.hook.unmount();
});

test("allowing every command can be taken back, and says so when it cannot", async () => {
  let reply = () => json({});
  const s = await shell(() => reply());
  s.lists.sessions[0].auto_approve = true;
  await s.c().stopAutoApprove();
  assert.deepEqual(s.api.calls[0].body, { enabled: false });
  assert.equal(s.api.calls[0].path, "/v1/sessions/s1/auto-approve");
  assert.equal(s.lists.sessions[0].auto_approve, false);
  reply = () => json({}, 500);
  await s.c().stopAutoApprove();
  reply = () => {
    throw new TypeError("offline");
  };
  await s.c().stopAutoApprove();
  assert.deepEqual(s.states, [
    ["could not stop allowing all commands", true],
    ["could not stop allowing all commands", true],
  ]);
  await s.hook.unmount();
});
