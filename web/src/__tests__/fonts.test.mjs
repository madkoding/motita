// The bundled fonts must pass the font sanitizer Firefox and Chrome run on downloaded fonts (OTS).
// Sansation shipped with maxp.maxZones = 0, which OTS reports as "maxp: Bad maxZones: 0" on every
// page load; a TrueType font with a version 1.0 maxp needs 1 or 2 zones.
import test from "node:test";
import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { brotliDecompressSync } from "node:zlib";

const here = dirname(fileURLToPath(import.meta.url));
const dirs = [
  join(here, "..", "..", "public"),
  join(here, "..", "..", "..", "site", "fonts"),
];

// WOFF2 table directory: known tags are an index into this list, others carry the tag inline.
const MAXP = 4;
const GLYF = 10;
const LOCA = 11;

function base128(buf, pos) {
  let value = 0;
  for (let i = 0; i < 5; i++) {
    const b = buf[pos.at++];
    value = value * 128 + (b & 0x7f);
    if (!(b & 0x80)) return value;
  }
  throw new Error("UIntBase128 longer than 5 bytes");
}

// maxpOf returns the decompressed maxp table of a WOFF2 file.
function maxpOf(buf) {
  assert.equal(buf.toString("latin1", 0, 4), "wOF2", "not a WOFF2 file");
  const numTables = buf.readUInt16BE(12);
  const compressed = buf.readUInt32BE(20);
  const pos = { at: 48 };
  let offset = 0;
  let maxp = null;
  for (let i = 0; i < numTables; i++) {
    const flags = buf[pos.at++];
    const index = flags & 0x3f;
    let tag = null;
    if (index === 63) {
      tag = buf.toString("latin1", pos.at, pos.at + 4);
      pos.at += 4;
    }
    const version = flags >> 6;
    let length = base128(buf, pos);
    const transformed =
      index === GLYF || index === LOCA ? version === 0 : version !== 0;
    if (transformed) length = base128(buf, pos);
    if (index === MAXP || tag === "maxp") maxp = { offset, length };
    offset += length;
  }
  assert.ok(maxp, "no maxp table");
  const data = brotliDecompressSync(buf.subarray(pos.at, pos.at + compressed));
  return data.subarray(maxp.offset, maxp.offset + maxp.length);
}

const fonts = dirs.flatMap((d) =>
  readdirSync(d)
    .filter((f) => f.endsWith(".woff2"))
    .map((f) => join(d, f)),
);

test("every bundled font is found", () => {
  assert.ok(fonts.some((f) => f.endsWith("Sansation-Regular.woff2")));
  assert.ok(fonts.length >= 12, `only ${fonts.length} fonts found`);
});

for (const font of fonts) {
  test(`${font.split("/").slice(-2).join("/")} has a maxp the browser accepts`, () => {
    const maxp = maxpOf(readFileSync(font));
    const version = maxp.readUInt32BE(0);
    if (version === 0x00005000) return; // CFF fonts have no zones
    assert.equal(version, 0x00010000);
    const zones = maxp.readUInt16BE(14);
    assert.ok(
      zones === 1 || zones === 2,
      `maxZones is ${zones}, must be 1 or 2`,
    );
  });
}

test("a file that is not WOFF2 is refused", () => {
  assert.throws(() => maxpOf(Buffer.from("OTTO0000")), /not a WOFF2 file/);
});

test("an overlong base-128 number is refused", () => {
  assert.throws(
    () => base128(Buffer.alloc(5, 0xff), { at: 0 }),
    /longer than 5 bytes/,
  );
});
