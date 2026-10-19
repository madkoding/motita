// useProjects, the projects' state: the New project dialog and what it sends, the git host
// connections and the identity a first clone may ask for, the edit dialog and its repositories,
// podman, and deleting a project. Runs the real hook against a fake gateway.
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

const RUNTIMES = {
  podman: true,
  podman_compose: true,
  podman_ready: true,
};

// setup mounts the hook. Every route not given answers as a healthy gateway would.
async function setup(routes = {}) {
  const api = fakeApi({
    "GET /v1/runtimes": res(200, RUNTIMES),
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

test("the machine's container runtimes are read on start", async () => {
  const s = await setup();
  assert.equal(s.p.hasPodman, true);
  assert.equal(s.p.hasPodmanCompose, true);
  assert.equal(s.p.podmanReady, true);
  await s.m.unmount();

  const off = await setup({
    "GET /v1/runtimes": () => {
      throw new Error("down");
    },
  });
  assert.equal(off.p.hasPodman, false);
  assert.equal(off.p.podmanReady, false);
  await off.m.unmount();
});

test("an answer about runtimes that arrives after the page is gone is dropped", async () => {
  let answer;
  let fail;
  const s = await setup({
    "GET /v1/runtimes": () =>
      new Promise((ok, no) => {
        answer = ok;
        fail = no;
      }),
  });
  // Opening the dialog asks again; the first answer is for a gone effect.
  const first = answer;
  await run(() => s.p.openNewProject());
  first(res(200, RUNTIMES));
  await flush();
  assert.equal(s.p.hasPodman, false);
  const second = fail;
  await s.m.unmount();
  second(new Error("late"));
  await flush();
});

test("the New project dialog opens on its first screen and lists the hosts", async () => {
  const accounts = [{ id: "github", name: "GitHub", connected: true }];
  const s = await setup({
    "GET /v1/git/accounts": res(200, { accounts }),
  });
  assert.equal(s.p.gitAccounts, null, "not read until the dialog opens");
  await run(() => s.p.openNewProject());
  await flush();
  assert.equal(s.p.showNewProject, true);
  assert.equal(s.p.newProjectMode, null);
  assert.equal(s.p.newProjectMain, "main");
  assert.deepEqual(s.p.gitAccounts, accounts);
  await run(() => s.p.closeNewProject());
  assert.equal(s.p.showNewProject, false);
  await s.m.unmount();
});

test("hosts that cannot be read count as none; a late answer is dropped", async () => {
  const s = await setup({
    "GET /v1/git/accounts": res(500, { error: "x" }),
  });
  await run(() => s.p.openNewProject());
  await flush();
  assert.deepEqual(s.p.gitAccounts, []);
  await s.m.unmount();

  let ok;
  let no;
  let n = 0;
  const late = await setup({
    "GET /v1/git/accounts": () =>
      new Promise((a, b) => {
        n++;
        if (n === 1) {
          ok = a;
        } else {
          no = b;
        }
      }),
  });
  await run(() => late.p.openNewProject());
  await run(() => late.p.closeNewProject());
  ok(res(200, { accounts: [{ id: "x", connected: true }] }));
  await flush();
  assert.equal(late.p.gitAccounts, null);
  await run(() => late.p.openNewProject());
  await run(() => late.p.closeNewProject());
  no(new Error("late"));
  await flush();
  assert.equal(late.p.gitAccounts, null);
  await late.m.unmount();
});

test("an empty project is created with what was typed and a session opened in it", async () => {
  const s = await setup({
    "POST /v1/projects": res(201, { id: "p9" }),
  });
  const events = [];
  const onCreate = (e) => events.push(e.detail);
  window.addEventListener("motita:create-session", onCreate);
  await run(() => s.p.openNewProject());
  await run(() => {
    s.p.setNewProjectMode("empty");
    s.p.setNewProjectTitle("  My App ");
    s.p.setNewProjectDesc(" about it ");
    s.p.setNewProjectGit("https://x.io/u/ignored.git");
    s.p.setNewProjectPodman(true);
  });
  await run(() => s.p.createProject());
  await flush();
  const [call] = callsTo(s, "POST /v1/projects");
  assert.deepEqual(call.body, {
    title: "My App",
    description: "about it",
    dir: "my-app",
    use_podman: true,
    main_branch: "main",
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

test("nothing is sent without a name", async () => {
  const s = await setup();
  await run(() => s.p.openNewProject());
  await run(() => s.p.setNewProjectMode("empty"));
  await run(() => s.p.createProject());
  assert.equal(callsTo(s, "POST").length, 0);
  // A name that makes no folder sends nothing either.
  await run(() => s.p.setNewProjectTitle("!!!"));
  await run(() => s.p.createProject());
  assert.equal(callsTo(s, "POST").length, 0);
  await s.m.unmount();
});

test("a project from a repository clones it, adds the other repositories and says so", async () => {
  let repoCall = 0;
  const s = await setup({
    "GET /v1/runtimes": res(200, {}),
    "POST /v1/projects/p1/repos": () => {
      repoCall++;
      if (repoCall === 1) {
        return res(200, { id: "p1" });
      }
      if (repoCall === 2) {
        return res(409, { error: "exists" });
      }
      throw new Error("offline");
    },
    "POST /v1/projects": res(201, { id: "p1", clone_log: "Cloning…" }),
  });
  await run(() => s.p.openNewProject());
  await run(() => {
    s.p.setNewProjectMode("git");
    s.p.setNewProjectGit(" https://github.com/u/web-client.git ");
    s.p.setNewProjectDir(" custom ");
    s.p.setNewProjectPodman(true);
    s.p.chooseMain("master");
  });
  for (const name of ["api", "lib", "docs"]) {
    await run(() =>
      assert.equal(
        s.p.addNewProjectRepo({ name, git_url: "https://x.io/u/" + name }),
        true,
      ),
    );
  }
  await run(() => s.p.removeNewProjectRepo("docs"));
  await run(() =>
    s.p.addNewProjectRepo({ name: "web", git_url: "https://x.io/u/web" }),
  );
  assert.deepEqual(
    s.p.newProjectRepos.map((r) => r.name),
    ["api", "lib", "web"],
  );
  await run(() => s.p.createProject({ name: "Ada", email: "ada@example.com" }));
  await flush();
  const [call] = callsTo(s, "POST /v1/projects");
  assert.deepEqual(call.body, {
    title: "web-client",
    description: "",
    dir: "custom",
    git_url: "https://github.com/u/web-client.git",
    main_branch: "master",
    git_user_name: "Ada",
    git_user_email: "ada@example.com",
  });
  assert.deepEqual(
    callsTo(s, "POST /v1/projects/p1/repos").map((c) => c.body.name),
    ["api", "lib", "web"],
  );
  assert.deepEqual(s.toasts.at(-1), {
    message:
      'Project "web-client" created, but some repositories could not be added: lib, web',
    type: "warning",
  });
  const events = [];
  const onCreate = (e) => events.push(e.detail);
  window.addEventListener("motita:create-session", onCreate);
  await new Promise((r) => setTimeout(r, 150));
  window.removeEventListener("motita:create-session", onCreate);
  assert.deepEqual(events, [{ project_id: "p1" }]);
  await s.m.unmount();
});

test("a cloned project with no id reports the clone and opens no session", async () => {
  const s = await setup({
    "POST /v1/projects//repos": res(200, { id: "" }),
    "POST /v1/projects": res(201, { clone_log: "done" }),
  });
  const events = [];
  const onCreate = (e) => events.push(e.detail);
  window.addEventListener("motita:create-session", onCreate);
  await run(() => s.p.openNewProject());
  await run(() => {
    s.p.setNewProjectMode("git");
    s.p.setNewProjectGit("https://github.com/u/tool");
    s.p.setNewProjectTitle("Tool");
    s.p.addNewProjectRepo({ name: "x", git_url: "https://x.io/u/x" });
  });
  await run(() => s.p.createProject());
  await flush();
  assert.equal(callsTo(s, "POST /v1/projects//repos").length, 1);
  assert.deepEqual(s.toasts.at(-1), {
    message: 'Project "Tool" created and repo cloned',
    type: "success",
  });
  await new Promise((r) => setTimeout(r, 150));
  assert.deepEqual(events, []);
  window.removeEventListener("motita:create-session", onCreate);
  await s.m.unmount();
});

test("a clone refused for credentials connects the host, then sends the create again", async () => {
  let n = 0;
  const s = await setup({
    "POST /v1/projects": () => {
      n++;
      if (n === 1) {
        return res(409, { code: "git_auth_required", service: "gitlab" });
      }
      if (n === 2) {
        return res(409, { code: "git_auth_required" });
      }
      return res(201, { id: "p2" });
    },
  });
  await run(() => s.p.openNewProject());
  await run(() => {
    s.p.setNewProjectMode("git");
    s.p.setNewProjectGit("https://gitlab.com/u/app.git");
  });
  await run(() => s.p.createProject());
  await flush();
  assert.deepEqual(s.p.gitConnect, { service: "gitlab", after: "create" });
  assert.equal(s.p.creatingProject, false);
  await run(() => s.p.createProject());
  await flush();
  assert.deepEqual(s.p.gitConnect, { service: undefined, after: "create" });
  const before = s.api.calls.length;
  await run(() => s.p.onGitConnected("gitlab", "ada"));
  assert.deepEqual(s.toasts.at(-1), {
    message: "Connected as ada",
    type: "success",
  });
  await new Promise((r) => setTimeout(r, 5));
  await flush();
  assert.equal(n, 3, "the create was sent again");
  assert.ok(s.api.calls.length > before);
  assert.equal(s.p.showNewProject, false);
  await s.m.unmount();
});

test("a git with no user asks for one; other refusals are reported", async () => {
  let n = 0;
  const s = await setup({
    "POST /v1/projects": () => {
      n++;
      if (n === 1) {
        return res(409, { code: "git_identity_required" });
      }
      if (n === 2) {
        return res(400, { error: "bad dir" });
      }
      if (n === 3) {
        return res(500, new Error("not json"));
      }
      throw new Error("network down");
    },
  });
  await run(() => s.p.openNewProject());
  await run(() => {
    s.p.setNewProjectMode("empty");
    s.p.setNewProjectTitle("x");
  });
  await run(() => s.p.createProject());
  await flush();
  assert.equal(s.p.showGitIdentity, true);
  await run(() => {
    s.p.setGitUserName("Ada");
    s.p.setGitUserEmail("ada@x.io");
  });
  assert.equal(s.p.gitUserName, "Ada");
  assert.equal(s.p.gitUserEmail, "ada@x.io");
  await run(() => s.p.closeGitIdentity());
  assert.equal(s.p.showGitIdentity, false);
  await run(() => s.p.createProject());
  await flush();
  await run(() => s.p.createProject());
  await flush();
  await run(() => s.p.createProject());
  await flush();
  assert.deepEqual(s.states, [
    ["bad dir", true],
    ["could not create the project", true],
    ["could not create the project: Error: network down", true],
  ]);
  assert.equal(s.p.creatingProject, false);
  await s.m.unmount();
});

test("a connected host opens the picker when that was asked, and connecting can be asked again", async () => {
  const s = await setup();
  await run(() => s.p.connectAnother());
  assert.deepEqual(s.p.gitConnect, { after: "picker" });
  await run(() => s.p.onGitConnected("github", ""));
  assert.deepEqual(s.toasts.at(-1), { message: "Connected", type: "success" });
  assert.equal(s.p.showRepoPicker, true);
  assert.equal(s.p.gitRev, 1);
  await run(() => s.p.closeRepoPicker());
  assert.equal(s.p.showRepoPicker, false);
  await run(() => s.p.setShowRepoPicker(true));
  await run(() => s.p.pickerAuthRequired("bitbucket"));
  assert.equal(s.p.showRepoPicker, false);
  assert.deepEqual(s.p.gitConnect, { service: "bitbucket", after: "picker" });
  await run(() => s.p.closeGitConnect());
  assert.equal(s.p.gitConnect, null);
  // Connected from Settings: nothing more to do.
  await run(() => s.p.onGitConnected("github", "ada"));
  assert.equal(s.p.showRepoPicker, false);
  await s.m.unmount();
});

test("a repository picked fills the form, keeping what was already typed", async () => {
  const s = await setup();
  await run(() => s.p.openNewProject());
  await run(() => s.p.setShowRepoPicker(true));
  await run(() =>
    s.p.pickRepo({
      full_name: "u/site",
      clone_url: "https://github.com/u/site.git",
      description: "The site",
    }),
  );
  assert.equal(s.p.newProjectMode, "git");
  assert.equal(s.p.newProjectGit, "https://github.com/u/site.git");
  assert.equal(s.p.newProjectTitle, "site");
  assert.equal(s.p.newProjectDesc, "The site");
  assert.equal(s.p.showRepoPicker, false);
  await run(() => s.p.setNewProjectDesc(""));
  await run(() =>
    s.p.pickRepo({ full_name: "u/other", clone_url: "https://x.io/u/other" }),
  );
  assert.equal(s.p.newProjectTitle, "site");
  assert.equal(s.p.newProjectDesc, "");
  await s.m.unmount();
});

test("a repository chosen for the other repositories joins them, once", async () => {
  const s = await setup();
  await run(() => s.p.openNewProject());
  await run(() => s.p.setNewProjectGit("https://github.com/u/site.git"));
  await run(() => s.p.openOtherRepoPicker());
  assert.equal(s.p.showRepoPicker, true);
  assert.equal(s.p.pickForOthers, true);
  await run(() =>
    s.p.pickRepo({
      full_name: "u/api",
      clone_url: "https://github.com/u/api.git",
    }),
  );
  assert.deepEqual(s.p.newProjectRepos, [
    { name: "api", git_url: "https://github.com/u/api.git" },
  ]);
  // The main repository stays the one typed above.
  assert.equal(s.p.newProjectGit, "https://github.com/u/site.git");
  assert.equal(s.p.showRepoPicker, false);
  assert.equal(s.p.pickForOthers, false);

  // The same name again, or the main's, is refused with a word, not added twice.
  for (const url of ["https://x.io/o/api", "https://x.io/o/site"]) {
    await run(() => s.p.openOtherRepoPicker());
    await run(() => s.p.pickRepo({ full_name: "o/x", clone_url: url }));
  }
  assert.equal(s.p.newProjectRepos.length, 1);
  assert.deepEqual(s.toasts.at(-1), {
    message: "The project already has a repository with that name.",
    type: "warning",
  });

  // Closing the picker forgets what it was opened for.
  await run(() => s.p.openOtherRepoPicker());
  await run(() => s.p.closeRepoPicker());
  assert.equal(s.p.pickForOthers, false);
  await s.m.unmount();
});

test("a repository chosen while editing a project is added to it at once, unless its name is taken", async () => {
  const s = await setup({
    "GET /v1/projects/p1/branches": res(200, { branches: ["develop"] }),
    "POST /v1/projects/p1/repos": res(200, {
      ...PROJECT,
      repos: [...PROJECT.repos, { name: "web", git_url: "u/web" }],
    }),
  });
  await run(() => s.p.openEditProject(PROJECT));
  await flush();
  assert.equal(
    callsTo(s, "GET /v1/git/accounts").length,
    1,
    "the connected hosts are read when the edit dialog opens",
  );
  await run(() => s.p.openOtherRepoPicker());
  await run(() =>
    s.p.pickRepo({
      full_name: "u/web",
      clone_url: "https://github.com/u/web.git",
    }),
  );
  await flush();
  const calls = callsTo(s, "POST /v1/projects/p1/repos");
  assert.equal(calls.length, 1);
  assert.deepEqual(calls[0].body, {
    name: "web",
    git_url: "https://github.com/u/web.git",
  });
  assert.equal(s.p.showRepoPicker, false);
  assert.equal(
    s.p.newProjectRepos.length,
    0,
    "the New project list is untouched",
  );

  // The main repository's name and another one's are taken.
  for (const url of ["https://x.io/o/api", "https://x.io/o/motita"]) {
    await run(() => s.p.openOtherRepoPicker());
    await run(() => s.p.pickRepo({ full_name: "o/x", clone_url: url }));
  }
  assert.equal(callsTo(s, "POST /v1/projects/p1/repos").length, 1);
  assert.deepEqual(s.toasts.at(-1), {
    message: "The project already has a repository with that name.",
    type: "warning",
  });
  await s.m.unmount();
});

test("the main repository moves into a folder of its own, or says why not", async () => {
  let n = 0;
  const s = await setup({
    "DELETE /v1/projects/p1/main": () => {
      n++;
      if (n === 1) {
        return res(200, {
          ...PROJECT,
          git_url: "",
          repos: [
            { name: "motita", git_url: PROJECT.git_url },
            ...PROJECT.repos,
          ],
        });
      }
      if (n === 2) {
        return res(409, { error: "a session is running" });
      }
      throw new Error("down");
    },
  });
  await run(() => s.p.openEditProject(PROJECT));
  await flush();
  assert.equal(s.p.editProject.mainRepo.movable, true);
  await run(() => s.p.detachProjectMain());
  assert.equal(s.p.repoBusy, "");
  assert.equal(
    s.p.editProject.mainRepo,
    null,
    "the folder only holds repositories now",
  );
  assert.deepEqual(
    s.p.editProject.repos.map((r) => r.name),
    ["motita", "api"],
  );
  assert.equal(s.counts.projects, 1);

  await run(() => s.p.detachProjectMain());
  assert.deepEqual(s.toasts.at(-1), {
    message: "Could not move the main repository",
    type: "error",
    detail: "a session is running",
  });
  await run(() => s.p.detachProjectMain());
  assert.deepEqual(s.toasts.at(-1), {
    message: "Could not move the main repository",
    type: "error",
  });
  assert.equal(s.p.repoBusy, "");
  await s.m.unmount();
});

test("the remote's main branch is offered, unless the person chose one", async (t) => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  let answer = () => res(200, { branch: "develop" });
  const s = await setup({
    "GET /v1/git/default-branch": () => answer(),
  });
  const tick = () => act(() => t.mock.timers.tick(600));
  await run(() => s.p.openNewProject());
  await run(() => s.p.setNewProjectMode("git"));
  // Not a repository URL yet: nothing is asked.
  await run(() => s.p.setNewProjectGit("web"));
  await tick();
  await flush();
  assert.equal(callsTo(s, "GET /v1/git/default-branch").length, 0);

  await run(() => s.p.setNewProjectGit("https://github.com/u/web.git"));
  await tick();
  await flush();
  const [call] = callsTo(s, "GET /v1/git/default-branch");
  assert.equal(
    call.path,
    "/v1/git/default-branch?url=" +
      encodeURIComponent("https://github.com/u/web.git"),
  );
  assert.equal(s.p.remoteMain, "develop");
  assert.equal(s.p.newProjectMain, "develop");

  // Chosen by the person: a detected branch no longer replaces it.
  await run(() => s.p.chooseMain("main"));
  answer = () => res(200, { branch: "trunk" });
  await run(() => s.p.setNewProjectGit("https://github.com/u/web2.git"));
  await tick();
  await flush();
  assert.equal(s.p.remoteMain, "trunk");
  assert.equal(s.p.newProjectMain, "main");

  // No answer, an answer without a branch, a failure: the selector keeps what it has.
  for (const a of [
    () => res(404, {}),
    () => res(200, {}),
    () => {
      throw new Error("down");
    },
  ]) {
    answer = a;
    await run(() => s.p.setNewProjectGit("https://github.com/u/web3.git"));
    await run(() => s.p.setNewProjectGit("https://github.com/u/web4.git"));
    await tick();
    await flush();
    assert.equal(s.p.remoteMain, "");
    assert.equal(s.p.newProjectMain, "main");
  }
  await s.m.unmount();
});

test("a branch answer for a URL that changed meanwhile is dropped", async (t) => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  const pending = [];
  const s = await setup({
    "GET /v1/git/default-branch": () => new Promise((ok) => pending.push(ok)),
  });
  await run(() => s.p.openNewProject());
  await run(() => s.p.setNewProjectMode("git"));
  await run(() => s.p.setNewProjectGit("https://github.com/u/a.git"));
  await act(() => t.mock.timers.tick(600));
  // The URL changes while the gateway is answering the headers...
  await run(() => s.p.setNewProjectGit("https://github.com/u/b.git"));
  pending[0](res(200, { branch: "old" }));
  await flush();
  assert.equal(s.p.remoteMain, "");
  // ...or while the body is being read.
  await act(() => t.mock.timers.tick(600));
  let body;
  pending[1]({
    ok: true,
    status: 200,
    json: () => new Promise((ok) => (body = ok)),
  });
  await flush();
  await run(() => s.p.setNewProjectGit("https://github.com/u/c.git"));
  body({ branch: "older" });
  await flush();
  assert.equal(s.p.remoteMain, "");
  assert.equal(s.p.newProjectMain, "main");
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
  auto_continue: true,
  repos: [{ name: "api", git_url: "u/api", dir: "/ws/motita/api" }],
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
    main: "develop",
    branches: ["develop", "main", "feat"],
    current: "feat",
    mergeMethod: "squash",
    prMaxFixes: 3,
    autoMerge: true,
    autoContinue: true,
    dir: "/ws/motita",
    path: "",
    mainRepo: {
      name: "motita",
      source: "https://github.com/u/motita.git",
      movable: true,
    },
    repos: PROJECT.repos,
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
    main: "main",
    branches: ["main"],
    current: "",
    mergeMethod: "",
    prMaxFixes: 0,
    autoMerge: false,
    autoContinue: false,
    dir: "/ws/bare/",
    path: "",
    mainRepo: { name: "bare", source: "/ws/bare/", movable: false },
    repos: [],
  });
  // A project at the root folder is shown by that folder.
  await run(() => s.p.openEditProject({ id: "p2", title: "Root", dir: "/" }));
  await flush();
  assert.deepEqual(s.p.editProject.mainRepo, {
    name: "/",
    source: "/",
    movable: false,
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

test("a repository added in the edit dialog is cloned at once", async () => {
  let n = 0;
  const s = await setup({
    "GET /v1/projects/p1/branches": res(200, {}),
    "POST /v1/projects/p1/repos": () => {
      n++;
      if (n === 1) {
        return res(200, {
          ...PROJECT,
          repos: [...PROJECT.repos, { name: "web", git_url: "u/web" }],
        });
      }
      if (n === 2) {
        return res(409, { error: "web exists" });
      }
      if (n === 3) {
        return res(500, new Error("not json"));
      }
      throw new Error("down");
    },
  });
  await run(() => s.p.openEditProject(PROJECT));
  await flush();
  let added;
  await run(async () => {
    const p = s.p.addProjectRepo({ name: "web", git_url: "u/web" });
    assert.equal(s.p.repoBusy, "", "not yet re-rendered");
    added = await p;
  });
  assert.equal(added, true);
  assert.deepEqual(
    s.p.editProject.repos.map((r) => r.name),
    ["api", "web"],
  );
  assert.equal(s.p.repoBusy, "");
  assert.equal(s.counts.projects, 1);
  const [call] = callsTo(s, "POST /v1/projects/p1/repos");
  assert.deepEqual(call.body, { name: "web", git_url: "u/web" });

  for (const detail of ["web exists", "", undefined]) {
    await run(async () => {
      added = await s.p.addProjectRepo({ name: "web", git_url: "u/web" });
    });
    assert.equal(added, false);
    const toast = { message: "Could not add web", type: "error" };
    if (detail !== undefined) {
      toast.detail = detail;
    }
    assert.deepEqual(s.toasts.at(-1), toast);
  }
  await s.m.unmount();
});

test("a repository answer for a dialog that was closed meanwhile changes nothing there", async () => {
  let pending;
  const s = await setup({
    "GET /v1/projects/p1/branches": res(200, {}),
    "POST /v1/projects/p1/repos": () => new Promise((ok) => (pending = ok)),
  });
  await run(() => s.p.openEditProject(PROJECT));
  await flush();
  let done;
  await run(() => {
    done = s.p.addProjectRepo({ name: "web", git_url: "u/web" });
  });
  assert.equal(s.p.repoBusy, "+");
  await run(() => s.p.closeEditProject());
  pending(res(200, { ...PROJECT, repos: [] }));
  await run(() => done);
  assert.equal(s.p.editProject, null);
  assert.equal(s.counts.projects, 1, "the list is still read again");
  await s.m.unmount();
});

test("removing a repository asks first, and discards only when told to", async () => {
  let n = 0;
  const s = await setup({
    "GET /v1/projects/p1/branches": res(200, {}),
    "DELETE /v1/projects/p1/repos/api": () => {
      n++;
      if (n === 1) {
        return res(409, { error: "api has 2 changes" });
      }
      return res(200, { ...PROJECT, repos: [] });
    },
  });
  await run(() => s.p.openEditProject(PROJECT));
  await flush();
  await run(() => s.p.removeProjectRepo("api", false));
  await flush();
  assert.deepEqual(s.p.repoConflict, {
    name: "api",
    message: "api has 2 changes",
  });
  assert.equal(s.p.repoBusy, "");
  await run(() => s.p.removeProjectRepo("api", true));
  await flush();
  assert.equal(s.p.repoConflict, null);
  assert.deepEqual(s.p.editProject.repos, []);
  assert.deepEqual(
    callsTo(s, "DELETE").map((c) => c.path),
    ["/v1/projects/p1/repos/api", "/v1/projects/p1/repos/api?force=1"],
  );
  await s.m.unmount();
});

test("a removal that fails says so", async () => {
  let n = 0;
  const s = await setup({
    "GET /v1/projects/p1/branches": res(200, {}),
    "DELETE /v1/projects/p1/repos/api": () => {
      n++;
      if (n === 1) {
        return res(500, { error: "disk full" });
      }
      throw new Error("down");
    },
  });
  await run(() => s.p.openEditProject(PROJECT));
  await flush();
  await run(() => s.p.removeProjectRepo("api", false));
  await run(() => s.p.removeProjectRepo("api", false));
  assert.deepEqual(s.toasts, [
    { message: "Could not remove api", type: "error", detail: "disk full" },
    { message: "Could not remove api", type: "error" },
  ]);
  assert.equal(s.p.editProject.repos.length, 1);
  await s.m.unmount();
});

test("podman can be turned on or off for a project", async () => {
  let n = 0;
  const s = await setup({
    "PATCH /v1/projects/p1": () => {
      n++;
      if (n === 1) {
        return res(200, {});
      }
      if (n === 2) {
        return res(400, { error: "podman missing" });
      }
      if (n === 3) {
        return res(500, new Error("not json"));
      }
      throw new Error("down");
    },
  });
  for (let i = 0; i < 4; i++) {
    await run(() => s.p.setProjectPodman("p1", true));
  }
  assert.deepEqual(callsTo(s, "PATCH")[0].body, { podman: true });
  assert.equal(s.counts.projects, 1);
  assert.deepEqual(s.states, [
    ["podman missing", true],
    ["could not change the project", true],
    ["could not change the project", true],
  ]);
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

test("rescanning the notes asks the gateway, or says why it could not", async () => {
  let n = 0;
  const s = await setup({
    "POST /v1/notes/scan?again=1": () => {
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
    await run(() => s.p.rescanNotes());
  }
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
  await run(() => s.p.deleteProject("p1"));
  await run(() => s.p.deleteProject("p1"));
  await run(() => s.p.deleteProject("p1"));
  assert.deepEqual(s.states, [
    ["has work", true],
    ["could not delete the project", true],
  ]);
  assert.deepEqual(s.counts, { projects: 2, sessions: 2 });
  await s.m.unmount();
});

test("the workspace catalog is read when a dialog with repositories opens", async () => {
  const CATALOG = [
    { name: "web", git_url: "u/web", dir: "/w/repos/web", projects: [] },
  ];
  let answer = res(200, { repos: CATALOG });
  const s = await setup({
    "GET /v1/repos": () => answer,
    "GET /v1/projects/p1/branches": res(200, {}),
  });
  assert.equal(
    callsTo(s, "GET /v1/repos").length,
    0,
    "nothing is read while no dialog is open",
  );
  await run(() => s.p.openNewProject());
  await flush();
  assert.deepEqual(s.p.repoCatalog, CATALOG);

  // Closing the dialog empties the list, and a refused answer leaves it empty.
  await run(() => s.p.closeNewProject());
  assert.deepEqual(s.p.repoCatalog, []);
  answer = res(500, { error: "x" });
  await run(() => s.p.openNewProject());
  await flush();
  assert.equal(callsTo(s, "GET /v1/repos").length, 2);
  assert.deepEqual(s.p.repoCatalog, []);

  // An answer with no list is an empty catalog.
  await run(() => s.p.closeNewProject());
  answer = res(200, {});
  await run(() => s.p.openEditProject(PROJECT));
  await flush();
  assert.deepEqual(s.p.repoCatalog, []);
  await s.m.unmount();
});

test("a catalog answer that arrives after the dialog closed is not shown", async () => {
  const CATALOG = [
    { name: "web", git_url: "u/web", dir: "/w/repos/web", projects: [] },
  ];
  let release;
  const s = await setup({
    "GET /v1/repos": () =>
      new Promise((ok) => {
        release = () => ok(res(200, { repos: CATALOG }));
      }),
  });
  await run(() => s.p.openNewProject());
  await run(() => s.p.closeNewProject());
  release();
  await flush();
  await flush();
  assert.deepEqual(
    s.p.repoCatalog,
    [],
    "the answer was for a dialog that is gone",
  );
  await s.m.unmount();
});

test("a shared repository behind its main branch is brought up to date, or says why not", async () => {
  let n = 0;
  const s = await setup({
    "POST /v1/projects/p1/repos/api/sync": () => {
      n++;
      if (n === 1) {
        return res(200, {
          ...PROJECT,
          repos: [{ ...PROJECT.repos[0], shared: true, behind: 0 }],
        });
      }
      if (n === 2) {
        return res(409, { error: "the folder has commits of its own" });
      }
      throw new Error("down");
    },
  });
  await run(() =>
    s.p.openEditProject({
      ...PROJECT,
      repos: [{ ...PROJECT.repos[0], shared: true, behind: 2 }],
    }),
  );
  await flush();
  assert.equal(s.p.editProject.repos[0].behind, 2);
  await run(() => s.p.syncProjectRepo("api"));
  assert.equal(s.p.repoBusy, "");
  assert.equal(s.p.editProject.repos[0].behind, 0);
  assert.equal(s.counts.projects, 1);
  await run(() => s.p.syncProjectRepo("api"));
  assert.deepEqual(s.toasts.at(-1), {
    message: "Could not bring api up to date",
    type: "error",
    detail: "the folder has commits of its own",
  });
  await run(() => s.p.syncProjectRepo("api"));
  assert.deepEqual(s.toasts.at(-1), {
    message: "Could not bring api up to date",
    type: "error",
  });
  assert.equal(s.p.repoBusy, "");
  await s.m.unmount();
});

test("a shared repository is made independent, or says why not", async () => {
  let n = 0;
  const s = await setup({
    "POST /v1/projects/p1/repos/api/standalone": () => {
      n++;
      if (n === 1) {
        return res(200, {
          ...PROJECT,
          repos: [{ ...PROJECT.repos[0], shared: false }],
        });
      }
      return res(409, { error: "the folder has uncommitted changes" });
    },
  });
  await run(() =>
    s.p.openEditProject({
      ...PROJECT,
      repos: [{ ...PROJECT.repos[0], shared: true }],
    }),
  );
  await flush();
  await run(() => s.p.standaloneProjectRepo("api"));
  assert.equal(s.p.editProject.repos[0].shared, false);
  await run(() => s.p.standaloneProjectRepo("api"));
  assert.deepEqual(s.toasts.at(-1), {
    message: "Could not make api independent",
    type: "error",
    detail: "the folder has uncommitted changes",
  });
  await s.m.unmount();
});

test("a connection made for the other repositories' picker keeps adding to them; one given up leaves the main picker alone", async () => {
  const s = await setup();
  await run(() => s.p.openOtherRepoPicker());
  assert.equal(s.p.pickForOthers, true);
  await run(() => s.p.pickerAuthRequired("bitbucket"));
  // Connected: the picker comes back and still picks for the others.
  await run(() => s.p.onGitConnected("bitbucket", "ada"));
  await run(() => s.p.closeGitConnect());
  assert.equal(s.p.showRepoPicker, true);
  assert.equal(s.p.pickForOthers, true);
  // Given up halfway: the next picker opened for the main repository is for the main repository.
  await run(() => s.p.pickerAuthRequired("bitbucket"));
  await run(() => s.p.closeGitConnect());
  await run(() => s.p.openRepoPicker());
  assert.equal(s.p.showRepoPicker, true);
  assert.equal(s.p.pickForOthers, false);
  await s.m.unmount();
});

test("one operation on the repositories at a time: a second is ignored while the first runs", async () => {
  let release;
  const s = await setup({
    "DELETE /v1/projects/p1/main": () =>
      new Promise((ok) => {
        release = () => ok(res(200, { ...PROJECT, git_url: "" }));
      }),
    "POST /v1/projects/p1/repos/api/sync": res(200, PROJECT),
    "POST /v1/projects/p1/repos": res(201, PROJECT),
    "DELETE /v1/projects/p1/repos/api": res(200, PROJECT),
  });
  await run(() => s.p.openEditProject(PROJECT));
  await flush();
  let first;
  await act(async () => {
    first = s.p.detachProjectMain();
    await flush();
  });
  assert.equal(s.p.repoBusy, "*");
  await run(() => s.p.syncProjectRepo("api"));
  await run(() => s.p.removeProjectRepo("api", false));
  assert.equal(await s.p.addProjectRepo({ name: "x", git_url: "u/x" }), false);
  await run(() => s.p.detachProjectMain());
  assert.equal(s.p.repoBusy, "*", "the ignored ones did not free the field");
  assert.equal(callsTo(s, "POST").length, 0);
  assert.equal(callsTo(s, "DELETE /v1/projects/p1/repos").length, 0);
  await act(async () => {
    release();
    await first;
  });
  assert.equal(s.p.repoBusy, "");
  await s.m.unmount();
});
