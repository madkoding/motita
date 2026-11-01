// The tab's word on sessions it is not showing, and on a model switch in the one it is.
// sessionWatch.ts imports i18n without an extension, which tsx-hooks.mjs resolves.
import test from "node:test";
import assert from "node:assert/strict";
import { load } from "./tsx-hooks.mjs";

const W = await load("sessionWatch.ts");

const row = (id, running, extra = {}) => ({
  id,
  title: "title " + id,
  running,
  created: "",
  last_used: "",
  ...extra,
});

test("a session that finishes while another is on screen is announced", () => {
  const before = new Map([
    ["A", true],
    ["B", true],
    ["C", true],
    ["D", false],
  ]);
  const now = [
    row("A", false),
    row("B", false),
    row("C", true),
    row("D", false),
  ];
  const done = W.finishedElsewhere(before, now, "B");
  assert.deepEqual(
    done.map((s) => s.id),
    ["A"],
    "only A: B is on screen, C still works, D was idle",
  );
  const toast = W.finishedToast(done);
  assert.equal(toast.type, "success");
  assert.match(toast.message, /title A/);
});

test("nothing is announced on the first read, for a deleted session, or when nothing finished", () => {
  assert.deepEqual(W.finishedElsewhere(new Map(), [row("A", false)], ""), []);
  assert.deepEqual(W.finishedElsewhere(new Map([["A", true]]), [], ""), []);
  assert.equal(W.finishedToast([]), null);
  const many = W.finishedToast([row("A", false), row("B", false)]);
  assert.match(many.message, /2/);
  assert.match(many.detail, /title A\ntitle B/);
});

test("a model switch warns only when the session already talked or is running", () => {
  assert.equal(W.modelChangeToast({ provider: "p", model: "m" }), null);
  assert.equal(
    W.modelChangeToast({ provider: "p", model: "m", warnings: [] }),
    null,
  );
  const both = W.modelChangeToast({
    provider: "p",
    model: "new",
    previous_model: "old",
    warnings: ["history", "running"],
  });
  assert.equal(both.type, "warning");
  assert.match(both.message, /new/);
  assert.match(both.detail, /old/);
  assert.equal(both.detail.split("\n").length, 2);

  assert.equal(
    W.switchWarning(false, true, true),
    "",
    "an unchanged model needs no word",
  );
  assert.equal(
    W.switchWarning(true, false, false),
    "",
    "a new session can take any model",
  );
  assert.match(W.switchWarning(true, true, false), /messages/);
  assert.match(W.switchWarning(true, true, true), /running/);
});

test("the session on screen is read again when another page moved it, not when this tab did", () => {
  const s = (rev, running = false) => row("A", running, { rev });
  let seen = { id: "", mine: false };
  const step = (row, following = false) => {
    const next = W.onScreenMoved(seen, row, following);
    seen = next.seen;
    return next.reread;
  };
  assert.equal(step(undefined), false, "no session on screen");
  assert.equal(step(s(4)), false, "the first look only records");
  assert.equal(step(s(4)), false, "nothing moved");
  assert.equal(step(s(5, true)), true, "a turn started elsewhere");
  // This tab now follows it, and the end of what it followed is already on screen.
  assert.equal(step(s(5, true), true), false);
  assert.equal(step(s(6)), false, "the end of the followed turn");
  assert.equal(step(s(7, true)), true, "the next turn from elsewhere");
  assert.equal(step(s(8)), true, "it ended without this tab following it");
  assert.equal(step(row("A", false)), false, "an older gateway sends no rev");
  assert.equal(
    step(row("B", false, { rev: 9 })),
    false,
    "another session only records",
  );
  assert.deepEqual(seen, { id: "B", rev: 9, mine: false });
});
