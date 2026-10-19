// Repositories in the sidebar: the hook that reads them, records one and deletes one, the group a
// repository is drawn as, and the dialog that adds one.
// Run with: node --test web/src/__tests__/cov-repositories.test.mjs
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
  click,
  type,
} from "./cov-projects-helpers.mjs";

const {
  AppCtx,
  useRepositories,
  repoScope,
  RepositoryGroup,
  repoTooltip,
  NewRepositoryModal,
} = await bundle({
  "useRepositories.ts": ["useRepositories", "repoScope"],
  "RepositoryGroup.tsx": ["RepositoryGroup", "repoTooltip"],
  "NewRepositoryModal.tsx": ["NewRepositoryModal"],
});

const API = {
  id: "r1",
  name: "api",
  git_url: "https://x.io/u/api.git",
  dir: "/w/repos/api",
  projects: [],
};

async function setup(routes = {}, projects = []) {
  const api = fakeApi({
    "GET /v1/repos": res(200, { repositories: [API] }),
    ...routes,
  });
  const toasts = [];
  const s = { api, toasts };
  function Harness({ projects }) {
    s.r = useRepositories({ api, setToast: (t) => toasts.push(t), projects });
    return null;
  }
  s.m = await mount(AppCtx, null, h(Harness, { projects }));
  s.again = (next) => s.m.rerender(null, h(Harness, { projects: next }));
  await flush();
  return s;
}

test("a repository's session carries the repository's scope", () => {
  assert.equal(repoScope("r1"), "repo_r1");
});

test("the list is read on start, and again when a project gains a repository", async () => {
  const s = await setup({}, [{ id: "p1" }]);
  assert.deepEqual(s.r.repositories, [API]);
  assert.equal(s.api.calls.length, 1);
  await s.again([{ id: "p1", repos: [{ name: "x" }] }]);
  await flush();
  assert.equal(s.api.calls.length, 2, "read again");
  await s.m.unmount();
});

test("a gateway with no list leaves the list empty or as it was", async () => {
  const s = await setup({ "GET /v1/repos": res(200, {}) });
  assert.deepEqual(s.r.repositories, []);
  await s.m.unmount();
  const down = await setup({
    "GET /v1/repos": () => Promise.reject(new Error("down")),
  });
  assert.deepEqual(down.r.repositories, []);
  await down.m.unmount();
});

test("the dialog records a repository by its URL or its folder and closes", async () => {
  const s = await setup({
    "POST /v1/repos": res(201, { id: "r2" }),
  });
  await act(() => s.r.openNewRepository());
  assert.equal(s.r.showNewRepository, true);
  await act(() => {
    s.r.setNewRepoUrl(" https://x.io/u/web.git ");
    s.r.setNewRepoPath(" ");
  });
  await act(() => s.r.createRepository());
  await flush();
  const post = s.api.calls.find((c) => c.method === "POST");
  assert.deepEqual(post.body, { git_url: "https://x.io/u/web.git", path: "" });
  assert.equal(s.r.showNewRepository, false);
  assert.equal(s.r.newRepoUrl, "", "the dialog is emptied");
  assert.equal(s.r.creatingRepository, false);
  assert.equal(
    s.api.calls.filter((c) => c.method === "GET").length,
    2,
    "read again",
  );
  await s.m.unmount();
});

test("a refusal or a failure stays in the dialog with its reason", async () => {
  let answer = res(409, { error: "that repository is already recorded" });
  const s = await setup({ "POST /v1/repos": () => answer });
  await act(() => s.r.openNewRepository());
  await act(() => s.r.createRepository());
  assert.equal(s.r.newRepoError, "that repository is already recorded");
  assert.equal(s.r.showNewRepository, true);

  answer = res(500, {});
  await act(() => s.r.createRepository());
  assert.equal(s.r.newRepoError, "Could not add the repository");

  answer = {
    ok: false,
    status: 500,
    json: () => Promise.reject(new Error("x")),
  };
  await act(() => s.r.createRepository());
  assert.equal(s.r.newRepoError, "Could not add the repository", "no body");

  answer = Promise.reject(new Error("network down"));
  await act(() => s.r.createRepository().catch(() => {}));
  assert.equal(s.r.newRepoError, "network down");
  assert.equal(s.r.creatingRepository, false);
  await act(() => s.r.closeNewRepository());
  assert.equal(s.r.newRepoError, "");
  await s.m.unmount();
});

test("deleting a repository says so, or says why it was refused", async () => {
  let answer = res(204, null);
  const s = await setup({ "DELETE /v1/repos/r1": () => answer });
  await act(() => s.r.deleteRepository(API));
  assert.deepEqual(s.toasts.at(-1), {
    message: "Deleted api",
    type: "success",
  });
  assert.equal(
    s.api.calls.filter((c) => c.method === "GET").length,
    2,
    "read again",
  );

  answer = res(409, { error: "the repository is used by Shop" });
  await act(() => s.r.deleteRepository(API));
  assert.deepEqual(s.toasts.at(-1), {
    message: "the repository is used by Shop",
    type: "error",
  });
  answer = res(500, {});
  await act(() => s.r.deleteRepository(API));
  assert.equal(s.toasts.at(-1).message, "Could not delete the repository");

  answer = Promise.reject(new Error("network down"));
  await act(() => s.r.deleteRepository(API));
  assert.equal(s.toasts.at(-1).message, "network down");
  await s.m.unmount();
});

// store is the part of the app store a repository group reads.
function store({ sessions = [], rowMenu = null, creating = null } = {}) {
  const log = [];
  const rec =
    (name) =>
    (...args) =>
      log.push([name, ...args]);
  return {
    log,
    value: {
      rs: { sessionId: "" },
      lists: { sessions, answeredHere: () => false },
      actions: {
        createSession: rec("createSession"),
        creatingSession: creating,
        renamingId: null,
      },
      conv: { switchSession: rec("switch") },
      menus: {
        rowMenu,
        rowMenuAbove: false,
        rowMenuClosing: false,
        rowMenuPos: null,
        closeRowMenu: rec("closeMenu"),
        toggleRowMenu: (el, target) => log.push(["toggleMenu", target]),
        setContextMenu: rec("contextMenu"),
      },
      repositories: { deleteRepository: rec("delete") },
      deletion: { setConfirmDelete: rec("confirm") },
    },
  };
}

test("a tooltip says where the repository is, its branch and its changes", () => {
  assert.equal(repoTooltip(API), "https://x.io/u/api.git");
  assert.equal(
    repoTooltip({ ...API, git_url: "", branch: "main", changes: 3 }),
    "/w/repos/api · branch main · 3 changes",
  );
});

test("a repository with no sessions says so, and its '+' starts one on it", async () => {
  const s = store();
  const m = await mount(AppCtx, s.value, h(RepositoryGroup, { r: API }));
  assert.match(m.root.textContent, /api/);
  assert.match(m.root.textContent, /No sessions yet/);
  assert.equal(
    m.q(".project-name").getAttribute("title"),
    "https://x.io/u/api.git",
  );
  assert.equal(m.root.textContent.includes("local"), false);
  await click(
    m.q("button.project-new-session, [title='New session in repository']"),
  );
  assert.deepEqual(s.log, [["createSession", "repo_r1"]]);
  await m.unmount();
});

test("its sessions are the ones anchored to it, and a folder of the user's is marked", async () => {
  const s = store({
    sessions: [
      { id: "s1", title: "Here", project_id: "repo_r1" },
      { id: "s2", title: "Elsewhere", project_id: "repo_r2" },
    ],
    creating: "repo_r1",
  });
  const m = await mount(
    AppCtx,
    s.value,
    h(RepositoryGroup, { r: { ...API, local: true } }),
  );
  assert.match(m.root.textContent, /Here/);
  assert.doesNotMatch(m.root.textContent, /Elsewhere/);
  assert.match(m.root.textContent, /local/);
  assert.doesNotMatch(m.root.textContent, /No sessions yet/);
  assert.ok(m.q(".btn-spinner"), "busy while a session is being made");
  await m.unmount();
});

test("the '⋯' menu opens for the repository, and deletes it", async () => {
  const closed = store();
  const m = await mount(AppCtx, closed.value, h(RepositoryGroup, { r: API }));
  await click(m.q("[data-row-menu-trigger]"));
  assert.deepEqual(closed.log.at(-1), [
    "toggleMenu",
    { type: "project", id: "repo_r1", title: "api" },
  ]);
  assert.equal(m.q("[data-testid=delete-repository]"), null);

  const open = store({ rowMenu: { id: "repo_r1" } });
  await m.rerender(open.value, h(RepositoryGroup, { r: API }));
  await click(m.q("[data-testid=delete-repository]"));
  assert.deepEqual(open.log, [["delete", API], ["closeMenu"]]);
  await m.unmount();
});

function modalStore(over = {}) {
  const log = [];
  const rec =
    (name) =>
    (...args) =>
      log.push([name, ...args]);
  return {
    log,
    value: {
      repositories: {
        newRepoUrl: "",
        newRepoPath: "",
        newRepoError: "",
        creatingRepository: false,
        setNewRepoUrl: rec("url"),
        setNewRepoPath: rec("path"),
        createRepository: rec("create"),
        closeNewRepository: rec("close"),
        ...over,
      },
    },
  };
}

test("the dialog cannot add until a URL or a folder is given, and sends what it has", async () => {
  const empty = modalStore();
  const m = await mount(AppCtx, empty.value, h(NewRepositoryModal));
  assert.equal(m.q("[data-testid=new-repo-create]").disabled, true);
  await type(m.q("[data-testid=new-repo-url]"), "https://x.io/u/a.git");
  await type(m.q("[data-testid=new-repo-path]"), "/home/me/a");
  assert.deepEqual(empty.log, [
    ["url", "https://x.io/u/a.git"],
    ["path", "/home/me/a"],
  ]);
  assert.equal(m.q("[data-testid=new-repo-error]"), null);

  const filled = modalStore({ newRepoPath: "/home/me/a" });
  await m.rerender(filled.value, h(NewRepositoryModal));
  assert.equal(m.q("[data-testid=new-repo-create]").disabled, false);
  await click(m.q("[data-testid=new-repo-create]"));
  await click(m.q("[aria-label=Close]"));
  await click([...m.qa("button")].find((b) => b.textContent === "Cancel"));
  assert.deepEqual(
    filled.log.map((l) => l[0]),
    ["create", "close", "close"],
  );
  await m.unmount();
});

test("the dialog shows the refusal and the work in progress", async () => {
  const busy = modalStore({
    newRepoUrl: "x",
    newRepoError: "that repository is already recorded",
    creatingRepository: true,
  });
  const m = await mount(AppCtx, busy.value, h(NewRepositoryModal));
  assert.equal(m.q("[data-testid=new-repo-create]").disabled, true);
  assert.ok(m.q("[data-testid=new-repo-create] svg"), "a spinner");
  assert.equal(
    m.q("[data-testid=new-repo-error]").textContent,
    "that repository is already recorded",
  );
  await m.unmount();
});
