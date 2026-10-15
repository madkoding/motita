// The artifacts of a session (useArtifacts.ts) while the person moves between sessions: nothing
// of the old session stays on screen, and answers for it that arrive late are dropped.
// Run with: node --test web/src/__tests__/cov-artifacts-switch.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { setupDom, load } from "./cov-render-helpers.mjs";

setupDom();
const { useArtifacts } = await load("useArtifacts.ts");
const { h, render } = await import("preact");
const { act } = await import("preact/test-utils");

// settle lets the fake gateway's responses (and the reads of their bodies) arrive.
const settle = async (n = 8) => {
  for (let i = 0; i < n; i++) {
    await act(() => new Promise((r) => setImmediate(r)));
  }
};

const json = (body, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const A = { name: "a.md", size: 1, type: "text/markdown", modified: "" };
const B = { name: "b.md", size: 2, type: "text/markdown", modified: "" };

// gateway is a fake api answering from `route(url)`; every requested url is kept.
function gateway(route) {
  const calls = [];
  const api = async (url) => {
    calls.push(url);
    return route(url);
  };
  return { api, calls };
}

// switchable mounts the hook over a session that the test can change, as the sidebar would.
async function switchable(api, first) {
  const hook = { current: null };
  function Harness({ sid }) {
    hook.current = useArtifacts(api, sid, () => {});
    return null;
  }
  const root = document.createElement("div");
  document.body.appendChild(root);
  const show = (sid) => act(() => render(h(Harness, { sid }), root));
  await show(first);
  return {
    hook,
    show,
    async done() {
      await act(() => render(null, root));
      root.remove();
    },
  };
}

// deferred is a reply the test releases when it wants it to arrive.
function deferred() {
  let release;
  const promise = new Promise((r) => {
    release = r;
  });
  return { promise, release };
}

test("moving to another session drops the old list and file, and reloads an open list", async () => {
  const g = gateway((url) => {
    if (url.includes("/s1/")) {
      return json({ artifacts: [A] });
    }
    return json({ artifacts: [B] });
  });
  const m = await switchable(g.api, "s1");
  await act(() => m.hook.current.openArtifacts());
  await settle();
  await act(() => m.hook.current.setArtifactView({ name: "x", type: "text/plain", body: "x" }));
  assert.deepEqual(m.hook.current.artifacts, [A]);
  await m.show("s2");
  await settle();
  assert.equal(m.hook.current.artifactView, null);
  assert.deepEqual(m.hook.current.artifacts, [B]);
  // With the list closed, a new session only clears what was there.
  await act(() => m.hook.current.closeArtifacts());
  await m.show("s3");
  await settle();
  assert.deepEqual(m.hook.current.artifacts, []);
  assert.equal(g.calls.length, 2);
  await m.done();
});

test("an answer for a session the person already left is dropped", async () => {
  const late = { list: deferred(), fail: deferred(), text: deferred(), image: deferred() };
  const g = gateway((url) => {
    if (url.endsWith("/s1/artifacts")) {
      return late.list.promise;
    }
    if (url.endsWith("/s1/artifacts?scope=project")) {
      return late.fail.promise.then(() => {
        throw new Error("offline");
      });
    }
    if (url.endsWith("/note.txt")) {
      return late.text.promise;
    }
    return late.image.promise;
  });
  const m = await switchable(g.api, "s1");
  let pending;
  await act(() => {
    pending = [
      m.hook.current.pickScope("session"),
      m.hook.current.pickScope("project"),
      m.hook.current.openArtifact({ name: "note.txt", type: "text/plain" }),
      m.hook.current.openArtifact({ name: "pic.png", type: "image/png" }),
    ];
  });
  await m.show("s2");
  late.list.release(json({ artifacts: [A] }));
  late.fail.release();
  late.text.release(new Response("old"));
  late.image.release(new Response(new Blob(["png"])));
  await settle();
  await act(() => Promise.all(pending));
  assert.deepEqual(m.hook.current.artifacts, []);
  assert.equal(m.hook.current.artifactView, null);
  await m.done();
});
