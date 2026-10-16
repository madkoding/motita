// The browser model's logic (browserLLMCore.ts) against a stand-in onnxruntime and tokenizer: the
// prompt it writes, how its answer is read from 16- or 32-bit scores, and its loading and
// generation step by step.
import test from "node:test";
import assert from "node:assert/strict";
import { source } from "./cov-voice-helpers.mjs";

const core = await source("browserLLMCore.ts");
const { BrowserModel, LLM_F16, LLM_F32, argmax, buildFor, chatPrompt, halfToFloat, isBrowserLLM } = core;

test("the conversation is written the way the model reads it", () => {
  assert.equal(
    chatPrompt([{ role: "user", content: "hi" }]),
    "<|im_start|>system\nYou are Qwen, created by Alibaba Cloud. You are a helpful assistant.<|im_end|>\n" +
      "<|im_start|>user\nhi<|im_end|>\n<|im_start|>assistant\n",
  );
  // Its own system turn is kept, and a turn of another kind (a tool's) is left out.
  assert.equal(
    chatPrompt([
      { role: "system", content: "be brief" },
      { role: "tool", content: "ignored" },
      { role: "user", content: "hi" },
      { role: "assistant", content: "yo" },
    ]),
    "<|im_start|>system\nbe brief<|im_end|>\n<|im_start|>user\nhi<|im_end|>\n<|im_start|>assistant\nyo<|im_end|>\n<|im_start|>assistant\n",
  );
});

test("16-bit floats are read from their bits", () => {
  assert.equal(halfToFloat(0x3c00), 1);
  assert.equal(halfToFloat(0xc000), -2);
  assert.equal(halfToFloat(0x0000), 0);
  assert.equal(halfToFloat(0x0001), 2 ** -24, "a subnormal");
  assert.equal(halfToFloat(0x7c00), Infinity);
  assert.equal(halfToFloat(0xfc00), -Infinity);
  assert.ok(Number.isNaN(halfToFloat(0x7e00)));
});

test("the best of the last scores wins, in either width", () => {
  assert.equal(argmax([9, 9, 9, 1, 5, 2], 3, false), 1, "only the last token's scores count");
  // 1.0, 2.0, 0.5 as 16-bit floats.
  assert.equal(argmax([0x3c00, 0x4000, 0x3800], 3, true), 1);
  assert.equal(argmax([0xfc00, 0xfc00], 2, true), 0, "nothing beats minus infinity but the first");
});

test("the catalog ids tell the browser model from a voice", () => {
  assert.equal(isBrowserLLM(LLM_F16), true);
  assert.equal(isBrowserLLM(LLM_F32), true);
  assert.equal(isBrowserLLM("whisper-base"), false);
  assert.equal(buildFor(true), LLM_F16);
  assert.equal(buildFor(false), LLM_F32);
});

// A stand-in model: it says the tokens in `says`, then stops, and keeps what it was fed.
function fakes({ half = false, says = [7, 8], stop = 151645 } = {}) {
  const log = { urls: [], feeds: [], tokenized: "" };
  class Tensor {
    constructor(type, data, dims) {
      Object.assign(this, { type, data, dims });
    }
    async getData() {
      return this.data;
    }
  }
  const vocab = stop + 1;
  let step = 0;
  const session = {
    run: async (feeds) => {
      log.feeds.push(feeds);
      const next = step < says.length ? says[step] : stop;
      step++;
      // The scores of an earlier position come first (all high): only the last `vocab` count.
      const scores = new Array(2 * vocab).fill(half ? 0xfc00 : -1);
      scores.fill(half ? 0x3c00 : 99, 0, vocab);
      scores[vocab + next] = half ? 0x3c00 : 5;
      const out = { logits: new Tensor(half ? "float16" : "float32", scores, [1, 2, vocab]) };
      for (let i = 0; i < 24; i++) {
        out[`present.${i}.key`] = new Tensor("x", [], [1, 2, step, 64]);
        out[`present.${i}.value`] = new Tensor("x", [], [1, 2, step, 64]);
      }
      return out;
    },
  };
  const deps = {
    f16: async () => half,
    bytes: async (url, progress) => {
      log.urls.push(url);
      progress?.(50, 100);
      progress?.(0, 0);
      const body = url.endsWith(".json") ? JSON.stringify({ name: url }) : "x";
      return new TextEncoder().encode(body).buffer;
    },
    gpu: async () => ({ Tensor, createSession: async () => session }),
    tokenizer: () => ({
      encode: (text) => {
        log.tokenized = text;
        return { ids: [1, 2, 3] };
      },
      decode: (ids) => "  " + ids.join(",") + " ",
    }),
  };
  return { deps, log };
}

test("it loads the build the card runs, and tells how far it is", async () => {
  for (const half of [true, false]) {
    const { deps, log } = fakes({ half });
    const seen = [];
    const m = new BrowserModel(deps);
    assert.deepEqual(await m.load((p) => seen.push(p)), { build: half ? LLM_F16 : LLM_F32 });
    const id = half ? LLM_F16 : LLM_F32;
    assert.deepEqual(log.urls.filter((u) => !u.includes("runtime")).sort(), [
      `/v1/voice/models/${id}/model.onnx`,
      `/v1/voice/models/${id}/tokenizer.json`,
      `/v1/voice/models/${id}/tokenizer_config.json`,
    ]);
    assert.ok(log.urls.includes("/v1/voice/models/runtime/ort-wasm-simd-threaded.asyncify.wasm"));
    assert.equal(seen[0], 5);
    assert.equal(seen.at(-1), 100);
    assert.ok(seen.every((p, i) => i === 0 || p >= 5));
  }
});

test("it answers greedily until the model ends its turn", async () => {
  for (const half of [true, false]) {
    const { deps, log } = fakes({ half, says: [7, 8] });
    const m = new BrowserModel(deps);
    await m.load(() => {});
    assert.equal(await m.generate([{ role: "user", content: "hi" }]), "7,8");
    assert.match(log.tokenized, /<\|im_start\|>user\nhi/);
    // The first step feeds the whole prompt with empty caches; the next one token, with the caches it got.
    const first = log.feeds[0];
    assert.equal(first.input_ids.dims[1], 3);
    assert.equal(first.attention_mask.dims[1], 3);
    assert.deepEqual([...first.position_ids.data], [0n, 1n, 2n]);
    assert.equal(first["past_key_values.0.key"].type, half ? "float16" : "float32");
    assert.deepEqual(first["past_key_values.23.value"].dims, [1, 2, 0, 64]);
    const second = log.feeds[1];
    assert.deepEqual([...second.input_ids.data], [7n]);
    assert.equal(second.attention_mask.dims[1], 4);
    assert.deepEqual([...second.position_ids.data], [3n]);
    assert.deepEqual(second["past_key_values.5.key"].dims, [1, 2, 1, 64]);
  }
});

test("an answer is cut at the most tokens asked for, and a model not loaded says so", async () => {
  const { deps } = fakes({ says: [1, 2, 3, 4, 5] });
  const m = new BrowserModel(deps);
  await assert.rejects(m.generate([]), /not loaded/);
  await m.load(() => {});
  assert.equal(await m.generate([{ role: "user", content: "x" }], 2), "1,2");
});
