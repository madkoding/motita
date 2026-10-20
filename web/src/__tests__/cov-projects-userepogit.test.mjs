// useRepoGit: the panel's state, the actions done at once to a repository inside a project's
// folder, and the requests handed to the agent. Runs the real hook against a fake gateway.
// Run with: node --test web/src/__tests__/cov-projects-userepogit.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import {
  bundle,
  h,
  act,
  mount,
  flush,
  res,
  fakeApi,
} from "./cov-projects-helpers.mjs";

const { AppCtx, useRepoGit } = await bundle({
  "useRepoGit.ts": ["useRepoGit"],
});

const REF = { projectId: "p1", repo: "group/web" };
const STATUS = { path: "group/web", branch: "main", changes: [], log: [] };

async function setup(routes = {}) {
  const api = fakeApi(routes);
  const s = { api, toasts: [], states: [], asked: [], opened: [], projects: 0 };
  function Harness() {
    s.g = useRepoGit({
      api,
      setState: (msg, err) => s.states.push([msg, err]),
      setToast: (t) => s.toasts.push(t),
      fetchProjects: async () => {
        s.projects++;
      },
      createSession: async (id) => {
        s.opened.push(id);
        return s.sessionFails ? undefined : "s1";
      },
      submit: (text) => s.asked.push(text),
    });
    return null;
  }
  s.m = await mount(AppCtx, null, h(Harness));
  return s;
}
const run = (fn) =>
  act(async () => {
    await fn();
  });

test("opening the panel reads what the repository holds, closing it forgets it", async () => {
  const s = await setup({
    "GET /v1/projects/p1/repo?repo=group%2Fweb": res(200, STATUS),
  });
  await run(() => s.g.openRepoGit(REF));
  await flush();
  assert.deepEqual(s.g.open, REF);
  assert.deepEqual(s.g.status, STATUS);
  await run(() => s.g.closeRepoGit());
  assert.equal(s.g.open, null);
  assert.equal(s.g.status, null);
  await s.m.unmount();
});

test("a repository that cannot be read says why, or says it could not", async () => {
  const s = await setup({
    "GET /v1/projects/p1/repo?repo=a": res(404, {
      error: "no such repository",
    }),
    "GET /v1/projects/p1/repo?repo=b": res(500, new Error("not json")),
    "GET /v1/projects/p1/repo?repo=c": () =>
      Promise.reject(new Error("offline")),
  });
  for (const repo of ["a", "b", "c"]) {
    await run(() => s.g.openRepoGit({ projectId: "p1", repo }));
    await flush();
  }
  assert.deepEqual(s.states, [
    ["no such repository", true],
    ["could not read the repository", true],
    ["could not read the repository", true],
  ]);
  await s.m.unmount();
});

test("an action says what it did, refreshes the projects and the panel", async () => {
  const s = await setup({
    "POST /v1/projects/p1/repo/pull?repo=group%2Fweb": res(200, {}),
    "POST /v1/projects/p1/repo/clean?repo=group%2Fweb": res(200, {
      deleted: ["old", "older"],
    }),
    "POST /v1/projects/p1/repo/stash?repo=group%2Fweb": res(
      200,
      "not an object",
    ),
    "GET /v1/projects/p1/repo?repo=group%2Fweb": res(200, STATUS),
  });
  let ok;
  await run(async () => {
    ok = await s.g.repoAction(REF, "pull");
  });
  assert.equal(ok, true);
  assert.deepEqual(s.toasts.at(-1), {
    message: "group/web is up to date",
    type: "success",
  });
  await run(() => s.g.repoAction(REF, "clean"));
  assert.equal(s.toasts.at(-1).message, "Deleted: old, older");
  await run(() => s.g.act(REF, "stash", {}));
  await flush();
  assert.equal(
    s.toasts.at(-1).message,
    "The changes of group/web are put aside",
  );
  assert.deepEqual(
    s.g.status,
    STATUS,
    "the panel is read again after an action",
  );
  assert.equal(s.projects, 3);
  const sent = s.api.calls.find((c) => c.path.includes("/clean"));
  assert.deepEqual(sent.body, {});
  await s.m.unmount();
});

test("a clean that deleted nothing says the normal thing", async () => {
  const s = await setup({
    "POST /v1/projects/p1/repo/clean": res(200, { deleted: [] }),
  });
  await run(() => s.g.repoAction(REF, "clean"));
  assert.equal(s.toasts.at(-1).message, "Merged branches of group/web deleted");
  await s.m.unmount();
});

test("a refused or failed action reports it and leaves the panel alone", async () => {
  const s = await setup({
    "POST /v1/projects/p1/repo/pull": res(409, {
      error: "uncommitted changes",
    }),
    "POST /v1/projects/p1/repo/main": res(500, new Error("not json")),
    "POST /v1/projects/p1/repo/stash": () =>
      Promise.reject(new Error("offline")),
  });
  let results = [];
  for (const a of ["pull", "main", "stash"]) {
    await run(async () => {
      results.push(await s.g.repoAction(REF, a));
    });
  }
  assert.deepEqual(results, [false, false, false]);
  assert.deepEqual(s.states, [
    ["uncommitted changes", true],
    ["the action failed", true],
    ["the action failed", true],
  ]);
  await run(() => s.g.act(REF, "pull"));
  assert.equal(
    s.g.status,
    null,
    "a failed action does not read the panel again",
  );
  await s.m.unmount();
});

test("what the agent is asked goes to the conversation of the project and names the repository", async () => {
  const s = await setup();
  for (const kind of ["commit", "push", "pr", "work"]) {
    await run(() => s.g.askAgent(REF, kind));
  }
  assert.deepEqual(s.opened, ["p1", "p1", "p1", "p1"]);
  assert.equal(s.asked.length, 4);
  assert.ok(s.asked.every((text) => text.includes("`group/web`")));
  assert.match(s.asked[0], /semantic commit message/);
  assert.match(s.asked[2], /pull request/);
  await s.m.unmount();
});

test("nothing is asked when no conversation could be opened", async () => {
  const s = await setup();
  s.sessionFails = true;
  await run(() => s.g.askAgent(REF, "push"));
  assert.deepEqual(s.opened, ["p1"]);
  assert.deepEqual(s.asked, []);
  await s.m.unmount();
});
