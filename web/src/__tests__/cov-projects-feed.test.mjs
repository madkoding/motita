// The change feed: the page re-reads the projects or the sessions when the gateway says they
// changed, both when the feed (re)connects and when the tab is shown again, falls back to the
// projects' own stream on a gateway without the combined one, and stops with the page or without
// a login.
// Run with: node --test web/src/__tests__/cov-projects-feed.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { installDom, load, settle } from "./cov-settings-helpers.mjs";

installDom("http://localhost/");

const { useChangeFeed } = await load("useProjectFeed.ts");
const { h, render } = await import("preact");

// feedResponse is a stream whose frames the test pushes one by one.
function feedResponse() {
  let controller;
  const body = new ReadableStream({
    start(c) {
      controller = c;
    },
  });
  const enc = new TextEncoder();
  return {
    res: { ok: true, body },
    push: (text) => controller.enqueue(enc.encode(text)),
    end: () => controller.close(),
  };
}

function mount(
  api,
  fetchProjects,
  authOk = true,
  fetchSessions = async () => {},
) {
  function Harness() {
    useChangeFeed(api, fetchProjects, fetchSessions, authOk);
    return null;
  }
  const root = document.createElement("div");
  document.body.appendChild(root);
  render(h(Harness), root);
  return async () => {
    render(null, root);
    await settle();
  };
}

const later = (ms = 30) => new Promise((r) => setTimeout(r, ms));

test("an event re-reads the projects, a comment does not", async () => {
  const feed = feedResponse();
  const asked = [];
  let reads = 0;
  const off = mount(
    async (path, init) => {
      asked.push([path, init?.signal instanceof AbortSignal]);
      return feed.res;
    },
    async () => {
      reads++;
    },
  );
  await later();
  assert.deepEqual(asked[0], ["/v1/events", true]);
  assert.equal(reads, 1, "connecting reads once");
  feed.push(": keepalive\n\n");
  await later();
  assert.equal(reads, 1);
  feed.push('id: 0\nevent: projects\ndata: {"changed":true}\n\n');
  await later();
  assert.equal(reads, 2);
  await off();
});

test("the tab coming back reads the projects, a hidden one does not", async () => {
  const feed = feedResponse();
  let reads = 0;
  const off = mount(
    async () => feed.res,
    async () => {
      reads++;
    },
  );
  await later();
  const base = reads;
  let state = "hidden";
  Object.defineProperty(document, "visibilityState", {
    configurable: true,
    get: () => state,
  });
  document.dispatchEvent(new window.Event("visibilitychange"));
  assert.equal(reads, base);
  state = "visible";
  document.dispatchEvent(new window.Event("visibilitychange"));
  assert.equal(reads, base + 1);
  await off();
});

test("a dropped feed is listened to again, a refused one too, and none while signed out", async (t) => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  const answers = [
    { ok: false, body: null },
    "boom",
    { ok: true, body: new ReadableStream({ start: (c) => c.close() }) },
  ];
  let calls = 0;
  const api = async () => {
    const a = answers[calls++];
    if (a === "boom") {
      throw new Error("offline");
    }
    return a ?? { ok: false, body: null };
  };
  const off = mount(api, async () => {});
  await settle();
  assert.equal(calls, 1);
  for (let i = 2; i <= 3; i++) {
    t.mock.timers.tick(5000);
    await settle();
    assert.equal(calls, i);
  }
  await off();
  t.mock.timers.tick(60_000);
  await settle();
  assert.equal(calls, 3, "unmounting stops the retries");

  const signedOut = mount(api, async () => {}, false);
  await settle();
  assert.equal(calls, 3, "no feed without a login");
  await signedOut();
});

test("the sessions are read on their own event only, and both on connecting", async () => {
  const feed = feedResponse();
  let projects = 0;
  let sessions = 0;
  const off = mount(
    async () => feed.res,
    async () => {
      projects++;
    },
    true,
    async () => {
      sessions++;
    },
  );
  await later();
  assert.deepEqual([projects, sessions], [1, 1], "connecting reads both");
  feed.push('id: 0\nevent: sessions\ndata: {"changed":true}\n\n');
  await later();
  assert.deepEqual([projects, sessions], [1, 2]);
  feed.push('id: 0\nevent: projects\ndata: {"changed":true}\n\n');
  await later();
  assert.deepEqual([projects, sessions], [2, 2]);
  await off();
});

test("a gateway without the combined stream is followed on the projects' own", async (t) => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  const asked = [];
  const off = mount(
    async (path) => {
      asked.push(path);
      return { ok: false, status: 404, body: null };
    },
    async () => {},
  );
  await settle();
  t.mock.timers.tick(5000);
  await settle();
  assert.deepEqual(asked, ["/v1/events", "/v1/projects/events"]);
  await off();
});
