// Regression tests for the motion system: the shared tokens, the reduced-motion fallback, the
// composited-only rule, and the exit copy (motion.ts). Run with: node --test web/src/__tests__/
import test from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { tmpdir } from "node:os";
import { build } from "esbuild";
import { JSDOM } from "jsdom";

const here = dirname(fileURLToPath(import.meta.url));
const motionCss = readFileSync(join(here, "..", "motion.css"), "utf8");
const indexCss = readFileSync(join(here, "..", "index.css"), "utf8");
const GHOST = ".motion-ghost";
// Longer than the ghost's removal fallback in motion.ts (GHOST_MAX_MS).
const PAST_GHOST_FALLBACK_MS = 650;

const dom = new JSDOM("<!doctype html><html><body></body></html>", {
  url: "http://localhost/",
});
globalThis.document = dom.window.document;
globalThis.window = dom.window;

const dir = mkdtempSync(join(tmpdir(), "motita-motion-test-"));
const out = join(dir, "motion.mjs");
await build({
  entryPoints: [join(here, "..", "motion.ts")],
  bundle: true,
  format: "esm",
  platform: "node",
  outfile: out,
  logLevel: "silent",
});
const { leaveGhost } = await import(out);
rmSync(dir, { recursive: true, force: true });

test("motion.css is imported by index.css", () => {
  assert.match(indexCss, /@import "\.\/motion\.css";/);
});

test("the motion tokens are defined once, on :root", () => {
  for (const token of [
    "--motion-fast",
    "--motion-base",
    "--motion-slow",
    "--motion-slower",
    "--ease-out",
    "--ease-in",
    "--ease-standard",
    "--ease-spring",
    "--motion-shift",
    "--motion-shift-lg",
    "--motion-scale",
  ]) {
    assert.match(motionCss, new RegExp(String.raw`:root \{[^}]*${token}:`), token);
  }
});

test("reduced motion turns every movement into a fade", () => {
  const reduced = /@media \(prefers-reduced-motion: reduce\) \{\s*:root \{([^}]*)\}/.exec(
    motionCss,
  );
  assert.ok(reduced, "a reduced-motion block redefines the tokens");
  assert.match(reduced[1], /--motion-shift: 0px;/);
  assert.match(reduced[1], /--motion-shift-lg: 0px;/);
  assert.match(reduced[1], /--motion-scale: 1;/);
  assert.match(reduced[1], /--motion-travel: 0;/);
});

test("the shared keyframes move only transform, scale and opacity", () => {
  const bodies = [...motionCss.matchAll(/@keyframes motion-[\w-]+ \{([\s\S]*?)\n\}/g)];
  assert.ok(bodies.length >= 10);
  for (const [, body] of bodies) {
    // Line by line rather than one multiline regex, so no match backtracks across lines.
    for (const line of body.split("\n")) {
      const decl = /^([a-z-]+):/.exec(line.trimStart());
      if (decl) {
        assert.ok(["opacity", "transform", "scale"].includes(decl[1]), decl[1]);
      }
    }
  }
});

test("chat text no longer animates a blur filter", () => {
  assert.doesNotMatch(indexCss, /textFadeIn/);
});

test("a leaving copy is not made outside a real browser", () => {
  const el = document.createElement("div");
  document.body.appendChild(el);
  leaveGhost(el, "fade", "pop");
  assert.equal(document.querySelectorAll(GHOST).length, 0);
  leaveGhost(null, "fade");
});

test("a leaving copy is inert, has no ids and does not count as a dialog", async () => {
  const el = document.createElement("div");
  el.id = "settings";
  el.dataset.modal = "";
  el.innerHTML =
    '<dialog open data-dialog-root data-testid="x"><input id="f" value="a"></dialog>';
  document.body.appendChild(el);
  el.querySelector("input").value = "typed";
  // jsdom has no Web Animations API and no layout: stand them in for this one element.
  el.animate = () => {};
  el.getBoundingClientRect = () => ({ top: 0, left: 0, width: 100, height: 50 });
  leaveGhost(el, "fade", "pop");
  const ghost = document.querySelector(GHOST);
  assert.ok(ghost);
  assert.equal(ghost.getAttribute("aria-hidden"), "true");
  assert.equal(ghost.id, "");
  assert.equal(ghost.querySelectorAll("[id]").length, 0);
  assert.equal(ghost.querySelectorAll("[data-modal], [data-dialog-root], [data-testid]").length, 0);
  assert.equal("modal" in ghost.dataset, false);
  assert.equal(ghost.querySelector("input").value, "typed");
  assert.ok(ghost.firstElementChild.classList.contains("motion-pop-out"));
  await new Promise((r) => setTimeout(r, PAST_GHOST_FALLBACK_MS));
  assert.equal(document.querySelectorAll(GHOST).length, 0, "removed by the fallback");
  el.remove();
});

// The stream reveal, against a stand-in for the CSS Custom Highlight API (jsdom has none).
const revealOut = join(mkdtempSync(join(tmpdir(), "motita-reveal-test-")), "reveal.mjs");
await build({
  entryPoints: [join(here, "..", "streamReveal.ts")],
  bundle: true,
  format: "esm",
  platform: "node",
  outfile: revealOut,
  logLevel: "silent",
});
const { streamReveal } = await import(revealOut);

function fakeHighlights() {
  const reg = new Map();
  globalThis.CSS = { highlights: reg };
  globalThis.Highlight = class extends Set {};
  globalThis.StaticRange = dom.window.StaticRange;
  globalThis.NodeFilter = dom.window.NodeFilter;
  globalThis.MutationObserver = dom.window.MutationObserver;
  globalThis.requestAnimationFrame = () => 1;
  globalThis.cancelAnimationFrame = () => {};
  return reg;
}
const hiddenText = (reg) =>
  [...(reg.get("stream-hidden") ?? [])]
    .map((r) => {
      const range = document.createRange();
      range.setStart(r.startContainer, r.startOffset);
      range.setEnd(r.endContainer, r.endOffset);
      return range.toString();
    })
    .join("");

test("an answer read from the history shows at once", () => {
  const reg = fakeHighlights();
  const el = document.createElement("div");
  el.innerHTML = '<div class="markdown-body"><button class="copy-msg-btn">copy</button><p>Hola mundo</p></div>';
  const r = streamReveal(el, "Hola mundo");
  assert.equal(hiddenText(reg), "");
  r.dispose();
});

test("the text a stream adds is hidden until revealed, and nothing else is", () => {
  const reg = fakeHighlights();
  const el = document.createElement("div");
  el.innerHTML = '<div class="markdown-body"><button class="copy-msg-btn">copy</button><p>Hola</p></div>';
  const r = streamReveal(el, "Hola");
  el.innerHTML =
    '<div class="markdown-body"><button class="copy-msg-btn">copy</button><p>Hola mundo</p></div>';
  r.update("Hola mundo");
  assert.equal(hiddenText(reg), " mundo");
  // An edit (not more of the same answer) shows at once.
  el.innerHTML = '<div class="markdown-body"><p>Adiós</p></div>';
  r.update("Adiós");
  assert.equal(hiddenText(reg), "");
  r.dispose();
  assert.equal(reg.get("stream-hidden").size, 0);
  delete globalThis.CSS;
  delete globalThis.Highlight;
});
