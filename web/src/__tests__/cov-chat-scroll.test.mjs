// The conversation's scroll: it follows the end while the reader is there, lets go the moment they
// move up on their own, takes the end again when they come back, and keeps the loading card up
// until nothing visible is still being built. The drawers' follower (useFollowEnd) too.
// Run with: node --test web/src/__tests__/cov-chat-scroll.test.mjs
import test, { mock } from "node:test";
import assert from "node:assert/strict";
import {
  act,
  copies,
  frames,
  h,
  mount,
  renderHook,
  tick,
} from "./cov-chat-helpers.mjs";

const forms = await copies("scroll", [
  ["useChatScroll, observeChildren", "useChatScroll.ts"],
  ["useFollowEnd", "useFollowEnd.ts"],
  ["cancelQuiet", "hydration.ts"],
]);

// The clock every scroll rule reads.
let clock = 10_000;
mock.method(performance, "now", () => clock);

// A ResizeObserver that records what it holds and fires on demand.
const observers = [];
class FakeResizeObserver {
  constructor(cb) {
    this.cb = cb;
    this.targets = new Set();
    this.disconnected = false;
    observers.push(this);
  }
  observe(el) {
    this.targets.add(el);
  }
  unobserve(el) {
    this.targets.delete(el);
  }
  disconnect() {
    this.disconnected = true;
    this.targets.clear();
  }
  fire() {
    this.cb([]);
  }
}
globalThis.ResizeObserver = FakeResizeObserver;

// scrollable gives an element a content height, a view height and a clamped scrollTop.
function scrollable(el, size) {
  if (el.__size) {
    return;
  }
  el.__size = size;
  let top = 0;
  const max = () => Math.max(0, size.height - size.client);
  Object.defineProperty(el, "scrollHeight", {
    get: () => size.height,
    configurable: true,
  });
  Object.defineProperty(el, "clientHeight", {
    get: () => size.client,
    configurable: true,
  });
  Object.defineProperty(el, "scrollTop", {
    get: () => top,
    set: (v) => {
      top = Math.min(max(), Math.max(0, v));
    },
    configurable: true,
  });
  el.scrollTo = ({ top: to }) => {
    el.scrolledTo = (el.scrolledTo ?? 0) + 1;
    el.scrollTop = to;
  };
}

const view = (messages, extra = {}) => ({
  messages,
  activity: null,
  liveThought: null,
  stepsByTurn: {},
  ...extra,
});

for (const M of forms) {
  // Chat draws the conversation box with the hook's ref, its messages inside.
  function Chat({ v, size, attach = true, out, hydrating = false }) {
    const s = M.useChatScroll(v);
    out.current = s;
    const ref = (node) => {
      if (node) {
        scrollable(node, size);
      }
      s.scrollRef.current = node;
    };
    return h(
      "div",
      { ref: attach ? ref : undefined, "data-testid": "box" },
      v.messages.map((m) =>
        h(
          "p",
          { key: m.id, "data-hydrating": hydrating ? "" : undefined },
          m.text,
        ),
      ),
    );
  }

  async function chat(messages, opts = {}) {
    const size = { height: 2000, client: 500, ...opts.size };
    const out = { current: null };
    const props = { v: view(messages), size, out, ...opts };
    const m = await mount(h(Chat, props));
    const box = m.root.querySelector("[data-testid=box]");
    return {
      box,
      size,
      out,
      async set(messages2, extra = {}) {
        await m.update(h(Chat, { ...props, ...extra, v: view(messages2) }));
      },
      async unmount() {
        await m.unmount();
        M.cancelQuiet();
      },
    };
  }

  const msgs = (n) =>
    Array.from({ length: n }, (_, i) => ({
      id: i + 1,
      role: "agent",
      text: "m" + i,
    }));
  const glide = () => {
    let guard = 0;
    while (frames.pending && guard++ < 400) {
      clock += 16;
      frames.flush(clock);
    }
  };

  test(`the conversation glides to the end as it grows, while the reader is there`, async () => {
    frames.clear();
    const c = await chat(msgs(2));
    glide();
    assert.equal(c.box.scrollTop, 1500, "at the end");
    c.size.height = 3000;
    await c.set(msgs(3));
    assert.ok(frames.pending > 0, "a new message kicks the follower");
    glide();
    assert.equal(c.box.scrollTop, 2500);
    await c.unmount();
  });

  test(`a wheel up lets go of the end, and new messages leave the reader where they are`, async () => {
    frames.clear();
    const c = await chat(msgs(2));
    glide();
    clock += 5000;
    c.box.dispatchEvent(new window.WheelEvent("wheel", { deltaY: -40 }));
    c.size.height = 4000;
    await c.set(msgs(3));
    glide();
    assert.equal(c.box.scrollTop, 1500, "not dragged down");
    // A scroll that lands exactly on the end takes it again, and the view follows once more.
    c.box.scrollTop = 3500;
    c.box.dispatchEvent(new window.Event("scroll"));
    c.size.height = 4500;
    await c.set(msgs(4));
    glide();
    assert.equal(c.box.scrollTop, 4000);
    // A wheel down keeps following.
    c.box.dispatchEvent(new window.WheelEvent("wheel", { deltaY: 40 }));
    c.size.height = 4600;
    await c.set(msgs(5));
    glide();
    assert.equal(c.box.scrollTop, 4100);
    await c.unmount();
  });

  test(`keys: the up keys let go, the others do not`, async () => {
    frames.clear();
    const c = await chat(msgs(2));
    glide();
    clock += 5000;
    c.box.dispatchEvent(
      new window.KeyboardEvent("keydown", { key: "PageDown" }),
    );
    c.size.height = 2500;
    await c.set(msgs(3));
    glide();
    assert.equal(c.box.scrollTop, 2000, "PageDown keeps following");
    c.box.dispatchEvent(new window.KeyboardEvent("keydown", { key: "Home" }));
    c.size.height = 3000;
    await c.set(msgs(4));
    glide();
    assert.equal(c.box.scrollTop, 2000, "Home lets go");
    await c.unmount();
  });

  test(`a drag of the bar or a finger moving up lets go; letting go of it ends the hold`, async () => {
    frames.clear();
    const c = await chat(msgs(2));
    glide();
    // A press on a message is not a drag of the bar.
    clock += 5000;
    c.box
      .querySelector("p")
      .dispatchEvent(new window.PointerEvent("pointerdown", { bubbles: true }));
    c.box.scrollTop = 1000;
    c.box.dispatchEvent(new window.Event("scroll"));
    c.size.height = 2100;
    await c.set(msgs(3));
    glide();
    assert.equal(
      c.box.scrollTop,
      1600,
      "content moving the view is not the reader: still pinned",
    );
    c.box.dispatchEvent(new window.Event("scroll"));
    // A press on the box itself is: held, a move up lets go.
    clock += 5000;
    c.box.dispatchEvent(
      new window.PointerEvent("pointerdown", { bubbles: true }),
    );
    clock += 5000;
    c.box.scrollTop = 1200;
    c.box.dispatchEvent(new window.Event("scroll"));
    c.size.height = 2400;
    await c.set(msgs(4));
    glide();
    assert.equal(c.box.scrollTop, 1200, "the held drag up let go of the end");
    window.dispatchEvent(new window.PointerEvent("pointerup"));
    window.dispatchEvent(new window.PointerEvent("pointerup"));
    // A finger: touchstart holds, a move down near the end takes the end back.
    clock += 5000;
    c.box.dispatchEvent(new window.TouchEvent("touchstart"));
    c.box.scrollTop = 1850;
    c.box.dispatchEvent(new window.Event("scroll"));
    window.dispatchEvent(new window.TouchEvent("touchend"));
    c.size.height = 2600;
    await c.set(msgs(5));
    glide();
    assert.equal(c.box.scrollTop, 2100, "followed again");
    await c.unmount();
  });

  test(`a quiet window anchors at the end when pinned, twice, and not when the reader left`, async () => {
    frames.clear();
    const c = await chat(msgs(2));
    frames.clear();
    document.dispatchEvent(new window.CustomEvent("motita:chat-quiet"));
    assert.equal(c.box.scrollTop, 1500);
    const before = c.box.scrolledTo;
    c.size.height = 2200;
    frames.flush((clock += 16));
    assert.equal(
      c.box.scrollTop,
      1700,
      "the second pass lands on what grew since",
    );
    assert.ok(c.box.scrolledTo > before);
    // Unpinned: nothing moves, now or on the next frame.
    clock += 5000;
    c.box.dispatchEvent(new window.WheelEvent("wheel", { deltaY: -40 }));
    c.size.height = 3000;
    document.dispatchEvent(new window.CustomEvent("motita:chat-quiet"));
    frames.flush((clock += 16));
    assert.equal(c.box.scrollTop, 1700);
    // Pinned at the quiet, but the reader leaves before the next frame.
    c.box.scrollTop = 2500;
    c.box.dispatchEvent(new window.Event("scroll"));
    document.dispatchEvent(new window.CustomEvent("motita:chat-quiet"));
    c.box.dispatchEvent(new window.WheelEvent("wheel", { deltaY: -40 }));
    c.size.height = 3300;
    frames.flush((clock += 16));
    assert.equal(
      c.box.scrollTop,
      2500,
      "the second pass sees the reader has left",
    );
    await c.unmount();
  });

  test(`the gate's log reads the hydration state`, async () => {
    const c = await chat(msgs(1));
    const log = window.__chatLoadingLog();
    assert.deepEqual(Object.keys(log).sort(), [
      "registered",
      "transitions",
      "visiblePending",
    ]);
    assert.equal(log.visiblePending, false);
    assert.ok(Array.isArray(window.__modalLog), "the modal log is kept");
    assert.ok(window.__modalLog.length > 0, "and has entries");
    await c.unmount();
  });

  test(`the loading card stays up while a message is hydrating and leaves with a fade`, async () => {
    frames.clear();
    const c = await chat(msgs(2), { hydrating: true });
    await act(() => c.out.current.requestModal(true));
    assert.equal(c.out.current.chatLoading, true);
    assert.equal(window.__modalLog.at(-1).loading, true);
    // A message still hydrating keeps it up.
    frames.flush((clock += 16));
    assert.equal(c.out.current.chatLoading, true);
    // While loading, growth jumps to the end instead of gliding.
    c.size.height = 2600;
    await c.set(msgs(3), { hydrating: true });
    assert.equal(c.box.scrollTop, 2100, "jumped at once");
    // Once nothing hydrates, the next frame closes it: the exit class two frames on, then gone.
    await c.set(msgs(3), { hydrating: false });
    await act(() => frames.flush((clock += 16)));
    assert.equal(
      c.out.current.modalLeaving,
      false,
      "the first frame mounts the scrim as it is",
    );
    await act(() => frames.flush((clock += 16)));
    await act(() => frames.flush((clock += 16)));
    assert.equal(c.out.current.modalLeaving, true);
    assert.equal(c.out.current.chatLoading, true, "held during the fade");
    await act(() => tick(260));
    assert.equal(c.out.current.chatLoading, false);
    assert.equal(c.out.current.modalLeaving, false);
    // Closing what is not open does nothing.
    await act(() => c.out.current.requestModal(false));
    assert.equal(frames.pending, 0);
    await c.unmount();
  });

  test(`reopening during the fade cancels it, and an empty conversation does not close it`, async () => {
    frames.clear();
    const c = await chat([]);
    await act(() => c.out.current.requestModal(true));
    frames.flush((clock += 16));
    assert.equal(
      c.out.current.chatLoading,
      true,
      "nothing to wait for is not the end of loading",
    );
    await act(() => c.out.current.requestModal(false));
    assert.ok(frames.pending > 0);
    // Back up before the exit frames ran: their frame is cancelled and so is the timer.
    await act(() => c.out.current.requestModal(true));
    assert.equal(frames.pending, 0);
    await act(() => tick(260));
    assert.equal(c.out.current.chatLoading, true);
    // A close followed by unmount clears the pending timer and frame.
    await act(() => c.out.current.requestModal(false));
    const cancelledBefore = frames.cancelled.length;
    await c.unmount();
    assert.ok(frames.cancelled.length > cancelledBefore);
  });

  test(`a message finishing its formulas keeps the view at the end, unless the reader left`, async () => {
    frames.clear();
    const c = await chat(msgs(2));
    frames.clear();
    const p = c.box.querySelector("p");
    c.size.height = 2400;
    p.dispatchEvent(
      new window.CustomEvent("motita:hydrated", { bubbles: true }),
    );
    assert.equal(c.box.scrollTop, 1900);
    c.size.height = 2450;
    p.dispatchEvent(
      new window.CustomEvent("motita:hydrated", { bubbles: true }),
    );
    c.size.height = 2500;
    frames.flush((clock += 16));
    assert.equal(c.box.scrollTop, 2000, "the frame after catches late layout");
    clock += 5000;
    c.box.dispatchEvent(new window.WheelEvent("wheel", { deltaY: -40 }));
    c.size.height = 2900;
    p.dispatchEvent(
      new window.CustomEvent("motita:hydrated", { bubbles: true }),
    );
    frames.flush((clock += 16));
    assert.equal(c.box.scrollTop, 2000);
    await c.unmount();
  });

  test(`growth no event announces is followed through the resize observer`, async () => {
    frames.clear();
    observers.length = 0;
    const c = await chat(msgs(2));
    glide();
    const ro = observers.find((o) => o.targets.has(c.box));
    assert.ok(ro, "the box is observed");
    assert.equal(ro.targets.size, 3, "and each message in it");
    c.size.height = 2300;
    ro.fire();
    ro.fire();
    glide();
    assert.equal(c.box.scrollTop, 1800);
    // Unpinned between the resize and its frame: no follow.
    c.size.height = 2600;
    ro.fire();
    clock += 5000;
    c.box.dispatchEvent(new window.WheelEvent("wheel", { deltaY: -40 }));
    glide();
    assert.equal(c.box.scrollTop, 1800);
    // Unpinned at the resize: nothing is even scheduled.
    ro.fire();
    assert.equal(frames.pending, 0);
    await c.unmount();
    assert.equal(ro.disconnected, true);
  });

  test(`without a box or a ResizeObserver nothing is wired`, async () => {
    frames.clear();
    const c = await chat(msgs(1), { attach: false });
    document.dispatchEvent(new window.CustomEvent("motita:chat-quiet"));
    frames.flush((clock += 16));
    assert.equal(c.box.scrollTop, 0);
    await c.unmount();
    const saved = globalThis.ResizeObserver;
    globalThis.ResizeObserver = undefined;
    observers.length = 0;
    const d = await chat(msgs(1));
    assert.equal(observers.length, 0);
    await d.unmount();
    globalThis.ResizeObserver = saved;
  });

  test(`observeChildren observes what mounts and releases what leaves`, async () => {
    const box = document.createElement("main");
    const a = document.createElement("div");
    box.append(a, document.createTextNode("text"));
    const held = new Set();
    const ro = {
      observe: (el) => held.add(el),
      unobserve: (el) => held.delete(el),
    };
    const mo = M.observeChildren(box, ro);
    assert.deepEqual([...held], [a]);
    const b = document.createElement("div");
    box.append(b, document.createTextNode("more"));
    await tick();
    assert.deepEqual([...held], [a, b]);
    // Moving an element already observed does not observe it twice; text leaving is ignored.
    box.firstChild.nextSibling.remove();
    box.removeChild(a);
    await tick();
    assert.deepEqual([...held], [b]);
    box.append(a);
    box.append(b);
    await tick();
    assert.deepEqual(new Set(held), new Set([a, b]));
    mo.disconnect();
  });

  // ── The drawers' follower ─────────────────────────────────────────────────────

  function Drawer({ open, attach = true, out, size }) {
    const f = M.useFollowEnd(open, 100);
    out.current = f;
    const ref = (node) => {
      if (node) {
        scrollable(node, size);
      }
      f.ref.current = node;
    };
    return h(
      "div",
      { ref: attach ? ref : undefined, ...f.handlers, "data-testid": "drawer" },
      h("span", {}, "x"),
    );
  }

  async function drawer(open, attach = true) {
    const size = { height: 1000, client: 200 };
    const out = { current: null };
    const m = await mount(h(Drawer, { open, attach, out, size }));
    const el = m.root.querySelector("[data-testid=drawer]");
    return {
      el,
      size,
      out,
      reopen: (o) => m.update(h(Drawer, { open: o, attach, out, size })),
      unmount: () => m.unmount(),
    };
  }

  test(`a drawer opens on its end and follows what is written into it`, async () => {
    frames.clear();
    const d = await drawer(true);
    assert.equal(d.el.scrollTop, 800, "opened on the end");
    d.size.height = 1300;
    d.el.append(document.createElement("div"));
    await tick();
    glide();
    assert.equal(d.el.scrollTop, 1100, "the new line was followed");
    d.size.height = 1400;
    d.out.current.stick();
    glide();
    assert.equal(d.el.scrollTop, 1200);
    await d.unmount();
  });

  test(`the reader taking the drawer's scroll stops the follow; the end takes it back`, async () => {
    frames.clear();
    const d = await drawer(false);
    assert.equal(d.el.scrollTop, 0, "closed: not moved");
    const fire = (type) =>
      d.el.dispatchEvent(new window.Event(type, { bubbles: true }));
    // The wheel marks the reader; a scroll far from the end lets go and stops a glide.
    d.out.current.stick();
    fire("wheel");
    d.el.scrollTop = 300;
    fire("scroll");
    glide();
    assert.equal(d.el.scrollTop, 300);
    // Near the end, the reader takes it again.
    fire("touchstart");
    d.el.scrollTop = 790;
    fire("scroll");
    d.size.height = 1100;
    d.out.current.stick();
    glide();
    assert.equal(d.el.scrollTop, 900);
    // A drag of the bar counts as the reader for as long as it is held.
    fire("pointerdown");
    clock += 5000;
    d.el.scrollTop = 100;
    fire("scroll");
    window.dispatchEvent(new window.Event("pointerup"));
    d.out.current.stick();
    glide();
    assert.equal(d.el.scrollTop, 100, "let go while held");
    // Not the reader: growth far from the end changes nothing; landing on the end pins.
    clock += 5000;
    fire("scroll");
    d.out.current.stick();
    glide();
    assert.equal(d.el.scrollTop, 100);
    fire("touchmove");
    clock += 5000;
    d.el.scrollTop = 899;
    fire("scroll");
    d.size.height = 1200;
    d.out.current.stick();
    glide();
    assert.equal(d.el.scrollTop, 1000);
    window.dispatchEvent(new window.Event("pointercancel"));
    // Opening again lands on the end.
    d.el.scrollTop = 0;
    await d.reopen(true);
    assert.equal(d.el.scrollTop, 1000);
    await d.unmount();
  });

  test(`a drawer with no box, or no MutationObserver, wires nothing`, async () => {
    frames.clear();
    const d = await drawer(true, false);
    d.out.current.handlers.onScroll();
    d.out.current.stick();
    glide();
    assert.equal(d.el.scrollTop, 0);
    await d.unmount();
    const saved = globalThis.MutationObserver;
    globalThis.MutationObserver = undefined;
    const e = await drawer(false);
    e.el.append(document.createElement("div"));
    await tick();
    assert.equal(frames.pending, 0, "no observer: nothing asks to follow");
    await e.unmount();
    globalThis.MutationObserver = saved;
  });
}
