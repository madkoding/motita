// The sidebar and its session rows, drawn in jsdom over a stand-in app store: what a row shows
// (title, time, facts, spinner), what its menu offers for a plain, a project and a merged session,
// renaming in place, the long press of a phone, and the sidebar's header, buttons and phone sheet.
import test, { mock } from "node:test";
import assert from "node:assert/strict";
import { installDom, source, text } from "./cov-stream-helpers.mjs";

installDom();
let phone = false;
window.matchMedia = () => ({
  matches: phone,
  addEventListener() {},
  removeEventListener() {},
});
const { AppCtx } = await source("appStore.ts");
const { SessionRow, RowMenu, DeleteItem } = await source("SessionRow.tsx");
const { Sidebar } = await source("Sidebar.tsx");
const { MachinesCtx } = await source("useMachines.ts");
const { h, render } = await import("preact");
const { act } = await import("preact/test-utils");

// store is the part of the app store the sidebar reads; every action records its call.
function store(over = {}) {
  const calls = [];
  const rec =
    (name) =>
    (...args) =>
      calls.push([name, ...args]);
  // A click handler is handed the event; only that it was called matters.
  const on = (name) => () => calls.push([name]);
  const s = {
    calls,
    rs: { sessionId: "" },
    lists: {
      sessions: [],
      projects: [],
      collapsedProjects: new Set(),
      answeredHere: () => false,
      toggleProject: rec("toggleProject"),
      closeSidebar: on("closeSidebar"),
    },
    actions: {
      renamingId: null,
      renameValue: "",
      creatingSession: null,
      createSession: rec("createSession"),
      startRename: rec("startRename"),
      setRenameValue: rec("setRenameValue"),
      renameSession: rec("renameSession"),
      setRenamingId: rec("setRenamingId"),
      continueSession: async (x) => calls.push(["continueSession", x.id]),
    },
    conv: { switchSession: rec("switchSession") },
    menus: {
      rowMenu: null,
      rowMenuAbove: false,
      rowMenuClosing: false,
      rowMenuPos: null,
      setContextMenu: rec("setContextMenu"),
      toggleRowMenu: (el, item) =>
        calls.push(["toggleRowMenu", el.tagName, item]),
      closeRowMenu: rec("closeRowMenu"),
    },
    deletion: { setConfirmDelete: rec("setConfirmDelete") },
    stream: { submit: async (...a) => calls.push(["submit", ...a]) },
    projects: { openNewProject: on("openNewProject"), hasPodman: false },
    repositories: {
      repositories: [],
      openNewRepository: on("openNewRepository"),
    },
    updates: { updateInfo: null, openUpgrade: on("openUpgrade") },
    skills: { setShowSkillLibrary: rec("setShowSkillLibrary") },
    artifacts: { openArtifacts: on("openArtifacts") },
    notes: { openNotes: on("openNotes") },
    schedules: { openScheduledTasks: on("openScheduledTasks") },
    openSettings: on("openSettings"),
  };
  for (const [k, v] of Object.entries(over)) {
    s[k] =
      typeof v === "object" && v && !Array.isArray(v) ? { ...s[k], ...v } : v;
  }
  return s;
}

// machines is the computers the sidebar is drawn among: this one only, shown and open.
const machines = {
  view: null,
  active: "",
  isOpen: (id) => id === "",
  openConnect() {},
};

async function mount(node, value) {
  const root = document.createElement("div");
  document.body.appendChild(root);
  const tree = (v) =>
    h(
      MachinesCtx.Provider,
      { value: machines },
      h(AppCtx.Provider, { value: v }, node),
    );
  await act(() => render(tree(value), root));
  return {
    root,
    rerender: (v) => act(() => render(tree(v), root)),
    done: async () => {
      await act(() => render(null, root));
      root.remove();
    },
  };
}
const row = (s, value) => mount(h(SessionRow, { s }), value);
const click = (el) =>
  act(() => el.dispatchEvent(new MouseEvent("click", { bubbles: true })));
const byTitle = (root, title) => root.querySelector(`[title="${title}"]`);
const buttonNamed = (root, name) =>
  [...root.querySelectorAll("button")].find((b) => text(b) === name);

test("a row shows the title, when it was used and its labelled facts", async () => {
  const when = new Date(2024, 4, 6, 7, 8, 9).toISOString();
  const s = {
    id: "s1abcdefghij",
    title: "Fix the build",
    last_used: when,
    agents_running: 2,
    branch: "motita/s1abcdefghij",
    worktree: "s9zzzzzzzzz",
    model: "gpt-x",
    provider: "p",
    changes: 1,
    running: true,
  };
  const v = store({ rs: { sessionId: "s1abcdefghij" } });
  const { root, rerender, done } = await row(s, v);
  const r = root.querySelector(".session-row");
  assert.match(r.className, /bg-accent\/10/, "the open session is marked");
  assert.match(root.querySelector(".session-spinner").className, /is-running/);
  assert.equal(text(root.querySelector(".truncate")), "Fix the build");
  assert.match(root.querySelector(".truncate").className, /text-accent/);
  assert.equal(
    text(root.querySelector(".session-when")),
    "06/05/2024 :: 07:08:09⧉ 2",
  );
  const facts = (r) =>
    [...r.querySelectorAll(".session-facts > span")].map((f) =>
      [...f.children].map(text).join(" "),
    );
  assert.deepEqual(facts(root), ["change 1"]);
  const hit = root.querySelector(".row-hit");
  assert.equal(hit.getAttribute("aria-label"), "Fix the build");
  assert.match(
    hit.getAttribute("title"),
    /^Last used .*\n2 background agents running\n1 uncommitted change/,
  );
  await click(hit);
  assert.deepEqual(v.calls, [["switchSession", "s1abcdefghij"]]);

  // Answered here already: no spinner although the gateway still says it runs. Another session
  // is open. Two changes, no other facts.
  const v2 = store({ lists: { answeredHere: (id) => id === "s1abcdefghij" } });
  await rerender(v2);
  assert.doesNotMatch(
    root.querySelector(".session-spinner").className,
    /is-running/,
  );
  assert.match(
    root.querySelector(".session-row").className,
    /border-transparent/,
  );
  await done();
  const plain = await row({ id: "s2", title: "", changes: 2 }, store());
  assert.equal(text(plain.root.querySelector(".truncate")), "s2");
  assert.match(
    plain.root.querySelector(".truncate").className,
    /text-\[#e8e8ea\]/,
  );
  assert.equal(text(plain.root.querySelector(".session-when")), "");
  assert.deepEqual(facts(plain.root), ["changes 2"]);
  assert.equal(
    plain.root.querySelector(".row-hit").getAttribute("title"),
    "2 uncommitted changes in this working tree",
  );
  await plain.done();
  const bare = await row({ id: "s3", title: "T" }, store());
  assert.equal(text(bare.root.querySelector(".session-facts")), "");
  assert.equal(bare.root.querySelector(".row-hit").getAttribute("title"), null);
  await bare.done();
});

test("the ⋯ button opens the row's menu, and the menu offers rename and delete", async () => {
  const s = { id: "s1", title: "Alpha" };
  const v = store();
  const { root, rerender, done } = await row(s, v);
  await click(byTitle(root, "More actions"));
  assert.deepEqual(v.calls, [
    ["toggleRowMenu", "BUTTON", { type: "session", id: "s1", title: "Alpha" }],
  ]);
  assert.equal(
    root.querySelector(".row-menu"),
    null,
    "drawn only when it is the open menu",
  );

  const open = store({
    menus: {
      rowMenu: { id: "s1" },
      rowMenuAbove: true,
      rowMenuClosing: true,
      rowMenuPos: { top: 10, left: 20 },
    },
  });
  await rerender(open);
  const menu = root.querySelector(".row-menu");
  assert.match(menu.className, / row-menu-above/);
  assert.match(menu.className, / row-menu-closing/);
  assert.equal(menu.style.top, "10px");
  assert.equal(menu.style.left, "20px");
  assert.match(
    root.querySelector(".row-actions").className,
    /row-actions-open/,
  );
  assert.deepEqual(
    [...menu.querySelectorAll("button")].map(text),
    ["Rename", "Delete"],
    "a session outside a project has nothing to integrate",
  );
  // Clicks inside the menu stay in it.
  let reached = 0;
  const onDoc = () => reached++;
  document.addEventListener("click", onDoc);
  await click(buttonNamed(menu, "Rename"));
  await click(buttonNamed(menu, "Delete"));
  document.removeEventListener("click", onDoc);
  assert.equal(reached, 0);
  assert.deepEqual(open.calls, [
    ["startRename", "s1", "Alpha"],
    ["closeRowMenu"],
    ["setConfirmDelete", { type: "session", id: "s1", title: "Alpha" }],
    ["closeRowMenu"],
  ]);
  await done();

  // A session without a title is renamed from an empty field and named by its id.
  const untitled = store({ menus: { rowMenu: { id: "s9" } } });
  const u = await row({ id: "s9" }, untitled);
  await click(buttonNamed(u.root, "Rename"));
  await click(buttonNamed(u.root, "Delete"));
  await click(byTitle(u.root, "More actions"));
  assert.deepEqual(untitled.calls, [
    ["startRename", "s9", ""],
    ["closeRowMenu"],
    ["setConfirmDelete", { type: "session", id: "s9", title: "s9" }],
    ["closeRowMenu"],
    ["toggleRowMenu", "BUTTON", { type: "session", id: "s9", title: "s9" }],
  ]);
  await u.done();
});

test("a project session can be integrated once it has work to integrate", async () => {
  const v = store({ menus: { rowMenu: { id: "p-s" } } });
  const { root, rerender, done } = await row(
    { id: "p-s", title: "W", project_id: "p1", mergeable: true },
    v,
  );
  const integrate = buttonNamed(root, "Integrate");
  assert.equal(integrate.disabled, false);
  assert.equal(
    integrate.title,
    "Integrate this session's work back into the project",
  );
  await act(async () => {
    integrate.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    await Promise.resolve();
  });
  assert.deepEqual(v.calls, [
    ["closeRowMenu"],
    ["submit", "Integrate", "/v1/sessions/p-s/merge"],
  ]);

  const idle = store({ menus: { rowMenu: { id: "p-s" } } });
  await rerender(idle);
  await done();
  const r2 = await row({ id: "p-s", title: "W", project_id: "p1" }, idle);
  const off = buttonNamed(r2.root, "Integrate");
  assert.equal(off.disabled, true);
  assert.equal(off.title, "Nothing to integrate yet");
  assert.match(off.className, /text-\[#8a8a9a\]/);
  // A click that still reaches it (a script, an old browser) does nothing and goes no further.
  let reached = 0;
  r2.root.addEventListener("click", () => reached++);
  await act(() =>
    off.dispatchEvent(new MouseEvent("click", { bubbles: true })),
  );
  assert.deepEqual(idle.calls, []);
  assert.equal(reached, 0);
  await r2.done();
});

test("a merged session offers to continue in a new one and names its commit", async () => {
  const v = store({ menus: { rowMenu: { id: "m" } } });
  const s = {
    id: "m",
    title: "Done",
    project_id: "p1",
    merged: true,
    continuable: true,
    merged_sha: "abcdef1234567",
  };
  const { root, done } = await row(s, v);
  assert.equal(buttonNamed(root, "Integrate"), undefined);
  assert.equal(
    text(root.querySelector(".row-menu div.row-menu-item")),
    "merged abcdef1",
  );
  await act(async () => {
    buttonNamed(root, "Continue").dispatchEvent(
      new MouseEvent("click", { bubbles: true }),
    );
    await Promise.resolve();
  });
  assert.deepEqual(v.calls, [["closeRowMenu"], ["continueSession", "m"]]);
  await done();
  const plain = await row(
    { id: "m", title: "Done", project_id: "p1", merged: true },
    store({ menus: { rowMenu: { id: "m" } } }),
  );
  assert.equal(buttonNamed(plain.root, "Continue"), undefined);
  assert.equal(plain.root.querySelector(".row-menu div.row-menu-item"), null);
  await plain.done();
});

test("a row being renamed is a field: Enter or ✓ saves, Escape or ✕ cancels", async () => {
  const v = store({ actions: { renamingId: "s1", renameValue: "New name" } });
  const { root, done } = await row({ id: "s1", title: "Old" }, v);
  assert.equal(root.querySelector(".row-hit"), null);
  assert.equal(root.querySelector(".session-when"), null);
  const input = root.querySelector("input");
  assert.equal(input.value, "New name");
  input.value = "Newer";
  await act(() => input.dispatchEvent(new Event("input", { bubbles: true })));
  const key = (k) =>
    act(() =>
      input.dispatchEvent(
        new KeyboardEvent("keydown", { key: k, bubbles: true }),
      ),
    );
  await key("a");
  await key("Enter");
  await key("Escape");
  let reached = 0;
  root.addEventListener("click", () => reached++);
  await click(input);
  assert.equal(reached, 0, "a click in the field does not open the session");
  await click(byTitle(root, "Confirm"));
  await click(byTitle(root, "Cancel"));
  assert.equal(reached, 0);
  assert.deepEqual(v.calls, [
    ["setRenameValue", "Newer"],
    ["renameSession", "s1", "New name"],
    ["setRenamingId", null],
    ["setRenamingId", null],
    ["renameSession", "s1", "New name"],
    ["setRenamingId", null],
    ["setRenamingId", null],
  ]);
  await done();
});

test("a long press on a row opens its menu where the finger rests", async () => {
  const v = store();
  const { root, done } = await row({ id: "s1", title: "Alpha" }, v);
  const r = root.querySelector(".session-row");
  const touch = (type, x, y) => {
    const e = new Event(type, { bubbles: true });
    e.touches = [{ clientX: x, clientY: y }];
    r.dispatchEvent(e);
  };
  mock.timers.enable({ apis: ["setTimeout"] });
  try {
    touch("touchstart", 30, 40);
    mock.timers.tick(499);
    assert.deepEqual(v.calls, []);
    mock.timers.tick(1);
    assert.deepEqual(v.calls, [
      [
        "setContextMenu",
        { type: "session", id: "s1", title: "Alpha", x: 30, y: 40 },
      ],
    ]);
  } finally {
    mock.timers.reset();
  }
  await done();
  const untitled = store();
  const u = await row({ id: "s7" }, untitled);
  mock.timers.enable({ apis: ["setTimeout"] });
  try {
    const e = new Event("touchstart", { bubbles: true });
    e.touches = [{ clientX: 1, clientY: 2 }];
    u.root.querySelector(".session-row").dispatchEvent(e);
    mock.timers.tick(500);
  } finally {
    mock.timers.reset();
  }
  assert.deepEqual(untitled.calls, [
    ["setContextMenu", { type: "session", id: "s7", title: "s7", x: 1, y: 2 }],
  ]);
  await u.done();
});

test("RowMenu and DeleteItem work for a project too", async () => {
  const v = store();
  const { root, done } = await mount(
    h(
      RowMenu,
      null,
      h(DeleteItem, { type: "project", id: "p1", title: "Proj" }),
    ),
    v,
  );
  const menu = root.querySelector(".row-menu");
  assert.equal(menu.className.includes("row-menu-above"), false);
  assert.equal(menu.getAttribute("style"), null);
  await click(buttonNamed(root, "Delete"));
  assert.deepEqual(v.calls, [
    ["setConfirmDelete", { type: "project", id: "p1", title: "Proj" }],
    ["closeRowMenu"],
  ]);
  await done();
});

test("the sidebar has its header, buttons, lists and footer", async () => {
  phone = false;
  const v = store({
    rs: { sessionId: "" },
    lists: {
      sessions: [
        { id: "a", title: "Loose" },
        { id: "b", title: "In project", project_id: "p1" },
      ],
      projects: [{ id: "p1", title: "Proj" }],
    },
    updates: {
      updateInfo: {
        current_version: "v1.2.3",
        latest_version: "v1.3.0",
        update_available: true,
      },
    },
    actions: { creatingSession: "" },
  });
  const { root, rerender, done } = await mount(h(Sidebar), v);
  assert.equal(text(root.querySelector("h1")), "Motita");
  assert.equal(text(root.querySelector("h1").nextElementSibling), "v1.2.3");
  assert.equal(
    text(root.querySelector("[data-testid=sidebar-sessions] .truncate")),
    "Loose",
  );
  assert.deepEqual(
    [...root.querySelectorAll("[data-testid=sidebar-empty]")].map(text),
    ["No repositories"],
    "only the repositories are none",
  );
  // Each section title carries its own "+", named for what it adds, and nothing sits above them.
  const addSession = root.querySelector("[data-testid=sidebar-new-session]");
  const addProject = root.querySelector("[data-testid=sidebar-new-project]");
  assert.equal(addSession.getAttribute("aria-label"), "New session");
  assert.equal(addProject.getAttribute("aria-label"), "New project");
  assert.ok(
    root
      .querySelector("[data-testid=sidebar-sessions] h2")
      .parentElement.contains(addSession),
  );
  assert.ok(
    root
      .querySelector("[data-testid=sidebar-projects] h2")
      .parentElement.contains(addProject),
  );
  assert.equal(
    root.querySelector(".btn-cut"),
    null,
    "the big New session button is gone",
  );
  assert.ok(
    addSession.querySelector(".btn-spinner"),
    "the new session button shows it is busy",
  );
  assert.equal(addSession.disabled, true);

  await click(root.querySelector('[aria-label="Close sidebar"]'));
  await click(addProject);
  await click(root.querySelector("[data-testid=sidebar-new-repository]"));
  const upgrade = buttonNamed(root, "Upgrade Motitav1.3.0");
  assert.equal(text(upgrade.querySelector(".font-mono")), "v1.3.0");
  await click(upgrade);
  await click(buttonNamed(root, "Skill library"));
  await click(root.querySelector("[data-testid=notes-button]"));
  const art = root.querySelector("[data-testid=artifacts-button]");
  assert.equal(art.disabled, true, "no session: no artifacts");
  await click(buttonNamed(root, "Scheduled tasks"));
  await click(buttonNamed(root, "Settings"));
  assert.deepEqual(v.calls, [
    ["closeSidebar"],
    ["openNewProject"],
    ["openNewRepository"],
    ["openUpgrade"],
    ["setShowSkillLibrary", true],
    ["openNotes"],
    ["openScheduledTasks"],
    ["openSettings"],
  ]);

  // Up to date, no version known, a session open, nothing being created.
  const v2 = store({
    rs: { sessionId: "a" },
    updates: { updateInfo: { update_available: false } },
  });
  await rerender(v2);
  assert.equal(
    root.querySelector("h1").nextElementSibling.tagName,
    "BUTTON",
    "no version shown",
  );
  assert.equal(buttonNamed(root, "Upgrade Motitav1.3.0"), undefined);
  assert.equal(root.querySelector(".btn-spinner"), null);
  await click(root.querySelector("[data-testid=artifacts-button]"));
  await click(root.querySelector("[data-testid=sidebar-new-session]"));
  // A click on the dimmed layer closes the sidebar; one inside it does not.
  const overlay = root.querySelector(".bg-black\\/50");
  await click(overlay);
  assert.deepEqual(v2.calls, [
    ["openArtifacts"],
    ["createSession"],
    ["closeSidebar"],
  ]);
  await done();
});

test("an empty sidebar says so in each section", async () => {
  const { root, done } = await mount(h(Sidebar), store());
  assert.deepEqual(
    [...root.querySelectorAll("[data-testid=sidebar-empty]")].map(text),
    ["No sessions", "No projects", "No repositories"],
  );
  await done();
});

test("with a gateway to ask, the bottom menu shows how much of each plan is used", async () => {
  const api = async (path) => {
    assert.equal(path, "/v1/llm/usage");
    return new Response(
      JSON.stringify({
        providers: [
          {
            id: "openai",
            name: "OpenAI",
            input: 1500,
            output: 500,
            cache_read: 0,
            cache_write: 0,
            calls: 2,
          },
        ],
      }),
      { status: 200 },
    );
  };
  const { root, done } = await mount(h(Sidebar), store({ api }));
  await act(() => new Promise((r) => setImmediate(r)));
  const shown = text(root.querySelector("[data-testid=provider-usage]"));
  await done();
  assert.match(shown, /Provider usageOpenAI—/);
});

test("on a phone, picking a session in the sidebar closes it", async () => {
  const v = store({ lists: { sessions: [{ id: "a", title: "A" }] } });
  const { root, done } = await mount(h(Sidebar), v);
  phone = false;
  await click(root.querySelector(".row-hit"));
  assert.deepEqual(
    v.calls,
    [["switchSession", "a"]],
    "a wide screen keeps it open",
  );
  phone = true;
  v.calls.length = 0;
  await click(root.querySelector(".row-hit"));
  assert.deepEqual(v.calls, [["switchSession", "a"], ["closeSidebar"]]);
  v.calls.length = 0;
  await click(buttonNamed(root, "Settings"));
  assert.deepEqual(v.calls, [["openSettings"]], "other clicks do not close it");
  phone = false;
  await done();
});
