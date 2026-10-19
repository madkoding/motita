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
  hasPodman = false,
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
        hasPodman,
        openEditProject: rec("edit"),
        setProjectPodman: rec("podman"),
        refreshProjectMain: rec("refreshMain"),
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
      p: { ...P, branch: "refs/heads/feature", podman: true, changes: 3 },
    }),
  );
  const text = m.root.textContent;
  assert.doesNotMatch(text, /feature|podman|changes/);
  const tip = m.q(".project-header .row-hit").getAttribute("title");
  assert.match(tip, /feature/);
  assert.match(tip, /podman/);
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
  assert.equal(m.q(".row-menu"), null);
  // Another row's menu is open: not this one's.
  await m.rerender(store({ rowMenu: { id: "p2" } }).value);
  assert.equal(m.q(".row-menu"), null);
  assert.equal(m.q(".row-actions-open"), null);
  await m.unmount();
});

test("the menu edits the project and asks before deleting it", async () => {
  const s = store({ rowMenu: { id: "p1" } });
  const m = await mount(AppCtx, s.value, h(ProjectGroup, { p: P }));
  assert.ok(m.q(".row-actions-open"));
  assert.equal(m.q("[data-testid=podman-toggle]"), null, "no podman here");
  await click(m.q("[data-testid=edit-project]"));
  await click(buttonByText(m.q(".row-menu"), "Delete"));
  assert.deepEqual(s.log, [
    ["edit", P],
    ["closeMenu"],
    ["confirm", { type: "project", id: "p1", title: "Motita" }],
    ["closeMenu"],
  ]);
  await m.unmount();
});

test("the menu offers to update the main branch only when the last update left a notice", async () => {
  const s = store({ rowMenu: { id: "p1" } });
  const m = await mount(AppCtx, s.value, h(ProjectGroup, { p: P }));
  assert.equal(m.q("[data-testid=refresh-main]"), null);
  const w = store({ rowMenu: { id: "p1" } });
  await m.rerender(
    w.value,
    h(ProjectGroup, { p: { ...P, main_warning: "main: could not fetch" } }),
  );
  await click(m.q("[data-testid=refresh-main]"));
  assert.deepEqual(w.log, [["refreshMain", "p1"], ["closeMenu"]]);
  await m.unmount();
});

test("the menu turns podman on where the machine has it, and off where it is on", async () => {
  const s = store({ rowMenu: { id: "p1" }, hasPodman: true });
  const m = await mount(AppCtx, s.value, h(ProjectGroup, { p: P }));
  const toggle = m.q("[data-testid=podman-toggle]");
  assert.equal(toggle.textContent, "Run with podman");
  await click(toggle);
  const on = store({ rowMenu: { id: "p1" } });
  await m.rerender(on.value, h(ProjectGroup, { p: { ...P, podman: true } }));
  const off = m.q("[data-testid=podman-toggle]");
  assert.equal(off.textContent, "Stop using podman");
  await click(off);
  assert.deepEqual(s.log, [["podman", "p1", true], ["closeMenu"]]);
  assert.deepEqual(on.log, [["podman", "p1", false], ["closeMenu"]]);
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

test("the facts line counts the shared repositories left behind their main branch", async () => {
  const s = store();
  const repos = [
    { name: "api", git_url: "u/api", dir: "/w/repos/api", behind: 2 },
    { name: "web", git_url: "u/web", dir: "/w/repos/web" },
    { name: "docs", git_url: "u/docs", dir: "/w/repos/docs", behind: 1 },
  ];
  const m = await mount(
    AppCtx,
    s.value,
    h(ProjectGroup, { p: { ...P, repos } }),
  );
  const fact = m.q("[data-testid=project-behind]");
  assert.equal(fact.textContent, "behind2");
  assert.equal(fact.title, "Behind their main branch: api, docs");
  await m.rerender(
    s.value,
    h(ProjectGroup, { p: { ...P, repos: repos.slice(1, 2) } }),
  );
  assert.equal(m.q("[data-testid=project-behind]"), null);
  await m.unmount();
});

test("the facts line says which dependencies are not installed, and how to install them", async () => {
  const s = store();
  const missing_deps = [
    {
      path: ".",
      manager: "composer",
      dir: "vendor",
      command: "composer install",
    },
    {
      path: "web",
      manager: "npm",
      dir: "node_modules",
      command: "npm install",
    },
  ];
  const m = await mount(
    AppCtx,
    s.value,
    h(ProjectGroup, { p: { ...P, missing_deps } }),
  );
  const fact = m.q("[data-testid=project-deps]");
  assert.equal(fact.textContent, "no deps");
  assert.equal(
    fact.title,
    "Dependencies not installed, so tests cannot run: composer install, npm install (web)",
  );
  await m.rerender(s.value, h(ProjectGroup, { p: P }));
  assert.equal(m.q("[data-testid=project-deps]"), null);
  await m.rerender(s.value, h(ProjectGroup, { p: { ...P, missing_deps: [] } }));
  assert.equal(m.q("[data-testid=project-deps]"), null);
  await m.unmount();
});
