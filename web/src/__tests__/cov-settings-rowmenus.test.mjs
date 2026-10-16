// The sidebar's row menus through the real useRowMenus hook: where the "⋯" menu goes (below its
// button, or above it near the bottom, kept inside the window), opening, toggling and closing
// with its exit animation (mocked timers), the presses and keys that close it, following its row
// on scroll, and the long-press menu's state.
// Run with: node --test web/src/__tests__/cov-settings-rowmenus.test.mjs
import test, { mock } from "node:test";
import assert from "node:assert/strict";
import { installDom, load, settle } from "./cov-settings-helpers.mjs";

installDom();
const { useRowMenus } = await load("useRowMenus.ts");
const { h, render } = await import("preact");

const EXIT = 140;
const ROWS = [
  { type: "session", id: "s1", title: "One" },
  { type: "project", id: "p1", title: "Proj" },
];

function mount() {
  const ref = {};
  function Harness() {
    const menus = useRowMenus();
    ref.m = menus;
    return h(
      "div",
      null,
      ROWS.map((r) =>
        h(
          "div",
          { key: r.id, class: "row", id: "row-" + r.id },
          h(
            "button",
            {
              "data-row-menu-trigger": "",
              id: "btn-" + r.id,
              onClick: (e) => menus.toggleRowMenu(e.currentTarget, r),
            },
            "⋯",
          ),
        ),
      ),
      menus.rowMenu &&
        h(
          "div",
          { class: "row-menu" + (menus.rowMenuClosing ? " closing" : "") },
          h("span", { id: "inside" }, menus.rowMenu.title),
        ),
    );
  }
  const root = document.createElement("div");
  document.body.appendChild(root);
  render(h(Harness, null), root);
  return {
    root,
    ref,
    async unmount() {
      render(null, root);
      root.remove();
      await settle();
    },
  };
}

// place gives a trigger the box the layout would.
function place(el, { top, bottom, right }) {
  el.getBoundingClientRect = () => ({
    top,
    bottom,
    right,
    left: right - 20,
    width: 20,
    height: bottom - top,
  });
}
function setWindow(w, hgt) {
  Object.defineProperty(window, "innerWidth", { configurable: true, value: w });
  Object.defineProperty(window, "innerHeight", {
    configurable: true,
    value: hgt,
  });
}
const fire = (el, type) =>
  el.dispatchEvent(new window.Event(type, { bubbles: true }));

test("the menu opens below its button, flush with its right edge, and toggles shut", async () => {
  mock.timers.enable({ apis: ["setTimeout"] });
  try {
    setWindow(800, 600);
    const m = mount();
    await settle();
    assert.equal(m.ref.m.rowMenu, null);
    const b = m.root.querySelector("#btn-s1");
    place(b, { top: 100, bottom: 120, right: 300 });
    b.click();
    await settle();
    assert.deepEqual(m.ref.m.rowMenu, ROWS[0]);
    assert.deepEqual(m.ref.m.rowMenuPos, { top: 124, left: 140 });
    assert.equal(m.ref.m.rowMenuAbove, false);
    assert.ok(m.root.querySelector(".row-menu"));

    // The same button again closes it, after the exit animation.
    b.click();
    await settle();
    assert.equal(m.ref.m.rowMenuClosing, true);
    assert.ok(m.root.querySelector(".row-menu.closing"));
    // A second close while it leaves changes nothing.
    m.ref.m.closeRowMenu();
    mock.timers.tick(EXIT - 1);
    await settle();
    assert.ok(m.root.querySelector(".row-menu"));
    mock.timers.tick(1);
    await settle();
    assert.equal(m.root.querySelector(".row-menu"), null);
    assert.equal(m.ref.m.rowMenu, null);
    assert.equal(m.ref.m.rowMenuClosing, false);
    await m.unmount();
  } finally {
    mock.timers.reset();
  }
});

test("near the bottom it flips above; a narrow window keeps it inside", async () => {
  setWindow(170, 300);
  const m = mount();
  await settle();
  const b = m.root.querySelector("#btn-p1");
  // Below would end at 290 + 4 + 96 > 300: above, at 260 - 4 - 96.
  place(b, { top: 260, bottom: 290, right: 400 });
  b.click();
  await settle();
  assert.equal(m.ref.m.rowMenuAbove, true);
  assert.deepEqual(m.ref.m.rowMenuPos, { top: 160, left: 6 });
  // Too close to the top to go above either: pinned to the gap. Left never under the gap.
  place(b, { top: 50, bottom: 290, right: 10 });
  fire(window, "resize");
  await settle();
  assert.deepEqual(m.ref.m.rowMenuPos, { top: 4, left: 4 });
  await m.unmount();
});

test("opening another row's menu while one leaves cancels the leaving", async () => {
  mock.timers.enable({ apis: ["setTimeout"] });
  try {
    setWindow(800, 600);
    const m = mount();
    await settle();
    const a = m.root.querySelector("#btn-s1");
    const b = m.root.querySelector("#btn-p1");
    place(a, { top: 0, bottom: 20, right: 300 });
    place(b, { top: 30, bottom: 50, right: 300 });
    a.click();
    await settle();
    m.ref.m.closeRowMenu();
    await settle();
    assert.equal(m.ref.m.rowMenuClosing, true);
    b.click();
    await settle();
    assert.deepEqual(m.ref.m.rowMenu, ROWS[1]);
    assert.equal(m.ref.m.rowMenuClosing, false);
    // The old close does not take the new menu down.
    mock.timers.tick(EXIT * 2);
    await settle();
    assert.deepEqual(m.ref.m.rowMenu, ROWS[1]);
    // And it can be closed again normally.
    m.ref.m.closeRowMenu();
    mock.timers.tick(EXIT);
    await settle();
    assert.equal(m.ref.m.rowMenu, null);
    await m.unmount();
  } finally {
    mock.timers.reset();
  }
});

test("a press outside, a touch outside and Escape close it; presses on the menu or a toggle do not", async () => {
  mock.timers.enable({ apis: ["setTimeout"] });
  try {
    setWindow(800, 600);
    const m = mount();
    await settle();
    const a = m.root.querySelector("#btn-s1");
    place(a, { top: 0, bottom: 20, right: 300 });
    const reopen = async () => {
      mock.timers.tick(EXIT);
      await settle();
      assert.equal(m.ref.m.rowMenu, null);
      a.click();
      await settle();
      assert.ok(m.ref.m.rowMenu);
    };
    a.click();
    await settle();

    fire(m.root.querySelector("#inside"), "mousedown");
    fire(m.root.querySelector("#btn-p1"), "mousedown");
    document.dispatchEvent(
      new window.KeyboardEvent("keydown", { key: "Enter" }),
    );
    await settle();
    assert.equal(m.ref.m.rowMenuClosing, false);

    fire(m.root.querySelector("#row-p1"), "mousedown");
    await settle();
    assert.equal(m.ref.m.rowMenuClosing, true);
    await reopen();
    fire(document.body, "touchstart");
    await settle();
    assert.equal(m.ref.m.rowMenuClosing, true);
    await reopen();
    document.dispatchEvent(
      new window.KeyboardEvent("keydown", { key: "Escape" }),
    );
    await settle();
    assert.equal(m.ref.m.rowMenuClosing, true);
    mock.timers.tick(EXIT);
    await settle();
    // Closed: presses no longer reach it.
    fire(document.body, "mousedown");
    await settle();
    assert.equal(m.ref.m.rowMenuClosing, false);
    await m.unmount();
  } finally {
    mock.timers.reset();
  }
});

test("on scroll the menu follows its row, and closes when the row is gone", async () => {
  mock.timers.enable({ apis: ["setTimeout"] });
  try {
    setWindow(800, 600);
    const m = mount();
    await settle();
    const a = m.root.querySelector("#btn-s1");
    place(a, { top: 0, bottom: 20, right: 300 });
    a.click();
    await settle();
    assert.equal(m.ref.m.rowMenuPos.top, 24);
    place(a, { top: 100, bottom: 120, right: 300 });
    fire(document.body, "scroll");
    await settle();
    assert.equal(m.ref.m.rowMenuPos.top, 124);
    a.remove();
    fire(document.body, "scroll");
    await settle();
    assert.equal(m.ref.m.rowMenuClosing, true);
    mock.timers.tick(EXIT);
    await settle();
    assert.equal(m.ref.m.rowMenu, null);
    await m.unmount();
  } finally {
    mock.timers.reset();
  }
});

test("the long-press menu is plain state", async () => {
  const m = mount();
  await settle();
  const at = { ...ROWS[0], x: 10, y: 20 };
  m.ref.m.setContextMenu(at);
  await settle();
  assert.deepEqual(m.ref.m.contextMenu, at);
  m.ref.m.closeContextMenu();
  await settle();
  assert.equal(m.ref.m.contextMenu, null);
  await m.unmount();
});
