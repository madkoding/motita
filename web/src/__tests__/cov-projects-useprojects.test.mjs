// useProjects, the projects' state: the New project dialog and what it sends, the git host
// connection modal, the edit dialog, and deleting a project. Runs the real hook against a fake gateway.
// Run with: node --test web/src/__tests__/cov-projects-useprojects.test.mjs
import test, { mock } from "node:test";
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

const { AppCtx, useProjects } = await bundle({
  "useProjects.ts": ["useProjects"],
});

// setup mounts the hook. Every route not given answers as a healthy gateway would.
async function setup(routes = {}) {
  const api = fakeApi({
    "GET /v1/git/accounts": res(200, { accounts: [] }),
    ...routes,
  });
  const toasts = [];
  const states = [];
  const counts = { projects: 0, sessions: 0 };
  const lists = {
    fetchProjects: async () => {
      counts.projects++;
    },
    fetchSessions: async () => {
      counts.sessions++;
    },
  };
  const s = { api, toasts, states, counts };
  function Harness() {
    s.p = useProjects({
      api,
      lists,
      setState: (msg, err) => states.push([msg, err]),
      setToast: (t) => toasts.push(t),
    });
    return null;
  }
  s.m = await mount(AppCtx, null, h(Harness));
  await flush();
  return s;
}

const run = (fn) =>
  act(async () => {
    await fn();
  });

const callsTo = (s, prefix) =>
  s.api.calls.filter((c) => (c.method + " " + c.path).startsWith(prefix));

test("an empty project is created with what was typed and a session opened in it", async () => {
  const s = await setup({
    "POST /v1/projects": res(201, { id: "p9" }),
  });
  const events = [];
  const onCreate = (e) => events.push(e.detail);
  window.addEventListener("motita:create-session", onCreate);
  await run(() => s.p.openNewProject());
  await run(() => {
    s.p.setNewProjectTitle("  My App ");
    s.p.setNewProjectDesc(" about it ");
  });
  await run(() => s.p.createProject());
  await flush();
  const [call] = callsTo(s, "POST /v1/projects");
  assert.deepEqual(call.body, {
    title: "My App",
    description: "about it",
    dir: "my-app",
  });
  assert.equal(s.counts.projects, 1);
  assert.deepEqual(s.toasts.at(-1), {
    message: 'Project "My App" created',
    type: "success",
  });
  // The dialog is emptied and closed.
  assert.equal(s.p.showNewProject, false);
  assert.equal(s.p.newProjectTitle, "");
  assert.equal(s.p.creatingProject, false);
  await new Promise((r) => setTimeout(r, 150));
  assert.deepEqual(events, [{ project_id: "p9" }]);
  window.removeEventListener("motita:create-session", onCreate);
  await s.m.unmount();
});

test("a repository is cloned only in clone mode, named after it", async () => {
  const s = await setup({
    "POST /v1/projects": res(201, { id: "p3" }),
  });
  await run(() => s.p.openNewProject());
  await run(() => s.p.setNewProjectGitUrl(" https://h.io/o/Cool.Repo.git/ "));
  // In folder mode the address is hidden, so it is not sent and it names nothing.
  await run(() => s.p.createProject());
  assert.equal(callsTo(s, "POST /v1/projects").length, 0);
  await run(() => s.p.setNewProjectSource("clone"));
  await run(() => s.p.createProject());
  await flush();
  const [call] = callsTo(s, "POST /v1/projects");
  assert.equal(call.body.git_url, "https://h.io/o/Cool.Repo.git/");
  assert.equal(call.body.title, "Cool.Repo");
  assert.equal(call.body.dir, "cool-repo");
  // The dialog goes back to its folder default once the project exists.
  assert.equal(s.p.newProjectSource, "folder");
  assert.equal(s.p.newProjectGitUrl, "");
  await s.m.unmount();
});

test("switching to clone lets go of the folder chosen before", async () => {
  const s = await setup();
  await run(() => s.p.openNewProject());
  await run(() => {
    s.p.setNewProjectPath("/srv/x");
    s.p.setNewProjectSure(true);
  });
  await run(() => s.p.setNewProjectSource("folder"));
  assert.equal(s.p.newProjectPath, "/srv/x");
  await run(() => s.p.setNewProjectSource("clone"));
  assert.equal(s.p.newProjectPath, "");
  assert.equal(s.p.newProjectSure, false);
  await s.m.unmount();
});

test("cloning without an address sends nothing", async () => {
  const s = await setup();
  await run(() => s.p.openNewProject());
  await run(() => s.p.setNewProjectSource("clone"));
  await run(() => s.p.setNewProjectTitle("Named"));
  await run(() => s.p.createProject());
  assert.equal(callsTo(s, "POST").length, 0);
  await s.m.unmount();
});

test("nothing is sent without a name", async () => {
  const s = await setup();
  await run(() => s.p.openNewProject());
  await run(() => s.p.createProject());
  assert.equal(callsTo(s, "POST").length, 0);
  // A name that makes no folder sends nothing either.
  await run(() => s.p.setNewProjectTitle("!!!"));
  await run(() => s.p.createProject());
  assert.equal(callsTo(s, "POST").length, 0);
  await s.m.unmount();
});

test("a connected git host counts, and says who it is connected as", async () => {
  const s = await setup();
  assert.equal(s.p.gitConnect, null);
  await run(() => s.p.setGitConnect({ service: "github" }));
  assert.deepEqual(s.p.gitConnect, { service: "github" });
  const rev = s.p.gitRev;
  await run(() => s.p.onGitConnected("github", "ada"));
  assert.equal(s.p.gitRev, rev + 1);
  assert.deepEqual(s.toasts.at(-1), {
    message: "Connected as ada",
    type: "success",
  });
  await run(() => s.p.onGitConnected("github", ""));
  assert.equal(s.toasts.at(-1).message, "Connected");
  await run(() => s.p.closeGitConnect());
  assert.equal(s.p.gitConnect, null);
  await s.m.unmount();
});

test("a refused create is reported and the dialog stays open", async () => {
  const s = await setup({
    "POST /v1/projects": res(409, { error: "that folder is taken" }),
  });
  await run(() => s.p.openNewProject());
  await run(() => s.p.setNewProjectTitle("Taken"));
  await run(() => s.p.createProject());
  assert.deepEqual(s.states.at(-1), ["that folder is taken", true]);
  assert.equal(s.p.showNewProject, true);
  assert.equal(s.p.creatingProject, false);
  await s.m.unmount();
});

test("a create that cannot reach the gateway is reported", async () => {
  const s = await setup({
    "POST /v1/projects": () => {
      throw new Error("offline");
    },
  });
  await run(() => s.p.openNewProject());
  await run(() => s.p.setNewProjectTitle("Lost"));
  await run(() => s.p.createProject());
  assert.match(
    s.states.at(-1)[0],
    /could not create the project: Error: offline/,
  );
  assert.equal(s.p.creatingProject, false);
  await s.m.unmount();
});

const PROJECT = {
  id: "p1",
  title: "Motita",
  description: "the app",
  dir: "/ws/motita",
  git_url: "https://github.com/u/motita.git",
  main_branch: "develop",
  branch: "feat",
  merge_method: "squash",
  pr_max_fixes: 3,
  auto_merge: true,
  auto_update_pr: true,
  auto_continue: true,
};

test("the edit dialog starts from the project's values and the gateway's branches", async () => {
  const s = await setup({
    "GET /v1/projects/p1/branches": res(200, {
      branches: ["develop", "main", "feat"],
      main_branch: "develop",
      current: "feat",
    }),
  });
  await run(() => s.p.openEditProject(PROJECT));
  await flush();
  assert.deepEqual(s.p.editProject, {
    id: "p1",
    title: "Motita",
    description: "the app",
    git: true,
    main: "develop",
    branches: ["develop", "main", "feat"],
    current: "feat",
    mergeMethod: "squash",
    prMaxFixes: 3,
    autoMerge: true,
    autoUpdatePR: true,
    autoContinue: true,
    dir: "/ws/motita",
    path: "",
  });
  await s.m.unmount();
});

test("a project with no settings yet gets the defaults", async () => {
  const s = await setup({
    "GET /v1/projects/p2/branches": res(200, {}),
  });
  await run(() =>
    s.p.openEditProject({ id: "p2", title: "Bare", dir: "/ws/bare/" }),
  );
  await flush();
  assert.deepEqual(s.p.editProject, {
    id: "p2",
    title: "Bare",
    description: "",
    git: false,
    main: "main",
    branches: ["main"],
    current: "",
    mergeMethod: "",
    prMaxFixes: 0,
    autoMerge: false,
    autoUpdatePR: false,
    autoContinue: false,
    dir: "/ws/bare/",
    path: "",
  });
  await s.m.unmount();
});

test("branches that cannot be read, or arrive too late, leave the dialog as it is", async () => {
  let pending;
  let n = 0;
  const s = await setup({
    "GET /v1/projects/p1/branches": () => {
      n++;
      if (n === 1) {
        return res(500, {});
      }
      if (n === 2) {
        throw new Error("down");
      }
      return new Promise((ok) => (pending = ok));
    },
  });
  await run(() => s.p.openEditProject(PROJECT));
  await flush();
  assert.deepEqual(s.p.editProject.branches, ["develop"]);
  await run(() => s.p.openEditProject(PROJECT));
  await flush();
  assert.deepEqual(s.p.editProject.branches, ["develop"]);
  await run(() => {
    void s.p.openEditProject(PROJECT);
  });
  await run(() => s.p.closeEditProject());
  pending(res(200, { branches: ["x"] }));
  await flush();
  assert.equal(s.p.editProject, null);
  // A change with the dialog closed changes nothing.
  await run(() => s.p.patchEditProject({ title: "nope" }));
  assert.equal(s.p.editProject, null);
  await s.m.unmount();
});

test("saving a plain folder sends no git settings", async () => {
  const s = await setup({
    "GET /v1/projects/p2/branches": res(200, {}),
    "PATCH /v1/projects/p2": res(200, {}),
  });
  await run(() =>
    s.p.openEditProject({ id: "p2", title: "Bare", dir: "/ws/bare/" }),
  );
  await flush();
  await run(() => s.p.saveEditProject());
  await flush();
  const [call] = callsTo(s, "PATCH /v1/projects/p2");
  assert.deepEqual(call.body, { title: "Bare", description: "" });
  await s.m.unmount();
});

test("saving sends the dialog, reads the list again and says so", async () => {
  const s = await setup({
    "GET /v1/projects/p1/branches": res(200, { branches: ["develop"] }),
    "PATCH /v1/projects/p1": res(200, {}),
  });
  await run(() => s.p.openEditProject(PROJECT));
  await flush();
  await run(() =>
    s.p.patchEditProject({ title: "Renamed", mergeMethod: "rebase" }),
  );
  await run(() => s.p.saveEditProject());
  await flush();
  const [call] = callsTo(s, "PATCH /v1/projects/p1");
  assert.deepEqual(call.body, {
    title: "Renamed",
    description: "the app",
    main_branch: "develop",
    merge_method: "rebase",
    pr_max_fixes: 3,
    auto_merge: true,
    auto_update_pr: true,
    auto_continue: true,
  });
  assert.equal(s.p.editProject, null);
  assert.equal(s.p.savingProject, false);
  assert.equal(s.counts.projects, 1);
  assert.deepEqual(s.toasts.at(-1), {
    message: "Project saved",
    type: "success",
  });
  await s.m.unmount();
});

test("a blank title or a closed dialog saves nothing; a refused save says why", async () => {
  let n = 0;
  const s = await setup({
    "GET /v1/projects/p1/branches": res(200, {}),
    "PATCH /v1/projects/p1": () => {
      n++;
      if (n === 1) {
        return res(400, { error: "title taken" });
      }
      if (n === 2) {
        return res(500, new Error("not json"));
      }
      throw new Error("down");
    },
  });
  await run(() => s.p.saveEditProject());
  assert.equal(callsTo(s, "PATCH").length, 0);
  await run(() => s.p.openEditProject(PROJECT));
  await flush();
  await run(() => s.p.patchEditProject({ title: "  " }));
  await run(() => s.p.saveEditProject());
  assert.equal(callsTo(s, "PATCH").length, 0);
  await run(() => s.p.patchEditProject({ title: "T" }));
  for (let i = 0; i < 3; i++) {
    await run(() => s.p.saveEditProject());
    await flush();
  }
  assert.deepEqual(s.toasts, [
    {
      message: "Could not save the project",
      type: "error",
      detail: "title taken",
    },
    { message: "Could not save the project", type: "error", detail: undefined },
    { message: "Could not save the project", type: "error" },
  ]);
  assert.notEqual(s.p.editProject, null, "the dialog stays open");
  assert.equal(s.p.savingProject, false);
  await s.m.unmount();
});

test("updating the main branch reads the projects again, or says why it could not", async () => {
  let n = 0;
  const s = await setup({
    "POST /v1/projects/p1/main/refresh": () => {
      n++;
      if (n === 1) {
        return res(200, { main_warning: "" });
      }
      if (n === 2) {
        return res(409, { error: "already updating" });
      }
      if (n === 3) {
        return res(500, new Error("not json"));
      }
      throw new Error("down");
    },
  });
  for (let i = 0; i < 4; i++) {
    await run(() => s.p.refreshProjectMain("p1"));
  }
  assert.equal(s.counts.projects, 1);
  assert.deepEqual(s.states, [
    ["already updating", true],
    ["could not update the main branch", true],
    ["could not update the main branch", true],
  ]);
  await s.m.unmount();
});

test("updating a repository inside the project names it, and shows the notice it comes back with", async () => {
  let n = 0;
  const s = await setup({
    "POST /v1/projects/p1/main/refresh?repo=group%2Fweb": () => {
      n++;
      if (n === 1) {
        return res(200, {
          main_warning: "main: the checkout has uncommitted changes",
        });
      }
      return res(200, { main_warning: "" });
    },
    "POST /v1/projects/p1/main/refresh": () => res(200, "not json"),
  });
  await run(() => s.p.refreshProjectMain("p1", "group/web"));
  await run(() => s.p.refreshProjectMain("p1", "group/web"));
  await run(() => s.p.refreshProjectMain("p1"));
  assert.equal(s.counts.projects, 3);
  assert.deepEqual(s.states, [
    ["main: the checkout has uncommitted changes", true],
  ]);
  await s.m.unmount();
});

test("rescanning the notes asks the gateway, or says why it could not", async () => {
  let n = 0;
  const s = await setup({
    "POST /v1/notes/scan?again=1&project=p%201": () => {
      n++;
      if (n === 1) {
        return res(202, { running: true });
      }
      if (n === 2) {
        return res(503, { error: "notes are off" });
      }
      if (n === 3) {
        return res(500, new Error("not json"));
      }
      throw new Error("down");
    },
  });
  for (let i = 0; i < 4; i++) {
    await run(() => s.p.rescanNotes("p 1"));
  }
  assert.deepEqual(s.toasts, [
    {
      message:
        "Rescanning the notes of this project in the background. The sidebar shows how far it got.",
      type: "success",
    },
  ]);
  assert.deepEqual(s.states, [
    ["notes are off", true],
    ["could not rescan the notes", true],
    ["could not rescan the notes", true],
  ]);
  await s.m.unmount();
});

test("deleting a project reads the lists again, or says why it was refused", async () => {
  let n = 0;
  const s = await setup({
    "DELETE /v1/projects/p1": () => {
      n++;
      if (n === 1) {
        return res(200, {});
      }
      if (n === 2) {
        return res(204, null);
      }
      if (n === 3) {
        return res(409, { error: "has work" });
      }
      if (n === 4) {
        return res(500, new Error("not json"));
      }
      throw new Error("down");
    },
  });
  await run(() => s.p.deleteProject("p1"));
  await run(() => s.p.deleteProject("p1", true));
  assert.deepEqual(
    callsTo(s, "DELETE").map((c) => c.path),
    ["/v1/projects/p1", "/v1/projects/p1?force=1"],
  );
  assert.deepEqual(s.counts, { projects: 2, sessions: 2 });
  await run(() => s.p.deleteProject("p1", true, true));
  await run(() => s.p.deleteProject("p1", false, true));
  await run(() => s.p.deleteProject("p1"));
  assert.deepEqual(
    callsTo(s, "DELETE")
      .map((c) => c.path)
      .slice(2),
    [
      "/v1/projects/p1?force=1&folder=delete",
      "/v1/projects/p1?folder=delete",
      "/v1/projects/p1",
    ],
  );
  assert.deepEqual(s.states, [
    ["has work", true],
    ["could not delete the project", true],
  ]);
  assert.deepEqual(s.counts, { projects: 2, sessions: 2 });
  await s.m.unmount();
});
