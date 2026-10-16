// The folder browser for a project outside the workspace: the listing client, the browser dialog,
// the fields of the New and Edit project dialogs that use it, and what the projects store sends.
// Run with: node --test web/src/__tests__/cov-projects-folders.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import {
  bundle,
  h,
  act,
  mount,
  click,
  flush,
  res,
  fakeApi,
} from "./cov-projects-helpers.mjs";

const {
  AppCtx,
  FolderBrowser,
  listDirs,
  NewProjectModal,
  EditProjectModal,
  useProjects,
} = await bundle({
  "FolderBrowser.tsx": ["FolderBrowser"],
  "fsApi.ts": ["listDirs"],
  "NewProjectModal.tsx": ["NewProjectModal"],
  "EditProjectModal.tsx": ["EditProjectModal"],
  "useProjects.ts": ["useProjects"],
});

const HOME = {
  path: "/home/ada",
  parent: "/home",
  home: "/home/ada",
  root: "/",
  dirs: [
    { name: "code", path: "/home/ada/code", git: true },
    { name: "docs", path: "/home/ada/docs" },
  ],
};

test("the listing is read with the path encoded, and a refusal says why", async () => {
  const api = fakeApi({
    "GET /v1/fs/dirs?path=%2Fa%20b": res(200, HOME),
    "GET /v1/fs/dirs": (p) =>
      p === "/v1/fs/dirs"
        ? res(200, HOME)
        : res(403, { error: "outside the workspace" }),
  });
  assert.deepEqual(await listDirs(api, "/a b"), HOME);
  assert.deepEqual(await listDirs(api), HOME);
  assert.equal(api.calls[1].path, "/v1/fs/dirs");
  const refused = fakeApi({ "GET /v1/fs/dirs": res(403, { error: "no" }) });
  await assert.rejects(listDirs(refused, "/x"), /no/);
  const bare = fakeApi({ "GET /v1/fs/dirs": res(500, new Error("x")) });
  await assert.rejects(listDirs(bare), /HTTP 500/);
});

// browser mounts the folder browser over a gateway that lists `tree` (path to listing).
async function browser(tree, props = {}) {
  const log = [];
  const api = fakeApi({
    "GET /v1/fs/dirs": (path) => {
      const q = new URL(path, "http://x").searchParams.get("path") || "";
      const l = tree[q];
      return l instanceof Error ? res(500, { error: l.message }) : res(200, l);
    },
  });
  const m = await mount(
    AppCtx,
    null,
    h(FolderBrowser, {
      api,
      onPick: (p) => log.push(["pick", p]),
      onClose: () => log.push(["close"]),
      ...props,
    }),
  );
  await flush();
  return { m, api, log };
}

const rows = (m) => m.qa("[data-testid=folder-row]");

test("it opens on the home folder and lists only the folders, marking repositories", async () => {
  const { m, api } = await browser({ "": HOME });
  assert.equal(api.calls[0].path, "/v1/fs/dirs");
  assert.equal(m.q("[data-testid=folder-path]").textContent, "/home/ada");
  assert.deepEqual(
    rows(m).map((r) => r.textContent),
    ["code" + "git", "docs"],
  );
  assert.equal(m.q("[data-testid=folder-up]").disabled, false);
  assert.equal(m.q("[data-testid=folder-home]"), null, "already at home");
  await m.unmount();
});

test("walking down, up and home, then using the folder it is on", async () => {
  const code = {
    path: "/home/ada/code",
    parent: "/home/ada",
    home: "/home/ada",
    root: "/",
    dirs: [],
  };
  const { m, api, log } = await browser({
    "": HOME,
    "/home/ada/code": code,
    "/home/ada": HOME,
  });
  await click(rows(m)[0]);
  await flush();
  assert.equal(m.q("[data-testid=folder-path]").textContent, "/home/ada/code");
  assert.match(
    m.q("[data-testid=folder-list]")?.textContent ?? "No folders in here",
    /No folders in here/,
  );
  assert.match(m.root.textContent, /No folders in here/);
  assert.ok(m.q("[data-testid=folder-home]"));
  await click(m.q("[data-testid=folder-home]"));
  await flush();
  assert.equal(m.q("[data-testid=folder-path]").textContent, "/home/ada");
  await click(rows(m)[0]);
  await flush();
  await click(m.q("[data-testid=folder-up]"));
  await flush();
  assert.equal(api.calls.at(-1).path, "/v1/fs/dirs?path=%2Fhome%2Fada");
  await click(m.q("[data-testid=folder-pick]"));
  assert.deepEqual(log, [["pick", "/home/ada"]]);
  await m.unmount();
});

test("it opens on the folder it is told to, and the top of the disk has no way up", async () => {
  const top = { path: "/", root: "/", dirs: [] };
  const { m, api, log } = await browser({ "/": top }, { start: "/" });
  assert.equal(api.calls[0].path, "/v1/fs/dirs?path=%2F");
  assert.equal(m.q("[data-testid=folder-up]").disabled, true);
  assert.equal(m.q("[data-testid=folder-home]"), null, "no home to go to");
  const cancel = [...m.root.querySelectorAll("button")].find(
    (b) => b.textContent === "Cancel",
  );
  await click(cancel);
  await click(m.q("[aria-label=Close]"));
  assert.deepEqual(log, [["close"], ["close"]]);
  await m.unmount();
});

test("a folder that cannot be listed shows why, and the way back stays", async () => {
  const { m } = await browser({
    "": HOME,
    "/home/ada/docs": new Error("permission denied"),
  });
  await click(rows(m)[1]);
  await flush();
  assert.match(m.q("[role=alert]").textContent, /permission denied/);
  assert.equal(m.q("[data-testid=folder-list]"), null);
  assert.equal(m.q("[data-testid=folder-pick]").disabled, true);
  await click(m.q("[data-testid=folder-up]"));
  await flush();
  const first = await browser({ "": new Error("nope") });
  assert.equal(first.m.q("[data-testid=folder-path]").textContent, "…");
  assert.equal(first.m.q("[data-testid=folder-up]").disabled, true);
  assert.equal(first.m.q("[data-testid=folder-pick]").disabled, true);
  await first.m.unmount();
  await m.unmount();
});

test("an answer that arrives after the browser closed is ignored", async () => {
  const pending = [];
  const m = await mount(
    AppCtx,
    null,
    h(FolderBrowser, {
      api: async () => new Promise((resolve) => pending.push(resolve)),
      onPick() {},
      onClose() {},
    }),
  );
  await m.unmount();
  pending[0](res(200, HOME));
  await flush();
});

// store is a projects store for the dialogs; its actions are recorded.
function store(state = {}) {
  const log = [];
  const rec =
    (name) =>
    (...a) =>
      log.push([name, ...a.filter((x) => !(x instanceof Event))]);
  return {
    log,
    value: {
      projects: {
        newProjectMode: "empty",
        newProjectTitle: "",
        newProjectDesc: "",
        newProjectDir: "",
        newProjectPath: "",
        newProjectGit: "",
        newProjectMain: "main",
        remoteMain: "",
        newProjectRepos: [],
        gitAccounts: null,
        repoBusy: "",
        fullDisk: false,
        setNewProjectDir: rec("dir"),
        setNewProjectPath: rec("path"),
        openFolderPicker: rec("browse"),
        patchEditProject: rec("patch"),
        ...state,
      },
    },
  };
}

const DRAFT = {
  id: "p1",
  title: "Motita",
  description: "",
  main: "main",
  branches: ["main"],
  current: "main",
  mergeMethod: "",
  prMaxFixes: 0,
  autoMerge: false,
  autoContinue: false,
  dir: "/ws/motita",
  path: "",
  mainRepo: null,
  repos: [],
};

const byTest = (m, id) => m.q("[data-testid=" + id + "]");

test("a new project keeps the folder name field unless the whole disk is allowed", async () => {
  const s = store();
  const m = await mount(AppCtx, s.value, h(NewProjectModal));
  assert.equal(byTest(m, "new-project-location"), null);
  assert.match(m.root.textContent, /Folder name/);
  await m.unmount();
});

test("with the whole disk a new project can pick a folder, and go back to the workspace", async () => {
  const s = store({ fullDisk: true });
  const m = await mount(AppCtx, s.value, h(NewProjectModal));
  assert.ok(byTest(m, "new-project-location"));
  assert.match(
    m.root.textContent,
    /Folder name/,
    "still a folder in the workspace",
  );
  assert.equal(byTest(m, "new-project-path"), null);
  assert.equal(byTest(m, "new-project-path-clear"), null);
  await click(byTest(m, "new-project-browse"));
  await m.rerender(
    store({ fullDisk: true, newProjectPath: "/srv/app" }).value,
    h(NewProjectModal),
  );
  assert.equal(byTest(m, "new-project-path").textContent, "/srv/app");
  assert.doesNotMatch(m.root.textContent, /Folder name/);
  const again = store({ fullDisk: true, newProjectPath: "/srv/app" });
  await m.rerender(again.value, h(NewProjectModal));
  await click(byTest(m, "new-project-path-clear"));
  assert.deepEqual(s.log, [["browse", "new"]]);
  assert.deepEqual(again.log, [["path", ""]]);
  await m.unmount();
});

test("the edit dialog shows the folder, and with the whole disk another can be chosen", async () => {
  const s = store();
  const m = await mount(AppCtx, s.value, h(EditProjectModal, { p: DRAFT }));
  assert.equal(byTest(m, "edit-project-dir").textContent, "/ws/motita");
  assert.equal(byTest(m, "edit-project-browse"), null);

  const full = store({ fullDisk: true });
  await m.rerender(full.value, h(EditProjectModal, { p: DRAFT }));
  await click(byTest(m, "edit-project-browse"));
  assert.equal(byTest(m, "edit-project-path-clear"), null);
  await m.rerender(
    full.value,
    h(EditProjectModal, { p: { ...DRAFT, path: "/mnt/data/app" } }),
  );
  assert.equal(byTest(m, "edit-project-dir").textContent, "/mnt/data/app");
  await click(byTest(m, "edit-project-path-clear"));
  assert.deepEqual(full.log, [
    ["browse", "edit"],
    ["patch", { path: "" }],
  ]);
  await m.unmount();
});

// setup mounts the real hook against a fake gateway.
async function setup(routes = {}) {
  const api = fakeApi({
    "GET /v1/runtimes": res(200, { podman: false }),
    "GET /v1/git/accounts": res(200, { accounts: [] }),
    ...routes,
  });
  const s = { api, toasts: [] };
  function Harness() {
    s.p = useProjects({
      api,
      lists: { fetchProjects: async () => {}, fetchSessions: async () => {} },
      setState() {},
      setToast: (t) => s.toasts.push(t),
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

test("the whole disk is asked about only while a project dialog is open", async () => {
  const s = await setup({
    "GET /v1/security": res(200, { disk_access: "full" }),
  });
  assert.equal(s.p.fullDisk, false);
  assert.equal(s.api.calls.filter((c) => c.path === "/v1/security").length, 0);
  await run(() => s.p.openNewProject());
  await flush();
  assert.equal(s.p.fullDisk, true);
  await s.m.unmount();

  const ws = await setup({
    "GET /v1/security": res(200, { disk_access: "workspace" }),
  });
  await run(() => ws.p.openNewProject());
  await flush();
  assert.equal(ws.p.fullDisk, false);
  await ws.m.unmount();

  const old = await setup({ "GET /v1/security": res(404, {}) });
  await run(() => old.p.openNewProject());
  await flush();
  assert.equal(old.p.fullDisk, false, "a gateway without the setting");
  await old.m.unmount();

  const broken = await setup();
  await run(() => broken.p.openNewProject());
  await flush();
  assert.equal(broken.p.fullDisk, false, "an unreachable gateway");
  await broken.m.unmount();
});

test("closing the dialog before the answer arrives changes nothing", async () => {
  let answer;
  const s = await setup({
    "GET /v1/security": () => new Promise((r) => (answer = r)),
  });
  await run(() => s.p.openNewProject());
  await s.m.unmount();
  answer(res(200, { disk_access: "full" }));
  await flush();
  assert.equal(s.p.fullDisk, false);

  let fail;
  const t = await setup({
    "GET /v1/security": () => new Promise((_, r) => (fail = r)),
  });
  await run(() => t.p.openNewProject());
  await t.m.unmount();
  fail(new Error("late"));
  await flush();
  assert.equal(t.p.fullDisk, false);
});

test("the folder picked goes to the dialog that asked, and new projects send it", async () => {
  const s = await setup({
    "GET /v1/security": res(200, { disk_access: "full" }),
    "POST /v1/projects": res(201, { id: "p9" }),
    "GET /v1/projects/p1/branches": res(200, {}),
    "PATCH /v1/projects/p1": res(200, {}),
  });
  await run(() => s.p.openNewProject());
  await run(() => {
    s.p.setNewProjectMode("empty");
    s.p.setNewProjectTitle("Out");
    s.p.openFolderPicker("new");
  });
  assert.equal(s.p.folderPicker, "new");
  await run(() => s.p.pickFolder("/srv/out"));
  assert.equal(s.p.folderPicker, null);
  assert.equal(s.p.newProjectPath, "/srv/out");
  await run(() => s.p.createProject());
  await flush();
  const [call] = s.api.calls.filter((c) => c.method === "POST");
  assert.equal(call.body.path, "/srv/out");
  assert.equal(s.p.newProjectPath, "", "emptied once the project exists");

  await run(() =>
    s.p.openEditProject({ id: "p1", title: "Edit", dir: "/ws/a" }),
  );
  await flush();
  await run(() => s.p.openFolderPicker("edit"));
  await run(() => s.p.pickFolder("/mnt/b"));
  assert.equal(s.p.editProject.path, "/mnt/b");
  await run(() => s.p.saveEditProject());
  await flush();
  const [patch] = s.api.calls.filter((c) => c.method === "PATCH");
  assert.equal(patch.body.path, "/mnt/b");
  await run(() => s.p.closeFolderPicker());
  await s.m.unmount();
});

test("a project that keeps its folder sends no path", async () => {
  const s = await setup({
    "GET /v1/projects/p1/branches": res(200, {}),
    "PATCH /v1/projects/p1": res(200, {}),
  });
  await run(() =>
    s.p.openEditProject({ id: "p1", title: "Edit", dir: "/ws/a" }),
  );
  await flush();
  await run(() => s.p.saveEditProject());
  await flush();
  const [patch] = s.api.calls.filter((c) => c.method === "PATCH");
  assert.equal("path" in patch.body, false);
  await s.m.unmount();
});
