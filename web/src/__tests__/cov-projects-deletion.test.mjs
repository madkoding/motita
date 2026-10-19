// Deleting a session, a project or a skill: the confirmation asks the gateway what the deletion
// would discard and shows it, the confirm button carries that answer and locks while the deletion
// runs, and the long-press menu of a row offers rename and delete.
// Run with: node --test web/src/__tests__/cov-projects-deletion.test.mjs
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
  buttonByText,
} from "./cov-projects-helpers.mjs";

const { AppCtx, useDeletion, ConfirmDeleteModal, ContextMenu } = await bundle({
  "useDeletion.ts": ["useDeletion"],
  "ConfirmDeleteModal.tsx": ["ConfirmDeleteModal", "ContextMenu"],
});

// deferred is a promise the test settles itself.
function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((a, b) => {
    resolve = a;
    reject = b;
  });
  return { promise, resolve, reject };
}

// harness runs the real useDeletion and draws the modal for what it is about.
function harness(api, deps = {}) {
  const calls = [];
  const state = {};
  const record =
    (name) =>
    async (...args) => {
      calls.push([name, ...args]);
      if (deps[name]) {
        await deps[name](...args);
      }
    };
  function Harness() {
    const deletion = useDeletion({
      api,
      deleteSession: record("deleteSession"),
      deleteSkill: record("deleteSkill"),
      deleteProject: record("deleteProject"),
    });
    state.d = deletion;
    return h(
      AppCtx.Provider,
      { value: { deletion } },
      deletion.confirmDelete &&
        h(ConfirmDeleteModal, { target: deletion.confirmDelete }),
    );
  }
  return { Harness, calls, state };
}

async function open(state, target) {
  await act(() => state.d.setConfirmDelete(target));
}

function confirmButton(root) {
  return root.querySelector(".bg-danger");
}

test("a session's changes are listed and the button says it discards them", async () => {
  const api = async (path) => {
    assert.equal(path, "/v1/sessions/s1/deletion-preview");
    return res(200, {
      worktree: "wt",
      branch: "feat",
      count: 2,
      changes: [
        { path: "a.go", kind: "modified" },
        { path: "b.go", kind: "renamed", from: "old.go" },
      ],
    });
  };
  const { Harness, calls, state } = harness(api);
  const m = await mount(AppCtx, null, h(Harness));
  assert.equal(m.root.textContent, "", "nothing is asked until a deletion is");
  await open(state, { type: "session", id: "s1", title: "My chat" });
  assert.match(m.root.textContent, /Checking what this session is holding/);
  assert.equal(confirmButton(m.root).disabled, true, "waits for the answer");
  await flush();
  const text = m.root.textContent;
  assert.match(text, /Delete session/);
  assert.match(text, /My chat/);
  assert.match(text, /2 uncommitted changes will be discarded in feat/);
  assert.match(text, /old\.go → b\.go/);
  assert.match(text, /modifieda\.go/);
  assert.equal(confirmButton(m.root).textContent, "Discard and delete");
  await click(confirmButton(m.root));
  await flush();
  assert.deepEqual(calls, [["deleteSession", "s1", true]]);
  assert.equal(state.d.confirmDelete, null);
  assert.equal(m.root.textContent, "");
  await m.unmount();
});

test("one change, no branch: singular and no branch named", async () => {
  const api = async () =>
    res(200, { count: 1, changes: [{ path: "x", kind: "added" }] });
  const { Harness, state } = harness(api);
  const m = await mount(AppCtx, null, h(Harness));
  await open(state, { type: "session", id: "s1", title: "t" });
  await flush();
  assert.match(m.root.textContent, /1 uncommitted change will be discarded/);
  assert.doesNotMatch(m.root.textContent, / in /);
  await m.unmount();
});

test("a clean session says nothing is lost; without a checkout it says nothing", async () => {
  let body = { worktree: "wt", count: 0, changes: [] };
  const api = async () => res(200, body);
  const { Harness, state } = harness(api);
  const m = await mount(AppCtx, null, h(Harness));
  await open(state, { type: "session", id: "s1", title: "t" });
  await flush();
  assert.match(m.root.textContent, /checkout is clean/);
  assert.equal(confirmButton(m.root).textContent, "Delete");
  await act(() => state.d.cancelDelete());
  body = { count: 0, changes: [] };
  await open(state, { type: "session", id: "s2", title: "t" });
  await flush();
  assert.doesNotMatch(m.root.textContent, /clean|could not/);
  await m.unmount();
});

test("a checkout that could not be inspected is said so, and the button discards", async () => {
  const api = async () =>
    res(200, {
      count: 0,
      changes: [],
      inspection_failed: true,
      error: "git status failed",
    });
  const { Harness, state } = harness(api);
  const m = await mount(AppCtx, null, h(Harness));
  await open(state, { type: "session", id: "s1", title: "t" });
  await flush();
  assert.match(m.root.textContent, /could not be inspected/);
  assert.match(m.root.textContent, /git status failed/);
  assert.equal(confirmButton(m.root).textContent, "Discard and delete");
  await m.unmount();
});

test("no readable answer falls back to the plain confirmation", async () => {
  const answers = [
    () => res(500, {}),
    () => res(200, new Error("bad json")),
    () => res(200, 42),
    () => {
      throw new Error("offline");
    },
  ];
  let next = 0;
  const api = async () => answers[next++]();
  const { Harness, state } = harness(api);
  const m = await mount(AppCtx, null, h(Harness));
  for (let i = 0; i < answers.length; i++) {
    await open(state, { type: "session", id: "s" + i, title: "t" });
    await flush();
    assert.equal(state.d.deletionPreview, null);
    assert.equal(state.d.previewLoading, false);
    assert.equal(confirmButton(m.root).textContent, "Delete");
    assert.equal(confirmButton(m.root).disabled, false);
  }
  await m.unmount();
});

test("an answer that arrives after the modal moved on is dropped", async () => {
  const late = [deferred(), deferred()];
  let i = 0;
  const api = () => late[i++].promise;
  const { Harness, state } = harness(api);
  const m = await mount(AppCtx, null, h(Harness));
  await open(state, { type: "session", id: "a", title: "A" });
  await open(state, { type: "session", id: "b", title: "B" });
  // The first one's answer, then its failure path, change nothing.
  late[0].resolve(res(200, { count: 9, changes: [] }));
  await flush();
  assert.equal(state.d.deletionPreview, null);
  assert.equal(state.d.previewLoading, true);
  await act(() => state.d.cancelDelete());
  late[1].reject(new Error("gone"));
  await flush();
  assert.equal(state.d.deletionPreview, null);
  await m.unmount();
});

test("a project lists each session's changes and deletes with its sessions", async () => {
  const api = async (path) => {
    assert.equal(path, "/v1/projects/p1/deletion-preview");
    return res(200, {
      count: 3,
      changes: [],
      sessions: [
        { id: "s1", title: "New session", changes: [{ path: "a", kind: "M" }] },
        { id: "s2", title: "", changes: [{ path: "b", kind: "M" }] },
        { id: "s3", title: "Fix bug", changes: [{ path: "c", kind: "M" }] },
        { id: "s4", title: "Clean", changes: [] },
      ],
    });
  };
  const { Harness, calls, state } = harness(api);
  const m = await mount(AppCtx, null, h(Harness));
  await open(state, { type: "project", id: "p1", title: "Proj" });
  assert.match(m.root.textContent, /this project's sessions are holding/);
  await flush();
  const text = m.root.textContent;
  assert.match(text, /Delete project/);
  assert.match(text, /3 uncommitted changes will be discarded/);
  assert.match(text, /New session/);
  assert.match(text, /s2/);
  assert.match(text, /Fix bug/);
  assert.doesNotMatch(text, /Clean/);
  await click(confirmButton(m.root));
  await flush();
  assert.deepEqual(calls, [["deleteProject", "p1", true]]);
  await m.unmount();
});

test("a project whose preview has no session list still warns", async () => {
  const api = async () => res(200, { count: 2, changes: [] });
  const { Harness, state } = harness(api);
  const m = await mount(AppCtx, null, h(Harness));
  await open(state, { type: "project", id: "p1", title: "Proj" });
  await flush();
  assert.match(m.root.textContent, /2 uncommitted changes/);
  await m.unmount();
});

test("a clean project and one that could not be inspected", async () => {
  let body = { count: 0, changes: [] };
  const api = async () => res(200, body);
  const { Harness, state } = harness(api);
  const m = await mount(AppCtx, null, h(Harness));
  await open(state, { type: "project", id: "p1", title: "Proj" });
  await flush();
  assert.match(m.root.textContent, /No session holds uncommitted changes/);
  await act(() => state.d.cancelDelete());
  body = { count: 0, changes: [], inspection_failed: true, error: "boom" };
  await open(state, { type: "project", id: "p2", title: "Proj" });
  await flush();
  assert.match(
    m.root.textContent,
    /A session's checkout could not be inspected/,
  );
  assert.match(m.root.textContent, /boom/);
  await act(() => state.d.cancelDelete());
  body = { count: 0, changes: [], keeps_folder: true, folder: "/srv/app" };
  await open(state, { type: "project", id: "p3", title: "Proj" });
  await flush();
  assert.match(m.root.textContent, /The folder stays on disk.*\/srv\/app/);
  await m.unmount();
});

test("a skill is deleted without asking what it holds", async () => {
  const api = async () => {
    throw new Error("a skill asks nothing");
  };
  const { Harness, calls, state } = harness(api);
  const m = await mount(AppCtx, null, h(Harness));
  await open(state, { type: "skill", id: "sk", title: "Skill" });
  await flush();
  assert.match(m.root.textContent, /Delete skill/);
  assert.doesNotMatch(m.root.textContent, /Checking/);
  await click(confirmButton(m.root));
  await flush();
  assert.deepEqual(calls, [["deleteSkill", "sk"]]);
  await m.unmount();
});

test("the default session has no checkout to show", async () => {
  const gate = deferred();
  const api = () => gate.promise;
  const { Harness, state } = harness(api);
  const m = await mount(AppCtx, null, h(Harness));
  await open(state, { type: "session", id: "default", title: "Default" });
  // Even while the answer is pending, the button is not held back.
  assert.equal(state.d.previewLoading, true);
  assert.equal(confirmButton(m.root).disabled, false);
  assert.doesNotMatch(m.root.textContent, /Checking/);
  gate.resolve(res(200, { count: 0, changes: [] }));
  await flush();
  await m.unmount();
});

test("while the deletion runs both buttons are locked and it says so", async () => {
  const gate = deferred();
  const api = async () => res(200, { count: 0, changes: [] });
  const { Harness, calls, state } = harness(api, {
    deleteSession: () => gate.promise,
  });
  const m = await mount(AppCtx, null, h(Harness));
  await open(state, { type: "session", id: "s1", title: "t" });
  await flush();
  await click(confirmButton(m.root));
  assert.equal(confirmButton(m.root).textContent, "Deleting…");
  assert.equal(confirmButton(m.root).disabled, true);
  assert.ok(m.root.querySelector(".state-working"));
  assert.equal(buttonByText(m.root, "Cancel").disabled, true);
  gate.resolve();
  await flush();
  assert.equal(calls.length, 1);
  assert.equal(state.d.deleting, false);
  assert.equal(m.root.textContent, "");
  // With nothing to confirm, confirming does nothing.
  await act(() => state.d.confirmDeletion());
  assert.equal(calls.length, 1);
  await m.unmount();
});

test("a failed deletion still closes and unlocks the modal", async () => {
  const api = async () => res(200, { count: 0, changes: [] });
  const { Harness, state } = harness(api, {
    deleteProject: () => Promise.reject(new Error("refused")),
  });
  const m = await mount(AppCtx, null, h(Harness));
  await open(state, { type: "project", id: "p", title: "t" });
  await flush();
  await assert.rejects(
    act(() => state.d.confirmDeletion()),
    /refused/,
  );
  await flush();
  assert.equal(state.d.confirmDelete, null);
  assert.equal(state.d.deleting, false);
  await m.unmount();
});

test("Cancel closes the confirmation", async () => {
  const api = async () => res(200, { count: 0, changes: [] });
  const { Harness, calls, state } = harness(api);
  const m = await mount(AppCtx, null, h(Harness));
  await open(state, { type: "session", id: "s1", title: "t" });
  await flush();
  await click(buttonByText(m.root, "Cancel"));
  assert.equal(state.d.confirmDelete, null);
  assert.equal(calls.length, 0);
  await m.unmount();
});

// The long-press menu.
function menuStore(contextMenu) {
  const log = [];
  return {
    log,
    value: {
      menus: { contextMenu, closeContextMenu: () => log.push(["close"]) },
      actions: { startRename: (id, title) => log.push(["rename", id, title]) },
      deletion: { setConfirmDelete: (t) => log.push(["confirm", t]) },
    },
  };
}

test("the long-press menu draws nothing until a row opens it", async () => {
  const { value } = menuStore(null);
  const m = await mount(AppCtx, value, h(ContextMenu));
  assert.equal(m.root.innerHTML, "");
  await m.unmount();
});

test("the long-press menu of a session renames it or asks to delete it, inside the window", async () => {
  const s = menuStore({
    type: "session",
    id: "s1",
    title: "Chat",
    x: 5000,
    y: 10,
  });
  const m = await mount(AppCtx, s.value, h(ContextMenu));
  const box = m.root.querySelector(".frosted");
  assert.equal(box.style.left, window.innerWidth - 180 + "px");
  assert.equal(box.style.top, "10px");
  await click(buttonByText(m.root, "Rename"));
  await click(buttonByText(m.root, "Delete"));
  assert.deepEqual(s.log, [
    ["rename", "s1", "Chat"],
    ["close"],
    ["confirm", { type: "session", id: "s1", title: "Chat" }],
    ["close"],
  ]);
  await m.unmount();
});

test("a project's long-press Rename only closes the menu", async () => {
  const s = menuStore({ type: "project", id: "p1", title: "P", x: 1, y: 1 });
  const m = await mount(AppCtx, s.value, h(ContextMenu));
  await click(buttonByText(m.root, "Rename"));
  assert.deepEqual(s.log, [["close"]]);
  await m.unmount();
});
