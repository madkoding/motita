// Auto mode in the web: reading the mode a turn ran in, and the message that corrects it.
import test from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";

// appParse.ts imports its siblings without extensions, so it is bundled like the hooks in
// sessions.test.mjs.
const here = dirname(fileURLToPath(import.meta.url));
const dir = mkdtempSync(join(tmpdir(), "motita-automode-test-"));
const out = join(dir, "appParse.mjs");
await build({
  entryPoints: [join(here, "..", "appParse.ts")],
  bundle: true,
  format: "esm",
  platform: "node",
  outfile: out,
  logLevel: "silent",
});
const { modeOf, switchedText, classifyProgress } = await import(out);
rmSync(dir, { recursive: true, force: true });

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
