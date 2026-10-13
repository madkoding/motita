// The phone layout: what made the page hard or impossible to use with a finger on a narrow
// screen, pinned so it does not come back. The desktop rules are checked to stay behind a hover
// or a min-width query, which is what keeps the desktop look unchanged.
// Run with: node --test web/src/__tests__/
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { JSDOM } from "jsdom";

const here = dirname(fileURLToPath(import.meta.url));
const src = (f) => readFileSync(join(here, "..", f), "utf8");
const css = src("index.css");

const dom = new JSDOM("<!doctype html><html><body></body></html>", {
  url: "http://localhost/",
});
globalThis.document = dom.window.document;
globalThis.window = dom.window;
globalThis.Element = dom.window.Element;

const { initialSidebarOpen, keyboardOpen, closesOnPhone, PHONE_QUERY } =
  await import("../viewport.ts");

// The bodies of every `@media <query>` block in the stylesheet, joined.
function mediaBlocks(query) {
  const out = [];
  let from = 0;
  for (;;) {
    const at = css.indexOf(`@media ${query}`, from);
    if (at < 0) {
      return out.join("\n");
    }
    const open = css.indexOf("{", at);
    let depth = 1;
    let i = open + 1;
    while (depth > 0) {
      if (css[i] === "{") depth++;
      if (css[i] === "}") depth--;
      i++;
    }
    out.push(css.slice(open + 1, i - 1));
    from = i;
  }
}

// The stylesheet with every `@media <query>` block taken out.
function outsideMedia(query) {
  let rest = css;
  for (;;) {
    const at = rest.indexOf(`@media ${query}`);
    if (at < 0) {
      return rest;
    }
    const open = rest.indexOf("{", at);
    let depth = 1;
    let i = open + 1;
    while (depth > 0) {
      if (rest[i] === "{") depth++;
      if (rest[i] === "}") depth--;
      i++;
    }
    rest = rest.slice(0, at) + rest.slice(i);
  }
}

// The declarations of the first rule whose selector list is exactly `selector`.
function rule(text, selector) {
  const at = text.indexOf(selector + " {");
  assert.ok(at >= 0, `${selector} has a rule`);
  return text.slice(at, text.indexOf("}", at));
}

test("the phone breakpoint is the one the sidebar markup switches at", () => {
  assert.equal(PHONE_QUERY, "(max-width: 767px)");
  assert.match(src("Sidebar.tsx"), /fixed md:relative/);
  assert.match(src("Sidebar.tsx"), /md:hidden/);
});

test("on a phone the sidebar starts closed, whatever the desktop remembered", () => {
  assert.equal(initialSidebarOpen(null, true), false);
  assert.equal(initialSidebarOpen("true", true), false);
  assert.equal(initialSidebarOpen("false", true), false);
  assert.equal(initialSidebarOpen(null, false), true, "desktop default: open");
  assert.equal(initialSidebarOpen("true", false), true);
  assert.equal(initialSidebarOpen("false", false), false);
});

test("a phone's open or closed sidebar is not saved as the desktop preference", () => {
  const list = src("useSessionList.ts");
  const persist = list.slice(list.indexOf("// Persist sidebar preference."));
  assert.match(
    persist.slice(0, persist.indexOf("localStorage.setItem")),
    /if \(isPhone\(\)\) \{\s*return;/,
  );
});

test("picking a session on a phone closes the sidebar sheet", () => {
  const list = src("useSessionList.ts");
  assert.match(
    list,
    /if \(rs\.sessionId && isPhone\(\)\) \{\s*setSidebarOpen\(false\);/,
  );
  // Tapping the session that is already open changes no id, so the sidebar closes on the tap.
  const row = document.createElement("div");
  row.className = "session-row";
  row.innerHTML = '<span class="label">x</span><button class="row-hit"></button>';
  const project = document.createElement("div");
  project.className = "project-header";
  project.innerHTML = '<button class="row-hit"></button>';
  assert.equal(closesOnPhone(row.querySelector(".row-hit")), true);
  assert.equal(closesOnPhone(row.querySelector(".label")), false);
  assert.equal(
    closesOnPhone(project.querySelector(".row-hit")),
    false,
    "folding a project keeps the list open",
  );
  assert.equal(closesOnPhone(null), false);
  assert.match(src("Sidebar.tsx"), /isPhone\(\) && closesOnPhone\(e\.target\)/);
});

test("the keyboard is told apart from the URL bar sliding away", () => {
  assert.equal(keyboardOpen(844, 844), false);
  assert.equal(keyboardOpen(844, 780), false, "browser chrome is not a keyboard");
  assert.equal(keyboardOpen(844, 500), true);
});

test("the shell is the visible height and keeps clear of the notch and home indicator", () => {
  const html = readFileSync(join(here, "..", "..", "index.html"), "utf8");
  assert.match(html, /viewport-fit=cover/);
  assert.match(html, /interactive-widget=resizes-content/);
  const shell = rule(css, ".app-shell");
  assert.match(shell, /height: var\(--app-h, 100dvh\)/);
  for (const side of ["top", "right", "bottom", "left"]) {
    assert.match(shell, new RegExp(`env\\(safe-area-inset-${side}\\)`));
  }
  assert.match(
    css,
    /html\[data-keyboard="open"\] \.app-shell \{\s*padding-bottom: 0;/,
  );
  assert.match(
    mediaBlocks("(max-width: 767px)"),
    /\.sidebar \{[^}]*env\(safe-area-inset-top\)[^}]*env\(safe-area-inset-bottom\)/,
  );
  const app = src("App.tsx");
  assert.match(app, /class="app-shell app-bg /);
  assert.match(app, /useVisualViewport\(\);/);
  assert.doesNotMatch(app, /h-dvh/, "nothing sizes itself past the keyboard");
  assert.match(src("ToastCard.tsx"), /env\(safe-area-inset-bottom\)/);
});

test("copy buttons are hidden for hover only where something can hover", () => {
  const touch = outsideMedia("(hover: hover)");
  for (const sel of [".markdown-body .copy-btn", ".markdown-body .copy-msg-btn"]) {
    const outside = touch.split(sel + " {").slice(1);
    assert.ok(outside.length > 0, `${sel} is styled`);
    for (const body of outside) {
      assert.doesNotMatch(
        body.slice(0, body.indexOf("}")),
        /opacity:\s*0;/,
        `${sel} is not hidden on a touch screen`,
      );
    }
  }
  assert.match(
    mediaBlocks("(hover: hover)"),
    /\.markdown-body \.copy-btn,\s*\.markdown-body \.copy-msg-btn \{\s*opacity: 0;/,
  );
});

test("a tapped button does not stay lifted by a sticky hover", () => {
  assert.doesNotMatch(
    outsideMedia("(hover: hover)"),
    /button:not\(:disabled\):hover \{\s*transform/,
  );
  assert.match(
    mediaBlocks("(hover: hover)"),
    /button:not\(:disabled\):hover \{\s*transform: translateY\(-1px\);/,
  );
});

test("touch targets are at least 44px, only for a finger", () => {
  const coarse = mediaBlocks("(pointer: coarse)");
  assert.match(
    coarse,
    /button:not\(\.row-hit\):not\(\.term-tab\),\s*select,\s*summary,\s*\[role="button"\] \{\s*min-height: 44px;/,
  );
  assert.match(coarse, /button:is\(\[aria-label\], \[title\]\)[^{]*\{\s*min-width: 44px;/);
  assert.match(coarse, /\.term-tab \{\s*left: -44px;\s*width: 44px;/);
  // The 44px floor must not leak to the desktop.
  assert.doesNotMatch(outsideMedia("(pointer: coarse)"), /min-height: 44px;/);
});

test("fields are 16px on a touch screen, so iOS does not zoom into them", () => {
  assert.match(
    mediaBlocks("(pointer: coarse)"),
    /:root :is\(input:not\(\[type="checkbox"\]\):not\(\[type="radio"\]\), textarea, select\) \{\s*font-size: 16px;/,
  );
});
