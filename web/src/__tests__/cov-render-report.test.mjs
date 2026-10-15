// The report card of a finished task (Report.tsx) and the full-window image preview it opens
// (Lightbox.tsx), run for real in jsdom.
// Run with: node --test web/src/__tests__/cov-render-report.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { setupDom, load, flush, text } from "./cov-render-helpers.mjs";

const dom = setupDom();
const W = dom.window;
globalThis.requestAnimationFrame = (cb) => setTimeout(() => cb(performance.now()), 0);
globalThis.cancelAnimationFrame = (id) => clearTimeout(id);

const { Lightbox } = await load("Lightbox.tsx");
const { ReportCard, hasReport } = await load("Report.tsx");
const { cancelQuiet } = await load("hydration.ts");
const { h, render } = await import("preact");
const { act } = await import("preact/test-utils");

async function mount(vnode) {
  const root = document.createElement("div");
  document.body.appendChild(root);
  await act(() => render(vnode, root));
  return {
    root,
    rerender: (v) => act(() => render(v, root)),
    async done() {
      await act(() => render(null, root));
      root.remove();
      cancelQuiet();
    },
  };
}

const fire = async (el, type, init = {}, Ctor = W.MouseEvent) => {
  const e = new Ctor(type, { bubbles: true, cancelable: true, ...init });
  await act(() => {
    el.dispatchEvent(e);
  });
  return e;
};
const press = async (key) => {
  const e = new W.KeyboardEvent("keydown", { key, bubbles: true, cancelable: true });
  await act(() => {
    document.body.dispatchEvent(e);
  });
  return e;
};
const lb = () => document.querySelector("[data-testid=lightbox]");
const zoomText = () => document.querySelector("[data-testid=lightbox-zoom]").textContent;
const imgEl = () => lb().querySelector("img.lb-img");
const button = (label) =>
  [...lb().querySelectorAll("button")].find(
    (b) => b.getAttribute("aria-label") === label || b.textContent.trim() === label,
  );
const PAIR = [
  { src: "blob:before", label: "Login — Before" },
  { src: "blob:after", label: "Login — After" },
];

// ── Lightbox ─────────────────────────────────────────────────────────────────

test("the preview opens in a portal on the image asked for, with a counter for a set", async () => {
  const { root, done } = await mount(
    h("div", {}, h(Lightbox, { images: PAIR, start: 5, onClose() {} })),
  );
  assert.equal(root.querySelector("[data-testid=lightbox]"), null, "drawn at the end of the body");
  assert.equal(lb().parentElement, document.body);
  assert.equal(lb().getAttribute("aria-label"), "Login — After");
  assert.equal(text(lb().querySelector(".lb-title")), "Login — After (2/2)");
  assert.equal(imgEl().getAttribute("src"), "blob:after");
  assert.equal(document.activeElement, button("Close"), "focus starts on Close");
  await done();
  assert.equal(lb(), null);
});

test("a single image has no counter, no arrows, and the arrow keys do nothing", async () => {
  const { done } = await mount(h(Lightbox, { images: [PAIR[0]], start: -3, onClose() {} }));
  assert.equal(text(lb().querySelector(".lb-title")), "Login — Before");
  assert.equal(button("Next image"), undefined);
  assert.equal(button("Previous image"), undefined);
  const e = await press("ArrowRight");
  assert.equal(e.defaultPrevented, false);
  assert.equal(imgEl().getAttribute("src"), "blob:before");
  await done();
});

test("the arrows, buttons and keys walk the set, wrapping around", async () => {
  const { done } = await mount(h(Lightbox, { images: PAIR, start: 0, onClose() {} }));
  await fire(button("Next image"), "click");
  assert.equal(imgEl().getAttribute("src"), "blob:after");
  await fire(button("Next image"), "click");
  assert.equal(imgEl().getAttribute("src"), "blob:before");
  await fire(button("Previous image"), "click");
  assert.equal(imgEl().getAttribute("src"), "blob:after");
  const e = await press("ArrowRight");
  assert.equal(e.defaultPrevented, true);
  assert.equal(imgEl().getAttribute("src"), "blob:before");
  await press("ArrowLeft");
  assert.equal(imgEl().getAttribute("src"), "blob:after");
  await done();
});

test("zoom by buttons and keys, between 10% and 1600%, and back to fit", async () => {
  const { done } = await mount(h(Lightbox, { images: PAIR, start: 0, onClose() {} }));
  assert.equal(zoomText(), "100%");
  await fire(button("Zoom in"), "click");
  assert.equal(zoomText(), "125%");
  await fire(button("Zoom out"), "click");
  assert.equal(zoomText(), "100%");
  await press("+");
  await press("=");
  assert.equal(zoomText(), "156%");
  await press("-");
  assert.equal(zoomText(), "125%");
  await press("0");
  assert.equal(zoomText(), "100%");
  for (let i = 0; i < 30; i++) await press("+");
  assert.equal(zoomText(), "1600%");
  for (let i = 0; i < 40; i++) await press("-");
  assert.equal(zoomText(), "10%");
  await fire(button("Fit"), "click");
  assert.equal(zoomText(), "100%");
  assert.match(imgEl().style.transform, /translate\(0px, 0px\) scale\(1\)/);
  const other = await press("q");
  assert.equal(other.defaultPrevented, false);
  await done();
});

test("1:1 shows the real pixels when the image's size is known, and fits otherwise", async () => {
  const { done } = await mount(h(Lightbox, { images: PAIR, start: 0, onClose() {} }));
  await press("+");
  await fire(button("1:1"), "click");
  assert.equal(zoomText(), "100%", "no natural size yet: back to fit");
  Object.defineProperty(imgEl(), "naturalWidth", { value: 1600, configurable: true });
  await fire(button("1:1"), "click");
  assert.equal(zoomText(), "100%", "not laid out: back to fit");
  Object.defineProperty(imgEl(), "clientWidth", { value: 400, configurable: true });
  await press("1");
  assert.equal(zoomText(), "400%");
  await done();
});

test("the wheel zooms around the pointer; a double click zooms in and then fits again", async () => {
  const { done } = await mount(h(Lightbox, { images: PAIR, start: 0, onClose() {} }));
  lb().getBoundingClientRect = () => ({ left: 0, top: 0, width: 200, height: 100 });
  const stage = lb().querySelector(".lb-stage");
  const wheel = await fire(stage, "wheel", { deltaY: -100, clientX: 200, clientY: 50 }, W.WheelEvent);
  assert.equal(wheel.defaultPrevented, true);
  assert.equal(zoomText(), "115%");
  // The point under the pointer (100px right of the centre) stays put: x = 100 - 100 * 1.15.
  const [x, y] = /translate\(([-\d.e]+)px, ([-\d.e]+)px\)/.exec(imgEl().style.transform).slice(1).map(Number);
  assert.ok(Math.abs(x + 15) < 1e-9 && y === 0, imgEl().style.transform);
  await fire(stage, "wheel", { deltaY: 100, clientX: 200, clientY: 50 }, W.WheelEvent);
  assert.equal(zoomText(), "100%");
  await fire(stage, "dblclick", { clientX: 100, clientY: 50 });
  assert.equal(zoomText(), "250%");
  await fire(stage, "dblclick", { clientX: 100, clientY: 50 });
  assert.equal(zoomText(), "100%");
  await done();
});

test("dragging pans the picture; a move without a press does nothing", async () => {
  const { done } = await mount(h(Lightbox, { images: PAIR, start: 0, onClose() {} }));
  const stage = lb().querySelector(".lb-stage");
  await fire(stage, "pointermove", { clientX: 50, clientY: 50 });
  assert.match(imgEl().style.transform, /translate\(0px, 0px\)/);
  await fire(stage, "pointerdown", { clientX: 10, clientY: 10 });
  await fire(stage, "pointermove", { clientX: 40, clientY: 25 });
  assert.match(imgEl().style.transform, /translate\(30px, 15px\)/);
  await fire(stage, "pointerup");
  await fire(stage, "pointermove", { clientX: 90, clientY: 90 });
  assert.match(imgEl().style.transform, /translate\(30px, 15px\)/);
  // Where the browser has pointer capture, the drag keeps the pointer.
  const captured = [];
  stage.setPointerCapture = (id) => captured.push(id);
  const down = new W.MouseEvent("pointerdown", { bubbles: true, clientX: 30, clientY: 15 });
  Object.defineProperty(down, "pointerId", { value: 7 });
  await act(() => {
    stage.dispatchEvent(down);
  });
  assert.deepEqual(captured, [7]);
  await fire(stage, "pointermove", { clientX: 40, clientY: 15 });
  await fire(stage, "pointercancel");
  assert.match(imgEl().style.transform, /translate\(40px, 15px\)/);
  await done();
});

test("a click around the image closes; on the image or in the bar it does not", async () => {
  const closed = [];
  const outside = [];
  const onDoc = () => outside.push(1);
  document.addEventListener("click", onDoc);
  const { done } = await mount(h(Lightbox, { images: PAIR, start: 0, onClose: () => closed.push(1) }));
  await fire(imgEl(), "click");
  assert.deepEqual(closed, []);
  assert.deepEqual(outside, [1], "a click on the picture is an ordinary click");
  outside.length = 0;
  await fire(lb().querySelector(".lb-title"), "click");
  assert.deepEqual(outside, [], "a click in the bar stays in the bar");
  await fire(lb().querySelector(".lb-stage"), "click");
  assert.deepEqual(closed, [1]);
  await fire(button("Close"), "click");
  assert.deepEqual(closed, [1, 1]);
  await press("Escape");
  assert.deepEqual(closed, [1, 1, 1]);
  await done();
  document.removeEventListener("click", onDoc);
});

test("with another dialog on top, the preview's keys are left to that dialog", async () => {
  const { done } = await mount(h(Lightbox, { images: PAIR, start: 0, onClose() {} }));
  const top = document.createElement("div");
  top.setAttribute("data-dialog-root", "");
  document.body.appendChild(top);
  const e = await press("+");
  assert.equal(e.defaultPrevented, false);
  assert.equal(zoomText(), "100%");
  top.remove();
  await done();
});

// ── hasReport ────────────────────────────────────────────────────────────────

test("a card is drawn only for a report with something beyond its summary", () => {
  assert.equal(hasReport(null), false);
  assert.equal(hasReport(undefined), false);
  assert.equal(hasReport({ summary: "only words" }), false);
  assert.equal(hasReport({ changes: [], risks: [] }), false);
  for (const key of ["changes", "verification", "evidence", "risks", "next_steps", "suggestions"]) {
    assert.equal(hasReport({ [key]: [{}] }), true, key);
  }
});

// ── ReportCard ───────────────────────────────────────────────────────────────

test("the card leads with the verdict and tallies, then the summary, the changes and the checks", async () => {
  const report = {
    status: "partial",
    summary: "Fixed **most** of it.",
    changes: [
      { path: "a.go", kind: "added", description: "new file" },
      { path: "b.go", kind: "weird" },
      { description: "no path, no kind" },
      { path: "a.go", kind: "added", description: "new file" },
    ],
    verification: [
      { check: "go test", result: "pass", evidence: "ok 12 tests" },
      { check: "lint", result: "fail" },
      { check: "e2e" },
      { check: "odd", result: "maybe" },
    ],
    risks: ["slow on big repos", "slow on big repos"],
    next_steps: ["ship it"],
  };
  const { root, done } = await mount(h(ReportCard, { report }));
  await flush(5);
  const card = root.querySelector(".rpt");
  assert.equal(card.className, "rpt rpt-partial");
  assert.equal(text(card.querySelector(".rpt-badge")), "Partly done");
  const [checks, changes] = card.querySelectorAll(".rpt-tally");
  assert.equal(checks.className, "rpt-tally bad");
  assert.equal(text(checks), "1/4 checks passed");
  assert.equal(text(changes), "4 changes");
  assert.equal(card.querySelector(".rpt-summary strong").textContent, "most");

  const items = [...card.querySelectorAll(".rpt-changes li")];
  assert.deepEqual(
    items.map((li) => text(li.querySelector(".rpt-kind"))),
    ["new", "changed", "changed", "new"],
  );
  assert.equal(items[1].querySelector(".rpt-kind").className, "rpt-kind weird");
  assert.equal(items[2].querySelector(".rpt-kind").className, "rpt-kind other");
  assert.equal(items[2].querySelector(".rpt-path"), null);
  assert.equal(items[1].querySelector(".rpt-desc"), null);

  const marks = [...card.querySelectorAll(".rpt-checks li")];
  assert.deepEqual(marks.map((li) => li.className), ["pass", "fail", "skipped", "maybe"]);
  assert.deepEqual(marks.map((li) => li.querySelector(".rpt-mark").textContent), ["✓", "✕", "–", "–"]);
  assert.equal(text(marks[0].querySelector(".rpt-evidence")), "ok 12 tests");
  assert.equal(marks[1].querySelector(".rpt-evidence"), null);

  assert.deepEqual([...card.querySelectorAll(".rpt-risks li")].map(text), ["slow on big repos", "slow on big repos"]);
  assert.deepEqual([...card.querySelectorAll(".rpt-next li")].map(text), ["ship it"]);
  await done();
});

test("a report with no lists draws only the verdict, and an unknown status reads as done", async () => {
  const { root, done } = await mount(h(ReportCard, { report: { status: "mystery" } }));
  const card = root.querySelector(".rpt");
  assert.equal(card.className, "rpt rpt-done");
  assert.equal(text(card), "Done");
  assert.equal(card.querySelector(".rpt-sec"), null);
  await done();
  const failed = await mount(h(ReportCard, { report: { status: "failed", changes: [{ path: "x" }] } }));
  assert.equal(text(failed.root.querySelector(".rpt-badge")), "Not completed");
  assert.equal(text(failed.root.querySelector(".rpt-tally")), "1 change");
  await failed.done();
  const none = await mount(h(ReportCard, { report: {} }));
  assert.equal(none.root.querySelector(".rpt").className, "rpt rpt-done");
  await none.done();
});

// deferred lets a test decide when an image load settles.
function deferred() {
  let resolve, reject;
  const promise = new Promise((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

test("evidence images load through the token, and open the preview with their other half", async () => {
  const pending = new Map();
  const loadImage = (name) => {
    const d = deferred();
    pending.set(name, d);
    return d.promise;
  };
  const report = {
    evidence: [
      { title: "Login", before: "b.png", after: "a.png", caption: "the button moved" },
      { before: "only-before.png", after: "" },
      { after: "broken.png" },
    ],
  };
  const { root, done } = await mount(h(ReportCard, { report, loadImage }));
  const sets = root.querySelectorAll(".rpt-evidence-set");
  assert.equal(sets.length, 3);
  assert.equal(text(sets[0].querySelector(".rpt-evidence-title")), "Login");
  assert.equal(text(sets[0].querySelector(".rpt-evidence-caption")), "the button moved");
  assert.equal(sets[1].querySelector(".rpt-evidence-title"), null);
  assert.equal(sets[1].querySelectorAll(".rpt-shot").length, 1, "an empty half is not drawn");
  assert.equal(sets[2].querySelector(".rpt-shot").className, "rpt-shot after");
  assert.deepEqual(
    [...root.querySelectorAll(".rpt-shot-empty")].map(text),
    ["Loading…", "Loading…", "Loading…", "Loading…"],
  );

  await act(async () => {
    pending.get("b.png").resolve("blob:b");
    pending.get("a.png").resolve("blob:a");
    pending.get("only-before.png").resolve(null);
    pending.get("broken.png").reject(new Error("401"));
  });
  await flush(3);
  const [before, after] = sets[0].querySelectorAll(".rpt-shot-btn");
  assert.equal(before.querySelector("img").getAttribute("src"), "blob:b");
  assert.equal(before.querySelector("img").alt, "Login — Before");
  assert.equal(before.title, "Click to enlarge");
  assert.equal(text(sets[1].querySelector(".rpt-shot-empty")), "Image not available");
  assert.equal(text(sets[2].querySelector(".rpt-shot-empty")), "Image not available");

  await fire(before, "click");
  assert.equal(lb().getAttribute("aria-label"), "Login — Before");
  assert.equal(text(lb().querySelector(".lb-title")), "Login — Before (1/2)");
  await press("ArrowRight");
  assert.equal(imgEl().getAttribute("src"), "blob:a");
  assert.equal(imgEl().alt, "Login — After");
  await fire(button("Close"), "click");
  assert.equal(lb(), null);

  await fire(after, "click");
  assert.equal(text(lb().querySelector(".lb-title")), "Login — After (2/2)");
  await press("ArrowLeft");
  assert.equal(imgEl().alt, "Login — Before");
  await press("Escape");
  assert.equal(lb(), null);
  await done();
});

test("a half whose pair never loaded opens alone; a card without a loader shows no image", async () => {
  const report = {
    evidence: [
      { title: "Pair", before: "ok.png", after: "missing.png" },
      { title: "Lone", after: "lone.png" },
    ],
  };
  const loadImage = async (name) => (name === "missing.png" ? null : `blob:${name}`);
  const { root, done } = await mount(h(ReportCard, { report, loadImage }));
  await flush(5);
  await fire(root.querySelector(".rpt-shot.before .rpt-shot-btn"), "click");
  assert.equal(text(lb().querySelector(".lb-title")), "Pair — Before");
  await press("Escape");
  const lone = [...root.querySelectorAll(".rpt-evidence-set")][1];
  await fire(lone.querySelector(".rpt-shot-btn"), "click");
  assert.equal(text(lb().querySelector(".lb-title")), "Lone — After");
  await press("Escape");
  await done();

  const bare = await mount(h(ReportCard, { report: { evidence: [{ before: "x.png" }] } }));
  await flush(5);
  assert.equal(text(bare.root.querySelector(".rpt-shot-empty")), "Image not available");
  assert.equal(text(bare.root.querySelector(".rpt-shot figcaption")), "Before");
  await bare.done();
});

test("an image that settles after the card is gone is dropped", async () => {
  const ok = deferred();
  const bad = deferred();
  const loadImage = (name) => (name === "ok.png" ? ok.promise : bad.promise);
  const { root, done } = await mount(
    h(ReportCard, { report: { evidence: [{ before: "ok.png", after: "bad.png" }] }, loadImage }),
  );
  await done();
  ok.resolve("blob:late");
  bad.reject(new Error("late"));
  await flush(5);
  assert.equal(root.innerHTML, "");
});

// ── suggestions ──────────────────────────────────────────────────────────────

const SUGGESTIONS = [
  { title: "test the empty header", detail: "it panics" },
  { title: "share the helper" },
  { title: "log it", detail: "at debug" },
];
const suggestionRows = (root) => [...root.querySelectorAll(".rpt-suggest li")];
const answerButtons = (li) => [...li.querySelectorAll(".rpt-suggest-actions button")];

test("suggestions are asked one at a time; the answers are sent once every one has one", async () => {
  const sent = [];
  const { root, done } = await mount(
    h(ReportCard, { report: { suggestions: SUGGESTIONS }, onAnswer: (a) => sent.push(a) }),
  );
  let rows = suggestionRows(root);
  assert.deepEqual(rows.map((li) => li.className), ["current", "", ""]);
  assert.equal(text(rows[0].querySelector(".rpt-evidence")), "it panics");
  assert.equal(rows[1].querySelector(".rpt-evidence"), null);
  assert.equal(answerButtons(rows[1]).length, 0, "only the current one is asked");
  assert.deepEqual(answerButtons(rows[0]).map(text), [
    "Apply this one",
    "Apply this one and the following",
    "Apply none",
  ]);

  await fire(answerButtons(rows[0])[0], "click");
  rows = suggestionRows(root);
  assert.deepEqual(rows.map((li) => li.className), ["apply", "current", ""]);
  const picked = answerButtons(rows[0]);
  assert.equal(text(picked[0]), "✓ Apply this one");
  assert.equal(picked[0].className, "picked");
  assert.equal(picked[0].getAttribute("aria-pressed"), "true");
  assert.equal(picked[1].getAttribute("aria-pressed"), "false");
  assert.ok(picked.every((b) => b.disabled), "an answered suggestion cannot be answered again");
  assert.equal(text(rows[0].querySelector(".rpt-verdict")), "will apply");
  assert.deepEqual(sent, []);

  await fire(answerButtons(rows[1])[2], "click");
  rows = suggestionRows(root);
  assert.deepEqual(rows.map((li) => li.className), ["apply", "skip", "skip"]);
  assert.equal(text(rows[2].querySelector(".rpt-verdict")), "not applied");
  assert.equal(answerButtons(rows[2]).length, 0, "settled by the earlier answer, never pressed");
  assert.equal(sent.length, 1);
  assert.deepEqual(sent[0].picks, ["this", "none", ""]);
  assert.deepEqual(sent[0].verdicts, ["apply", "skip", "skip"]);
  assert.equal(
    sent[0].request,
    "Apply these suggestions you made, and nothing else:\n- test the empty header: it panics",
  );
  await done();
});

test("apply this one and the following settles the rest at once", async () => {
  const sent = [];
  const { root, done } = await mount(
    h(ReportCard, { report: { suggestions: SUGGESTIONS }, onAnswer: (a) => sent.push(a) }),
  );
  await fire(answerButtons(suggestionRows(root)[0])[1], "click");
  assert.deepEqual(suggestionRows(root).map((li) => li.className), ["apply", "apply", "apply"]);
  assert.deepEqual(sent[0].picks, ["rest", "", ""]);
  await done();
});

test("while a run is in flight the suggestions are shown but cannot be answered", async () => {
  const { root, done } = await mount(h(ReportCard, { report: { suggestions: SUGGESTIONS } }));
  const buttons = answerButtons(suggestionRows(root)[0]);
  assert.equal(buttons.length, 3);
  assert.ok(buttons.every((b) => b.disabled));
  await done();
});

test("answers given back redraw what was chosen", async () => {
  const answers = {
    picks: ["none", "", ""],
    verdicts: ["skip", "skip", "skip"],
    request: "",
  };
  const sent = [];
  const { root, done } = await mount(
    h(ReportCard, {
      report: { suggestions: SUGGESTIONS },
      answers,
      auto: true,
      onAnswer: (a) => sent.push(a),
    }),
  );
  const rows = suggestionRows(root);
  assert.deepEqual(rows.map((li) => li.className), ["skip", "skip", "skip"]);
  assert.equal(text(answerButtons(rows[0])[2]), "✓ Apply none");
  assert.deepEqual(sent, [], "nothing is sent again");
  await done();
});

test("a list left unanswered when the conversation moved on is no longer offered", async () => {
  const { root, done } = await mount(
    h(ReportCard, { report: { suggestions: SUGGESTIONS }, expired: true, onAnswer() {} }),
  );
  const sec = root.querySelector(".rpt-suggest");
  assert.equal(sec.className, "rpt-sec rpt-suggest expired");
  assert.equal(
    text(sec.querySelector(".rpt-expired")),
    "Not answered before the conversation moved on; no longer offered.",
  );
  assert.deepEqual(suggestionRows(root).map((li) => li.className), ["", "", ""]);
  assert.equal(root.querySelectorAll(".rpt-suggest-actions").length, 0);
  await done();

  // Half answered before it expired: it keeps its marks.
  const half = await mount(
    h(ReportCard, {
      report: { suggestions: SUGGESTIONS },
      expired: true,
      answers: { picks: ["this", "", ""], verdicts: ["apply", undefined, undefined] },
      onAnswer() {},
    }),
  );
  assert.equal(half.root.querySelector(".rpt-expired"), null);
  assert.deepEqual(suggestionRows(half.root).map((li) => li.className), ["apply", "current", ""]);
  await half.done();
});

test("in auto mode the needed suggestions are marked, and a list of only needed ones applies itself once", async () => {
  const needed = [
    { title: "fix the nil check", needed: true, why_needed: "it crashes on start" },
    { title: "add the test", needed: true },
  ];
  const sent = [];
  const props = { report: { suggestions: needed }, auto: true, onAnswer: (a) => sent.push(a) };
  const { root, rerender, done } = await mount(h(ReportCard, props));
  const tags = [...root.querySelectorAll(".rpt-needed")];
  assert.equal(text(tags[0]), "needed: it crashes on start");
  assert.equal(tags[0].title, "it crashes on start");
  assert.equal(text(tags[1]), "needed");
  assert.equal(sent.length, 1);
  assert.deepEqual(sent[0].verdicts, ["apply", "apply"]);
  assert.deepEqual(sent[0].picks, ["", ""]);
  await rerender(h(ReportCard, { ...props }));
  assert.equal(sent.length, 1, "applied once");
  await done();

  // Mixed: the needed one is accepted, the other is asked.
  const mixed = await mount(
    h(ReportCard, {
      report: { suggestions: [needed[0], { title: "rename it" }] },
      auto: true,
      onAnswer: (a) => sent.push(a),
    }),
  );
  assert.deepEqual(suggestionRows(mixed.root).map((li) => li.className), ["apply", "current"]);
  assert.equal(sent.length, 1);
  await mixed.done();

  // Outside auto mode the mark is not shown and nothing is applied; nor while a run is in flight.
  const manual = await mount(h(ReportCard, { report: { suggestions: needed }, onAnswer: (a) => sent.push(a) }));
  assert.equal(manual.root.querySelector(".rpt-needed"), null);
  await manual.done();
  const busy = await mount(h(ReportCard, { report: { suggestions: needed }, auto: true }));
  assert.deepEqual(suggestionRows(busy.root).map((li) => li.className), ["apply", "apply"]);
  await busy.done();
  const stale = await mount(
    h(ReportCard, { report: { suggestions: needed }, auto: true, expired: true, onAnswer: (a) => sent.push(a) }),
  );
  await stale.done();
  assert.equal(sent.length, 1);
});
