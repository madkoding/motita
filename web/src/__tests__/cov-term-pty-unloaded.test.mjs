// The interactive terminal when its screen cannot be loaded (a chunk that could not be fetched).
// Run with: node --test web/src/__tests__/cov-term-pty-unloaded.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import {
  setupDom,
  load,
  mounter,
  flush,
  unreachable,
} from "./cov-shell-helpers.mjs";

setupDom("http://motita.test/");
unreachable(["xtermView.ts"]);
const { TermPty, terminalUrl } = await load("TermPty.tsx");
const { h } = await import("preact");
const { act } = await import("preact/test-utils");
const mount = await mounter();

test("a screen that cannot be loaded ends the terminal with that reason", async () => {
  assert.match(terminalUrl("", "s", 80, 24), /^ws:\/\/motita\.test\//);
  const ends = [];
  const m = await mount(
    h(TermPty, {
      machine: "",
      sid: "s",
      open: true,
      onEnd: (r) => ends.push(r),
    }),
  );
  await act(() => flush(5));
  assert.deepEqual(ends, ["The terminal could not be loaded"]);
  await m.done();
});
