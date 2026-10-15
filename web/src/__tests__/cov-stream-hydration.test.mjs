// When an answer's expensive parts get built: a message is built when it comes near the viewport
// (one shared IntersectionObserver), dropped again when it goes far away, and the page announces
// it is quiet once nothing happened for a second. Runs against a stand-in IntersectionObserver
// the test drives by hand.
import test, { mock } from "node:test";
import assert from "node:assert/strict";
import { installDom, source } from "./cov-stream-helpers.mjs";

installDom();

// The stand-in observers: each remembers its options and what it watches; see() reports records.
const observers = [];
class FakeIO {
  constructor(cb, options) {
    this.cb = cb;
    this.options = options;
    this.watched = new Set();
    observers.push(this);
  }
  observe(el) {
    this.watched.add(el);
  }
  unobserve(el) {
    this.watched.delete(el);
  }
  disconnect() {}
  see(...records) {
    this.cb(records.map(([target, isIntersecting]) => ({ target, isIntersecting })));
  }
}
globalThis.IntersectionObserver = FakeIO;

const h = await source("hydration.ts");
const near = () => observers.find((o) => o.options.rootMargin === "600px 0px");
const far = () => observers.find((o) => o.options.rootMargin === "4000px 0px");
const settle = async () => {
  for (let i = 0; i < 5; i++) {
    await Promise.resolve();
  }
};
const msg = () => {
  const el = document.createElement("div");
  document.body.appendChild(el);
  return el;
};

test("a message is built once it comes near the viewport, and only once", async () => {
  const el = msg();
  let built = 0;
  const stop = h.watchForHydration(el, () => {
    built++;
  });
  assert.equal(el.dataset.hydrating, "1");
  assert.equal(el.dataset.inview, "0");
  assert.equal(near().options.threshold, 0);
  assert.ok(near().watched.has(el));
  assert.equal(far(), undefined, "no far observer for work that cannot be undone");
  assert.deepEqual(h.hydrationState(), {
    registered: 1,
    pending: 1,
    visiblePending: false,
  });

  // A record for something not registered is ignored.
  near().see([document.createElement("p"), true]);
  assert.equal(built, 0);

  near().see([el, true]);
  assert.equal(el.dataset.inview, "1");
  assert.equal(built, 1);
  await settle();
  assert.equal(el.dataset.hydrating, undefined);
  assert.deepEqual(h.hydrationState(), {
    registered: 1,
    pending: 0,
    visiblePending: false,
  });
  near().see([el, false]);
  assert.equal(el.dataset.inview, "0");
  near().see([el, true]);
  assert.equal(built, 1, "built once");

  stop();
  assert.equal(near().watched.has(el), false);
  assert.equal(h.hydrationState().registered, 0);
  const log = h.hydrationLog();
  assert.deepEqual(
    log.map((t) => [t.idle, t.pending, t.reason]),
    [[true, 0, "register"]],
    "registering one message and finishing it changes nothing worth a row",
  );
  assert.ok(log.every((t) => typeof t.at === "number"));
  el.remove();
});

test("without IntersectionObserver for the far margin, a message is only built", async () => {
  const saved = globalThis.IntersectionObserver;
  delete globalThis.IntersectionObserver;
  try {
    // The near observer exists already; the far one cannot be made here, and is not needed.
    const el = msg();
    const stop = h.watchForHydration(el, () => {}, () => true);
    assert.equal(far(), undefined);
    near().see([el, true]);
    await settle();
    assert.equal(el.dataset.hydrating, undefined);
    stop();
    el.remove();
  } finally {
    globalThis.IntersectionObserver = saved;
    h.cancelQuiet();
  }
});

test("work still being built in view holds the page busy, and a failure still finishes", async () => {
  const el = msg();
  let fail;
  const stop = h.watchForHydration(
    el,
    () =>
      new Promise((_, reject) => {
        fail = reject;
      }),
  );
  near().see([el, true]);
  assert.equal(h.hydrationState().visiblePending, true);
  fail(new Error("diagram failed"));
  await settle();
  assert.equal(el.dataset.hydrating, undefined);
  assert.equal(h.hydrationState().visiblePending, false);
  stop();
  el.remove();
});

test("a built message far out of view drops its work and is built again on the way back", async () => {
  const el = msg();
  let built = 0;
  let heavy = true;
  const releases = [];
  const stop = h.watchForHydration(
    el,
    () => {
      built++;
    },
    () => {
      releases.push(heavy);
      return heavy;
    },
  );
  assert.ok(far(), "the far observer is created for work that can be undone");
  assert.ok(far().watched.has(el));

  // Not built yet: nothing to release.
  far().see([el, false]);
  assert.deepEqual(releases, []);
  near().see([el, true]);
  await settle();
  // Still in view: kept.
  far().see([el, false]);
  assert.deepEqual(releases, []);
  near().see([el, false]);
  // In the far margin: kept.
  far().see([el, true]);
  assert.deepEqual(releases, []);
  // Far away: released, at the height it had.
  el.getBoundingClientRect = () => ({ height: 321 });
  far().see([el, false]);
  assert.deepEqual(releases, [true]);
  assert.equal(el.style.minHeight, "321px");
  assert.equal(el.dataset.released, "1");
  assert.equal(el.dataset.hydrating, "1");
  // Coming back builds it again, and gives it its own height back.
  near().see([el, true]);
  assert.equal(built, 2);
  await settle();
  assert.equal(el.dataset.released, undefined);
  assert.equal(el.style.minHeight, "");
  // A release with nothing heavy to drop leaves the message as it is.
  near().see([el, false]);
  heavy = false;
  far().see([el, false]);
  assert.deepEqual(releases, [true, false]);
  assert.equal(el.dataset.released, undefined);
  assert.equal(el.dataset.hydrating, undefined);

  // releaseFar on an element nobody registered does nothing.
  const stray = msg();
  h.releaseFar(stray);
  assert.equal(stray.dataset.released, undefined);

  stop();
  assert.equal(far().watched.has(el), false);
  el.remove();
  stray.remove();
});

test("a released message registered again starts from its own height", async () => {
  const el = msg();
  el.dataset.released = "1";
  el.style.minHeight = "50px";
  const stop = h.watchForHydration(el, () => {});
  assert.equal(el.dataset.released, undefined);
  assert.equal(el.style.minHeight, "");
  stop();
  el.remove();
});

test("the page is announced quiet a second after the last activity", () => {
  mock.timers.enable({ apis: ["setTimeout"] });
  try {
    let quiet = 0;
    const on = () => quiet++;
    document.addEventListener("motita:chat-quiet", on);
    h.noteActivity();
    mock.timers.tick(900);
    h.noteActivity();
    mock.timers.tick(900);
    assert.equal(quiet, 0, "activity restarts the window");
    mock.timers.tick(100);
    assert.equal(quiet, 1);
    h.noteActivity();
    h.cancelQuiet();
    mock.timers.tick(2000);
    assert.equal(quiet, 1, "a cancelled window says nothing");
    h.cancelQuiet();
    document.removeEventListener("motita:chat-quiet", on);
  } finally {
    mock.timers.reset();
  }
});

test("the transition log keeps only changes, and its last sixty", () => {
  const els = [];
  const stops = [];
  for (let i = 0; i < 65; i++) {
    const el = msg();
    els.push(el);
    stops.push(h.watchForHydration(el, () => {}));
  }
  const log = h.hydrationLog();
  assert.equal(log.length, 60);
  // Each registration is logged with the messages that were there before it.
  assert.equal(log.at(-1).pending, 64);
  assert.equal(log.at(-1).registered, 64);
  assert.equal(log.at(-1).reason, "register");
  stops.forEach((s) => s());
  els.forEach((e) => e.remove());
  h.cancelQuiet();
});

