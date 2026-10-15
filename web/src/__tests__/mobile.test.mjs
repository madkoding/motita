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

const SIDEBAR = "Sidebar.tsx";
const HOVER = "(hover: hover)";
const COARSE = "(pointer: coarse)";
const PHONE_MEDIA = "(max-width: 767px)";

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
      if (css[i] === "{") {
        depth++;
      }
      if (css[i] === "}") {
        depth--;
      }
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
      if (rest[i] === "{") {
        depth++;
      }
      if (rest[i] === "}") {
        depth--;
      }
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
  assert.equal(PHONE_QUERY, PHONE_MEDIA);
  assert.match(src(SIDEBAR), /fixed md:relative/);
  assert.match(src(SIDEBAR), /md:hidden/);
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
  row.innerHTML =
    '<span class="label">x</span><button class="row-hit"></button>';
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
  assert.match(src(SIDEBAR), /isPhone\(\) && closesOnPhone\(e\.target\)/);
});

test("the keyboard is told apart from the URL bar sliding away", () => {
  // Layout height of a phone, the visible height once the URL bar hides, and with a keyboard up.
  const PHONE_H = 844;
  const URL_BAR_H = 780;
  const KEYBOARD_H = 500;
  assert.equal(keyboardOpen(PHONE_H, PHONE_H), false);
  assert.equal(
    keyboardOpen(PHONE_H, URL_BAR_H),
    false,
    "browser chrome is not a keyboard",
  );
  assert.equal(keyboardOpen(PHONE_H, KEYBOARD_H), true);
});

test("the shell is the visible height and keeps clear of the notch and home indicator", () => {
  const html = readFileSync(join(here, "..", "..", "index.html"), "utf8");
  assert.match(html, /viewport-fit=cover/);
  assert.match(html, /interactive-widget=resizes-content/);
  const shell = rule(css, ".app-shell");
  assert.match(shell, /height: var\(--app-h, 100dvh\)/);
  for (const side of ["top", "right", "bottom", "left"]) {
    assert.match(shell, new RegExp(String.raw`env\(safe-area-inset-${side}\)`));
  }
  assert.match(
    css,
    /html\[data-keyboard="open"\] \.app-shell \{\s*padding-bottom: 0;/,
  );
  assert.match(
    mediaBlocks(PHONE_MEDIA),
    /\.sidebar \{[^}]*env\(safe-area-inset-top\)[^}]*env\(safe-area-inset-bottom\)/,
  );
  const app = src("App.tsx");
  assert.match(app, /class="app-shell app-bg /);
  assert.match(app, /useVisualViewport\(\);/);
  assert.doesNotMatch(app, /h-dvh/, "nothing sizes itself past the keyboard");
  // The toast sits under the header bar, so it keeps clear of the notch rather than the home bar.
  assert.match(
    src("ToastCard.tsx"),
    /top-\[calc\([^\]]*env\(safe-area-inset-top\)/,
  );
});

test("copy buttons are hidden for hover only where something can hover", () => {
  const touch = outsideMedia(HOVER);
  for (const sel of [
    ".markdown-body .copy-btn",
    ".markdown-body .copy-msg-btn",
  ]) {
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
    mediaBlocks(HOVER),
    /\.markdown-body \.copy-btn,\s*\.markdown-body \.copy-msg-btn \{\s*opacity: 0;/,
  );
});

test("a tapped button does not stay lifted by a sticky hover", () => {
  assert.doesNotMatch(
    outsideMedia(HOVER),
    /button:not\(:disabled\):hover \{\s*transform/,
  );
  assert.match(
    mediaBlocks(HOVER),
    /button:not\(:disabled\):hover \{\s*transform: translateY\(-1px\);/,
  );
});

test("touch targets are at least 44px, only for a finger", () => {
  const coarse = mediaBlocks(COARSE);
  assert.match(
    coarse,
    /button:not\(\.row-hit\):not\(\.term-tab\),\s*select,\s*summary,\s*\[role="button"\] \{\s*min-height: 44px;/,
  );
  assert.match(
    coarse,
    /button:is\(\[aria-label\], \[title\]\)[^{]*\{\s*min-width: 44px;/,
  );
  assert.match(coarse, /\.term-tab \{\s*left: -44px;\s*width: 44px;/);
  // The 44px floor must not leak to the desktop.
  assert.doesNotMatch(outsideMedia(COARSE), /min-height: 44px;/);
});

test("on a phone only the open drawer keeps its tab, so the others do not cover its text", () => {
  assert.match(
    mediaBlocks(PHONE_MEDIA),
    /body:has\(\.term-drawer\.is-open\) \.term-drawer:not\(\.is-open\) \.term-tab \{\s*visibility: hidden;/,
  );
  assert.doesNotMatch(outsideMedia(PHONE_MEDIA), /:has\(\.term-drawer\.is-open\)/);
});

test("fields are 16px on a touch screen, so iOS does not zoom into them", () => {
  assert.match(
    mediaBlocks(COARSE),
    /:root :is\(input:not\(\[type="checkbox"\]\):not\(\[type="radio"\]\), textarea, select\) \{\s*font-size: 16px;/,
  );
});

test("a project's + is visible on a touch screen", () => {
  const button = src("NewSessionButton.ts");
  assert.doesNotMatch(
    button,
    /group-hover:|opacity-0/,
    "no hover-only utility",
  );
  assert.match(button, /project-new-session/);
  assert.doesNotMatch(
    outsideMedia(HOVER),
    /\.project-new-session[^{]*\{\s*opacity: 0;/,
  );
  assert.match(
    mediaBlocks(HOVER),
    /\.project-header \.project-new-session \{\s*opacity: 0;/,
  );
  assert.match(
    mediaBlocks(HOVER),
    /\.project-header:hover \.project-new-session/,
  );
});

test("the settings tabs fit a phone's width instead of running off the panel", () => {
  const phone = mediaBlocks(PHONE_MEDIA);
  assert.match(phone, /\.settings-tab\s*\{[^}]*white-space:\s*normal/);
  // Equal share (zero basis) with a floor, so the row scrolls rather than squeezing a label.
  assert.match(phone, /\.settings-tab\s*\{[^}]*flex:\s*1 0 5\.5rem/);
  assert.match(phone, /\.settings-tab\s*\{[^}]*min-width:\s*5\.5rem/);
});

test("an open dialog moves the toast off its title", () => {
  assert.match(
    css,
    /body:has\(\[data-modal\]\) \.toast-layer\s*\{[^}]*bottom:/,
  );
  assert.match(src("ToastCard.tsx"), /class="toast-layer /);
  assert.match(src("ModalBackdrop.tsx"), /data-modal=/);
  for (const f of ["AuthModal.tsx", "GitConnect.tsx", "RepoPicker.tsx"]) {
    assert.match(src(f), /\bdata-modal\b/, f);
  }
});

test("full-window previews opened from a chat bubble are drawn in a portal", () => {
  // A bubble's backdrop-filter is the containing block of a fixed descendant.
  for (const f of ["Lightbox.tsx", "ConversationParts.tsx"]) {
    assert.match(src(f), /<BodyPortal>/, f);
  }
});
