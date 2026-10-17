// The suggestion for the next request: asked when a turn ends, dropped when the next one starts.
// Run with: node --test web/src/__tests__/cov-chat-nextsuggestion.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { act, fakeApi, json, load, renderHook } from "./cov-chat-helpers.mjs";

const { useNextSuggestion, nextSuggestion } = await load(
  "useNextSuggestion.ts",
);

const MESSAGES = [
  { id: 1, role: "user", text: "fix the parser" },
  {
    id: 2,
    role: "agent",
    text: "Fixed.",
    report: { summary: "Parser fixed" },
    changes: {
      files: [{ path: "parser.go", status: "M", added: 1, deleted: 0 }],
    },
  },
];

const settle = () => act(() => new Promise((r) => setTimeout(r, 0)));

function mountWith(api, over = {}) {
  return renderHook((p) => useNextSuggestion(p), {
    api,
    sessionId: "s1",
    running: false,
    messages: MESSAGES,
    ...over,
  });
}

test("the request goes to the session's endpoint with the language", async () => {
  const api = fakeApi(() => json({ text: " Add tests " }));
  assert.equal(
    await nextSuggestion(api, "a/b", { request: "r", answer: "a" }),
    "Add tests",
  );
  assert.equal(api.calls[0].path, "/v1/sessions/a%2Fb/next-suggestion");
  assert.deepEqual(api.calls[0].body, {
    request: "r",
    answer: "a",
    lang: "en",
  });
  const empty = fakeApi(() => json({}));
  assert.equal(
    await nextSuggestion(empty, "s", { request: "", answer: "" }),
    "",
  );
  const down = fakeApi(() => json({ error: "x" }, 502));
  await assert.rejects(nextSuggestion(down, "s", { request: "", answer: "" }));
});

test("a finished turn gets a suggestion, and the next turn drops it", async () => {
  const api = fakeApi(() => json({ text: "Add tests" }));
  const hook = await mountWith(api, { running: true });
  assert.equal(hook.result.current, "");
  await hook.rerender({
    api,
    sessionId: "s1",
    running: false,
    messages: MESSAGES,
  });
  await settle();
  assert.equal(hook.result.current, "Add tests");
  assert.deepEqual(api.calls[0].body.request, "fix the parser");
  assert.equal(api.calls[0].body.answer, "Parser fixed");
  assert.deepEqual(api.calls[0].body.files, ["parser.go"]);
  await hook.rerender({
    api,
    sessionId: "s1",
    running: true,
    messages: MESSAGES,
  });
  assert.equal(hook.result.current, "");
  await hook.unmount();
});

test("the settings switch turns the suggestion off", async () => {
  localStorage.setItem("motita.ui.nextHint", "off");
  const api = fakeApi(() => json({ text: "Add tests" }));
  const hook = await mountWith(api, { running: true });
  await hook.rerender({
    api,
    sessionId: "s1",
    running: false,
    messages: MESSAGES,
  });
  await settle();
  assert.equal(api.calls.length, 0);
  assert.equal(hook.result.current, "");
  localStorage.removeItem("motita.ui.nextHint");
  await hook.unmount();
});

test("a failing model leaves the usual hint, and loading a session asks nothing", async () => {
  const api = fakeApi(() => json({ error: "x" }, 502));
  const hook = await mountWith(api, { running: true });
  await hook.rerender({
    api,
    sessionId: "s1",
    running: false,
    messages: MESSAGES,
  });
  await settle();
  assert.equal(hook.result.current, "");
  assert.equal(api.calls.length, 1);
  // Not running before and after: nothing ended, nothing asked.
  await hook.rerender({
    api,
    sessionId: "s2",
    running: false,
    messages: MESSAGES,
  });
  await settle();
  assert.equal(api.calls.length, 1);
  await hook.unmount();
});

test("no api, no answer, or an answer that arrives after the session moved on", async () => {
  const bare = await mountWith(null, { running: true });
  await bare.rerender({
    api: null,
    sessionId: "s1",
    running: false,
    messages: MESSAGES,
  });
  assert.equal(bare.result.current, "");
  const api = fakeApi(() => json({ text: "late" }));
  const noAnswer = await mountWith(api, { running: true, messages: [] });
  await noAnswer.rerender({
    api,
    sessionId: "s1",
    running: false,
    messages: [],
  });
  assert.equal(api.calls.length, 0);
  let release;
  const slow = fakeApi(
    () => new Promise((r) => (release = () => r(json({ text: "late" })))),
  );
  const hook = await mountWith(slow, { running: true });
  await hook.rerender({
    api: slow,
    sessionId: "s1",
    running: false,
    messages: MESSAGES,
  });
  await hook.rerender({
    api: slow,
    sessionId: "s2",
    running: false,
    messages: MESSAGES,
  });
  release();
  await settle();
  assert.equal(hook.result.current, "");
  await hook.unmount();
});

test("an answer that changed nothing sends no files", async () => {
  for (const changes of [undefined, {}]) {
    const api = fakeApi(() => json({ text: "Next" }));
    const messages = [
      MESSAGES[0],
      { id: 2, role: "agent", text: "Done.", changes },
    ];
    const hook = await mountWith(api, { running: true, messages });
    await hook.rerender({ api, sessionId: "s1", running: false, messages });
    await settle();
    assert.deepEqual(api.calls[0].body.files, []);
    await hook.unmount();
  }
});
