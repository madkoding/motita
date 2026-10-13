// The session row opens its conversation from anywhere on it: a transparent `.row-hit` button
// covers the row and the row's text is drawn under it. Anything in the text that creates a
// stacking context (opacity, transform, filter, z-index, positioning) is painted ABOVE that
// button and swallows the click: clicking the "last used" date did not switch sessions.
// Run with: node --test web/src/__tests__/
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const src = readFileSync(join(here, "..", "SessionRow.tsx"), "utf8");

// The body of a component, from its declaration to the next top-level function.
function component(name) {
  const start = src.indexOf(`function ${name}(`);
  assert.ok(start >= 0, `${name} exists`);
  const next = src.indexOf("\nfunction ", start + 1);
  const nextExport = src.indexOf("\nexport function ", start + 1);
  const ends = [next, nextExport].filter((i) => i > 0);
  return src.slice(start, ends.length ? Math.min(...ends) : undefined);
}

// Tailwind utilities that make an element a stacking context or a positioned box.
const ABOVE_ROW_HIT =
  /(?:^|[\s"'`])(?:opacity-\S+|(?:-?)(?:translate|rotate|scale|skew)-\S+|transform\S*|filter|blur\S*|backdrop-\S+|isolate|z-\S+|relative|absolute|fixed|sticky|mix-blend-\S+|will-change-\S+)(?=[\s"'`]|$)/;

for (const name of ["SessionSummary", "SessionFacts"]) {
  test(`${name} draws nothing above the row's click target`, () => {
    const classes = [...component(name).matchAll(/class=\{?[`"]([^`"]*)[`"]/g)].map(
      (m) => m[1],
    );
    assert.ok(classes.length > 0, `${name} has classes to check`);
    for (const c of classes) {
      assert.doesNotMatch(
        c,
        ABOVE_ROW_HIT,
        `"${c}" would cover .row-hit and swallow the click that opens the session`,
      );
    }
  });
}

test("the row-hit button switches to the session", () => {
  assert.match(
    component("SessionRow"),
    /class="row-hit"[\s\S]*?onClick=\{\(\) => conv\.switchSession\(s\.id\)\}/,
  );
});
