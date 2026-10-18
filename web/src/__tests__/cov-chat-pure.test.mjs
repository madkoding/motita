// The chat's pure rules: answering a report's suggestions, who owns a scroll, the smooth follower
// of the end, the transcript drawn when a conversation is opened again, and the turn view.
// Run with: node --test web/src/__tests__/cov-chat-pure.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { copies, frames, renderHook, mount, h } from "./cov-chat-helpers.mjs";

const proposalCopies = await copies("proposals", [
  ["initialVerdicts, proposalRequest, answersOf, isApplying", "proposals.ts"],
]);
const intentCopies = await copies("intent", [
  ["newIntent, readerActs, scrolled, scrollKey", "scrollIntent.ts"],
]);
const followerCopies = (
  await copies("follower", [["createFollower", "smoothscroll.ts"]])
).map((m) => m.createFollower);
const transcriptCopies = await copies("transcript", [
  ["transcriptMessages", "transcript.ts"],
]);
const turnCopies = await copies("turn", [
  ["useTurnView, useTurn, lastUserIdOf, sameTurn, TurnCtx", "turnView.ts"],
]);

test("initialVerdicts keeps the answers or accepts the needed ones in auto mode", () => {
  for (const M of proposalCopies) {
    const answered = ["skip", "apply"];
    assert.equal(M.initialVerdicts([{}, {}], true, answered), answered);
    assert.deepEqual(M.initialVerdicts([{}, {}], false, undefined, true), [
      "apply",
      "apply",
    ]);
    assert.deepEqual(M.initialVerdicts([{ needed: true }, {}], true), [
      "apply",
      undefined,
    ]);
    assert.deepEqual(M.initialVerdicts([{ needed: true }, {}], false), [
      undefined,
      undefined,
    ]);
  }
});

test("proposalRequest lists the accepted suggestions under the lead, or nothing", () => {
  for (const M of proposalCopies) {
    const s = [
      { title: "a", detail: "do a" },
      { title: "b" },
      { detail: "untitled" },
      { title: "skipped" },
    ];
    assert.equal(
      M.proposalRequest("  Apply these:  ", s, [
        "apply",
        "apply",
        "apply",
        "skip",
      ]),
      "Apply these:\n- a: do a\n- b\n- : untitled",
    );
    assert.equal(M.proposalRequest("Apply", s, ["skip", undefined]), "");
  }
});

test("answersOf keeps a button for every suggestion, and isApplying recognises the applying turn", () => {
  for (const M of proposalCopies) {
    const s = [{ title: "a", detail: "do a" }, { title: "b" }, { title: "c" }];
    const answers = M.answersOf("Apply:", s, [true, false, true]);
    assert.deepEqual(answers, {
      picks: ["this", "none", "this"],
      verdicts: ["apply", "skip", "apply"],
      request: "Apply:\n- a: do a\n- c",
    });
    const none = M.answersOf("Apply:", s, [false, false, false]);
    assert.deepEqual(none.picks, ["none", "none", "none"]);
    assert.equal(none.request, "");
    assert.equal(M.isApplying(answers, "Apply:\n- a: do a\n- c"), true);
    assert.equal(M.isApplying(answers, "something else"), false);
    assert.equal(M.isApplying({ picks: [], verdicts: [] }, ""), false);
    assert.equal(M.isApplying(undefined, "do it"), false);
  }
});

test("scroll intent: every rule", () => {
  for (const M of intentCopies) {
    const s = M.newIntent();
    assert.deepEqual(
      { pinned: s.pinned, holding: s.holding, lastTop: s.lastTop },
      { pinned: true, holding: false, lastTop: 0 },
    );
    // A wheel up with room to move lets go at once; a move down does not.
    M.readerActs(s, 1000, false, 500);
    assert.equal(s.pinned, true);
    M.readerActs(s, 1000, true, 500);
    assert.equal(s.pinned, false);
    M.readerActs(s, 1000, true, 0);
    assert.equal(s.userAt, 1000);
    // Content moving long after the reader acted changes nothing, except landing on the end.
    const c = M.newIntent(100);
    c.pinned = false;
    M.scrolled(c, 50, 500, 99999, 120);
    assert.equal(c.pinned, false);
    M.scrolled(c, 50, 1, 99999, 120);
    assert.equal(c.pinned, true);
    // The reader's own moves: up unpins, down near the end pins, down far away keeps.
    const r = M.newIntent(1000);
    r.holding = true;
    M.scrolled(r, 900, 300, 0, 120);
    assert.equal(r.pinned, false);
    M.scrolled(r, 950, 250, 0, 120);
    assert.equal(r.pinned, false, "down but far from the end");
    M.scrolled(r, 950, 250, 0, 120);
    assert.equal(r.pinned, false, "still");
    M.scrolled(r, 1100, 100, 0, 120);
    assert.equal(r.pinned, true);
    assert.equal(r.lastTop, 1100);
    // Keys.
    assert.equal(M.scrollKey(" ", false), true);
    assert.equal(M.scrollKey("PageDown", false), true);
    assert.equal(M.scrollKey("x", false), false);
    assert.equal(M.scrollKey("ArrowUp", true), false);
  }
});

// box is a scrolling element: the end is scrollHeight - clientHeight.
function box(scrollHeight, clientHeight = 100, scrollTop = 0) {
  return { scrollHeight, clientHeight, scrollTop };
}

test("the follower glides to the end, frame by frame, and stops there", () => {
  for (const create of followerCopies) {
    frames.clear();
    const el = box(1100);
    let on = true;
    const f = create(
      () => el,
      () => on,
      130,
    );
    f.kick();
    assert.equal(frames.pending, 1);
    f.kick();
    assert.equal(frames.pending, 1, "a second kick while flying does nothing");
    let t = performance.now();
    let guard = 0;
    while (frames.pending && guard++ < 500) {
      t += 16;
      frames.flush(t);
      assert.ok(el.scrollTop <= 1000);
    }
    assert.equal(el.scrollTop, 1000, "arrived at the end");
    assert.equal(frames.pending, 0, "not left running");
    // The box grows: a kick follows it further. The reader taking over stops it on the next frame.
    el.scrollHeight = 2100;
    f.kick();
    frames.flush((t += 16));
    assert.ok(el.scrollTop > 1000, `scrolled to ${el.scrollTop}, past 1000`);
    assert.ok(
      el.scrollTop < 2000,
      `scrolled to ${el.scrollTop}, short of 2000`,
    );
    const at = el.scrollTop;
    on = false;
    frames.flush((t += 16));
    assert.equal(
      el.scrollTop,
      at,
      "no last nudge once the reader has the scroll",
    );
    assert.equal(frames.pending, 0);
    // Not active: a kick does not start.
    f.kick();
    assert.equal(frames.pending, 0);
  }
});

test("the follower's jump and stop", () => {
  for (const create of followerCopies) {
    frames.clear();
    const el = box(600, 100, 0);
    const f = create(
      () => el,
      () => true,
    );
    f.kick();
    assert.equal(frames.pending, 1);
    f.jump();
    assert.equal(frames.pending, 0, "the flight is cancelled");
    assert.equal(el.scrollTop, 500);
    f.jump();
    assert.equal(el.scrollTop, 500);
    f.stop();
    f.kick();
    assert.equal(frames.pending, 1);
    f.stop();
    assert.equal(frames.pending, 0);
    // No element: nothing moves and nothing throws.
    const none = create(
      () => null,
      () => true,
    );
    none.jump();
    none.kick();
    frames.flush();
    assert.equal(frames.pending, 0);
    // A box smaller than its view has its end at 0.
    const small = box(50, 100, 0);
    const g = create(
      () => small,
      () => true,
    );
    g.kick();
    frames.flush();
    assert.equal(small.scrollTop, 0);
  }
});

for (const B of transcriptCopies) {
  test(`transcriptMessages draws answers, errors, the applying turn and a failed last run`, () => {
    let id = 0;
    const nextId = () => ++id;
    const report = { status: "done", changes: ["x"] };
    const files = [{ path: "a.go", status: "modified", added: 1, deleted: 0 }];
    const answers = {
      picks: ["this"],
      verdicts: ["apply"],
      request: "apply it",
    };
    const turns = [
      { User: "do a thing", Agent: "did it" },
      { User: "apply it", Agent: "applied" },
      { User: "broken", Agent: "" },
      { Agent: "said only" },
      { User: "plain", Agent: "plain answer" },
    ];
    const cps = [
      {
        turn: 0,
        files: true,
        steps: [],
        answers,
        outcome: {
          result: "the full answer",
          report,
          files,
          artifacts: [{ name: "a.png", type: "image/png", size: 3 }],
        },
      },
      { turn: 1, files: false, steps: [], outcome: { result: "", error: "" } },
      { turn: 2, files: false, steps: [], outcome: { error: "it failed" } },
      {
        turn: 4,
        files: false,
        steps: [],
        outcome: { result: "full", report: {} },
      },
      {
        turn: 5,
        task: "the last input",
        files: false,
        steps: [],
        outcome: { error: "crashed" },
      },
    ];
    const msgs = B.transcriptMessages(turns, cps, nextId);
    const view = msgs.map((m) => [m.role, m.text, m.kind ?? "", m.shown ?? ""]);
    assert.deepEqual(view, [
      ["user", "do a thing", "", ""],
      ["agent", "the full answer", "", ""],
      ["user", "apply it", "", "Applying suggestions"],
      ["agent", "applied", "", ""],
      ["user", "broken", "", ""],
      ["agent", "it failed", "error", ""],
      ["agent", "said only", "", ""],
      ["user", "plain", "", ""],
      ["agent", "full", "", ""],
      ["user", "the last input", "", ""],
      ["agent", "crashed", "error", ""],
    ]);
    const first = msgs[1];
    assert.equal(first.turn, 0);
    assert.equal(first.answers, answers);
    assert.deepEqual(first.changes, { files });
    assert.equal(first.report, report);
    assert.equal(first.artifacts.length, 1);
    const bare = msgs[8];
    assert.equal(bare.changes, undefined);
    assert.equal(bare.report, undefined, "an empty report is not drawn");
    assert.equal(bare.artifacts, undefined);
    assert.equal(msgs[9].turn, 5);
    // A result with nothing said keeps the result; a failed run with no task is not drawn.
    const only = B.transcriptMessages(
      [{ User: "q" }],
      [
        { turn: 0, files: false, steps: [], outcome: { result: "r" } },
        { turn: 1, files: false, steps: [], outcome: { error: "e" } },
      ],
      nextId,
    );
    assert.deepEqual(
      only.map((m) => m.text),
      ["q", "r"],
    );
    assert.deepEqual(B.transcriptMessages([], [], nextId), []);
  });
}

for (const B of turnCopies) {
  test(`lastUserIdOf and sameTurn`, () => {
    assert.equal(B.lastUserIdOf([]), -1);
    assert.equal(
      B.lastUserIdOf([
        { id: 1, role: "user" },
        { id: 2, role: "agent" },
        { id: 3, role: "user" },
        { id: 4, role: "agent" },
      ]),
      3,
    );
    assert.equal(B.lastUserIdOf([{ id: 2, role: "agent" }]), -1);
    const m1 = { id: 1 };
    const m2 = { id: 2 };
    const steps = [];
    const base = {
      g: { key: 1, turn: 0, items: [m1, m2] },
      steps,
      liveHere: false,
      activity: null,
    };
    const same = { ...base, g: { key: 1, turn: 0, items: [m1, m2] } };
    assert.equal(B.sameTurn(base, same), true);
    assert.equal(B.sameTurn(base, { ...same, steps: [] }), false);
    assert.equal(B.sameTurn(base, { ...same, liveHere: true }), false);
    assert.equal(B.sameTurn(base, { ...same, activity: "x" }), false);
    assert.equal(
      B.sameTurn(base, { ...same, g: { ...same.g, key: 2 } }),
      false,
    );
    assert.equal(
      B.sameTurn(base, { ...same, g: { ...same.g, turn: 1 } }),
      false,
    );
    assert.equal(
      B.sameTurn(base, { ...same, g: { ...same.g, items: [m1] } }),
      false,
    );
    assert.equal(
      B.sameTurn(base, { ...same, g: { ...same.g, items: [m1, { id: 2 }] } }),
      false,
    );
  });

  test(`useTurnView keeps stable actions that reach the latest store`, async () => {
    const calls = [];
    const cpInfo = {};
    const messages = [
      { id: 1, role: "user" },
      { id: 2, role: "agent" },
    ];
    const store = (over = {}) => ({
      rs: { messages, running: false, activityKind: "", cpInfo },
      checkpoints: {
        cpMenu: null,
        cpArmed: false,
        setCpMenu: (m) => calls.push(["setCpMenu", m, over.tag]),
        setCpArmed: (a) => calls.push(["setCpArmed", a, over.tag]),
        toggleCpMenu: (t) => calls.push(["toggleCpMenu", t, over.tag]),
        restoreCheckpoint: async (t, f) =>
          calls.push(["restore", t, f, over.tag]),
      },
      stream: {
        stopRun: () => calls.push(["stopRun", over.tag]),
        submit: async (x) => calls.push(["submit", x, over.tag]),
      },
      answerSuggestions: async (m, a) =>
        calls.push(["answer", m.id, a, over.tag]),
      speech: {
        speakingId: null,
        supported: true,
        speak: (m) => calls.push(["speak", m.id, over.tag]),
      },
      artifacts: {
        showRunArtifact: (a) => calls.push(["artifact", a.name, over.tag]),
      },
      ...over,
    });
    const s1 = store({ tag: 1 });
    const hook = await renderHook((p) => B.useTurnView(p.store), { store: s1 });
    const v1 = hook.result.current;
    assert.equal(v1.lastMsgId, 2);
    assert.equal(v1.lastUserId, 1);
    assert.equal(v1.running, false);
    assert.equal(v1.speechSupported, true);
    // Same inputs, new store object: the same view.
    const s2 = store({ tag: 2 });
    await hook.rerender({ store: s2 });
    assert.equal(hook.result.current, v1, "nothing a turn draws changed");
    // The actions act on the latest store.
    v1.setCpMenu(3);
    v1.setCpArmed(true);
    v1.toggleCpMenu(4);
    v1.restoreCheckpoint(5, true);
    v1.stopRun();
    v1.submit("again");
    v1.answerSuggestions({ id: 9 }, { picks: [] });
    v1.speak({ id: 8 });
    v1.showRunArtifact({ name: "f.txt", type: "text/plain" });
    assert.deepEqual(calls, [
      ["setCpMenu", 3, 2],
      ["setCpArmed", true, 2],
      ["toggleCpMenu", 4, 2],
      ["restore", 5, true, 2],
      ["stopRun", 2],
      ["submit", "again", 2],
      ["answer", 9, { picks: [] }, 2],
      ["speak", 8, 2],
      ["artifact", "f.txt", 2],
    ]);
    // A run starting changes the view; an empty conversation has no last message.
    const s3 = store({ tag: 3 });
    s3.rs = { ...s3.rs, running: true, messages: [] };
    await hook.rerender({ store: s3 });
    const v3 = hook.result.current;
    assert.notEqual(v3, v1);
    assert.equal(v3.running, true);
    assert.equal(v3.lastMsgId, null);
    assert.equal(v3.lastUserId, -1);
    assert.equal(v3.stopRun, v1.stopRun, "the actions keep their identity");
    await hook.unmount();
  });

  test(`useTurn reads the turn view from its context`, async () => {
    const view = { running: true };
    const seen = [];
    const hook = await renderHook(() => B.useTurn());
    assert.equal(hook.result.current, null, "no provider: no view");
    await hook.unmount();
    function Reader() {
      seen.push(B.useTurn());
      return null;
    }
    const m = await mount(
      h(B.TurnCtx.Provider, { value: view }, h(Reader, {})),
    );
    assert.equal(seen[0], view);
    await m.unmount();
  });
}
