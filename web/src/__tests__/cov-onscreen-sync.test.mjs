// The conversation on screen follows what other pages do to it: a turn started or ended from
// another browser or computer reads it again, a turn this tab follows does not.
// Run with: node --test web/src/__tests__/cov-onscreen-sync.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { installDom, source, renderHook } from "./cov-stream-helpers.mjs";

installDom();
const { useOnScreenSync } = await source("sessionWatch.ts");

test("another page's turn reads the conversation on screen again", async () => {
  const reread = [];
  let following = false;
  const h = await renderHook(
    ({ sessions, onScreen }) =>
      useOnScreenSync(
        sessions,
        onScreen,
        () => following,
        (id) => reread.push(id),
      ),
    { sessions: [{ id: "s1", running: false, rev: 1 }], onScreen: "s1" },
  );
  await h.rerender({
    sessions: [{ id: "s1", running: true, rev: 2 }],
    onScreen: "s1",
  });
  assert.deepEqual(reread, ["s1"], "a turn started elsewhere");
  following = true;
  await h.rerender({
    sessions: [{ id: "s1", running: true, rev: 2 }, { id: "s2" }],
    onScreen: "s1",
  });
  following = false;
  await h.rerender({
    sessions: [{ id: "s1", running: false, rev: 3 }],
    onScreen: "s1",
  });
  assert.deepEqual(reread, ["s1"], "the end of the turn this tab followed");
  await h.unmount();
});
