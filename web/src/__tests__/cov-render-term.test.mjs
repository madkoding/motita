// The terminal drawer's text (Term.tsx): the output colouring, and the typewriter that brings each
// command, its output and its status in one after the other. Animation frames are stepped by hand.
// Run with: node --test web/src/__tests__/cov-render-term.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { setupDom, load, flush, waitFor } from "./cov-render-helpers.mjs";

setupDom();

// requestAnimationFrame only queues; frame(ms) runs what is queued as if `ms` had passed since
// the typing started.
let seq = 0;
const queue = new Map();
const cancelled = [];
globalThis.requestAnimationFrame = (cb) => {
  queue.set(++seq, cb);
  return seq;
};
globalThis.cancelAnimationFrame = (id) => {
  cancelled.push(id);
  queue.delete(id);
};
let base = performance.now();
const startClock = () => {
  base = performance.now();
};
function frame(ms) {
  const due = [...queue.values()];
  queue.clear();
  for (const cb of due) {
    cb(base + ms);
  }
  return due.length;
}

const T = await load("Term.tsx");
const { h, render } = await import("preact");
const { act } = await import("preact/test-utils");

async function mount(vnode) {
  const root = document.createElement("div");
  document.body.appendChild(root);
  await act(() => render(vnode, root));
  // After the render: the typing started during it, so frames count from a moment after.
  startClock();
  return {
    root,
    async rerender(v) {
      await act(() => render(v, root));
      startClock();
    },
    async done() {
      await act(() => render(null, root));
      root.remove();
      queue.clear();
    },
  };
}

// ── colouring ─────────────────────────────────────────────────────────────────

test("esc escapes the four characters HTML cares about", () => {
  assert.equal(T.esc(`<a href="x">&</a>`), "&lt;a href=&quot;x&quot;&gt;&amp;&lt;/a&gt;");
});

test("each output line takes the colour of what it says", () => {
  const lines = [
    "+++ b/main.go",
    "--- a/main.go",
    "--- FAIL: TestParse (0.01s)",
    "@@ -1,2 +1,3 @@",
    "+added",
    "-removed",
    "FAIL\tgithub.com/x/y",
    "  panic: boom",
    "make: *** [all] exit status 2",
    "ok  \tgithub.com/x/y\t0.2s",
    "Warning: deprecated flag",
    "just text",
  ];
  const html = T.colorizeOutput(lines.join("\n")).split("\n");
  const cls = html.map((l) => /^<span class="([\w-]+)">/.exec(l)?.[1] ?? "");
  assert.deepEqual(cls, [
    "to-diffhead",
    "to-diffhead",
    "to-err",
    "to-hunk",
    "to-add",
    "to-del",
    "to-err",
    "to-err",
    "to-err",
    "to-ok",
    "to-warn",
    "",
  ]);
  assert.equal(html[11], "just text");
});

test("a file:line location is underlined; things that only look like one are not", () => {
  const out = T.colorizeOutput(
    "at src/app/main.go:42:7 and lib.ts:3, not .go:12, not file.go:x, not a.b, <tag>",
  );
  const locs = [...out.matchAll(/<span class="to-loc">([^<]*)<\/span>/g)].map((m) => m[1]);
  assert.deepEqual(locs, ["src/app/main.go:42:7", "lib.ts:3"]);
  assert.match(out, /&lt;tag&gt;$/);
});

// ── the typewriter ───────────────────────────────────────────────────────────

test("instant text is all there at once, with one tick and one done", async () => {
  const ticks = [];
  const dones = [];
  const { root, done } = await mount(
    h(T.Typed, {
      html: 'a <span class="hljs-x">b</span><script>x</script><b>c</b>',
      as: "div",
      class: "out",
      instant: true,
      onTick: () => ticks.push(1),
      onDone: () => dones.push(1),
    }),
  );
  const el = root.querySelector("div.out");
  assert.equal(el.textContent, "a bc", "only spans survive the sanitizer");
  assert.ok(el.querySelector("span.hljs-x"));
  assert.equal(el.querySelector("script, b"), null);
  assert.equal(ticks.length, 1);
  assert.equal(dones.length, 1);
  assert.equal(el.classList.contains("is-typing"), false);
  assert.equal(queue.size, 0);
  await done();
});

test("text is typed at the asked speed, keeping its colours, then finishes once", async () => {
  const ticks = [];
  const dones = [];
  const props = {
    html: 'ab<span class="c">cd</span>ef',
    cps: 100,
    onTick: () => ticks.push(1),
    onDone: () => dones.push(1),
  };
  const { root, rerender, done } = await mount(h(T.Typed, props));
  const el = root.querySelector("span");
  assert.equal(el.textContent, "");
  assert.ok(el.classList.contains("is-typing"));
  frame(30); // 3 characters at 100 per second
  assert.equal(el.textContent, "abc");
  assert.ok(el.querySelector("span.c"), "the colour span is there from the first letter");
  const before = ticks.length;
  frame(30); // the same time again: nothing new, no tick
  assert.equal(ticks.length, before);
  frame(1000);
  assert.equal(el.textContent, "abcdef");
  assert.equal(el.classList.contains("is-typing"), false);
  assert.deepEqual(dones, [1]);
  // A re-render with a new speed but the same text has nothing left to type.
  await rerender(h(T.Typed, { ...props, cps: 50 }));
  assert.equal(el.textContent, "abcdef");
  assert.deepEqual(dones, [1], "done is reported once");
  await done();
});

test("a different rendering of the same text goes on from what was typed", async () => {
  const dones = [];
  const v = (html, extra = {}) =>
    h(T.Typed, { html, cps: 100, onDone: () => dones.push(1), ...extra });
  const { root, rerender, done } = await mount(v("echo hi"));
  const el = root.querySelector("span");
  frame(40);
  assert.equal(el.textContent, "echo");
  startClock();
  await rerender(v('<span class="hljs-built_in">echo</span> hi'));
  assert.equal(el.textContent, "echo", "what was typed stays typed");
  assert.ok(el.querySelector(".hljs-built_in"));
  frame(1000);
  assert.equal(el.textContent, "echo hi");
  assert.deepEqual(dones, [1]);
  // More text after it finished starts the typing again, and finishes again.
  startClock();
  await rerender(v("echo hi there"));
  assert.ok(el.classList.contains("is-typing"));
  frame(1000);
  assert.equal(el.textContent, "echo hi there");
  assert.deepEqual(dones, [1, 1]);
  // Switching to instant half way shows the whole text, not the cut one.
  startClock();
  await rerender(v("a much longer line"));
  frame(20);
  // A different text keeps the count typed so far (13) and goes on: 2 more at 100 per second.
  assert.equal(el.textContent, "a much longer l");
  await rerender(v("a much longer line", { instant: true }));
  assert.equal(el.textContent, "a much longer line");
  await done();
});

test("without a speed the typing takes about a second, faster with a boost", async () => {
  const long = "x".repeat(600);
  const a = await mount(h(T.Typed, { html: long }));
  frame(100); // 600 chars / 1.2 s = 500 per second
  assert.equal(a.root.textContent.length, 50);
  await a.done();
  const b = await mount(h(T.Typed, { html: long, boost: 2 }));
  frame(100);
  assert.equal(b.root.textContent.length, 100);
  await b.done();
  const c = await mount(h(T.Typed, { html: "short" }));
  frame(30); // a short line is typed at the minimum of 70 per second
  assert.equal(c.root.textContent, "sh");
  await c.done();
});

test("the typing never cuts a character in two", async () => {
  const { root, done } = await mount(h(T.Typed, { html: "a😀b\uD800c！d", cps: 1000 }));
  const el = root.querySelector("span");
  frame(2); // 2 code units: the cut falls inside the emoji's surrogate pair
  assert.equal(el.textContent, "a");
  frame(3); // after the whole emoji
  assert.equal(el.textContent, "a😀");
  frame(5); // right after a lone first half: that is held back too
  assert.equal(el.textContent, "a😀b");
  frame(7); // after a wide character outside the surrogate range
  assert.equal(el.textContent, "a😀b\uD800c！");
  await done();
});

test("unmounting stops the typing", async () => {
  const { done } = await mount(h(T.Typed, { html: "unfinished", cps: 10 }));
  const pending = [...queue.keys()];
  assert.equal(pending.length, 1);
  await done();
  assert.ok(cancelled.includes(pending[0]));
});

// ── the pieces ───────────────────────────────────────────────────────────────

test("a command is shown escaped at once, then coloured as bash", async () => {
  const { root, done } = await mount(h(T.TermCommand, { cmd: "ls -la <dir>", instant: true }));
  assert.equal(root.querySelector(".term-prompt").textContent, "$");
  const code = root.querySelector(".term-code");
  assert.equal(code.textContent, "ls -la <dir>");
  await waitFor(() => code.querySelector("span[class^=hljs]"), "the bash colours");
  assert.equal(code.textContent, "ls -la <dir>");
  await done();
});

test("a command or an output unmounted before the grammar arrives is left alone", async () => {
  const root = document.createElement("div");
  document.body.appendChild(root);
  // Synchronous acts: the effect starts the import and the unmount cancels it in the same tick.
  act(() =>
    render(
      h("div", {}, [
        h(T.TermCommand, { cmd: "echo late", instant: true }),
        h(T.TermOutput, { text: '{"late": 1}', instant: true }),
      ]),
      root,
    ),
  );
  assert.equal(root.querySelector(".term-code").textContent, "echo late");
  act(() => render(null, root));
  await flush(10);
  assert.equal(root.innerHTML, "", "nothing is drawn back into the unmounted place");
  root.remove();
});

test("JSON output goes through the JSON grammar; other output keeps its line colours", async () => {
  const json = await mount(h(T.TermOutput, { text: '{"a": [1, "b"]}', instant: true }));
  const pre = json.root.querySelector("pre.term-output");
  await waitFor(() => pre.querySelector(".hljs-attr"), "the JSON colours");
  assert.equal(pre.textContent, '{"a": [1, "b"]}');
  assert.ok(pre.querySelector(".hljs-attr"));
  await json.done();

  const arr = await mount(h(T.TermOutput, { text: '["x"]', instant: true }));
  await waitFor(() => arr.root.querySelector(".hljs-string"), "the JSON colours");
  await arr.done();

  for (const text of ["[ok] built", "[1, 2]"]) {
    const log = await mount(h(T.TermOutput, { text, instant: true }));
    await flush(10);
    assert.equal(log.root.querySelector("[class^=hljs]"), null, text);
    await log.done();
  }

  const failed = await mount(h(T.TermOutput, { text: "FAIL x", instant: true }));
  assert.ok(failed.root.querySelector(".to-err"));
  await failed.done();

});

test("an entry waits its turn, then types command, output and status one after the other", async () => {
  const finished = [];
  const props = {
    cmd: "go test",
    out: "ok  pkg",
    status: { label: "exit 0", kind: "" },
    phase: "wait",
    boost: 1,
    onFinished: () => finished.push(1),
  };
  const { root, rerender, done } = await mount(h(T.TermEntry, props));
  assert.equal(root.innerHTML, "", "waiting: nothing drawn");
  startClock();
  await rerender(h(T.TermEntry, { ...props, phase: "live" }));
  assert.ok(root.querySelector(".term-code"));
  assert.equal(root.querySelector(".term-output"), null, "the output waits for the command");
  assert.equal(root.querySelector(".term-status"), null);
  frame(5000);
  await flush(2);
  assert.ok(root.querySelector(".term-output"));
  assert.equal(root.querySelector(".term-status"), null, "the status waits for the output");
  startClock();
  frame(5000);
  await flush(2);
  const status = root.querySelector(".term-status");
  assert.ok(status);
  assert.equal(status.className, "term-status");
  frame(5000);
  await flush(2);
  assert.equal(status.textContent, "exit 0");
  assert.deepEqual(finished, [1]);
  await done();
});

test("an entry already seen is drawn whole; a failed one is marked", async () => {
  const finished = [];
  const { root, done } = await mount(
    h(T.TermEntry, {
      cmd: "make",
      out: "error: nope",
      status: { label: "exit 2", kind: "bad" },
      phase: "done",
      onFinished: () => finished.push(1),
    }),
  );
  assert.equal(root.querySelector(".term-code").textContent, "make");
  assert.equal(root.querySelector(".term-output").textContent, "error: nope");
  assert.equal(root.querySelector(".term-status").className, "term-status bad");
  assert.deepEqual(finished, [1]);
  await done();
});

test("a command with no output goes straight to its status; a running one keeps the caret", async () => {
  const finished = [];
  const { root, done } = await mount(
    h(T.TermEntry, {
      cmd: "sleep 9",
      out: "",
      status: { label: "running…", kind: "running" },
      phase: "live",
      onFinished: () => finished.push(1),
    }),
  );
  frame(5000);
  await flush(2);
  const status = root.querySelector(".term-status.running");
  assert.ok(status);
  assert.equal(root.querySelector(".term-output"), null);
  frame(5000);
  await flush(2);
  assert.equal(status.textContent, "running…");
  assert.ok(status.querySelector(".term-caret"));
  assert.deepEqual(finished, [], "a running command is not finished");
  await done();

  const quiet = await mount(
    h(T.TermEntry, { cmd: "true", out: "", status: { label: "ok", kind: "" }, phase: "done" }),
  );
  assert.equal(quiet.root.querySelector(".term-status").textContent, "ok");
  await quiet.done();
});

test("the empty terminal types its placeholder with a caret", async () => {
  const ticks = [];
  const { root, done } = await mount(h(T.TermEmpty, { onTick: () => ticks.push(1) }));
  frame(5000);
  assert.equal(root.querySelector(".term-empty").textContent, "no commands yet");
  assert.ok(root.querySelector(".term-caret"));
  assert.ok(ticks.length >= 2);
  await done();
});
