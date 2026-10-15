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
// A tag index that means the tag follows inline.
const ARBITRARY_TAG = 63;
// WOFF2 header offsets and the start of its table directory.
const NUM_TABLES_AT = 12;
const COMPRESSED_LENGTH_AT = 20;
const TABLE_DIRECTORY_AT = 48;
// A UIntBase128 is at most this many bytes.
const BASE128_MAX_BYTES = 5;
const ALL_BITS = 0xff;
// maxp versions: 0.5 for CFF fonts, 1.0 for TrueType ones; maxZones sits at this offset.
const MAXP_CFF = 0x00005000;
const MAXP_TRUETYPE = 0x00010000;
const MAX_ZONES_AT = 14;
// Fonts bundled in public/ and site/fonts/.
const BUNDLED_FONTS = 12;
// A font is named in a test title by its folder and file name.
const DIR_AND_FILE = 2;

function base128(buf, pos) {
  let value = 0;
  for (let i = 0; i < BASE128_MAX_BYTES; i++) {
    const b = buf[pos.at];
    pos.at += 1;
    value = value * 128 + (b & 0x7f);
    if (!(b & 0x80)) {
      return value;
    }
  }
  throw new Error("UIntBase128 longer than 5 bytes");
}

// isTransformed tells whether a table carries a transform length: glyf and loca are transformed
// by default (version 0), every other table only with a non-zero version.
function isTransformed(index, version) {
  if (index === GLYF || index === LOCA) {
    return version === 0;
  }
  return version !== 0;
}

// maxpOf returns the decompressed maxp table of a WOFF2 file.
function maxpOf(buf) {
  assert.equal(buf.toString("latin1", 0, 4), "wOF2", "not a WOFF2 file");
  const numTables = buf.readUInt16BE(NUM_TABLES_AT);
  const compressed = buf.readUInt32BE(COMPRESSED_LENGTH_AT);
  const pos = { at: TABLE_DIRECTORY_AT };
  let offset = 0;
  let maxp = null;
  for (let i = 0; i < numTables; i++) {
    const flags = buf[pos.at];
    pos.at += 1;
    const index = flags & 0x3f;
    let tag = null;
    if (index === ARBITRARY_TAG) {
      tag = buf.toString("latin1", pos.at, pos.at + 4);
      pos.at += 4;
    }
    const version = flags >> 6;
    let length = base128(buf, pos);
    if (isTransformed(index, version)) {
      length = base128(buf, pos);
    }
    if (index === MAXP || tag === "maxp") {
      maxp = { offset, length };
    }
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
  assert.ok(fonts.length >= BUNDLED_FONTS, `only ${fonts.length} fonts found`);
});

for (const font of fonts) {
  const shortName = font.split("/").slice(-DIR_AND_FILE).join("/");
  test(`${shortName} has a maxp the browser accepts`, () => {
    const maxp = maxpOf(readFileSync(font));
    const version = maxp.readUInt32BE(0);
    if (version === MAXP_CFF) {
      return; // CFF fonts have no zones
    }
    assert.equal(version, MAXP_TRUETYPE);
    const zones = maxp.readUInt16BE(MAX_ZONES_AT);
    assert.ok([1, 2].includes(zones), `maxZones is ${zones}, must be 1 or 2`);
  });
}

test("a file that is not WOFF2 is refused", () => {
  assert.throws(() => maxpOf(Buffer.from("OTTO0000")), /not a WOFF2 file/);
});

test("an overlong base-128 number is refused", () => {
  assert.throws(
    () => base128(Buffer.alloc(BASE128_MAX_BYTES, ALL_BITS), { at: 0 }),
    /longer than 5 bytes/,
  );
});
