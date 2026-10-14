// The conversation lets the reader scroll up while an answer is being written, and follows the end
// again when they come back to it. Run with: node --test web/src/__tests__/
import test from "node:test";
import assert from "node:assert/strict";

const { newIntent, readerActs, scrolled, USER_SCROLL_MS } = await import(
  "../scrollIntent.ts"
);

const SLACK = 120;

test("the first wheel up from the end lets go of the end at once", () => {
  const s = newIntent(5000);
  readerActs(s, 1000, true, 5000);
  assert.equal(s.pinned, false, "unpinned before any scroll event");
  // The flick lands inside the slack, which used to keep the reader pinned.
  scrolled(s, 4950, 50, 1010, SLACK);
  assert.equal(s.pinned, false);
});

test("a finger dragging the view up from the end lets go of it", () => {
  const s = newIntent(5000);
  s.holding = true;
  readerActs(s, 1000, false, 5000);
  assert.equal(s.pinned, true, "a touch alone says nothing yet");
  scrolled(s, 4990, 10, 1016, SLACK);
  assert.equal(s.pinned, false);
});

test("the content growing or shrinking never unpins the reader", () => {
  const s = newIntent(4551);
  scrolled(s, 4511, 40, 5000, SLACK);
  assert.equal(s.pinned, true);
});

test("coming back down near the end takes it again", () => {
  const s = newIntent(5000);
  readerActs(s, 1000, true, 5000);
  scrolled(s, 4000, 1000, 1010, SLACK);
  assert.equal(s.pinned, false);
  readerActs(s, 2000, false, 4000);
  scrolled(s, 4900, 100, 2010, SLACK);
  assert.equal(s.pinned, true);
});

test("a move down that stops far from the end leaves the reader where they are", () => {
  const s = newIntent(5000);
  readerActs(s, 1000, true, 5000);
  scrolled(s, 3000, 2000, 1010, SLACK);
  readerActs(s, 2000, false, 3000);
  scrolled(s, 3500, 1500, 2010, SLACK);
  assert.equal(s.pinned, false);
});

test("landing exactly on the end re-pins, whoever moved", () => {
  const s = newIntent(5000);
  readerActs(s, 1000, true, 5000);
  scrolled(s, 4000, 1000, 1010, SLACK);
  scrolled(s, 4000, 0, 1010 + USER_SCROLL_MS * 4, SLACK);
  assert.equal(s.pinned, true);
});

test("a wheel up on a conversation that does not scroll keeps following", () => {
  const s = newIntent(0);
  readerActs(s, 1000, true, 0);
  assert.equal(s.pinned, true);
});
