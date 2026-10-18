// The offer to give a conversation a project: which repositories a talk names, what to offer for
// them, and what accepting does. Run with: node --test web/src/__tests__/cov-hint-logic.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { act, fakeApi, json, load, renderHook } from "./cov-chat-helpers.mjs";

const { repoMentions, repoKey, mentionedIn, projectHint, hintKey } =
  await load("projectHints.ts");
const { useProjectHint } = await load("useProjectHint.ts");

const user = (text, extra = {}) => ({ id: 1, role: "user", text, ...extra });

test("repositories are read from web and ssh addresses, once each", () => {
  const got = repoMentions(
    "clone https://github.com/ada/engine.git, then see " +
      "https://github.com/ada/engine/pull/12 and git@gitlab.com:ada/tools.git. " +
      "Also https://example.com and https://example.com/only",
  );
  assert.deepEqual(got, [
    { url: "https://github.com/ada/engine.git", name: "engine" },
    { url: "git@gitlab.com:ada/tools.git", name: "tools" },
  ]);
  assert.deepEqual(repoMentions("https://github.com/ada/lib."), [
    { url: "https://github.com/ada/lib", name: "lib" },
  ]);
  assert.deepEqual(repoMentions("nothing here"), []);
});

test("spellings of one address have one key", () => {
  const k = repoKey("https://GitHub.com/ada/engine.git/");
  assert.equal(k, "github.com/ada/engine");
  assert.equal(repoKey("git@github.com:ada/engine"), k);
  assert.equal(repoKey("ssh://git@github.com/ada/engine.git"), k);
});

test("only what the person wrote is read, the latest message first", () => {
  const got = mentionedIn([
    user("see https://github.com/ada/old"),
    { id: 2, role: "agent", text: "try https://github.com/ada/agent-said" },
    user("and https://github.com/ada/new"),
    user("https://github.com/ada/shown", { shown: "Applying suggestions" }),
  ]);
  assert.deepEqual(
    got.map((r) => r.name),
    ["new", "old"],
  );
});

const free = {
  id: "s1",
  title: "t",
  created: "",
  last_used: "",
  running: false,
};
const inProject = { ...free, project_id: "p1" };
const project = (id, extra = {}) => ({
  id,
  title: "Project " + id,
  dir: "/w/" + id,
  created: "",
  ...extra,
});
const talk = [user("work on https://github.com/ada/engine")];

test("a session with no project is offered a new one, or the one that has the repository", () => {
  assert.equal(projectHint([], free, []), null);
  assert.equal(projectHint(talk, undefined, []), null);
  assert.equal(projectHint([user("hello")], free, []), null);
  assert.deepEqual(projectHint(talk, free, [project("a")]), {
    kind: "create",
    repo: { url: "https://github.com/ada/engine", name: "engine" },
  });
  const byMain = project("a", { git_url: "git@github.com:ada/engine.git" });
  const byRepo = project("b", {
    repos: [{ name: "engine", git_url: "https://github.com/ada/engine.git" }],
  });
  for (const owner of [byMain, byRepo]) {
    const hint = projectHint(talk, free, [project("z"), owner]);
    assert.equal(hint.kind, "move");
    assert.equal(hint.projectId, owner.id);
    assert.equal(hint.projectTitle, owner.title);
  }
});

test("a session in a project is offered the repository the project lacks", () => {
  assert.equal(projectHint(talk, inProject, []), null);
  const lacking = project("p1", { git_url: "https://github.com/ada/other" });
  const hint = projectHint(talk, inProject, [lacking]);
  assert.equal(hint.kind, "add");
  assert.equal(hint.projectId, "p1");
  const has = project("p1", {
    repos: [{ name: "engine", git_url: "git@github.com:ada/engine.git" }],
  });
  assert.equal(projectHint(talk, inProject, [has]), null);
  assert.equal(hintKey("s1", hint), "s1|add|github.com/ada/engine");
});

// mount the hook over a gateway that answers by "METHOD path".
function mountHint({
  routes,
  session = free,
  projects = [],
  over = {},
  messages = talk,
  keep = false,
}) {
  const api = fakeApi((path, init) => {
    const key = (init?.method || "GET") + " " + path;
    const route = Object.keys(routes).find((k) => key.startsWith(k));
    return route ? routes[route](key, init) : undefined;
  });
  if (!keep) {
    localStorage.removeItem("motita.ui.projectHintsTurnedDown");
  }
  const toasts = [];
  const refreshed = [];
  const props = {
    api,
    rs: {
      sessionId: "s1",
      running: false,
      noSession: false,
      messages,
      ...over,
    },
    lists: {
      sessions: [session],
      projects,
      fetchProjects: async () => refreshed.push("projects"),
      fetchSessions: async () => refreshed.push("sessions"),
    },
    setToast: (t) => toasts.push(t),
  };
  return renderHook((p) => useProjectHint(p), props).then((hook) => ({
    hook,
    api,
    toasts,
    refreshed,
    props,
  }));
}

const settle = () => act(() => new Promise((r) => setTimeout(r, 0)));

test("accepting a new project creates it, moves the session and indexes it", async () => {
  const { hook, api, toasts, refreshed } = await mountHint({
    routes: {
      "POST /v1/projects/pnew/code-index": () => json({}),
      "POST /v1/projects": () => json({ id: "pnew" }, 201),
      "POST /v1/sessions/s1/project": () => json({}),
    },
  });
  assert.equal(hook.result.current.hint.kind, "create");
  await act(() => hook.result.current.accept());
  await settle();
  assert.deepEqual(api.calls[0].body, {
    title: "engine",
    dir: "engine",
    git_url: "https://github.com/ada/engine",
  });
  assert.deepEqual(api.calls[1].body, { project_id: "pnew" });
  assert.equal(api.calls[2].path, "/v1/projects/pnew/code-index");
  assert.equal(toasts[0].type, "success");
  assert.match(toasts[0].message, /engine/);
  assert.deepEqual(refreshed, ["projects", "sessions"]);
  assert.equal(hook.result.current.busy, false);
  await hook.unmount();
});

test("a project whose session could not be moved says so", async () => {
  const { hook, toasts } = await mountHint({
    routes: {
      "POST /v1/projects": () => json({ id: "pnew" }, 201),
      "POST /v1/sessions/s1/project": () => json({ error: "busy" }, 409),
    },
  });
  await act(() => hook.result.current.accept());
  assert.equal(toasts[0].type, "error");
  assert.match(
    toasts[0].detail,
    /created, but the session was not moved: busy/,
  );
  await hook.unmount();
});

test("a refused project shows the gateway's reason, and an unreadable refusal its status", async () => {
  const refused = await mountHint({
    routes: { "POST /v1/projects": () => json({ error: "no clone" }, 409) },
  });
  await act(() => refused.hook.result.current.accept());
  assert.equal(refused.toasts[0].detail, "no clone");
  await refused.hook.unmount();
  const bare = await mountHint({
    routes: {
      "POST /v1/projects": () => new Response("x", { status: 502 }),
    },
  });
  await act(() => bare.hook.result.current.accept());
  assert.equal(bare.toasts[0].detail, "HTTP 502");
  await bare.hook.unmount();
});

test("moving into the project that has the repository only moves", async () => {
  const owner = project("a", { git_url: "https://github.com/ada/engine" });
  const { hook, api, toasts } = await mountHint({
    projects: [owner],
    routes: { "POST /v1/sessions/s1/project": () => json({}) },
  });
  assert.equal(hook.result.current.hint.kind, "move");
  await act(() => hook.result.current.accept());
  assert.equal(api.calls.length, 1);
  assert.deepEqual(api.calls[0].body, { project_id: "a" });
  assert.match(toasts[0].message, /Project a/);
  await hook.unmount();
});

test("adding a repository clones it into the project; an index that fails changes nothing", async () => {
  const lacking = project("p1");
  const { hook, api, toasts } = await mountHint({
    session: inProject,
    projects: [lacking],
    routes: {
      "POST /v1/projects/p1/repos": () => json({}, 201),
      "POST /v1/projects/p1/code-index": () => {
        throw new Error("off");
      },
    },
  });
  assert.equal(hook.result.current.hint.kind, "add");
  await act(() => hook.result.current.accept());
  await settle();
  assert.deepEqual(api.calls[0].body, {
    name: "engine",
    git_url: "https://github.com/ada/engine",
  });
  assert.equal(toasts[0].type, "success");
  await hook.unmount();
});

test("turning the offer down hides it for that conversation only", async () => {
  const { hook, props } = await mountHint({ routes: {} });
  await act(() => hook.result.current.dismiss());
  assert.equal(hook.result.current.hint, null);
  // Nothing to turn down, nothing to accept.
  await act(() => hook.result.current.dismiss());
  await act(() => hook.result.current.accept());
  await hook.rerender({ ...props, rs: { ...props.rs, sessionId: "s2" } });
  await hook.unmount();
});

test("no offer while a turn runs or when there is no session", async () => {
  for (const over of [{ running: true }, { noSession: true }]) {
    const { hook } = await mountHint({ routes: {}, over });
    assert.equal(hook.result.current.hint, null);
    await hook.unmount();
  }
});

test("a second press while it works does nothing", async () => {
  let release;
  const gate = new Promise((r) => (release = r));
  const { hook, api } = await mountHint({
    projects: [project("a", { git_url: "https://github.com/ada/engine" })],
    routes: {
      "POST /v1/sessions/s1/project": async () => {
        await gate;
        return json({});
      },
    },
  });
  const first = hook.result.current.accept();
  await settle();
  assert.equal(hook.result.current.busy, true);
  await act(() => hook.result.current.accept());
  release();
  await first;
  assert.equal(api.calls.length, 1);
  await hook.unmount();
});

test("owner/name and `name` read as the known repositories they say, and a path does not", () => {
  const known = [
    { url: "https://github.com/ada/engine.git", name: "engine" },
    { url: "git@github.com:ada/tools.git", name: "tools" },
  ];
  const got = (text) => mentionedIn([user(text)], known).map((r) => r.name);
  assert.deepEqual(got("fix ada/engine please"), ["engine"]);
  assert.deepEqual(got("look at `tools` again"), ["tools"]);
  assert.deepEqual(got("edit src/main and the engine folder"), []);
  assert.deepEqual(got("https://github.com/ada/engine/issues/3"), ["engine"]);
  const lib = project("a", { git_url: "https://github.com/ada/engine" });
  const hint = projectHint([user("ada/engine")], inProject, [
    project("p1"),
    lib,
  ]);
  assert.equal(hint.kind, "add");
  assert.equal(hint.repo.url, "https://github.com/ada/engine");
});

test("a named session with work and no repository is offered a project of its own", () => {
  const worked = { ...free, title: "Billing script", changes: 3 };
  const hint = projectHint([user("hello")], worked, []);
  assert.deepEqual(hint, { kind: "start", title: "Billing script" });
  assert.equal(hintKey("s1", hint), "s1|start");
  for (const s of [
    { ...worked, changes: 0 },
    { ...worked, title: "New session" },
    { ...worked, project_id: "p1" },
  ]) {
    assert.equal(projectHint([user("hello")], s, []), null);
  }
});

test("a project of its own is created without a repository, with a folder name always", async () => {
  const worked = { ...free, title: "Billing script", changes: 3 };
  const a = await mountHint({
    session: worked,
    messages: [user("hello")],
    routes: {
      "POST /v1/projects/pnew/code-index": () => json({}),
      "POST /v1/projects": () => json({ id: "pnew" }, 201),
      "POST /v1/sessions/s1/project": () => json({}),
    },
  });
  await act(() => a.hook.result.current.accept());
  assert.deepEqual(a.api.calls[0].body, {
    title: "Billing script",
    dir: "billing-script",
  });
  await a.hook.unmount();
  const b = await mountHint({
    session: { ...worked, title: "¡¡¡" },
    messages: [user("hello")],
    routes: {
      "POST /v1/projects": () => json({ id: "pnew" }, 201),
      "POST /v1/sessions/s1/project": () => json({}),
    },
  });
  await act(() => b.hook.result.current.accept());
  assert.equal(b.api.calls[0].body.dir, "project-s1");
  await b.hook.unmount();
});

test("an index that is off is said, once the project is set up", async () => {
  const { hook, toasts } = await mountHint({
    routes: {
      "POST /v1/projects/pnew/code-index": () => json({ error: "off" }, 409),
      "POST /v1/projects": () => json({ id: "pnew" }, 201),
      "POST /v1/sessions/s1/project": () => json({}),
    },
  });
  await act(() => hook.result.current.accept());
  await settle();
  await settle();
  assert.equal(toasts.at(-1).type, "warning");
  assert.match(toasts.at(-1).message, /code index is off/);
  await hook.unmount();
});

test("an offer turned down is remembered by the browser, and a browser that cannot say forgets", async () => {
  localStorage.removeItem("motita.ui.projectHintsTurnedDown");
  const first = await mountHint({ routes: {} });
  await act(() => first.hook.result.current.dismiss());
  assert.deepEqual(
    JSON.parse(localStorage.getItem("motita.ui.projectHintsTurnedDown")),
    ["s1|create|github.com/ada/engine"],
  );
  await first.hook.unmount();
  const again = await mountHint({ routes: {}, keep: true });
  assert.equal(again.hook.result.current.hint, null);
  await again.hook.unmount();

  const real = Object.getOwnPropertyDescriptor(window, "localStorage");
  Object.defineProperty(window, "localStorage", {
    configurable: true,
    get() {
      throw new Error("blocked");
    },
  });
  const blockedGlobal = Object.getOwnPropertyDescriptor(
    globalThis,
    "localStorage",
  );
  Object.defineProperty(globalThis, "localStorage", {
    configurable: true,
    get() {
      throw new Error("blocked");
    },
  });
  try {
    const blocked = await mountHint({ routes: {}, keep: true });
    assert.equal(blocked.hook.result.current.hint.kind, "create");
    await act(() => blocked.hook.result.current.dismiss());
    assert.equal(blocked.hook.result.current.hint, null);
    await blocked.hook.unmount();
  } finally {
    Object.defineProperty(globalThis, "localStorage", blockedGlobal);
    Object.defineProperty(window, "localStorage", real);
    localStorage.removeItem("motita.ui.projectHintsTurnedDown");
  }
});
