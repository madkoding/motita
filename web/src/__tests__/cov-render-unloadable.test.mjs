// When the lazily loaded renderers cannot be fetched (an old cached shell, a dropped connection),
// an answer still reads: code stays plain, a formula or a diagram says what failed and shows its
// source, emoji stay characters, and the terminal keeps its uncoloured text.
// Run with: node --test web/src/__tests__/cov-render-unloadable.test.mjs
import test, { mock } from "node:test";
import assert from "node:assert/strict";
import { setupDom, load, flush, FAIL } from "./cov-render-helpers.mjs";

setupDom({
  overrides: {
    "./highlight": FAIL,
    "./math": FAIL,
    "./mermaid": FAIL,
    "./emoji": FAIL,
  },
});
globalThis.requestAnimationFrame = (cb) =>
  setTimeout(() => cb(performance.now() + 1e6), 0);
globalThis.cancelAnimationFrame = (id) => clearTimeout(id);

const md = await load("Markdown.tsx");
const term = await load("Term.tsx");
const { cancelQuiet } = await load("hydration.ts");
const { h, render } = await import("preact");
const { act } = await import("preact/test-utils");

async function mount(vnode) {
  const root = document.createElement("div");
  document.body.appendChild(root);
  await act(() => render(vnode, root));
  await flush(10);
  return {
    root,
    async done() {
      await act(() => render(null, root));
      root.remove();
    },
  };
}

test("an answer whose renderers cannot be loaded shows its source and the reason", async () => {
  const info = mock.method(console, "info", () => {});
  const content = [
    'Hi 😀 $x^2$ and <span class="math-inline" data-math="y^3">y</span>',
    "",
    "```go",
    "a := 1 // -->",
    "```",
    "",
    "```mermaid",
    "graph LR; A --> B",
    "```",
  ].join("\n");
  const { root, done } = await mount(h(md.Markdown, { content }));
  const el = root.firstElementChild;

  const pre = el.querySelector("pre.code-block");
  assert.equal(pre.dataset.done, "plain");
  assert.equal(
    pre.querySelector("code").textContent,
    "a := 1 // -->",
    "uncoloured, but intact",
  );
  assert.equal(
    pre.querySelector(".copy-btn").dataset.copyText,
    "a := 1 // -->",
  );

  const [plainMath, givenMath] = el.querySelectorAll(".math-inline");
  for (const m of [plainMath, givenMath]) {
    assert.equal(m.dataset.done, "failed");
    assert.ok(m.classList.contains("render-error"));
    assert.equal(
      m.querySelector("p").textContent,
      "the math renderer could not be loaded.",
    );
    assert.equal(
      m.querySelector(".render-error-reason").textContent,
      "the module could not be loaded",
    );
  }
  assert.equal(plainMath.querySelector("pre").textContent, "");
  assert.equal(
    givenMath.querySelector("pre").textContent,
    "y^3",
    "a formula that carries its TeX shows it",
  );

  const diagram = el.querySelector(".mermaid-block");
  assert.equal(diagram.dataset.done, "failed");
  assert.equal(
    diagram.querySelector("p").textContent,
    "the diagram renderer could not be loaded.",
  );

  assert.equal(el.dataset.emoji, "plain");
  assert.match(el.textContent, /😀/);
  assert.equal(el.querySelector("img.emoji"), null);

  const said = info.mock.calls.map((c) => c.arguments[0]);
  assert.ok(said.includes("motita: syntax highlighting is unavailable:"));
  assert.ok(said.includes("motita: the emoji images are unavailable:"));
  info.mock.restore();
  await done();
  cancelQuiet();
});

test("the terminal keeps the escaped command and the line colours when the grammar cannot load", async () => {
  const { root, done } = await mount(
    h("div", {}, [
      h(term.TermCommand, { cmd: "echo '<x>'", instant: true }),
      h(term.TermOutput, { text: '{"ok": true}', instant: true }),
    ]),
  );
  const code = root.querySelector(".term-code");
  assert.equal(code.textContent, "echo '<x>'");
  assert.equal(code.querySelector("span"), null, "no grammar colours");
  const out = root.querySelector(".term-output");
  assert.equal(out.textContent, '{"ok": true}');
  assert.equal(out.querySelector(".hljs-attr"), null);
  await done();
});
