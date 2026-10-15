// The pull request bar and its toasts: when the bar is drawn, what it says about the pull request
// and its CI, what Create PR / Merge / Try again send, and which steps of the gateway's CI watch
// become a toast, a spoken notice or a browser notification.
// Run with: node --test web/src/__tests__/cov-git-prbar.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { installDom, loadSources, fakeApi, json, text } from "./cov-git-helpers.mjs";

installDom();
const { PRBar, usePullRequest, AppCtx } = await loadSources({
  PRBar: "PullRequestBar.tsx",
  usePullRequest: "PullRequestBar.tsx",
  AppCtx: "appStore.ts",
});
const { h, render } = await import("preact");
const { act } = await import("preact/test-utils");

const flush = () => new Promise((r) => setImmediate(r));
const settle = async () => {
  for (let i = 0; i < 4; i++) {
    await act(flush);
  }
};

const said = [];
window.addEventListener("motita-speech-notice", (e) => said.push(e.detail));

const sess = (o = {}) => ({
  id: "s1",
  title: "Work",
  project_id: "p1",
  running: false,
  created: "",
  last_used: "",
  ...o,
});

// makeStore is the part of the app store the bar and its hook read, recording what they do.
function makeStore({ session, sessions = [], sessionId = "s1" } = {}) {
  const log = {
    toasts: [],
    submits: [],
    gitConnect: [],
    running: [],
    states: [],
    follows: 0,
    fetches: 0,
    switches: [],
    continues: [],
  };
  const sessionRef = { current: sessionId };
  const base = {
    rs: {
      sessionId,
      sessionRef,
      runSessionRef: { current: "" },
      lastIdRef: { current: 5 },
      handoffRef: { current: false },
    },
    lists: {
      sessions,
      setRunningState: (v) => log.running.push(v),
      fetchSessions: async () => {
        log.fetches++;
      },
    },
    stream: {
      submit: async (...a) => {
        log.submits.push(a);
      },
      followReconnect: async () => {
        log.follows++;
      },
    },
    projects: { setGitConnect: (v) => log.gitConnect.push(v) },
    status: {
      setToast: (t) => log.toasts.push(t),
      setState: (s) => log.states.push(s),
    },
    selectedSession: session,
    conv: {
      switchSession: async (id) => {
        log.switches.push(id);
      },
    },
    actions: {
      continueSession: async (s) => {
        log.continues.push(s.id);
      },
    },
  };
  // next is the same store with some of its values changed, as the app would render it again.
  const next = (o = {}) => {
    if (o.sessionId !== undefined) {
      sessionRef.current = o.sessionId;
    }
    return {
      ...base,
      rs: { ...base.rs, sessionId: o.sessionId ?? base.rs.sessionId },
      lists: { ...base.lists, sessions: o.sessions ?? base.lists.sessions },
      selectedSession: "session" in o ? o.session : base.selectedSession,
    };
  };
  return { store: base, log, next };
}

let pr;
function Harness({ store, api }) {
  pr = usePullRequest(store, api);
  return h(AppCtx.Provider, { value: store }, h(PRBar, { pr }));
}

async function mount(routes, opts) {
  const f = fakeApi(routes);
  const s = makeStore(opts);
  const root = document.createElement("div");
  document.body.appendChild(root);
  const draw = async (store) => {
    await act(() => {
      render(h(Harness, { store, api: f.api }), root);
    });
    await settle();
  };
  await draw(s.store);
  const done = async () => {
    await act(() => render(null, root));
    root.remove();
  };
  return { root, f, ...s, draw, done };
}

const button = (root, label) =>
  [...root.querySelectorAll("button")].find((b) => text(b) === label);
const click = async (el) => {
  assert.ok(el, "the button is there");
  await act(() => {
    el.dispatchEvent(new MouseEvent("click", { bubbles: true }));
  });
  await settle();
};
const gets = (f) => f.calls.filter((c) => c.method === "GET").length;
const posts = (f, tail) =>
  f.calls.filter((c) => c.method === "POST" && c.path.endsWith(tail));
const view = (o = {}) => ({
  state: "open",
  branch: "b",
  pr: { number: 12, url: "https://host/pr/12", title: "Add things" },
  ...o,
});

// ── when the bar is drawn ────────────────────────────────────────────────────

test("no bar and no question for a session outside a project, or none at all", async () => {
  for (const session of [sess({ project_id: undefined, mergeable: true }), undefined]) {
    const m = await mount({}, { session });
    assert.equal(m.root.innerHTML, "");
    assert.equal(m.f.calls.length, 0);
    assert.equal(pr.prSessionId, "");
    await m.done();
  }
});

test("a project session with nothing to propose has no bar; with work in it, Create PR", async () => {
  const routes = { "GET /v1/sessions/": json(200, { state: "none", branch: "b" }) };
  let m = await mount(routes, { session: sess() });
  assert.equal(m.f.calls[0].path, "/v1/sessions/s1/pr");
  assert.equal(m.root.innerHTML, "");
  await m.done();

  m = await mount(routes, { session: sess({ mergeable: true }) });
  const create = button(m.root, "Create PR");
  assert.equal(create.disabled, false);
  assert.equal(
    create.title,
    "Commit, push and open a pull request with this session's work",
  );
  await m.draw(m.next({ session: sess({ mergeable: true, running: true }) }));
  assert.equal(create.disabled, true);
  assert.equal(create.title, "Wait for the current turn to end");
  await m.done();
});

test("a host that cannot be asked hides the bar; a passing failure does not", async () => {
  for (const code of ["pr_no_remote", "pr_no_host"]) {
    const m = await mount(
      { "GET /v1/sessions/": json(409, { error: "no", code }) },
      { session: sess({ mergeable: true }) },
    );
    assert.equal(pr.prBlocked, true);
    assert.equal(m.root.innerHTML, "");
    await m.done();
  }
  const m = await mount(
    { "GET /v1/sessions/": json(503, { error: "host down" }) },
    { session: sess({ mergeable: true }) },
  );
  assert.equal(pr.prBlocked, false);
  assert.ok(button(m.root, "Create PR"));
  await m.done();
});

// ── Create PR ────────────────────────────────────────────────────────────────

test("Create PR asks the host first, asks to notify once, then has the agent open it", async () => {
  let requested = 0;
  globalThis.Notification = class {
    static permission = "default";
    static requestPermission() {
      requested++;
      return Promise.resolve("granted");
    }
  };
  const m = await mount(
    { "GET /v1/sessions/": json(200, { state: "none", branch: "b" }) },
    { session: sess({ mergeable: true }) },
  );
  await click(button(m.root, "Create PR"));
  assert.equal(requested, 1);
  assert.deepEqual(m.log.submits, [["Create PR", "/v1/sessions/s1/pr"]]);

  // Once answered, the permission is not asked again.
  globalThis.Notification.permission = "granted";
  await click(button(m.root, "Create PR"));
  assert.equal(requested, 1);
  assert.equal(m.log.submits.length, 2);
  delete globalThis.Notification;
  await m.done();
});

test("Create PR on a host that is not connected opens the connect dialog instead", async () => {
  // A browser whose Notification API throws is no reason to fail.
  globalThis.Notification = class {
    static get permission() {
      throw new Error("blocked");
    }
  };
  let answer = json(200, { state: "none", branch: "b" });
  const m = await mount(
    { "GET /v1/sessions/": () => answer },
    { session: sess({ mergeable: true }) },
  );
  answer = json(409, { error: "login", code: "git_auth_required", service: "gitlab" });
  await click(button(m.root, "Create PR"));
  answer = json(409, { error: "login", code: "git_auth_required" });
  await click(button(m.root, "Create PR"));
  assert.deepEqual(m.log.gitConnect, [{ service: "gitlab" }, { service: undefined }]);
  delete globalThis.Notification;

  // Without the Notification API at all, any other refusal is a toast.
  answer = json(500, { error: "host exploded" });
  await click(button(m.root, "Create PR"));
  assert.deepEqual(m.log.toasts, [
    {
      message: "Could not create the pull request",
      type: "error",
      detail: "host exploded",
    },
  ]);
  assert.equal(m.log.submits.length, 0);
  await m.done();
});

// ── an open pull request ─────────────────────────────────────────────────────

test("an open pull request links to it and says how its CI is going", async () => {
  let v = view({
    ci: {
      state: "pending",
      checks: [
        { name: "a", state: "success" },
        { name: "b", state: "pending" },
        { name: "c", state: "pending" },
      ],
    },
  });
  const m = await mount(
    { "GET /v1/sessions/": () => json(200, v) },
    { session: sess() },
  );
  const a = m.root.querySelector("a");
  assert.equal(text(a), "Pull request #12");
  assert.equal(a.href, "https://host/pr/12");
  assert.equal(a.title, "Add things");
  assert.match(text(m.root), /CI running 1\/3/);

  // Asked again when the agent's turn ends.
  v = view({ pr: { number: 12, url: "u" }, ci: { state: "none", checks: [] } });
  await m.draw(m.next({ session: sess({ running: true }) }));
  await m.draw(m.next({ session: sess({ running: false }) }));
  assert.equal(gets(m.f), 2);
  assert.equal(m.root.querySelector("a").title, "");
  assert.match(text(m.root), /No CI checks yet/);

  // A pull request without CI information shows only its link.
  v = view();
  await m.draw(m.next({ session: sess({ running: true }) }));
  await m.draw(m.next({ session: sess({ running: false }) }));
  assert.equal(text(m.root), "Pull request #12");
  await m.done();
});

test("an open view without its pull request falls back to Create PR", async () => {
  const m = await mount(
    { "GET /v1/sessions/": json(200, { state: "open", branch: "b" }) },
    { session: sess() },
  );
  assert.ok(button(m.root, "Create PR"));
  await m.done();
});

test("a green CI: Merge asks twice, losing focus disarms it, and a merge refreshes", async () => {
  let mergeAnswer = json(409, { error: "branch protected" });
  const m = await mount(
    {
      "GET /v1/sessions/": json(
        200,
        view({ ci: { state: "success", checks: [] }, merge: { code: "ok" } }),
      ),
      "POST /v1/sessions/": () => mergeAnswer,
    },
    { session: sess() },
  );
  assert.match(text(m.root), /CI passed — ready to merge/);
  const merge = button(m.root, "Merge");
  assert.equal(merge.title, "Merge this pull request on the host");
  await click(merge);
  assert.equal(text(merge), "Click again to merge");
  assert.equal(posts(m.f, "/merge").length, 0);
  await act(() => {
    // Preact listens for onBlur as focusout.
    merge.dispatchEvent(new window.FocusEvent("focusout", { bubbles: true }));
  });
  assert.equal(text(merge), "Merge");

  await click(merge);
  await click(merge);
  assert.equal(posts(m.f, "/merge").length, 1);
  assert.deepEqual(m.log.toasts, [
    {
      message: "Could not merge the pull request",
      type: "error",
      detail: "branch protected",
    },
  ]);
  assert.equal(text(merge), "Merge");

  mergeAnswer = json(200, {});
  const before = gets(m.f);
  await click(merge);
  await click(merge);
  assert.equal(posts(m.f, "/merge").length, 2);
  assert.equal(gets(m.f), before + 1, "the bar asks again after the merge");
  assert.equal(m.log.fetches, 1);
  await m.done();
});

test("a green CI the host would not merge says why", async () => {
  const cases = [
    [{ code: "blocked" }, "Waiting for approvals or required checks", false],
    [{ code: "conflict" }, "Conflicts with the base branch", false],
    [{ code: "draft" }, "Still a draft", false],
    [{ code: "behind" }, "Out of date with the base branch", true],
    [undefined, "", true],
  ];
  for (const [merge, why, canMerge] of cases) {
    const m = await mount(
      {
        "GET /v1/sessions/": json(
          200,
          view({ ci: { state: "success", checks: [] }, merge }),
        ),
      },
      { session: sess() },
    );
    const label = m.root.querySelector(".text-\\[\\#f0a040\\]");
    assert.equal(text(label), why);
    assert.equal(!!button(m.root, "Merge"), canMerge, JSON.stringify(merge));
    await m.done();
  }
});

test("a red CI names the failed checks and what Motita does about it", async () => {
  const checks = [
    { name: "lint", state: "failure" },
    { name: "unit", state: "success" },
    { name: "e2e", state: "failure" },
  ];
  const m = await mount(
    { "GET /v1/sessions/": json(200, view({ ci: { state: "failure", checks } })) },
    { session: sess({ pr_watch: { status: "base_red", attempts: 1, max: 3 } }) },
  );
  const red = m.root.querySelector("[title='lint, e2e']");
  assert.equal(text(red), "CI failed");
  assert.match(text(m.root), /Also failing on the base branch — not caused by this change/);
  assert.equal(button(m.root, "Try again"), undefined);
  await m.done();

  const m2 = await mount(
    { "GET /v1/sessions/": json(200, view({ ci: { state: "failure" } })) },
    { session: sess({ pr_watch: { status: "fixing", attempts: 2, max: 3 } }) },
  );
  assert.match(text(m2.root), /CI failed — Motita is fixing it \(2\/3\)/);
  assert.equal(m2.root.querySelector("span[title]").title, "");
  await m2.done();
});

test("after Motita gave up, Try again sends it back and follows the run it started", async () => {
  let fix = json(200, { started: true });
  const gaveUp = sess({ pr_watch: { status: "gave_up", attempts: 3, max: 3 } });
  const m = await mount(
    {
      "GET /v1/sessions/": json(200, view({ ci: { state: "failure", checks: [] } })),
      "POST /v1/sessions/": () => fix,
    },
    { session: gaveUp },
  );
  assert.match(text(m.root), /CI failed — Motita gave up after 3 attempts/);
  const again = button(m.root, "Try again");
  assert.equal(again.disabled, false);
  await click(again);
  assert.equal(posts(m.f, "/fix").length, 1);
  assert.equal(m.store.rs.runSessionRef.current, "s1");
  assert.equal(m.store.rs.lastIdRef.current, 0);
  assert.deepEqual(m.log.running, [true]);
  assert.deepEqual(m.log.states, ["working"]);
  assert.equal(m.log.follows, 1);
  assert.equal(m.store.rs.handoffRef.current, false);

  // Queued behind a turn: the run is handed off instead of followed.
  fix = json(200, { started: false });
  await click(again);
  assert.equal(m.store.rs.handoffRef.current, true);
  assert.equal(m.log.follows, 1);

  // Started, but the user went to another session meanwhile: handed off too.
  m.store.rs.handoffRef.current = false;
  let release;
  fix = () =>
    new Promise((r) => {
      release = () => r(json(200, { started: true }));
    });
  m.f.routes["POST /v1/sessions/"] = fix;
  await click(again);
  assert.equal(pr.prBusy, true);
  assert.equal(again.disabled, true);
  m.store.rs.sessionRef.current = "elsewhere";
  await act(() => release());
  await settle();
  assert.equal(m.store.rs.handoffRef.current, true);
  assert.equal(m.log.follows, 1);
  m.store.rs.sessionRef.current = "s1";

  m.f.routes["POST /v1/sessions/"] = json(500, { error: "agent busy" });
  await click(again);
  assert.deepEqual(m.log.toasts.at(-1), {
    message: "Could not ask Motita to fix the CI",
    type: "error",
    detail: "agent busy",
  });
  assert.equal(pr.prBusy, false);

  // Not offered while the agent is working.
  await m.draw(m.next({ session: { ...gaveUp, running: true } }));
  assert.equal(button(m.root, "Try again").disabled, true);
  await m.done();
});

// ── a merged pull request ────────────────────────────────────────────────────

test("a merged pull request offers the session opened from it, or to continue in a new one", async () => {
  const merged = (o) =>
    sess({
      merged: true,
      pr_watch: { status: "merged", attempts: 0, max: 3, number: 7, ...o },
    });
  let m = await mount({}, { session: merged({ next: "s9" }) });
  // A merged session is not asked about its pull request any more.
  assert.equal(m.f.calls.length, 0);
  assert.match(text(m.root), /Pull request #7 merged/);
  const open = button(m.root, "Open the new session");
  assert.equal(open.title, "A new session was opened from the updated branch");
  await click(open);
  assert.deepEqual(m.log.switches, ["s9"]);
  await m.done();

  m = await mount({}, { session: { ...merged({}), continuable: true } });
  const cont = button(m.root, "Continue in a new session");
  assert.equal(cont.title, "Open a new session from the updated project branch");
  await click(cont);
  assert.deepEqual(m.log.continues, ["s1"]);
  await m.done();

  m = await mount({}, { session: merged({ number: undefined }) });
  assert.equal(text(m.root), "Pull request # merged");
  assert.equal(m.root.querySelector("button"), null);
  await m.done();

  // A session that merged by other means, with no watch saying so, has no bar.
  m = await mount({}, { session: sess({ merged: true, mergeable: true }) });
  assert.equal(m.root.innerHTML, "");
  await m.done();
});

// ── what the bar asks and when ───────────────────────────────────────────────

test("the bar asks again when the watch moves", async () => {
  const m = await mount(
    { "GET /v1/sessions/": json(200, view()) },
    { session: sess({ mergeable: true }) },
  );
  assert.equal(gets(m.f), 1);
  assert.equal(text(m.root.querySelector("a")), "Pull request #12");
  const watch = (attempts) =>
    sess({ mergeable: true, pr_watch: { status: "fixing", attempts, max: 3 } });
  await m.draw(m.next({ session: watch(1) }));
  assert.equal(gets(m.f), 2);
  await m.draw(m.next({ session: watch(1) }));
  assert.equal(gets(m.f), 2, "the same step is not asked twice");
  await m.draw(m.next({ session: watch(2) }));
  assert.equal(gets(m.f), 3);
  await m.done();
});

test("answers for a session the user already left are dropped", async () => {
  const pending = [];
  const m = await mount(
    {
      "GET /v1/sessions/": (path) =>
        new Promise((r) => {
          pending.push([path, r]);
        }),
    },
    { session: sess({ mergeable: true }) },
  );
  // The user moves to s2 before s1's answer comes.
  await m.draw(m.next({ sessionId: "s2", session: sess({ id: "s2", mergeable: true }) }));
  assert.deepEqual(pending.map(([p]) => p), ["/v1/sessions/s1/pr", "/v1/sessions/s2/pr"]);
  await act(() => pending[0][1](json(200, view({ pr: { number: 99, url: "u" } }))));
  await settle();
  assert.equal(pr.prView, null);
  assert.ok(button(m.root, "Create PR"));

  // And on to s3 before s2's refusal comes: s3 is not blocked by it.
  await m.draw(m.next({ sessionId: "s3", session: sess({ id: "s3", mergeable: true }) }));
  await act(() => pending[1][1](json(409, { code: "pr_no_remote" })));
  await settle();
  assert.equal(pr.prBlocked, false);
  assert.ok(button(m.root, "Create PR"));

  // s3's own answer is drawn.
  assert.equal(pending[2][0], "/v1/sessions/s3/pr");
  await act(() => pending[2][1](json(200, view({ pr: { number: 3, url: "u3" } }))));
  await settle();
  assert.equal(text(m.root.querySelector("a")), "Pull request #3");
  await m.done();
});

test("moving straight from one project session to another asks about the new one", async () => {
  const m = await mount(
    {
      "GET /v1/sessions/s1/": json(200, view()),
      "GET /v1/sessions/s2/": json(200, view({ pr: { number: 7, url: "u7" } })),
    },
    { session: sess({ mergeable: true }) },
  );
  assert.equal(text(m.root.querySelector("a")), "Pull request #12");
  // s1's view is on screen when s2 is opened: s2 is still asked, and its own pull request drawn.
  await m.draw(m.next({ sessionId: "s2", session: sess({ id: "s2", mergeable: true }) }));
  assert.deepEqual(
    m.f.calls.map((c) => c.path),
    ["/v1/sessions/s1/pr", "/v1/sessions/s2/pr"],
  );
  assert.equal(text(m.root.querySelector("a")), "Pull request #7");
  // Out to a session outside a project and back to s2: it is asked again.
  await m.draw(m.next({ sessionId: "s9", session: sess({ id: "s9", project_id: undefined }) }));
  await m.draw(m.next({ sessionId: "s2", session: sess({ id: "s2", mergeable: true }) }));
  assert.equal(m.f.calls.at(-1).path, "/v1/sessions/s2/pr");
  assert.equal(m.f.calls.length, 3);
  assert.equal(text(m.root.querySelector("a")), "Pull request #7");
  await m.done();
});

test("while the gateway follows the CI the bar asks every 20 seconds, and stops after", async (t) => {
  t.mock.timers.enable({ apis: ["setInterval"] });
  const following = sess({ pr_watch: { status: "following", attempts: 0, max: 3 } });
  const m = await mount(
    { "GET /v1/sessions/": json(200, view({ ci: { state: "pending", checks: [] } })) },
    { session: following },
  );
  const first = gets(m.f);
  await act(() => t.mock.timers.tick(20000));
  await settle();
  assert.equal(gets(m.f), first + 1);
  await act(() => t.mock.timers.tick(20000));
  await settle();
  assert.equal(gets(m.f), first + 2);

  await m.draw(
    m.next({ session: sess({ pr_watch: { status: "passed", attempts: 0, max: 3 } }) }),
  );
  const after = gets(m.f);
  await act(() => t.mock.timers.tick(60000));
  await settle();
  assert.equal(gets(m.f), after);
  await m.done();
});

// ── toasts ───────────────────────────────────────────────────────────────────

const W = (status, o = {}) => ({ status, attempts: 1, max: 3, number: 5, ...o });

test("each step of the watch is told once, with the session's name when it is not on screen", async () => {
  said.length = 0;
  const other = (w) => ({ id: "s2", title: "Other work", pr_watch: w });
  const m = await mount({}, { sessions: [other(W("following"))] });
  assert.deepEqual(m.log.toasts, [], "what happened before the tab looked is not news");

  const steps = [
    [W("passed"), "CI passed", "success", "Pull request #5 is green: you can go and merge it."],
    [W("fixing", { attempts: 2 }), "CI failed", "error", "Motita is fixing it — attempt 2 of 3."],
    [
      W("reviewing", { attempts: 2 }),
      "New review comments",
      "warning",
      "Motita is answering the comments on pull request #5 — attempt 2 of 3.",
    ],
    [
      W("resolving", { attempts: 2 }),
      "Merge conflict",
      "warning",
      "Pull request #5 conflicts with its base branch: Motita is resolving it — attempt 2 of 3.",
    ],
    [
      W("gave_up", { attempts: 3 }),
      "Motita could not fix the CI",
      "error",
      "It tried 3 times. Look at pull request #5 and try again when you are ready.",
    ],
    [W("no_ci"), "This repository has no CI", "warning", "Pull request #5 has no checks to wait for."],
    [
      W("base_red"),
      "The CI is red on the base branch too",
      "warning",
      "Pull request #5 fails the same checks as the branch it merges into: this change did not cause it.",
    ],
    [W("closed"), "Pull request closed", "warning", "Pull request #5 was closed without being merged."],
    [
      W("merged"),
      "Pull request merged",
      "success",
      "Pull request #5 was merged. Use Continue in the bar to keep working from the updated branch.",
    ],
  ];
  for (const [w, message, type, detail] of steps) {
    await m.draw(m.next({ sessions: [other(w)] }));
    assert.deepEqual(m.log.toasts.at(-1), {
      message: message + " — Other work",
      type,
      detail,
    });
    assert.equal(said.at(-1), `${message} — Other work. ${detail}`);
  }
  assert.equal(m.log.toasts.length, steps.length);
  // The same step seen again is not news.
  await m.draw(m.next({ sessions: [other(W("merged"))] }));
  assert.equal(m.log.toasts.length, steps.length);
  await m.done();
});

test("the session on screen is not named; one without a title is named by its id", async () => {
  const m = await mount(
    {},
    {
      sessions: [
        { id: "s1", title: "Here", pr_watch: null },
        { id: "s7", title: "", pr_watch: null },
        { id: "s8", title: "Quiet" },
      ],
    },
  );
  await m.draw(
    m.next({
      sessions: [
        { id: "s1", title: "Here", pr_watch: W("merged", { next: "s3", number: undefined }) },
        { id: "s8", title: "Quiet" },
      ],
    }),
  );
  assert.deepEqual(m.log.toasts, [
    {
      message: "Pull request merged",
      type: "success",
      detail: "Pull request  was merged. A new session was opened from the updated branch.",
    },
  ]);
  await m.draw(m.next({ sessions: [{ id: "s7", title: "", pr_watch: W("no_ci") }] }));
  assert.equal(m.log.toasts.at(-1).message, "This repository has no CI — s7");
  // The agent sent again is news at each new attempt, not when the same attempt is seen again.
  await m.draw(m.next({ sessions: [{ id: "s7", title: "", pr_watch: W("fixing", { attempts: 2 }) }] }));
  await m.draw(m.next({ sessions: [{ id: "s7", title: "", pr_watch: W("fixing", { attempts: 2 }) }] }));
  assert.equal(m.log.toasts.length, 3);
  assert.equal(m.log.toasts.at(-1).message, "CI failed — s7");
  // A status this page does not know is no toast.
  await m.draw(m.next({ sessions: [{ id: "s7", title: "", pr_watch: W("paused") }] }));
  assert.equal(m.log.toasts.length, 3);
  await m.done();
});

test("several pull requests moving at once are one toast, as loud as the loudest", async () => {
  const list = (a, b, c) => [
    { id: "a", title: "A", pr_watch: a },
    { id: "b", title: "B", pr_watch: b },
    { id: "c", title: "C", pr_watch: c },
  ];
  const f = W("following");
  const m = await mount({}, { sessions: list(f, f, f) });
  await m.draw(m.next({ sessions: list(W("passed"), W("no_ci"), f) }));
  assert.deepEqual(m.log.toasts.at(-1), {
    message: "2 pull requests moved",
    type: "warning",
    detail: "CI passed — A · This repository has no CI — B",
  });
  await m.draw(m.next({ sessions: list(W("passed"), W("no_ci"), W("gave_up")) }));
  assert.equal(m.log.toasts.at(-1).message, "Motita could not fix the CI — C");
  await m.draw(m.next({ sessions: list(W("closed"), W("merged"), W("fixing", { attempts: 2 })) }));
  assert.equal(m.log.toasts.at(-1).type, "error");
  await m.draw(m.next({ sessions: list(W("passed"), W("passed"), W("passed")) }));
  assert.deepEqual(m.log.toasts.at(-1), {
    message: "3 pull requests moved",
    type: "success",
    detail: "CI passed — A · CI passed — B · CI passed — C",
  });
  await m.done();
});

test("toasts can be turned off; a hidden tab gets a browser notification unless phone push is on", async () => {
  const shown = [];
  globalThis.Notification = class {
    static permission = "granted";
    constructor(title, options) {
      shown.push([title, options]);
    }
  };
  Object.defineProperty(document, "hidden", { value: true, configurable: true });
  localStorage.setItem("motita.ui.ciToast", "off");
  said.length = 0;
  const one = (w) => [{ id: "s2", title: "T", pr_watch: w }];
  const m = await mount({}, { sessions: one(W("following")) });
  await m.draw(m.next({ sessions: one(W("passed")) }));
  await settle();
  assert.deepEqual(m.log.toasts, [], "the toast is off");
  assert.equal(said.length, 1, "the notice is still spoken");
  assert.deepEqual(shown, [
    [
      "CI passed — T",
      { body: "Pull request #5 is green: you can go and merge it.", tag: "motita-pr" },
    ],
  ]);

  // With phone notifications on, the gateway pushes it: no second notification.
  localStorage.setItem("motita.ui.push", "on");
  await m.draw(m.next({ sessions: one(W("closed")) }));
  await settle();
  assert.equal(shown.length, 1);
  localStorage.removeItem("motita.ui.push");

  // A Notification API that throws is ignored.
  globalThis.Notification = class {
    static get permission() {
      throw new Error("no");
    }
  };
  await m.draw(m.next({ sessions: one(W("no_ci")) }));
  assert.equal(said.length, 3);

  delete globalThis.Notification;
  await m.draw(m.next({ sessions: one(W("base_red")) }));
  assert.equal(said.length, 4);
  localStorage.removeItem("motita.ui.ciToast");
  delete document.hidden;
  await m.done();
});
