// The service worker (sw.ts) run against a stand-in worker scope: what it precaches, the caches it
// retires, how it answers a request, the notification a push shows, a tap on one, and a renewed
// push subscription handed to the gateway. cov-voice-sw.test.mjs runs these scenarios, and
// cov-voice-sw-bare.test.mjs once more, with the worker built without a precache manifest.
import assert from "node:assert/strict";

// The scenarios are collected here, not registered: each .test.mjs that runs them declares them
// with its own test() calls, in a process of its own (where a flag it sets first picks the
// variant), so a file node runs always holds its own tests.
export const cases = [];
const test = (...args) => cases.push(args);

const bare = globalThis.COV_VOICE_SW_BARE === true;

// The stand-in scope: the listeners the worker adds, and what it asks of the browser.
const log = {
  skipped: 0,
  claimed: 0,
  shown: [],
  opened: [],
  posted: [],
  focused: 0,
  subscribed: [],
};
const listeners = {};
const scope = {
  navigator: { language: "es-CL" },
  addEventListener(type, f) {
    listeners[type] = f;
  },
  skipWaiting() {
    log.skipped++;
  },
  clients: {
    windows: [],
    claim() {
      log.claimed++;
    },
    async matchAll(o) {
      assert.deepEqual(o, { type: "window", includeUncontrolled: true });
      return this.windows;
    },
    async openWindow(url) {
      log.opened.push(url);
      return null;
    },
  },
  registration: {
    async showNotification(title, o) {
      log.shown.push({ title, ...o });
    },
    pushManager: {
      async subscribe(o) {
        log.subscribed.push(o);
        return { toJSON: () => ({ endpoint: "https://push/renewed" }) };
      },
    },
  },
};
if (!bare) {
  scope.__WB_MANIFEST = [
    { url: "assets/index-1.js" },
    { url: "/" },
    { url: "" },
    { url: "index.html" },
    { url: "/app/index.html?v=2" },
    { url: "icon-192.png" },
    { url: "missing.css" },
  ];
}
globalThis.self = scope;

// Cache Storage: named caches of url -> Response.
const store = new Map();
function cacheNamed(name) {
  if (!store.has(name)) {
    store.set(name, new Map());
  }
  const entries = store.get(name);
  const key = (r) => (typeof r === "string" ? r : new URL(r.url).pathname);
  return {
    entries,
    async add(url) {
      if (url === "missing.css") {
        throw new Error("404");
      }
      entries.set(url, new Response("asset " + url));
    },
    async put(r, res) {
      entries.set(key(r), res);
    },
  };
}
globalThis.caches = {
  async open(name) {
    return cacheNamed(name);
  },
  async keys() {
    return [...store.keys()];
  },
  async delete(name) {
    return store.delete(name);
  },
  async match(r) {
    const k = typeof r === "string" ? r : new URL(r.url).pathname;
    for (const entries of store.values()) {
      if (entries.has(k)) {
        return entries.get(k).clone();
      }
    }
    return undefined;
  },
};
const fetched = [];
let network = async () => new Response("net");
globalThis.fetch = async (r, init) => {
  fetched.push([typeof r === "string" ? r : r.url, init]);
  return network(r, init);
};

await import("../sw.ts");

// event makes an ExtendableEvent whose waitUntil and respondWith promises the test awaits.
function event(extra = {}) {
  const e = {
    waits: [],
    waitUntil(p) {
      e.waits.push(p);
    },
    respondWith(p) {
      e.response = p;
    },
    ...extra,
  };
  return e;
}
const settled = (e) => Promise.all(e.waits);

test("install precaches the assets but never the document, and a missing one does not fail it", async () => {
  const e = event();
  listeners.install(e);
  await settled(e);
  assert.equal(log.skipped, 1);
  const shell = [...store.entries()].find(([n]) =>
    n.startsWith("motita-shell"),
  );
  assert.ok(shell, "the shell cache is opened");
  if (bare) {
    assert.deepEqual(
      [...shell[1].keys()],
      [],
      "nothing to precache without a manifest",
    );
  } else {
    assert.deepEqual(
      [...shell[1].keys()],
      ["assets/index-1.js", "icon-192.png"],
    );
  }
});

test("activate retires every other shell cache, keeps the voices, and takes the open pages", async () => {
  cacheNamed("motita-shell-v1");
  cacheNamed("motita-voices-v1");
  const e = event();
  listeners.activate(e);
  await settled(e);
  assert.equal(store.has("motita-shell-v1"), false);
  assert.equal(
    store.has("motita-voices-v1"),
    true,
    "the downloaded models survive an update",
  );
  assert.equal(
    [...store.keys()].some((n) => n.startsWith("motita-shell")),
    true,
  );
  assert.equal(log.claimed, 1);
});

test("the API is left to the network, the document comes from it, and assets from the cache", async () => {
  const api = event({
    request: { url: "https://h/v1/sessions", mode: "cors" },
  });
  listeners.fetch(api);
  assert.equal(
    api.response,
    undefined,
    "an API call is not answered by the worker",
  );

  network = async () => new Response("<html>new", { status: 200 });
  const nav = event({
    request: { url: "https://h/?session=1", mode: "navigate" },
  });
  listeners.fetch(nav);
  assert.equal(await (await nav.response).text(), "<html>new");
  await new Promise((r) => setImmediate(r));
  assert.equal(
    await (await caches.match("/")).text(),
    "<html>new",
    "the offline copy is refreshed",
  );

  network = async () => new Response("oops", { status: 500 });
  const bad = event({ request: { url: "https://h/", mode: "navigate" } });
  listeners.fetch(bad);
  assert.equal((await bad.response).status, 500);
  assert.equal(
    await (await caches.match("/")).text(),
    "<html>new",
    "an error page is not kept",
  );

  network = async () => {
    throw new TypeError("offline");
  };
  const offline = event({ request: { url: "https://h/", mode: "navigate" } });
  listeners.fetch(offline);
  assert.equal(
    await (await offline.response).text(),
    "<html>new",
    "offline, the copy is shown",
  );
  for (const entries of store.values()) {
    entries.delete("/");
  }
  const none = event({ request: { url: "https://h/", mode: "navigate" } });
  listeners.fetch(none);
  assert.equal(
    (await none.response).type,
    "error",
    "offline with no copy: a network error",
  );

  network = async () => new Response("js", { status: 200 });
  const asset = event({
    request: { url: "https://h/assets/a.js", mode: "no-cors" },
  });
  listeners.fetch(asset);
  assert.equal(await (await asset.response).text(), "js");
  await new Promise((r) => setImmediate(r));
  fetched.length = 0;
  network = async () => new Response("gone", { status: 404 });
  const again = event({
    request: { url: "https://h/assets/a.js", mode: "no-cors" },
  });
  listeners.fetch(again);
  assert.equal(
    await (await again.response).text(),
    "js",
    "the second time from the cache",
  );
  assert.deepEqual(fetched, []);
  const missing = event({
    request: { url: "https://h/assets/b.js", mode: "no-cors" },
  });
  listeners.fetch(missing);
  assert.equal((await missing.response).status, 404);
  assert.equal(
    await caches.match({ url: "https://h/assets/b.js" }),
    undefined,
    "a 404 is not kept",
  );
});

test("a push shows its notification unless a window is in front; the test always shows", async () => {
  const data = (v) => ({ json: () => v, text: () => String(v) });
  const push = async (d) => {
    const e = event({ data: d });
    listeners.push(e);
    await settled(e);
  };
  await push(
    data({ title: "CI passed", body: "PR 1", tag: "ci-1", url: "/?session=a" }),
  );
  assert.deepEqual(log.shown.at(-1), {
    title: "CI passed",
    body: "PR 1",
    tag: "ci-1",
    icon: "/icon-192.png",
    badge: "/badge-96.png",
    data: { url: "/?session=a" },
  });
  await push(undefined);
  assert.equal(
    log.shown.at(-1).title,
    "Motita",
    "an empty push still says who sent it",
  );
  assert.deepEqual(log.shown.at(-1).data, { url: "/" });
  await push({
    json() {
      throw new SyntaxError("not json");
    },
    text: () => "plain words",
  });
  assert.equal(log.shown.at(-1).title, "plain words");
  await push(data(null));
  assert.equal(log.shown.at(-1).title, "Motita");

  scope.clients.windows = [
    { visibilityState: "hidden" },
    { visibilityState: "visible" },
  ];
  const before = log.shown.length;
  await push(data({ title: "CI failed", tag: "ci-2" }));
  assert.equal(log.shown.length, before, "the page on screen tells it itself");
  await push(data({ title: "Test", tag: "motita-test" }));
  assert.equal(log.shown.length, before + 1);
  scope.clients.windows = [];
});

test("a tap focuses the open window on the session, or opens one", async () => {
  const tap = async (data) => {
    let closed = 0;
    const e = event({ notification: { data, close: () => closed++ } });
    listeners.notificationclick(e);
    await settled(e);
    assert.equal(closed, 1);
  };
  await tap({ url: "/?session=b" });
  assert.deepEqual(log.opened, ["/?session=b"]);
  await tap(undefined);
  assert.deepEqual(log.opened, ["/?session=b", "/"]);
  const win = {
    postMessage: (m) => log.posted.push(m),
    focus: async () => {
      log.focused++;
      return win;
    },
  };
  scope.clients.windows = [win];
  await tap({ url: "/?session=c" });
  assert.deepEqual(log.posted, [{ type: "motita-open", url: "/?session=c" }]);
  assert.equal(log.focused, 1);
  scope.clients.windows = [];
});

test("a renewed subscription is handed to the gateway, in the device's language", async () => {
  fetched.length = 0;
  network = async () => new Response(null, { status: 204 });
  const change = async (extra) => {
    const e = event(extra);
    listeners.pushsubscriptionchange(e);
    await settled(e);
  };
  await change({
    newSubscription: { toJSON: () => ({ endpoint: "https://push/new" }) },
  });
  assert.equal(fetched.length, 1);
  assert.equal(fetched[0][0], "/v1/push/subscriptions");
  assert.equal(fetched[0][1].credentials, "same-origin");
  assert.deepEqual(JSON.parse(fetched[0][1].body), {
    endpoint: "https://push/new",
    lang: "es",
  });

  scope.navigator.language = "en-US";
  await change({ oldSubscription: { options: { userVisibleOnly: true } } });
  assert.deepEqual(log.subscribed, [{ userVisibleOnly: true }]);
  assert.deepEqual(JSON.parse(fetched[1][1].body), {
    endpoint: "https://push/renewed",
    lang: "en",
  });

  await change({ oldSubscription: null });
  await change({ oldSubscription: { options: null } });
  assert.equal(
    fetched.length,
    2,
    "nothing to hand over without a subscription",
  );
});
