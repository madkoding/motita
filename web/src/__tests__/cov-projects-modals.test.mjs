// The New project, Edit project and git identity dialogs: what each screen shows for the state
// the projects store holds, and which of the store's actions each control calls.
// Run with: node --test web/src/__tests__/cov-projects-modals.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import {
  bundle,
  h,
  mount,
  click,
  type,
  change,
  key,
  flush,
  buttonByText,
} from "./cov-projects-helpers.mjs";

const { AppCtx, NewProjectModal, EditProjectModal, GitIdentityModal } =
  await bundle({
    "NewProjectModal.tsx": ["NewProjectModal"],
    "EditProjectModal.tsx": ["EditProjectModal"],
    "GitIdentityModal.tsx": ["GitIdentityModal"],
  });

// store is a projects store whose every action is recorded in `log`.
function store(state = {}) {
  const log = [];
  const rec =
    (name, ret) =>
    (...args) => {
      // A handler wired straight to onClick gets the event: it says nothing about the action.
      log.push([name, ...args.filter((a) => !(a instanceof Event))]);
      return ret;
    };
  const projects = {
    newProjectMode: null,
    newProjectTitle: "",
    newProjectDesc: "",
    newProjectDir: "",
    newProjectGit: "",
    newProjectMain: "main",
    remoteMain: "",
    newProjectRepos: [],
    newProjectPodman: false,
    hasPodman: false,
    hasPodmanCompose: false,
    podmanReady: false,
    gitAccounts: null,
    creatingProject: false,
    gitUserName: "",
    gitUserEmail: "",
    repoBusy: "",
    repoConflict: null,
    savingProject: false,
    setNewProjectMode: rec("mode"),
    setNewProjectTitle: rec("title"),
    setNewProjectDesc: rec("desc"),
    setNewProjectDir: rec("dir"),
    setNewProjectGit: rec("git"),
    setNewProjectPodman: rec("podman"),
    chooseMain: rec("main"),
    addNewProjectRepo: rec("addRepo", true),
    removeNewProjectRepo: rec("removeRepo"),
    openRepoPicker: rec("picker"),
    openOtherRepoPicker: rec("pickOther"),
    setGitConnect: rec("connect"),
    createProject: rec("create"),
    closeNewProject: rec("close"),
    closeGitIdentity: rec("closeIdentity"),
    setGitUserName: rec("userName"),
    setGitUserEmail: rec("userEmail"),
    patchEditProject: rec("patch"),
    closeEditProject: rec("closeEdit"),
    saveEditProject: rec("save", Promise.resolve()),
    addProjectRepo: rec("addProjectRepo", Promise.resolve(true)),
    detachProjectMain: rec("detachMain", Promise.resolve()),
    removeProjectRepo: rec("removeProjectRepo", Promise.resolve()),
    ...state,
  };
  return { log, value: { projects } };
}

const heading = (m) => m.q("h2").textContent;
const fieldByLabel = (root, text) =>
  [...root.querySelectorAll("label")]
    .find((l) => l.textContent.includes(text))
    .parentElement.querySelector("input,select");

test("the dialog asks for a name and nothing else up front: a project is a plain folder", async () => {
  const s = store();
  const m = await mount(AppCtx, s.value, h(NewProjectModal));
  assert.equal(heading(m), "New project");
  assert.equal(
    m.q("[data-testid=new-project-choose]"),
    null,
    "no first screen",
  );
  assert.equal(m.q("[data-testid=new-project-back]"), null);
  assert.equal(m.q("[data-testid=new-project-main]"), null, "no branch");
  assert.equal(m.q("[data-testid=repo-list]"), null, "no repositories");
  const name = fieldByLabel(m.root, "Name");
  assert.equal(name.placeholder, "My project");
  assert.match(name.parentElement.querySelector("label").textContent, /\*/);
  assert.equal(fieldByLabel(m.root, "Folder name").placeholder, "my-project");
  assert.equal(buttonByText(m.root, "Create").disabled, true, "no name yet");
  assert.equal(m.q("[data-testid=podman-offer]"), null);

  await type(name, "Hello");
  await type(fieldByLabel(m.root, "Description"), "about");
  await type(fieldByLabel(m.root, "Folder name"), "hello-dir");
  await click(m.q("[aria-label=Close]"));
  assert.deepEqual(s.log, [
    ["title", "Hello"],
    ["desc", "about"],
    ["dir", "hello-dir"],
    ["close"],
  ]);
  await m.unmount();
});

test("a named project can be created, in the folder named after it", async () => {
  const s = store({
    newProjectTitle: "My App",
    hasPodman: true,
    podmanReady: true,
    hasPodmanCompose: true,
  });
  const m = await mount(AppCtx, s.value, h(NewProjectModal));
  assert.equal(fieldByLabel(m.root, "Folder name").placeholder, "my-app");
  const offer = m.q("[data-testid=podman-offer]");
  assert.match(offer.textContent, /started in containers with podman\./);
  await change(offer.querySelector("input"), true);
  const create = buttonByText(m.root, "Create");
  assert.equal(create.disabled, false);
  await click(create);
  assert.deepEqual(s.log, [["podman", true], ["create"]]);
  await m.unmount();
});

test("while a project is being created the dialog says so and cannot be sent again", async () => {
  const s = store({ newProjectTitle: "x", creatingProject: true });
  const m = await mount(AppCtx, s.value, h(NewProjectModal));
  assert.match(m.root.textContent, /Creating the project…/);
  const btn = buttonByText(m.root, "Creating…");
  assert.equal(btn.disabled, true);
  assert.ok(btn.querySelector(".animate-spin"));
  await click(buttonByText(m.root, "Cancel"));
  assert.deepEqual(s.log, [["close"]]);
  await m.unmount();
});

const DRAFT = {
  id: "p1",
  title: "Motita",
  description: "the app",
  main: "develop",
  branches: ["main", "develop"],
  current: "feat",
  mergeMethod: "squash",
  prMaxFixes: 3,
  autoMerge: true,
  autoContinue: false,
  mainRepo: { name: "motita", source: "https://x.io/u/motita.git" },
  repos: [{ name: "api", git_url: "https://x.io/u/api.git" }],
};

test("the edit dialog offers the person's repositories when a host is connected", async () => {
  const s = store({ gitAccounts: [{ id: "github", connected: true }] });
  const m = await mount(AppCtx, s.value, h(EditProjectModal, { p: DRAFT }));
  await click(m.q("[data-testid=repo-add-open]"));
  await click(m.q("[data-testid=repo-choose]"));
  assert.deepEqual(s.log, [["pickOther"]]);
  await m.unmount();
  const none = store({ gitAccounts: [] });
  const n = await mount(AppCtx, none.value, h(EditProjectModal, { p: DRAFT }));
  await click(n.q("[data-testid=repo-add-open]"));
  assert.equal(n.q("[data-testid=repo-choose]"), null);
  await n.unmount();
});

test("the edit dialog moves the main repository when asked", async () => {
  const s = store();
  const draft = {
    ...DRAFT,
    mainRepo: {
      name: "motita",
      source: "https://x.io/u/motita.git",
      movable: true,
    },
  };
  const m = await mount(AppCtx, s.value, h(EditProjectModal, { p: draft }));
  await click(m.q("[data-testid=repo-main-remove]"));
  await click(m.q("[data-testid=repo-main-move]"));
  assert.deepEqual(s.log, [["detachMain"]]);
  await m.unmount();
});

test("the edit dialog shows the project's settings", async () => {
  const s = store();
  const m = await mount(AppCtx, s.value, h(EditProjectModal, { p: DRAFT }));
  assert.equal(heading(m), "Edit project");
  assert.equal(fieldByLabel(m.root, "Title").value, "Motita");
  assert.equal(fieldByLabel(m.root, "Description").value, "the app");
  const main = m.q("[data-testid=edit-project-main]");
  assert.deepEqual(
    [...main.options].map((o) => o.value),
    ["main", "develop"],
  );
  assert.equal(main.value, "develop");
  assert.match(m.root.textContent, /It is on feat now\./);
  assert.equal(m.q("[data-testid=edit-project-merge]").value, "squash");
  assert.equal(m.q("[data-testid=edit-project-fixes]").value, "3");
  assert.equal(m.q("[data-testid=edit-project-automerge]").checked, true);
  assert.equal(m.q("[data-testid=edit-project-autocontinue]").checked, false);
  assert.match(m.q("[data-testid=repo-list]").textContent, /motita/);
  assert.equal(buttonByText(m.root, "Save").disabled, false);
  // On its main branch, or with no branch known, nothing more is said.
  await m.rerender(
    s.value,
    h(EditProjectModal, { p: { ...DRAFT, current: "develop" } }),
  );
  assert.doesNotMatch(m.root.textContent, /It is on/);
  await m.rerender(
    s.value,
    h(EditProjectModal, { p: { ...DRAFT, current: "" } }),
  );
  assert.doesNotMatch(m.root.textContent, /It is on/);
  await m.unmount();
});

test("each field of the edit dialog patches the draft", async () => {
  const s = store();
  const m = await mount(AppCtx, s.value, h(EditProjectModal, { p: DRAFT }));
  await type(fieldByLabel(m.root, "Title"), "New");
  await type(fieldByLabel(m.root, "Description"), "desc");
  await change(m.q("[data-testid=edit-project-main]"), "main");
  await change(m.q("[data-testid=edit-project-merge]"), "rebase");
  const fixes = m.q("[data-testid=edit-project-fixes]");
  for (const v of ["7", "3.6", "99", "-4", "abc"]) {
    await type(fixes, v);
  }
  await change(m.q("[data-testid=edit-project-automerge]"), false);
  await change(m.q("[data-testid=edit-project-autocontinue]"), true);
  assert.deepEqual(
    s.log.map((e) => e[1]),
    [
      { title: "New" },
      { description: "desc" },
      { main: "main" },
      { mergeMethod: "rebase" },
      { prMaxFixes: 7 },
      { prMaxFixes: 4 },
      { prMaxFixes: 20 },
      { prMaxFixes: 0 },
      { prMaxFixes: 0 },
      { autoMerge: false },
      { autoContinue: true },
    ],
  );
  await m.unmount();
});

test("the edit dialog's repositories change at once", async () => {
  const s = store({
    repoConflict: { name: "api", message: "api holds work" },
  });
  const m = await mount(AppCtx, s.value, h(EditProjectModal, { p: DRAFT }));
  await click(m.q("[data-testid=repo-remove]"));
  await click(buttonByText(m.root, "Discard the changes and remove it"));
  await click(m.q("[data-testid=repo-add-open]"));
  const input = m.q("[data-testid=repo-add-url]");
  await type(input, "https://x.io/u/web.git");
  await click(m.q("[data-testid=repo-add]"));
  await flush();
  assert.deepEqual(s.log, [
    ["removeProjectRepo", "api", false],
    ["removeProjectRepo", "api", true],
    ["addProjectRepo", { name: "web", git_url: "https://x.io/u/web.git" }],
  ]);
  await m.unmount();
});

test("the edit dialog saves, cancels and closes", async () => {
  const s = store();
  const m = await mount(AppCtx, s.value, h(EditProjectModal, { p: DRAFT }));
  await click(buttonByText(m.root, "Save"));
  await click(buttonByText(m.root, "Cancel"));
  await click(m.q("[aria-label=Close]"));
  assert.deepEqual(s.log, [["save"], ["closeEdit"], ["closeEdit"]]);
  await m.rerender(
    store({ savingProject: true }).value,
    h(EditProjectModal, { p: DRAFT }),
  );
  assert.equal(buttonByText(m.root, "Saving…").disabled, true);
  await m.rerender(
    s.value,
    h(EditProjectModal, { p: { ...DRAFT, title: " " } }),
  );
  assert.equal(buttonByText(m.root, "Save").disabled, true);
  await m.unmount();
});

test("the git identity dialog needs a name and an email before it saves", async () => {
  const s = store();
  const m = await mount(AppCtx, s.value, h(GitIdentityModal));
  assert.equal(heading(m), "Git user");
  const save = buttonByText(m.root, "Save and create");
  assert.equal(save.disabled, true);
  const [name, email] = m.root.querySelectorAll("input");
  await type(name, "Ada");
  await type(email, "ada@example.com");
  assert.deepEqual(s.log, [
    ["userName", "Ada"],
    ["userEmail", "ada@example.com"],
  ]);
  // A name without a real email is not enough either.
  await m.rerender(store({ gitUserName: "Ada", gitUserEmail: "ada" }).value);
  assert.equal(buttonByText(m.root, "Save and create").disabled, true);
  await m.unmount();
});

test("the git identity is sent with the project, trimmed; Cancel closes it", async () => {
  const s = store({ gitUserName: " Ada ", gitUserEmail: " ada@x.io " });
  const m = await mount(AppCtx, s.value, h(GitIdentityModal));
  const save = buttonByText(m.root, "Save and create");
  assert.equal(save.disabled, false);
  await click(save);
  await click(buttonByText(m.root, "Cancel"));
  assert.deepEqual(s.log, [
    ["create", { name: "Ada", email: "ada@x.io" }],
    ["closeIdentity"],
  ]);
  await m.rerender(
    store({ gitUserName: "Ada", gitUserEmail: "a@x", creatingProject: true })
      .value,
  );
  assert.equal(buttonByText(m.root, "Saving…").disabled, true);
  await m.unmount();
});
