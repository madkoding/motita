// Auto mode in the web: reading the mode a turn ran in, and the message that corrects it.
import test from "node:test";
import assert from "node:assert/strict";
import { load } from "./tsx-hooks.mjs";

// appParse.ts imports its siblings without extensions, which tsx-hooks.mjs resolves.
const {
  modeOf,
  modeWhyOf,
  unsureWhy,
  whyParts,
  switchedText,
  classifyProgress,
} = await load("appParse.ts");

test("the mode line of a turn is read, and only that line", () => {
  assert.equal(modeOf("mode: plan (question)"), "plan");
  assert.equal(modeOf('mode: task (action "fix")'), "task");
  assert.equal(modeOf("mode: other (x)"), null);
  assert.equal(modeOf("analysing the task..."), null);
  assert.equal(classifyProgress("mode: plan (question)"), "analysis");
});

test("a message is sent again in the other mode, replacing any mode it named", () => {
  assert.equal(
    switchedText("how does it work?", "plan"),
    "/task how does it work?",
  );
  assert.equal(switchedText("fix it", "task"), "/plan fix it");
  assert.equal(switchedText("/plan add a cache", "plan"), "/task add a cache");
  assert.equal(switchedText("/t add a cache", "task"), "/plan add a cache");
  assert.equal(switchedText("/planning day", "task"), "/plan /planning day");
});

test("the reason of the mode line is read, and a bare default is unsure", () => {
  assert.equal(modeWhyOf("mode: plan (question)"), "question");
  assert.equal(modeWhyOf('mode: task (action "fix")'), 'action "fix"');
  assert.equal(modeWhyOf("analysing the task..."), "");
  assert.equal(unsureWhy("default; the model did not decide"), true);
  assert.equal(unsureWhy("question; the model did not decide"), true);
  assert.equal(unsureWhy("model"), false);
  assert.equal(unsureWhy("default"), true);
  assert.equal(unsureWhy("question"), false);
  assert.equal(unsureWhy(""), false);
  assert.deepEqual(whyParts("question; the model did not decide"), [
    "question",
    "the model did not decide",
  ]);
});
