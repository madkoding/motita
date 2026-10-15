// The tab's word on sessions it is not showing, and on a model switch in the one it is.
// sessionWatch.ts imports i18n, so it is bundled with esbuild like the hooks in sessions.test.mjs.
import test from "node:test";
import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { build } from "esbuild";

const src = join(dirname(fileURLToPath(import.meta.url)), "..");
const dir = mkdtempSync(join(tmpdir(), "motita-watch-test-"));
const out = join(dir, "watch.mjs");
await build({
  sourcemap: "inline",
  entryPoints: [join(src, "sessionWatch.ts")],
  bundle: true,
  format: "esm",
  platform: "node",
  outfile: out,
  logLevel: "silent",
  jsx: "automatic",
  jsxImportSource: "preact",
});
const W = await import(out);
rmSync(dir, { recursive: true, force: true });

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
