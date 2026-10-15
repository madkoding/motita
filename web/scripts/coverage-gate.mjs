// The web UI's coverage gate: every source file under src/ must be fully covered, lines and
// branches, by the node --test suite (npm run test:coverage writes coverage/lcov.info).
// A file the suite never loads is not in the report at all, so the gate walks src/ itself:
// a file missing from the report fails the same way a file at 0 % would.
//
//   node scripts/coverage-gate.mjs [coverage/lcov.info] [src/File.tsx ...]
//
// With file arguments only those files are checked (handy while writing a test).
import { readFileSync, readdirSync } from "node:fs";
import { join, relative } from "node:path";

const [lcovPath = "coverage/lcov.info", ...only] = process.argv.slice(2);

// Files with nothing to execute: type declarations only.
const isSource = (f) => /\.(ts|tsx)$/.test(f) && !f.endsWith(".d.ts");

function sources(dir) {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = join(dir, e.name);
    if (e.isDirectory()) return e.name === "__tests__" ? [] : sources(p);
    return isSource(e.name) ? [relative(".", p)] : [];
  });
}

function parse(text) {
  const files = new Map();
  let cur;
  for (const line of text.split("\n")) {
    const [tag, rest = ""] = line.split(/:(.*)/s);
    if (tag === "SF") {
      cur = { lines: [], branches: [] };
      files.set(rest, cur);
    } else if (tag === "DA") {
      const [n, hits] = rest.split(",").map(Number);
      if (hits === 0) cur.lines.push(n);
    } else if (tag === "BRDA") {
      const [n, , , hits] = rest.split(",");
      if (hits === "0" || hits === "-") cur.branches.push(Number(n));
    }
  }
  return files;
}

const report = parse(readFileSync(lcovPath, "utf8"));
const wanted = only.length ? only.map((f) => relative(".", f)) : sources("src");
let failed = 0;
for (const f of wanted.sort()) {
  const r = report.get(f);
  if (!r) {
    console.log(`${f}: not loaded by any test`);
    failed++;
    continue;
  }
  const lines = [...new Set(r.lines)];
  const branches = [...new Set(r.branches)];
  if (lines.length || branches.length) {
    console.log(`${f}: uncovered lines [${lines.join(",")}] branches at [${branches.join(",")}]`);
    failed++;
  }
}
console.log(failed ? `${failed} of ${wanted.length} files below 100 %` : `${wanted.length} files at 100 %`);
process.exit(failed ? 1 : 0);
