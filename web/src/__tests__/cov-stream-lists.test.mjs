// The sidebar's lists (useSessionList) and what a person does to a session from it
// (useSessionActions), run as hooks over the real run state against a fake gateway.
import test, { mock } from "node:test";
import assert from "node:assert/strict";
import { installDom, source, renderHook, res } from "./cov-stream-helpers.mjs";

installDom();
const { useRunState } = await source("useRunState.ts");
const { useSessionList, withSession } = await source("useSessionList.ts");
const { useSessionActions, nextAfterDelete } = await source(
  "useSessionActions.ts",
);

const SIDEBAR_KEY = "motita:sidebar-open";
const COLLAPSE_KEY = "motita:collapsed-projects";

// gateway is a fake api: routes maps "METHOD path" to a function returning a response (or
// throwing); every call is recorded.
function gateway(routes = {}) {
  const calls = [];
  const api = async (path, init = {}) => {
    const key = (init.method || "GET") + " " + path;
    calls.push({ key, body: init.body });
    const route = routes[key];
    if (!route) {
      throw new Error("no route " + key);
    }
    return route(init);
  };
  return { api, calls, routes };
}

function phone(on) {
  window.matchMedia = () => ({ matches: on });
}

async function lists(gw) {
  return renderHook(() => {
    const rs = useRunState();
    return { rs, lists: useSessionList(gw.api, rs) };
  });
}

test("withSession changes one session's fields", () => {
  const list = [{ id: "a" }, { id: "b" }];
  assert.deepEqual(withSession(list, "b", { running: true }), [
    { id: "a" },
    { id: "b", running: true },
  ]);
});

test("the sidebar remembers whether it is open and which projects are folded", async () => {
  phone(false);
  localStorage.clear();
  localStorage.setItem(SIDEBAR_KEY, "false");
  localStorage.setItem(COLLAPSE_KEY, '["p1"]');
  const h = await lists(gateway());
  const l = () => h.result.current.lists;
  assert.equal(l().sidebarOpen, false);
  assert.deepEqual([...l().collapsedProjects], ["p1"]);
  await h.act(() => l().openSidebar());
  assert.equal(l().sidebarOpen, true);
  assert.equal(localStorage.getItem(SIDEBAR_KEY), "true");
  await h.act(() => l().toggleProject("p1"));
  await h.act(() => l().toggleProject("p2"));
  assert.deepEqual([...l().collapsedProjects], ["p2"]);
  assert.equal(localStorage.getItem(COLLAPSE_KEY), '["p2"]');
  await h.act(() => l().closeSidebar());
  assert.equal(localStorage.getItem(SIDEBAR_KEY), "false");
  await h.unmount();

  // Nothing remembered: open, nothing folded.
  localStorage.clear();
  const fresh = await lists(gateway());
  assert.equal(fresh.result.current.lists.sidebarOpen, true);
  assert.equal(fresh.result.current.lists.collapsedProjects.size, 0);
  await fresh.unmount();

  // A remembered list that cannot be read is no folded project.
  localStorage.setItem(COLLAPSE_KEY, "{not json");
  const bad = await lists(gateway());
  assert.equal(bad.result.current.lists.collapsedProjects.size, 0);
  await bad.unmount();
  localStorage.clear();
});

test("on a phone the sidebar starts closed, is not remembered and closes on a pick", async () => {
  phone(true);
  localStorage.clear();
  const h = await lists(gateway());
  const cur = () => h.result.current;
  assert.equal(cur().lists.sidebarOpen, false);
  await h.act(() => cur().lists.openSidebar());
  assert.equal(cur().lists.sidebarOpen, true);
  assert.equal(localStorage.getItem(SIDEBAR_KEY), null, "a phone's choice is not kept");
  await h.act(() => cur().rs.setSessionId("s1"));
  assert.equal(cur().lists.sidebarOpen, false, "picking a session closes the sheet");
  await h.unmount();

  // On a wide screen picking a session leaves the sidebar as it is.
  phone(false);
  const wide = await lists(gateway());
  await wide.act(() => wide.result.current.rs.setSessionId("s1"));
  assert.equal(wide.result.current.lists.sidebarOpen, true);
  await wide.unmount();
  localStorage.clear();
});

test("a browser that refuses storage still gets a working sidebar", async () => {
  phone(false);
  const saved = globalThis.localStorage;
  globalThis.localStorage = {
    getItem() {
      throw new Error("denied");
    },
    setItem() {
      throw new Error("denied");
    },
  };
  try {
    const h = await lists(gateway());
    const l = () => h.result.current.lists;
    assert.equal(l().sidebarOpen, true);
    await h.act(() => l().toggleProject("p"));
    assert.deepEqual([...l().collapsedProjects], ["p"]);
    await h.act(() => l().closeSidebar());
    assert.equal(l().sidebarOpen, false);
    await h.unmount();
  } finally {
    globalThis.localStorage = saved;
  }
});

test("the session list is read from the gateway, and the answered session stays masked", async () => {
  phone(false);
  let sessions = [
    { id: "s1", running: true },
    { id: "s2", running: false },
  ];
  const gw = gateway({
    "GET /v1/sessions": () => res(200, { sessions }),
    "GET /v1/projects": () => res(200, { projects: [{ id: "p1" }] }),
  });
  const h = await lists(gw);
  const cur = () => h.result.current;
  await h.act(() => cur().rs.setSessionId("s1"));
  await h.act(() => {
    cur().rs.sessionRef.current = "s1";
  });
  let got;
  await h.act(async () => {
    got = await cur().lists.fetchSessions();
  });
  assert.deepEqual(got, sessions);
  assert.deepEqual(cur().lists.sessions, sessions);
  await h.act(async () => {
    got = await cur().lists.fetchProjects();
  });
  assert.deepEqual(cur().lists.projects, [{ id: "p1" }]);

  // This tab's run starts: its row runs at once, and nothing is masked.
  await h.act(() => cur().lists.setRunningState(true));
  assert.equal(cur().rs.running, true);
  assert.equal(cur().rs.runningRef.current, true);
  assert.equal(cur().lists.sessions[0].running, true);
  assert.equal(cur().lists.answeredHere("s1"), false);
  // The answer arrives: the row is masked while the gateway still says it runs.
  await h.act(() => cur().lists.setRunningState(false));
  assert.equal(cur().lists.sessions[0].running, false);
  assert.equal(cur().lists.answeredHere("s1"), true);
  assert.equal(cur().lists.answeredHere("s2"), false);
  await h.act(() => cur().lists.fetchSessions());
  assert.equal(cur().lists.answeredHere("s1"), true, "a stale read keeps the mask");
  sessions = [{ id: "s1", running: false }];
  await h.act(() => cur().lists.fetchSessions());
  assert.equal(cur().lists.answeredHere("s1"), false, "the gateway's own word clears it");
  // Another session named explicitly.
  await h.act(() => cur().lists.setRunningState(false, "s2"));
  assert.equal(cur().lists.answeredHere("s2"), true);

  // Answers without lists are empty lists; a failure is an empty list and keeps what is shown.
  gw.routes["GET /v1/sessions"] = () => res(200, {});
  gw.routes["GET /v1/projects"] = () => res(200, {});
  await h.act(async () => {
    got = await cur().lists.fetchSessions();
  });
  assert.deepEqual(got, []);
  await h.act(async () => {
    got = await cur().lists.fetchProjects();
  });
  assert.deepEqual(cur().lists.projects, []);
  gw.routes["GET /v1/sessions"] = () => {
    throw new Error("offline");
  };
  gw.routes["GET /v1/projects"] = () => res(200, new Error("bad json"));
  await h.act(() => cur().lists.setSessions([{ id: "keep" }]));
  await h.act(async () => {
    got = await cur().lists.fetchSessions();
  });
  assert.deepEqual(got, []);
  assert.deepEqual(cur().lists.sessions, [{ id: "keep" }]);
  // An error status is a failure too, not an empty list to show.
  gw.routes["GET /v1/sessions"] = () => res(503, { error: "down" });
  await h.act(async () => {
    got = await cur().lists.loadSessions();
  });
  assert.equal(got, null);
  assert.deepEqual(cur().lists.sessions, [{ id: "keep" }]);
  await h.act(async () => {
    got = await cur().lists.fetchProjects();
  });
  assert.deepEqual(got, []);
  await h.unmount();
});

test("the list is polled while anything runs, and the poll stops when nothing does", async () => {
  phone(false);
  mock.timers.enable({ apis: ["setInterval"] });
  try {
    let sessions = [{ id: "s1", running: true }];
    const gw = gateway({
      "GET /v1/sessions": () => res(200, { sessions }),
    });
    const h = await lists(gw);
    const cur = () => h.result.current;
    mock.timers.tick(10_000);
    assert.equal(gw.calls.length, 0, "nothing runs: no poll");
    // Another client's run shows in the list: the poll starts.
    await h.act(() => cur().lists.setSessions(sessions));
    mock.timers.tick(3000);
    assert.equal(gw.calls.length, 1);
    sessions = [{ id: "s1", running: false }];
    await h.act(async () => {
      mock.timers.tick(3000);
      await Promise.resolve();
    });
    assert.equal(gw.calls.length, 2);
    await h.act(async () => {
      await new Promise((r) => setImmediate(r));
    });
    assert.equal(cur().lists.sessions[0].running, false);
    mock.timers.tick(9000);
    assert.equal(gw.calls.length, 2, "the poll stopped");
    // This tab's own run polls too.
    await h.act(() => cur().rs.setRunning(true));
    mock.timers.tick(3000);
    assert.equal(gw.calls.length, 3);
    await h.unmount();
    mock.timers.tick(9000);
    assert.equal(gw.calls.length, 3, "unmounting stops it");
  } finally {
    mock.timers.reset();
  }
});

// actions mounts the run state, the lists and the session actions over a fake conversation.
async function actions(gw, convOver = {}) {
  const conv = {
    switched: [],
    noSession: 0,
    switchSession: async (id) => {
      conv.switched.push(id);
    },
    showNoSession: () => {
      conv.noSession++;
    },
    ...convOver,
  };
  const states = [];
  const toasts = [];
  const h = await renderHook(() => {
    const rs = useRunState();
    const l = useSessionList(gw.api, rs);
    const a = useSessionActions({
      api: gw.api,
      rs,
      lists: l,
      conv,
      setState: (...args) => states.push(args),
      setToast: (t) => toasts.push(t),
    });
    return { rs, lists: l, actions: a };
  });
  return { h, conv, states, toasts, cur: () => h.result.current };
}

test("nextAfterDelete picks the most recent session left, or none", () => {
  const list = [
    { id: "a", last_used: "2024-01-01T00:00:00Z" },
    { id: "b", last_used: "2024-03-01T00:00:00Z" },
  ];
  assert.equal(nextAfterDelete(list, "b"), "a");
  assert.equal(nextAfterDelete([list[0]], "a"), "");
});

test("a new session is created, listed and opened; a refusal says why", async () => {
  phone(false);
  const gw = gateway({
    "POST /v1/sessions": (init) =>
      res(200, { id: init.body === "{}" ? "new1" : "new2" }),
    "GET /v1/sessions": () => res(200, { sessions: [{ id: "new1" }] }),
  });
  const { h, conv, states, cur } = await actions(gw);
  await h.act(() => cur().actions.createSession());
  assert.deepEqual(conv.switched, ["new1"]);
  assert.deepEqual(gw.calls.map((c) => c.key), ["POST /v1/sessions", "GET /v1/sessions"]);
  assert.equal(cur().actions.creatingSession, null);
  assert.deepEqual(cur().lists.sessions, [{ id: "new1" }]);

  // In a project, the project is named; the button shows it is busy while the gateway works.
  let release;
  gw.routes["POST /v1/sessions"] = (init) =>
    new Promise((r) => {
      release = () => r(res(200, { id: JSON.parse(init.body).project_id + "-s" }));
    });
  let creating;
  await h.act(async () => {
    creating = cur().actions.createSession("p1");
  });
  assert.equal(cur().actions.creatingSession, "p1");
  await h.act(async () => {
    release();
    await creating;
  });
  assert.equal(cur().actions.creatingSession, null);
  assert.deepEqual(conv.switched, ["new1", "p1-s"]);
  assert.equal(gw.calls.at(-2).body, '{"project_id":"p1"}');

  // The gateway refuses: its reason, or a plain one.
  gw.routes["POST /v1/sessions"] = () => res(409, { error: "project is busy" });
  await h.act(() => cur().actions.createSession());
  gw.routes["POST /v1/sessions"] = () => res(500, new Error("not json"));
  await h.act(() => cur().actions.createSession());
  gw.routes["POST /v1/sessions"] = () => {
    throw new Error("offline");
  };
  await h.act(() => cur().actions.createSession());
  assert.deepEqual(states, [
    ["project is busy", true],
    ["could not create a session", true],
    ["could not create a session", true],
  ]);
  assert.equal(cur().actions.creatingSession, null);
  await h.unmount();
});

test("a project created elsewhere asks for its first session through an event", async () => {
  phone(false);
  const gw = gateway({
    "POST /v1/sessions": () => res(200, { id: "ps" }),
    "GET /v1/sessions": () => res(200, { sessions: [] }),
  });
  const { h, conv } = await actions(gw);
  await h.act(async () => {
    window.dispatchEvent(new CustomEvent("motita:create-session", { detail: {} }));
    window.dispatchEvent(new window.CustomEvent("motita:create-session"));
    window.dispatchEvent(
      new window.CustomEvent("motita:create-session", { detail: { project_id: "p9" } }),
    );
    await new Promise((r) => setTimeout(r, 0));
  });
  assert.deepEqual(conv.switched, ["ps"]);
  assert.equal(gw.calls[0].body, '{"project_id":"p9"}');
  await h.unmount();
  window.dispatchEvent(
    new window.CustomEvent("motita:create-session", { detail: { project_id: "p9" } }),
  );
  assert.equal(gw.calls.length, 2, "an unmounted sidebar no longer listens");
});

test("deleting a session removes it at once and opens the next one, or none", async () => {
  phone(false);
  let listed = [
    { id: "a", last_used: "2024-01-01T00:00:00Z" },
    { id: "b", last_used: "2024-02-01T00:00:00Z" },
  ];
  const gw = gateway({
    "DELETE /v1/sessions/a": () => res(204, null),
    "DELETE /v1/sessions/b?force=1": () => res(200, {}),
    "DELETE /v1/sessions/c": () => res(204, null),
    "GET /v1/sessions": () => res(200, { sessions: listed }),
  });
  const { h, conv, states, cur } = await actions(gw);
  await h.act(() => {
    cur().lists.setSessions([...listed, { id: "c" }]);
    cur().rs.sessionRef.current = "b";
  });
  // Not the open one: removed, nothing else opened.
  listed = [listed[1]];
  await h.act(() => cur().actions.deleteSession("c"));
  assert.deepEqual(conv.switched, []);
  // The open one, with its changes discarded: the next most recent opens.
  listed = [{ id: "a", last_used: "2024-01-01T00:00:00Z" }];
  await h.act(() => cur().actions.deleteSession("b", true));
  assert.deepEqual(conv.switched, ["a"]);
  // The last one: nothing is open any more.
  await h.act(() => {
    cur().rs.sessionRef.current = "a";
  });
  listed = [];
  await h.act(() => cur().actions.deleteSession("a"));
  assert.equal(conv.noSession, 1);
  assert.deepEqual(cur().lists.sessions, []);

  // Refused (uncommitted work), with or without a reason, or unreachable.
  gw.routes["DELETE /v1/sessions/d"] = () => res(409, { error: "has changes" });
  gw.routes["DELETE /v1/sessions/e"] = () => res(500, new Error("x"));
  gw.routes["DELETE /v1/sessions/f"] = () => {
    throw new Error("offline");
  };
  await h.act(() => cur().lists.setSessions([{ id: "d" }]));
  await h.act(() => cur().actions.deleteSession("d"));
  await h.act(() => cur().actions.deleteSession("e"));
  await h.act(() => cur().actions.deleteSession("f"));
  assert.deepEqual(cur().lists.sessions, [{ id: "d" }], "a refused session stays");
  assert.deepEqual(states, [
    ["has changes", true],
    ["could not delete the session", true],
    ["could not delete the session", true],
  ]);
  await h.unmount();
});

test("deleting the open session offline opens the next one still listed", async () => {
  phone(false);
  const gw = gateway({
    "DELETE /v1/sessions/b": () => res(204, null),
    "GET /v1/sessions": () => {
      throw new Error("offline");
    },
  });
  const { h, conv, cur } = await actions(gw);
  await h.act(() => {
    cur().lists.setSessions([
      { id: "a", last_used: "2024-01-01T00:00:00Z" },
      { id: "b", last_used: "2024-02-01T00:00:00Z" },
    ]);
    cur().rs.sessionRef.current = "b";
  });
  await h.act(() => cur().actions.deleteSession("b"));
  assert.deepEqual(conv.switched, ["a"]);
  assert.equal(conv.noSession, 0);
  assert.deepEqual(
    cur().lists.sessions.map((s) => s.id),
    ["a"],
  );
  await h.unmount();
});

test("a session is renamed in place", async () => {
  phone(false);
  const gw = gateway({
    "PATCH /v1/sessions/a": (init) =>
      res(200, { id: "a", title: JSON.parse(init.body).title }),
  });
  const { h, states, cur } = await actions(gw);
  await h.act(() => cur().lists.setSessions([{ id: "a", title: "old" }, { id: "b" }]));
  await h.act(() => cur().actions.startRename("a", "old"));
  assert.equal(cur().actions.renamingId, "a");
  assert.equal(cur().actions.renameValue, "old");
  await h.act(() => cur().actions.setRenameValue("  new  "));
  // An empty title is not sent.
  await h.act(() => cur().actions.renameSession("a", "   "));
  assert.equal(gw.calls.length, 0);
  assert.equal(cur().actions.renamingId, "a");
  await h.act(() => cur().actions.renameSession("a", cur().actions.renameValue));
  assert.equal(gw.calls[0].body, '{"title":"new"}');
  assert.deepEqual(cur().lists.sessions, [{ id: "a", title: "new" }, { id: "b" }]);
  assert.equal(cur().actions.renamingId, null);

  gw.routes["PATCH /v1/sessions/b"] = () => res(400, { error: "too long" });
  gw.routes["PATCH /v1/sessions/c"] = () => res(400, new Error("x"));
  gw.routes["PATCH /v1/sessions/d"] = () => {
    throw new Error("offline");
  };
  await h.act(() => cur().actions.setRenamingId("b"));
  await h.act(() => cur().actions.renameSession("b", "x"));
  assert.equal(cur().actions.renamingId, "b", "a refused rename stays open");
  await h.act(() => cur().actions.renameSession("c", "x"));
  await h.act(() => cur().actions.renameSession("d", "x"));
  assert.equal(cur().actions.renamingId, null);
  assert.deepEqual(states, [
    ["too long", true],
    ["could not rename the session", true],
    ["could not rename the session", true],
  ]);
  await h.unmount();
});

test("a merged session is continued in a new one", async () => {
  phone(false);
  const gw = gateway({
    "POST /v1/sessions/m/continue": () => res(200, { id: "m2", title: "Next" }),
    "GET /v1/sessions": () => res(200, { sessions: [{ id: "m2" }] }),
  });
  const { h, conv, toasts, cur } = await actions(gw);
  await h.act(() => cur().actions.continueSession({ id: "m" }));
  assert.deepEqual(conv.switched, ["m2"]);
  assert.deepEqual(toasts, [
    { message: "Continued in a new session", type: "success", detail: "Next" },
  ]);
  gw.routes["POST /v1/sessions/x/continue"] = () => res(409, { error: "not merged" });
  gw.routes["POST /v1/sessions/y/continue"] = () => res(502, new Error("x"));
  gw.routes["POST /v1/sessions/z/continue"] = () => {
    throw new Error("offline");
  };
  await h.act(() => cur().actions.continueSession({ id: "x" }));
  await h.act(() => cur().actions.continueSession({ id: "y" }));
  await h.act(() => cur().actions.continueSession({ id: "z" }));
  assert.deepEqual(toasts.slice(1), [
    { message: "Could not continue this session", type: "error", detail: "not merged" },
    { message: "Could not continue this session", type: "error", detail: "HTTP 502" },
    { message: "Could not reach the gateway", type: "error", detail: "Error: offline" },
  ]);
  await h.unmount();
});
