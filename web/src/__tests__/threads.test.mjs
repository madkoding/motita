// Threads in the sidebar: which session is a project's main one and how threads are ordered under it.
// Run with: node --test web/src/__tests__/threads.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { load } from "./cov-chat-helpers.mjs";

const { mainSessionOf, groupThreads } = await load("threads.ts");

const s = (id, created, extra = {}) => ({ id, created, ...extra });

test("the main session is the oldest one of the project that is not a thread", () => {
  const list = [
    s("t", "2024-02", { project_id: "p", parent_id: "m" }),
    s("m", "2024-01", { project_id: "p" }),
    s("o", "2023-01", { project_id: "other" }),
  ];
  assert.equal(mainSessionOf(list, "p").id, "m");
  assert.equal(mainSessionOf(list, "none"), undefined);
});

test("each main session is followed by its threads, oldest first", () => {
  const list = [
    s("t2", "2024-03", { parent_id: "a" }),
    s("a", "2024-01"),
    s("b", "2024-02"),
    s("t1", "2024-02", { parent_id: "a" }),
    s("lost", "2024-04", { parent_id: "gone" }),
  ];
  assert.deepEqual(
    groupThreads(list).map((x) => x.id),
    ["a", "t1", "t2", "b", "lost"],
  );
});
