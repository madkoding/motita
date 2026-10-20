// FloatingMenu's placement, and the guard that every "⋯" menu is drawn in a portal.
// Run with: node --test web/src/__tests__/floating-menu.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { setupDom, load } from "./cov-render-helpers.mjs";

const dom = setupDom();
const { floatingPlace, FloatingMenu } = await load("FloatingMenu.tsx");
const { h, render } = await import("preact");
const { act } = await import("preact/test-utils");

const rect = (o) => ({ top: 0, bottom: 0, left: 0, right: 0, ...o });

test("a menu opens below its button, flush with the right edge", () => {
  const p = floatingPlace(
    rect({ top: 100, bottom: 130, right: 300 }),
    200,
    100,
    1000,
    800,
  );
  assert.deepEqual(p, { top: 134, left: 100 });
});

test("a menu with no room below flips above its button", () => {
  const p = floatingPlace(
    rect({ top: 700, bottom: 730, right: 300 }),
    200,
    100,
    1000,
    800,
  );
  assert.equal(p.top, 596);
});

test("a menu is kept inside the window on every side", () => {
  const far = floatingPlace(
    rect({ top: 10, bottom: 40, right: 20 }),
    240,
    100,
    390,
    800,
  );
  assert.equal(far.left, 4, "not off the left edge");
  const right = floatingPlace(
    rect({ top: 10, bottom: 40, right: 900 }),
    240,
    100,
    390,
    800,
  );
  assert.equal(right.left, 390 - 240 - 4, "not off the right edge");
  const tall = floatingPlace(
    rect({ top: 10, bottom: 40, right: 300 }),
    200,
    900,
    1000,
    800,
  );
  assert.equal(tall.top, 4, "not above the top edge");
});

test("every ⋯ menu is drawn in a portal, out of transformed and blurred ancestors", () => {
  // .project-bar has a transform and .sidebar/.msg a backdrop-filter: each is the containing
  // block of a `fixed` descendant, which is where the menus were lost.
  const src = (f) => readFileSync(new URL(`../${f}`, import.meta.url), "utf8");
  assert.match(
    src("SessionRow.tsx"),
    /<BodyPortal>\s*<div\s+ref=\{ref\}\s+class=\{`row-menu fixed/,
  );
  assert.match(src("FloatingMenu.tsx"), /<BodyPortal>/);
  assert.match(src("ConversationParts.tsx"), /<FloatingMenu\b/);
});

// jsdom lays nothing out: the menu's own size is given by hand, and the anchor's rect by a stub.
Object.defineProperty(dom.window.HTMLElement.prototype, "offsetWidth", {
  configurable: true,
  get: () => 200,
});
Object.defineProperty(dom.window.HTMLElement.prototype, "offsetHeight", {
  configurable: true,
  get: () => 100,
});

function anchorAt(r) {
  const a = document.createElement("button");
  document.body.appendChild(a);
  a.getBoundingClientRect = () => rect(r);
  return a;
}

async function draw(vnode) {
  const root = document.createElement("div");
  document.body.appendChild(root);
  await act(() => render(vnode, root));
  return {
    menu: () => document.querySelector(".floating-menu"),
    gone: () => act(() => render(null, root)),
  };
}

test("the menu is drawn in the body, shown at its button, and follows it on scroll", async () => {
  const a = anchorAt({ top: 100, bottom: 130, right: 600 });
  const d = await draw(h(FloatingMenu, { anchor: a, class: "cp-menu" }, "hi"));
  const el = d.menu();
  assert.equal(el.parentElement, document.body, "portaled out of its parent");
  assert.match(el.className, /cp-menu/);
  assert.equal(el.style.visibility, "visible");
  assert.equal(el.style.top, "134px");
  assert.equal(el.style.left, `${600 - 200}px`);
  a.getBoundingClientRect = () => rect({ top: 300, bottom: 330, right: 600 });
  await act(() => window.dispatchEvent(new window.Event("scroll")));
  assert.equal(d.menu().style.top, "334px");
  await act(() => window.dispatchEvent(new window.Event("resize")));
  await d.gone();
  assert.equal(d.menu(), null);
});

test("a menu whose button is gone, or that has none, stays hidden where it is", async () => {
  const none = await draw(h(FloatingMenu, { anchor: null }, "hi"));
  assert.equal(none.menu().style.visibility, "hidden");
  assert.match(none.menu().className, /^floating-menu\s*$/);
  await none.gone();
  const a = anchorAt({ top: 100, bottom: 130, right: 600 });
  a.remove();
  const off = await draw(h(FloatingMenu, { anchor: a }, "hi"));
  assert.equal(off.menu().style.visibility, "hidden");
  await off.gone();
});
