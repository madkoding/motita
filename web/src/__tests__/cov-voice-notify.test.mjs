// The notification settings (NotifySettings.tsx, PushSetting.tsx, notifyBrowser.ts), the subtitles
// (Subtitles.tsx) and the insecure-connection pill (InsecureWarning.tsx), each loaded as the
// source itself: what the person sees for each answer of the browser and of the gateway, and what
// is asked of them.
import test, { beforeEach } from "node:test";
import assert from "node:assert/strict";
import { setupDom, source, useSourceHooks, settle as settleWith, json } from "./cov-voice-helpers.mjs";

const dom = setupDom("https://localhost/");
useSourceHooks();
const { NotifySetting, unblockSteps } = await source("NotifySettings.tsx");
const { PushSetting } = await source("PushSetting.tsx");
const { InsecureWarning } = await source("InsecureWarning.tsx");
const { Subtitles } = await source("Subtitles.tsx");
const nb = await source("notifyBrowser.ts");
const { showSubtitle } = await import("../speech.ts");
const { PUSH_KEY } = await import("../pushApi.ts");
const { h, render } = await import("preact");
const { act } = await import("preact/test-utils");

const settle = () => settleWith(act, 4);
const text = (el) => (el?.textContent ?? "").replace(/\s+/g, " ").trim();

async function mount(vnode) {
  const root = document.createElement("div");
  document.body.appendChild(root);
  await act(() => render(vnode, root));
  await settle();
  return {
    root,
    q: (s) => root.querySelector(s),
    set: async (v) => {
      await act(() => render(v, root));
      await settle();
    },
    done: async () => {
      await act(() => render(null, root));
      root.remove();
    },
  };
}
const change = async (input, checked) => {
  input.checked = checked;
  await act(() => input.dispatchEvent(new dom.window.Event("change", { bubbles: true })));
  await settle();
};
const click = async (el) => {
  await act(() => el.click());
  await settle();
};

// The browser's notifications: the permission now, and what asking answers.
const perm = { permission: "default", answer: "granted", asked: 0, throws: false, noPromise: false };
function installNotification() {
  globalThis.Notification = function (title, o) {
    perm.made = [title, o];
  };
  Object.defineProperty(globalThis.Notification, "permission", { get: () => perm.permission, configurable: true });
  globalThis.Notification.requestPermission = () => {
    perm.asked++;
    if (perm.throws) {
      throw new Error("not from a gesture");
    }
    if (perm.noPromise) {
      return undefined;
    }
    perm.permission = perm.answer;
    return Promise.resolve(perm.answer);
  };
}
installNotification();
// A user agent per platform.
const UA = {
  chrome: "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36",
  ios: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1",
  android: "Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Mobile Safari/537.36",
  firefox: "Mozilla/5.0 (X11; Linux x86_64; rv:127.0) Gecko/20100101 Firefox/127.0",
  safari: "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15",
  edge: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36 Edg/126.0",
  other: "SomeBrowser/1.0",
};
const useUA = (ua, touch = 0) => {
  Object.defineProperty(navigator, "userAgent", { value: ua, configurable: true });
  Object.defineProperty(navigator, "maxTouchPoints", { value: touch, configurable: true });
};
useUA(UA.chrome);
window.isSecureContext = true;
// A test that fails leaves no insecure page or other platform to the next one.
beforeEach(() => {
  window.isSecureContext = true;
  useUA(UA.chrome);
  perm.throws = false;
  perm.noPromise = false;
});

test("the insecure pill: the reason folded under Why?, or the one a setting gives", async () => {
  const m = await mount(h(InsecureWarning, { text: "Blocked here.", guide: "https://guide" }));
  assert.equal(m.q("[data-testid=insecure-warning]").getAttribute("role"), "note");
  assert.match(text(m.root), /Connection not secure/);
  assert.match(text(m.q("details")), /secure address/);
  assert.equal(m.q("a").getAttribute("href"), "https://guide");
  await m.set(h(InsecureWarning, { text: "x", guide: "g", why: "Your key would travel in the clear." }));
  assert.match(text(m.q("details")), /^Why\? ?Your key would travel in the clear\. How to set it up$/);
  await m.done();
});

test("the subtitles follow the voice, fade out with their words, and turn off", async () => {
  const m = await mount(h(Subtitles, { on: false, size: "medium" }));
  await act(() => showSubtitle("All done."));
  assert.equal(m.root.innerHTML, "");
  await m.set(h(Subtitles, { on: true, size: "large" }));
  const p = m.q(".subtitles");
  assert.equal(p.className, "subtitles subtitles-large");
  assert.equal("shown" in p.dataset, false);
  await act(() => showSubtitle("All done."));
  assert.equal(p.textContent, "All done.");
  assert.equal(p.dataset.shown, "1");
  await act(() => showSubtitle(""));
  assert.equal("shown" in p.dataset, false);
  assert.equal(p.textContent, "All done.", "the words stay while it fades");
  await m.set(h(Subtitles, { on: false, size: "large" }));
  assert.equal(m.q(".subtitles"), null);
  await m.done();
});

test("the browser's side: installed or not, the permission, a secure page, the platform", () => {
  window.matchMedia = () => ({ matches: true });
  assert.equal(nb.isInstalled(), true);
  window.matchMedia = () => ({ matches: false });
  Object.defineProperty(navigator, "standalone", { value: true, configurable: true });
  assert.equal(nb.isInstalled(), true, "Safari's own flag");
  delete navigator.standalone;
  assert.equal(nb.isInstalled(), false);
  delete window.matchMedia;
  assert.equal(nb.isInstalled(), false, "no matchMedia");
  window.matchMedia = () => {
    throw new Error("broken");
  };
  assert.equal(nb.isInstalled(), false);
  delete window.matchMedia;

  assert.equal(nb.readPermission(), "default");
  const N = globalThis.Notification;
  delete globalThis.Notification;
  assert.equal(nb.readPermission(), undefined);
  globalThis.Notification = N;

  assert.equal(nb.isSecurePage(), true);
  window.isSecureContext = false;
  assert.equal(nb.isSecurePage(), false);
  window.isSecureContext = true;
  const win = globalThis.window;
  delete globalThis.window;
  assert.equal(nb.isSecurePage(), true, "outside a browser");
  globalThis.window = win;

  useUA(UA.ios, 5);
  assert.equal(nb.currentPlatform(), "ios");
  Object.defineProperty(navigator, "maxTouchPoints", { value: undefined, configurable: true });
  useUA(UA.chrome);
  assert.equal(nb.currentPlatform(), "chrome");
  assert.match(nb.NOTIFY_GUIDE, /^https:\/\/github\.com\//);
});

test("the steps to allow notifications again, for each platform", () => {
  assert.match(unblockSteps("ios", false)[0], /Settings app/);
  assert.match(unblockSteps("android", true)[0], /App info/);
  assert.match(unblockSteps("android", false)[0], /Permissions/);
  assert.match(unblockSteps("firefox", false)[1], /clear the block/);
  assert.match(unblockSteps("safari", false)[0], /Open Safari/);
  assert.match(unblockSteps("chrome", true)[0], /app's menu/);
  assert.match(unblockSteps("edge", false)[0], /left of the address/);
  assert.match(unblockSteps("other", false)[0], /site's settings/);
});

test("desktop notifications: asked, allowed, switched, tested, and the CI toast's own switch", async () => {
  localStorage.clear();
  perm.permission = "default";
  perm.answer = "granted";
  const shown = [];
  Object.defineProperty(navigator, "serviceWorker", {
    value: { getRegistration: async () => ({ showNotification: async (t, o) => shown.push([t, o.tag]) }) },
    configurable: true,
  });
  const m = await mount(h(NotifySetting, {}));
  const card = () => m.q("[data-testid=notifications-setting]");
  assert.match(text(card()), /The browser has to allow it first/);
  await click(card().querySelector("button"));
  assert.equal(perm.asked, 1);
  const sw = () => card().querySelector("input[role=switch]");
  assert.equal(sw().checked, true, "allowing turns the switch on");
  assert.match(text(card()), /Only while this tab is in the background\./);
  await click([...card().querySelectorAll("button")].find((b) => text(b) === "Send"));
  assert.deepEqual(shown, [["Motita notifications work", "motita-test"]]);
  assert.match(text(card()), /check the system's do-not-disturb/);
  await change(sw(), false);
  assert.equal(sw().checked, false);
  assert.equal(card().querySelector("button"), null, "no test while off");
  await change(sw(), true);

  // The worker refuses and so does the constructor: the browser did not show it.
  navigator.serviceWorker.getRegistration = async () => undefined;
  globalThis.Notification = function () {
    throw new TypeError("Illegal constructor");
  };
  Object.defineProperty(globalThis.Notification, "permission", { get: () => perm.permission, configurable: true });
  await click([...card().querySelectorAll("button")].find((b) => text(b) === "Send"));
  assert.match(text(card()), /The browser did not show it\./);
  installNotification();

  // The CI toast.
  const toast = m.q("[data-testid=ci-toast-setting] input");
  assert.equal(toast.checked, true);
  await change(toast, false);
  assert.equal(toast.checked, false);
  await m.done();
});

test("desktop notifications: refused, ignored, or a browser that answers no promise", async () => {
  localStorage.clear();
  useUA(UA.firefox);
  perm.permission = "default";
  perm.answer = "default";
  const m = await mount(h(NotifySetting, {}));
  const card = () => m.q("[data-testid=notifications-setting]");
  await click(card().querySelector("button"));
  assert.match(text(card()), /The browser did not ask\. To allow them by hand:/);
  assert.match(text(m.q("[data-testid=notify-steps]")), /clear the block/);
  await m.done();

  perm.permission = "default";
  perm.throws = true;
  const t = await mount(h(NotifySetting, {}));
  await click(t.q("[data-testid=notifications-setting] button"));
  assert.ok(t.q("[data-testid=notify-steps]"), "a request that threw counts as asked");
  perm.throws = false;
  await t.done();

  perm.permission = "default";
  perm.noPromise = true;
  const n = await mount(h(NotifySetting, {}));
  await click(n.q("[data-testid=notifications-setting] button"));
  assert.equal(n.q("[data-testid=notify-steps]"), null, "Safari before 15 answers through focus");
  perm.noPromise = false;
  await n.done();

  // Blocked: the steps, and Check again reads it once more; allowed in the browser meanwhile.
  useUA(UA.chrome);
  perm.permission = "denied";
  const b = await mount(h(NotifySetting, {}));
  assert.match(text(b.root), /Blocked in this browser/);
  assert.match(text(b.q("[data-testid=notify-steps]")), /Reset permission/);
  perm.permission = "granted";
  await click(b.q("[data-testid=notifications-setting] button"));
  assert.equal(b.q("[data-testid=notify-steps]"), null);
  assert.ok(b.q("[data-testid=notifications-setting] input[role=switch]"));
  await b.done();
});

test("desktop notifications: an iPhone outside the app, a browser without them, an insecure page", async () => {
  perm.permission = "default";
  const N = globalThis.Notification;
  delete globalThis.Notification;
  useUA(UA.ios, 5);
  let m = await mount(h(NotifySetting, {}));
  assert.match(text(m.root), /only work in the installed app/);
  await m.done();
  useUA(UA.chrome);
  m = await mount(h(NotifySetting, {}));
  assert.match(text(m.root), /This browser cannot show notifications\./);
  await m.done();
  globalThis.Notification = N;
  window.isSecureContext = false;
  m = await mount(h(NotifySetting, {}));
  assert.ok(m.q("[data-testid=notifications-setting] [data-testid=insecure-warning]"));
  assert.equal(m.q("[data-testid=notifications-setting] input[role=switch]"), null);
  await m.done();
  window.isSecureContext = true;
});

test("the permission is read again on focus, on coming back, and when the browser says it changed", async () => {
  perm.permission = "default";
  let status;
  Object.defineProperty(navigator, "permissions", {
    value: {
      query: async ({ name }) => {
        assert.equal(name, "notifications");
        status = {
          listeners: new Set(),
          addEventListener(_t, f) {
            this.listeners.add(f);
          },
          removeEventListener(_t, f) {
            this.listeners.delete(f);
          },
        };
        return status;
      },
    },
    configurable: true,
  });
  const m = await mount(h(NotifySetting, {}));
  const button = () => text(m.q("[data-testid=notifications-setting] button"));
  assert.equal(button(), "Allow notifications");
  perm.permission = "denied";
  await act(() => window.dispatchEvent(new dom.window.Event("focus")));
  assert.equal(button(), "Check again");
  perm.permission = "default";
  await act(() => document.dispatchEvent(new dom.window.Event("visibilitychange")));
  assert.equal(button(), "Allow notifications");
  perm.permission = "denied";
  await act(() => [...status.listeners][0]());
  assert.equal(button(), "Check again");
  await m.done();
  assert.equal(status.listeners.size, 0, "it stops listening when it goes");
  // A browser whose Permissions API refuses the name: focus is enough.
  navigator.permissions.query = async () => {
    throw new TypeError("notifications");
  };
  const r = await mount(h(NotifySetting, {}));
  await r.done();
  delete navigator.permissions;
});

// PUSH: a service worker with a push manager, and the gateway's answers.
const push = { sub: null, subscribed: [], unsubscribed: 0, regFails: false, noReg: false };
function subscription(endpoint) {
  return {
    endpoint,
    toJSON: () => ({ endpoint, keys: { p256dh: "k", auth: "a" } }),
    unsubscribe: async () => {
      push.unsubscribed++;
      if (push.unsubscribeFails) {
        throw new Error("gone");
      }
      push.sub = null;
      return true;
    },
  };
}
const pushManager = {
  async getSubscription() {
    return push.sub;
  },
  async subscribe(o) {
    push.subscribed.push(o);
    push.sub = subscription("https://push/new");
    return push.sub;
  },
};
const pushWorker = {
    async getRegistration() {
      if (push.regFails) {
        throw new Error("no worker");
      }
      return push.noReg ? undefined : { pushManager };
    },
    ready: Promise.resolve({ pushManager }),
};
// Each phone test starts from this worker (the desktop ones put their own).
const usePushWorker = () =>
  Object.defineProperty(navigator, "serviceWorker", { value: pushWorker, configurable: true });
window.PushManager = function () {};

function gateway(view, overrides = {}) {
  const calls = [];
  const api = async (path, init) => {
    calls.push([init?.method ?? "GET", path, init?.body ? JSON.parse(init.body) : undefined]);
    const key = (init?.method ?? "GET") + " " + path.split("?")[0];
    if (overrides[key]) {
      return overrides[key]();
    }
    if (key === "GET /v1/push") {
      return json(view());
    }
    return new Response(null, { status: 204 });
  };
  return { api, calls };
}
const pushCard = (m) => m.q("[data-testid=push-setting]");
const pushSwitch = (m) => pushCard(m).querySelector("input[role=switch]");

test("phone notifications: turned on and off, and tested through the gateway", async () => {
  usePushWorker();
  localStorage.clear();
  perm.permission = "default";
  perm.answer = "granted";
  push.sub = null;
  let known = false;
  const g = gateway(() => ({ available: true, public_key: "AQID", subscribed: known }), {
    "POST /v1/push/subscriptions": () => {
      known = true;
      return new Response(null, { status: 204 });
    },
    "DELETE /v1/push/subscriptions": () => {
      known = false;
      return new Response(null, { status: 204 });
    },
    "POST /v1/push/test": () => json({ error: "push service said 410" }, 502),
  });
  // NotifySetting with push draws the phone row under the desktop one.
  const m = await mount(h(NotifySetting, { push: { api: g.api } }));
  assert.match(text(pushCard(m)), /Even with Motita closed/);
  assert.equal(pushSwitch(m).checked, false);
  await change(pushSwitch(m), true);
  assert.deepEqual(Array.from(push.subscribed[0].applicationServerKey), [1, 2, 3]);
  assert.equal(push.subscribed[0].userVisibleOnly, true);
  const saved = g.calls.find((c) => c[0] === "POST" && c[1] === "/v1/push/subscriptions");
  assert.equal(saved[2].endpoint, "https://push/new");
  assert.equal(saved[2].lang, "en");
  assert.equal(localStorage.getItem(PUSH_KEY), "on");
  assert.equal(pushSwitch(m).checked, true);
  assert.equal(g.calls.at(-1)[1], "/v1/push?endpoint=" + encodeURIComponent("https://push/new"));

  const send = () => [...pushCard(m).querySelectorAll("button")].find((b) => text(b) === "Send");
  await click(send());
  assert.match(text(pushCard(m)), /push service said 410/);

  await change(pushSwitch(m), false);
  assert.equal(push.unsubscribed, 1);
  assert.equal(localStorage.getItem(PUSH_KEY), null);
  assert.equal(pushSwitch(m).checked, false);
  await m.done();
});

test("phone notifications: a test that goes, a subscription the browser kept, one the gateway forgot", async () => {
  usePushWorker();
  localStorage.clear();
  perm.permission = "granted";
  // The browser already holds a subscription; the gateway's folder was cleared, and the switch was on.
  push.sub = subscription("https://push/kept");
  localStorage.setItem(PUSH_KEY, "on");
  let known = false;
  const g = gateway(() => ({ available: true, public_key: "AQID", subscribed: known }), {
    "POST /v1/push/subscriptions": () => {
      known = true;
      return new Response(null, { status: 204 });
    },
  });
  const m = await mount(h(PushSetting, { api: g.api }));
  assert.equal(pushSwitch(m).checked, true, "handed over again, it stays on");
  const send = () => [...pushCard(m).querySelectorAll("button")].find((b) => text(b) === "Send");
  await act(() => send().click());
  assert.match(text(pushCard(m)), /Sending…/);
  await settle();
  assert.match(text(pushCard(m)), /do-not-disturb/);
  await m.done();

  // Subscribing again uses the subscription the browser holds instead of a new one.
  localStorage.clear();
  push.subscribed = [];
  known = false;
  const again = await mount(h(PushSetting, { api: g.api }));
  assert.equal(pushSwitch(again).checked, false, "the gateway does not know it and it was not on");
  await change(pushSwitch(again), true);
  assert.deepEqual(push.subscribed, [], "the browser's own subscription is handed over");
  assert.equal(pushSwitch(again).checked, true);
  await again.done();
});

test("phone notifications: turning off survives a gateway and a browser that cannot forget", async () => {
  usePushWorker();
  localStorage.setItem(PUSH_KEY, "on");
  perm.permission = "granted";
  push.sub = subscription("https://push/x");
  push.unsubscribeFails = true;
  const g = gateway(() => ({ available: true, public_key: "AQID", subscribed: true }), {
    "DELETE /v1/push/subscriptions": () => new Response("<html>", { status: 500 }),
  });
  const m = await mount(h(PushSetting, { api: g.api }));
  assert.equal(pushSwitch(m).checked, true);
  await change(pushSwitch(m), false);
  assert.equal(localStorage.getItem(PUSH_KEY), null);
  assert.equal(pushCard(m).querySelector("[role=alert]"), null, "no error to show");
  push.unsubscribeFails = false;
  push.sub = null;
  await m.done();
});

test("phone notifications: refused permission, a failed hand-over, and what blocks them", async () => {
  usePushWorker();
  localStorage.clear();
  push.sub = null;
  perm.permission = "default";
  perm.answer = "denied";
  const view = { available: true, public_key: "AQID", subscribed: false };
  const g = gateway(() => view, {
    "POST /v1/push/subscriptions": () => json({ error: "disk full" }, 507),
  });
  let m = await mount(h(PushSetting, { api: g.api }));
  await change(pushSwitch(m), true);
  assert.equal(text(pushCard(m).querySelector("[role=alert]")), "The browser did not allow notifications.");
  assert.match(text(pushCard(m)), /blocked for this site/, "denied now: the switch gives way to the hint");
  await m.done();

  // A request that throws, on a browser that already allowed them: it goes ahead.
  perm.permission = "granted";
  perm.throws = true;
  m = await mount(h(PushSetting, { api: g.api }));
  await change(pushSwitch(m), true);
  assert.equal(text(pushCard(m).querySelector("[role=alert]")), "disk full");
  perm.throws = false;
  await m.done();

  // The gateway cannot push, or does not answer.
  m = await mount(h(PushSetting, { api: gateway(() => ({ available: false, subscribed: false })).api }));
  assert.match(text(pushCard(m)), /This gateway cannot send them/);
  assert.equal(pushSwitch(m), null);
  await m.done();
  m = await mount(h(PushSetting, { api: async () => Promise.reject(new Error("offline")) }));
  assert.match(text(pushCard(m)), /This gateway cannot send them/);
  await m.done();

  // A worker that cannot be asked, or none registered: no subscription here.
  push.regFails = true;
  m = await mount(h(PushSetting, { api: g.api }));
  assert.equal(pushSwitch(m).checked, false);
  await m.done();
  push.regFails = false;
  push.noReg = true;
  m = await mount(h(PushSetting, { api: g.api }));
  assert.equal(pushSwitch(m).checked, false);
  await m.done();
  push.noReg = false;

  // An iPhone outside the app, a browser without push, an insecure page.
  const PM = window.PushManager;
  delete window.PushManager;
  useUA(UA.ios, 5);
  m = await mount(h(PushSetting, { api: g.api }));
  assert.match(text(pushCard(m)), /only work in the installed app/);
  await m.done();
  useUA(UA.chrome);
  m = await mount(h(PushSetting, { api: g.api }));
  assert.match(text(pushCard(m)), /cannot receive push notifications/);
  await m.done();
  window.PushManager = PM;
  window.isSecureContext = false;
  m = await mount(h(PushSetting, { api: g.api }));
  assert.ok(pushCard(m).querySelector("[data-testid=insecure-warning]"));
  assert.equal(pushSwitch(m), null);
  await m.done();
  window.isSecureContext = true;
});

test("phone notifications: a kept subscription the gateway will not take back, and a gateway with no key", async () => {
  usePushWorker();
  localStorage.setItem(PUSH_KEY, "on");
  perm.permission = "granted";
  push.sub = subscription("https://push/kept");
  const g = gateway(() => ({ available: true, public_key: "AQID", subscribed: false }), {
    "POST /v1/push/subscriptions": () => json({ error: "disk full" }, 507),
  });
  let m = await mount(h(PushSetting, { api: g.api }));
  assert.ok(g.calls.some((c) => c[0] === "POST"), "it tried to hand it over");
  assert.equal(pushSwitch(m).checked, false, "shown as off");
  await m.done();

  // A gateway that gives no key: the browser is asked with an empty one.
  localStorage.clear();
  perm.answer = "granted";
  push.sub = null;
  push.subscribed = [];
  m = await mount(h(PushSetting, { api: gateway(() => ({ available: true, subscribed: false })).api }));
  await change(pushSwitch(m), true);
  assert.equal(push.subscribed[0].applicationServerKey.length, 0);
  push.sub = null;
  await m.done();
});
