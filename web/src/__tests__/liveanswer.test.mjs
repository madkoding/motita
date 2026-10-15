// The answer is shown growing where it will be while it is written, and never filed as steps.
//
// Reported from real use: "instead of showing the text appearing in the answer, it piled up as
// actions". The plan's streamed answer reached the page as progress lines, one per fragment, and
// each became a step of the turn. It now arrives as `answer` snapshots that draw one draft message,
// which the finished answer replaces as the same message. Run with: node --test web/src/__tests__/
import test from "node:test";
import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { JSDOM } from "jsdom";
import { build } from "esbuild";

const here = dirname(fileURLToPath(import.meta.url));
const dom = new JSDOM("<!doctype html><html><body></body></html>", {
  url: "http://localhost/",
});
globalThis.window = dom.window;
globalThis.document = dom.window.document;
globalThis.localStorage = dom.window.localStorage;

const dir = mkdtempSync(join(tmpdir(), "motita-liveanswer-test-"));
const out = join(dir, "events.mjs");
await build({
  sourcemap: "inline",
  entryPoints: [join(here, "..", "streamEvents.ts")],
  bundle: true,
  format: "esm",
  platform: "browser",
  outfile: out,
  logLevel: "silent",
  jsx: "automatic",
  jsxImportSource: "preact",
});
const { handleStreamEvent, withoutDraft } = await import(out);
rmSync(dir, { recursive: true, force: true });

// fakeRun is the part of the run state the events write, with plain values in place of hooks.
function fakeRun() {
  let id = 10;
  const st = {
    messages: [{ id: 1, role: "user", text: "explain it" }],
    steps: {},
    finished: 0,
  };
  const apply = (key) => (v) => {
    if (typeof v === "function") {
      st[key] = v(st[key]);
    } else {
      st[key] = v;
    }
  };
  const rs = {
    nextId: () => {
      id += 1;
      return id;
    },
    liveTurnRef: { current: 3 },
    setMessages: apply("messages"),
    setStepsByTurn: apply("steps"),
    setActivity: () => {},
    setActivityKind: () => {},
    setLiveThought: () => {},
    setShellLog: () => {},
  };
  const ctx = {
    rs,
    finish: () => {
      st.finished += 1;
    },
  };
  return { st, send: (name, payload) => handleStreamEvent(ctx, name, payload) };
}

test("the answer grows as one draft and becomes the answer, the same message", () => {
  const { st, send } = fakeRun();
  send("answer", { text: "The answer " });
  send("answer", { text: "The answer is here." });
  assert.equal(st.messages.length, 2);
  const draft = st.messages[1];
  assert.equal(draft.draft, true);
  assert.equal(draft.text, "The answer is here.");
  send("done", { result: "The answer is here, whole." });
  assert.equal(st.messages.length, 2);
  assert.equal(
    st.messages[1].id,
    draft.id,
    "the same message, so it does not blink",
  );
  assert.equal(st.messages[1].draft, undefined);
  assert.equal(st.messages[1].text, "The answer is here, whole.");
  assert.deepEqual(st.steps, {}, "no fragment is a step");
});

test("an empty snapshot clears the draft", () => {
  const { st, send } = fakeRun();
  send("answer", { text: "talking while it works" });
  send("answer", { text: "" });
  assert.equal(st.messages.length, 1);
  send("answer", { text: "" });
  assert.equal(st.messages.length, 1);
});

test("an error takes the draft's place, and a run ending without an answer drops it", () => {
  const { st, send } = fakeRun();
  send("answer", { text: "half" });
  send("error", { error: "boom" });
  assert.equal(st.messages.length, 2);
  assert.equal(st.messages[1].kind, "error");

  const other = fakeRun();
  other.send("answer", { text: "half" });
  other.send("done", {});
  assert.equal(other.st.messages.length, 1);
  other.send("error", {});
  assert.equal(other.st.messages.length, 1);
});

test("withoutDraft leaves a conversation without a draft as it is", () => {
  const prev = [{ id: 1, role: "user", text: "x" }];
  assert.equal(withoutDraft(prev), prev);
  assert.equal(withoutDraft([]).length, 0);
});
