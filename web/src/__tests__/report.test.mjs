// The report's before/after evidence and the full-window image preview. The components are TSX,
// which node cannot import here, so these check the wiring in the source the way the other UI
// tests check the CSS: the pieces that make it work must be present and connected.
// Run with: node --test web/src/__tests__/
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const src = (f) =>
  readFileSync(join(dirname(fileURLToPath(import.meta.url)), "..", f), "utf8");
const report = src("Report.tsx");
const lightbox = src("Lightbox.tsx");
const css = src("report.css");
const app = src("App.tsx");

test("a report that only has screenshots still draws a card", () => {
  assert.match(report, /r\.evidence\?\.length/);
});

test("before and after sit side by side and fall back to a column on a narrow screen", () => {
  assert.match(
    css,
    /\.rpt-shots\s*\{[^}]*grid-template-columns:\s*repeat\(auto-fit,\s*minmax\(min\(100%/,
  );
});

test("clicking a screenshot opens the preview with the pair, after loading it with the token", () => {
  assert.match(report, /<Lightbox images=\{zoom\.images\}/);
  assert.match(report, /urls\.current\.get\(other\)/);
  assert.match(app, /loadEvidenceImage[\s\S]*artifactBase\(\)/);
  assert.match(
    app,
    /<ReportCard\s+report=\{m\.report\}\s+loadImage=\{loadEvidenceImage\}/,
  );
});

test("the preview fills the window and has zoom, pan, fit, actual size and keyboard control", () => {
  assert.match(css, /\.lb\s*\{[^}]*position:\s*fixed;\s*inset:\s*0/);
  for (const handler of ["onWheel", "onPointerMove", "onDblClick"]) {
    assert.ok(lightbox.includes(handler), handler);
  }
  for (const key of ["+", "-", "0", "1", "ArrowRight", "ArrowLeft"]) {
    // Quoted either way.
    assert.ok([`'${key}'`, `"${key}"`].some((q) => lightbox.includes(q)), key);
  }
  assert.ok(
    lightbox.includes("useDialog"),
    "Escape and focus come from the shared dialog behaviour",
  );
});

test("a picture in an answer opens the same preview, unless it is a link", () => {
  assert.match(
    app,
    /target\.tagName === ['"]IMG['"] &&\s*target\.closest\(['"]\.markdown-body['"]\) &&\s*!target\.closest\(['"]a['"]\)/,
  );
  assert.match(app, /<Lightbox images=\{\[previewImage\]\}/);
});

const { settle, proposalRequest, initialVerdicts } =
  await import("../proposals.ts");
const es = (await import("../i18n.es.ts")).default;

test("a suggestion answer settles the ones after it, never the ones before", () => {
  const none = [undefined, undefined, undefined];
  assert.deepEqual(settle(none, 0, "this"), ["apply", undefined, undefined]);
  assert.deepEqual(settle(["apply", undefined, undefined], 1, "none"), [
    "apply",
    "skip",
    "skip",
  ]);
  assert.deepEqual(settle(none, 0, "rest"), ["apply", "apply", "apply"]);
});

test("a needed suggestion auto mode accepted is never settled away by a later answer", () => {
  assert.deepEqual(settle([undefined, "apply", undefined], 0, "none"), [
    "skip",
    "apply",
    "skip",
  ]);
});

test("auto mode starts with the needed suggestions accepted, and only in auto mode", () => {
  const s = [{ title: "a", needed: true }, { title: "b" }];
  assert.deepEqual(initialVerdicts(s, true), ["apply", undefined]);
  assert.deepEqual(initialVerdicts(s, false), [undefined, undefined]);
  assert.deepEqual(initialVerdicts(s, true, ["skip", "skip"]), [
    "skip",
    "skip",
  ]);
  assert.equal(es["needed"], "necesaria");
});

test("only the accepted suggestions are sent, and none accepted sends nothing", () => {
  const s = [
    { title: "test the empty header", detail: "it panics" },
    { title: "share the helper" },
    { title: "log it" },
  ];
  assert.equal(proposalRequest("Apply:", s, ["skip", "skip", "skip"]), "");
  assert.equal(
    proposalRequest(" Apply: ", s, ["apply", "skip", "apply"]),
    "Apply:\n- test the empty header: it panics\n- log it",
  );
});

test("the report offers the three answers, in both languages, and only while nothing runs", () => {
  for (const label of [
    "Apply this one",
    "Apply this one and the following",
    "Apply none",
    "Apply these suggestions you made, and nothing else:",
  ]) {
    assert.ok(report.includes(`t("${label}")`), label);
    assert.ok(es[label], `missing Spanish for ${label}`);
  }
  assert.match(report, /r\.suggestions\?\.length/);
  assert.match(app, /\brunning,\s*undefined/);
  assert.match(app, /onAnswer=\{onAnswer\}/);
});
