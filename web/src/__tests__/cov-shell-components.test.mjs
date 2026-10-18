// The parts of the page shell drawn from the store: the header, the session modal and the approval
// panel, the three drawers on the right edge, the toast and the token prompt. Each is rendered in
// jsdom with a store made of plain values and recording functions.
// Run with: node --test web/src/__tests__/cov-shell-components.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import {
  setupDom,
  load,
  stub,
  stubModule,
  mounter,
  text,
  flush,
  APP_STORE_STUB,
} from "./cov-shell-helpers.mjs";

setupDom();
stub("./appStore", APP_STORE_STUB);

const { AppCtx } = await stubModule("./appStore");
const { ChatHeader } = await load("ChatHeader.tsx");
const { ChatLoadingModal, ApprovalPanel } = await load("ChatPanels.tsx");
const { TermDrawer, ThinkDrawer, AgentsDrawer } = await load("Drawers.tsx");
const { SubagentsStrip } = await load("ConversationParts.tsx");
const { ToastCard } = await load("ToastCard.tsx");
const { AuthModal } = await load("AuthModal.tsx");
const { ConnectionBanner } = await load("ConnectionBanner.tsx");
const { h } = await import("preact");
const { act } = await import("preact/test-utils");
const mount = await mounter();

// recorder returns a function that logs its calls under `name`.
function recorder() {
  const calls = [];
  // Event arguments (a click handed straight to the handler) are left out of the log.
  const fn =
    (name) =>
    (...args) =>
      calls.push([name, ...args.filter((a) => !(a instanceof window.Event))]);
  return { calls, fn };
}

async function show(Component, store, props = {}) {
  const m = await mount(
    h(AppCtx.Provider, { value: store }, h(Component, props)),
  );
  return {
    ...m,
    update: (s, p = props) =>
      m.rerender(h(AppCtx.Provider, { value: s }, h(Component, p))),
  };
}

// ── ChatHeader ───────────────────────────────────────────────────────────────

function headerStore(over = {}) {
  const { calls, fn } = recorder();
  const store = {
    rs: { noSession: false, ...over.rs },
    lists: {
      sidebarOpen: false,
      openSidebar: fn("openSidebar"),
      ...over.lists,
    },
    status: { stateText: "ready", stateClass: "", ...over.status },
    model: {
      config: {
        provider: "openai",
        model: "gpt-5",
        reasoning: "high",
        reasoning_enabled: true,
        api_key_present: true,
      },
      openModelSwitcher: fn("openModelSwitcher"),
      ...over.model,
    },
    shell: { stopAutoApprove: fn("stopAutoApprove") },
    selectedSession: over.selectedSession,
  };
  return { store, calls };
}

test("the header names the session, shows the state and the model, and opens the sidebar", async () => {
  const { store, calls } = headerStore({
    selectedSession: { id: "s1", title: "Fix the build" },
    status: { stateText: "working", stateClass: "state-working" },
  });
  const { root, done } = await show(ChatHeader, store);
  assert.equal(text(root.querySelector("h1")), "Fix the build");
  const pill = root.querySelector(".state-pill");
  assert.ok(pill.classList.contains("state-working"));
  assert.equal(text(pill), "working");
  const model = [...root.querySelectorAll("button")].find((b) =>
    /gpt-5/.test(text(b)),
  );
  assert.equal(text(model), "openai / gpt-5high");
  assert.equal(model.title, "openai / gpt-5 — reasoning: high");
  assert.equal(text(model.querySelector(".header-reasoning")), "high");
  assert.ok(
    model.querySelector(".header-reasoning").classList.contains("is-high"),
  );
  model.click();
  root.querySelector("[aria-label='Open sidebar']").click();
  assert.deepEqual(calls, [["openModelSwitcher"], ["openSidebar"]]);
  assert.equal(root.querySelector(".auto-approve-pill"), null);
  await done();
});

test("with the sidebar open and no session the header is the bare title and state", async () => {
  const { store } = headerStore({
    lists: { sidebarOpen: true },
    rs: { noSession: true },
  });
  const { root, done } = await show(ChatHeader, store);
  assert.equal(root.querySelector("[aria-label='Open sidebar']"), null);
  assert.equal(text(root.querySelector("h1")), "Motita");
  assert.equal(
    root.querySelectorAll("button").length,
    0,
    "no model pill without a session",
  );
  await done();
});

test("without a config there is no model pill; an all-allowed session offers to ask again", async () => {
  const { store, calls } = headerStore({
    lists: { sidebarOpen: true },
    model: { config: null },
    selectedSession: { id: "s2", title: "Auto", auto_approve: true },
  });
  const { root, done } = await show(ChatHeader, store);
  const pill = root.querySelector(".auto-approve-pill");
  assert.equal(text(pill), "all allowed ✕");
  assert.match(pill.title, /runs without asking/);
  assert.equal(root.querySelectorAll("button").length, 1);
  pill.click();
  assert.deepEqual(calls, [["stopAutoApprove"]]);
  await done();
});

// ── ChatPanels ───────────────────────────────────────────────────────────────

test("the session modal says it is loading, and fades while it leaves", async () => {
  const { root, update, done } = await show(ChatLoadingModal, {
    scroll: { modalLeaving: false },
  });
  const scrim = root.querySelector(".chat-modal-scrim");
  assert.equal(scrim.className, "chat-modal-scrim");
  assert.equal(
    text(root.querySelector(".chat-modal-label")),
    "Loading session",
  );
  await update({ scroll: { modalLeaving: true } });
  assert.equal(scrim.className, "chat-modal-scrim chat-modal-leaving");
  await done();
});

function approvalStore(sel = 0) {
  const { calls, fn } = recorder();
  const store = {
    rs: {
      approvalSel: sel,
      setApprovalSel: fn("setApprovalSel"),
      approvalRef: { current: null },
    },
    shell: {
      answerApproval: fn("answerApproval"),
      onApprovalKey: (e) => calls.push(["key", e.key]),
    },
  };
  return { store, calls };
}

test("the approval panel asks the question, shows the command and takes the three answers", async () => {
  const { store, calls } = approvalStore(1);
  const approval = {
    id: "ap1",
    question: "Run the migration?",
    reason: "writes",
    command: "make migrate",
  };
  const { root, done } = await show(ApprovalPanel, store, { approval });
  const panel = root.querySelector("fieldset");
  assert.equal(panel.getAttribute("aria-label"), "This needs your approval");
  assert.equal(text(root.querySelector("h2")), "Run the migration?");
  assert.equal(text(root.querySelector("pre")), "make migrate");
  const [run, all, no] = root.querySelectorAll("button");
  assert.deepEqual(
    [text(run), text(all), text(no)],
    ["Run it", "Allow all this session", "No"],
  );
  assert.deepEqual(
    [run, all, no].map((b) => b.getAttribute("aria-pressed")),
    ["false", "true", "false"],
  );
  assert.ok(all.className.endsWith(" is-picked"));
  assert.ok(!run.className.includes("is-picked"));
  for (const b of [run, all, no]) {
    b.dispatchEvent(new MouseEvent("mouseover", { bubbles: true }));
    b.dispatchEvent(new MouseEvent("mouseenter"));
    b.click();
  }
  panel.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowRight" }));
  assert.deepEqual(
    calls.filter((c) => c[0] !== "setApprovalSel"),
    [
      ["answerApproval", "ap1", true],
      ["answerApproval", "ap1", true, true],
      ["answerApproval", "ap1", false],
      ["key", "ArrowRight"],
    ],
  );
  assert.deepEqual(
    [
      ...new Set(
        calls.filter((c) => c[0] === "setApprovalSel").map((c) => c[1]),
      ),
    ],
    [0, 1, 2],
  );
  await done();
});

test("an approval without a question uses its reason, and without either the default", async () => {
  const { store } = approvalStore();
  let { root, done } = await show(ApprovalPanel, store, {
    approval: { id: "a", reason: "Touches /etc" },
  });
  assert.equal(text(root.querySelector("h2")), "Touches /etc");
  assert.equal(text(root.querySelector("pre")), "");
  await done();
  ({ root, done } = await show(ApprovalPanel, approvalStore().store, {
    approval: { id: "b" },
  }));
  assert.equal(text(root.querySelector("h2")), "This needs your approval");
  await done();
});

// ── Drawers ──────────────────────────────────────────────────────────────────

function drawerStore({ rs = {}, drawers = {} } = {}) {
  const { calls, fn } = recorder();
  const store = {
    rs: {
      shellLog: [],
      termSeenId: 0,
      running: false,
      liveThought: null,
      stepsByTurn: {},
      agents: [],
      agentOpenId: null,
      ...rs,
    },
    drawers: {
      termOpen: false,
      thinkOpen: false,
      agentsOpen: false,
      agentCopied: null,
      agentNow: Date.parse("2024-01-01T00:01:00Z"),
      term: { ref: { current: null }, handlers: {} },
      think: { ref: { current: null }, handlers: {} },
      stickTerm: fn("stickTerm"),
      toggleTerm: fn("toggleTerm"),
      closeTerm: fn("closeTerm"),
      toggleThink: fn("toggleThink"),
      closeThink: fn("closeThink"),
      toggleAgents: fn("toggleAgents"),
      closeAgents: fn("closeAgents"),
      toggleAgent: fn("toggleAgent"),
      copyMerge: fn("copyMerge"),
      termEntryDone: fn("termEntryDone"),
      ...drawers,
    },
  };
  return { store, calls };
}

const entry = (id, exit, out = "") => ({ id, cmd: "cmd" + id, exit, out });

test("a closed terminal drawer mounts nothing and offers to open", async () => {
  const { store, calls } = drawerStore();
  const { root, done } = await show(TermDrawer, store);
  const tab = root.querySelector(".term-tab");
  assert.equal(tab.title, "Open the terminal history");
  assert.equal(tab.getAttribute("aria-expanded"), "false");
  assert.equal(root.querySelector(".term-tab-count"), null);
  assert.equal(
    root.querySelector(".term-panel").getAttribute("aria-hidden"),
    "true",
  );
  assert.equal(root.querySelector(".term-close").tabIndex, -1);
  assert.equal(
    text(root.querySelector(".term-titlebar span")),
    "motita@shell — 0 commands",
  );
  assert.equal(root.querySelector(".term-screen").children.length, 0);
  assert.equal(
    root.querySelectorAll(".term-fx").length,
    4,
    "the full CRT effects",
  );
  tab.click();
  root.querySelector(".term-close").click();
  assert.deepEqual(calls, [["toggleTerm"], ["closeTerm"]]);
  await done();
});

test("an open empty terminal says no commands yet", async () => {
  const { store } = drawerStore({ drawers: { termOpen: true } });
  const { root, done } = await show(TermDrawer, store);
  assert.ok(root.querySelector(".term-drawer").classList.contains("is-open"));
  assert.equal(root.querySelector(".term-tab").title, "Close the terminal");
  assert.equal(root.querySelector(".term-close").tabIndex, 0);
  await act(() => flush(5));
  assert.equal(text(root.querySelector(".term-empty")), "no commands yet");
  await done();
});

test("the terminal draws what was seen whole, types the next and holds the rest", async () => {
  const NO_RESULT = -999;
  const { store, calls } = drawerStore({
    drawers: { termOpen: true },
    rs: {
      termSeenId: 3,
      shellLog: [
        entry(1, 0, "fine"),
        entry(2, 2),
        entry(3, NO_RESULT),
        entry(4, null),
        entry(5, 0),
      ],
    },
  });
  const { root, done } = await show(TermDrawer, store);
  await act(() => flush(5));
  assert.equal(text(root.querySelector(".term-tab-count")), "5");
  assert.equal(
    text(root.querySelector(".term-titlebar span")),
    "motita@shell — 5 commands",
  );
  const entries = root.querySelectorAll(".term-entry");
  assert.equal(
    entries.length,
    4,
    "the one after the live entry waits, undrawn",
  );
  const statuses = [...root.querySelectorAll(".term-status")].map((s) => [
    text(s),
    s.className,
  ]);
  assert.deepEqual(statuses.slice(0, 3), [
    ["[ok]", "term-status"],
    ["[exit 2]", "term-status bad"],
    ["[no result]", "term-status"],
  ]);
  assert.match(text(entries[0]), /cmd1/);
  assert.match(text(entries[0]), /fine/);
  // The seen entries report they are finished; the running one never does.
  const done1 = calls.filter((c) => c[0] === "termEntryDone").map((c) => c[1]);
  assert.deepEqual([...new Set(done1)].sort(), [1, 2, 3]);
  assert.ok(
    calls.some((c) => c[0] === "stickTerm"),
    "typing keeps the screen at the end",
  );
  // The live one is typed until its command is in, then shows it is still running.
  for (
    let i = 0;
    i < 20 && !text(root.querySelector(".term-status.running"));
    i++
  ) {
    await act(() => new Promise((r) => setTimeout(r, 20)));
  }
  assert.equal(text(root.querySelector(".term-status.running")), "running…");
  await done();
});

test("a long history shows its newest window, and the earlier ones on demand", async () => {
  const log = Array.from({ length: 51 }, (_, i) => entry(i + 1, 0));
  const { store } = drawerStore({
    drawers: { termOpen: true },
    rs: { shellLog: log, termSeenId: 51 },
  });
  const { root, done } = await show(TermDrawer, store);
  const earlier = root.querySelector(".term-earlier");
  assert.equal(text(earlier), "Show 1 earlier");
  assert.equal(root.querySelectorAll(".term-entry").length, 50);
  await act(() => earlier.click());
  assert.equal(root.querySelector(".term-earlier"), null);
  assert.equal(root.querySelectorAll(".term-entry").length, 51);
  await done();

  const many = Array.from({ length: 70 }, (_, i) => entry(i + 1, 0));
  const big = drawerStore({
    drawers: { termOpen: true },
    rs: { shellLog: many, termSeenId: 70 },
  });
  const m2 = await show(TermDrawer, big.store);
  assert.equal(text(m2.root.querySelector(".term-earlier")), "Show 20 earlier");
  await m2.done();
});

test("the window never starts after the entry waiting to be typed", async () => {
  const log = Array.from({ length: 60 }, (_, i) => entry(i + 1, 0));
  // Entry 3 is the first not yet played: it and everything after stay on screen.
  const { store } = drawerStore({
    rs: { shellLog: log, termSeenId: 2 },
    drawers: { termOpen: true },
  });
  const { root, done } = await show(TermDrawer, store);
  assert.equal(text(root.querySelector(".term-earlier")), "Show 2 earlier");
  await done();
  // Closed, nothing is live: entries not yet seen wait.
  const closed = drawerStore({
    rs: { shellLog: [entry(1, 0)], termSeenId: 0 },
  });
  const m2 = await show(TermDrawer, closed.store);
  assert.equal(m2.root.querySelectorAll(".term-entry").length, 0);
  await m2.done();
});

test("the thinking drawer lists the thoughts in turn order, the live one last", async () => {
  const { store, calls } = drawerStore({
    drawers: { thinkOpen: true },
    rs: {
      running: true,
      liveThought: "weighing options",
      stepsByTurn: {
        10: [{ kind: "thought", text: "thinking: second" }],
        2: [
          { kind: "step", text: "ran ls" },
          { kind: "thought", text: "first" },
        ],
      },
    },
  });
  const { root, done } = await show(ThinkDrawer, store);
  const tab = root.querySelector(".term-tab");
  assert.equal(tab.className, "term-tab is-live");
  assert.equal(tab.title, "Close the thinking");
  assert.equal(text(tab.querySelector(".term-tab-count")), "3");
  assert.equal(
    text(root.querySelector(".term-titlebar span")),
    "motita@mind — 3 thoughts",
  );
  const thoughts = [...root.querySelectorAll(".think-entry")];
  assert.deepEqual(
    thoughts.map((t) => text(t)),
    ["first", "second", "weighing options"],
  );
  assert.deepEqual(
    thoughts.map((t) => t.className),
    ["think-entry", "think-entry", "think-entry is-live"],
  );
  tab.click();
  root.querySelector(".term-close").click();
  assert.deepEqual(calls, [["toggleThink"], ["closeThink"]]);
  await done();
});

test("a closed thinking drawer mounts nothing; an open empty one says so; a long one folds", async () => {
  let s = drawerStore({
    rs: {
      liveThought: "ignored while idle",
      stepsByTurn: { 1: [{ kind: "thought", text: "one" }] },
    },
  });
  let m = await show(ThinkDrawer, s.store);
  assert.equal(
    m.root.querySelector(".term-tab").className,
    "term-tab",
    "a thought without a run is not live",
  );
  assert.equal(m.root.querySelector(".term-tab").title, "Open the thinking");
  assert.equal(
    text(m.root.querySelector(".term-titlebar span")),
    "motita@mind — 1 thought",
  );
  assert.equal(m.root.querySelector(".term-screen").children.length, 0);
  assert.equal(m.root.querySelector(".term-close").tabIndex, -1);
  await m.done();

  s = drawerStore({ drawers: { thinkOpen: true } });
  m = await show(ThinkDrawer, s.store);
  assert.equal(m.root.querySelector(".term-tab-count"), null);
  assert.equal(text(m.root.querySelector(".term-empty")), "no thoughts yet");
  await m.done();

  const steps = {
    1: Array.from({ length: 55 }, (_, i) => ({
      kind: "thought",
      text: "t" + i,
    })),
  };
  s = drawerStore({ drawers: { thinkOpen: true }, rs: { stepsByTurn: steps } });
  m = await show(ThinkDrawer, s.store);
  assert.equal(m.root.querySelectorAll(".think-entry").length, 50);
  await act(() => m.root.querySelector(".term-earlier").click());
  assert.equal(m.root.querySelectorAll(".think-entry").length, 55);
  await m.done();
});

const agent = (over) => ({
  id: "a1",
  purpose: "fix tests",
  state: "running",
  started: "2024-01-01T00:00:00Z",
  elapsed_ms: 0,
  tokens: { input: 1000, output: 500 },
  calls: 1,
  ...over,
});

test("the agents drawer is not there until a run has something to show", async () => {
  let { root, done } = await show(
    AgentsDrawer,
    drawerStore({ rs: { agents: [] } }).store,
  );
  assert.equal(root.innerHTML, "");
  await done();
  ({ root, done } = await show(
    AgentsDrawer,
    drawerStore({
      rs: { agents: [agent({ tokens: { input: 0, output: 0 } })] },
    }).store,
  ));
  assert.equal(root.innerHTML, "", "a main agent that spent nothing yet");
  await done();
  ({ root, done } = await show(
    AgentsDrawer,
    drawerStore({ rs: { agents: [agent({ state: "passed" })] } }).store,
  ));
  assert.ok(
    root.querySelector(".is-agents"),
    "a main agent that spent tokens is shown",
  );
  assert.equal(root.querySelector(".term-tab").className, "term-tab");
  assert.equal(root.querySelector(".term-tab-count"), null);
  assert.equal(root.querySelector(".term-tab").title, "Open the agents");
  assert.equal(root.querySelector(".term-close").tabIndex, -1);
  assert.equal(
    root.querySelectorAll(".term-fx").length,
    2,
    "the light CRT effects",
  );
  await done();
});

test("the agents drawer lists the fleet with time, tokens and state", async () => {
  const { store, calls } = drawerStore({
    drawers: { agentsOpen: true },
    rs: {
      agents: [
        agent({
          id: "main",
          purpose: "",
          tokens: undefined,
          round: 2,
          activity: "planning",
        }),
        agent({
          id: "sub1",
          parent: "main",
          purpose: "write docs",
          activity: "editing README",
          tokens: { input: 12000, output: 345, cache_read: 7 },
        }),
        agent({
          id: "sub2",
          parent: "main",
          purpose: "",
          state: "failed",
          finished: "2024-01-01T00:00:30Z",
          activity: "gave up",
        }),
      ],
    },
  });
  const { root, done } = await show(AgentsDrawer, store);
  const tab = root.querySelector(".term-tab");
  assert.equal(tab.className, "term-tab is-live");
  assert.equal(tab.title, "Close the agents");
  assert.equal(
    text(tab.querySelector(".term-tab-count")),
    "1",
    "one sub-agent running",
  );
  const title = root.querySelector(".term-titlebar span");
  assert.equal(text(title), "motita@fleet — 2 subagents · 14k tok");
  assert.equal(title.title, "in 13k · out 845 · cache read 7");
  const rows = [...root.querySelectorAll(".agent-row")];
  assert.deepEqual(
    rows.map((r) => r.className),
    [
      "agent-row is-running",
      "agent-row is-running",
      "agent-row is-failed is-done",
    ],
  );
  // The main agent: tagged, named by its id when it has no purpose, its round shown.
  assert.equal(text(rows[0].querySelector(".agent-purpose")), "mainmain");
  assert.equal(
    text(rows[0].querySelector(".agent-meta")),
    "1m 00s · 0 tok · r2",
  );
  assert.equal(rows[0].querySelector(".agent-meta").title, "in 0 · out 0");
  assert.ok(rows[0].querySelector(".agent-spin"));
  assert.equal(
    rows[0].querySelector(".row-hit"),
    null,
    "a running agent does not fold",
  );
  assert.equal(text(rows[1].querySelector(".agent-purpose")), "write docs");
  assert.equal(text(rows[1].querySelector(".agent-meta")), "1m 00s · 12k tok");
  assert.equal(
    text(rows[1].querySelector(".agent-activity")),
    "editing README",
  );
  assert.equal(text(rows[2].querySelector(".agent-state")), "✗");
  assert.equal(
    rows[2].querySelector(".agent-state").getAttribute("aria-label"),
    "failed",
  );
  assert.equal(text(rows[2].querySelector(".agent-meta")), "30s · 1.5k tok");
  const hit = rows[2].querySelector(".row-hit");
  assert.equal(hit.getAttribute("aria-expanded"), "false");
  assert.equal(
    hit.getAttribute("aria-label"),
    "sub2",
    "named by its id without a purpose",
  );
  hit.click();
  tab.click();
  root.querySelector(".term-close").click();
  assert.deepEqual(calls, [
    ["toggleAgent", "sub2"],
    ["toggleAgents"],
    ["closeAgents"],
  ]);
  await done();
});

test("a finished agent shows how it ended instead of the phase it was in, and a running one is worded", async () => {
  const done = agent({
    id: "e1",
    parent: "m",
    state: "passed",
    summary: "Docs written.\nsecond",
    activity: "",
  });
  const none = agent({ id: "e2", parent: "m", state: "failed", activity: "" });
  const busy = agent({
    id: "e3",
    parent: "m",
    activity: "waiting for 2 background agent(s)...",
  });
  const { store } = drawerStore({
    drawers: { agentsOpen: true },
    rs: { agents: [done, none, busy] },
  });
  const { root, done: close } = await show(AgentsDrawer, store);
  const lines = [...root.querySelectorAll(".agent-row")].map((r) =>
    text(r.querySelector(".agent-activity")),
  );
  assert.deepEqual(lines, [
    "Docs written. second",
    "",
    "waiting for 2 background agent(s)...",
  ]);
  await close();
});

test("the conversation counts the background agents at work and opens their list", async () => {
  const a = agent({ id: "s1", parent: "m", purpose: "write the docs" });
  const b = agent({ id: "s2", parent: "m", purpose: "", state: "passed" });
  const c = agent({ id: "s3", parent: "m", purpose: "" });
  const main = agent({ id: "m", state: "running" });
  const { store, calls } = drawerStore({ rs: { agents: [main, a, b, c] } });
  const { root, update, done } = await show(SubagentsStrip, store);
  assert.equal(
    text(root.querySelector(".subagents-count")),
    "2 background agents running",
  );
  const chips = [...root.querySelectorAll(".subagents-chip")];
  assert.deepEqual(
    chips.map(text),
    ["write the docs", "s3"],
    "named by the id without a purpose",
  );
  assert.equal(chips[0].title, "write the docs");
  root.querySelector(".subagents-strip").click();
  assert.deepEqual(calls, [["toggleAgents"]]);
  await update({ ...store, rs: { ...store.rs, agents: [main, a] } });
  assert.equal(
    text(root.querySelector(".subagents-count")),
    "1 background agent running",
  );
  await update({ ...store, drawers: { ...store.drawers, agentsOpen: true } });
  assert.equal(root.innerHTML, "", "not drawn while the list is open");
  await update({ ...store, rs: { ...store.rs, agents: [main, b] } });
  assert.equal(root.innerHTML, "", "not drawn when nothing runs");
  await done();
});

test("an unfolded agent shows its summary and the merge command, which copies", async () => {
  const a = agent({
    id: "b1",
    parent: "m",
    state: "passed",
    summary: "Docs written.",
    branch: "agent/docs",
    activity: "hidden while open",
  });
  const { store, calls } = drawerStore({
    drawers: { agentsOpen: true },
    rs: { agents: [a], agentOpenId: "b1" },
  });
  const outside = [];
  document.addEventListener("click", () => outside.push(1));
  const { root, update, done } = await show(AgentsDrawer, store);
  assert.equal(
    root.querySelector(".row-hit").getAttribute("aria-expanded"),
    "true",
  );
  assert.equal(
    root.querySelector(".agent-activity"),
    null,
    "the activity gives way to the detail",
  );
  assert.equal(text(root.querySelector(".agent-summary")), "Docs written.");
  assert.equal(
    text(root.querySelector(".agent-branch code")),
    "git merge --no-edit agent/docs",
  );
  const copy = root.querySelector(".agent-copy");
  assert.equal(text(copy), "copy");
  copy.click();
  assert.deepEqual(calls, [
    ["copyMerge", "b1", "git merge --no-edit agent/docs"],
  ]);
  assert.equal(outside.length, 0, "clicks inside the detail stay there");
  await update({ ...store, drawers: { ...store.drawers, agentCopied: "b1" } });
  assert.equal(text(root.querySelector(".agent-copy")), "copied");
  await done();
});

test("an unfolded agent without summary or branch says so, and offers nothing to copy", async () => {
  const a = agent({ id: "c1", parent: "m", state: "cancelled" });
  const { store } = drawerStore({
    drawers: { agentsOpen: true },
    rs: { agents: [a], agentOpenId: "c1" },
  });
  const { root, done } = await show(AgentsDrawer, store);
  assert.equal(text(root.querySelector(".agent-state")), "⊘");
  assert.equal(text(root.querySelector(".agent-detail")), "no summary");
  assert.equal(root.querySelector(".agent-branch"), null);
  await done();
});

test("drawn where there is no document to copy from, the merge command is shown without a copy button", async () => {
  const a = agent({
    id: "d1",
    parent: "m",
    state: "passed",
    summary: "ok",
    branch: "agent/x",
  });
  const { store } = drawerStore({
    drawers: { agentsOpen: true },
    rs: { agents: [a], agentOpenId: "d1" },
  });
  const root = document.createElement("div");
  document.body.appendChild(root);
  const { render } = await import("preact");
  const doc = globalThis.document;
  delete globalThis.document;
  try {
    // preact draws through the container's own document, so the component runs without a global one.
    render(h(AppCtx.Provider, { value: store }, h(AgentsDrawer, {})), root);
  } finally {
    globalThis.document = doc;
  }
  assert.equal(
    text(root.querySelector(".agent-branch code")),
    "git merge --no-edit agent/x",
  );
  assert.equal(root.querySelector(".agent-copy"), null);
  render(null, root);
  root.remove();
});

// ── ToastCard ────────────────────────────────────────────────────────────────

function toastStore({ detailsOpen = false, updateInfo = null } = {}) {
  const { calls, fn } = recorder();
  const store = {
    status: {
      toastDetailsOpen: detailsOpen,
      toggleToastDetails: fn("toggleToastDetails"),
      dismissToast: fn("dismissToast"),
    },
    updates: {
      updateInfo,
      openUpgrade: fn("openUpgrade"),
      dismissUpgradeToast: fn("dismissUpgradeToast"),
    },
  };
  return { store, calls };
}

test("an error toast is red, says what failed and folds the technical text", async () => {
  const { store, calls } = toastStore();
  const toast = {
    message: "Merge failed",
    type: "error",
    detail: "Nothing changed.",
    technical: "exit 128",
  };
  const { root, update, done } = await show(ToastCard, store, { toast });
  const card = root.querySelector(".toast-card");
  assert.match(card.getAttribute("style"), /rgba\(239, ?68, ?68, ?0.35\)/);
  assert.ok(card.querySelector("svg").classList.contains("text-danger"));
  const [title, detail] = card.querySelectorAll("p");
  assert.equal(text(title), "Merge failed");
  assert.ok(title.classList.contains("text-danger"));
  assert.equal(text(detail), "Nothing changed.");
  const details = [...card.querySelectorAll("button")].find(
    (b) => text(b) === "Details",
  );
  assert.equal(card.querySelector("pre"), null);
  details.click();
  await update(toastStore({ detailsOpen: true }).store, { toast });
  assert.equal(text(card.querySelector("pre")), "exit 128");
  assert.ok(
    [...card.querySelectorAll("button")].some(
      (b) => text(b) === "Hide details",
    ),
  );
  card.querySelector("[aria-label='Dismiss']").click();
  assert.deepEqual(calls, [["toggleToastDetails"]]);
  await done();
});

test("a warning has its triangle and amber, a success its tick, and both just go away", async () => {
  let { store, calls } = toastStore();
  let { root, done } = await show(ToastCard, store, {
    toast: { message: "Retrying", type: "warning" },
  });
  let card = root.querySelector(".toast-card");
  assert.match(card.getAttribute("style"), /rgba\(237, ?181, ?88, ?0.35\)/);
  assert.ok(card.querySelector("svg").classList.contains("text-[#edc06a]"));
  assert.ok(card.querySelector("p").classList.contains("text-[#edc06a]"));
  assert.equal(card.querySelectorAll("p").length, 1, "no detail line");
  card.querySelector("[aria-label='Dismiss']").click();
  assert.deepEqual(calls, [["dismissToast"]]);
  await done();

  ({ store, calls } = toastStore({ updateInfo: { update_available: false } }));
  ({ root, done } = await show(ToastCard, store, {
    toast: { message: "Saved", type: "success", update: true },
  }));
  card = root.querySelector(".toast-card");
  assert.match(card.getAttribute("style"), /rgba\(76, ?194, ?255, ?0.35\)/);
  assert.ok(card.querySelector("svg").classList.contains("text-accent"));
  assert.ok(!card.querySelector("svg").classList.contains("animate-pulse"));
  assert.ok(card.querySelector("p").classList.contains("text-[#e8e8ea]"));
  assert.equal(
    card.querySelectorAll("button").length,
    1,
    "no upgrade without a new version",
  );
  card.querySelector("[aria-label='Dismiss']").click();
  assert.deepEqual(calls, [["dismissToast"]]);
  await done();
});

test("a new-version toast offers the upgrade, and dismissing it remembers", async () => {
  const { store, calls } = toastStore({
    updateInfo: { update_available: true },
  });
  const { root, done } = await show(ToastCard, store, {
    toast: { message: "v2 is out", type: "success", update: true },
  });
  const card = root.querySelector(".toast-card");
  assert.ok(
    card.querySelector("svg").classList.contains("animate-pulse"),
    "the download icon",
  );
  const upgrade = [...card.querySelectorAll("button")].find(
    (b) => text(b) === "Upgrade now",
  );
  upgrade.click();
  card.querySelector("[aria-label='Dismiss']").click();
  assert.deepEqual(calls, [["openUpgrade"], ["dismissUpgradeToast"]]);
  await done();
});

// ── AuthModal ────────────────────────────────────────────────────────────────

function authStore(over = {}) {
  const { calls, fn } = recorder();
  return {
    calls,
    store: {
      auth: {
        authState: "needed",
        authInput: "",
        authBusy: false,
        authError: "",
        setAuthInput: fn("setAuthInput"),
        submitAuth: fn("submitAuth"),
        ...over,
      },
    },
  };
}

test("while the credential is checked the prompt only says so", async () => {
  const { store } = authStore({ authState: "checking" });
  const { root, done } = await show(AuthModal, store);
  assert.equal(text(root.querySelector("h2")), "Authentication required");
  assert.match(text(root), /Checking credentials…/);
  assert.equal(root.querySelector("form"), null);
  await done();
});

test("the token prompt takes the token and submits it, and keeps its clicks", async () => {
  const outside = [];
  const listener = () => outside.push(1);
  document.addEventListener("click", listener);
  let { store, calls } = authStore();
  let { root, done } = await show(AuthModal, store);
  const input = root.querySelector("#auth-token");
  const submit = root.querySelector("button[type=submit]");
  assert.equal(input.type, "password");
  assert.equal(input.placeholder, "Paste the gateway token…");
  assert.equal(submit.disabled, true, "nothing to send yet");
  assert.equal(text(submit), "Connect");
  assert.match(text(root.querySelector("form")), /motita gateway start/);
  input.value = "tok";
  input.dispatchEvent(new Event("input", { bubbles: true }));
  root.querySelector("h2").click();
  assert.equal(outside.length, 0);
  await done();
  assert.deepEqual(calls, [["setAuthInput", "tok"]]);

  ({ store, calls } = authStore({
    authInput: "tok",
    authError: "That token was rejected. Try again.",
  }));
  ({ root, done } = await show(AuthModal, store));
  assert.equal(root.querySelector("button[type=submit]").disabled, false);
  assert.match(
    text(root.querySelector("p.text-danger")),
    /That token was rejected/,
  );
  const form = root.querySelector("form");
  const ev = new Event("submit", { bubbles: true, cancelable: true });
  form.dispatchEvent(ev);
  assert.equal(ev.defaultPrevented, true, "the page does not navigate");
  assert.deepEqual(calls, [["submitAuth"]]);
  await done();
  document.removeEventListener("click", listener);
});

test("with no gateway the modal says so and offers a retry, not the token", async () => {
  let { store, calls } = authStore({ authState: "offline", retrying: false });
  store.auth.retry = () => calls.push(["retry"]);
  let { root, done } = await show(AuthModal, store);
  assert.equal(text(root.querySelector("h2")), "No connection to the server");
  assert.equal(root.querySelector("form"), null, "no token field");
  assert.equal(root.querySelector("#auth-token"), null);
  const button = root.querySelector("button");
  assert.equal(text(button), "Retry");
  assert.equal(button.disabled, false);
  button.click();
  assert.deepEqual(calls, [["retry"]]);
  await done();

  ({ store } = authStore({ authState: "offline", retrying: true }));
  ({ root, done } = await show(AuthModal, store));
  const busy = root.querySelector("button");
  assert.equal(busy.disabled, true);
  assert.equal(text(busy), "Connecting…");
  assert.ok(busy.querySelector(".animate-spin"));
  await done();
});

test("while the token is checked the prompt is busy", async () => {
  const { store } = authStore({ authInput: "tok", authBusy: true });
  const { root, done } = await show(AuthModal, store);
  const submit = root.querySelector("button[type=submit]");
  assert.equal(submit.disabled, true);
  assert.equal(text(submit), "Connecting…");
  assert.ok(submit.querySelector(".animate-spin"));
  assert.equal(root.querySelector("#auth-token").disabled, true);
  await done();
});

// ── ConnectionBanner ─────────────────────────────────────────────────────────

test("the connection banner is silent while connected and says what is wrong otherwise", async () => {
  let { root, done } = await show(ConnectionBanner, {
    status: { connection: "ok" },
  });
  assert.equal(root.querySelector("output"), null);
  await done();
  ({ root, done } = await show(ConnectionBanner, {
    status: { connection: "reconnecting" },
  }));
  assert.equal(
    text(root.querySelector("output")),
    "Connection lost. Reconnecting…",
  );
  await done();
  ({ root, done } = await show(ConnectionBanner, {
    status: { connection: "lost" },
  }));
  assert.equal(
    text(root.querySelector("output")),
    "No connection to the server",
  );
  await done();
});
