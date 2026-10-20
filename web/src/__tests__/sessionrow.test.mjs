// A session row and a project header react to a click anywhere on them through a transparent
// `.row-hit` button laid over the box. Boxes in the same paint layer are drawn in document order,
// so the button must come AFTER the text (or a text style that makes a stacking context, such as
// the `opacity-70` the "last used" date had, is painted above it and swallows the click) and
// BEFORE the actions (or it covers the "⋯" and the new-session buttons).
// Run with: node --test web/src/__tests__/
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { load } from "./tsx-hooks.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const src = (f) => readFileSync(join(here, "..", f), "utf8");

const SESSION_ROW = "SessionRow.tsx";
const PROJECT_GROUP = "ProjectGroup.tsx";
const ROW_HIT = 'class="row-hit"';

// The body of a component, from its declaration to the next top-level function.
function component(file, name) {
  const code = src(file);
  const start = code.search(new RegExp(String.raw`function ${name}\(`));
  assert.ok(start >= 0, `${name} exists`);
  const rest = code.slice(start + 1);
  const end = rest.search(/\n(?:export )?function /);
  if (end < 0) {
    return code.slice(start);
  }
  return code.slice(start, start + 1 + end);
}

// The position of each marker in the JSX, in the order given; each must be found.
function order(body, markers) {
  return markers.map((m) => {
    const i = body.indexOf(m);
    assert.ok(i >= 0, `${m} is rendered`);
    return i;
  });
}

test("a session row's click target is drawn above its text and under its actions", () => {
  const row = component(SESSION_ROW, "SessionRow");
  const [text, hit, actions] = order(row, [
    "<SessionSummary",
    ROW_HIT,
    "<SessionActions",
  ]);
  assert.ok(text < hit, "the button comes after the text");
  assert.ok(hit < actions, "the button comes before the actions");
  assert.match(row, /onClick=\{\(\) => conv\.switchSession\(s\.id\)\}/);
});

test("the row buttons carry the tooltips of the labels they cover", () => {
  const row = component(SESSION_ROW, "SessionRow");
  const hit = row.slice(row.indexOf(ROW_HIT));
  assert.match(
    hit.slice(0, hit.indexOf("/>")),
    /title=\{sessionTooltip\(s\)\}/,
  );
  const header = component(PROJECT_GROUP, "ProjectHeader");
  const phit = header.slice(header.indexOf(ROW_HIT));
  assert.match(
    phit.slice(0, phit.indexOf("/>")),
    /title=\{projectTooltip\(p\)\}/,
  );
  // A label's own title would never show: the button is on top of it.
  for (const [file, name] of [
    [SESSION_ROW, "SessionFacts"],
    [SESSION_ROW, "SessionSummary"],
  ]) {
    assert.doesNotMatch(
      component(file, name),
      /\btitle=/,
      `${name} has no dead title`,
    );
  }
});

test("a project header's click target is drawn above its title and under its actions", () => {
  const header = component(PROJECT_GROUP, "ProjectHeader");
  const [title, hit, actions] = order(header, [
    "<StaleFact",
    ROW_HIT,
    "<ProjectActions",
  ]);
  assert.ok(title < hit, "the button comes after the title");
  assert.ok(hit < actions, "the button comes before the actions");
});

// appFormat.ts imports its siblings without extensions, which tsx-hooks.mjs resolves.
const { sessionTooltip, projectTooltip } = await load("appFormat.ts");

test("sessionTooltip says every fact the row shows, one per line", () => {
  const lines = sessionTooltip({
    id: "s1",
    last_used: "2026-10-13T04:00:00Z",
    agents_running: 2,
    changes: 3,
  }).split("\n");
  // One line each: last used, agents, changes.
  const SESSION_LINES = 3;
  assert.equal(lines.length, SESSION_LINES);
  const [used, agents, changes] = lines;
  assert.match(used, /2026/);
  assert.match(agents, /2/);
  assert.match(changes, /3/);
});

test("a row with nothing to say has no tooltip", () => {
  assert.equal(sessionTooltip({ id: "s1" }), undefined);
  assert.equal(projectTooltip({ id: "p1", title: "P", dir: "/p" }), undefined);
});

test("projectTooltip says the checkout's branch and changes", () => {
  const lines = projectTooltip({
    id: "p1",
    title: "P",
    dir: "/p",
    branch: "main",
    changes: 1,
  }).split("\n");
  // One line each: branch, changes.
  const PROJECT_LINES = 2;
  assert.equal(lines.length, PROJECT_LINES);
  assert.match(lines[0], /main/);
  assert.match(lines[1], /1/);
});
