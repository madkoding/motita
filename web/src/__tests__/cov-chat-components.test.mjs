// What the reader sees of the chat: the composer (its tags, the slash popup, the send and mic
// buttons, a field that grows with its text) and the parts a turn is drawn with (its menus, the
// trail of steps, the activity line, the changes card and the files a run saved).
// Run with: node --test web/src/__tests__/cov-chat-components.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import {
  act,
  frames,
  h,
  load,
  mount,
  text,
  tick,
} from "./cov-chat-helpers.mjs";

const { Composer } = await load("Composer.tsx");
const { AppCtx } = await load("appStore.ts");
const P = await load("ConversationParts.tsx");
const { TurnCtx } = await load("turnView.ts");

// ── Composer ─────────────────────────────────────────────────────────────────

const observers = [];
class FakeResizeObserver {
  constructor(cb) {
    this.cb = cb;
    this.observed = [];
    this.disconnected = false;
    observers.push(this);
  }
  observe(el) {
    this.observed.push(el);
  }
  disconnect() {
    this.disconnected = true;
  }
}

function store(over = {}) {
  const calls = [];
  const composer = {
    input: "",
    slashPopup: null,
    activeTags: [],
    shellMode: false,
    setSlashPopup: (fn) => calls.push(["setSlashPopup", fn]),
    pickSlash: (c) => calls.push(["pickSlash", c.name]),
    removeTag: (k) => calls.push(["removeTag", k]),
    onInput: (v) => calls.push(["onInput", v]),
    handleKeydown: (e) => calls.push(["keydown", e.key]),
    handleSubmit: (e) => {
      e.preventDefault();
      calls.push(["submit"]);
    },
    ...over.composer,
  };
  return {
    calls,
    value: {
      composer,
      isMerged: false,
      rs: { running: false },
      voice: {
        phase: "idle",
        listening: false,
        showMic: false,
        toggleDictation: async () => calls.push(["dictate"]),
        ...over.voice,
      },
      ...over.top,
    },
  };
}

async function composer(over) {
  const s = store(over);
  const m = await mount(
    h(AppCtx.Provider, { value: s.value }, h(Composer, {})),
  );
  const q = (sel) => m.root.querySelector(sel);
  return { ...s, m, q, root: m.root };
}

test("after a turn the empty field hints at the next request, unless tags are on", async () => {
  const c = await composer({
    composer: { nextSuggestion: "Add tests for the parser" },
  });
  assert.equal(c.q("textarea#task").placeholder, "Add tests for the parser →");
  const tagged = await composer({
    composer: {
      nextSuggestion: "Add tests",
      activeTags: [{ name: "/task", group: "mode", key: 1 }],
    },
  });
  assert.equal(tagged.q("textarea#task").placeholder, "argument…");
  assert.equal(tagged.q("[data-testid=use-suggestion]") !== null, true);
});

test("the arrow button takes the suggestion, and goes away once there is text", async () => {
  const c = await composer({
    composer: {
      nextSuggestion: "Add tests",
      setInput: (v) => c.calls.push(["setInput", v]),
    },
  });
  c.q("[data-testid=use-suggestion]").click();
  assert.deepEqual(c.calls, [["setInput", "Add tests"]]);
  const typed = await composer({
    composer: { nextSuggestion: "Add tests", input: "hi" },
  });
  assert.equal(typed.q("[data-testid=use-suggestion]"), null);
});

test("an empty composer asks for something and sends", async () => {
  const c = await composer();
  const field = c.q("textarea#task");
  assert.equal(field.placeholder, "Ask for something…");
  assert.equal(field.disabled, false);
  assert.equal(text(c.q("label[for=task]")), "Task");
  const send = c.q("button[type=submit]");
  assert.equal(send.getAttribute("aria-label"), "Send");
  assert.equal(send.getAttribute("title"), null);
  assert.equal(send.disabled, false);
  assert.ok(send.className.includes("bg-accent"));
  assert.ok(send.querySelector("polygon"), "the paper plane");
  assert.equal(c.q("[data-testid=composer-mic]"), null);
  assert.equal(c.q("[data-testid=voice-listening]"), null);
  // Typing, keys and the form go to the composer's handlers.
  field.value = "hi";
  field.dispatchEvent(new window.Event("input", { bubbles: true }));
  field.dispatchEvent(
    new window.KeyboardEvent("keydown", { key: "Enter", bubbles: true }),
  );
  c.q("form").dispatchEvent(
    new window.Event("submit", { bubbles: true, cancelable: true }),
  );
  assert.deepEqual(c.calls, [
    ["onInput", "hi"],
    ["keydown", "Enter"],
    ["submit"],
  ]);
  await c.m.unmount();
});

test("a running conversation cannot send; a merged one is read-only", async () => {
  const running = await composer({ top: { rs: { running: true } } });
  assert.equal(running.q("button[type=submit]").disabled, true);
  await running.m.unmount();
  const merged = await composer({
    top: { isMerged: true },
    voice: { showMic: true },
  });
  assert.match(text(merged.root), /Merged — Read Only/);
  const field = merged.q("textarea");
  assert.equal(field.disabled, true);
  assert.equal(
    field.placeholder,
    "This session is read-only after integration",
  );
  assert.equal(merged.q("button[type=submit]").disabled, true);
  assert.equal(merged.q("[data-testid=composer-mic]").disabled, true);
  await merged.m.unmount();
});

test("active commands are pills before the text, each with its own remove button", async () => {
  const tags = [
    { name: "/plan", group: "mode", key: 7 },
    { name: "/run", group: "action", key: 8 },
  ];
  const c = await composer({ composer: { activeTags: tags } });
  assert.equal(c.q("textarea").placeholder, "argument…");
  const pills = [...c.root.querySelectorAll(".composer-field > span")];
  assert.deepEqual(pills.map(text), ["/plan", "/run"]);
  assert.ok(pills[0].className.includes("text-accent"));
  pills[1].querySelector("button").click();
  assert.deepEqual(c.calls, [["removeTag", 8]]);
  await c.m.unmount();
});

test("the slash popup lists the commands, highlights one, and picks on click", async () => {
  const items = [
    {
      name: "/plan",
      group: "mode",
      help: "Plan first",
      aliases: ["/p", "/pl"],
    },
    { name: "/clear", group: "session", help: "Start over" },
    { name: "/odd", group: "other", help: "x", aliases: [] },
  ];
  const c = await composer({ composer: { slashPopup: { items, index: 1 } } });
  const rows = [...c.root.querySelectorAll("form > div.absolute button")];
  assert.deepEqual(rows.map(text), [
    "/plan/p, /plPlan first",
    "/clearStart over",
    "/oddx",
  ]);
  assert.ok(rows[1].className.includes("bg-white/10"), "the highlighted row");
  assert.ok(!rows[0].className.includes("bg-white/10"));
  assert.ok(rows[0].querySelector("span").className.includes("text-accent"));
  assert.ok(
    rows[2].querySelector("span").className.includes("text-[#e8e8ea]"),
    "an unknown group",
  );
  rows[0].click();
  rows[2].dispatchEvent(new window.MouseEvent("mouseover", { bubbles: true }));
  rows[2].dispatchEvent(new window.MouseEvent("mouseenter"));
  const hover = c.calls.find((x) => x[0] === "setSlashPopup")[1];
  assert.deepEqual(hover({ items, index: 0 }), { items, index: 2 });
  assert.equal(hover(null), null);
  assert.deepEqual(c.calls[0], ["pickSlash", "/plan"]);
  await c.m.unmount();
});

test("a shell line turns the send button into run-in-the-shell", async () => {
  const c = await composer({ composer: { shellMode: true } });
  const send = c.q("button[type=submit]");
  assert.equal(send.getAttribute("aria-label"), "Run in the shell");
  assert.equal(send.getAttribute("title"), "Run in the shell");
  assert.ok(send.className.includes("send-shell"));
  assert.ok(send.querySelector("polyline"), "the prompt glyph");
  await c.m.unmount();
});

test("the mic button dictates, says what it does, and the wake-phrase badge shows while listening", async () => {
  for (const [phase, label, pressed, disabled] of [
    ["idle", "Dictate", "false", false],
    ["recording", "Stop and transcribe", "true", false],
    ["transcribing", "Transcribing…", "false", true],
  ]) {
    const c = await composer({
      voice: { showMic: true, phase, listening: phase === "idle" },
    });
    const mic = c.q("[data-testid=composer-mic]");
    assert.equal(mic.getAttribute("aria-label"), label);
    assert.equal(mic.getAttribute("title"), label);
    assert.equal(mic.getAttribute("aria-pressed"), pressed);
    assert.equal(mic.disabled, disabled);
    assert.equal(
      mic.className.includes("animate-pulse"),
      phase === "recording",
    );
    mic.click();
    assert.deepEqual(c.calls, disabled ? [] : [["dictate"]]);
    assert.equal(
      text(c.q("[data-testid=voice-listening]")) || null,
      phase === "idle" ? "Listening for the wake phrase" : null,
    );
    await c.m.unmount();
  }
});

test("the field grows with its text, and again when its width changes", async () => {
  globalThis.ResizeObserver = FakeResizeObserver;
  try {
    let scroll = 40;
    const proto = window.HTMLTextAreaElement.prototype;
    Object.defineProperty(proto, "scrollHeight", {
      get: () => scroll,
      configurable: true,
    });
    let width = 300;
    Object.defineProperty(proto, "clientWidth", {
      get: () => width,
      configurable: true,
    });
    const s = store({ composer: { input: "one" } });
    const m = await mount(
      h(AppCtx.Provider, { value: s.value }, h(Composer, {})),
    );
    const field = m.root.querySelector("textarea");
    assert.equal(field.style.height, "40px");
    scroll = 80;
    const s2 = store({ composer: { input: "one\ntwo" } });
    await m.update(h(AppCtx.Provider, { value: s2.value }, h(Composer, {})));
    assert.equal(field.style.height, "80px", "refitted when the text changed");
    const ro = observers.at(-1);
    assert.deepEqual(ro.observed, [field]);
    scroll = 120;
    ro.cb([]);
    assert.equal(
      field.style.height,
      "80px",
      "same width: the fit's own resize is ignored",
    );
    width = 200;
    ro.cb([]);
    assert.equal(field.style.height, "120px", "rewrapped at the new width");
    await m.unmount();
    assert.equal(ro.disconnected, true);
    // No ResizeObserver: the field still fits its text.
    globalThis.ResizeObserver = undefined;
    const n = observers.length;
    const m2 = await mount(
      h(AppCtx.Provider, { value: store().value }, h(Composer, {})),
    );
    assert.equal(observers.length, n);
    assert.equal(m2.root.querySelector("textarea").style.height, "120px");
    await m2.unmount();
    delete proto.scrollHeight;
    delete proto.clientWidth;
  } finally {
    delete globalThis.ResizeObserver;
  }
});

// ── The parts of a turn ──────────────────────────────────────────────────────

function turnView(over = {}) {
  const calls = [];
  const view = {
    running: false,
    activityKind: "",
    cpInfo: {},
    cpMenu: null,
    cpArmed: false,
    speakingId: null,
    speechSupported: false,
    lastMsgId: null,
    lastUserId: -1,
    setCpMenu: (m) => calls.push(["setCpMenu", m]),
    setCpArmed: (a) => calls.push(["setCpArmed", a]),
    toggleCpMenu: (t) => calls.push(["toggleCpMenu", t]),
    restoreCheckpoint: (t, f) => calls.push(["restore", t, f]),
    stopRun: () => calls.push(["stopRun"]),
    submit: (x) => calls.push(["submit", x]),
    answerSuggestions: () => {},
    speak: () => {},
    showRunArtifact: (a) => calls.push(["artifact", a.name]),
    ...over,
  };
  return { view, calls };
}

async function inTurn(vnode, over) {
  const { view, calls } = turnView(over);
  const m = await mount(h(TurnCtx.Provider, { value: view }, vnode));
  return {
    m,
    root: m.root,
    calls,
    q: (s) => m.root.querySelector(s),
    qa: (s) => [...m.root.querySelectorAll(s)],
  };
}

test("a command's status: running, ok, failed with its code, or cut off", async () => {
  const cases = [
    [{ exit: null }, "running", "term-exit running"],
    [{ exit: undefined, settled: true }, "no result", "term-exit"],
    [{ exit: 0 }, "ok", "term-exit"],
    [{ exit: 2 }, "failed · exit 2", "term-exit bad"],
  ];
  for (const [props, label, cls] of cases) {
    const m = await mount(h(P.ShellStatus, props));
    assert.equal(text(m.root), label);
    assert.equal(m.root.firstChild.className, cls);
    await m.unmount();
  }
});

test("the changes card shows the previews, zooms one, and folds the files under a count", async () => {
  const changes = {
    previews: [
      { name: "home.png", url: "data:image/png;base64,AA" },
      { name: "menu.png", url: "data:image/png;base64,BB" },
    ],
    files: [
      { path: "a.go", status: "modified", added: 3, deleted: 1 },
      { path: "b.bin", status: "renamed", added: 0, deleted: 0 },
      { path: "c.txt", status: "weird", added: 0, deleted: 2 },
    ],
  };
  const m = await mount(h(P.ChangesCard, { changes }));
  const shots = [...m.root.querySelectorAll("figure")];
  assert.deepEqual(
    shots.map((f) => text(f.querySelector("figcaption"))),
    ["home.png", "menu.png"],
  );
  const summary = m.root.querySelector("summary");
  assert.equal(text(summary), "3 files changed +3 −3");
  const rows = [...m.root.querySelectorAll("li")];
  assert.deepEqual(rows.map(text), [
    "editeda.go+3 −1",
    "movedb.bin",
    "weirdc.txt+0 −2",
  ]);
  // A preview opens full size over the page; Escape closes it.
  await act(() => shots[1].querySelector("button").click());
  let zoom = document.querySelector("dialog.changes-zoom");
  assert.equal(
    zoom.querySelector("img").getAttribute("src"),
    "data:image/png;base64,BB",
  );
  assert.equal(zoom.getAttribute("aria-label"), "preview");
  await act(() =>
    document.dispatchEvent(
      new window.KeyboardEvent("keydown", { key: "Escape" }),
    ),
  );
  assert.equal(document.querySelector("dialog.changes-zoom"), null);
  // A click on it closes it too.
  await act(() => shots[0].querySelector("button").click());
  zoom = document.querySelector("dialog.changes-zoom");
  await act(() => zoom.click());
  assert.equal(document.querySelector("dialog.changes-zoom"), null);
  await m.unmount();
  // One file, no pictures; and a card with neither.
  const one = await mount(
    h(P.ChangesCard, {
      changes: {
        files: [{ path: "x", status: "added", added: 1, deleted: 0 }],
      },
    }),
  );
  assert.equal(text(one.root.querySelector("summary")), "1 file changed +1 −0");
  assert.equal(one.root.querySelector(".changes-previews"), null);
  await one.unmount();
  const none = await mount(h(P.ChangesCard, { changes: {} }));
  assert.equal(none.root.querySelector(".changes-card").children.length, 0);
  await none.unmount();
});

test("a user's message is its text, or a label with the prompt folded under it", async () => {
  const plain = await mount(
    h(P.UserText, { m: { id: 1, role: "user", text: "hello" } }),
  );
  assert.equal(plain.root.innerHTML, "<span>hello</span>");
  await plain.unmount();
  const shown = await mount(
    h(P.UserText, {
      m: { id: 2, role: "user", text: "- do x", shown: "Applying suggestions" },
    }),
  );
  assert.equal(
    text(shown.root.querySelector("summary")),
    "Applying suggestions",
  );
  assert.equal(text(shown.root.querySelector("pre")), "- do x");
  await shown.unmount();
});

test("the input being worked on has one menu that stops the agent, under a bar that says who wrote it", async () => {
  const head = { id: 1, role: "user", text: "go" };
  const closed = await inTurn(
    h(P.TurnHead, { head, turn: 0, liveHere: true }),
    { running: true },
  );
  assert.equal(text(closed.q(".turn-who")), "You");
  assert.equal(text(closed.q(".turn-text")), "go");
  assert.equal(closed.q(".mode-badge"), null, "no mode, no badge");
  const btn = closed.q(".turn-bar button.cp-btn");
  assert.equal(btn.getAttribute("aria-label"), "Message menu");
  assert.equal(btn.getAttribute("aria-expanded"), "false");
  assert.equal(closed.q("[role=menu]"), null);
  btn.click();
  assert.deepEqual(closed.calls, [["toggleCpMenu", -1]]);
  await closed.m.unmount();
  const open = await inTurn(
    h(P.TurnHead, { head, liveHere: true, mode: "task" }),
    {
      cpMenu: -1,
      running: true,
    },
  );
  assert.equal(open.q("button.cp-btn").getAttribute("aria-expanded"), "true");
  const items = open.qa("[role=menuitem]");
  assert.equal(items.length, 1, "while it runs, stopping is all there is");
  assert.match(text(items[0]), /^Stop the agent/);
  items[0].click();
  assert.deepEqual(open.calls, [["setCpMenu", null], ["stopRun"]]);
  await open.m.unmount();
});

test("an input with a checkpoint can go back to it, and asks twice before putting the files back", async () => {
  const head = { id: 1, role: "user", text: "change it" };
  // No checkpoint, or a run working: no menu.
  for (const over of [{}, { cpInfo: { 3: { files: true } }, running: true }]) {
    const t = await inTurn(
      h(P.TurnHead, { head, turn: 3, liveHere: false }),
      over,
    );
    assert.equal(t.q("button.cp-btn"), null);
    await t.m.unmount();
  }
  const noTurn = await inTurn(h(P.TurnHead, { head, liveHere: false }), {
    cpInfo: { 3: { files: true } },
  });
  assert.equal(noTurn.q("button.cp-btn"), null);
  await noTurn.m.unmount();
  // Closed menu.
  const closed = await inTurn(
    h(P.TurnHead, { head, turn: 3, liveHere: false }),
    {
      cpInfo: { 3: { files: true } },
    },
  );
  const btn = closed.q("button.cp-btn");
  assert.equal(btn.getAttribute("aria-label"), "Message menu");
  assert.equal(btn.getAttribute("aria-expanded"), "false");
  btn.click();
  assert.deepEqual(closed.calls, [["toggleCpMenu", 3]]);
  await closed.m.unmount();
  // Open, not armed: the first press on the files item arms it, the conversation item restores.
  const open = await inTurn(h(P.TurnHead, { head, turn: 3, liveHere: false }), {
    cpInfo: { 3: { files: true } },
    cpMenu: 3,
  });
  const [convo, files] = open.qa("[role=menuitem]");
  assert.match(text(convo), /^Go back to this checkpoint/);
  assert.match(text(files), /^Go back and restore the changes/);
  assert.equal(files.disabled, false);
  assert.equal(files.getAttribute("title"), "");
  assert.equal(files.className, "");
  let outside = 0;
  document.addEventListener("click", () => (outside += 1));
  files.click();
  assert.equal(
    outside,
    0,
    "arming does not reach the page (which would close the menu)",
  );
  convo.click();
  assert.deepEqual(open.calls, [
    ["setCpArmed", true],
    ["restore", 3, false],
  ]);
  await open.m.unmount();
  // Armed: the second press puts the files back.
  const armed = await inTurn(
    h(P.TurnHead, { head, turn: 3, liveHere: false }),
    {
      cpInfo: { 3: { files: true } },
      cpMenu: 3,
      cpArmed: true,
    },
  );
  const second = armed.qa("[role=menuitem]")[1];
  assert.match(text(second), /^Click again: discard changes since/);
  assert.equal(second.className, "is-armed");
  second.click();
  assert.deepEqual(armed.calls, [["restore", 3, true]]);
  await armed.m.unmount();
  // A checkpoint that kept no files cannot put them back.
  const bare = await inTurn(h(P.TurnHead, { head, turn: 3, liveHere: false }), {
    cpInfo: { 3: { files: false } },
    cpMenu: 3,
  });
  const nope = bare.qa("[role=menuitem]")[1];
  assert.equal(nope.disabled, true);
  assert.equal(
    nope.getAttribute("title"),
    "This checkpoint did not keep the files",
  );
  await bare.m.unmount();
});

test("the mode badge says what auto mode picked, and the menu sends the message again in the other mode", async () => {
  const head = { id: 1, role: "user", text: "/plan sketch it" };
  // No checkpoint: the menu is the switch alone, keyed by the input itself.
  const plan = await inTurn(
    h(P.TurnHead, { head, liveHere: false, mode: "plan" }),
  );
  const badge = plan.q(".turn-bar .mode-badge");
  assert.equal(text(badge), "Plan");
  assert.equal(badge.dataset.mode, "plan");
  plan.q("button.cp-btn").click();
  assert.deepEqual(plan.calls, [["toggleCpMenu", -3]]);
  await plan.m.unmount();
  const open = await inTurn(
    h(P.TurnHead, { head, liveHere: false, mode: "plan" }),
    { cpMenu: -3 },
  );
  const items = open.qa("[role=menuitem]");
  assert.equal(items.length, 1);
  assert.match(text(items[0]), /^Run as task/);
  items[0].click();
  assert.deepEqual(open.calls, [
    ["setCpMenu", null],
    ["submit", "/task sketch it"],
  ]);
  await open.m.unmount();
  // With a checkpoint the switch comes first, then going back.
  const both = await inTurn(
    h(P.TurnHead, {
      head: { ...head, text: "build it" },
      turn: 2,
      liveHere: false,
      mode: "task",
    }),
    {
      cpInfo: { 2: { files: true } },
      cpMenu: 2,
    },
  );
  assert.equal(text(both.q(".mode-badge")), "Task");
  const [sw, back] = both.qa("[role=menuitem]");
  assert.match(text(sw), /^Run as plan/);
  assert.equal(sw.className, "cp-mode");
  assert.match(text(back), /^Go back to this checkpoint/);
  await both.m.unmount();
  // While a run works the badge stays and the switch is gone.
  const busy = await inTurn(
    h(P.TurnHead, { head, turn: 2, liveHere: false, mode: "task" }),
    {
      cpInfo: { 2: { files: true } },
      running: true,
    },
  );
  assert.ok(busy.q(".mode-badge"));
  assert.equal(busy.q("button.cp-btn"), null);
  await busy.m.unmount();
});

test("the top of a turn counts its actions in the input's bar and opens them under the card", async () => {
  const head = { id: 1, role: "user", text: "go" };
  const steps = [
    { key: 1, kind: "command", text: "running: ls", exit: 0 },
    { key: 2, kind: "progress", text: "thinking: about it" },
  ];
  const top = await inTurn(
    h(P.TurnTop, { head, turn: 0, liveHere: false, mode: null, steps }),
  );
  const count = top.q(".turn-bar button.action-trail-toggle");
  assert.equal(text(count), "▸2 actions");
  assert.equal(top.q(".turn-steps"), null, "folded at first");
  await act(() => count.click());
  assert.equal(count.getAttribute("aria-expanded"), "true");
  assert.equal(top.qa(".turn-steps .trail-step").length, 2);
  assert.equal(top.q(".turn-steps").getAttribute("aria-label"), "steps");
  await act(() => count.click());
  assert.equal(top.q(".turn-steps"), null);
  await top.m.unmount();
  // No steps: no counter.
  const bare = await inTurn(
    h(P.TurnTop, { head, turn: 0, liveHere: false, mode: null, steps: [] }),
  );
  assert.equal(bare.q(".action-trail-toggle"), null);
  assert.equal(text(bare.q(".turn-text")), "go");
  await bare.m.unmount();
  // No input: the counter stands on its own line, and with no steps there is nothing.
  const lone = await inTurn(
    h(P.TurnTop, { head: null, liveHere: true, mode: null, steps }),
  );
  assert.equal(lone.q(".turn-card"), null);
  assert.ok(lone.q(".action-trail > button.action-trail-toggle"));
  await lone.m.unmount();
  const none = await inTurn(
    h(P.TurnTop, { head: null, liveHere: false, mode: null, steps: [] }),
  );
  assert.equal(none.q(".action-trail"), null);
  await none.m.unmount();
});

test("the trail folds the steps behind a count and draws commands and progress", async () => {
  const steps = [
    { key: 1, kind: "command", text: "running: ls -la", exit: 0 },
    { key: 2, kind: "command", text: "make", exit: null },
    { key: 3, kind: "progress", text: "thinking: about it" },
    { key: 4, kind: "progress", text: "plain words" },
  ];
  const t = await inTurn(h(P.Trail, { steps, liveHere: false }));
  const toggle = t.q(".action-trail-toggle");
  assert.equal(text(toggle), "▸4 actions");
  await act(() => toggle.click());
  const rows = t.qa(".trail-step");
  assert.equal(rows.length, 4);
  assert.equal(rows[0].getAttribute("title"), "ls -la");
  assert.equal(text(rows[0]), "ls -laok");
  assert.equal(
    text(rows[1]),
    "makeno result",
    "a finished turn's command with no result was cut off",
  );
  assert.equal(rows[2].className, "trail-step thought");
  assert.equal(rows[3].className, "trail-step ");
  assert.equal(text(rows[3]), "plain words");
  await t.m.unmount();
  const live = await inTurn(
    h(P.Trail, { steps: steps.slice(1, 2), liveHere: true }),
  );
  await act(() => live.q(".action-trail-toggle").click());
  assert.equal(text(live.q(".trail-step")), "makerunning");
  await live.m.unmount();
});

test("the activity line types what the agent is doing, with a command's status", async () => {
  frames.clear();
  const cmd = await inTurn(
    h(P.ActivityLine, {
      activity: "running: npm test",
      shown: [{ key: 1, kind: "command", exit: 1 }],
    }),
    { activityKind: "command" },
  );
  const line = cmd.q(".msg.activity");
  assert.equal(line.className, "msg activity command");
  assert.equal(line.getAttribute("title"), "running: npm test");
  assert.ok(cmd.q(".spin-command"));
  await act(() => frames.flush(performance.now() + 60_000));
  assert.equal(text(cmd.q(".activity-typed.term-cmd-text")), "npm test");
  assert.equal(text(cmd.q(".term-exit")), "failed · exit 1");
  await cmd.m.unmount();
  const idle = await inTurn(
    h(P.ActivityLine, { activity: "reading   the\nfiles", shown: [] }),
  );
  assert.ok(idle.q(".spin-default"));
  await act(() => frames.flush(performance.now() + 60_000));
  const typed = idle.q(".activity-typed");
  assert.equal(typed.className.trim(), "activity-typed");
  assert.equal(text(typed), "reading the files");
  await idle.m.unmount();
  // A command with no steps yet is still running.
  const first = await inTurn(
    h(P.ActivityLine, { activity: "running: ls", shown: [] }),
    {
      activityKind: "command",
    },
  );
  assert.equal(text(first.q(".term-exit")), "running");
  await first.m.unmount();
  frames.clear();
});

test("the files a run saved open over the artifact list", async () => {
  const t = await inTurn(
    h(P.RunArtifacts, {
      artifacts: [
        { name: "report.pdf", type: "application/pdf", size: 1 },
        { name: "chart.png", type: "image/png", size: 2 },
      ],
    }),
  );
  const btns = t.qa("[data-testid=run-artifacts] button");
  assert.deepEqual(btns.map(text), ["report.pdf", "chart.png"]);
  btns[1].click();
  assert.deepEqual(t.calls, [["artifact", "chart.png"]]);
  await t.m.unmount();
  await tick();
});
