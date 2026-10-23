// The terminal drawer's prompt (TermPrompt.tsx): a line typed there runs as a typed command, the
// arrows walk what was typed, and while the agent works the prompt gives way to a note.
// Run with: node --test web/src/__tests__/cov-shell-termprompt.test.mjs
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
const { TermPrompt } = await load("TermPrompt.tsx");
const { h } = await import("preact");
const { act } = await import("preact/test-utils");
const mount = await mounter();

// promptStore is a store whose runShellLine records the line and waits until `release` is called.
function promptStore({ rs = {}, drawers = {}, selectedSession } = {}) {
  const calls = [];
  let release = () => {};
  const store = {
    rs: { noSession: false, running: false, ...rs },
    drawers: {
      termOpen: true,
      stickTerm: () => calls.push(["stickTerm"]),
      ...drawers,
    },
    shell: {
      runShellLine: (command) => {
        calls.push(["run", command]);
        return new Promise((r) => {
          release = r;
        });
      },
    },
    selectedSession,
  };
  return { store, calls, release: () => release() };
}

let interactive = 0;
async function show(store) {
  return mount(
    h(
      AppCtx.Provider,
      { value: store },
      h(TermPrompt, { onInteractive: () => interactive++ }),
    ),
  );
}

const type = (input, value) =>
  act(() => {
    input.value = value;
    input.dispatchEvent(new window.Event("input", { bubbles: true }));
  });
const submit = (form) =>
  act(() => {
    form.dispatchEvent(
      new window.Event("submit", { bubbles: true, cancelable: true }),
    );
  });
const key = (input, k) => {
  const e = new window.KeyboardEvent("keydown", {
    key: k,
    bubbles: true,
    cancelable: true,
  });
  return act(() => input.dispatchEvent(e)).then(() => e);
};

test("nothing is offered without a session, or in one already integrated", async () => {
  const none = await show(promptStore({ rs: { noSession: true } }).store);
  assert.equal(none.root.innerHTML, "");
  await none.done();
  const merged = await show(
    promptStore({ selectedSession: { id: "s", merged: true } }).store,
  );
  assert.equal(merged.root.innerHTML, "");
  await merged.done();
});

test("while the agent works the prompt gives way to a note", async () => {
  const { root, done } = await show(
    promptStore({ rs: { running: true } }).store,
  );
  assert.equal(root.querySelector("input"), null);
  assert.equal(
    text(root.querySelector(".term-locked")),
    "the agent is working: the terminal is yours again when it finishes",
  );
  await done();
});

test("a typed line runs, holds the prompt until it ends, and gives the focus back", async () => {
  const { store, calls, release } = promptStore({
    selectedSession: { id: "s" },
  });
  const { root, done } = await show(store);
  const form = root.querySelector("form.term-input-line");
  const input = root.querySelector("input.term-input");
  assert.equal(input.tabIndex, 0);
  assert.equal(input.placeholder, "type a command");
  assert.equal(
    input.getAttribute("aria-label"),
    "Command to run in the session's folder",
  );

  // A blank line runs nothing.
  await type(input, "   ");
  await submit(form);
  assert.deepEqual(calls, []);

  await type(input, "  ls -la ");
  await submit(form);
  assert.deepEqual(calls, [["stickTerm"], ["run", "ls -la"]]);
  assert.equal(input.value, "");
  assert.equal(input.disabled, true);
  assert.equal(input.placeholder, "running…");

  const pty = root.querySelector("button.term-pty-open");
  assert.equal(pty.disabled, true, "no terminal while a line runs");

  release();
  await act(() => flush(3));
  assert.equal(input.disabled, false);
  assert.equal(document.activeElement, input);
  assert.equal(pty.tabIndex, 0);
  assert.equal(text(pty), "interactive");
  await act(() => pty.click());
  assert.equal(interactive, 1, "the button opens the interactive terminal");
  await done();
});

test("the arrows walk the lines typed, a repeat kept once, and other keys are the input's", async () => {
  const { store, release } = promptStore({ drawers: { termOpen: false } });
  const { root, done } = await show(store);
  const form = root.querySelector("form");
  const input = root.querySelector("input");
  assert.equal(input.tabIndex, -1, "a closed drawer keeps it out of Tab");

  for (const line of ["pwd", "git status", "git status"]) {
    await type(input, line);
    await submit(form);
    release();
    await act(() => flush(3));
  }
  const other = await key(input, "a");
  assert.equal(other.defaultPrevented, false);

  assert.equal((await key(input, "ArrowUp")).defaultPrevented, true);
  assert.equal(input.value, "git status");
  await key(input, "ArrowUp");
  assert.equal(input.value, "pwd");
  // The oldest line is as far up as it goes.
  await key(input, "ArrowUp");
  assert.equal(input.value, "pwd");
  await key(input, "ArrowDown");
  assert.equal(input.value, "git status");
  // Past the newest is the empty line being written, and no further.
  await key(input, "ArrowDown");
  assert.equal(input.value, "");
  await key(input, "ArrowDown");
  assert.equal(input.value, "");
  await done();
});
