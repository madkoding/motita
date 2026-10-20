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

const { AppCtx, NewProjectModal, EditProjectModal } = await bundle({
  "NewProjectModal.tsx": ["NewProjectModal"],
  "EditProjectModal.tsx": ["EditProjectModal"],
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
    newProjectTitle: "",
    newProjectDesc: "",
    newProjectPath: "",
    openFolderPicker: rec("browse"),
    setNewProjectPath: rec("path"),
    newProjectDir: "",
    newProjectPodman: false,
    hasPodman: false,
    hasPodmanCompose: false,
    podmanReady: false,
    creatingProject: false,
    savingProject: false,
    setNewProjectTitle: rec("title"),
    setNewProjectDesc: rec("desc"),
    setNewProjectDir: rec("dir"),
    setNewProjectPodman: rec("podman"),
    createProject: rec("create"),
    closeNewProject: rec("close"),
    patchEditProject: rec("patch"),
    closeEditProject: rec("closeEdit"),
    saveEditProject: rec("save", Promise.resolve()),
    ...state,
  };
  return { log, value: { projects } };
}

const heading = (m) => m.q("h2").textContent;
const fieldByLabel = (root, text) =>
  [...root.querySelectorAll("label")]
    .find((l) => l.textContent.includes(text))
    .parentElement.querySelector("input,select");

test("the dialog asks for the folder up front, the name comes from it, the rest is folded", async () => {
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
  const name = fieldByLabel(m.root, "Name");
  assert.equal(name.placeholder, "My project");
  assert.match(
    m.q("[data-testid=new-project-folder]").textContent,
    /A new folder in the workspace/,
  );
  assert.equal(m.root.textContent.includes("Folder name"), false);
  assert.equal(buttonByText(m.root, "Create").disabled, true, "no name yet");
  assert.equal(m.q("[data-testid=podman-offer]"), null);

  await type(name, "Hello");
  await type(fieldByLabel(m.root, "Description"), "about");
  await click(m.q("[aria-label=Close]"));
  assert.deepEqual(s.log, [["title", "Hello"], ["desc", "about"], ["close"]]);
  await m.unmount();
});

test("a folder chosen names the project and lets it be created", async () => {
  const s = store({ newProjectPath: "/srv/my-app/" });
  const m = await mount(AppCtx, s.value, h(NewProjectModal));
  assert.equal(fieldByLabel(m.root, "Name").placeholder, "my-app");
  assert.equal(buttonByText(m.root, "Create").disabled, false);
  assert.equal(m.root.textContent.includes("created in the workspace"), false);
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
  git: true,
  main: "develop",
  branches: ["main", "develop"],
  current: "feat",
  mergeMethod: "squash",
  prMaxFixes: 3,
  autoMerge: true,
  autoContinue: false,
};

test("the edit dialog shows the project's settings", async () => {
  const s = store();
  const m = await mount(AppCtx, s.value, h(EditProjectModal, { p: DRAFT }));
  assert.equal(heading(m), "Edit project");
  assert.equal(fieldByLabel(m.root, "Title").value, "Motita");
  assert.equal(fieldByLabel(m.root, "Description").value, "the app");
  assert.equal(m.q("[data-testid=edit-project-git]").open, false, "folded");
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

test("a plain folder's dialog has no git or pull request settings", async () => {
  const s = store();
  const m = await mount(
    AppCtx,
    s.value,
    h(EditProjectModal, { p: { ...DRAFT, git: false } }),
  );
  assert.equal(m.q("[data-testid=edit-project-git]"), null);
  assert.ok(buttonByText(m.root, "Save"), "the rest of the dialog stays");
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
