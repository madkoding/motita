// The small pieces of the page shell: the keyboard and backdrop helpers, the credential calls,
// the constants, the "+" button, the folded action counter and the drawer window.
// Run with: node --test web/src/__tests__/cov-shell-basics.test.mjs
import test, { mock } from "node:test";
import assert from "node:assert/strict";
import {
  setupDom,
  load,
  mounter,
  text,
  jsonResponse,
} from "./cov-shell-helpers.mjs";

setupDom("http://localhost/chat?x=1");

const m = {
  ...(await load("a11y.ts")),
  ...(await load("appAuth.ts")),
  ...(await load("NewSessionButton.ts")),
  ...(await load("ActionTrail.tsx")),
  ...(await load("drawerWindow.ts")),
  appConst: await load("appConst.ts"),
};
const { h } = await import("preact");
const { act } = await import("preact/test-utils");
const mount = await mounter();

// ── a11y ─────────────────────────────────────────────────────────────────────

test("onActivate runs the action on Enter and Space from the element itself only", () => {
  let runs = 0;
  const handler = m.onActivate(() => runs++);
  const el = {};
  const ev = (key, target = el) => {
    const e = { key, target, currentTarget: el, prevented: false };
    e.preventDefault = () => (e.prevented = true);
    return e;
  };
  const enter = ev("Enter");
  handler(enter);
  assert.equal(runs, 1);
  assert.equal(enter.prevented, true);
  const space = ev(" ");
  handler(space);
  assert.equal(runs, 2);
  const other = ev("a");
  handler(other);
  assert.equal(runs, 2, "another key does nothing");
  assert.equal(other.prevented, false);
  const inner = ev("Enter", {});
  handler(inner);
  assert.equal(runs, 2, "a key from a control inside keeps its own meaning");
  assert.equal(inner.prevented, false);
});

test("backdropClick closes on the backdrop and stops clicks from inside the dialog", () => {
  let closed = 0;
  const handler = m.backdropClick(() => closed++);
  const el = {};
  let stopped = 0;
  handler({ target: el, currentTarget: el, stopPropagation: () => stopped++ });
  assert.equal(closed, 1);
  assert.equal(stopped, 0);
  handler({ target: {}, currentTarget: el, stopPropagation: () => stopped++ });
  assert.equal(closed, 1, "a click inside does not close");
  assert.equal(stopped, 1, "and does not bubble further");
});

// ── appAuth ──────────────────────────────────────────────────────────────────

function fakeFetch(answer) {
  const calls = [];
  globalThis.fetch = async (url, init = {}) => {
    calls.push({ url, init });
    return answer(url, init);
  };
  return calls;
}

test("authGate asks the protected sessions endpoint and reports whether it answered ok", async () => {
  let calls = fakeFetch(() => jsonResponse([], 200));
  assert.equal(await m.authGate(), true);
  assert.equal(calls[0].url, "/v1/sessions");
  assert.equal(calls[0].init.credentials, "same-origin");
  fakeFetch(() => jsonResponse({}, 401));
  assert.equal(await m.authGate(), false);
  fakeFetch(() => {
    throw new TypeError("offline");
  });
  assert.equal(await m.authGate(), false, "a network failure is no credential");
});

test("clearCredential deletes the cookie and the remembered session, whatever fails", async () => {
  localStorage.setItem("motita:last-session", "s1");
  const calls = fakeFetch(() => new Response(null, { status: 204 }));
  await m.clearCredential();
  assert.equal(calls[0].url, "/v1/webui/session");
  assert.equal(calls[0].init.method, "DELETE");
  assert.equal(calls[0].init.headers["X-Motita"], "1");
  assert.equal(localStorage.getItem("motita:last-session"), null);

  localStorage.setItem("motita:last-session", "s2");
  fakeFetch(() => {
    throw new TypeError("offline");
  });
  const removeItem = Object.getPrototypeOf(localStorage).removeItem;
  let tried = 0;
  Object.getPrototypeOf(localStorage).removeItem = () => {
    tried++;
    throw new Error("storage blocked");
  };
  try {
    await m.clearCredential(); // neither failure escapes
  } finally {
    Object.getPrototypeOf(localStorage).removeItem = removeItem;
  }
  assert.equal(
    tried,
    1,
    "the storage was still asked after the request failed",
  );
});

test("submitToken trades the token as a bearer and reports the answer", async () => {
  const calls = fakeFetch(() => new Response(null, { status: 204 }));
  assert.equal(await m.submitToken("abc"), true);
  assert.equal(calls[0].init.method, "POST");
  assert.equal(calls[0].init.headers.Authorization, "Bearer abc");
  fakeFetch(() => new Response(null, { status: 401 }));
  assert.equal(await m.submitToken("bad"), false);
  fakeFetch(() => {
    throw new TypeError("offline");
  });
  assert.equal(await m.submitToken("x"), false);
});

test("exchange trades the #t= fragment and erases it, whatever the answer", async () => {
  history.replaceState(null, "", "/chat?x=1");
  let calls = fakeFetch(() => new Response(null, { status: 204 }));
  assert.equal(await m.exchange(), false, "no fragment, nothing to trade");
  assert.equal(calls.length, 0);

  history.replaceState(null, "", "/chat?x=1#t=");
  assert.equal(await m.exchange(), false, "an empty token is not traded");
  assert.equal(calls.length, 0);

  history.replaceState(null, "", "/chat?x=1#t=tok123");
  assert.equal(await m.exchange(), true);
  assert.equal(calls[0].init.headers.Authorization, "Bearer tok123");
  assert.equal(location.hash, "", "the token left the address");
  assert.equal(location.pathname + location.search, "/chat?x=1");

  history.replaceState(null, "", "/chat#t=rejected");
  fakeFetch(() => new Response(null, { status: 401 }));
  assert.equal(await m.exchange(), false);
  assert.equal(location.hash, "");

  history.replaceState(null, "", "/chat#t=offline");
  fakeFetch(() => {
    throw new TypeError("offline");
  });
  assert.equal(await m.exchange(), false);
  assert.equal(location.hash, "", "erased even when the request failed");
});

// ── appConst ─────────────────────────────────────────────────────────────────

test("the constants keep the endpoints and the timings the page relies on", () => {
  const c = m.appConst;
  assert.equal(c.SESSIONS_URL, "/v1/sessions");
  assert.equal(c.WEBUI_SESSION_URL, "/v1/webui/session");
  assert.ok(c.SESSIONS_PATH.startsWith(c.SESSIONS_URL));
  assert.deepEqual(c.HANDOFF_RELOAD_MS, [400, 1800]);
  assert.ok(c.TOAST_ERROR_MS > c.TOAST_WARNING_MS);
  assert.ok(c.TOAST_WARNING_MS > c.TOAST_UPDATE_MS);
  assert.ok(c.TOAST_UPDATE_MS > c.TOAST_MS);
  assert.equal(c.DAY_MS, c.DAY_S * 1000);
  assert.deepEqual(
    [c.APPROVAL_RUN, c.APPROVAL_ALL, c.APPROVAL_NO, c.APPROVAL_OPTIONS],
    [0, 1, 2, 3],
  );
  assert.deepEqual(c.REASONING_LEVELS, ["off", "low", "medium", "high"]);
  assert.equal(c.KB, 1024);
});

test("appTypes is declarations only: nothing of it exists at run time", async () => {
  // Loaded as TypeScript by node itself: its types are stripped and nothing is left to export.
  const types = await import("../appTypes.ts");
  assert.deepEqual(Object.keys(types), []);
});

// ── NewSessionButton ─────────────────────────────────────────────────────────

test("the full button shows a plus and its label, and a spinner while creating", async () => {
  let clicks = 0;
  const v = (loading) =>
    h(m.NewSessionButton, {
      loading,
      onClick: () => clicks++,
      label: "New session",
    });
  const { root, rerender, done } = await mount(v(false));
  const btn = root.querySelector("button");
  assert.equal(text(btn), "New session");
  assert.equal(btn.disabled, false);
  const svg = btn.querySelector("svg");
  assert.equal(svg.getAttribute("width"), "16");
  assert.equal(svg.getAttribute("stroke-width"), "2");
  btn.click();
  assert.equal(clicks, 1);
  await rerender(v(true));
  assert.equal(btn.disabled, true);
  assert.equal(btn.querySelector("svg"), null);
  assert.ok(btn.querySelector(".btn-spinner"));
  await done();
});

test("the compact button is icon-only, smaller and bolder, with its title", async () => {
  const v = (loading) =>
    h(m.NewSessionButton, {
      loading,
      onClick() {},
      compact: true,
      title: "New in project",
    });
  const { root, rerender, done } = await mount(v(false));
  const btn = root.querySelector("button");
  assert.ok(btn.classList.contains("project-new-session"));
  assert.equal(btn.title, "New in project");
  assert.equal(text(btn), "");
  const svg = btn.querySelector("svg");
  assert.equal(svg.getAttribute("width"), "12");
  assert.equal(svg.getAttribute("stroke-width"), "2.5");
  await rerender(v(true));
  assert.equal(btn.disabled, true);
  assert.ok(btn.querySelector(".btn-spinner"));
  await done();
});

// ── ActionTrail ──────────────────────────────────────────────────────────────

test("the action trail is a folded counter that opens and closes its detail", async () => {
  assert.equal(m.actionLabel(1), "1 action");
  assert.equal(m.actionLabel(4), "4 actions");
  const { root, done } = await mount(
    h(m.ActionTrail, { count: 2 }, h("span", { class: "child" }, "ran ls")),
  );
  const toggle = root.querySelector(".action-trail-toggle");
  assert.equal(text(toggle), "▸2 actions");
  assert.equal(toggle.getAttribute("aria-expanded"), "false");
  assert.equal(root.querySelector(".activity-trail"), null);
  await act(() => toggle.click());
  assert.equal(toggle.getAttribute("aria-expanded"), "true");
  assert.equal(text(root.querySelector(".action-trail-caret")), "▾");
  assert.equal(text(root.querySelector(".activity-trail")), "ran ls");
  assert.equal(
    root.querySelector(".activity-trail").getAttribute("aria-label"),
    "steps",
  );
  await act(() => toggle.click());
  assert.equal(root.querySelector(".activity-trail"), null);
  await done();
});

// ── drawerWindow ─────────────────────────────────────────────────────────────

test("windowStart is the first index shown, never below zero", () => {
  assert.equal(m.windowStart(120, 50), 70);
  assert.equal(m.windowStart(10, 50), 0);
  assert.equal(m.DRAWER_WINDOW, 50);
});

test("a drawer mounts when it opens, grows on demand, and empties after the slide out", async () => {
  mock.timers.enable({ apis: ["setTimeout"] });
  try {
    let seen;
    function Probe({ open }) {
      seen = m.useDrawerContent(open);
      return null;
    }
    const { rerender, done } = await mount(h(Probe, { open: false }));
    assert.equal(seen.mounted, false, "a closed drawer mounts nothing");
    assert.equal(seen.shown, 50);
    await rerender(h(Probe, { open: true }));
    assert.equal(seen.mounted, true);
    await act(() => seen.showEarlier());
    assert.equal(seen.shown, 100);
    await rerender(h(Probe, { open: false }));
    assert.equal(seen.mounted, true, "still there while the panel slides out");
    await act(() => mock.timers.tick(m.DRAWER_LINGER_MS - 1));
    assert.equal(seen.mounted, true);
    await act(() => mock.timers.tick(1));
    assert.equal(seen.mounted, false);
    assert.equal(seen.shown, 50, "the window is back to its size");
    // Reopened before the linger ends: the timer is cancelled and the content stays.
    await rerender(h(Probe, { open: true }));
    await rerender(h(Probe, { open: false }));
    await rerender(h(Probe, { open: true }));
    await act(() => mock.timers.tick(m.DRAWER_LINGER_MS * 2));
    assert.equal(seen.mounted, true);
    await done();
  } finally {
    mock.timers.reset();
  }
});
