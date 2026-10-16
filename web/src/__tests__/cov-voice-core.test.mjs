// The Chatterbox readers of localVoiceCore.ts, loaded as the source itself, on the edges of their
// inputs: merges whose ranks are not in the order the pairs come, a vocabulary without its own
// [UNK] or [SPACE], and a voice.bin shorter than its own counts.
import test from "node:test";
import assert from "node:assert/strict";

const core = await import("../localVoiceCore.ts");

const tokenizer = (vocab, merges, added = []) =>
  core.chatterboxTokenizer(
    JSON.stringify({
      model: { vocab, merges },
      added_tokens: added,
      post_processor: {
        special_tokens: {
          EXAGGERATION: { ids: [90] },
          BOS: { ids: [91] },
          EOS: { ids: [92] },
          START_SPEECH: { ids: [93] },
        },
      },
    }),
  );

test("BPE joins the lowest-ranked pair first, whatever order the pairs come in", () => {
  // "abc": the pairs a+b (rank 1) and b+c (rank 0): b+c wins although a+b comes first, and a
  // later pair of a higher rank never displaces a lower one.
  const tk = tokenizer({ a: 1, b: 2, c: 3, bc: 4, abc: 5, ab: 6 }, [
    "b c",
    "a b",
    "a bc",
  ]);
  const ids = core.chatterboxTokens(tk, "abc");
  assert.ok(ids.includes(5), "a + bc joined last into abc");
  assert.ok(!ids.includes(6), "a + b was never joined");
  const cab = core.chatterboxTokens(
    tokenizer({ c: 1, a: 2, b: 3, ab: 4, ca: 5 }, ["a b", "c a"]),
    "cab",
  );
  // c + ab: a+b (rank 0) beats c+a (rank 1) that comes first.
  assert.ok(cab.includes(4), "a + b joined into ab");
  assert.ok(cab.includes(1), "c left alone");
});

test("a vocabulary without [UNK] or [SPACE] falls back to id 1", () => {
  const tk = tokenizer({ a: 7 }, []);
  const ids = core.chatterboxTokens(tk, "a z");
  // The frame around the text, then a, the space and z, both unknown here.
  assert.deepEqual(
    ids.filter((n) => n === 7 || n === 1),
    [7, 1, 1],
  );
});

test("a voice.bin with no room for its counts is not a voice", () => {
  for (const bytes of [0, 4, 8]) {
    assert.throws(
      () => core.chatterboxVoice(new ArrayBuffer(bytes)),
      /not a Chatterbox voice/,
    );
  }
});
