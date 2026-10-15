// Answering a report's suggestions, with the real card in jsdom (via tsx-hooks.mjs, since node
// cannot import TSX): the button pressed stays marked, the answers come back after a reload, and
// the turn that applies them is drawn as "Applying suggestions" instead of its prompt.
// Run with: node --test web/src/__tests__/suggestions.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { readFileSync } from "node:fs";
import { JSDOM } from "jsdom";
import { load } from "./tsx-hooks.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const dom = new JSDOM("<!doctype html><html><body></body></html>", {
  url: "http://localhost/",
});
for (const k of [
  "document",
  "window",
  "HTMLElement",
  "Node",
  "MutationObserver",
  "NodeFilter",
  "Event",
  "CustomEvent",
]) {
  globalThis[k] = dom.window[k];
}
Object.defineProperty(globalThis, "navigator", {
  value: dom.window.navigator,
  configurable: true,
});
globalThis.requestAnimationFrame = (cb) => setTimeout(() => cb(Date.now()), 0);
globalThis.cancelAnimationFrame = (id) => clearTimeout(id);

const { ReportCard } = await load("Report.tsx");
const { transcriptMessages } = await load("transcript.ts");
const { UserText } = await load("ConversationParts.tsx");
const { h, render } = await import("preact");
const { act } = await import("preact/test-utils");

const report = {
  status: "done",
  suggestions: [
    { title: "test the empty header" },
    { title: "share the helper" },
    { title: "log the dropped rows" },
  ],
};

function mount(props) {
  const root = document.createElement("div");
  document.body.appendChild(root);
  render(h(ReportCard, { report, ...props }), root);
  return root;
}

// The label the applying turn is drawn with.
const APPLYING = "Applying suggestions";

const rows = (root) => [...root.querySelectorAll(".rpt-suggest li")];
const buttons = (li) => [...li.querySelectorAll("button")];
const pressed = (li) =>
  buttons(li)
    .filter((b) => b.getAttribute("aria-pressed") === "true")
    .map((b) => b.textContent);

test("the button pressed stays marked, and the answer is handed over once all are settled", async () => {
  const got = [];
  const root = mount({ onAnswer: (a) => got.push(a) });
  await act(() => buttons(rows(root)[0])[0].click()); // apply this one
  let [first, second] = rows(root);
  assert.deepEqual(pressed(first), ["✓ Apply this one"]);
  assert.ok(buttons(first).every((b) => b.disabled), "an answered row cannot be answered again");
  assert.equal(got.length, 0, "nothing is sent while a suggestion is still open");

  await act(() => buttons(second)[2].click()); // apply none
  [first, second] = rows(root);
  const third = rows(root)[2];
  assert.deepEqual(pressed(first), ["✓ Apply this one"]);
  assert.deepEqual(pressed(second), ["✓ Apply none"]);
  assert.equal(buttons(third).length, 0, "a row settled by an earlier answer has no buttons");
  assert.match(third.textContent, /not applied/);

  assert.equal(got.length, 1);
  assert.deepEqual(got[0].picks, ["this", "none", ""]);
  assert.deepEqual(got[0].verdicts, ["apply", "skip", "skip"]);
  assert.match(got[0].request, /^Apply these suggestions you made, and nothing else:\n- test the empty header$/);
  root.remove();
});

test("answering none still hands the answer over, with nothing to send", async () => {
  const got = [];
  const root = mount({ onAnswer: (a) => got.push(a) });
  await act(() => buttons(rows(root)[0])[2].click());
  assert.deepEqual(got[0].picks, ["none", "", ""]);
  assert.equal(got[0].request, "");
  root.remove();
});

test("answers read back draw the same pressed buttons and offer nothing to press", () => {
  const root = mount({
    answers: { picks: ["rest", "", ""], verdicts: ["apply", "apply", "apply"], request: "x" },
    onAnswer: () => assert.fail("an answered card must not answer again"),
  });
  const [first, second] = rows(root);
  assert.deepEqual(pressed(first), ["✓ Apply this one and the following"]);
  assert.ok(buttons(first).every((b) => b.disabled));
  assert.equal(buttons(second).length, 0);
  assert.match(second.textContent, /will apply/);
  root.remove();
});

test("the turn that applies the suggestions is drawn by name, not by its prompt", () => {
  let id = 0;
  const nextId = () => {
    id += 1;
    return id;
  };
  const request = "Apply these suggestions you made, and nothing else:\n- test the empty header";
  const msgs = transcriptMessages(
    [
      { User: "fix the parser", Agent: "fixed" },
      { User: request, Agent: "applied" },
      { User: request, Agent: "again, typed by hand" },
    ],
    [
      {
        turn: 0,
        files: false,
        steps: [],
        outcome: { result: "fixed", report },
        answers: { picks: ["this", "none", ""], verdicts: ["apply", "skip", "skip"], request },
      },
      { turn: 1, files: false, steps: [], outcome: { result: "applied" } },
    ],
    nextId,
  );
  const users = msgs.filter((m) => m.role === "user");
  assert.equal(users[0].shown, undefined);
  assert.equal(users[1].shown, APPLYING);
  assert.equal(users[1].text, request, "the text sent is kept, only the label changes");
  assert.equal(users[2].shown, undefined, "only the turn right after the answered report is labelled");
  const answer = msgs.find((m) => m.report);
  assert.equal(answer.turn, 0);
  assert.deepEqual(answer.answers.picks, ["this", "none", ""]);
});

test("suggestions left unanswered when the conversation moved on are no longer offered", () => {
  const root = mount({ expired: true, onAnswer: () => assert.fail("expired") });
  assert.equal(root.querySelectorAll(".rpt-suggest button").length, 0);
  assert.ok(root.querySelector(".rpt-suggest.expired"));
  assert.match(root.querySelector(".rpt-expired").textContent, /no longer offered/);
  root.remove();
});

test("an answered card keeps its marks even when the conversation moved on", () => {
  const root = mount({
    expired: true,
    answers: { picks: ["none", "", ""], verdicts: ["skip", "skip", "skip"] },
  });
  assert.equal(root.querySelector(".rpt-expired"), null);
  assert.deepEqual(pressed(rows(root)[0]), ["✓ Apply none"]);
  root.remove();
});

test("the label folds the prompt it sent, to open on request", () => {
  const root = document.createElement("div");
  document.body.appendChild(root);
  render(h(UserText, { m: { id: 1, role: "user", text: "the whole prompt", shown: APPLYING } }), root);
  const det = root.querySelector("details.msg-shown");
  assert.ok(det, "a label is drawn as a fold");
  assert.equal(det.open, false, "closed until asked");
  assert.equal(det.querySelector("summary").textContent, APPLYING);
  assert.equal(det.querySelector("pre").textContent, "the whole prompt");
  render(h(UserText, { m: { id: 2, role: "user", text: "plain" } }), root);
  assert.equal(root.querySelector("details"), null);
  assert.equal(root.textContent, "plain");
  root.remove();
});

test("the conversation draws the label, the stream keeps the turn of the answer, and Spanish has it", () => {
  const src = (f) => readFileSync(join(here, "..", f), "utf8");
  assert.match(src("ConversationParts.tsx"), /<UserText m=\{head\} \/>/);
  assert.match(src("App.tsx"), /expired=\{expired\}/);
  assert.match(src("streamEvents.ts"), /turn: rs\.liveTurnRef\.current/);
  const hook = src("useSuggestions.ts");
  assert.match(hook, /t\("Applying suggestions"\)/);
  assert.match(hook, /method: "PUT"/);
  assert.match(hook, /"\/task"/, "applying is a task, never routed to the planner");
  assert.match(hook, /\/checkpoints\//);
});
