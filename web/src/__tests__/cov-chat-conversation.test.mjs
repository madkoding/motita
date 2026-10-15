// Opening a conversation: its transcript and the steps kept for each input, its agents, its
// configuration and a run already in flight; following the address bar and phone notifications;
// and the screen with no conversation at all.
// Run with: node --test web/src/__tests__/cov-chat-conversation.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { act, fakeApi, fakeRunState, json, load, renderHook, tick } from "./cov-chat-helpers.mjs";

const { useConversation } = await load("useConversation.ts");

const STORAGE_KEY = "motita:last-session";
const NOTHING_YET = "Nothing yet. Ask for something below.";

// gateway answers the conversation's endpoints from `data[session]`, or with what `over` says.
function gateway(data, over = {}) {
  return fakeApi(async (path, init) => {
    const m = /^\/v1\/sessions\/([^/]+)\/(.+)$/.exec(path);
    const [, rawId, what] = m;
    const id = decodeURIComponent(rawId);
    if (over[what]) {
      return over[what](id, init);
    }
    const s = data[id] ?? {};
    if (what === "messages") {
      return json({ messages: s.messages });
    }
    if (what === "checkpoints") {
      return json({ checkpoints: s.checkpoints });
    }
    if (what === "run") {
      return s.run ? json(s.run) : json({}, 404);
    }
    if (what === "agents") {
      return json({ agents: s.agents ?? [] });
    }
    if (what === "config") {
      return json(s.config ?? { model: "m-" + id });
    }
    return undefined;
  });
}

async function conversation(api, { running = false } = {}) {
  const rs = fakeRunState();
  rs.runningRef.current = running;
  const states = [];
  const configs = [];
  const modal = [];
  const runningState = [];
  const lists = { setRunningState: (...a) => runningState.push(a) };
  const hook = await renderHook(() =>
    useConversation({
      api,
      rs,
      lists,
      setState: (...a) => states.push(a),
      setConfig: (c) => configs.push(c),
      requestModal: (o) => modal.push(o),
    }),
  );
  const c = () => hook.result.current;
  let detached = 0;
  let followed = 0;
  c().detachRef.current = () => (detached += 1);
  c().followRef.current = () => (followed += 1);
  return {
    hook,
    rs,
    c,
    states,
    configs,
    modal,
    runningState,
    detached: () => detached,
    followed: () => followed,
  };
}

const search = () => new URLSearchParams(window.location.search).get("session");

const turns = [
  { User: "list the files", Agent: "done" },
  { User: "and again", Agent: "again" },
];
const cps = [
  {
    turn: 0,
    files: true,
    steps: [
      { kind: "command", text: "running: ls", exit: 0, out: "a b" },
      { kind: "progress", text: "thinking: hmm" },
    ],
  },
  { turn: 1, files: false, steps: [{ kind: "command", text: "running: pwd" }] },
  { turn: 2, files: false },
];

test("opening a conversation draws its transcript, steps, terminal, agents and config", async () => {
  history.replaceState(null, "", "/");
  const api = gateway({
    "s 1": {
      messages: turns,
      checkpoints: cps,
      agents: [
        { id: "b", parent: "a", name: "helper" },
        { id: "a", name: "main" },
      ],
    },
  });
  const v = await conversation(api);
  assert.equal(v.c().switchSessionRef.current, v.c().switchSession);
  await act(() => v.c().switchSession("s 1"));
  assert.equal(v.detached(), 1, "the run being followed is let go");
  assert.equal(v.rs.cleared, 1);
  assert.equal(v.rs.sessionId, "s 1");
  assert.equal(v.rs.noSession, false);
  assert.equal(v.rs.sessionRef.current, "s 1");
  assert.equal(localStorage.getItem(STORAGE_KEY), "s 1");
  assert.equal(search(), "s 1");
  assert.deepEqual(v.modal, [true]);
  assert.deepEqual(
    v.rs.messages.map((m) => [m.role, m.text]),
    [
      ["user", "list the files"],
      ["agent", "done"],
      ["user", "and again"],
      ["agent", "again"],
    ],
  );
  assert.deepEqual(Object.keys(v.rs.stepsByTurn), ["0", "1", "2"]);
  assert.ok(v.rs.stepsByTurn[0].every((st) => typeof st.key === "number"), "steps are keyed");
  assert.deepEqual(v.rs.stepsByTurn[2], []);
  assert.deepEqual(v.rs.cpInfo, { 0: { files: true }, 1: { files: false }, 2: { files: false } });
  assert.deepEqual(
    v.rs.shellLog.map((e) => [e.cmd, e.exit, e.out]),
    [
      ["ls", 0, "a b"],
      ["pwd", -999, ""],
    ],
  );
  assert.equal(v.rs.termSeenId, v.rs.shellLog[1].id, "what was there is drawn whole");
  assert.deepEqual(v.rs.agents.map((a) => a.id), ["a", "b"]);
  assert.deepEqual(v.states, [["ready"]]);
  assert.deepEqual(v.configs, [{ model: "m-s 1" }]);
  assert.equal(v.followed(), 0, "no run to follow");
  assert.deepEqual(
    api.calls.map((c) => c.path).sort(),
    [
      "/v1/sessions/s%201/agents",
      "/v1/sessions/s%201/checkpoints",
      "/v1/sessions/s%201/config",
      "/v1/sessions/s%201/messages",
      "/v1/sessions/s%201/run",
    ],
  );
  await v.hook.unmount();
});

test("a run already working in the conversation is followed; one this tab runs is not", async () => {
  history.replaceState(null, "", "/");
  const api = gateway({ live: { messages: [], run: { outcome: null } } });
  const v = await conversation(api);
  await act(() => v.c().switchSession("live", "replace"));
  assert.equal(search(), "live");
  assert.deepEqual(
    v.rs.messages.map((m) => [m.text, m.kind]),
    [[NOTHING_YET, "kind"]],
    "an empty conversation says so",
  );
  assert.equal(v.rs.runSessionRef.current, "live");
  assert.deepEqual(v.runningState, [[true, "live"]]);
  assert.deepEqual(v.states, [["ready"], ["working"]]);
  assert.equal(v.followed(), 1);
  assert.equal(v.rs.lastIdRef.current, 0);
  await v.hook.unmount();
  // A run that has an outcome is over; a tab already running does not take it again.
  const ended = await conversation(gateway({ x: { messages: turns, run: { outcome: "done" } } }));
  await act(() => ended.c().switchSession("x"));
  assert.equal(ended.followed(), 0);
  await ended.hook.unmount();
  const busy = await conversation(gateway({ live: { messages: [], run: {} } }), { running: true });
  await act(() => busy.c().switchSession("live"));
  assert.equal(busy.followed(), 0);
  await busy.hook.unmount();
});

test("a soft re-read keeps the conversation, its run and its screen", async () => {
  history.replaceState(null, "", "/?session=s1");
  const v = await conversation(gateway({ s1: { messages: turns, checkpoints: [] } }));
  await act(() => v.c().switchSession("s1", "none", true));
  assert.equal(v.detached(), 0);
  assert.equal(v.rs.cleared, 0);
  assert.deepEqual(v.modal, []);
  assert.equal(v.rs.messages.length, 4);
  assert.equal(search(), "s1");
  await v.hook.unmount();
});

test("the gateway failing parts of a conversation leaves the rest on screen", async () => {
  history.replaceState(null, "", "/");
  // No checkpoints, no run endpoint, an unreadable config, and agents refused.
  const flaky = gateway(
    { f: { messages: turns } },
    {
      checkpoints: () => json({}, 500),
      run: () => {
        throw new TypeError("offline");
      },
      config: () => new Response("garbage"),
      agents: () => json({}, 404),
    },
  );
  const v = await conversation(flaky);
  v.rs.agents = [{ id: "old" }];
  await act(() => v.c().switchSession("f"));
  assert.equal(v.rs.messages.length, 4);
  assert.deepEqual(v.rs.stepsByTurn, {}, "no steps to draw");
  assert.deepEqual(v.rs.agents, [], "a gateway without agents has none");
  assert.deepEqual(v.configs, []);
  assert.deepEqual(v.states, [["ready"]]);
  await v.hook.unmount();
  // Checkpoints that cannot be fetched at all, an answer with none, agents that throw.
  const worse = gateway(
    { g: { messages: [] } },
    {
      checkpoints: () => {
        throw new TypeError("offline");
      },
      agents: () => {
        throw new TypeError("offline");
      },
    },
  );
  const w = await conversation(worse);
  await act(() => w.c().switchSession("g"));
  assert.deepEqual(w.rs.messages.map((m) => m.text), [NOTHING_YET]);
  await w.hook.unmount();
  const blank = await conversation(gateway({ h: {} }));
  await act(() => blank.c().switchSession("h"));
  assert.deepEqual(blank.rs.messages.map((m) => m.text), [NOTHING_YET], "no messages field");
  assert.deepEqual(blank.rs.stepsByTurn, {}, "no checkpoints field");
  await blank.hook.unmount();
});

test("a transcript that cannot be read says so and takes the loading card down", async () => {
  const v = await conversation(
    gateway({}, { messages: () => new Response("<html>") }),
  );
  await act(() => v.c().switchSession("bad"));
  assert.deepEqual(v.states, [["could not load the conversation", true]]);
  assert.deepEqual(v.modal, [true, false]);
  await v.hook.unmount();
});

test("answers that arrive after the reader moved on are dropped", async () => {
  let v;
  const moveOn = (when) => async (id) => {
    if (when === id) {
      v.rs.sessionRef.current = "other";
    }
    return undefined;
  };
  // Moved on while the transcript was loading.
  v = await conversation(
    gateway(
      { a: { messages: turns } },
      {
        messages: async (id) => {
          v.rs.sessionRef.current = "other";
          return json({ messages: id ? turns : [] });
        },
      },
    ),
  );
  await act(() => v.c().switchSession("a"));
  assert.deepEqual(v.rs.messages, []);
  assert.deepEqual(v.states, []);
  await v.hook.unmount();
  // Moved on while the steps were loading.
  v = await conversation(
    gateway(
      { b: { messages: turns } },
      {
        checkpoints: async (id) => {
          await moveOn(id)(id);
          return json({ checkpoints: cps });
        },
      },
    ),
  );
  await act(() => v.c().switchSession("b"));
  assert.deepEqual(v.rs.messages, []);
  assert.deepEqual(v.configs, []);
  await v.hook.unmount();
  // Moved on while the configuration was loading.
  v = await conversation(
    gateway(
      { c: { messages: turns } },
      {
        config: async (id) => {
          await moveOn(id)(id);
          return json({ model: "late" });
        },
      },
    ),
  );
  await act(() => v.c().switchSession("c"));
  assert.equal(v.rs.messages.length, 4);
  assert.deepEqual(v.configs, [], "a late configuration is not shown");
  await v.hook.unmount();
});

test("agents: none for no session, and late answers do not paint over another conversation", async () => {
  let v;
  const late = (stage) => (id) => {
    if (stage === "fetch") {
      v.rs.sessionRef.current = "other";
      return json({ agents: [{ id: "x" }] });
    }
    return {
      ok: true,
      json: async () => {
        v.rs.sessionRef.current = "other";
        return { agents: [{ id: id }] };
      },
    };
  };
  v = await conversation(gateway({}, { agents: late("fetch") }));
  v.rs.sessionRef.current = "a1";
  await v.c().loadAgents("a1");
  await v.c().loadAgents("");
  assert.deepEqual(v.rs.agents, []);
  await v.hook.unmount();
  v = await conversation(gateway({}, { agents: late("json") }));
  v.rs.sessionRef.current = "a2";
  await v.c().loadAgents("a2");
  assert.deepEqual(v.rs.agents, []);
  await v.hook.unmount();
});

test("steps refreshed at the end of a run keep the live turn's and leave the terminal alone", async () => {
  const fresh = [
    { turn: 0, files: true, steps: [{ kind: "command", text: "running: ls" }] },
    { turn: 1, files: false, steps: [{ kind: "progress", text: "new" }] },
  ];
  let reply = () => json({ checkpoints: fresh });
  const v = await conversation(gateway({}, { checkpoints: () => reply() }));
  v.rs.sessionRef.current = "s";
  const live = [{ kind: "progress", text: "live step", key: 1 }];
  v.rs.stepsByTurn = { 1: live, 5: [{ text: "kept", key: 2 }] };
  v.rs.liveTurnRef.current = 1;
  v.rs.shellLog = [{ id: 1, cmd: "old" }];
  await v.c().loadCheckpoints("s");
  assert.equal(v.rs.stepsByTurn[1], live, "the live turn keeps what the stream drew");
  assert.equal(v.rs.stepsByTurn[0][0].text, "running: ls");
  assert.equal(v.rs.stepsByTurn[5][0].text, "kept", "not rebuilt: the others stay");
  assert.deepEqual(v.rs.shellLog, [{ id: 1, cmd: "old" }]);
  // A live turn the screen has no steps for yet takes the gateway's.
  v.rs.liveTurnRef.current = 7;
  await v.c().loadCheckpoints("s");
  assert.equal(v.rs.stepsByTurn[7], undefined);
  // Rebuilding the terminal from steps with no commands leaves it empty.
  reply = () => json({ checkpoints: [{ turn: 0, files: false, steps: [] }] });
  await v.c().loadCheckpoints("s", true);
  assert.deepEqual(v.rs.shellLog, []);
  assert.equal(v.rs.termSeenId, 0);
  assert.deepEqual(v.rs.stepsByTurn, { 0: [] }, "rebuilt from scratch");
  // Late or failed answers draw nothing.
  v.rs.stepsByTurn = { 9: [] };
  await v.c().loadCheckpoints("not-on-screen", true);
  reply = () => json({}, 500);
  await v.c().loadCheckpoints("s", true);
  assert.deepEqual(v.rs.stepsByTurn, { 9: [] });
  await v.hook.unmount();
});

test("the terminal rebuilt from a long history keeps the last 500 commands", async () => {
  const many = [
    {
      turn: 0,
      files: false,
      steps: Array.from({ length: 520 }, (_, i) => ({ kind: "command", text: "running: c" + i, exit: 0 })),
    },
  ];
  const v = await conversation(gateway({ s: { messages: turns, checkpoints: many } }));
  await act(() => v.c().switchSession("s"));
  assert.equal(v.rs.shellLog.length, 500);
  assert.equal(v.rs.shellLog[0].cmd, "c20");
  await v.hook.unmount();
});

test("repainting the conversation on screen", async () => {
  let reply = { messages: turns, checkpoints: cps };
  const v = await conversation(
    gateway(
      {},
      {
        messages: () => json({ messages: reply.messages }),
        checkpoints: () => (reply.checkpoints ? json({ checkpoints: reply.checkpoints }) : json({}, 500)),
      },
    ),
  );
  v.rs.sessionRef.current = "p";
  await v.c().transcript();
  assert.equal(v.rs.messages.length, 4);
  assert.equal(v.rs.shellLog.length, 2);
  reply = { messages: turns, checkpoints: null };
  v.rs.shellLog = [];
  await v.c().transcript();
  assert.equal(v.rs.messages.length, 4);
  assert.deepEqual(v.rs.shellLog, [], "no steps: nothing rebuilt");
  reply = { checkpoints: [] };
  await v.c().transcript();
  assert.deepEqual(v.rs.messages.map((m) => m.text), [NOTHING_YET]);
  // Moved on while it loaded.
  const before = v.rs.messages;
  reply = {
    get messages() {
      v.rs.sessionRef.current = "q";
      return turns;
    },
    checkpoints: [],
  };
  await v.c().transcript();
  assert.equal(v.rs.messages, before);
  await v.hook.unmount();
});

test("back and forward open the conversation the address names", async () => {
  history.replaceState(null, "", "/");
  const api = gateway({ s1: { messages: turns }, s2: { messages: [] } });
  const v = await conversation(api);
  await act(() => v.c().switchSession("s1"));
  const calls = api.calls.length;
  // Same conversation, or none named: nothing loads.
  window.dispatchEvent(new window.PopStateEvent("popstate"));
  history.replaceState(null, "", "/");
  window.dispatchEvent(new window.PopStateEvent("popstate"));
  assert.equal(api.calls.length, calls);
  history.replaceState(null, "", "/?session=s2");
  await act(async () => {
    window.dispatchEvent(new window.PopStateEvent("popstate"));
    await tick(5);
  });
  assert.equal(v.rs.sessionRef.current, "s2");
  assert.equal(search(), "s2");
  await v.hook.unmount();
});

test("a tap on a phone notification opens its conversation", async () => {
  history.replaceState(null, "", "/");
  const sw = new window.EventTarget();
  Object.defineProperty(navigator, "serviceWorker", { value: sw, configurable: true });
  const api = gateway({ s1: { messages: turns }, s9: { messages: turns } });
  const v = await conversation(api);
  await act(() => v.c().switchSession("s1"));
  const message = (data) => {
    const e = new window.Event("message");
    e.data = data;
    return e;
  };
  sw.dispatchEvent(message({ type: "other" }));
  sw.dispatchEvent(message({ type: "motita-open", url: "/?session=s1" }));
  assert.equal(v.rs.sessionRef.current, "s1");
  await act(async () => {
    sw.dispatchEvent(message({ type: "motita-open", url: "/?session=s9#x" }));
    await tick(5);
  });
  assert.equal(v.rs.sessionRef.current, "s9");
  assert.equal(search(), "s9");
  await v.hook.unmount();
  delete navigator.serviceWorker;
});

test("with no conversation left the screen is emptied and the address cleared", async () => {
  history.replaceState(null, "", "/?session=gone");
  localStorage.setItem(STORAGE_KEY, "gone");
  const v = await conversation(gateway({}));
  v.rs.sessionRef.current = "gone";
  await act(() => v.c().showNoSession());
  assert.equal(v.detached(), 1);
  assert.equal(v.rs.sessionId, "");
  assert.equal(v.rs.noSession, true);
  assert.equal(v.rs.sessionRef.current, "");
  assert.equal(v.rs.cleared, 1);
  assert.deepEqual(v.configs, [null]);
  assert.equal(localStorage.getItem(STORAGE_KEY), null);
  assert.equal(search(), null);
  assert.deepEqual(v.modal, [false]);
  assert.deepEqual(v.states, [["ready"]]);
  await v.hook.unmount();
});

test("storage that refuses to be written does not stop a conversation opening or closing", async () => {
  const saved = globalThis.localStorage;
  globalThis.localStorage = {
    setItem() {
      throw new Error("denied");
    },
    removeItem() {
      throw new Error("denied");
    },
  };
  try {
    const v = await conversation(gateway({ s: { messages: turns } }));
    await act(() => v.c().switchSession("s", "none"));
    assert.equal(v.rs.messages.length, 4);
    await act(() => v.c().showNoSession());
    assert.equal(v.rs.noSession, true);
    await v.hook.unmount();
  } finally {
    globalThis.localStorage = saved;
  }
});
