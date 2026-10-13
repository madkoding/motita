// Phone notifications (Web Push): what the settings say for each device and gateway, the key
// conversion, the session a tap opens, and the gateway calls.
// Run with: node --test web/src/__tests__/push.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const {
  pushState,
  keyBytes,
  sessionFromNotice,
  getPush,
  saveSubscription,
  removeSubscription,
  testPush,
  loadPushOn,
  savePushOn,
} = await import("../pushApi.ts");

const ready = {
  secure: true,
  supported: true,
  ios: false,
  installed: false,
  gateway: { available: true, public_key: "k", subscribed: true },
  permission: "granted",
  subscribed: true,
};

test("the state names what blocks push first", () => {
  assert.equal(pushState(ready), "on");
  assert.equal(pushState({ ...ready, secure: false }), "insecure");
  assert.equal(
    pushState({ ...ready, supported: false, ios: true }),
    "install",
    "an iPhone in Safari: push needs the app on the home screen",
  );
  assert.equal(
    pushState({ ...ready, supported: false, ios: true, installed: true }),
    "unsupported",
  );
  assert.equal(pushState({ ...ready, supported: false }), "unsupported");
  assert.equal(pushState({ ...ready, gateway: null }), "unavailable");
  assert.equal(
    pushState({ ...ready, gateway: { available: false, subscribed: false } }),
    "unavailable",
  );
  assert.equal(pushState({ ...ready, permission: "denied" }), "blocked");
  assert.equal(pushState({ ...ready, subscribed: false }), "off");
  assert.equal(
    pushState({
      ...ready,
      gateway: { available: true, subscribed: false },
    }),
    "off",
    "a subscription the gateway does not know is not on",
  );
});

test("the gateway's base64url key becomes the bytes subscribe takes", () => {
  const raw = Uint8Array.from({ length: 65 }, (_, i) => (i * 37) % 256);
  const b64url = Buffer.from(raw)
    .toString("base64")
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/, "");
  assert.deepEqual(Array.from(keyBytes(b64url)), Array.from(raw));
});

test("a tap on a notification names the session to open", () => {
  assert.equal(
    sessionFromNotice({ type: "motita-open", url: "/?session=s%201" }),
    "s 1",
  );
  assert.equal(sessionFromNotice({ type: "motita-open", url: "/" }), "");
  assert.equal(sessionFromNotice({ type: "other", url: "/?session=x" }), "");
  assert.equal(sessionFromNotice({ type: "motita-open", url: 3 }), "");
  assert.equal(sessionFromNotice(null), "");
});

test("the switch is remembered per browser", () => {
  const store = new Map();
  globalThis.localStorage = {
    getItem: (k) => store.get(k) ?? null,
    setItem: (k, v) => store.set(k, v),
    removeItem: (k) => store.delete(k),
  };
  assert.equal(loadPushOn(), false);
  savePushOn(true);
  assert.equal(loadPushOn(), true);
  savePushOn(false);
  assert.equal(loadPushOn(), false);
  delete globalThis.localStorage;
  assert.equal(loadPushOn(), false, "no storage reads as off");
});

test("the calls: an older gateway reads as unavailable, a refusal carries its reason", async () => {
  const calls = [];
  const answers = [];
  const api = async (path, init) => {
    calls.push([path, init?.method ?? "GET", init?.body]);
    return answers.shift();
  };
  answers.push(new Response("", { status: 404 }));
  assert.deepEqual(await getPush(api), { available: false, subscribed: false });
  answers.push(
    Response.json({ available: true, public_key: "k", subscribed: true }),
  );
  assert.equal((await getPush(api, "https://x/?a")).subscribed, true);
  assert.equal(calls[1][0], "/v1/push?endpoint=https%3A%2F%2Fx%2F%3Fa");

  answers.push(new Response(null, { status: 204 }));
  await saveSubscription(
    api,
    { endpoint: "e", keys: { p256dh: "p", auth: "a" } },
    "es",
  );
  assert.deepEqual(JSON.parse(calls[2][2]), {
    endpoint: "e",
    keys: { p256dh: "p", auth: "a" },
    lang: "es",
  });
  answers.push(
    Response.json({ error: "not a usable push subscription" }, { status: 400 }),
  );
  await assert.rejects(saveSubscription(api, {}, "en"), /not a usable/);

  answers.push(new Response(null, { status: 204 }));
  await removeSubscription(api, "e");
  assert.deepEqual(calls[4].slice(0, 2), ["/v1/push/subscriptions", "DELETE"]);
  answers.push(new Response("", { status: 500 }));
  await assert.rejects(removeSubscription(api, "e"), /HTTP 500/);

  answers.push(new Response(null, { status: 204 }));
  await testPush(api, "e");
  answers.push(
    Response.json({ error: "the push service refused" }, { status: 502 }),
  );
  await assert.rejects(testPush(api, "e"), /refused/);
});

test("the service worker shows a push, leaves it to a page on screen, and opens the session", () => {
  const sw = readFileSync(join(here, "..", "sw.ts"), "utf8");
  assert.match(sw, /addEventListener\("push"/);
  assert.match(sw, /visibilityState === "visible"/);
  assert.match(
    sw,
    /m\.tag !== "motita-test"/,
    "the test is shown even with the page in front",
  );
  assert.match(sw, /postMessage\(\{ type: "motita-open", url \}\)/);
  assert.match(sw, /openWindow\(url\)/);
  assert.match(sw, /addEventListener\("pushsubscriptionchange"/);
});
