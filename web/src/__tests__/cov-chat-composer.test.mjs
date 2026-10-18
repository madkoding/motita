// The composer's logic: the slash-command popup and tags, what Enter and the send button do with
// what was typed, and answering a report's suggestions.
// Run with: node --test web/src/__tests__/cov-chat-composer.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { act, fakeApi, json, load, renderHook } from "./cov-chat-helpers.mjs";

const { useComposer } = await load("useComposer.ts");
const { useSuggestions, answersPath, questionAnswersPath } =
  await load("useSuggestions.ts");

const CMDS = [
  { name: "/plan", group: "mode", help: "plan", aliases: ["/p"] },
  { name: "/task", group: "mode", help: "task", aliases: ["/t"] },
  { name: "/agents", group: "meta", help: "agents" },
  { name: "/clear", group: "session", help: "clear" },
];

// composer mounts useComposer with recording fakes.
async function composer({
  running = false,
  nextSuggestion = "",
  session = "s1",
  startSession = async () => "s1",
} = {}) {
  const calls = [];
  const rs = {
    runningRef: { current: running },
    sessionRef: { current: session },
    messages: [
      { id: 1, kind: "kind", text: "placeholder" },
      { id: 2, text: "kept" },
    ],
    setMessages(fn) {
      rs.messages = fn(rs.messages);
    },
  };
  const deps = {
    rs,
    drawers: { showAgents: () => calls.push(["agents"]) },
    stream: {
      submit: (t) => calls.push(["submit", t]),
      sendWhileRunning: (t) => calls.push(["while", t]),
      stopRun: () => calls.push(["stop"]),
    },
    runShellLine: async (c) => calls.push(["shell", c]),
    startSession: async () => {
      calls.push(["start"]);
      const id = await startSession();
      rs.sessionRef.current = id ?? "";
      return id;
    },
    nextSuggestion,
  };
  const hook = await renderHook(() => useComposer(deps));
  const c = () => hook.result.current;
  await act(() => c().setSlashCommands(CMDS));
  const run = (fn) => act(() => fn(c()));
  return { hook, c, calls, rs, run };
}

// settled waits for the promises a send left behind (opening a conversation) to run out.
const settled = () => act(() => new Promise((r) => setTimeout(r, 0)));

// key is a keydown event as the handlers read it.
function key(k, mods = {}) {
  const e = {
    key: k,
    shiftKey: false,
    ctrlKey: false,
    metaKey: false,
    altKey: false,
    ...mods,
  };
  e.prevented = false;
  e.preventDefault = () => {
    e.prevented = true;
  };
  return e;
}

test("ArrowRight on an empty field takes the suggested request", async () => {
  const { c, run } = await composer({ nextSuggestion: "Add tests" });
  const plain = key("ArrowRight");
  await run((x) => x.handleKeydown(plain));
  assert.equal(plain.prevented, true);
  assert.equal(c().input, "Add tests");
  // Now the field has text: the key moves the caret as usual.
  const again = key("ArrowRight");
  await run((x) => x.handleKeydown(again));
  assert.equal(again.prevented, false);
});

test("ArrowRight leaves the field alone with modifiers, tags or nothing suggested", async () => {
  const none = await composer();
  const a = key("ArrowRight");
  await none.run((x) => x.handleKeydown(a));
  assert.equal(a.prevented, false);
  const { c, run } = await composer({ nextSuggestion: "Add tests" });
  const shifted = key("ArrowRight", { shiftKey: true });
  await run((x) => x.handleKeydown(shifted));
  assert.equal(shifted.prevented, false);
  const left = key("ArrowLeft");
  await run((x) => x.handleKeydown(left));
  assert.equal(left.prevented, false);
  await run((x) => x.pickSlash({ name: "/task", group: "mode" }));
  const tagged = key("ArrowRight");
  await run((x) => x.handleKeydown(tagged));
  assert.equal(tagged.prevented, false);
  assert.equal(c().input, "");
});

const names = (items) => items.map((x) => x.name);

test("typing a slash filters the commands, by name or alias, and closes when nothing matches", async () => {
  const { c, run, hook } = await composer();
  await run((x) => x.onInput("/"));
  assert.deepEqual(names(c().slashPopup.items), [
    "/plan",
    "/task",
    "/agents",
    "/clear",
  ]);
  assert.equal(c().slashPopup.index, 0);
  await run((x) => x.onInput("/T"));
  assert.deepEqual(
    names(c().slashPopup.items),
    ["/task"],
    "matched by name and by its alias",
  );
  await run((x) => x.onInput("/zz"));
  assert.equal(c().slashPopup, null);
  await run((x) => x.onInput("/c"));
  assert.deepEqual(names(c().slashPopup.items), ["/clear"]);
  await run((x) => x.onInput("hello"));
  assert.equal(c().slashPopup, null);
  assert.equal(c().input, "hello");
  // At most SLASH_POPUP_MAX (8) rows.
  const many = Array.from({ length: 12 }, (_, i) => ({
    name: "/x" + i,
    group: "action",
    help: "",
  }));
  await run((x) => x.setSlashCommands(many));
  await run((x) => x.onInput("/x"));
  assert.equal(c().slashPopup.items.length, 8);
  await hook.unmount();
});

test("a command followed by a space becomes a tag; plan and task exclude each other", async () => {
  const { c, run, hook } = await composer();
  await run((x) => x.onInput("/P   draw it"));
  assert.deepEqual(
    c().activeTags.map((t) => t.name),
    ["/plan"],
    "named by its alias",
  );
  assert.equal(c().input, "draw it");
  assert.equal(c().slashPopup, null);
  // /task is blocked while /plan is on: not offered, not tagged, not picked.
  await run((x) => x.onInput("/t"));
  assert.equal(c().slashPopup, null, "the only match is blocked");
  await run((x) => x.onInput("/task now"));
  assert.deepEqual(
    c().activeTags.map((t) => t.name),
    ["/plan"],
  );
  assert.equal(c().input, "/task now");
  await run((x) => x.pickSlash(CMDS[1]));
  assert.deepEqual(
    c().activeTags.map((t) => t.name),
    ["/plan"],
  );
  // An unknown first word stays text.
  await run((x) => x.onInput("/nope x"));
  assert.equal(c().input, "/nope x");
  assert.equal(c().activeTags.length, 1);
  // Picking another command adds it, empties the field and focuses it.
  const field = document.createElement("textarea");
  field.id = "task";
  document.body.appendChild(field);
  await run((x) => x.pickSlash(CMDS[3]));
  assert.deepEqual(
    c().activeTags.map((t) => t.name),
    ["/plan", "/clear"],
  );
  assert.equal(document.activeElement, field);
  field.remove();
  await run((x) => x.pickSlash({ name: "/x", group: "action" }));
  assert.equal(c().activeTags.length, 3, "picked with no field to focus");
  // Removing one keeps the others and their keys.
  const [plan, clear] = c().activeTags;
  await run((x) => x.removeTag(plan.key));
  assert.deepEqual(
    c()
      .activeTags.map((t) => t.key)
      .slice(0, 1),
    [clear.key],
  );
  // With /task as a tag, /plan is the blocked one.
  await run((x) => x.removeTag(clear.key));
  await run((x) => x.onInput("/task go"));
  assert.ok(c().activeTags.some((t) => t.name === "/task"));
  await run((x) => x.onInput("/p"));
  assert.equal(c().slashPopup, null, "/plan is not offered next to /task");
  await hook.unmount();
});

test("the popup answers the arrows, Tab, Enter and Escape", async () => {
  const { c, run, hook, calls } = await composer();
  await run((x) => x.onInput("/"));
  const down = key("ArrowDown");
  await run((x) => x.handleKeydown(down));
  assert.equal(down.prevented, true);
  assert.equal(c().slashPopup.index, 1);
  await run((x) => x.handleKeydown(key("ArrowUp")));
  await run((x) => x.handleKeydown(key("ArrowUp")));
  assert.equal(c().slashPopup.index, 3, "wraps from the top to the bottom");
  await run((x) => x.handleKeydown(key("ArrowDown")));
  assert.equal(c().slashPopup.index, 0, "and from the bottom to the top");
  // A key the popup does not take falls through (and is not Enter: nothing happens).
  const a = key("a");
  await run((x) => x.handleKeydown(a));
  assert.equal(a.prevented, false);
  assert.ok(c().slashPopup);
  // Shift+Enter is not an accept, and leaves the line alone.
  const se = key("Enter", { shiftKey: true });
  await run((x) => x.handleKeydown(se));
  assert.equal(se.prevented, false);
  // Tab takes the highlighted one.
  await run((x) => x.handleKeydown(key("Tab")));
  assert.deepEqual(
    c().activeTags.map((t) => t.name),
    ["/plan"],
  );
  assert.equal(c().slashPopup, null);
  // Enter takes it too; /agents opens the drawer instead of becoming a tag.
  await run((x) => x.onInput("/ag"));
  const enter = key("Enter");
  await run((x) => x.handleKeydown(enter));
  assert.equal(enter.prevented, true);
  assert.deepEqual(calls, [["agents"]]);
  assert.equal(c().slashPopup, null);
  assert.equal(c().activeTags.length, 1);
  // Escape closes it.
  await run((x) => x.onInput("/c"));
  const esc = key("Escape");
  await run((x) => x.handleKeydown(esc));
  assert.equal(esc.prevented, true);
  assert.equal(c().slashPopup, null);
  // A blocked or missing selection is not taken.
  await run((x) => x.setSlashPopup({ items: [CMDS[1]], index: 0 }));
  await run((x) => x.handleKeydown(key("Tab")));
  assert.deepEqual(
    c().activeTags.map((t) => t.name),
    ["/plan"],
  );
  await run((x) => x.setSlashPopup({ items: [CMDS[3]], index: 5 }));
  await run((x) => x.handleKeydown(key("Tab")));
  assert.deepEqual(
    c().activeTags.map((t) => t.name),
    ["/plan"],
  );
  await run((x) => x.setSlashPopup((p) => p && { ...p, index: 0 }));
  await run((x) => x.setSlashPopup(null));
  await hook.unmount();
});

test("a message typed with no conversation opens one and goes there", async () => {
  const { c, run, calls } = await composer({ session: "" });
  await run((x) => x.onInput("make it so"));
  await run((x) => x.handleKeydown(key("Enter")));
  assert.equal(c().input, "", "the field is free at once");
  await settled();
  assert.deepEqual(calls, [["start"], ["submit", "make it so"]]);
  // The conversation is open now: the next message needs no second start.
  await run((x) => x.onInput("again"));
  await run((x) => x.handleKeydown(key("Enter")));
  assert.deepEqual(calls.slice(2), [["submit", "again"]]);
});

test("a message whose conversation could not be opened goes back to the field", async () => {
  const { c, run, calls } = await composer({
    session: "",
    startSession: async () => undefined,
  });
  await run((x) => x.onInput("keep me"));
  await run((x) => x.handleKeydown(key("Enter")));
  await settled();
  assert.deepEqual(calls, [["start"]]);
  assert.equal(c().input, "keep me");
});

test("Enter sends what the composer holds: a run, a shell line, the agents, or nothing", async () => {
  const { c, run, hook, calls, rs } = await composer();
  // Nothing typed: nothing sent.
  await run((x) => x.handleKeydown(key("Enter")));
  assert.deepEqual(calls, []);
  // Plain text starts a run and drops the placeholder.
  await run((x) => x.onInput("  hello  "));
  assert.equal(c().shellMode, false);
  await run((x) => x.handleKeydown(key("Enter")));
  assert.deepEqual(calls.pop(), ["submit", "hello"]);
  assert.deepEqual(
    rs.messages.map((m) => m.id),
    [2],
  );
  assert.equal(c().input, "");
  // Tags go first; tags alone are sent alone.
  await run((x) => x.onInput("/plan the thing"));
  await run((x) => x.handleKeydown(key("Enter")));
  assert.deepEqual(calls.pop(), ["submit", "/plan the thing"]);
  assert.equal(c().activeTags.length, 0, "cleared");
  await run((x) => x.onInput("/clear "));
  await run((x) => x.handleKeydown(key("Enter")));
  assert.deepEqual(calls.pop(), ["submit", "/clear"]);
  // A shell line runs in the shell, and the button says so.
  await run((x) => x.onInput("!ls -la"));
  assert.equal(c().shellMode, true);
  await run((x) => x.handleKeydown(key("Enter")));
  assert.deepEqual(calls.pop(), ["shell", "ls -la"]);
  await run((x) => x.onInput("/shell pwd"));
  assert.equal(c().shellMode, true);
  // Typed /agents opens the drawer.
  await run((x) => x.setInput("/agents"));
  const e = {
    prevented: false,
    preventDefault() {
      this.prevented = true;
    },
  };
  await run((x) => x.handleSubmit(e));
  assert.equal(e.prevented, true);
  assert.deepEqual(calls.pop(), ["agents"]);
  assert.equal(c().input, "");
  // Shift, Ctrl, Meta and (with no run) Alt insert a newline instead.
  await run((x) => x.onInput("line"));
  for (const mods of [
    { shiftKey: true },
    { ctrlKey: true },
    { metaKey: true },
    { altKey: true },
  ]) {
    await run((x) => x.handleKeydown(key("Enter", mods)));
  }
  assert.deepEqual(calls, []);
  // On a touch screen Enter is a newline.
  const saved = window.matchMedia;
  window.matchMedia = (q) => ({ matches: q === "(pointer: coarse)" });
  await run((x) => x.handleKeydown(key("Enter")));
  window.matchMedia = saved;
  assert.deepEqual(calls, []);
  assert.equal(c().input, "line");
  await hook.unmount();
});

test("while a run works, Enter sends the message to the run, Alt+Enter keeps the line, Escape stops it", async () => {
  const { run, hook, calls, c } = await composer({ running: true });
  await run((x) => x.onInput("more"));
  await run((x) => x.handleKeydown(key("Enter")));
  assert.deepEqual(calls.pop(), ["while", "more"]);
  await run((x) => x.onInput("later"));
  await run((x) => x.handleKeydown(key("Enter", { altKey: true })));
  assert.equal(c().input, "later");
  assert.deepEqual(calls, []);
  await run((x) => x.handleSubmit({ preventDefault() {} }));
  assert.deepEqual(calls.pop(), ["while", "later"]);
  const esc = key("Escape");
  await run((x) => x.handleKeydown(esc));
  assert.equal(esc.prevented, true);
  assert.deepEqual(calls.pop(), ["stop"]);
  await hook.unmount();
});

// answering mounts useSuggestions over a run state on session "s" with messages of turns 3 to 7
// and one with no turn, answering requests with `route`. What it sent, toasted and reloaded is
// recorded.
async function answering(route) {
  const api = fakeApi((path, init) => route(path, init, rs));
  const sent = [];
  const toasts = [];
  let reloads = 0;
  const rs = {
    sessionRef: { current: "s" },
    messages: [3, 4, 5, 6, 7]
      .map((turn) => ({ id: turn, turn, text: "q" }))
      .concat([{ id: 9, text: "c" }]),
    setMessages(fn) {
      rs.messages = fn(rs.messages);
    },
  };
  const stream = { submit: async (...a) => sent.push(a) };
  const ui = {
    reload: async () => {
      reloads += 1;
    },
    setToast: (t) => toasts.push(t),
  };
  const hook = await renderHook(() => useSuggestions(api, rs, stream, ui));
  return {
    api,
    rs,
    sent,
    toasts,
    reloads: () => reloads,
    hook,
    ...hook.result.current,
  };
}

const byTurn = (rs, turn) => rs.messages.find((m) => m.turn === turn);

test("answering questions keeps the answers, stores them, and sends them under their label", async () => {
  assert.equal(
    questionAnswersPath("a b", 3),
    "/v1/sessions/a%20b/checkpoints/3/question-answers",
  );
  const x = await answering(async () => json({}));
  const qa = { answers: ["x"], request: "do it\n\nanswers" };
  await x.answerQuestions(byTurn(x.rs, 3), qa, "x");
  assert.equal(byTurn(x.rs, 3).questionAnswers, qa);
  assert.equal(byTurn(x.rs, 4).questionAnswers, undefined);
  assert.deepEqual(
    x.api.calls.map((c) => [c.path, c.init.method, c.body]),
    [["/v1/sessions/s/checkpoints/3/question-answers", "PUT", qa]],
  );
  assert.deepEqual(x.sent, [["do it\n\nanswers", "/v1/sessions/s/task", "x"]]);
  // No turn number: nothing stored; no label: the prompt is drawn as it is.
  await x.answerQuestions(x.rs.messages[5], qa, "");
  assert.equal(x.api.calls.length, 1);
  assert.deepEqual(x.sent[1], [
    "do it\n\nanswers",
    "/v1/sessions/s/task",
    undefined,
  ]);
  assert.deepEqual(x.toasts, []);
  await x.hook.unmount();
});

test("answers the gateway did not keep are not sent: the card unlocks and a toast says so", async () => {
  const x = await answering(async (path, _init, rs) => {
    if (path.includes("/checkpoints/4/")) {
      throw new TypeError("offline");
    }
    if (path.includes("/checkpoints/5/")) {
      rs.sessionRef.current = "other"; // the user left while the answers were being kept
    }
    return json({ error: "boom" }, 500);
  });
  const qa = { answers: ["x"], request: "r" };
  await x.answerQuestions(byTurn(x.rs, 4), qa, "x");
  assert.equal(
    byTurn(x.rs, 4).questionAnswers,
    undefined,
    "unlocked to be sent again",
  );
  await x.answerQuestions(byTurn(x.rs, 3), qa, "x");
  assert.equal(byTurn(x.rs, 3).questionAnswers, undefined);
  await x.answerQuestions(byTurn(x.rs, 5), qa, "x");
  assert.equal(
    byTurn(x.rs, 5).questionAnswers,
    qa,
    "the conversation on screen is another one",
  );
  assert.deepEqual(x.sent, []);
  assert.deepEqual(
    x.toasts.map((t) => [t.message, t.type]),
    [
      ["Could not send the answers", "error"],
      ["Could not send the answers", "error"],
      ["Could not send the answers", "error"],
    ],
  );
  assert.equal(x.reloads(), 0);
  await x.hook.unmount();
});

test("questions another tab answered first are not answered twice: the conversation is read again", async () => {
  const x = await answering(async (path, _init, rs) => {
    if (path.includes("/checkpoints/7/")) {
      rs.sessionRef.current = "other";
    }
    return json({ error: "already answered" }, 409);
  });
  const qa = { answers: ["x"], request: "r" };
  await x.answerQuestions(byTurn(x.rs, 6), qa, "x");
  assert.equal(x.reloads(), 1);
  // Gone to another conversation meanwhile: that one is not the one to read again.
  await x.answerQuestions(byTurn(x.rs, 7), qa, "x");
  assert.equal(x.reloads(), 1);
  assert.deepEqual(x.sent, []);
  assert.deepEqual(x.toasts, []);
  await x.hook.unmount();
});

test("answers kept while the user moved to another conversation start the turn where they were asked", async () => {
  let post = "ok";
  const x = await answering(async (path, init, rs) => {
    if (init.method === "PUT") {
      rs.sessionRef.current = "other";
      return json({});
    }
    if (post === "offline") {
      throw new TypeError("offline");
    }
    return post === "empty" ? new Response(null) : json({});
  });
  const qa = { answers: ["x"], request: "r" };
  await x.answerQuestions(byTurn(x.rs, 3), qa, "x");
  const posts = () => x.api.calls.filter((c) => c.init.method === "POST");
  assert.deepEqual(
    posts().map((c) => [c.path, c.body]),
    [["/v1/sessions/s/task", { task: "r" }]],
  );
  assert.deepEqual(
    x.sent,
    [],
    "the stream draws into the conversation on screen, not this one",
  );
  x.rs.sessionRef.current = "s";
  post = "empty";
  await x.answerQuestions(byTurn(x.rs, 4), qa, "x");
  assert.equal(posts().length, 2);
  x.rs.sessionRef.current = "s";
  post = "offline";
  await x.answerQuestions(byTurn(x.rs, 5), qa, "x");
  assert.deepEqual(
    x.toasts.map((t) => t.message),
    ["Could not send the answers"],
  );
  await x.hook.unmount();
});

test("a turn the gateway refused after the user left is told, and the card unlocks if they came back", async () => {
  let back = false;
  const x = await answering(async (path, init, rs) => {
    if (init.method === "PUT") {
      rs.sessionRef.current = "other";
      return json({});
    }
    if (back) {
      rs.sessionRef.current = "s"; // the user returned while the turn was refused
    }
    return json({ error: "busy" }, 409);
  });
  const qa = { answers: ["x"], request: "r" };
  await x.answerQuestions(byTurn(x.rs, 3), qa, "x");
  assert.equal(
    byTurn(x.rs, 3).questionAnswers,
    qa,
    "the card on screen belongs to another conversation",
  );
  back = true;
  x.rs.sessionRef.current = "s";
  await x.answerQuestions(byTurn(x.rs, 4), qa, "x");
  assert.equal(
    byTurn(x.rs, 4).questionAnswers,
    undefined,
    "unlocked to be answered again",
  );
  assert.deepEqual(
    x.toasts.map((t) => t.message),
    ["Could not send the answers", "Could not send the answers"],
  );
  await x.hook.unmount();
});

test("answering suggestions keeps the answers, stores them, and sends the accepted ones", async () => {
  assert.equal(
    answersPath("a b", 3),
    "/v1/sessions/a%20b/checkpoints/3/answers",
  );
  const api = fakeApi(async (path) => {
    if (path.includes("/checkpoints/4/")) {
      throw new TypeError("offline");
    }
    return json({});
  });
  const sent = [];
  const rs = {
    sessionRef: { current: "s 1" },
    messages: [
      { id: 1, turn: 3, text: "a" },
      { id: 2, turn: 4, text: "b" },
      { id: 3, text: "c" },
    ],
    setMessages(fn) {
      rs.messages = fn(rs.messages);
    },
  };
  const stream = { submit: async (...a) => sent.push(a) };
  const ui = {
    reload: async () => {},
    setToast: () => assert.fail("no toast"),
  };
  const hook = await renderHook(() => useSuggestions(api, rs, stream, ui));
  const { answerSuggestions } = hook.result.current;
  const answers = { picks: ["this"], verdicts: ["apply"], request: "- do it" };
  await answerSuggestions(rs.messages[0], answers);
  assert.equal(rs.messages[0].answers, answers);
  assert.equal(rs.messages[1].answers, undefined);
  assert.deepEqual(
    api.calls.map((c) => [c.path, c.init.method, c.body]),
    [["/v1/sessions/s%201/checkpoints/3/answers", "PUT", answers]],
  );
  assert.deepEqual(sent, [
    ["- do it", "/v1/sessions/s%201/task", "Applying suggestions"],
  ]);
  // A gateway that cannot keep them: the turn is sent anyway.
  await answerSuggestions(rs.messages[1], answers);
  assert.equal(sent.length, 2);
  // Nothing accepted, and a message with no turn: nothing stored, nothing sent.
  const none = { picks: ["none"], verdicts: ["skip"], request: "" };
  await answerSuggestions(rs.messages[2], none);
  assert.equal(api.calls.length, 2);
  assert.equal(sent.length, 2);
  assert.equal(rs.messages[2].answers, none);
  await hook.unmount();
});

test("answering suggestions another tab already answered sends nothing and reads the conversation again", async () => {
  let leave = false;
  const api = fakeApi(async () => {
    if (leave) {
      rs.sessionRef.current = "other";
    }
    return json({ error: "answered" }, 409);
  });
  const sent = [];
  const reloads = [];
  const rs = {
    sessionRef: { current: "s" },
    messages: [{ id: 1, turn: 3, text: "a" }],
    setMessages(fn) {
      rs.messages = fn(rs.messages);
    },
  };
  const stream = { submit: async (...a) => sent.push(a) };
  const ui = {
    reload: async () => reloads.push(1),
    setToast: () => assert.fail("no toast"),
  };
  const hook = await renderHook(() => useSuggestions(api, rs, stream, ui));
  const answers = { picks: ["this"], verdicts: ["apply"], request: "- do it" };
  await hook.result.current.answerSuggestions(rs.messages[0], answers);
  assert.equal(
    sent.length,
    0,
    "the turn the other tab sent is the one that runs",
  );
  assert.equal(reloads.length, 1);
  // The user moved to another conversation meanwhile: nothing to read again.
  leave = true;
  await hook.result.current.answerSuggestions(rs.messages[0], answers);
  assert.equal(sent.length, 0);
  assert.equal(reloads.length, 1);
  await hook.unmount();
});
