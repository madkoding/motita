// What each event of a run's stream does to the conversation on screen, applied to the real run
// state (useRunState) the page keeps; and the finished-elsewhere and model-switch notices.
import test from "node:test";
import assert from "node:assert/strict";
import { installDom, source, renderHook } from "./cov-stream-helpers.mjs";

installDom();
const { useRunState, keyed } = await source("useRunState.ts");
const { handleStreamEvent, withoutDraft } = await source("streamEvents.ts");
const watch = await source("sessionWatch.ts");

// run mounts the run state and returns a way to send it events.
async function run() {
  const hook = await renderHook(() => useRunState());
  let finished = 0;
  const rs = () => hook.result.current;
  const send = (name, payload) =>
    hook.act(() =>
      handleStreamEvent({ rs: rs(), finish: () => finished++ }, name, payload),
    );
  return { hook, rs, send, finished: () => finished };
}

test("keyed numbers the steps as they arrive", () => {
  const a = keyed({ kind: "step", text: "a" });
  const b = keyed({ kind: "step", text: "b" });
  assert.equal(b.key, a.key + 1);
  assert.equal(a.text, "a");
});

test("the run state starts empty and clearConversation empties it again", async () => {
  const { hook, rs } = await run();
  assert.deepEqual(rs().messages, []);
  assert.equal(rs().running, false);
  assert.equal(rs().sessionId, "");
  assert.equal(rs().nextId(), 1);
  assert.equal(rs().nextId(), 2);
  await hook.act(() => {
    rs().setMessages([{ id: 1, role: "user", text: "hi" }]);
    rs().setActivity("x");
    rs().setStepsByTurn({ 1: [] });
    rs().setCpInfo({ 1: {} });
    rs().setShellLog([{ id: 1 }]);
    rs().setTermSeenId(4);
    rs().setLiveThought("hm");
    rs().setApproval({ id: "a" });
    rs().setAgents([{ id: "a" }]);
    rs().setAgentOpenId("a");
    rs().liveTurnRef.current = 3;
  });
  assert.equal(rs().messages.length, 1);
  await hook.act(() => rs().clearConversation());
  const s = rs();
  assert.deepEqual(
    [s.messages, s.activity, s.stepsByTurn, s.cpInfo, s.shellLog, s.termSeenId],
    [[], null, {}, {}, [], 0],
  );
  assert.deepEqual([s.liveThought, s.approval, s.agents, s.agentOpenId], [null, null, [], null]);
  assert.equal(s.liveTurnRef.current, null);
  await hook.unmount();
});

test("attached marks the user's message with its turn and resets a replayed turn's steps", async () => {
  const { hook, rs, send } = await run();
  await hook.act(() => {
    rs().setMessages([
      { id: 1, role: "user", text: "old", turn: 1 },
      { id: 2, role: "user", text: "new" },
      { id: 3, role: "agent", text: "..." },
    ]);
    rs().setStepsByTurn({ 2: [{ kind: "step", text: "loaded" }] });
  });
  await send("attached", {
    turn: 2,
    dropped: 3,
    pending_approval: { id: "ap", command: "rm" },
    agents: [
      { id: "bg", parent: "main", started: "2024-01-01T00:00:00Z" },
      { id: "main", started: "2024-01-01T00:00:01Z" },
    ],
  });
  const s = rs();
  assert.equal(s.liveTurnRef.current, 2);
  assert.equal(s.messages[1].turn, 2);
  assert.equal(s.messages[0].turn, 1);
  assert.deepEqual(s.stepsByTurn[2], []);
  assert.equal(s.messages[3].kind, "kind");
  assert.equal(
    s.messages[3].text,
    "(3 event(s) were not kept while nothing was listening)",
  );
  assert.equal(s.approval.id, "ap");
  assert.deepEqual(s.agents.map((a) => a.id), ["main", "bg"]);

  // A reconnection (events already read) keeps the steps; a turn already marked stays as it is.
  await hook.act(() => {
    rs().lastIdRef.current = 5;
    rs().setStepsByTurn({ 2: [{ kind: "step", text: "kept" }] });
  });
  const before = rs().messages;
  await send("attached", { turn: 2 });
  assert.equal(rs().stepsByTurn[2][0].text, "kept");
  assert.equal(rs().messages, before, "nothing to mark: the list is unchanged");
  assert.equal(rs().messages.length, 4, "no dropped-events line");

  // No user message at all: nothing is marked; no turn: nothing changes.
  await hook.act(() => rs().setMessages([{ id: 9, role: "agent", text: "a" }]));
  await send("attached", { turn: 7 });
  assert.equal(rs().messages[0].turn, undefined);
  await send("attached", { dropped: 0 });
  assert.equal(rs().liveTurnRef.current, 7);
  await hook.unmount();
});

test("agents and thinking snapshots replace the ones before them", async () => {
  const { hook, rs, send } = await run();
  await send("agents", { agents: [{ id: "a", started: "x" }] });
  assert.deepEqual(rs().agents.map((a) => a.id), ["a"]);
  await send("agents", { agents: "nonsense" });
  assert.deepEqual(rs().agents, []);
  await send("thinking", { text: "pondering" });
  assert.equal(rs().liveThought, "pondering");
  await send("thinking", {});
  assert.equal(rs().liveThought, null);
  // An event this page does not know is ignored.
  await send("future-event", { text: "x" });
  assert.equal(rs().messages.length, 0);
  await hook.unmount();
});

test("progress lines become the turn's steps, its commands and the terminal log", async () => {
  const { hook, rs, send } = await run();
  // Before the gateway names the turn, a line only moves the activity.
  await send("progress", { text: "planning..." });
  assert.equal(rs().activity, "planning...");
  assert.equal(rs().activityKind, "plan");
  assert.deepEqual(rs().stepsByTurn, {});

  await hook.act(() => {
    rs().liveTurnRef.current = 1;
    rs().setLiveThought("live");
  });
  await send("progress", { text: "thinking: the plan is sound" });
  assert.equal(rs().activity, "planning...", "a thought does not change the activity");
  assert.equal(rs().liveThought, null);
  await send("progress", { text: "running: ls -la" });
  await send("progress", { text: "some phase" });
  await send("progress", {});
  let steps = rs().stepsByTurn[1];
  assert.deepEqual(
    steps.map((s) => [s.kind, s.text, s.exit]),
    [
      ["thought", "thinking: the plan is sound", undefined],
      ["command", "running: ls -la", null],
      ["step", "some phase", undefined],
    ],
  );
  assert.equal(rs().activity, "", "an empty line is the activity too");
  assert.deepEqual(
    rs().shellLog.map((e) => [e.cmd, e.exit, e.out]),
    [["ls -la", null, ""]],
  );

  // The output lands on the command it belongs to, in the trail and in the terminal.
  await send("progress", { text: "output (exit 2):\nno such file" });
  steps = rs().stepsByTurn[1];
  assert.deepEqual([steps[1].exit, steps[1].out], [2, "no such file"]);
  assert.deepEqual(
    rs().shellLog.map((e) => [e.exit, e.out]),
    [[2, "no such file"]],
  );
  // Output with no command waiting for it changes nothing.
  const trail = rs().stepsByTurn;
  const log = rs().shellLog;
  await send("progress", { text: "output (exit 0):\nagain" });
  assert.equal(rs().stepsByTurn, trail);
  assert.equal(rs().shellLog, log);
  // An output line whose code cannot be read counts as a success.
  await send("progress", { text: "running: make" });
  await send("progress", { text: "output (exit ?)" });
  assert.deepEqual(
    rs().shellLog.map((e) => [e.cmd, e.exit, e.out]),
    [
      ["ls -la", 2, "no such file"],
      ["make", 0, "output (exit ?)"],
    ],
  );

  // A turn with no steps yet takes its first one, and settles nothing on output.
  await hook.act(() => {
    rs().liveTurnRef.current = 2;
  });
  await send("progress", { text: "output (exit 1):\nx" });
  assert.equal(rs().stepsByTurn[2], undefined);
  await send("progress", { text: "first" });
  assert.equal(rs().stepsByTurn[2][0].text, "first");

  // Output with no turn named only reaches the terminal.
  await hook.act(() => {
    rs().liveTurnRef.current = null;
  });
  await send("progress", { text: "running: pwd" });
  await send("progress", { text: "output (exit 0):\n/tmp" });
  assert.deepEqual(rs().shellLog.at(-1), {
    ...rs().shellLog.at(-1),
    cmd: "pwd",
    exit: 0,
    out: "/tmp",
  });

  // The agent list as a plain line is a snapshot, never a step.
  await send("progress", { text: 'agents: [{"id":"z","started":"x"}]' });
  assert.deepEqual(rs().agents.map((a) => a.id), ["z"]);
  await send("progress", { text: "agents: not json" });
  assert.deepEqual(rs().agents.map((a) => a.id), ["z"]);
  await hook.unmount();
});

test("the answer grows as a draft and the final one takes its place", async () => {
  const { hook, rs, send, finished } = await run();
  await hook.act(() => {
    rs().setMessages([{ id: rs().nextId(), role: "user", text: "q" }]);
  });
  await send("answer", { text: "Hel" });
  const draftId = rs().messages[1].id;
  assert.deepEqual(rs().messages[1], {
    id: draftId,
    role: "agent",
    text: "Hel",
    turn: undefined,
    draft: true,
  });
  await hook.act(() => {
    rs().liveTurnRef.current = 4;
  });
  await send("answer", { text: "Hello" });
  assert.equal(rs().messages.length, 2);
  assert.equal(rs().messages[1].id, draftId, "the same message keeps growing");
  assert.equal(rs().messages[1].turn, 4);
  await send("answer", {});
  assert.equal(rs().messages.length, 1, "an empty snapshot clears the draft");

  await send("answer", { text: "Hello" });
  const id2 = rs().messages[1].id;
  await send("done", {
    result: "Hello world",
    changes: { files: [] },
    artifacts: [{ name: "a.txt", type: "text/plain", size: 1 }],
    report: { risks: [{ risk: "x" }] },
  });
  const done = rs().messages[1];
  assert.equal(done.id, id2);
  assert.equal(done.text, "Hello world");
  assert.equal(done.draft, undefined);
  assert.equal(done.turn, 4);
  assert.deepEqual(done.changes, { files: [] });
  assert.equal(done.artifacts.length, 1);
  assert.deepEqual(done.report, { risks: [{ risk: "x" }] });
  assert.equal(finished(), 1);
  assert.equal(rs().activity, null);

  // Without a draft the answer is a new message; an empty list of artifacts and an empty report
  // are not shown.
  await hook.act(() => {
    rs().liveTurnRef.current = null;
  });
  await send("done", { result: "Again", artifacts: [], report: {} });
  const again = rs().messages[2];
  assert.ok(again.id > id2);
  assert.deepEqual(
    [again.text, again.turn, again.artifacts, again.report],
    ["Again", undefined, undefined, undefined],
  );
  // A run that ends with no answer only drops the draft.
  await send("answer", { text: "half" });
  await send("done", {});
  assert.equal(rs().messages.length, 3);
  assert.equal(finished(), 3);
  await hook.unmount();
});

test("an answer that asked carries its questions and the request they are about", async () => {
  const { hook, rs, send } = await run();
  const questions = [{ text: "which?", options: ["a"] }];
  await send("done", { result: "which?", questions });
  assert.deepEqual([rs().messages[0].questions, rs().messages[0].origin], [questions, ""]);
  await hook.act(() => {
    rs().setMessages([
      { id: 1, role: "user", text: "first" },
      { id: 2, role: "user", text: "add login" },
    ]);
  });
  await send("done", { result: "which?", questions });
  assert.equal(rs().messages[2].origin, "add login", "the newest input is the request asked about");
  // Typed with a mode, the request is what the gateway kept for the turn, as a reload reads it.
  await hook.act(() => {
    rs().setMessages([{ id: 1, role: "user", text: "  /task add login " }]);
  });
  await send("done", { result: "which?", questions });
  assert.equal(rs().messages[1].origin, "add login");
  await send("done", { result: "ok", questions: [] });
  assert.equal(rs().messages[3].questions, undefined, "an empty list asks nothing");
  assert.equal(rs().messages[3].origin, undefined);
  await hook.unmount();
});

test("an error ends the run with its message, and an approval asks the question", async () => {
  const { hook, rs, send, finished } = await run();
  await send("answer", { text: "partial" });
  await send("error", { error: "model unavailable" });
  assert.deepEqual(
    rs().messages.map((m) => [m.role, m.kind, m.text]),
    [["agent", "error", "model unavailable"]],
  );
  await send("answer", { text: "partial" });
  await send("error", {});
  assert.equal(rs().messages.length, 1);
  assert.equal(finished(), 2);
  await hook.act(() => rs().setActivity("running"));
  await send("approval", { id: "q1", command: "rm -rf build" });
  assert.equal(rs().activity, null);
  assert.deepEqual(rs().approval, { id: "q1", command: "rm -rf build" });
  await hook.unmount();
});

test("an approval refused with nobody to ask is a warning in the conversation; the run goes on", async () => {
  const { hook, rs, send, finished } = await run();
  await send("approval_denied", {
    command: "rm -rf build",
    reason: "deletes files",
    rule: "destructive",
    error: "set agent.policy.enforce=false to run such actions unattended",
  });
  // Without a command the reason says what was refused; without an error there is no detail.
  await send("approval_denied", { reason: "writes outside the project" });
  await send("approval_denied", {});
  assert.deepEqual(
    rs().messages.map((m) => [m.role, m.kind, m.text]),
    [
      [
        "agent",
        "warning",
        "Refused, nobody was there to approve it: rm -rf build — " +
          "set agent.policy.enforce=false to run such actions unattended",
      ],
      ["agent", "warning", "Refused, nobody was there to approve it: writes outside the project"],
      ["agent", "warning", "Refused, nobody was there to approve it: "],
    ],
  );
  assert.equal(finished(), 0, "the run is not over");
  await hook.unmount();
});

test("withoutDraft drops only a trailing draft", () => {
  const list = [{ id: 1 }, { id: 2, draft: true }];
  assert.deepEqual(withoutDraft(list), [{ id: 1 }]);
  const clean = [{ id: 1 }];
  assert.equal(withoutDraft(clean), clean);
  const empty = [];
  assert.equal(withoutDraft(empty), empty);
});

test("a session that finished while another was on screen is announced", async () => {
  const toasts = [];
  const a = { id: "a", title: "Alpha", running: true };
  const b = { id: "b", title: "", running: true };
  const c = { id: "c", title: "Gamma", running: true };
  const hook = await renderHook(
    (p) => watch.useFinishedElsewhere(p.sessions, p.onScreen, (t) => toasts.push(t)),
    { sessions: [a, b, c], onScreen: "c" },
  );
  assert.deepEqual(toasts, [], "the first list announces nothing");
  await hook.rerender({
    sessions: [{ ...a, running: false }, b, { ...c, running: false }],
    onScreen: "c",
  });
  assert.deepEqual(toasts, [{ message: "“Alpha” finished", type: "success" }]);
  await hook.rerender({
    sessions: [{ ...a, running: false }, { ...b, running: false }],
    onScreen: "c",
  });
  assert.equal(toasts.length, 2);
  assert.equal(toasts[1].message, "“b” finished");
  await hook.unmount();

  assert.equal(watch.finishedToast([]), null);
  assert.deepEqual(
    watch.finishedToast([
      { id: "x", title: "X" },
      { id: "y", title: "New session" },
    ]),
    { message: "2 sessions finished", type: "success", detail: "X\nNew session" },
  );
  assert.deepEqual(
    watch
      .finishedElsewhere(
        new Map([
          ["x", true],
          ["y", false],
          ["gone", true],
        ]),
        [
          { id: "x", running: false },
          { id: "y", running: false },
          { id: "z", running: false },
        ],
        "",
      )
      .map((s) => s.id),
    ["x"],
  );
});

test("a model switch warns about the history and the turn in progress", () => {
  assert.equal(watch.modelChangeToast({ model: "m2" }), null);
  assert.equal(watch.modelChangeToast({ model: "m2", warnings: [] }), null);
  assert.deepEqual(
    watch.modelChangeToast({
      model: "m2",
      previous_model: "m1",
      warnings: ["history", "running"],
    }),
    {
      message: "Model switched to m2 in this session",
      type: "warning",
      detail:
        "The conversation so far was written with m1; m2 takes it over from the next turn and may answer differently. Open a new session to keep each model on its own.\nThe turn in progress finishes with the previous model.",
    },
  );
  assert.match(
    watch.modelChangeToast({ model: "m2", warnings: ["history"] }).detail,
    /written with \?;/,
  );
  assert.equal(
    watch.modelChangeToast({ model: "m2", warnings: ["other"] }).detail,
    "",
  );
  assert.equal(watch.switchWarning(false, true, true), "");
  assert.match(watch.switchWarning(true, true, true), /^A turn is running/);
  assert.match(watch.switchWarning(true, true, false), /^This conversation already/);
  assert.equal(watch.switchWarning(true, false, false), "");
});
