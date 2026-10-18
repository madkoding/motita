// The project's index panel: the bar, the numbers, the update button and the errors.
// Run with: node --test web/src/__tests__/cov-settings-projectindex.test.mjs
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
  byText,
} from "./cov-settings-helpers.mjs";

installDom();
const { ProjectIndexPanel } = await load("ProjectIndexPanel.tsx");
const es = (await import("../i18n.es.ts")).default;
const { h, render } = await import("preact");

const READY = {
  state: "ready",
  candidates: 40,
  files: 38,
  percent: 100,
  chunks: 120,
  terms: 900,
  max_files: 5000,
  capped: false,
  cache_bytes: 2048,
  semantic: true,
  embeddings: false,
};

async function mount(fake) {
  const root = document.createElement("div");
  document.body.appendChild(root);
  render(h(ProjectIndexPanel, { api: fake.api, id: "p 1" }), root);
  await settle();
  return root;
}
const q = (root, id) => root.querySelector(`[data-testid=project-index-${id}]`);

test("it shows the bar and the numbers of a ready index", async () => {
  const gw = fakeApi({
    "GET /v1/projects/p%201/code-index": () => json(200, READY),
  });
  const root = await mount(gw);
  assert.equal(text(q(root, "state")), "Up to date");
  assert.equal(root.querySelector("progress").value, 100);
  assert.equal(text(q(root, "files")), "38files");
  assert.equal(text(q(root, "chunks")), "120passages");
  assert.equal(text(q(root, "terms")), "900terms");
  assert.equal(text(q(root, "size")), "2 KBon disk");
  assert.match(
    text(q(root, "note")),
    /38 files indexed; 2 binary or unreadable skipped\. Ranked also by meaning\./,
  );
});

test("a large cache is shown in MB", async () => {
  const gw = fakeApi({
    "GET /v1/projects/p%201/code-index": () =>
      json(200, { ...READY, cache_bytes: 36463469 }),
  });
  const root = await mount(gw);
  assert.equal(text(q(root, "size")), "34.8 MBon disk");
});

test("an old index is marked out of date and a capped folder says so", async () => {
  const gw = fakeApi({
    "GET /v1/projects/p%201/code-index": () =>
      json(200, {
        ...READY,
        state: "stale",
        percent: 60,
        capped: true,
        embeddings: true,
      }),
  });
  const root = await mount(gw);
  assert.equal(text(q(root, "state")), "Out of date");
  assert.equal(root.querySelector("progress").dataset.state, "stale");
  assert.match(
    text(q(root, "note")),
    /limit of 5000 files is reached.*embeddings/,
  );
});

test("Index now builds and shows the new state; a project never indexed says so", async () => {
  let built = false;
  const gw = fakeApi({
    "GET /v1/projects/p%201/code-index": () =>
      json(200, {
        ...READY,
        state: "none",
        percent: 0,
        files: 0,
        semantic: false,
      }),
    "POST /v1/projects/p%201/code-index": () => {
      built = true;
      return json(200, READY);
    },
  });
  const root = await mount(gw);
  assert.equal(text(q(root, "state")), "Not indexed");
  click(byText(root, "button", "Index now"));
  await settle();
  assert.ok(built);
  assert.equal(text(q(root, "state")), "Up to date");
  assert.match(text(q(root, "done")), /Index updated: 38 files, 120 passages/);
});

test("errors are shown, from the first read and from a build", async () => {
  const failing = fakeApi({
    "GET /v1/projects/p%201/code-index": () =>
      json(409, { error: "the code index is turned off" }),
  });
  const root = await mount(failing);
  assert.match(text(q(root, "error")), /turned off/);
  assert.equal(q(root, "state"), null);
  assert.equal(q(root, "build").disabled, true);

  const buildFails = fakeApi({
    "GET /v1/projects/p%201/code-index": () => json(200, READY),
    "POST /v1/projects/p%201/code-index": () => json(500, new Error("x")),
  });
  const second = await mount(buildFails);
  click(q(second, "build"));
  await Promise.resolve();
  assert.ok(q(second, "working"));
  await settle();
  assert.match(text(q(second, "error")), /HTTP 500/);
  assert.equal(q(second, "done"), null);
  assert.equal(q(second, "working"), null);
});

test("leaving the panel ignores a late answer", async () => {
  const gw = fakeApi({
    "GET /v1/projects/p%201/code-index": () => json(200, READY),
  });
  const root = document.createElement("div");
  document.body.appendChild(root);
  render(h(ProjectIndexPanel, { api: gw.api, id: "p 1" }), root);
  render(null, root);
  await settle();
  assert.equal(root.textContent, "");
  const bad = fakeApi({
    "GET /v1/projects/p%201/code-index": () => json(500, { error: "late" }),
  });
  const gone = document.createElement("div");
  document.body.appendChild(gone);
  render(h(ProjectIndexPanel, { api: bad.api, id: "p 1" }), gone);
  render(null, gone);
  await settle();
  assert.equal(gone.textContent, "");
});

test("every new string has its Spanish", async () => {
  const src = (await import("node:fs")).readFileSync(
    new URL("../ProjectIndexPanel.tsx", import.meta.url),
    "utf8",
  );
  for (const m of src.matchAll(/\bt[f]?\(\s*"((?:[^"\\]|\\.)*)"/g)) {
    assert.ok(es[m[1]], `missing Spanish for: ${m[1]}`);
  }
});
