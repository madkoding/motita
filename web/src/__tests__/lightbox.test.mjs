// Runs the real Lightbox in jsdom (bundled with esbuild, since node cannot import TSX): zoom
// buttons, keyboard, pair navigation and closing.
// Run with: node --test web/src/__tests__/lightbox.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { fileURLToPath, pathToFileURL } from "node:url";
import { dirname, join } from "node:path";
import { mkdtempSync, rmSync } from "node:fs";
import { JSDOM } from "jsdom";
import { build } from "esbuild";

const here = dirname(fileURLToPath(import.meta.url));
const dom = new JSDOM("<!doctype html><html><body></body></html>", {
  url: "http://localhost/",
});
for (const k of [
  "document",
  "window",
  "KeyboardEvent",
  "HTMLElement",
  "Node",
]) {
  globalThis[k] = dom.window[k];
}
Object.defineProperty(globalThis, "navigator", {
  value: dom.window.navigator,
  configurable: true,
});
globalThis.requestAnimationFrame = (cb) => setTimeout(() => cb(Date.now()), 0);
globalThis.cancelAnimationFrame = (id) => clearTimeout(id);

const out = mkdtempSync(join(here, ".lb-"));
await build({
  entryPoints: [join(here, "..", "Lightbox.tsx")],
  outfile: join(out, "lb.mjs"),
  bundle: true,
  format: "esm",
  jsx: "automatic",
  jsxImportSource: "preact",
  platform: "node",
  external: ["preact", "preact/*"],
  logLevel: "silent",
});
const { Lightbox } = await import(pathToFileURL(join(out, "lb.mjs")).href);
const { h, render } = await import("preact");
const { act } = await import("preact/test-utils");
rmSync(out, { recursive: true });

// act() flushes Preact's pending renders AND effects before it resolves. A fixed timer was not
// enough: under load the effect that attaches the key listener could still be pending when the next
// key was sent, so the key was lost and the test failed now and then.
const key = (k) =>
  document.dispatchEvent(
    new dom.window.KeyboardEvent("keydown", { key: k, bubbles: true }),
  );

test("zoom, fit, pair navigation and close work", async () => {
  const root = document.createElement("div");
  document.body.appendChild(root);
  let closed = 0;
  await act(() => {
    render(
      h(Lightbox, {
        images: [
          { src: "a.png", label: "A" },
          { src: "b.png", label: "B" },
        ],
        start: 0,
        onClose: () => {
          closed++;
        },
      }),
      root,
    );
  });
  const zoom = () =>
    root.querySelector("[data-testid=lightbox-zoom]").textContent;
  const img = () => root.querySelector(".lb-img").getAttribute("src");
  assert.equal(zoom(), "100%");
  assert.equal(img(), "a.png");

  await act(() => key("+"));
  assert.equal(zoom(), "125%");
  await act(() => {
    key("-");
    key("-");
  });
  assert.equal(zoom(), "80%");
  await act(() => key("0"));
  assert.equal(zoom(), "100%");

  await act(() => key("ArrowRight"));
  assert.equal(img(), "b.png");
  assert.match(root.querySelector(".lb-title").textContent, /B \(2\/2\)/);
  await act(() => key("ArrowRight"));
  assert.equal(img(), "a.png", "the set wraps around");

  await act(() => key("Escape"));
  assert.equal(closed, 1);
});

test("a single image has no navigation", async () => {
  const root = document.createElement("div");
  document.body.appendChild(root);
  await act(() => {
    render(
      h(Lightbox, {
        images: [{ src: "a.png", label: "A" }],
        start: 0,
        onClose: () => {},
      }),
      root,
    );
  });
  assert.equal(root.querySelector('[aria-label="Next image"]'), null);
  assert.doesNotMatch(root.querySelector(".lb-title").textContent, /\(/);
});
