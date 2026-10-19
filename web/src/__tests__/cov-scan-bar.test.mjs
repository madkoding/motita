// The sidebar's bar of a notes scan running in the background: hidden while none runs, following
// one that starts, gone when it ends.
// Run with: node --test web/src/__tests__/cov-scan-bar.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import {
  installDom,
  load,
  fakeApi,
  json,
  text,
  settle,
} from "./cov-settings-helpers.mjs";

installDom();
const { ScanBar } = await load("ScanBar.tsx");
const notesScan = await import("../NotesScanSettings.tsx");
const { SCAN_STARTED_EVENT } = await import("../notesApi.ts");
const { h, render } = await import("preact");

notesScan.scanPoll.ms = 1;

const IDLE = {
  running: false,
  done: 0,
  total: 0,
  chapters: 0,
  chapter_total: 0,
};
const MAPPING = {
  ...IDLE,
  running: true,
  phase: "projects",
  project: "App",
  total: 2,
};

async function mount(gw) {
  const root = document.createElement("div");
  document.body.appendChild(root);
  render(h(ScanBar, { api: gw.api }), root);
  await settle();
  return root;
}

const bar = (root) => root.querySelector("[data-testid=sidebar-scan]");

// until settles the page until check holds.
async function until(check) {
  for (let i = 0; i < 100 && !check(); i++) {
    await new Promise((r) => setTimeout(r, 2));
    await settle();
  }
}

test("a scan that starts is followed in the sidebar until it ends", async () => {
  let state = IDLE;
  const gw = fakeApi({ "GET /v1/notes/scan": () => json(200, state) });
  const root = await mount(gw);
  assert.equal(bar(root), null, "nothing runs");
  state = MAPPING;
  window.dispatchEvent(new Event(SCAN_STARTED_EVENT));
  // A second start announced while it is asking is asked about again after that answer.
  window.dispatchEvent(new Event(SCAN_STARTED_EVENT));
  await until(() => bar(root));
  assert.match(text(bar(root)), /Scanning the notes · Mapping App \(1 of 2\)/);
  assert.equal(bar(root).querySelector("[style]").style.width, "0%");
  state = IDLE;
  await until(() => !bar(root));
  assert.equal(bar(root), null, "the scan ended");
  render(null, root);
});

test("a gateway that cannot say draws nothing, and leaving stops the following", async () => {
  const down = await mount(fakeApi({ "GET /v1/notes/scan": new Error("off") }));
  assert.equal(bar(down), null);
  render(null, down);

  let answer = null;
  const gw = fakeApi({
    "GET /v1/notes/scan": () =>
      new Promise((r) => {
        answer = r;
      }),
  });
  const root = document.createElement("div");
  render(h(ScanBar, { api: gw.api }), root);
  await settle();
  render(null, root);
  await settle();
  answer(json(200, MAPPING));
  await settle();
  assert.equal(bar(root), null, "an answer after leaving is dropped");

  const running = fakeApi({ "GET /v1/notes/scan": json(200, MAPPING) });
  const live = await mount(running);
  await until(() => bar(live));
  render(null, live);
  await settle();
  const asked = running.calls.length;
  await new Promise((r) => setTimeout(r, 10));
  assert.equal(running.calls.length, asked, "no more asking once it left");
});
