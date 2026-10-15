// The sidebar labels its two sections, free-standing sessions and projects, and says so when one is
// empty. The upgrade modal has no button to press once the gateway restarts: it shows a spinner
// and waits for the page to reload by itself, and offers a manual reload only if that never comes.
// Runs the real components in jsdom (via tsx-hooks.mjs, since node cannot import TSX).
// Run with: node --test web/src/__tests__/sidebar.test.mjs
import test, { mock } from "node:test";
import assert from "node:assert/strict";
import { JSDOM } from "jsdom";
import { load } from "./tsx-hooks.mjs";

const SIDEBAR_EMPTY = "[data-testid=sidebar-empty]";
const UPGRADE_RELOADING = "[data-testid=upgrade-reloading]";
const dom = new JSDOM("<!doctype html><html><body></body></html>", {
  url: "http://localhost/",
});
for (const k of [
  "document",
  "window",
  "MouseEvent",
  "Event",
  "HTMLElement",
  "Node",
  "localStorage",
]) {
  globalThis[k] = dom.window[k];
}
Object.defineProperty(globalThis, "navigator", {
  value: dom.window.navigator,
  configurable: true,
});
window.matchMedia = () => ({
  matches: false,
  addEventListener() {},
  removeEventListener() {},
});
globalThis.requestAnimationFrame = (cb) => setTimeout(() => cb(Date.now()), 0);
globalThis.cancelAnimationFrame = (id) => clearTimeout(id);

// One module graph per process, so the components and the test share the same AppCtx.
const { SessionList } = await load("Sidebar.tsx");
const { UpgradeModal } = await load("UpgradeModal.tsx");
const { useUpdates } = await load("useUpdates.ts");
const { AppCtx } = await load("appStore.ts");
const { h, render } = await import("preact");
const { act } = await import("preact/test-utils");

const noop = () => {};

// store is the part of the app store the sidebar list and the upgrade modal read.
function store({ sessions = [], projects = [], updates = {} } = {}) {
  return {
    rs: { sessionId: "" },
    lists: {
      sessions,
      projects,
      collapsedProjects: new Set(),
      answeredHere: () => false,
      toggleProject: noop,
    },
    actions: {
      renamingId: null,
      creatingSession: null,
      createSession: noop,
    },
    conv: { switchSession: noop },
    menus: { rowMenu: null, setContextMenu: noop },
    projects: {},
    updates: {
      upgradeBusy: false,
      upgradeProgress: null,
      upgradeError: "",
      showUpgradeError: false,
      reloadStalled: false,
      awaitingReload: false,
      reloadPage: noop,
      closeUpgradeIfIdle: noop,
      finishUpgrade: noop,
      toggleUpgradeError: noop,
      ...updates,
    },
  };
}

async function mount(component, value) {
  const root = document.createElement("div");
  document.body.appendChild(root);
  await act(() => {
    render(h(AppCtx.Provider, { value }, h(component, {})), root);
  });
  const done = async () => {
    await act(() => render(null, root));
    root.remove();
  };
  return { root, done };
}

const section = (root, name) =>
  root.querySelector(`[data-testid=sidebar-${name}]`);
const text = (el) => el.textContent.replace(/\s+/g, " ").trim();

test("the sidebar labels sessions and projects and says when each is empty", async () => {
  const { root, done } = await mount(SessionList, store());
  const sessions = section(root, "sessions");
  const projects = section(root, "projects");
  assert.ok(sessions && projects, "both sections are drawn");
  assert.equal(
    sessions.compareDocumentPosition(projects) &
      dom.window.Node.DOCUMENT_POSITION_FOLLOWING,
    dom.window.Node.DOCUMENT_POSITION_FOLLOWING,
    "sessions come first",
  );
  assert.equal(text(sessions.querySelector("h2")), "Sessions");
  assert.equal(text(projects.querySelector("h2")), "Projects");
  assert.equal(
    sessions.getAttribute("aria-labelledby"),
    sessions.querySelector("h2").id,
  );
  assert.equal(text(sessions.querySelector(SIDEBAR_EMPTY)), "No sessions");
  assert.equal(text(projects.querySelector(SIDEBAR_EMPTY)), "No projects");
  await done();
});

test("a free session goes under Sessions, a project's session under its project", async () => {
  const { root, done } = await mount(
    SessionList,
    store({
      sessions: [
        { id: "s1", title: "Loose one" },
        { id: "s2", title: "In project", project_id: "p1" },
      ],
      projects: [{ id: "p1", title: "Motita" }],
    }),
  );
  const sessions = section(root, "sessions");
  const projects = section(root, "projects");
  assert.equal(sessions.querySelector(SIDEBAR_EMPTY), null);
  assert.equal(projects.querySelector(SIDEBAR_EMPTY), null);
  assert.match(text(sessions), /Loose one/);
  assert.doesNotMatch(text(sessions), /In project/);
  assert.match(text(projects), /Motita/);
  assert.match(text(projects), /In project/);
  await done();
});

test("only projects: the sessions section still says there are none", async () => {
  const { root, done } = await mount(
    SessionList,
    store({ projects: [{ id: "p1", title: "Motita" }] }),
  );
  assert.equal(
    text(section(root, "sessions").querySelector(SIDEBAR_EMPTY)),
    "No sessions",
  );
  assert.equal(section(root, "projects").querySelector(SIDEBAR_EMPTY), null);
  await done();
});

const buttonLabels = (root) =>
  [...root.querySelectorAll("button")]
    .map((b) => text(b) || b.getAttribute("aria-label"))
    .filter(Boolean);

for (const stage of ["done", "restarting"]) {
  test(`once the upgrade is ${stage}, a spinner waits for the reload and there is no Done`, async () => {
    const { root, done } = await mount(
      UpgradeModal,
      store({
        updates: {
          upgradeBusy: true,
          awaitingReload: true,
          upgradeProgress: { stage, percent: 100, message: "ok" },
        },
      }),
    );
    const notice = root.querySelector(UPGRADE_RELOADING);
    assert.ok(notice, "the reload notice is shown");
    assert.equal(notice.getAttribute("role"), "status");
    assert.ok(notice.querySelector(".animate-spin"), "it spins");
    assert.match(text(notice), /Wait for the page to reload automatically/);
    const labels = buttonLabels(root);
    assert.ok(!labels.includes("Done"), "no Done button");
    assert.deepEqual(labels, ["Close"], "only the header's ✕ is left");
    assert.ok(
      root.querySelector("button[aria-label=Close]").disabled,
      "and it is disabled while the page waits",
    );
    await done();
  });
}

test("if the gateway never comes back, the modal offers a manual reload", async () => {
  let reloads = 0;
  const { root, done } = await mount(
    UpgradeModal,
    store({
      updates: {
        upgradeProgress: { stage: "done", percent: 100 },
        awaitingReload: true,
        reloadStalled: true,
        reloadPage: () => {
          reloads += 1;
        },
      },
    }),
  );
  assert.equal(root.querySelector(UPGRADE_RELOADING), null);
  const reload = [...root.querySelectorAll("button")].find(
    (b) => text(b) === "Reload page",
  );
  assert.ok(reload, "a reload button");
  await act(() => reload.click());
  assert.equal(reloads, 1);
  assert.ok(!buttonLabels(root).includes("Done"));
  await done();
});

test("a failed upgrade still has a Close button", async () => {
  let closed = 0;
  const { root, done } = await mount(
    UpgradeModal,
    store({
      updates: {
        upgradeProgress: { stage: "error", percent: 40, message: "boom" },
        upgradeError: "boom",
        finishUpgrade: () => {
          closed += 1;
        },
      },
    }),
  );
  assert.equal(root.querySelector(UPGRADE_RELOADING), null);
  const close = [...root.querySelectorAll("button")].find(
    (b) => text(b) === "Close",
  );
  assert.ok(close);
  await act(() => close.click());
  assert.equal(closed, 1);
  await done();
});

test("a run that ends without a restart keeps its message and a Close button", async () => {
  const { root, done } = await mount(
    UpgradeModal,
    store({
      updates: {
        upgradeProgress: {
          stage: "done",
          percent: 100,
          message: "Already up to date",
        },
      },
    }),
  );
  assert.equal(root.querySelector(UPGRADE_RELOADING), null);
  assert.ok(buttonLabels(root).includes("Close"));
  assert.match(text(root), /Already up to date/);
  await done();
});

// runStream drives the real useUpdates hook through one upgrade stream and returns its state.
async function runStream(events) {
  const body = events
    .map((e) => `event: progress\ndata: ${JSON.stringify(e)}\n\n`)
    .join("");
  const api = async () => new Response(body, { status: 200 });
  // The hook's state lives on an object: the probe writes it on every render.
  const probe = {};
  function Probe() {
    probe.state = useUpdates(api, noop, false);
    return null;
  }
  const root = document.createElement("div");
  await act(() => render(h(Probe, {}), root));
  // The reload poll is not this test's subject: fake timers drop it instead of letting it run.
  mock.timers.enable({ apis: ["setTimeout", "setInterval"] });
  try {
    await act(async () => {
      await probe.state.runUpgrade();
    });
  } finally {
    mock.timers.reset();
  }
  const snapshot = probe.state;
  await act(() => render(null, root));
  assert.ok(snapshot, "the hook rendered");
  return snapshot;
}

test("the hook waits for the reload only when the gateway said it restarts", async () => {
  const restarted = await runStream([
    { stage: "restarting", percent: 100, message: "Restarting the gateway" },
    { stage: "done", percent: 100, message: "Upgrade complete." },
  ]);
  assert.equal(restarted.awaitingReload, true);
  assert.equal(restarted.upgradeBusy, true, "locked until the page reloads");

  const upToDate = await runStream([
    { stage: "done", message: "Already up to date" },
  ]);
  assert.equal(upToDate.awaitingReload, false);
  assert.equal(upToDate.upgradeBusy, false, "it can be closed");
});
