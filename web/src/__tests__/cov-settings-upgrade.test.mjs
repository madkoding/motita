// The new-version check and the upgrade modal, driven through the real useUpdates hook: the toast
// that offers a version, the confirm view, the progress stream with each of its stages, the
// failure details, and the wait for the restarted gateway (with mocked timers).
// Run with: node --test web/src/__tests__/cov-settings-upgrade.test.mjs
import test, { mock } from "node:test";
import assert from "node:assert/strict";
import { VirtualConsole } from "jsdom";
import {
  installDom,
  load,
  fakeApi,
  json,
  text,
  flush,
  settle,
  click,
  byText,
} from "./cov-settings-helpers.mjs";

// jsdom reports a page reload as a navigation it does not implement: that report is how the tests
// see the page reloading.
const reloads = [];
const vc = new VirtualConsole();
vc.on("jsdomError", (e) => {
  if (/navigation/i.test(e.message)) {
    reloads.push(e.message);
  }
});
installDom("http://localhost/", { virtualConsole: vc });

const { useUpdates } = await load("useUpdates.ts");
const { UpgradeModal } = await load("UpgradeModal.tsx");
const { AppCtx } = await load("appStore.ts");
const { h, render } = await import("preact");

const DISMISS_KEY = "motita:upgrade-dismissed";
const INFO = {
  current_version: "1.0.0",
  latest_version: "2.0.0",
  update_available: true,
  release_url: "https://example.com/r/2.0.0",
  release_name: "Big release",
};

// A response whose body is a stream the test feeds frame by frame.
function streamResponse() {
  let ctl;
  const body = new ReadableStream({
    start(c) {
      ctl = c;
    },
  });
  const enc = new TextEncoder();
  return {
    res: new Response(body, { status: 200 }),
    send(evt) {
      ctl.enqueue(enc.encode("data: " + JSON.stringify(evt) + "\n\n"));
    },
    raw(s) {
      ctl.enqueue(enc.encode(s));
    },
    end() {
      ctl.close();
    },
    fail(e) {
      ctl.error(e);
    },
  };
}

// mount renders the hook with the modal under the app context, and records the toasts.
function mount(api, authOk = true) {
  const toasts = [];
  const setToast = (x) => toasts.push(x);
  const ref = {};
  function Harness() {
    const updates = useUpdates(api, setToast, authOk);
    ref.u = updates;
    return h(
      AppCtx.Provider,
      { value: { updates } },
      updates.showUpgrade ? h(UpgradeModal, null) : null,
    );
  }
  const root = document.createElement("div");
  document.body.appendChild(root);
  render(h(Harness, null), root);
  return {
    root,
    toasts,
    ref,
    // unmount takes the modal down and lets the hook's cleanups run.
    async unmount() {
      await settle();
      render(null, root);
      root.remove();
      await settle();
    },
  };
}

const button = (root, label) => byText(root, "button", label);

test("the check runs only once signed in, and offers a version not dismissed before", async () => {
  localStorage.clear();
  const f = fakeApi({ "GET /v1/update/check": json(200, INFO) });
  const off = mount(f.api, false);
  await settle();
  assert.equal(f.calls.length, 0);
  await off.unmount();

  const m = mount(f.api);
  await settle();
  assert.deepEqual(
    f.calls.map((c) => c.path),
    ["/v1/update/check"],
  );
  assert.equal(m.toasts.length, 1);
  assert.equal(
    m.toasts[0].message,
    "New version 2.0.0 available — click to upgrade",
  );
  assert.equal(m.toasts[0].type, "success");
  assert.equal(m.toasts[0].update, true);
  assert.equal(m.ref.u.updateInfo.latest_version, "2.0.0");

  // Dismissing remembers the version and takes the toast down.
  m.ref.u.dismissUpgradeToast();
  assert.equal(localStorage.getItem(DISMISS_KEY), "2.0.0");
  assert.equal(m.toasts.at(-1), null);
  await m.unmount();

  // The same version is not offered again.
  const again = mount(f.api);
  await settle();
  assert.equal(again.toasts.length, 0);
  assert.equal(again.ref.u.updateInfo.current_version, "1.0.0");
  await again.unmount();
});

test("no toast for an up-to-date gateway, a refusal, a network failure or unreadable storage", async () => {
  localStorage.clear();
  for (const [route, stored] of [
    [json(200, { ...INFO, update_available: false }), "2.0.0"],
    [json(200, { ...INFO, latest_version: "" }), null],
    [json(500, { error: "x" }), null],
    [new Error("offline"), null],
  ]) {
    localStorage.clear();
    const f = fakeApi({ "GET /v1/update/check": route });
    const m = mount(f.api);
    await settle();
    assert.equal(f.calls.length, 1);
    assert.equal(m.toasts.length, 0);
    // Dismissing remembers the latest version known, if any, and takes the toast down.
    m.ref.u.dismissUpgradeToast();
    assert.deepEqual(m.toasts, [null]);
    assert.equal(localStorage.getItem(DISMISS_KEY), stored);
    await m.unmount();
  }

  const real = globalThis.localStorage;
  Object.defineProperty(globalThis, "localStorage", {
    configurable: true,
    value: {
      getItem() {
        throw new Error("denied");
      },
      setItem() {
        throw new Error("denied");
      },
    },
  });
  try {
    const f = fakeApi({ "GET /v1/update/check": json(200, INFO) });
    const m = mount(f.api);
    await settle();
    assert.equal(m.toasts.length, 0);
    // A dismissal that cannot be stored still takes the toast down.
    m.ref.u.dismissUpgradeToast();
    assert.deepEqual(m.toasts, [null]);
    await m.unmount();
  } finally {
    Object.defineProperty(globalThis, "localStorage", {
      configurable: true,
      value: real,
    });
  }
});

test("the check repeats every ten minutes and stops when unmounted", async () => {
  localStorage.clear();
  mock.timers.enable({ apis: ["setInterval"] });
  try {
    const f = fakeApi({
      "GET /v1/update/check": json(200, { ...INFO, update_available: false }),
    });
    const m = mount(f.api);
    await settle();
    assert.equal(f.calls.length, 1);
    mock.timers.tick(10 * 60 * 1000);
    await settle();
    assert.equal(f.calls.length, 2);
    await m.unmount();
    mock.timers.tick(10 * 60 * 1000);
    await settle();
    assert.equal(f.calls.length, 2);
  } finally {
    mock.timers.reset();
  }
});

test("opening the modal takes down only the update toast, and shows the versions to confirm", async () => {
  localStorage.clear();
  const f = fakeApi({ "GET /v1/update/check": json(200, INFO) });
  const m = mount(f.api);
  await settle();
  m.ref.u.openUpgrade();
  await settle();
  const updater = m.toasts.at(-1);
  assert.equal(typeof updater, "function");
  assert.equal(updater({ message: "new", update: true }), null);
  const other = { message: "saved" };
  assert.equal(updater(other), other);
  assert.equal(updater(null), null);

  const t = text(m.root);
  assert.match(t, /Upgrade Motita/);
  assert.match(t, /Current version\s*1\.0\.0/);
  assert.match(t, /Latest version\s*2\.0\.0/);
  assert.match(t, /Big release/);
  const notes = byText(m.root, "a", "View release notes");
  assert.equal(notes.getAttribute("href"), INFO.release_url);

  // Cancel closes it.
  click(button(m.root, "Cancel"));
  await settle();
  assert.equal(m.ref.u.showUpgrade, false);
  assert.equal(text(m.root), "");

  // The close button and a click on the backdrop close it too while nothing runs.
  m.ref.u.openUpgrade();
  await settle();
  click(m.root.querySelector("button[aria-label=Close]"));
  await settle();
  assert.equal(m.ref.u.showUpgrade, false);
  m.ref.u.openUpgrade();
  await settle();
  click(m.root.querySelector("[data-modal]"));
  await settle();
  assert.equal(m.ref.u.showUpgrade, false);
  await m.unmount();
});

test("the confirm view says unknown when the versions are not known", async () => {
  const f = fakeApi({ "GET /v1/update/check": json(500, {}) });
  const m = mount(f.api);
  await settle();
  m.ref.u.openUpgrade();
  await settle();
  const t = text(m.root);
  assert.match(t, /Current version\s*unknown/);
  assert.match(t, /Latest version\s*unknown/);
  assert.equal(byText(m.root, "a", "View release notes"), undefined);
  assert.doesNotMatch(t, /Big release/);
  await m.unmount();
});

test("a refused start shows the gateway's reason, or a generic one", async () => {
  for (const [res, want] of [
    [json(409, { error: "already running" }), "already running"],
    [new Response("nope", { status: 500 }), "could not start the upgrade"],
  ]) {
    const f = fakeApi({
      "GET /v1/update/check": json(200, INFO),
      "POST /v1/update/run": res,
    });
    const m = mount(f.api);
    await settle();
    m.ref.u.openUpgrade();
    await settle();
    click(button(m.root, "Download & Install"));
    await settle();
    assert.deepEqual(f.calls.at(-1), {
      method: "POST",
      path: "/v1/update/run",
      body: undefined,
    });
    assert.equal(m.ref.u.upgradeBusy, false);
    assert.match(text(m.root), /The upgrade did not finish/);
    // The raw reason is folded behind Details.
    assert.equal(m.root.querySelector("pre"), null);
    click(button(m.root, "Details"));
    await settle();
    assert.equal(text(m.root.querySelector("pre")), want);
    click(button(m.root, "Hide details"));
    await settle();
    assert.equal(m.root.querySelector("pre"), null);
    await m.unmount();
  }
});

test("the progress stream moves the bar, ignores noise, and ends without a restart", async () => {
  const s = streamResponse();
  const f = fakeApi({
    "GET /v1/update/check": json(200, INFO),
    "POST /v1/update/run": () => s.res,
  });
  const m = mount(f.api);
  await settle();
  m.ref.u.openUpgrade();
  await settle();
  click(button(m.root, "Download & Install"));
  await settle();
  // Starting: the modal cannot be closed while it runs.
  assert.match(text(m.root), /Starting upgrade…/);
  assert.match(text(m.root), /0%/);
  const close = m.root.querySelector("button[aria-label=Close]");
  assert.equal(close.disabled, true);
  click(m.root.querySelector("[data-modal]"));
  await settle();
  assert.equal(m.ref.u.showUpgrade, true);

  s.send({
    stage: "downloading",
    percent: 40,
    message: "Downloaded 40%",
    version: "2.0.0",
  });
  await settle();
  assert.match(text(m.root), /40%/);
  assert.match(text(m.root), /→ 2\.0\.0/);
  assert.match(text(m.root), /Downloaded 40%/);
  const bar = m.root.querySelector("[style*='width']");
  assert.match(bar.getAttribute("style"), /width: 40%/);
  assert.match(bar.getAttribute("style"), /box-shadow: 0 0 12px/);

  // A frame split across chunks, one with no data line, and one that is not JSON are ignored.
  s.raw("event: ping\n\n");
  s.raw("data: {not json\n\n");
  s.raw('data: {"stage":"installing","mess');
  s.raw('age":"Installed 2.0.0"}\n\n');
  await settle();
  assert.match(text(m.root), /Installed 2\.0\.0/);
  assert.match(text(m.root), /0%/);
  assert.doesNotMatch(text(m.root), /→/);

  s.send({ stage: "done", percent: 100, message: "Already up to date" });
  s.end();
  await settle();
  assert.match(text(m.root), /Complete/);
  assert.equal(m.ref.u.upgradeBusy, false);
  assert.equal(m.ref.u.awaitingReload, false);
  assert.match(
    bar.getAttribute("style"),
    /width: 100%.*rgb\(76, 194, 255\).*box-shadow: none/,
  );
  // No confirm view comes back while the result is shown; Close forgets it.
  assert.equal(button(m.root, "Download & Install"), undefined);
  click(button(m.root, "Close"));
  await settle();
  assert.equal(m.ref.u.showUpgrade, false);
  assert.equal(m.ref.u.upgradeProgress, null);
  await m.unmount();
});

test("an error stage shows the failure, with a default message when the gateway gave none", async () => {
  for (const [evt, want] of [
    [
      { stage: "error", percent: 30, message: "checksum mismatch" },
      "checksum mismatch",
    ],
    [{ stage: "error" }, "upgrade failed"],
  ]) {
    const s = streamResponse();
    const f = fakeApi({
      "GET /v1/update/check": json(200, INFO),
      "POST /v1/update/run": () => s.res,
    });
    const m = mount(f.api);
    await settle();
    m.ref.u.openUpgrade();
    await settle();
    click(button(m.root, "Download & Install"));
    await settle();
    s.send(evt);
    s.end();
    await settle();
    assert.match(text(m.root), /Failed/);
    assert.match(text(m.root), /The upgrade did not finish/);
    assert.match(
      m.root.querySelector("[style*='width']").getAttribute("style"),
      /rgb\(255, 107, 107\)/,
    );
    click(button(m.root, "Details"));
    await settle();
    assert.equal(text(m.root.querySelector("pre")), want);
    // Close forgets the failure; the next open is the confirm view again.
    click(button(m.root, "Close"));
    await settle();
    m.ref.u.openUpgrade();
    await settle();
    assert.ok(button(m.root, "Download & Install"));
    assert.doesNotMatch(text(m.root), /did not finish/);
    await m.unmount();
  }
});

test("a stream that breaks before the end is a failure", async () => {
  const s = streamResponse();
  const f = fakeApi({
    "GET /v1/update/check": json(200, INFO),
    "POST /v1/update/run": () => s.res,
  });
  const m = mount(f.api);
  await settle();
  m.ref.u.openUpgrade();
  await settle();
  click(button(m.root, "Download & Install"));
  await settle();
  s.send({ stage: "downloading", percent: 10 });
  s.fail(new Error("socket hang up"));
  await settle();
  assert.equal(m.ref.u.upgradeBusy, false);
  click(button(m.root, "Details"));
  await settle();
  assert.equal(text(m.root.querySelector("pre")), "Error: socket hang up");
  await m.unmount();
});

// restartRun runs an upgrade that restarts the gateway, then breaks the stream as the gateway goes
// down, and returns the mounted modal waiting for it to come back.
async function restartRun(health) {
  const s = streamResponse();
  const f = fakeApi({
    "GET /v1/update/check": json(200, INFO),
    "POST /v1/update/run": () => s.res,
  });
  const m = mount(f.api);
  await settle();
  m.ref.u.openUpgrade();
  await settle();
  click(button(m.root, "Download & Install"));
  await settle();
  globalThis.fetch = health;
  s.send({ stage: "restarting", percent: 100, message: "Restarting" });
  await settle();
  assert.match(text(m.root), /Restarting…/);
  s.send({ stage: "done", percent: 100, message: "Done" });
  await settle();
  s.fail(new TypeError("terminated"));
  await settle();
  return m;
}

test("after a restart the page waits for the gateway and reloads once it answers", async () => {
  const realFetch = globalThis.fetch;
  mock.timers.enable({ apis: ["setTimeout", "setInterval"] });
  try {
    const health = [];
    const answers = [
      new Error("down"),
      new Response("", { status: 502 }),
      new Response("ok", { status: 200 }),
    ];
    const m = await restartRun(async (url) => {
      health.push(url);
      const a = answers.shift();
      if (a instanceof Error) {
        throw a;
      }
      return a;
    });
    // The break of the stream is the restart, not a failure; nothing can be pressed.
    assert.doesNotMatch(text(m.root), /did not finish/);
    assert.ok(m.root.querySelector("[data-testid=upgrade-reloading]"));
    assert.match(text(m.root), /Wait for the page to reload automatically/);
    assert.equal(button(m.root, "Close"), undefined);
    assert.equal(m.ref.u.upgradeBusy, true);

    mock.timers.tick(2000);
    await settle();
    assert.equal(health.length, 0);
    for (let i = 0; i < 3; i++) {
      mock.timers.tick(1000);
      await settle();
    }
    assert.deepEqual(health, ["/v1/health", "/v1/health", "/v1/health"]);
    assert.equal(reloads.length, 1);
    // It stopped polling once the gateway answered.
    mock.timers.tick(5000);
    await settle();
    assert.equal(health.length, 3);
    await m.unmount();
  } finally {
    mock.timers.reset();
    globalThis.fetch = realFetch;
    reloads.length = 0;
  }
});

test("a gateway that does not come back in time unlocks the modal with a manual reload", async () => {
  const realFetch = globalThis.fetch;
  mock.timers.enable({ apis: ["setTimeout", "setInterval"] });
  try {
    let polls = 0;
    const m = await restartRun(async () => {
      polls++;
      throw new Error("down");
    });
    mock.timers.tick(2000);
    for (let i = 0; i < 30; i++) {
      mock.timers.tick(1000);
      await flush();
    }
    await settle();
    assert.ok(polls >= 29);
    assert.equal(m.ref.u.reloadStalled, true);
    assert.equal(m.ref.u.upgradeBusy, false);
    assert.ok(m.root.querySelector("[data-testid=upgrade-reload-stalled]"));
    assert.match(text(m.root), /did not come back in time/);
    // Polling stopped.
    const before = polls;
    mock.timers.tick(5000);
    await settle();
    assert.equal(polls, before);
    assert.equal(reloads.length, 0);
    click(button(m.root, "Reload page"));
    assert.equal(reloads.length, 1);
    await m.unmount();
  } finally {
    mock.timers.reset();
    globalThis.fetch = realFetch;
    reloads.length = 0;
  }
});
