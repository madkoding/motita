// Answering a report's suggestions, with the real card in jsdom (bundled with esbuild, since node
// cannot import TSX): the button pressed stays marked, the answers come back after a reload, and
// the turn that applies them is drawn as "Applying suggestions" instead of its prompt.
// Run with: node --test web/src/__tests__/suggestions.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { fileURLToPath, pathToFileURL } from "node:url";
import { dirname, join } from "node:path";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { JSDOM } from "jsdom";
import { build } from "esbuild";

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

const out = mkdtempSync(join(here, ".sg-"));
await build({
  stdin: {
    contents: `export { ReportCard } from "./Report";
export { transcriptMessages } from "./transcript";`,
    resolveDir: join(here, ".."),
    loader: "ts",
    sourcefile: "entry.ts",
  },
  outfile: join(out, "sg.mjs"),
  bundle: true,
  format: "esm",
  jsx: "automatic",
  jsxImportSource: "preact",
  platform: "node",
  external: ["preact", "preact/*"],
  loader: { ".css": "empty" },
  logLevel: "silent",
});
const { ReportCard, transcriptMessages } = await import(
  pathToFileURL(join(out, "sg.mjs")).href
);
const { h, render } = await import("preact");
const { act } = await import("preact/test-utils");
rmSync(out, { recursive: true });

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
  const nextId = () => ++id;
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
  assert.equal(users[1].shown, "Applying suggestions");
  assert.equal(users[1].text, request, "the text sent is kept, only the label changes");
  assert.equal(users[2].shown, undefined, "only the turn right after the answered report is labelled");
  const answer = msgs.find((m) => m.report);
  assert.equal(answer.turn, 0);
  assert.deepEqual(answer.answers.picks, ["this", "none", ""]);
});

test("the conversation draws the label, the stream keeps the turn of the answer, and Spanish has it", () => {
  const src = (f) => readFileSync(join(here, "..", f), "utf8");
  assert.match(src("ConversationParts.tsx"), /head\.shown \?\? head\.text/);
  assert.match(src("streamEvents.ts"), /turn: rs\.liveTurnRef\.current/);
  assert.match(src("useSuggestions.ts"), /t\("Applying suggestions"\)/);
  assert.match(src("useSuggestions.ts"), /method: "PUT"/);
  assert.match(src("useSuggestions.ts"), /\/checkpoints\//);
});
