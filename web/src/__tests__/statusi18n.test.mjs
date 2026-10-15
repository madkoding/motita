// Every failure a hook reports with setState(text, true) is shown as a toast, translated there
// with t(text): the English text is the key. A key the Spanish dictionary does not have reaches a
// reader of the Spanish interface in English, so each one written in the sources is checked here.
// Run with: node --test web/src/__tests__/statusi18n.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const src = join(dirname(fileURLToPath(import.meta.url)), "..");
const es = (await import("../i18n.es.ts")).default;

// failures are the literal texts passed to setState with `true`, directly or as the fallback
// after a gateway's own error (`err.error || "..."`), in every source file of the page.
function failures() {
  const keys = new Set();
  for (const name of readdirSync(src)) {
    if (!/\.tsx?$/.test(name)) {
      continue;
    }
    const code = readFileSync(join(src, name), "utf8");
    for (const m of code.matchAll(/setState\(\s*(?:[\w.]+\s*\|\|\s*)?"([^"]+)",\s*true\s*\)/g)) {
      keys.add(m[1]);
    }
  }
  return [...keys];
}

test("every failure reported through setState has its Spanish translation", () => {
  const keys = failures();
  // The hooks named in the audit that left these in English are among the ones checked.
  for (const k of [
    "could not open the artifact",
    "could not delete the artifact",
    "could not pin the artifact",
    "could not save the file",
    "could not change the project",
  ]) {
    assert.ok(keys.includes(k), `found in the sources: ${k}`);
  }
  for (const k of keys) {
    assert.ok(es[k], `missing Spanish for: ${k}`);
  }
});
