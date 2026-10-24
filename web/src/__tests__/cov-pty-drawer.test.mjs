// The interactive terminal's drawer (PtyDrawer.tsx): its own PTY tab beside the TTY, a shell that
// starts when the drawer opens, waits for the agent, outlives a closed drawer and ends with its
// session.
// Run with: node --test web/src/__tests__/cov-pty-drawer.test.mjs
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
// The terminal itself stands in as a line that hands its props out (it has its own tests).
stub(
  "./TermPty",
  `import { h } from "preact";
export function TermPty(props) { globalThis.__ptyProps = props; return h("div", { class: "pty-stand-in" }); }`,
);

const { AppCtx } = await stubModule("./appStore");
const { PtyDrawer } = await load("PtyDrawer.tsx");
const { h } = await import("preact");
const { act } = await import("preact/test-utils");
const mount = await mounter();

function ptyStore({ rs = {}, ptyOpen = false, selectedSession } = {}) {
  const calls = [];
  const fn =
    (name) =>
    (...args) =>
      calls.push([name, ...args.filter((a) => !(a instanceof window.Event))]);
  const store = {
    machine: "",
    selectedSession,
    status: { setToast: fn("setToast") },
    rs: { sessionId: "s1", noSession: false, running: false, ...rs },
    drawers: {
      ptyOpen,
      togglePty: fn("togglePty"),
      closePty: fn("closePty"),
    },
  };
  return { store, calls };
}

async function show(store) {
  const m = await mount(h(AppCtx.Provider, { value: store }, h(PtyDrawer)));
  return {
    ...m,
    update: (s) =>
      act(() => m.rerender(h(AppCtx.Provider, { value: s }, h(PtyDrawer)))),
  };
}

const isPty = (root) => !!root.querySelector(".pty-stand-in");

test("a closed drawer is only its PTY tab, and starts nothing", async () => {
  const { store, calls } = ptyStore();
  const { root, done } = await show(store);
  const tab = root.querySelector(".term-tab");
  assert.equal(text(tab), "PTY");
  assert.equal(
    tab.title,
    "Open an interactive terminal, for commands that ask for input",
  );
  assert.equal(tab.getAttribute("aria-expanded"), "false");
  assert.ok(!tab.classList.contains("is-live"));
  assert.ok(root.querySelector(".term-drawer").classList.contains("is-pty"));
  assert.equal(
    root.querySelector(".term-panel").getAttribute("aria-hidden"),
    "true",
  );
  assert.equal(
    text(root.querySelector(".term-titlebar span")),
    "motita@pty — interactive",
  );
  assert.equal(root.querySelector(".term-close").tabIndex, -1);
  assert.equal(root.querySelector(".term-pty-open").tabIndex, -1);
  assert.ok(!isPty(root));
  tab.click();
  root.querySelector(".term-close").click();
  assert.deepEqual(calls, [["togglePty"], ["closePty"]]);
  await done();
});

test("opening the drawer starts the shell, which outlives a closed drawer", async () => {
  const { store, calls } = ptyStore();
  const { root, done, update } = await show(store);
  const open = { ...store, drawers: { ...store.drawers, ptyOpen: true } };
  await update(open);
  await act(() => flush(2));
  assert.ok(isPty(root));
  const tab = root.querySelector(".term-tab");
  assert.ok(tab.classList.contains("is-live"));
  assert.equal(tab.title, "Close the interactive terminal");
  assert.equal(globalThis.__ptyProps.sid, "s1");
  assert.equal(globalThis.__ptyProps.machine, "");
  assert.equal(globalThis.__ptyProps.open, true);
  assert.equal(root.querySelector(".term-pty-end").tabIndex, 0);

  await update(store);
  assert.ok(isPty(root), "a closed drawer keeps its shell");
  assert.equal(globalThis.__ptyProps.open, false);
  assert.equal(root.querySelector(".term-pty-end").tabIndex, -1);

  // Its shell ended: back to the start button, which starts another.
  await update(open);
  await act(() => globalThis.__ptyProps.onEnd());
  assert.ok(!isPty(root));
  assert.equal(
    text(root.querySelector(".term-pty-open")),
    "▸ start the interactive terminal",
  );
  await act(() => root.querySelector(".term-pty-open").click());
  assert.ok(isPty(root));

  // A failure is said.
  await act(() => globalThis.__ptyProps.onEnd("no pty here"));
  assert.ok(!isPty(root));
  const toasts = calls.filter((c) => c[0] === "setToast");
  assert.equal(toasts.length, 1);
  assert.equal(toasts[0][1].message, "The terminal could not be opened");
  assert.equal(toasts[0][1].detail, "no pty here");

  // Ended by hand.
  await act(() => root.querySelector(".term-pty-open").click());
  await act(() => root.querySelector(".term-pty-end").click());
  assert.ok(!isPty(root));

  // Another session is another shell.
  await act(() => root.querySelector(".term-pty-open").click());
  await update({ ...open, rs: { ...open.rs, sessionId: "s2" } });
  await act(() => flush(2));
  assert.ok(!isPty(root));
  await done();
});

test("while the agent works the terminal waits, and nothing starts", async () => {
  const { store } = ptyStore({ rs: { running: true }, ptyOpen: true });
  const { root, done } = await show(store);
  await act(() => flush(2));
  assert.ok(!isPty(root));
  assert.equal(
    text(root.querySelector(".term-locked")),
    "the agent is working: the terminal is yours again when it finishes",
  );
  const start = root.querySelector(".term-pty-open");
  assert.equal(start.disabled, true);
  assert.equal(start.tabIndex, 0);
  await done();
});

test("with no session, or one already integrated, it says what it needs", async () => {
  for (const opts of [
    { rs: { noSession: true } },
    { selectedSession: { id: "s1", merged: true } },
  ]) {
    const { store } = ptyStore({ ...opts, ptyOpen: true });
    const { root, done } = await show(store);
    await act(() => flush(2));
    assert.ok(!isPty(root));
    assert.equal(root.querySelector(".term-pty-open"), null);
    assert.equal(
      text(root.querySelector(".term-locked")),
      "open a session to use the interactive terminal",
    );
    await done();
  }
});
