// The page shell's hooks: the toast and the header pill, the drawers on the right edge, and the
// start of the page (the credential, the first load, the language).
// Run with: node --test web/src/__tests__/cov-shell-hooks.test.mjs
import test, { mock } from "node:test";
import assert from "node:assert/strict";
import {
  setupDom,
  load,
  mounter,
  flush,
  jsonResponse,
} from "./cov-shell-helpers.mjs";

setupDom("http://localhost/");

const { useToastStatus } = await load("useToastStatus.ts");
const { useDrawers } = await load("useDrawers.ts");
const { useAuth, useLanguage, lastSessionOf } = await load("useAuth.ts");
const i18n = await load("i18n.ts");
const { h } = await import("preact");
const { useState } = await import("preact/hooks");
const { act } = await import("preact/test-utils");
const mount = await mounter();
// micro lets promise callbacks run, even while the timers are faked.
const micro = async () => {
  for (let i = 0; i < 10; i++) {
    await Promise.resolve();
  }
};

// probe mounts a hook and returns a handle on its latest value.
async function probe(useHook, props = {}) {
  const ref = { current: null };
  function Probe(p) {
    ref.current = useHook(p);
    return null;
  }
  const m = await mount(h(Probe, props));
  return {
    get v() {
      return ref.current;
    },
    rerender: (p) => m.rerender(h(Probe, p)),
    done: m.done,
  };
}

// ── useToastStatus ───────────────────────────────────────────────────────────

test("the pill starts connecting, and takes the working states as given", async () => {
  const s = await probe(useToastStatus);
  assert.equal(s.v.stateText, "connecting");
  assert.equal(s.v.stateClass, "");
  await act(() => s.v.setState("working"));
  assert.equal(s.v.stateText, "working");
  assert.equal(s.v.stateClass, "state-working");
  await act(() => s.v.setState("reconnecting"));
  assert.equal(s.v.stateClass, "state-working");
  await act(() => s.v.setState("ready"));
  assert.equal(s.v.stateClass, "");
  assert.equal(s.v.toast, null, "a state is never a toast");
  await s.done();
});

test("a failure is toasted once and the pill only says error", async () => {
  const s = await probe(useToastStatus);
  await act(() => s.v.setState("could not load the skill library", true));
  assert.deepEqual(s.v.toast, {
    message: "could not load the skill library",
    type: "error",
  });
  assert.equal(s.v.stateText, "error");
  assert.equal(s.v.stateClass, "bad");
  await act(() => s.v.setState("", true));
  assert.deepEqual(
    s.v.toast,
    { message: "", type: "error" },
    "an empty failure still says so",
  );
  await s.done();
});

test("connection states are drawn by the pill, never toasted", async () => {
  const s = await probe(useToastStatus);
  await act(() => s.v.setState(" Reconnecting ", true));
  assert.equal(s.v.toast, null);
  assert.equal(s.v.stateText, "warning", "reconnecting is the recoverable one");
  assert.equal(s.v.stateClass, "state-warning bad");
  await act(() => s.v.setState("not connected", true));
  assert.equal(s.v.toast, null);
  assert.equal(s.v.stateText, "error");
  assert.equal(s.v.stateClass, "bad");
  await act(() => s.v.setState("connecting", true));
  assert.equal(s.v.toast, null);
  assert.equal(s.v.stateText, "error");
  await s.done();
});

test("each kind of toast stays its own time, and details fold again on a new toast", async () => {
  mock.timers.enable({ apis: ["setTimeout"] });
  try {
    const s = await probe(useToastStatus);
    const cases = [
      [{ message: "boom", type: "error" }, 12000],
      [{ message: "hmm", type: "warning" }, 9000],
      [{ message: "v2", type: "success", update: true }, 8000],
      [{ message: "done", type: "success" }, 5000],
    ];
    for (const [toast, ms] of cases) {
      await act(() => s.v.setToast(toast));
      assert.equal(s.v.toast, toast);
      await act(() => mock.timers.tick(ms - 1));
      assert.equal(
        s.v.toast,
        toast,
        `${toast.message} is still up before ${ms}ms`,
      );
      await act(() => mock.timers.tick(1));
      assert.equal(s.v.toast, null, `${toast.message} is gone at ${ms}ms`);
    }
    await act(() =>
      s.v.setToast({ message: "a", type: "error", technical: "trace" }),
    );
    assert.equal(s.v.toastDetailsOpen, false);
    await act(() => s.v.toggleToastDetails());
    assert.equal(s.v.toastDetailsOpen, true);
    await act(() => s.v.setToast({ message: "b", type: "error" }));
    assert.equal(s.v.toastDetailsOpen, false, "a new toast starts folded");
    await act(() => s.v.dismissToast());
    assert.equal(s.v.toast, null);
    await s.done();
  } finally {
    mock.timers.reset();
  }
});

// ── useDrawers ───────────────────────────────────────────────────────────────

// drawerHarness mounts useDrawers over a run state the test controls.
async function drawerHarness({ shellLog = [], agents = [] } = {}) {
  const ctl = {};
  function Harness() {
    const [log, setLog] = useState(shellLog);
    const [termSeenId, setTermSeenId] = useState(0);
    const [agentList, setAgents] = useState(agents);
    const [agentOpenId, setAgentOpenId] = useState(null);
    ctl.setLog = setLog;
    ctl.setAgents = setAgents;
    ctl.termSeenId = termSeenId;
    ctl.agentOpenId = agentOpenId;
    ctl.d = useDrawers({
      shellLog: log,
      termSeenId,
      setTermSeenId,
      agents: agentList,
      setAgentOpenId,
    });
    return null;
  }
  const m = await mount(h(Harness, {}));
  return { ctl, done: m.done };
}

const open = (d) => [d.termOpen, d.thinkOpen, d.agentsOpen];

test("the drawers share the edge: opening one closes the others", async () => {
  const { ctl, done } = await drawerHarness();
  assert.deepEqual(open(ctl.d), [false, false, false]);
  await act(() => ctl.d.toggleTerm());
  assert.deepEqual(open(ctl.d), [true, false, false]);
  await act(() => ctl.d.toggleThink());
  assert.deepEqual(open(ctl.d), [false, true, false]);
  await act(() => ctl.d.toggleAgents());
  assert.deepEqual(open(ctl.d), [false, false, true]);
  await act(() => ctl.d.showTerm());
  assert.deepEqual(open(ctl.d), [true, false, false]);
  await act(() => ctl.d.showAgents());
  assert.deepEqual(open(ctl.d), [false, false, true]);
  await act(() => ctl.d.toggleAgents());
  assert.deepEqual(
    open(ctl.d),
    [false, false, false],
    "a toggle closes its own drawer",
  );
  await act(() => ctl.d.toggleThink());
  await act(() => ctl.d.closeThink());
  assert.deepEqual(open(ctl.d), [false, false, false]);
  await act(() => ctl.d.showTerm());
  await act(() => ctl.d.closeTerm());
  await act(() => ctl.d.showAgents());
  await act(() => ctl.d.closeAgents());
  assert.deepEqual(open(ctl.d), [false, false, false]);
  await done();
});

test("a running agent's clock ticks only while the agents drawer is open", async () => {
  mock.timers.enable({ apis: ["setInterval", "Date"], now: 1_000_000 });
  try {
    const { ctl, done } = await drawerHarness({
      agents: [{ id: "a", state: "running" }],
    });
    const before = ctl.d.agentNow;
    await act(() => mock.timers.tick(3000));
    assert.equal(ctl.d.agentNow, before, "closed: no tick");
    await act(() => ctl.d.showAgents());
    await act(() => mock.timers.tick(1000));
    assert.equal(ctl.d.agentNow, before + 4000);
    await act(() => mock.timers.tick(1000));
    assert.equal(ctl.d.agentNow, before + 5000);
    await act(() => ctl.setAgents([{ id: "a", state: "passed" }]));
    const stopped = ctl.d.agentNow;
    await act(() => mock.timers.tick(5000));
    assert.equal(ctl.d.agentNow, stopped, "nothing running: the clock stops");
    await done();
  } finally {
    mock.timers.reset();
  }
});

test("an open terminal far behind skips to the last few entries", async () => {
  const entries = (n) =>
    Array.from({ length: n }, (_, i) => ({
      id: i + 1,
      cmd: "c" + i,
      exit: 0,
      out: "",
    }));
  const { ctl, done } = await drawerHarness({ shellLog: entries(40) });
  assert.equal(ctl.termSeenId, 0, "closed: nothing is skipped");
  await act(() => ctl.d.showTerm());
  assert.equal(ctl.termSeenId, 35, "only the newest few remain to be played");
  await act(() => ctl.setLog(entries(45)));
  assert.equal(ctl.termSeenId, 35, "ten behind is within the cap");
  await act(() => ctl.d.termEntryDone(36));
  assert.equal(ctl.termSeenId, 36);
  await act(() => ctl.d.termEntryDone(20));
  assert.equal(ctl.termSeenId, 36, "an older entry finishing never goes back");
  await done();
});

test("an agent's summary folds and unfolds, one at a time", async () => {
  const { ctl, done } = await drawerHarness();
  await act(() => ctl.d.toggleAgent("a"));
  assert.equal(ctl.agentOpenId, "a");
  await act(() => ctl.d.toggleAgent("b"));
  assert.equal(ctl.agentOpenId, "b");
  await act(() => ctl.d.toggleAgent("b"));
  assert.equal(ctl.agentOpenId, null);
  await done();
});

test("copying an agent's merge says copied for a moment, and nothing when the copy fails", async () => {
  const writes = [];
  let fail = false;
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: {
      writeText: async (t) => {
        if (fail) {
          throw new Error("denied");
        }
        writes.push(t);
      },
    },
  });
  mock.timers.enable({ apis: ["setTimeout"] });
  try {
    const { ctl, done } = await drawerHarness();
    await act(async () => {
      ctl.d.copyMerge("a", "git merge --no-edit x");
      await micro();
    });
    assert.deepEqual(writes, ["git merge --no-edit x"]);
    assert.equal(ctl.d.agentCopied, "a");
    await act(() => mock.timers.tick(700));
    await act(async () => {
      ctl.d.copyMerge("b", "git merge --no-edit y");
      await micro();
    });
    assert.equal(ctl.d.agentCopied, "b");
    await act(() => mock.timers.tick(800));
    assert.equal(ctl.d.agentCopied, "b", "a's moment ending does not clear b");
    await act(() => mock.timers.tick(700));
    assert.equal(ctl.d.agentCopied, null);
    fail = true; // and the legacy route is not there in jsdom either
    await act(async () => {
      ctl.d.copyMerge("c", "git merge --no-edit z");
      await micro();
    });
    assert.equal(ctl.d.agentCopied, null);
    await done();
  } finally {
    mock.timers.reset();
    delete navigator.clipboard;
  }
});

// ── useAuth ──────────────────────────────────────────────────────────────────

// gateway is a fake of the gateway's credential endpoints for authGate, exchange and submitToken.
function gateway({ authed = true, accept = "good" } = {}) {
  const state = { authed, calls: [] };
  globalThis.fetch = async (url, init = {}) => {
    state.calls.push({ url, method: init.method || "GET", init });
    if (url === "/v1/sessions") {
      return new Response("[]", { status: state.authed ? 200 : 401 });
    }
    if (url === "/v1/webui/session" && init.method === "POST") {
      const ok = init.headers.Authorization === "Bearer " + accept;
      if (ok) {
        state.authed = true;
      }
      return new Response(null, { status: ok ? 204 : 401 });
    }
    return new Response(null, { status: 404 });
  };
  return state;
}

// authDeps are the parts of the store useAuth drives, recording what it is asked.
function authDeps({
  sessions = [],
  commands = { commands: [{ name: "help" }] },
  projectsFail = false,
} = {}) {
  const log = [];
  const deps = {
    api: async (path) => {
      log.push(["api", path]);
      if (commands instanceof Error) {
        throw commands;
      }
      return jsonResponse(commands);
    },
    rs: {
      reconnectTimer: { current: null },
      setMessages: (v) => log.push(["setMessages", v]),
    },
    lists: {
      fetchProjects: async () => {
        log.push(["fetchProjects"]);
        if (projectsFail) {
          throw new Error("down");
        }
      },
      fetchSessions: async () => {
        log.push(["fetchSessions"]);
        return sessions;
      },
      setSessions: (v) => log.push(["setSessions", v]),
      setProjects: (v) => log.push(["setProjects", v]),
    },
    conv: {
      switchSession: async (id, mode) => log.push(["switchSession", id, mode]),
      showNoSession: () => log.push(["showNoSession"]),
    },
    setState: (text, bad) => log.push(["setState", text, bad]),
    setSlashCommands: (list) => log.push(["setSlashCommands", list]),
  };
  return { deps, log };
}

const S = (id, last_used) => ({
  id,
  title: id,
  created: last_used,
  last_used,
  running: false,
});

test("with a credential the page loads everything and opens the last session", async () => {
  history.replaceState(null, "", "/");
  localStorage.setItem("motita:last-session", "s1");
  gateway();
  const { deps, log } = authDeps({
    sessions: [
      S("s1", "2024-01-01T00:00:00Z"),
      S("s2", "2024-02-01T00:00:00Z"),
    ],
  });
  const a = await probe(() => useAuth(deps));
  assert.equal(a.v.authState, "checking");
  await act(() => flush());
  assert.equal(a.v.authState, "ok");
  assert.deepEqual(log, [
    ["fetchProjects"],
    ["api", "/v1/commands"],
    ["setSlashCommands", [{ name: "help" }]],
    ["fetchSessions"],
    ["switchSession", "s1", "replace"],
    ["setState", "ready", undefined],
  ]);
  await a.done();
  localStorage.clear();
});

test("without a remembered session the newest is opened, and with none the chat waits", async () => {
  gateway();
  let { deps, log } = authDeps({
    sessions: [
      S("old", "2024-01-01T00:00:00Z"),
      S("new", "2024-03-01T00:00:00Z"),
    ],
    commands: {},
  });
  let a = await probe(() => useAuth(deps));
  await act(() => flush());
  assert.deepEqual(
    log.find((e) => e[0] === "switchSession"),
    ["switchSession", "new", "replace"],
  );
  assert.deepEqual(
    log.find((e) => e[0] === "setSlashCommands"),
    ["setSlashCommands", []],
  );
  await a.done();

  ({ deps, log } = authDeps({
    sessions: [],
    commands: new Error("no commands"),
  }));
  a = await probe(() => useAuth(deps));
  await act(() => flush());
  assert.ok(log.some((e) => e[0] === "showNoSession"));
  assert.ok(
    !log.some((e) => e[0] === "setSlashCommands"),
    "a failed command list is not fatal",
  );
  assert.deepEqual(log.at(-1), ["setState", "ready", undefined]);
  await a.done();
});

test("a gateway that cannot be reached says not connected", async () => {
  gateway();
  const { deps, log } = authDeps({ projectsFail: true });
  const a = await probe(() => useAuth(deps));
  await act(() => flush());
  assert.equal(a.v.authState, "ok");
  assert.deepEqual(log.at(-1), ["setState", "not connected", true]);
  await a.done();
});

test("without a credential the token is asked for; a rejected one says so", async () => {
  history.replaceState(null, "", "/");
  const gw = gateway({ authed: false });
  const { deps, log } = authDeps();
  const a = await probe(() => useAuth(deps));
  await act(() => flush());
  assert.equal(a.v.authState, "needed");
  assert.equal(log.length, 0, "nothing is loaded without a credential");

  await act(() => a.v.submitAuth());
  assert.equal(
    gw.calls.filter((c) => c.method === "POST").length,
    0,
    "an empty token is not sent",
  );

  await act(() => a.v.setAuthInput("  wrong  "));
  let pending;
  await act(() => {
    pending = a.v.submitAuth();
  });
  assert.equal(a.v.authBusy, true);
  await act(() => a.v.submitAuth()); // a second press while the first is in flight
  await act(() => pending);
  await act(() => flush());
  const posts = gw.calls.filter((c) => c.method === "POST");
  assert.equal(posts.length, 1, "one request while busy");
  assert.equal(
    posts[0].init.headers.Authorization,
    "Bearer wrong",
    "the token is trimmed",
  );
  assert.equal(a.v.authError, "That token was rejected. Try again.");
  assert.equal(a.v.authBusy, false);
  assert.equal(a.v.authState, "needed");
  await a.done();
});

test("an accepted token opens the page, erases the fragment and loads everything", async () => {
  history.replaceState(null, "", "/?session=s2#junk");
  gateway({ authed: false, accept: "good" });
  const { deps, log } = authDeps({
    sessions: [
      S("s1", "2024-01-01T00:00:00Z"),
      S("s2", "2023-01-01T00:00:00Z"),
    ],
  });
  const a = await probe(() => useAuth(deps));
  await act(() => flush());
  assert.equal(a.v.authState, "needed");
  await act(() => a.v.setAuthInput("good"));
  await act(() => a.v.submitAuth());
  await act(() => flush());
  assert.equal(a.v.authState, "ok");
  assert.equal(a.v.authInput, "");
  assert.equal(a.v.authError, "");
  assert.equal(location.hash, "");
  assert.deepEqual(
    log.find((e) => e[0] === "switchSession"),
    ["switchSession", "s2", "replace"],
    "the session the address names wins",
  );
  await a.done();
  history.replaceState(null, "", "/");
});

test("a failing exchange does not stop the start", async () => {
  history.replaceState(null, "", "/#t=tok");
  gateway({ authed: false });
  const real = history.replaceState;
  history.replaceState = () => {
    throw new Error("blocked");
  };
  const { deps } = authDeps();
  let a;
  try {
    a = await probe(() => useAuth(deps));
    await act(() => flush());
  } finally {
    history.replaceState = real;
  }
  assert.equal(a.v.authState, "needed", "the gate was still asked");
  await a.done();
  history.replaceState(null, "", "/");
});

test("a 401 elsewhere brings the token prompt back and clears what was shown", async () => {
  gateway();
  const { deps, log } = authDeps();
  const a = await probe(() => useAuth(deps));
  await act(() => flush());
  await act(() => a.v.setAuthInput("leftover"));
  log.length = 0;
  await act(() => window.dispatchEvent(new CustomEvent("motita:auth-needed")));
  assert.equal(a.v.authState, "needed");
  assert.equal(a.v.authInput, "");
  assert.deepEqual(log, [
    ["setMessages", []],
    ["setSessions", []],
    ["setProjects", []],
    ["setState", "not connected", true],
  ]);
  await a.done();
  log.length = 0;
  window.dispatchEvent(new CustomEvent("motita:auth-needed"));
  assert.equal(log.length, 0, "the listener leaves with the page");
});

test("leaving the page cancels a pending reconnect", async () => {
  gateway();
  const { deps } = authDeps();
  const timer = setTimeout(() => {}, 60_000);
  deps.rs.reconnectTimer.current = timer;
  const a = await probe(() => useAuth(deps));
  await act(() => flush());
  const cleared = [];
  const realClear = globalThis.clearTimeout;
  globalThis.clearTimeout = (id) => {
    cleared.push(id);
    realClear(id);
  };
  try {
    await a.done();
  } finally {
    globalThis.clearTimeout = realClear;
  }
  assert.ok(cleared.includes(timer), "the reconnect timer was cleared");
});

test("lastSessionOf: the address, then the remembered one, then the newest, else none", () => {
  const list = [S("a", "2024-01-01T00:00:00Z"), S("b", "2024-05-01T00:00:00Z")];
  history.replaceState(null, "", "/?session=a");
  localStorage.setItem("motita:last-session", "b");
  assert.equal(lastSessionOf(list), "a");
  history.replaceState(null, "", "/?session=gone");
  assert.equal(
    lastSessionOf(list),
    "b",
    "an address naming no session is ignored",
  );
  localStorage.setItem("motita:last-session", "deleted");
  assert.equal(
    lastSessionOf(list),
    "b",
    "the newest when the remembered one is gone",
  );
  assert.equal(lastSessionOf([]), "");
  history.replaceState(null, "", "/");
  const proto = Object.getPrototypeOf(localStorage);
  const getItem = proto.getItem;
  proto.getItem = () => {
    throw new Error("blocked");
  };
  try {
    assert.equal(
      lastSessionOf(list),
      "b",
      "storage that cannot be read is skipped",
    );
  } finally {
    proto.getItem = getItem;
  }
  localStorage.clear();
});

// ── useLanguage ──────────────────────────────────────────────────────────────

function uiApi(handler) {
  const calls = [];
  const api = async (path, init) => {
    calls.push({ path, init });
    return handler(path, init);
  };
  return { api, calls };
}

async function langProbe(api, authState, toasts) {
  const ref = { current: null };
  function P({ authState }) {
    ref.current = useLanguage(api, authState, (t) => toasts.push(t));
    return null;
  }
  const m = await mount(h(P, { authState }));
  return {
    get v() {
      return ref.current;
    },
    rerender: (s) => m.rerender(h(P, { authState: s })),
    done: m.done,
  };
}

test("the language follows the browser until the gateway's setting is read", async () => {
  const { api, calls } = uiApi(() => jsonResponse({ language: "es" }));
  const toasts = [];
  const l = await langProbe(api, "checking", toasts);
  await act(() => flush());
  assert.equal(calls.length, 0, "nothing is asked without a credential");
  assert.equal(l.v.uiLangAvailable, false);
  assert.equal(i18n.lang(), "en");
  await l.rerender("ok");
  await act(() => flush(10));
  assert.equal(calls[0].path, "/v1/ui");
  assert.equal(l.v.uiLangSetting, "es");
  assert.equal(l.v.uiLangAvailable, true);
  assert.equal(i18n.lang(), "es");
  assert.equal(document.documentElement.lang, "es");
  await l.done();
  await i18n.setLang("en");
});

test("an unknown language is the browser's, and an older gateway leaves it hidden", async () => {
  let l = await langProbe(
    uiApi(() => jsonResponse({ language: "fr" })).api,
    "ok",
    [],
  );
  await act(() => flush());
  assert.equal(l.v.uiLangSetting, "auto");
  assert.equal(l.v.uiLangAvailable, true);
  await l.done();

  l = await langProbe(uiApi(() => jsonResponse({}, 404)).api, "ok", []);
  await act(() => flush());
  assert.equal(l.v.uiLangAvailable, false, "a 404 hides the setting");
  await l.done();

  l = await langProbe(
    uiApi(() => {
      throw new Error("unauthorised");
    }).api,
    "ok",
    [],
  );
  await act(() => flush());
  assert.equal(l.v.uiLangAvailable, false);
  await l.done();
});

test("an answer that arrives after the page left is ignored", async () => {
  let release;
  const gate = new Promise((r) => (release = r));
  let parsed = 0;
  const { api } = uiApi(async () => {
    await gate;
    return {
      ok: true,
      json: async () => {
        parsed++;
        return { language: "es" };
      },
    };
  });
  const l = await langProbe(api, "ok", []);
  await l.done();
  release();
  await flush();
  assert.equal(parsed, 0);
  assert.equal(i18n.lang(), "en");
});

test("changing the language saves it for all of motita, and reverts when it cannot", async () => {
  const toasts = [];
  let answer = () => new Response(null, { status: 204 });
  const { api, calls } = uiApi((path, init) =>
    init?.method === "PUT" ? answer() : jsonResponse({ language: "en" }),
  );
  const l = await langProbe(api, "ok", toasts);
  await act(() => flush());
  assert.equal(l.v.uiLangSetting, "en");

  await act(() => l.v.changeLanguage("es"));
  const put = calls.find((c) => c.init?.method === "PUT");
  assert.equal(put.path, "/v1/ui");
  assert.deepEqual(JSON.parse(put.init.body), { language: "es" });
  assert.equal(put.init.headers["Content-Type"], "application/json");
  assert.equal(l.v.uiLangSetting, "es");
  assert.equal(i18n.lang(), "es");
  assert.equal(toasts.length, 0);

  answer = () => jsonResponse({ error: "read-only config" }, 409);
  await act(() => l.v.changeLanguage("en"));
  await act(() => flush());
  assert.equal(l.v.uiLangSetting, "es", "reverted");
  assert.equal(i18n.lang(), "es");
  assert.deepEqual(toasts.at(-1), {
    message: "No se pudo guardar el idioma",
    type: "error",
    detail: "Error: read-only config",
  });

  answer = () => new Response("not json", { status: 500 });
  await act(() => l.v.changeLanguage("auto"));
  await act(() => flush());
  assert.equal(toasts.at(-1).detail, "Error: HTTP 500");
  assert.equal(l.v.uiLangSetting, "es");

  answer = () => jsonResponse({}, 502);
  await act(() => l.v.changeLanguage("en"));
  await act(() => flush());
  assert.equal(toasts.at(-1).detail, "Error: HTTP 502");

  answer = () => {
    throw new Error("unauthorised");
  };
  await act(() => l.v.changeLanguage("en"));
  await act(() => flush());
  assert.equal(toasts.at(-1).detail, "Error: unauthorised");
  assert.equal(toasts.length, 4);
  await l.done();
  await i18n.setLang("en");
});
