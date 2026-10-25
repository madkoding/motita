// A project in the sidebar: its header (title, facts, new-session and "⋯" buttons, a click that
// folds the list, a long press that opens the context menu), its menu, and its sessions.
// Run with: node --test web/src/__tests__/cov-projects-projectgroup.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import {
  bundle,
  h,
  act,
  mount,
  click,
  buttonByText,
} from "./cov-projects-helpers.mjs";

const { AppCtx, ProjectGroup } = await bundle({
  "ProjectGroup.tsx": ["ProjectGroup"],
});

const P = { id: "p1", title: "Motita", dir: "/ws/motita" };

// store is the part of the app store a project group reads; every action lands in `log`.
function store({
  sessions = [],
  collapsed = [],
  rowMenu = null,
  creatingSession = null,
} = {}) {
  const log = [];
  const rec =
    (name) =>
    (...args) =>
      log.push([name, ...args]);
  return {
    log,
    value: {
      rs: { sessionId: "" },
      lists: {
        sessions,
        collapsedProjects: new Set(collapsed),
        toggleProject: rec("toggle"),
        answeredHere: () => false,
      },
      actions: {
        createSession: rec("createSession"),
        creatingSession,
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
      projects: {
        openEditProject: rec("edit"),
        refreshProjectMain: rec("refreshMain"),
        rescanNotes: rec("rescan"),
      },
      repoGit: {
        openRepoGit: rec("details"),
        repoAction: rec("action"),
        askAgent: rec("ask"),
      },
      deletion: { setConfirmDelete: rec("confirm") },
    },
  };
}

test("a project with no sessions says so, and its header folds the list", async () => {
  const s = store();
  const m = await mount(AppCtx, s.value, h(ProjectGroup, { p: P }));
  assert.match(m.root.textContent, /Motita/);
  assert.match(m.root.textContent, /No sessions yet/);
  const hit = m.q(".row-hit");
  assert.equal(hit.getAttribute("aria-expanded"), "true");
  assert.equal(hit.getAttribute("title"), null, "nothing to say about it");
  assert.equal(
    m.q(".project-chevron").classList.contains("is-collapsed"),
    false,
  );
  assert.equal(
    m.q(".project-sessions").classList.contains("is-collapsed"),
    false,
  );
  await click(hit);
  assert.deepEqual(s.log, [["toggle", "p1"]]);
  await m.unmount();
});

test("a folded project hides its sessions", async () => {
  const s = store({ collapsed: ["p1"] });
  const m = await mount(AppCtx, s.value, h(ProjectGroup, { p: P }));
  assert.equal(m.q(".row-hit").getAttribute("aria-expanded"), "false");
  assert.ok(m.q(".project-chevron").classList.contains("is-collapsed"));
  assert.ok(m.q(".project-sessions").classList.contains("is-collapsed"));
  await m.unmount();
});

test("only the project's own sessions are listed under it", async () => {
  const s = store({
    sessions: [
      { id: "s1", title: "Mine", project_id: "p1" },
      { id: "s2", title: "Elsewhere", project_id: "p2" },
    ],
  });
  const m = await mount(AppCtx, s.value, h(ProjectGroup, { p: P }));
  const rows = m.qa(".session-row");
  assert.equal(rows.length, 1);
  assert.match(rows[0].textContent, /Mine/);
  assert.doesNotMatch(m.root.textContent, /No sessions yet/);
  await m.unmount();
});

test("the header is the title alone: the checkout's facts are in its tooltip", async () => {
  const s = store();
  const m = await mount(
    AppCtx,
    s.value,
    h(ProjectGroup, {
      p: { ...P, branch: "refs/heads/feature", changes: 3 },
    }),
  );
  const text = m.root.textContent;
  assert.doesNotMatch(text, /feature|changes/);
  const tip = m.q(".project-header .row-hit").getAttribute("title");
  assert.match(tip, /feature/);
  await m.unmount();
});

test("the new-session button opens one in the project without folding it", async () => {
  const s = store();
  const m = await mount(AppCtx, s.value, h(ProjectGroup, { p: P }));
  const btn = m.q(".project-new-session");
  assert.equal(btn.title, "New session in project");
  assert.equal(btn.disabled, false);
  await click(btn);
  assert.deepEqual(s.log, [["createSession", "p1"]]);
  await m.rerender(store({ creatingSession: "p1" }).value);
  assert.equal(m.q(".project-new-session").disabled, true);
  await m.unmount();
});

test("the ⋯ button opens the project's menu without folding it", async () => {
  const s = store();
  const m = await mount(AppCtx, s.value, h(ProjectGroup, { p: P }));
  await click(m.q("[data-row-menu-trigger]"));
  assert.deepEqual(s.log, [
    ["toggleMenu", { type: "project", id: "p1", title: "Motita" }],
  ]);
  assert.equal(document.querySelector(".row-menu"), null);
  // Another row's menu is open: not this one's.
  await m.rerender(store({ rowMenu: { id: "p2" } }).value);
  assert.equal(document.querySelector(".row-menu"), null);
  assert.equal(m.q(".row-actions-open"), null);
  await m.unmount();
});

test("the menu edits the project and asks before deleting it", async () => {
  const s = store({ rowMenu: { id: "p1" } });
  const m = await mount(AppCtx, s.value, h(ProjectGroup, { p: P }));
  assert.ok(m.q(".row-actions-open"));
  await click(m.q("[data-testid=edit-project]"));
  await click(buttonByText(document.querySelector(".row-menu"), "Delete"));
  assert.deepEqual(s.log, [
    ["edit", P],
    ["closeMenu"],
    ["confirm", { type: "project", id: "p1", title: "Motita" }],
    ["closeMenu"],
  ]);
  await m.unmount();
});

test("threads are listed under their main session, indented", async () => {
  const s = store({
    sessions: [
      { id: "t1", project_id: "p1", parent_id: "m", created: "2024-02" },
      { id: "x", project_id: "p2", created: "2024-01" },
      { id: "m", project_id: "p1", created: "2024-01" },
      { id: "t2", project_id: "p1", parent_id: "m", created: "2024-03" },
    ],
  });
  const m = await mount(AppCtx, s.value, h(ProjectGroup, { p: P }));
  const rows = [...m.root.querySelectorAll(".session-row")];
  assert.equal(rows.length, 3);
  assert.deepEqual(
    rows.map((r) => r.classList.contains("session-thread")),
    [false, true, true],
  );
  await m.unmount();
});

test("the menu updates the main branch: nothing else does it by itself", async () => {
  const s = store({ rowMenu: { id: "p1" } });
  const git = { ...P, main_branch: "main" };
  const m = await mount(AppCtx, s.value, h(ProjectGroup, { p: git }));
  await click(m.q("[data-testid=refresh-main]"));
  assert.deepEqual(s.log, [["refreshMain", "p1"], ["closeMenu"]]);
  await m.unmount();
});

test("the repositories inside a plain folder are a tree, each with its own menu", async () => {
  const withRepos = {
    ...P,
    nested_repos: [
      { path: "api" },
      { path: "group/web", branch: "main", changes: 3 },
      { path: "lib", branch: "dev" },
      { path: "own", branch: "trunk", main_branch: "trunk" },
    ],
  };
  const closed = store({});
  const m = await mount(
    AppCtx,
    closed.value,
    h(ProjectGroup, { p: withRepos }),
  );
  const rows = m.qa("[data-testid=nested-repo]");
  assert.deepEqual(
    rows.map((r) => r.textContent),
    ["api", "group/web", "lib", "own"],
  );
  const icons = m.qa("[data-testid=nested-state]");
  assert.deepEqual(
    icons.map((i) => i.title),
    ["api", "Branch main · 3±", "Branch dev", "Branch trunk"],
    "the branch is the tooltip of the repository's icon",
  );
  assert.deepEqual(
    icons.map((i) => i.dataset.offMain),
    [undefined, undefined, "true", undefined],
    "only a branch other than the main line changes the icon's colour",
  );
  assert.equal(m.q("[data-testid=refresh-nested]"), null, "menus start closed");
  await click(m.qa("[data-testid=nested-menu]")[1]);
  assert.deepEqual(closed.log, [
    [
      "toggleMenu",
      { type: "project", id: "p1\u0000group/web", title: "group/web" },
    ],
  ]);
  await m.unmount();

  const s = store({ rowMenu: { id: "p1\u0000group/web" } });
  const open = await mount(AppCtx, s.value, h(ProjectGroup, { p: withRepos }));
  const item = open.q("[data-testid=refresh-nested]");
  assert.equal(item.textContent, "Update group/web");
  assert.match(item.title, /group\/web/);
  await click(item);
  assert.deepEqual(s.log, [["refreshMain", "p1", "group/web"], ["closeMenu"]]);
  await open.unmount();
});

test("each entry of a repository's menu does its one thing and closes the menu", async () => {
  const ref = { projectId: "p1", repo: "api" };
  const cases = [
    ["repo-details", ["details", ref]],
    ["repo-pull", ["action", ref, "pull"]],
    ["repo-main", ["action", ref, "main"]],
    ["repo-stash", ["action", ref, "stash"]],
    ["repo-clean", ["action", ref, "clean"]],
    ["repo-commit", ["ask", ref, "commit"]],
    ["repo-push", ["ask", ref, "push"]],
    ["repo-pr", ["ask", ref, "pr"]],
    ["repo-work", ["ask", ref, "work"]],
  ];
  for (const [id, expected] of cases) {
    const s = store({ rowMenu: { id: "p1\u0000api" } });
    const m = await mount(
      AppCtx,
      s.value,
      h(ProjectGroup, { p: { ...P, nested_repos: [{ path: "api" }] } }),
    );
    await click(m.q(`[data-testid=${id}]`));
    assert.deepEqual(s.log, [expected, ["closeMenu"]], id);
    await m.unmount();
  }
});

test("a folder with no repository inside draws no tree", async () => {
  const s = store({});
  const m = await mount(AppCtx, s.value, h(ProjectGroup, { p: P }));
  assert.equal(m.q("[data-testid=nested-repo]"), null);
  await m.unmount();
});

test("a project that is only a folder has no main branch to update", async () => {
  const s = store({ rowMenu: { id: "p1" } });
  const m = await mount(AppCtx, s.value, h(ProjectGroup, { p: P }));
  assert.equal(m.q("[data-testid=refresh-main]"), null);
  assert.ok(m.q("[data-testid=rescan-notes]"), "the rest of the menu stays");
  await m.unmount();
});

test("the menu rescans the notes of its project", async () => {
  const s = store({ rowMenu: { id: "p1" } });
  const m = await mount(AppCtx, s.value, h(ProjectGroup, { p: P }));
  await click(m.q("[data-testid=rescan-notes]"));
  assert.deepEqual(s.log, [["rescan", "p1"], ["closeMenu"]]);
  await m.unmount();
});

test("a long press on the header opens the context menu where the finger is", async (t) => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  const s = store();
  const m = await mount(AppCtx, s.value, h(ProjectGroup, { p: P }));
  const header = m.q(".project-header");
  const touch = new Event("touchstart");
  touch.touches = [{ clientX: 12, clientY: 34 }];
  await act(() => {
    header.dispatchEvent(touch);
    t.mock.timers.tick(500);
  });
  assert.deepEqual(s.log, [
    [
      "contextMenu",
      { type: "project", id: "p1", title: "Motita", x: 12, y: 34 },
    ],
  ]);
  await m.unmount();
});

test("a main branch that could not be updated is flagged with the reason", async () => {
  const s = store();
  const m = await mount(
    AppCtx,
    s.value,
    h(ProjectGroup, { p: { ...P, main_warning: "main: could not fetch" } }),
  );
  const fact = m.q("[data-testid=project-stale]");
  assert.equal(fact.textContent, "outdated");
  assert.equal(fact.title, "Main branch not updated: main: could not fetch");
  await m.rerender(s.value, h(ProjectGroup, { p: P }));
  assert.equal(m.q("[data-testid=project-stale]"), null);
  await m.unmount();
});

test("news on origin shows a tag with the count and what to do", async () => {
  const s = store();
  const news = [
    { branch: "main", behind: 2, commit: "a", latest: ["fix login", "docs"] },
    { repo: "api", branch: "main", behind: 1, commit: "b" },
  ];
  const m = await mount(
    AppCtx,
    s.value,
    h(ProjectGroup, { p: { ...P, remote_changes: news } }),
  );
  const fact = m.q("[data-testid=project-remote]");
  assert.equal(fact.textContent, "3 new");
  assert.match(fact.title, /3 new commit\(s\) on origin: fix login; docs\./);
  assert.match(fact.title, /Update main branch/);
  await m.rerender(s.value, h(ProjectGroup, { p: P }));
  assert.equal(m.q("[data-testid=project-remote]"), null);
  await m.unmount();
});
