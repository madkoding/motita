// The browser model's worker (browserLLM.worker.ts) on a stand-in worker scope: its files come from
// the gateway, onnxruntime and the tokenizer are stand-ins, and WebGPU is whatever each test says
// the browser has.
import test from "node:test";
import assert from "node:assert/strict";
import { source, stub } from "./cov-voice-helpers.mjs";

const STOP = 151645;
class Tensor {
  constructor(type, data, dims) {
    Object.assign(this, { type, data, dims });
  }
  async getData() {
    return this.data;
  }
}
const made = [];
globalThis.__covLLM = {
  env: { wasm: {} },
  Tensor,
  InferenceSession: {
    async create(model, opts) {
      made.push({ model: new TextDecoder().decode(model), opts });
      return {
        // It says token 5 for the prompt and stops after it, or fails on a prompt that says "boom".
        async run(feeds) {
          if (feeds.input_ids.data[0] === 99n) {
            throw new Error("out of memory");
          }
          const first = feeds["past_key_values.0.key"].dims[2] === 0;
          const scores = new Array(STOP + 1).fill(0);
          scores[first ? 5 : STOP] = 1;
          const out = {
            logits: new Tensor("float32", scores, [1, 1, STOP + 1]),
          };
          for (let i = 0; i < 24; i++) {
            out[`present.${i}.key`] = new Tensor("float32", [], [1, 2, 1, 64]);
            out[`present.${i}.value`] = new Tensor(
              "float32",
              [],
              [1, 2, 1, 64],
            );
          }
          return out;
        },
      };
    },
  },
};
const fakeOrt = `const f = globalThis.__covLLM;\nexport const env = f.env, Tensor = f.Tensor, InferenceSession = f.InferenceSession;\n`;
stub("onnxruntime-web/wasm", fakeOrt);
stub("onnxruntime-web/webgpu", fakeOrt);
stub(
  "@huggingface/tokenizers",
  `export class Tokenizer {
     constructor(json, config) { this.json = json; this.config = config; }
     encode(text) { return { ids: [text.includes("boom") ? 99 : 1] }; }
     decode(ids) { return "title " + ids.join(","); }
   }`,
);
globalThis.caches = {
  async open() {
    return { match: async () => undefined, put: async () => undefined };
  },
};
const missing = new Set();
globalThis.fetch = async (url) => {
  if (missing.has(url)) {
    return new Response("not here", { status: 404 });
  }
  const body = url.endsWith(".json") ? "{}" : url;
  return new Response(new TextEncoder().encode(body), { status: 200 });
};

const posted = [];
const card = (features, info) => ({
  info,
  limits: { maxBindGroups: 4 },
  features: new Set(features),
  requestDevice: async () => ({}),
});
globalThis.self = {
  navigator: {
    gpu: {
      requestAdapter: async () =>
        card(["shader-f16"], { vendor: "nvidia", architecture: "ada" }),
    },
  },
  postMessage(m) {
    posted.push(m);
  },
};
await source("browserLLM.worker.ts");

const send = (data) => self.onmessage({ data });
async function next(match, from) {
  for (let i = 0; i < 500; i++) {
    const hit = posted.slice(from).find(match);
    if (hit) {
      return hit;
    }
    await new Promise((r) => setImmediate(r));
  }
  throw new Error("no message came: " + JSON.stringify(posted.slice(from)));
}
const outcome = (m) => m.kind === "ready" || m.kind === "failed";

test("it loads the build the card runs, with its progress, and writes an answer", async () => {
  let from = posted.length;
  send({ kind: "load" });
  assert.deepEqual(await next(outcome, from), {
    kind: "ready",
    build: "llm-qwen25-0.5b",
    name: "nvidia ada",
    software: false,
  });
  assert.ok(
    posted.slice(from).some((m) => m.kind === "progress"),
    "its progress is told",
  );
  assert.equal(
    made.at(-1).model,
    "/v1/voice/models/llm-qwen25-0.5b/model.onnx",
  );
  assert.deepEqual(made.at(-1).opts.executionProviders[0].name, "webgpu");

  from = posted.length;
  send({
    kind: "generate",
    seq: 1,
    messages: [{ role: "user", content: "hi" }],
  });
  assert.deepEqual(await next((m) => m.seq === 1, from), {
    seq: 1,
    text: "title 5",
  });
});

test("a request the model cannot run is answered with why", async () => {
  const from = posted.length;
  send({
    kind: "generate",
    seq: 2,
    messages: [{ role: "user", content: "boom" }],
  });
  assert.deepEqual(await next((m) => m.seq === 2, from), {
    seq: 2,
    error: "out of memory",
  });
});

test("a card without 16-bit floats runs the larger build", async () => {
  self.navigator.gpu.requestAdapter = async () => card([]);
  const from = posted.length;
  send({ kind: "load" });
  assert.deepEqual(await next(outcome, from), {
    kind: "ready",
    build: "llm-qwen25-0.5b-f32",
    name: "",
    software: false,
  });
});

test("a file the gateway lacks, or a browser without WebGPU, says why", async () => {
  missing.add("/v1/voice/models/llm-qwen25-0.5b-f32/model.onnx");
  let from = posted.length;
  send({ kind: "load" });
  assert.deepEqual(await next(outcome, from), {
    kind: "failed",
    error: "HTTP 404 /v1/voice/models/llm-qwen25-0.5b-f32/model.onnx",
  });
  // No graphics card handed out, and then no WebGPU in the worker at all.
  missing.clear();
  self.navigator.gpu.requestAdapter = async () => null;
  from = posted.length;
  send({ kind: "load" });
  assert.equal((await next(outcome, from)).kind, "failed");
  self.navigator = {};
  from = posted.length;
  send({ kind: "load" });
  assert.match((await next(outcome, from)).error, /no WebGPU/);
});
