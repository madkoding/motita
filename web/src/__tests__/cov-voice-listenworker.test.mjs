// The listening worker (speechListen.worker.ts) on a stand-in worker scope: its files come from the
// page's copy or the gateway, onnxruntime is a stand-in, and WebGPU is whatever each test says the
// browser has.
import test from "node:test";
import assert from "node:assert/strict";
import { source, stub } from "./cov-voice-helpers.mjs";
import { GEN, decoder, encoder, tokenizer } from "./listenFakes.mjs";

// The stand-in onnxruntime: InferenceSession.create answers the encoder or a decoder by file name.
const made = [];
const session = (kind) => async (model, opts) => {
  const name = new TextDecoder().decode(model);
  made.push({ kind, name, opts });
  if (name.includes("broken")) {
    throw new Error("bad model");
  }
  return name.endsWith("encoder.onnx") ? encoder : decoder();
};
class Tensor {
  constructor(type, data, dims) {
    Object.assign(this, { type, data, dims });
  }
}
globalThis.__covListenFake = {
  cpu: {
    env: { wasm: {} },
    Tensor,
    InferenceSession: { create: session("cpu") },
  },
  gpu: {
    env: { wasm: {} },
    Tensor,
    InferenceSession: { create: session("gpu") },
  },
};
const fakeOrt = (kind) =>
  `const f = globalThis.__covListenFake.${kind};\nexport const env = f.env, Tensor = f.Tensor, InferenceSession = f.InferenceSession;\n`;
stub("onnxruntime-web/wasm", fakeOrt("cpu"));
stub("onnxruntime-web/webgpu", fakeOrt("gpu"));

// No copy in the page: every file comes from the gateway, a missing one as a 404.
globalThis.caches = {
  async open() {
    return { match: async () => undefined, put: async () => undefined };
  },
};
const missing = new Set();
const fetched = [];
globalThis.fetch = async (url) => {
  fetched.push(url);
  if (missing.has(url)) {
    return new Response("not here", { status: 404 });
  }
  let body = url;
  if (url.endsWith("tokenizer.json")) {
    body = tokenizer();
  } else if (url.endsWith("generation_config.json")) {
    body = GEN;
  }
  return new Response(new TextEncoder().encode(body), { status: 200 });
};

const posted = [];
globalThis.self = {
  navigator: {},
  postMessage(m) {
    posted.push(m);
  },
};
await source("speechListen.worker.ts");

const send = (data) => self.onmessage({ data });
async function next(match, from) {
  for (let i = 0; i < 500; i++) {
    const hit = posted.slice(from).find(match);
    if (hit) {
      return hit;
    }
    await new Promise((r) => setImmediate(r));
  }
  throw new Error("no message came: " + JSON.stringify(posted));
}
const outcome = (id) => (m) =>
  m.id === id && (m.kind === "ready" || m.kind === "failed");

test("a model loads with its progress, writes down a recording, and is forgotten", async () => {
  let from = posted.length;
  send({ kind: "warm", id: "whisper-base" });
  assert.deepEqual(await next(outcome("whisper-base"), from), {
    kind: "ready",
    id: "whisper-base",
    gpu: false,
    gpuError: undefined,
  });
  assert.ok(
    posted.slice(from).some((m) => m.kind === "progress"),
    "its progress is told",
  );
  assert.deepEqual(made.at(-1).opts, { executionProviders: ["wasm"] });

  from = posted.length;
  send({
    kind: "hear",
    seq: 1,
    id: "whisper-base",
    samples: new Float32Array(1600),
    lang: "",
  });
  assert.deepEqual(await next((m) => m.seq === 1, from), {
    seq: 1,
    text: "mundo hola!",
    lang: "es",
  });

  send({ kind: "forget", id: "whisper-base" });
  const before = made.length;
  from = posted.length;
  send({ kind: "warm", id: "whisper-base" });
  await next(outcome("whisper-base"), from);
  assert.equal(made.length, before + 2, "forgotten, it loads again");
});

test("a model that cannot be had says why, and so does a recording it cannot write down", async () => {
  missing.add("/v1/voice/models/whisper-gone/encoder.onnx");
  let from = posted.length;
  send({ kind: "warm", id: "whisper-gone" });
  assert.deepEqual(await next(outcome("whisper-gone"), from), {
    kind: "failed",
    id: "whisper-gone",
    error: "HTTP 404 /v1/voice/models/whisper-gone/encoder.onnx",
  });
  from = posted.length;
  send({
    kind: "hear",
    seq: 2,
    id: "whisper-broken",
    samples: new Float32Array(10),
    lang: "es",
  });
  assert.deepEqual(await next((m) => m.seq === 2, from), {
    seq: 2,
    error: "bad model",
  });
});

test("the model for the graphics card runs there, or says why it could not", async () => {
  let from = posted.length;
  send({ kind: "warm", id: "whisper-small-gpu" });
  assert.deepEqual(await next(outcome("whisper-small-gpu"), from), {
    kind: "ready",
    id: "whisper-small-gpu",
    gpu: false,
    gpuError: "this browser has no WebGPU in its workers",
  });
  assert.ok(
    !fetched.some((u) => u.endsWith(".asyncify.wasm")),
    "without WebGPU its runtime is not fetched",
  );
  // Asked to load WebGPU's runtime anyway, it says the same.
  const { gpuOrt } = await source("workerOrt.ts");
  await assert.rejects(gpuOrt(new ArrayBuffer(0)), /no WebGPU in its workers/);

  self.navigator.gpu = {
    async requestAdapter() {
      return {
        limits: {},
        features: new Set(),
        async requestDevice(o) {
          return { asked: o };
        },
      };
    },
  };
  from = posted.length;
  send({ kind: "warm", id: "whisper-base-gpu" });
  assert.deepEqual(await next(outcome("whisper-base-gpu"), from), {
    kind: "ready",
    id: "whisper-base-gpu",
    gpu: true,
    gpuError: undefined,
  });
  assert.equal(made.at(-1).kind, "gpu");
  assert.equal(made.at(-1).opts.executionProviders[0].name, "webgpu");
});
