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
const notesScan = await load("NotesScanSettings.tsx");
const { NotesScanSettings } = notesScan;
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

// IDLE is what the gateway answers while no scan has run since it started.
const IDLE = {
  running: false,
  done: 0,
  total: 0,
  chapters: 0,
  chapter_total: 0,
};
const RUNNING = { ...IDLE, running: true, phase: "sessions" };

// scanGw is a gateway whose background scan goes through steps, then ends with last; before any
// scan it is idle. The POST answers the first step.
function scanGw(steps, last) {
  let queue = null;
  return fakeApi({
    "POST /v1/notes/scan": () => {
      queue = [...steps, last];
      return json(202, RUNNING);
    },
    "GET /v1/notes/scan": () => {
      if (!queue) {
        return json(200, IDLE);
      }
      return json(200, queue.length > 1 ? queue.shift() : queue[0]);
    },
  });
}

const posts = (gw) =>
  gw.calls.filter((c) => c.method === "POST").map((c) => c.path);

notesScan.scanPoll.ms = 1;

// until settles the page until the element with that test id shows.
async function until(root, id) {
  for (let i = 0; i < 100 && !root.querySelector(`[data-testid=${id}]`); i++) {
    await new Promise((r) => setTimeout(r, 2));
    await settle();
  }
}

test("the button scans in the background, shows how far it got and reports what it did", async () => {
  const mapping = {
    ...RUNNING,
    phase: "projects",
    project: "App",
    done: 0,
    total: 2,
    chapters: 4,
    chapter_total: 10,
  };
  let seen = [];
  const gw = scanGw(
    [mapping, { ...mapping, project: "Web", done: 1, chapter_total: 0 }],
    { ...IDLE, result: REPORT },
  );
  const root = await mount(gw);
  assert.equal(root.querySelector("[data-testid=notes-scan-done]"), null);
  const observer = new window.MutationObserver(() => {
    const p = root.querySelector("[data-testid=notes-scan-progress]");
    if (p) {
      seen.push(text(p) + " " + p.querySelector("[style]").style.width);
    }
  });
  observer.observe(root, {
    subtree: true,
    childList: true,
    characterData: true,
    attributes: true,
  });
  await click(root.querySelector("[data-testid=notes-scan]"));
  await until(root, "notes-scan-done");
  observer.disconnect();
  seen = [...new Set(seen)];
  assert.ok(
    seen.includes("Writing the notes of your sessions… 0%"),
    seen.join(" | "),
  );
  assert.ok(
    seen.includes("Mapping App (1 of 2) · folder 5 of 10 20%"),
    seen.join(" | "),
  );
  assert.ok(seen.includes("Mapping Web (2 of 2) 50%"), seen.join(" | "));
  assert.match(
    text(root.querySelector("[data-testid=notes-scan-done]")),
    /2 projects and 5 sessions scanned: 6 notes created, 1 updated, 0 unchanged, 3 skipped\./,
  );
  assert.equal(root.querySelector("[data-testid=notes-scan-progress]"), null);
  assert.deepEqual(posts(gw), ["/v1/notes/scan?background=1"]);
});

test("a scan already running when the card opens is followed, and its failure is said", async () => {
  let stop = false;
  const gw = fakeApi({
    "GET /v1/notes/scan": () =>
      json(
        200,
        stop
          ? { ...IDLE, error: "disk full" }
          : { ...RUNNING, phase: "projects", total: 0 },
      ),
  });
  const root = await mount(gw);
  assert.match(
    text(root.querySelector("[data-testid=notes-scan-progress]")),
    /Mapping/,
  );
  assert.match(
    text(root.querySelector("[data-testid=notes-scan]")),
    /Scanning…/,
  );
  stop = true;
  await until(root, "notes-scan-error");
  assert.match(
    text(root.querySelector("[data-testid=notes-scan-error]")),
    /disk full/,
  );

  // A scan that ended with nothing to say leaves the card as it was.
  const quiet = await mount(
    fakeApi({ "GET /v1/notes/scan": () => json(200, IDLE) }),
  );
  assert.equal(quiet.querySelector("[data-testid=notes-scan-done]"), null);
  assert.equal(quiet.querySelector("[data-testid=notes-scan-progress]"), null);

  // Closing the card stops following it.
  const open = await mount(
    fakeApi({ "GET /v1/notes/scan": () => json(200, RUNNING) }),
  );
  render(null, open);
  await settle();
});

test("after a scan, a button asks the app to review the notes", async () => {
  const { REVIEW_NOTES_EVENT } = await load("useReviewNotes.ts");
  const gw = scanGw([], { ...IDLE, result: REPORT });
  const root = await mount(gw);
  assert.equal(root.querySelector("[data-testid=notes-review-start]"), null);
  await click(root.querySelector("[data-testid=notes-scan]"));
  await until(root, "notes-scan-done");
  let asked = 0;
  window.addEventListener(REVIEW_NOTES_EVENT, () => asked++);
  await click(root.querySelector("[data-testid=notes-review-start]"));
  assert.equal(asked, 1);
});

test("the summary switch asks the gateway for summaries and the report counts them", async () => {
  const gw = scanGw([], {
    ...IDLE,
    result: {
      ...REPORT,
      summarized: 3,
      mapped: 2,
      documented: 40,
      explained: 1,
    },
  });
  const root = await mount(gw);
  const on = [
    ...root.querySelectorAll("input[name=settings-notes-summary]"),
  ].find((i) => i.value === "on");
  on.checked = true;
  on.dispatchEvent(new window.Event("change", { bubbles: true }));
  await settle();
  await click(root.querySelector("[data-testid=notes-scan]"));
  await until(root, "notes-scan-done");
  assert.deepEqual(posts(gw), [
    "/v1/notes/scan?summarize=1&modules=30&background=1",
  ]);
  assert.match(
    text(root.querySelector("[data-testid=notes-scan-done]")),
    /3 long sessions summarised\. 2 projects mapped into 40 manual notes, 1 explained by the model\./,
  );
});

test("the folders choice sets how many folders the model explains", async () => {
  const gw = scanGw([], { ...IDLE, result: REPORT });
  const root = await mount(gw);
  const pick = async (name, value) => {
    const input = [...root.querySelectorAll(`input[name=${name}]`)].find(
      (i) => i.value === value,
    );
    input.checked = true;
    input.dispatchEvent(new window.Event("change", { bubbles: true }));
    await settle();
  };
  const all = [
    ...root.querySelectorAll("input[name=settings-notes-folders]"),
  ].find((i) => i.value === "all");
  assert.equal(all.disabled, true, "off until the model is on");
  await pick("settings-notes-summary", "on");
  await pick("settings-notes-folders", "all");
  await click(root.querySelector("[data-testid=notes-scan]"));
  await until(root, "notes-scan-done");
  assert.deepEqual(posts(gw), [
    "/v1/notes/scan?summarize=1&modules=2000&background=1",
  ]);
});

test("a refusal is shown, with the gateway's reason or its status", async () => {
  let root = await mount(
    fakeApi({
      "GET /v1/notes/scan": () => json(200, IDLE),
      "POST /v1/notes/scan": () => json(501, { error: "no notes" }),
    }),
  );
  await click(root.querySelector("[data-testid=notes-scan]"));
  await settle();
  assert.match(
    text(root.querySelector("[data-testid=notes-scan-error]")),
    /no notes/,
  );
  assert.equal(root.querySelector("[data-testid=notes-scan-done]"), null);
  assert.equal(root.querySelector("[data-testid=notes-scan-progress]"), null);

  root = await mount(
    fakeApi({
      "GET /v1/notes/scan": () => new Response("x", { status: 500 }),
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
  const bg = fakeApi({
    "POST /v1/notes/scan": () => json(202, RUNNING),
    "GET /v1/notes/scan": () => json(200, IDLE),
  });
  assert.deepEqual(await notesApi.startScan(bg.api), RUNNING);
  assert.deepEqual(await notesApi.scanProgress(bg.api), IDLE);
  await assert.rejects(
    notesApi.scanProgress(
      fakeApi({ "GET /v1/notes/scan": () => json(503, {}) }).api,
    ),
    /HTTP 503/,
  );
});

test("every string of the card has a Spanish translation", () => {
  for (const key of [
    "Second brain",
    "Scan sessions and projects",
    "Scan now",
    "Review the notes",
    "Folders the model explains per scan",
    "All",
    "Review notes",
    "Opens a conversation where the agent checks the notes for contradictions, stale notes, missing sources and broken links. It changes nothing until you agree.",
    "Use the model to summarise sessions and explain projects",
    "{mapped} projects mapped into {documented} manual notes, {explained} explained by the model.",
    "Writes a note for every project and every session you have used, linked to each other, and a manual of each project with a note per folder: its files, its API, what it uses and what uses it. Run it again to refresh them; what you add around the generated block stays.",
    "{n} long sessions summarised.",
    "Scanning…",
    "Writing the notes of your sessions…",
    "Mapping {project} ({n} of {total})",
    "folder {n} of {total}",
  ]) {
    assert.ok(es[key], key);
  }
});
