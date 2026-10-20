// The small shared pieces of the page: the body portal, the modal backdrop, the dialog keyboard
// rules, the element listeners, the phone viewport, the exit animation copy, the clipboard and
// the icons. Each runs for real in jsdom, bundled with esbuild.
// Run with: node --test web/src/__tests__/cov-render-small.test.mjs
import test, { mock } from "node:test";
import assert from "node:assert/strict";
import { setupDom, load } from "./cov-render-helpers.mjs";

const dom = setupDom();
const m = Object.assign(
  {},
  await load("BodyPortal.tsx"),
  await load("ModalBackdrop.tsx"),
  await load("useBackdropClose.ts"),
  await load("useDialog.ts"),
  await load("domHooks.ts"),
  await load("viewport.ts"),
  await load("motion.ts"),
  await load("clipboard.ts"),
  await load("icons.tsx"),
  await load("SettingsIcons.tsx"),
  await load("providerLogos.ts"),
);
const { h, render } = await import("preact");
const { useRef } = await import("preact/hooks");
const { act } = await import("preact/test-utils");

// jsdom lays nothing out, so offsetParent is always null; a laid-out element has its parent.
Object.defineProperty(dom.window.HTMLElement.prototype, "offsetParent", {
  configurable: true,
  get() {
    return this.hidden ? null : this.parentElement;
  },
});

async function mount(vnode) {
  const root = document.createElement("div");
  document.body.appendChild(root);
  await act(() => render(vnode, root));
  return {
    root,
    rerender: (v) => act(() => render(v, root)),
    done: async () => {
      await act(() => render(null, root));
      root.remove();
    },
  };
}

const click = (el) =>
  el.dispatchEvent(
    new MouseEvent("click", { bubbles: true, cancelable: true }),
  );
const key = (k, opts = {}) => {
  const e = new KeyboardEvent("keydown", {
    key: k,
    bubbles: true,
    cancelable: true,
    ...opts,
  });
  (document.activeElement || document.body).dispatchEvent(e);
  return e;
};

// ── BodyPortal ────────────────────────────────────────────────────────────────

test("BodyPortal draws its children at the end of the body, not where it is rendered", async () => {
  const { root, done } = await mount(
    h(
      "section",
      { id: "bubble" },
      h(m.BodyPortal, {}, h("p", { id: "layer" }, "over all")),
    ),
  );
  const layer = document.getElementById("layer");
  assert.equal(layer.textContent, "over all");
  assert.equal(layer.parentElement, document.body);
  assert.equal(root.querySelector("#layer"), null);
  await done();
  assert.equal(document.getElementById("layer"), null);
});

// ── ModalBackdrop / useBackdropClose ──────────────────────────────────────────

test("a click on the backdrop closes the dialog; a click inside stays inside", async () => {
  const closed = [];
  const outside = [];
  const onDoc = () => outside.push(1);
  document.addEventListener("click", onDoc);
  const { root, done } = await mount(
    h(
      m.ModalBackdrop,
      { onClose: () => closed.push(1) },
      h("button", { id: "in" }, "ok"),
    ),
  );
  const backdrop = root.firstElementChild;
  assert.equal(backdrop.className, m.BACKDROP_CLASS);
  assert.equal(backdrop.getAttribute("data-modal"), "true");
  click(document.getElementById("in"));
  assert.deepEqual(closed, []);
  assert.deepEqual(outside, [], "the inside click never reached the page");
  click(backdrop);
  assert.deepEqual(closed, [1]);
  await done();
  document.removeEventListener("click", onDoc);
});

test("a backdrop that is not a dialog has its own class and no data-modal", async () => {
  const { root, done } = await mount(
    h(m.ModalBackdrop, { onClose() {}, class: "catcher", modal: false }, "x"),
  );
  const el = root.firstElementChild;
  assert.equal(el.className, "catcher");
  assert.equal(el.hasAttribute("data-modal"), false);
  await done();
});

test("the backdrop calls the latest onClose it was given", async () => {
  const calls = [];
  const v = (n) => h(m.ModalBackdrop, { onClose: () => calls.push(n) }, "x");
  const { root, rerender, done } = await mount(v(1));
  await rerender(v(2));
  click(root.firstElementChild);
  assert.deepEqual(calls, [2]);
  await done();
});

test("useBackdropClose does nothing while no element is attached", async () => {
  let closed = 0;
  function Detached() {
    const ref = useRef(null);
    m.useBackdropClose(ref, () => closed++);
    return h("div", { id: "detached" });
  }
  const { done } = await mount(h(Detached));
  click(document.getElementById("detached"));
  assert.equal(closed, 0);
  await done();
});

// ── useDialog ─────────────────────────────────────────────────────────────────

function Dialog({ id, onClose, children, rootAttr = true, attach = true }) {
  const ref = useRef(null);
  m.useDialog(ref, onClose);
  return h(
    "div",
    {
      ref: attach ? ref : undefined,
      id,
      "data-dialog-root": rootAttr || undefined,
    },
    children,
  );
}

test("a dialog focuses its autofocus element and gives focus back when it closes", async () => {
  const opener = document.createElement("button");
  document.body.appendChild(opener);
  opener.focus();
  const { done } = await mount(
    h(Dialog, { id: "d", onClose() {} }, [
      h("button", { id: "first" }, "first"),
      h("button", { id: "auto", "data-autofocus": true }, "auto"),
    ]),
  );
  assert.equal(document.activeElement.id, "auto");
  await done();
  assert.equal(document.activeElement, opener);
  opener.remove();
});

test("without an autofocus element the first focusable one gets the focus", async () => {
  const { done } = await mount(
    h(Dialog, { id: "d", onClose() {} }, [
      h("button", { id: "off", disabled: true }, "off"),
      h("input", { id: "field" }),
    ]),
  );
  assert.equal(document.activeElement.id, "field");
  await done();
});

test("Escape closes only the topmost dialog", async () => {
  const closed = [];
  const { done } = await mount(
    h("div", {}, [
      h(
        Dialog,
        { id: "under", onClose: () => closed.push("under") },
        h("button", {}, "a"),
      ),
      h(
        Dialog,
        { id: "over", onClose: () => closed.push("over") },
        h("button", {}, "b"),
      ),
    ]),
  );
  const ime = key("Escape", { isComposing: true });
  assert.deepEqual(closed, [], "an Escape that drops an IME candidate");
  assert.equal(ime.defaultPrevented, false);
  const e = key("Escape");
  assert.deepEqual(closed, ["over"]);
  assert.equal(e.defaultPrevented, true);
  key("x");
  assert.deepEqual(closed, ["over"], "another key closes nothing");
  await done();
});

test("Escape in an open listbox closes the list, not the dialog", async () => {
  const closed = [];
  const { done } = await mount(
    h(
      Dialog,
      { id: "pick", onClose: () => closed.push(1) },
      h("button", {
        id: "trigger",
        "aria-haspopup": "listbox",
        "aria-expanded": "true",
      }),
    ),
  );
  document.getElementById("trigger").focus();
  const e = key("Escape");
  assert.deepEqual(closed, [], "the open list keeps the dialog open");
  assert.equal(e.defaultPrevented, false);
  document.getElementById("trigger").setAttribute("aria-expanded", "false");
  key("Escape");
  assert.deepEqual(closed, [1], "a closed list lets the dialog close");
  await done();
});

test("a dialog without data-dialog-root still answers Escape (nothing else claims it)", async () => {
  const closed = [];
  const { done } = await mount(
    h(
      Dialog,
      { id: "plain", rootAttr: false, onClose: () => closed.push(1) },
      "text only",
    ),
  );
  key("Escape");
  assert.deepEqual(closed, [1]);
  await done();
});

test("a dialog whose element is not attached ignores the keyboard", async () => {
  const closed = [];
  const { done } = await mount(
    h(
      Dialog,
      { id: "ghost", attach: false, onClose: () => closed.push(1) },
      h("button", {}, "x"),
    ),
  );
  key("Escape");
  assert.deepEqual(closed, []);
  await done();
});

test("Tab and Shift+Tab wrap around inside the dialog", async () => {
  const outside = document.createElement("button");
  outside.id = "outside";
  document.body.appendChild(outside);
  const { done } = await mount(
    h(Dialog, { id: "d", onClose() {} }, [
      h("button", { id: "a" }, "a"),
      h("button", { id: "hidden", hidden: true }, "hidden"),
      h("button", { id: "mid" }, "mid"),
      h("button", { id: "z" }, "z"),
    ]),
  );
  assert.equal(document.activeElement.id, "a");
  // Shift+Tab on the first wraps to the last.
  let e = key("Tab", { shiftKey: true });
  assert.equal(document.activeElement.id, "z");
  assert.equal(e.defaultPrevented, true);
  // Tab on the last wraps to the first.
  e = key("Tab");
  assert.equal(document.activeElement.id, "a");
  assert.equal(e.defaultPrevented, true);
  // In the middle the browser moves the focus itself.
  document.getElementById("mid").focus();
  e = key("Tab");
  assert.equal(e.defaultPrevented, false);
  e = key("Tab", { shiftKey: true });
  assert.equal(e.defaultPrevented, false);
  assert.equal(document.activeElement.id, "mid");
  // Focus that escaped the dialog is brought back: Tab to the first, Shift+Tab to the last.
  outside.focus();
  key("Tab");
  assert.equal(document.activeElement.id, "a");
  outside.focus();
  key("Tab", { shiftKey: true });
  assert.equal(document.activeElement.id, "z");
  await done();
  outside.remove();
});

test("Tab in a dialog with nothing to focus is left alone", async () => {
  const { done } = await mount(
    h(Dialog, { id: "d", onClose() {} }, "only text"),
  );
  const e = key("Tab");
  assert.equal(e.defaultPrevented, false);
  await done();
});

test("closing a dialog whose opener cannot take focus any more does not throw", async () => {
  const opener = document.createElement("button");
  document.body.appendChild(opener);
  opener.focus();
  opener.focus = () => {
    throw new Error("gone");
  };
  const { done } = await mount(
    h(Dialog, { id: "d", onClose() {} }, h("button", {}, "x")),
  );
  assert.ok(document.getElementById("d"), "the dialog is open");
  await assert.doesNotReject(done(), "unmounted without throwing");
  assert.equal(document.getElementById("d"), null, "and it is gone");
  opener.remove();
});

// ── domHooks ─────────────────────────────────────────────────────────────────

test("useListener calls the latest handler, and nothing once unmounted", async () => {
  const seen = [];
  let target;
  function Box({ tag }) {
    const ref = useRef(null);
    m.useListener(ref, "click", () => seen.push(tag));
    return h("div", { ref, id: "box" });
  }
  const { rerender, done } = await mount(h(Box, { tag: "first" }));
  target = document.getElementById("box");
  click(target);
  await rerender(h(Box, { tag: "second" }));
  click(target);
  assert.deepEqual(seen, ["first", "second"]);
  await done();
  click(target);
  assert.deepEqual(seen, ["first", "second"]);
});

test("useListener with no element attached listens to nothing", async () => {
  const seen = [];
  function Loose() {
    const ref = useRef(null);
    m.useListener(ref, "click", () => seen.push(1));
    return h("div", { id: "loose" });
  }
  const { done } = await mount(h(Loose));
  click(document.getElementById("loose"));
  assert.deepEqual(seen, []);
  await done();
});

test("useStopClicks keeps a click inside its element", async () => {
  const outer = [];
  function Menu() {
    const ref = useRef(null);
    m.useStopClicks(ref);
    return h("div", { ref }, h("button", { id: "item" }, "item"));
  }
  const { root, done } = await mount(h(Menu));
  root.addEventListener("click", () => outer.push(1));
  click(document.getElementById("item"));
  assert.deepEqual(outer, []);
  await done();
});

function touch(el, type, x = 0, y = 0) {
  const e = new Event(type, { bubbles: true });
  Object.defineProperty(e, "touches", { value: [{ clientX: x, clientY: y }] });
  el.dispatchEvent(e);
}

test("a finger resting on a row opens its menu at the touch; a move or a lift cancels it", async () => {
  const pressed = [];
  function Row() {
    const ref = useRef(null);
    m.useLongPress(ref, (x, y) => pressed.push([x, y]));
    return h("div", { ref, id: "row" });
  }
  const { done } = await mount(h(Row));
  const row = document.getElementById("row");
  mock.timers.enable({ apis: ["setTimeout"] });
  try {
    touch(row, "touchstart", 12, 34);
    mock.timers.tick(499);
    assert.deepEqual(pressed, []);
    mock.timers.tick(1);
    assert.deepEqual(pressed, [[12, 34]]);

    touch(row, "touchstart", 1, 1);
    touch(row, "touchmove");
    mock.timers.tick(600);
    touch(row, "touchstart", 2, 2);
    touch(row, "touchend");
    mock.timers.tick(600);
    assert.deepEqual(pressed, [[12, 34]], "a move and a lift cancel the press");
    touch(row, "touchend");

    // Two presses in a row: the first timer still fires after the second was cancelled.
    touch(row, "touchstart", 3, 3);
    touch(row, "touchstart", 4, 4);
    touch(row, "touchmove");
    mock.timers.tick(600);
    assert.deepEqual(pressed, [
      [12, 34],
      [3, 3],
    ]);
  } finally {
    mock.timers.reset();
  }
  await done();
});

// ── viewport ──────────────────────────────────────────────────────────────────

test("the phone helpers read the media query and the sidebar preference", () => {
  assert.equal(m.PHONE_QUERY, "(max-width: 767px)");
  const saved = window.matchMedia;
  window.matchMedia = (q) => ({ matches: q === m.PHONE_QUERY });
  assert.equal(m.isPhone(), true);
  window.matchMedia = () => {
    throw new Error("no media queries");
  };
  assert.equal(m.isPhone(), false);
  window.matchMedia = saved;
  assert.equal(m.initialSidebarOpen(null, false), true);
  assert.equal(m.initialSidebarOpen("false", false), false);
  assert.equal(m.initialSidebarOpen(null, true), false);
  const row = document.createElement("div");
  row.className = "session-row";
  row.innerHTML = '<button class="row-hit"><span id="label">x</span></button>';
  document.body.appendChild(row);
  assert.equal(m.closesOnPhone(document.getElementById("label")), true);
  assert.equal(m.closesOnPhone(document.body), false);
  assert.equal(m.closesOnPhone(null), false);
  row.remove();
  assert.equal(m.keyboardOpen(800, 500), true);
  assert.equal(m.keyboardOpen(800, 700), false);
});

function Shell() {
  m.useVisualViewport();
  return h("div", {}, "shell");
}

test("without a visual viewport the shell keeps the CSS height", async () => {
  const saved = window.visualViewport;
  window.visualViewport = undefined;
  const { done } = await mount(h(Shell));
  assert.equal(document.documentElement.style.getPropertyValue("--app-h"), "");
  await done();
  window.visualViewport = saved;
});

test("the shell follows the visual viewport, marks the keyboard and drops it while zoomed", async () => {
  const root = document.documentElement;
  Object.defineProperty(root, "clientHeight", {
    value: 800,
    configurable: true,
  });
  const vv = new dom.window.EventTarget();
  vv.scale = 1;
  vv.height = 799.6;
  window.visualViewport = vv;
  const scrolls = [];
  window.scrollTo = (x, y) => scrolls.push([x, y]);
  const resize = (patch) => {
    Object.assign(vv, patch);
    vv.dispatchEvent(new Event("resize"));
  };
  const { done } = await mount(h(Shell));
  assert.equal(root.style.getPropertyValue("--app-h"), "800px");
  assert.equal(root.dataset.keyboard, undefined);

  resize({ height: 450 });
  assert.equal(root.style.getPropertyValue("--app-h"), "450px");
  assert.equal(root.dataset.keyboard, "open");
  assert.deepEqual(scrolls, [[0, 0]]);

  resize({ scale: 2 });
  assert.equal(root.style.getPropertyValue("--app-h"), "");
  assert.equal(root.dataset.keyboard, undefined);

  resize({ scale: 1, height: 760 });
  assert.equal(root.style.getPropertyValue("--app-h"), "760px");
  assert.equal(root.dataset.keyboard, undefined);

  await done();
  resize({ height: 300 });
  assert.equal(
    root.style.getPropertyValue("--app-h"),
    "760px",
    "no longer listening",
  );
  delete window.visualViewport;
});

// ── motion ────────────────────────────────────────────────────────────────────

// laidOut gives an element a box and the Web Animations entry point jsdom lacks.
function laidOut(el, rect = { top: 10, left: 20, width: 300, height: 200 }) {
  el.getBoundingClientRect = () => rect;
  el.animate = () => ({});
  return el;
}

test("leaveGhost does nothing for a missing, detached, unanimatable or empty element", () => {
  const before = document.body.children.length;
  m.leaveGhost(null, "fade");
  const detached = laidOut(document.createElement("div"));
  m.leaveGhost(detached, "fade");
  const plain = document.createElement("div");
  document.body.appendChild(plain);
  m.leaveGhost(plain, "fade");
  const empty = laidOut(document.createElement("div"), {
    top: 0,
    left: 0,
    width: 0,
    height: 5,
  });
  document.body.appendChild(empty);
  m.leaveGhost(empty, "fade");
  const flat = laidOut(document.createElement("div"), {
    top: 0,
    left: 0,
    width: 5,
    height: 0,
  });
  document.body.appendChild(flat);
  m.leaveGhost(flat, "fade");
  assert.equal(document.querySelectorAll(".motion-ghost").length, 0);
  plain.remove();
  empty.remove();
  flat.remove();
  assert.equal(document.body.children.length, before);
});

test("leaveGhost pins an inert copy with the typed values, then removes it", () => {
  const el = laidOut(document.createElement("div"));
  el.id = "dlg";
  el.setAttribute("data-modal", "true");
  el.setAttribute("data-dialog-root", "");
  el.setAttribute("data-testid", "x");
  el.innerHTML =
    '<form data-testid="inner"><input id="t" type="text"><input type="checkbox"><input type="radio">' +
    '<input type="file"><textarea></textarea><span data-modal="1">s</span></form>';
  document.body.appendChild(el);
  const [text, box, radio, , area] = el.querySelectorAll("input, textarea");
  text.value = "typed";
  box.checked = true;
  radio.checked = true;
  area.value = "notes";

  mock.timers.enable({ apis: ["setTimeout"] });
  try {
    m.leaveGhost(el, "pop", "sink");
    const ghost = document.querySelector(".motion-ghost");
    assert.ok(ghost);
    assert.ok(ghost.classList.contains("motion-pop-out"));
    assert.ok(ghost.firstElementChild.classList.contains("motion-sink-out"));
    assert.equal(ghost.getAttribute("aria-hidden"), "true");
    assert.equal(ghost.inert, true);
    assert.equal(ghost.id, "");
    assert.equal(
      ghost.querySelectorAll(
        "[id],[data-modal],[data-dialog-root],[data-testid]",
      ).length,
      0,
    );
    assert.equal(ghost.hasAttribute("data-modal"), false);
    assert.equal(ghost.style.position, "fixed");
    assert.equal(ghost.style.top, "10px");
    assert.equal(ghost.style.left, "20px");
    assert.equal(ghost.style.width, "300px");
    assert.equal(ghost.style.height, "200px");
    const [gt, gb, gr, gf, ga] = ghost.querySelectorAll("input, textarea");
    assert.equal(gt.value, "typed");
    assert.equal(gb.checked, true);
    assert.equal(gr.checked, true);
    assert.equal(gf.value, "");
    assert.equal(ga.value, "notes");

    // A child's animation ending does not remove the copy; its own does.
    ghost.firstElementChild.dispatchEvent(
      new Event("animationend", { bubbles: true }),
    );
    assert.ok(ghost.isConnected);
    ghost.dispatchEvent(new Event("animationend"));
    assert.equal(ghost.isConnected, false);
    // The fallback timer finds it already gone.
    mock.timers.tick(600);
    assert.equal(document.querySelectorAll(".motion-ghost").length, 0);
  } finally {
    mock.timers.reset();
  }
  el.remove();
});

test("leaveGhost removes the copy after the fallback when no animation ends", () => {
  const el = laidOut(document.createElement("div"));
  el.innerHTML = "<p>bye</p>";
  document.body.appendChild(el);
  mock.timers.enable({ apis: ["setTimeout"] });
  try {
    m.leaveGhost(el, "slide-left");
    const ghost = document.querySelector(".motion-ghost");
    assert.ok(ghost.classList.contains("motion-slide-out-left"));
    assert.equal(
      ghost.firstElementChild.className,
      "",
      "no child animation was asked for",
    );
    mock.timers.tick(599);
    assert.ok(ghost.isConnected);
    mock.timers.tick(1);
    assert.equal(ghost.isConnected, false);
    ghost.dispatchEvent(new Event("animationend"));
  } finally {
    mock.timers.reset();
  }
  el.remove();
});

test("useLeave copies the element as the component unmounts", async () => {
  function Toast() {
    const ref = useRef(null);
    m.useLeave(ref, "lift");
    return h(
      "div",
      {
        ref: (n) => {
          ref.current = n && laidOut(n);
        },
        class: "toast",
      },
      "saved",
    );
  }
  mock.timers.enable({ apis: ["setTimeout"] });
  try {
    const { done } = await mount(h(Toast));
    await done();
    const ghost = document.querySelector(".motion-ghost.motion-lift-out");
    assert.ok(ghost);
    assert.equal(ghost.textContent, "saved");
    mock.timers.tick(600);
    assert.equal(document.querySelector(".motion-ghost"), null);
  } finally {
    mock.timers.reset();
  }
});

// ── clipboard ─────────────────────────────────────────────────────────────────

function setClipboard(value) {
  Object.defineProperty(navigator, "clipboard", { value, configurable: true });
}

test("copyText uses the Clipboard API when the page has it", async () => {
  const written = [];
  setClipboard({ writeText: async (t) => written.push(t) });
  assert.equal(await m.copyText("hello"), true);
  assert.deepEqual(written, ["hello"]);
});

test("copyText falls back to the selection copy without the Clipboard API, or when it fails", async () => {
  const copies = [];
  document.execCommand = (cmd) => {
    const ta = document.querySelector("textarea[readonly]");
    copies.push([cmd, ta.value, ta.style.opacity]);
    return true;
  };
  setClipboard(undefined);
  assert.equal(await m.copyText("plain http"), true);
  setClipboard({
    writeText: async () => {
      throw new Error("denied");
    },
  });
  assert.equal(await m.copyText("denied"), true);
  assert.deepEqual(copies, [
    ["copy", "plain http", "0"],
    ["copy", "denied", "0"],
  ]);
  assert.equal(
    document.querySelector("textarea[readonly]"),
    null,
    "the helper is removed",
  );
});

test("copyText reports false when even the legacy copy fails", async () => {
  setClipboard({});
  document.execCommand = () => false;
  assert.equal(await m.copyText("x"), false);
  document.execCommand = () => {
    throw new Error("unsupported");
  };
  assert.equal(await m.copyText("x"), false);
  assert.equal(document.querySelector("textarea[readonly]"), null);
  delete document.execCommand;
});

// ── icons ─────────────────────────────────────────────────────────────────────

async function svgOf(Icon, props) {
  const { root, done } = await mount(h(Icon, props));
  const svg = root.querySelector("svg");
  const html = svg.outerHTML;
  const attrs = {
    width: svg.getAttribute("width"),
    stroke: svg.getAttribute("stroke-width"),
    cls: svg.getAttribute("class"),
    style: svg.getAttribute("style"),
    hidden: svg.getAttribute("aria-hidden"),
    shapes: svg.children.length,
  };
  await done();
  return { html, ...attrs };
}

test("every shared icon draws a 24-unit line drawing at the size asked for", async () => {
  const names = [
    "IconClose",
    "IconTrash",
    "IconEdit",
    "IconSpinner",
    "IconAlert",
    "IconDownload",
    "IconCheck",
    "IconFolder",
    "IconMonitor",
    "IconBook",
    "IconNotes",
    "IconClock",
    "IconDots",
  ];
  const drawn = new Set();
  for (const name of names) {
    const s = await svgOf(m[name], {
      size: 16,
      class: "ic",
      style: "color: red",
    });
    assert.equal(s.width, "16", name);
    assert.equal(s.cls, "ic", name);
    assert.match(s.style, /red/, name);
    assert.ok(s.shapes >= 1, name);
    assert.match(s.html, /viewBox="0 0 24 24"/);
    drawn.add(s.html.replace(/<svg[^>]*>/, ""));
  }
  assert.equal(drawn.size, names.length, "each icon is a different drawing");
});

test("icons take the default stroke unless one is given; the spinner and tick are bolder", async () => {
  assert.equal((await svgOf(m.IconClose, { size: 18 })).stroke, "2");
  assert.equal(
    (await svgOf(m.IconClose, { size: 18, stroke: 1.5 })).stroke,
    "1.5",
  );
  assert.equal((await svgOf(m.IconSpinner, { size: 18 })).stroke, "2.5");
  assert.equal(
    (await svgOf(m.IconSpinner, { size: 18, stroke: "3" })).stroke,
    "3",
  );
  assert.equal((await svgOf(m.IconCheck, { size: 18 })).stroke, "2.5");
  assert.equal((await svgOf(m.IconCheck, { size: 18, stroke: 1 })).stroke, "1");
});

test("the new-project folder is hidden from assistive technology only when asked", async () => {
  const shown = await svgOf(m.IconFolderPlus, { size: 20, class: "fp" });
  assert.equal(shown.hidden, null);
  assert.equal(shown.cls, "fp");
  assert.equal(shown.width, "20");
  assert.equal(shown.shapes, 3);
  assert.equal(
    (await svgOf(m.IconFolderPlus, { size: 20, hidden: true })).hidden,
    "true",
  );
});

test("the settings tab icons are decorative 18px drawings, each its own", async () => {
  const drawn = new Set();
  for (const name of [
    "IconBell",
    "IconMic",
    "IconSpeaker",
    "IconShield",
    "IconGlobe",
    "IconSliders",
    "IconChip",
    "IconPlug",
    "IconBranch",
  ]) {
    const s = await svgOf(m[name], {});
    assert.equal(s.width, "18", name);
    assert.equal(s.hidden, "true", name);
    assert.ok(s.shapes >= 1, name);
    drawn.add(s.html);
  }
  assert.equal(drawn.size, 9);
});

// ── provider logos ────────────────────────────────────────────────────────────

test("each known provider has a coloured logo path, and aliases share their brand's", () => {
  const L = m.PROVIDER_LOGOS;
  assert.deepEqual(Object.keys(L).sort(), [
    "anthropic",
    "claude-code",
    "codex",
    "copilot",
    "gemini",
    "ollama",
    "ollama-cloud",
    "openai",
    "qwen",
  ]);
  for (const [name, logo] of Object.entries(L)) {
    assert.match(logo.color, /^#[0-9a-f]{6}$/, name);
    assert.match(logo.d, /^[Mm]/, name);
  }
  assert.equal(L.codex.d, L.openai.d);
  assert.equal(L["ollama-cloud"].d, L.ollama.d);
  assert.notEqual(L.qwen.d, L.openai.d);
  assert.equal(L["claude-code"].color, "#d97757");
  assert.equal(
    L.unknown,
    undefined,
    "an unknown provider gets its initial instead",
  );
});
