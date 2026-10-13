// A session row and a project header react to a click anywhere on them through a transparent
// `.row-hit` button laid over the box. Boxes in the same paint layer are drawn in document order,
// so the button must come AFTER the text (or a text style that makes a stacking context, such as
// the `opacity-70` the "last used" date had, is painted above it and swallows the click) and
// BEFORE the actions (or it covers the "⋯" and the new-session buttons).
// Run with: node --test web/src/__tests__/
import test from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { tmpdir } from "node:os";
import { build } from "esbuild";

const here = dirname(fileURLToPath(import.meta.url));
const src = (f) => readFileSync(join(here, "..", f), "utf8");

// The body of a component, from its declaration to the next top-level function.
function component(file, name) {
  const code = src(file);
  const start = code.search(new RegExp(`function ${name}\\(`));
  assert.ok(start >= 0, `${name} exists`);
  const rest = code.slice(start + 1);
  const end = rest.search(/\n(?:export )?function /);
  return code.slice(start, end < 0 ? undefined : start + 1 + end);
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
  const row = component("SessionRow.tsx", "SessionRow");
  const [text, hit, actions] = order(row, [
    "<SessionSummary",
    'class="row-hit"',
    "<SessionActions",
  ]);
  assert.ok(text < hit, "the button comes after the text");
  assert.ok(hit < actions, "the button comes before the actions");
  assert.match(row, /onClick=\{\(\) => conv\.switchSession\(s\.id\)\}/);
});

test("the row buttons carry the tooltips of the labels they cover", () => {
  const row = component("SessionRow.tsx", "SessionRow");
  const hit = row.slice(row.indexOf('class="row-hit"'));
  assert.match(
    hit.slice(0, hit.indexOf("/>")),
    /title=\{sessionTooltip\(s\)\}/,
  );
  const header = component("ProjectGroup.tsx", "ProjectHeader");
  const phit = header.slice(header.indexOf('class="row-hit"'));
  assert.match(
    phit.slice(0, phit.indexOf("/>")),
    /title=\{projectTooltip\(p\)\}/,
  );
  // A label's own title would never show: the button is on top of it.
  for (const [file, name] of [
    ["SessionRow.tsx", "SessionFacts"],
    ["SessionRow.tsx", "SessionSummary"],
    ["ProjectGroup.tsx", "ProjectFacts"],
  ]) {
    assert.doesNotMatch(
      component(file, name),
      /\btitle=/,
      `${name} has no dead title`,
    );
  }
});

test("a project header's click target is drawn above its title and under its actions", () => {
  const header = component("ProjectGroup.tsx", "ProjectHeader");
  const [title, hit, actions] = order(header, [
    "<ProjectFacts",
    'class="row-hit"',
    "<ProjectActions",
  ]);
  assert.ok(title < hit, "the button comes after the title");
  assert.ok(hit < actions, "the button comes before the actions");
});

// appFormat.ts imports its siblings without extensions, so it is bundled first.
const dir = mkdtempSync(join(tmpdir(), "motita-sessionrow-test-"));
const out = join(dir, "format.mjs");
await build({
  entryPoints: [join(here, "..", "appFormat.ts")],
  bundle: true,
  format: "esm",
  platform: "node",
  outfile: out,
  logLevel: "silent",
});
const { sessionTooltip, projectTooltip } = await import(out);
rmSync(dir, { recursive: true, force: true });

test("sessionTooltip says every fact the row shows, one per line", () => {
  const lines = sessionTooltip({
    id: "s1",
    last_used: "2026-10-13T04:00:00Z",
    agents_running: 2,
    branch: "feature/x",
    worktree: "wt1",
    model: "m1",
    provider: "p1",
    changes: 3,
  }).split("\n");
  assert.equal(lines.length, 6);
  assert.match(lines[0], /2026/);
  assert.match(lines[1], /2/);
  assert.match(lines[2], /feature\/x/);
  assert.match(lines[3], /wt1/);
  assert.match(lines[4], /p1 \/ m1/);
  assert.match(lines[5], /3/);
});

test("a row with nothing to say has no tooltip", () => {
  assert.equal(sessionTooltip({ id: "s1" }), undefined);
  assert.equal(projectTooltip({ id: "p1", title: "P", dir: "/p" }), undefined);
});

test("projectTooltip says the checkout's branch, podman and changes", () => {
  const lines = projectTooltip({
    id: "p1",
    title: "P",
    dir: "/p",
    branch: "main",
    podman: true,
    changes: 1,
  }).split("\n");
  assert.equal(lines.length, 3);
  assert.match(lines[0], /main/);
  assert.match(lines[1], /podman/);
  assert.match(lines[2], /1/);
});
