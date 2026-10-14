// Runs the subtitles in jsdom (bundled with esbuild, since node cannot import TSX): off they draw
// nothing; on, they show the sentence the voice starts and fade it out when it ends, keeping the
// words while they fade. Also checks where the app draws them and the CSS that keeps them legible,
// out of the way of clicks, and still for a reader who asked for less motion.
// Run with: node --test web/src/__tests__/subtitles.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { fileURLToPath, pathToFileURL } from "node:url";
import { dirname, join } from "node:path";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { JSDOM } from "jsdom";
import { build } from "esbuild";

const here = dirname(fileURLToPath(import.meta.url));
const src = (f) => readFileSync(join(here, "..", f), "utf8");
const dom = new JSDOM("<!doctype html><html><body></body></html>", {
  url: "http://localhost/",
});
for (const k of ["document", "window", "Event", "CustomEvent", "HTMLElement", "Node"]) {
  globalThis[k] = dom.window[k];
}

const out = mkdtempSync(join(here, ".sub-"));
await build({
  entryPoints: [join(here, "..", "Subtitles.tsx")],
  outfile: join(out, "sub.mjs"),
  bundle: true,
  format: "esm",
  platform: "node",
  jsx: "automatic",
  jsxImportSource: "preact",
  external: ["preact", "preact/*"],
  logLevel: "silent",
});
const { Subtitles } = await import(pathToFileURL(join(out, "sub.mjs")).href);
rmSync(out, { recursive: true });
const { SUBTITLE_EVENT, showSubtitle } = await import("../speech.ts");
const { h, render } = await import("preact");
const { act } = await import("preact/test-utils");

async function mount(props) {
  const root = document.createElement("div");
  document.body.appendChild(root);
  const set = (p) => act(() => render(h(Subtitles, p), root));
  await set(props);
  const line = () => root.querySelector(".subtitles");
  const done = async () => {
    await act(() => render(null, root));
    root.remove();
  };
  return { root, set, line, done };
}

test("off: nothing is drawn, and what the voice says is not listened to", async () => {
  const s = await mount({ on: false, size: "medium" });
  await act(() => showSubtitle("All done."));
  assert.equal(s.root.innerHTML, "");
  await s.done();
});

test("on: the sentence the voice starts is shown, and fades out with its words when it ends", async () => {
  const s = await mount({ on: true, size: "large" });
  const p = s.line();
  assert.equal(p.className, "subtitles subtitles-large");
  assert.equal(p.getAttribute("role"), "status");
  assert.equal(p.getAttribute("aria-live"), "polite", "a discreet live region");
  assert.equal(p.getAttribute("aria-atomic"), "true");
  assert.equal(p.hasAttribute("data-shown"), false, "hidden until the voice speaks");

  await act(() => showSubtitle("All done."));
  assert.equal(p.textContent, "All done.");
  assert.equal(p.dataset.shown, "1");
  await act(() => showSubtitle("Tests pass."));
  assert.equal(p.textContent, "Tests pass.", "it follows the next sentence");

  await act(() => showSubtitle(""));
  assert.equal(p.hasAttribute("data-shown"), false);
  assert.equal(p.textContent, "Tests pass.", "the words stay while it fades");

  await s.set({ on: true, size: "small" });
  assert.equal(s.line().className, "subtitles subtitles-small", "the size follows the settings");

  // Turned off while something is shown: it goes, and comes back hidden.
  await act(() => showSubtitle("Again."));
  await s.set({ on: false, size: "small" });
  assert.equal(s.line(), null);
  await s.set({ on: true, size: "small" });
  assert.equal(s.line().hasAttribute("data-shown"), false);
  await s.done();
  // Unmounted, it stops listening.
  window.dispatchEvent(new CustomEvent(SUBTITLE_EVENT, { detail: "late" }));
});

test("the app draws them above the approval panel and the composer, in the conversation's column", () => {
  const app = src("App.tsx");
  const column = app.slice(app.indexOf("function MainColumn"));
  const at = (s) => column.indexOf(s);
  assert.ok(at("<Subtitles") > at("<Conversation />"));
  assert.ok(at("<Subtitles") < at("<ApprovalPanel"));
  assert.ok(at("<Subtitles") < at("<Composer />"));
  assert.match(column, /on=\{speech\.prefs\.subtitles\}/);
  assert.match(column, /size=\{speech\.prefs\.subtitleSize\}/);
});

test("the CSS keeps them legible on any background, out of the way, and still for less motion", () => {
  const css = src("index.css");
  const rule = (sel) => {
    const i = css.indexOf(sel + " {");
    assert.ok(i >= 0, sel);
    return css.slice(i, css.indexOf("}", i));
  };
  assert.match(rule(".subtitles-anchor"), /pointer-events: none/);
  assert.match(rule(".subtitles-anchor"), /height: 0/);
  const box = rule(".subtitles");
  assert.match(box, /background: rgba\(8, 8, 12, 0\.78\)/, "a dark translucent box");
  assert.match(box, /color: #fff/);
  assert.match(box, /text-shadow/);
  assert.match(box, /line-clamp: 3/, "a long sentence does not cover the conversation");
  assert.match(box, /visibility 0s linear 180ms/, "hidden only after it fades");
  for (const size of ["small", "medium", "large"]) {
    assert.match(rule(`.subtitles-${size}`), /font-size/);
  }
  const reduced = css.slice(css.indexOf("/* No slide and no fade"));
  assert.match(reduced.slice(0, 200), /transition: none/);
  const media = css.lastIndexOf("@media (prefers-reduced-motion: reduce)", css.indexOf("/* No slide and no fade"));
  assert.ok(media > css.indexOf(".subtitles {"), "the reduced-motion rule is the subtitles' own");
});
