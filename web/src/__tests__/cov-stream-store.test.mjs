// The app store: every part of the page's state gathered by useAppStore and handed down through
// AppCtx. Mounted whole against a fake gateway; conversation mode is replaced by a stand-in that
// keeps the `send` the store gives it, so the test can speak a turn.
import test from "node:test";
import assert from "node:assert/strict";
import { installDom, source, stub, flush, res } from "./cov-stream-helpers.mjs";

installDom();
window.matchMedia = () => ({
  matches: false,
  addEventListener() {},
  removeEventListener() {},
});
stub(
  "./useVoiceConversation",
  "export function useVoiceConversation(o) { globalThis.covStreamConversation = o; }",
);
const { useAppStore, useApp, AppCtx } = await source("appStore.ts");
const { h, render } = await import("preact");
const { act } = await import("preact/test-utils");

const calls = [];
async function api(path, init = {}) {
  calls.push([(init.method || "GET") + " " + path, init.body]);
  if (path === "/v1/sessions") {
    return res(200, { sessions: [] });
  }
  return res(404, {});
}

test("the store gathers the page's state and hands it down", async () => {
  let store;
  let seen;
  function Reader() {
    seen = useApp();
    return null;
  }
  function App() {
    store = useAppStore(api);
    return h(AppCtx.Provider, { value: store }, h(Reader));
  }
  const root = document.createElement("div");
  document.body.appendChild(root);
  await act(() => render(h(App), root));
  await act(() => flush());
  assert.equal(seen, store, "a component under the app reads the same store");
  assert.equal(store.api, api);
  for (const k of [
    "rs",
    "lists",
    "conv",
    "actions",
    "stream",
    "composer",
    "auth",
    "updates",
    "voice",
    "speech",
    "menus",
  ]) {
    assert.ok(store[k], k);
  }
  assert.equal(store.selectedSession, undefined);
  assert.equal(store.isMerged, false);

  // What the voice settings say reaches speech: the store re-renders with it.
  const info = { tts: { enabled: false }, local: { enabled: false } };
  await act(() => store.voice.setInfo(info));
  assert.equal(store.voice.info, info);
  assert.equal(seen, store);

  // Settings open and close.
  assert.equal(store.showSettings, false);
  await act(() => store.openSettings());
  assert.equal(store.showSettings, true);
  await act(() => store.closeSettings());
  assert.equal(store.showSettings, false);

  // The open session is looked up in the list; a merged one says so.
  await act(() => {
    store.lists.setSessions([{ id: "s1", title: "One", merged: true }]);
    store.rs.setSessionId("s1");
    store.rs.sessionRef.current = "s1";
  });
  assert.equal(store.selectedSession.title, "One");
  assert.equal(store.isMerged, true);

  // Conversation mode sends what it heard as a new turn, dropping the notes of the last one.
  const conv = globalThis.covStreamConversation;
  assert.equal(conv.on, store.speech.prefs.conversation);
  await act(() =>
    store.rs.setMessages([
      { id: 1, role: "agent", kind: "kind", text: "(note)" },
      { id: 2, role: "agent", text: "answer" },
    ]),
  );
  calls.length = 0;
  await act(async () => {
    globalThis.covStreamConversation.send("what next");
    await flush();
  });
  assert.deepEqual(calls[0], [
    "POST /v1/sessions/s1/run",
    '{"task":"what next"}',
  ]);
  assert.deepEqual(
    store.rs.messages.slice(0, 2).map((m) => m.text),
    ["answer", "what next"],
  );
  // While a turn runs, nothing heard is sent.
  calls.length = 0;
  store.rs.runningRef.current = true;
  await act(() => globalThis.covStreamConversation.send("ignored"));
  assert.deepEqual(calls, []);
  store.rs.runningRef.current = false;
  await act(() => render(null, root));
  root.remove();
});
