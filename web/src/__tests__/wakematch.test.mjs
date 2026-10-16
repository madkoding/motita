// The wake phrase looked for in the browser (wakeMatch.ts): the gateway's own matching
// (internal/voice/match.go), so the same cases as its TestMatch, and the same answers.
import test from "node:test";
import assert from "node:assert/strict";

const { matchWake } = await import("../wakeMatch.ts");

test("a wake phrase matches as the gateway's does", () => {
  const phrases = ["hey motita", "motita", "motita new task", "", "ok go"];
  const cases = [
    ["Hey Motita, run the tests.", "hey motita", "run the tests."],
    ["Motita new task: write docs", "motita new task", "write docs"],
    ["uh, mutita  check the build", "motita", "check the build"],
    ["MOTÍTA", "motita", ""],
    ["well so then motita go", null],
    ["ok go now", "ok go", "now"],
    ["ok no", null],
    ["", null],
    ["motor", null],
  ];
  for (const [text, phrase, rest] of cases) {
    const got = matchWake(text, phrases);
    if (phrase === null) {
      assert.equal(got, undefined, text);
    } else {
      assert.deepEqual(got, { phrase, rest }, text);
    }
  }
  assert.equal(matchWake("motita", []), undefined);
});

test("the earliest phrase wins, then the longest, whatever order they are listed in", () => {
  // Listed longest first: the shorter one at the same place does not replace it.
  assert.deepEqual(
    matchWake("motita new task go", ["motita new task", "motita"]),
    {
      phrase: "motita new task",
      rest: "go",
    },
  );
  // One that starts earlier wins over one listed before it.
  assert.deepEqual(
    matchWake("motita, hey motita go", ["hey motita", "motita"]),
    {
      phrase: "motita",
      rest: "hey motita go",
    },
  );
  // At most two words before it; accents and ñ fold, in the phrase too.
  assert.deepEqual(matchWake("eh ok Señor Ñandú: hola", ["senor ñandu"]), {
    phrase: "senor ñandu",
    rest: "hola",
  });
  assert.equal(matchWake("eh ok um motita", ["motita"]), undefined);
  // Digits are words; a letter outside the Basic Plane keeps the rest where it was.
  assert.deepEqual(matchWake("𝒜 r2d2 · ve", ["r2d2"]), {
    phrase: "r2d2",
    rest: "ve",
  });
});
