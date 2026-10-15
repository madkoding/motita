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

const say = (line) => process.stdout.write(`${line}\n`);

// Files with nothing to execute: type declarations only.
function isSource(name) {
  return /\.(ts|tsx)$/.test(name) && !name.endsWith(".d.ts");
}

function sources(dir) {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = join(dir, e.name);
    if (e.isDirectory()) {
      if (e.name === "__tests__") {
        return [];
      }
      return sources(p);
    }
    if (isSource(e.name)) {
      return [relative(".", p)];
    }
    return [];
  });
}

// parse reads, per file, the lines that never ran and the lines holding a branch that never ran.
function parse(text) {
  const files = new Map();
  let cur;
  for (const line of text.split("\n")) {
    const [tag, rest = ""] = line.split(/:(.*)/s);
    switch (tag) {
      case "SF":
        cur = { lines: [], branches: [] };
        files.set(rest, cur);
        break;
      case "DA": {
        const [n, hits] = rest.split(",").map(Number);
        if (hits === 0) {
          cur.lines.push(n);
        }
        break;
      }
      case "BRDA": {
        const [n, , , hits] = rest.split(",");
        if (hits === "0" || hits === "-") {
          cur.branches.push(Number(n));
        }
        break;
      }
      default:
        // Totals (LF, LH, BRF...) and function records: the gate reads lines and branches only.
        break;
    }
  }
  return files;
}

const report = parse(readFileSync(lcovPath, "utf8"));
let wanted = sources("src");
if (only.length) {
  wanted = only.map((f) => relative(".", f));
}
let failed = 0;
for (const f of wanted.sort((a, b) => a.localeCompare(b))) {
  const r = report.get(f);
  if (!r) {
    say(`${f}: not loaded by any test`);
    failed++;
    continue;
  }
  const lines = [...new Set(r.lines)];
  const branches = [...new Set(r.branches)];
  if (lines.length || branches.length) {
    say(`${f}: uncovered lines [${lines.join(",")}] branches at [${branches.join(",")}]`);
    failed++;
  }
}
if (failed) {
  say(`${failed} of ${wanted.length} files below 100 %`);
  process.exit(1);
}
say(`${wanted.length} files at 100 %`);
