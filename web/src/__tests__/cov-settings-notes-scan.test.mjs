// The Second brain card of the Indexing tab: one button that scans sessions and projects into
// notes, and says what it did.
// Run with: node --test web/src/__tests__/cov-settings-notes-scan.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import {
  installDom,
  load,
  fakeApi,
  json,
  text,
  settle,
  click,
} from "./cov-settings-helpers.mjs";

installDom();
const { NotesScanSettings } = await load("NotesScanSettings.tsx");
const notesApi = await import("../notesApi.ts");
const es = (await import("../i18n.es.ts")).default;
const { h, render } = await import("preact");

async function mount(fake) {
  const root = document.createElement("div");
  document.body.appendChild(root);
  render(h(NotesScanSettings, { api: fake.api }), root);
  await settle();
  return root;
}

const REPORT = {
  ran: true,
  summarized: 0,
  mapped: 0,
  documented: 0,
  explained: 0,
  projects: 2,
  sessions: 5,
  created: 6,
  updated: 1,
  unchanged: 0,
  skipped: 3,
};

test("the button scans and reports what it did", async () => {
  const gw = fakeApi({ "POST /v1/notes/scan": () => json(200, REPORT) });
  const root = await mount(gw);
  assert.equal(root.querySelector("[data-testid=notes-scan-done]"), null);
  await click(root.querySelector("[data-testid=notes-scan]"));
  await settle();
  assert.match(
    text(root.querySelector("[data-testid=notes-scan-done]")),
    /2 projects and 5 sessions scanned: 6 notes created, 1 updated, 0 unchanged, 3 skipped\./,
  );
  assert.equal(gw.calls.filter((c) => c.method === "POST").length, 1);
});

test("after a scan, a button asks the app to review the notes", async () => {
  const { REVIEW_NOTES_EVENT } = await load("useReviewNotes.ts");
  const gw = fakeApi({ "POST /v1/notes/scan": () => json(200, REPORT) });
  const root = await mount(gw);
  assert.equal(root.querySelector("[data-testid=notes-review-start]"), null);
  await click(root.querySelector("[data-testid=notes-scan]"));
  await settle();
  let asked = 0;
  window.addEventListener(REVIEW_NOTES_EVENT, () => asked++);
  await click(root.querySelector("[data-testid=notes-review-start]"));
  assert.equal(asked, 1);
});

test("the summary switch asks the gateway for summaries and the report counts them", async () => {
  const gw = fakeApi({
    "POST /v1/notes/scan": () =>
      json(200, {
        ...REPORT,
        summarized: 3,
        mapped: 2,
        documented: 40,
        explained: 1,
      }),
  });
  const root = await mount(gw);
  const on = [
    ...root.querySelectorAll("input[name=settings-notes-summary]"),
  ].find((i) => i.value === "on");
  on.checked = true;
  on.dispatchEvent(new window.Event("change", { bubbles: true }));
  await settle();
  await click(root.querySelector("[data-testid=notes-scan]"));
  await settle();
  assert.deepEqual(
    gw.calls.map((c) => c.path),
    ["/v1/notes/scan?summarize=1"],
  );
  assert.match(
    text(root.querySelector("[data-testid=notes-scan-done]")),
    /3 long sessions summarised\. 2 projects mapped into 40 manual notes, 1 explained by the model\./,
  );
});

test("a refusal is shown, with the gateway's reason or its status", async () => {
  let root = await mount(
    fakeApi({ "POST /v1/notes/scan": () => json(501, { error: "no notes" }) }),
  );
  await click(root.querySelector("[data-testid=notes-scan]"));
  await settle();
  assert.match(
    text(root.querySelector("[data-testid=notes-scan-error]")),
    /no notes/,
  );
  assert.equal(root.querySelector("[data-testid=notes-scan-done]"), null);

  root = await mount(
    fakeApi({
      "POST /v1/notes/scan": () => new Response("x", { status: 500 }),
    }),
  );
  await click(root.querySelector("[data-testid=notes-scan]"));
  await settle();
  assert.match(
    text(root.querySelector("[data-testid=notes-scan-error]")),
    /HTTP 500/,
  );
});

test("scanNotes posts and returns the report, and asks for a first-use scan when told", async () => {
  const gw = fakeApi({ "POST /v1/notes/scan": () => json(200, REPORT) });
  assert.deepEqual(await notesApi.scanNotes(gw.api), REPORT);
  await notesApi.scanNotes(gw.api, { first: true });
  await notesApi.scanNotes(gw.api, { first: true, summarize: true });
  assert.deepEqual(
    gw.calls.map((c) => c.path),
    [
      "/v1/notes/scan",
      "/v1/notes/scan?first=1",
      "/v1/notes/scan?first=1&summarize=1",
    ],
  );
});

test("every string of the card has a Spanish translation", () => {
  for (const key of [
    "Second brain",
    "Scan sessions and projects",
    "Scan now",
    "Review the notes",
    "Review notes",
    "Opens a conversation where the agent checks the notes for contradictions, stale notes, missing sources and broken links. It changes nothing until you agree.",
    "Use the model to summarise sessions and explain projects",
    "{mapped} projects mapped into {documented} manual notes, {explained} explained by the model.",
    "Writes a note for every project and every session you have used, linked to each other, and a manual of each project with a note per folder: its files, its API, what it uses and what uses it. Run it again to refresh them; what you add around the generated block stays.",
    "{n} long sessions summarised.",
  ]) {
    assert.ok(es[key], key);
  }
});
