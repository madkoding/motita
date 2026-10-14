// Regression tests for what the conversation re-renders and keeps in the page: memoized turns,
// drawers that mount nothing while closed, and built messages released once far away.
// The modules import their siblings without extensions, so they are bundled with esbuild.
import test from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { build } from "esbuild";
import { JSDOM } from "jsdom";

const src = join(dirname(fileURLToPath(import.meta.url)), "..");
const dom = new JSDOM("<!doctype html><html><body></body></html>", {
  url: "http://localhost/",
});
globalThis.window = dom.window;
globalThis.document = dom.window.document;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.CustomEvent = dom.window.CustomEvent;

// A controllable IntersectionObserver: each instance is kept with its margin, so a test can say
// which elements its observer sees.
const observers = [];
globalThis.IntersectionObserver = class {
  constructor(cb, opts) {
    this.cb = cb;
    this.margin = opts?.rootMargin;
    this.targets = new Set();
    observers.push(this);
  }
  observe(el) { this.targets.add(el); }
  unobserve(el) { this.targets.delete(el); }
  disconnect() { this.targets.clear(); }
  fire(el, isIntersecting) { this.cb([{ target: el, isIntersecting }]); }
};

// Inside web/, so the bundles resolve the same preact as this test.
const dir = mkdtempSync(join(src, "..", "node_modules", ".rendercost-"));
async function load(entry) {
  const out = join(dir, entry.replace(/\W/g, "_") + ".mjs");
  await build({
    entryPoints: [join(src, entry)],
    bundle: true, format: "esm", platform: "node", outfile: out, logLevel: "silent",
    jsx: "automatic", jsxImportSource: "preact",
    // One preact for the test and the bundle: two copies would not share hooks.
    external: ["preact", "preact/*"],
  });
  return import(out);
}
const TV = await load("turnView.ts");
const DW = await load("drawerWindow.ts");
const HY = await load("hydration.ts");
const { h, render } = await import("preact");
const { act } = await import("preact/test-utils");
rmSync(dir, { recursive: true, force: true });

const flush = () => new Promise((r) => setTimeout(r, 0));

// ── Turns ────────────────────────────────────────────────────────────────────

test("a turn whose messages and steps did not change is the same turn", () => {
  const a = { id: 1, role: "user", text: "hi" };
  const b = { id: 2, role: "agent", text: "hello" };
  const steps = [{ kind: "step", text: "x" }];
  const base = { g: { key: 1, turn: 1, items: [a, b] }, steps, liveHere: false, activity: null };
  // The groups are rebuilt on every render: a new group holding the same messages is the same turn.
  assert.ok(TV.sameTurn(base, { ...base, g: { key: 1, turn: 1, items: [a, b] } }));
  assert.ok(!TV.sameTurn(base, { ...base, g: { key: 1, turn: 1, items: [a, { ...b, text: "hello!" }] } }),
    "a streamed token makes a new message object: that turn renders again");
  assert.ok(!TV.sameTurn(base, { ...base, steps: [...steps] }), "new steps render the turn");
  assert.ok(!TV.sameTurn(base, { ...base, liveHere: true }), "the run reaching it renders it");
  assert.ok(!TV.sameTurn(base, { ...base, g: { key: 1, turn: 1, items: [a] } }));
});

test("the newest input of the user is what makes old suggestions stale", () => {
  assert.equal(TV.lastUserIdOf([]), -1);
  assert.equal(TV.lastUserIdOf([{ id: 1, role: "user" }, { id: 5, role: "agent" }, { id: 7, role: "user" }, { id: 9, role: "agent" }]), 7);
});

function fakeStore(over = {}) {
  return {
    rs: { running: false, activityKind: "", cpInfo: {}, messages: [{ id: 1, role: "user", text: "q" }], ...over.rs },
    checkpoints: { cpMenu: null, cpArmed: false },
    speech: { speakingId: null, supported: true },
    stream: { stopRun() {}, submit() {} },
    artifacts: { showRunArtifact() {} },
    answerSuggestions() {},
  };
}

test("the turn view keeps its identity while only a streaming answer changes", () => {
  const seen = [];
  function Probe({ store }) { seen.push(TV.useTurnView(store)); return null; }
  const root = document.createElement("div");
  const cpInfo = {};
  const msgs = (text) => [{ id: 1, role: "user", text: "q" }, { id: 2, role: "agent", text }];
  act(() => render(h(Probe, { store: fakeStore({ rs: { cpInfo, messages: msgs("a") } }) }), root));
  act(() => render(h(Probe, { store: fakeStore({ rs: { cpInfo, messages: msgs("ab") } }) }), root));
  assert.equal(seen[0], seen[1], "a token does not hand the turns a new value");
  act(() => render(h(Probe, { store: fakeStore({ rs: { cpInfo, messages: msgs("ab"), running: true } }) }), root));
  assert.notEqual(seen[1], seen[2], "a run starting does");
  assert.equal(seen[2].running, true);
  assert.equal(seen[0].stopRun, seen[2].stopRun, "the actions never change identity");
  render(null, root);
});

// ── Drawers ──────────────────────────────────────────────────────────────────

test("a drawer window shows the newest entries", () => {
  assert.equal(DW.windowStart(10, DW.DRAWER_WINDOW), 0);
  assert.equal(DW.windowStart(500, DW.DRAWER_WINDOW), 500 - DW.DRAWER_WINDOW);
});

test("a closed drawer mounts nothing once it has finished closing", async () => {
  let last;
  function Probe({ open }) { last = DW.useDrawerContent(open); return null; }
  const root = document.createElement("div");
  act(() => render(h(Probe, { open: false }), root));
  assert.equal(last.mounted, false, "closed by default: nothing mounted");
  act(() => render(h(Probe, { open: true }), root));
  assert.equal(last.mounted, true, "the render that opens it draws the content");
  act(() => last.showEarlier());
  assert.equal(last.shown, 2 * DW.DRAWER_WINDOW, "earlier entries are added on request");
  act(() => render(h(Probe, { open: false }), root));
  assert.equal(last.mounted, true, "kept while the panel slides out");
  await new Promise((r) => setTimeout(r, DW.DRAWER_LINGER_MS + 50));
  act(() => {});
  assert.equal(last.mounted, false, "gone once it has closed");
  assert.equal(last.shown, DW.DRAWER_WINDOW, "and the window is back to its size");
  render(null, root);
});

// ── Far messages ─────────────────────────────────────────────────────────────

test("a built message far out of view is released, at its height, and built again on return", async () => {
  const el = document.createElement("div");
  document.body.appendChild(el);
  let builds = 0;
  let releases = 0;
  const stop = HY.watchForHydration(el, () => { builds++; }, () => { releases++; return true; });
  const near = observers.find((o) => o.margin === "600px 0px");
  const far = observers.find((o) => o.margin === "4000px 0px");
  assert.ok(near && far, "a near and a far observer watch the message");
  assert.ok(far.targets.has(el));

  near.fire(el, true);
  await flush();
  assert.equal(builds, 1, "built on approach");
  assert.equal(el.dataset.hydrating, undefined);

  near.fire(el, false);
  far.fire(el, false);
  assert.equal(releases, 1, "released once far away");
  assert.equal(el.dataset.hydrating, "1", "and pending again");
  assert.match(el.style.minHeight, /px$/, "its height is locked so the page does not move");

  near.fire(el, true);
  await flush();
  assert.equal(builds, 2, "built again on the way back");
  assert.equal(el.style.minHeight, "", "with its own height back");

  stop();
  assert.ok(!far.targets.has(el), "unwatched on unmount");
  HY.cancelQuiet();
});

test("a message still building, or near the reader, is never released", async () => {
  const el = document.createElement("div");
  let releases = 0;
  const stop = HY.watchForHydration(el, () => new Promise(() => {}), () => { releases++; return true; });
  const near = observers.find((o) => o.margin === "600px 0px");
  const far = observers.find((o) => o.margin === "4000px 0px");
  far.fire(el, false);
  assert.equal(releases, 0, "never built: nothing to release");
  near.fire(el, true);
  near.fire(el, false);
  far.fire(el, false);
  assert.equal(releases, 0, "still building: its work would land on the placeholders");
  stop();
  HY.cancelQuiet();
});
