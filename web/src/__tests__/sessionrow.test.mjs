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

test("the session row's button carries the last-used tooltip its text cannot show", () => {
  const row = component("SessionRow.tsx", "SessionRow");
  const hit = row.slice(row.indexOf('class="row-hit"'));
  assert.match(hit.slice(0, hit.indexOf("/>")), /Last used \{when\}/);
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
