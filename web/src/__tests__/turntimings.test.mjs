// How a finished turn divided its time, drawn under the answer.
// Run with: node --test web/src/__tests__/turntimings.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { installDom, load } from "./cov-settings-helpers.mjs";

installDom();
const { seconds, timingParts, hasTimings, TurnTimingsLine } =
  await load("TurnTimings.tsx");
const { h, render } = await import("preact");

test("a duration reads in ms, seconds or minutes", () => {
  assert.equal(seconds(850), "850 ms");
  assert.equal(seconds(4200), "4.2 s");
  assert.equal(seconds(185000), "3 min 05 s");
});

test("only the parts the turn spent time on are named, with their counts", () => {
  assert.deepEqual(
    timingParts({
      total_ms: 60000,
      model_ms: 30000,
      model_calls: 6,
      commands_ms: 8000,
      commands: 12,
      approval_wait_ms: 2000,
      approvals_asked: 1,
      approvals_reused: 2,
      anchor_ms: 5000,
    }),
    [
      "model 30.0 s (6 calls)",
      "commands 8.0 s (12)",
      "waiting for you 2.0 s (1)",
      "checks 5.0 s",
      "2 approved earlier in the turn",
    ],
  );
  assert.deepEqual(timingParts({ total_ms: 10, commands_ms: 5 }), [
    "commands 5 ms (0)",
  ]);
});

test("only a payload with a total is drawn", () => {
  assert.equal(hasTimings({ total_ms: 1 }), true);
  assert.equal(hasTimings({ total_ms: 0 }), false);
  assert.equal(hasTimings({}), false);
  assert.equal(hasTimings(null), false);
  assert.equal(hasTimings("x"), false);
});

test("the line starts with the whole turn", () => {
  const root = document.createElement("div");
  render(
    h(TurnTimingsLine, {
      timings: { total_ms: 4200, model_ms: 3000, model_calls: 2 },
    }),
    root,
  );
  const line = root.querySelector(".turn-timings");
  assert.equal(line.textContent, "4.2 s · model 3.0 s (2 calls)");
  assert.ok(line.getAttribute("title"));
});
