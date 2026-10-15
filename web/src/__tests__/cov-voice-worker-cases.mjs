// The local voices' worker (speechModel.worker.ts) on a stand-in worker scope: its files come from
// the page's copy or the gateway, onnxruntime and the phonemizer are stand-ins, and WebGPU is
// whatever each test says the browser has.
// cov-voice-worker.test.mjs runs these scenarios, and cov-voice-worker-cpu.test.mjs once more,
// where Supertonic cannot load at first.
import assert from "node:assert/strict";
import { source, stub } from "./cov-voice-helpers.mjs";
import {
  CB_TOKENIZER,
  MODEL_OUTPUT,
  PHONEME_IDS,
  SUPERTONIC_VOICE,
  VOICE,
  cbVoice,
  enc,
  fakes,
} from "./localvoiceFakes.mjs";

// The scenarios are collected here, not registered: each .test.mjs that runs them declares them
// with its own test() calls, in a process of its own (where a flag it sets first picks the
// variant), so a file node runs always holds its own tests.
export const cases = [];
const test = (...args) => cases.push(args);

const cpuRun = globalThis.COV_VOICE_WORKER_CPU === true;
const RUNTIME_WASM = "/v1/voice/models/runtime/ort-wasm-simd-threaded.wasm";

// The stand-in onnxruntime: the shared fakes' sessions, made through InferenceSession.create.
const { deps } = fakes();
const ort = await deps.ort(enc(RUNTIME_WASM));
const made = { cpu: [], gpu: [] };
const session = (kind) => async (model, opts) => {
  made[kind].push(opts);
  if (new TextDecoder().decode(model).includes("broken")) {
    throw "bad model"; // onnxruntime rejects with what its WebAssembly threw: not an Error
  }
  return ort.createSession(model);
};
const phonemizerCalls = [];
globalThis.__covVoiceFake = {
  cpu: { env: { wasm: {} }, Tensor: ort.Tensor, InferenceSession: { create: session("cpu") } },
  gpu: { env: { wasm: {} }, Tensor: ort.Tensor, InferenceSession: { create: session("gpu") } },
  // The phonemizer module asks for its files the way Emscripten's does, then prints its answer.
  piper: async (o) => {
    phonemizerCalls.push({
      located: o.locateFile("piper_phonemize.wasm"),
      data: new TextDecoder().decode(o.getPreloadedPackage("piper_phonemize.data", 0)),
      wasm: new TextDecoder().decode(o.wasmBinary),
    });
    o.printErr("espeak: a warning nobody reads");
    return {
      callMain() {
        o.print("[espeak] ready");
        o.print(JSON.stringify({ phoneme_ids: PHONEME_IDS, phonemes: ["o", "l", "a"] }));
      },
    };
  },
};
// The packages the worker imports are stand-in modules; the worker itself, and localVoiceCore.ts,
// are the sources as they are (loaded through tsx-hooks.mjs).
const fakeOrt = (kind) =>
  `const f = globalThis.__covVoiceFake.${kind};\nexport const env = f.env, Tensor = f.Tensor, InferenceSession = f.InferenceSession;\n`;
stub("onnxruntime-web/wasm", fakeOrt("cpu"));
stub("onnxruntime-web/webgpu", fakeOrt("gpu"));
stub(
  "@diffusionstudio/piper-wasm/build/piper_phonemize.js",
  "export default (o) => globalThis.__covVoiceFake.piper(o);\n",
);

// The page's copy (Cache Storage), and the gateway behind fetch.
const cached = new Map();
const putRefused = new Set();
globalThis.caches = {
  async open(name) {
    assert.equal(name, "motita-voices-v1");
    return {
      async match(url) {
        return cached.get(url)?.clone();
      },
      async put(url, res) {
        if (putRefused.has(url)) {
          throw new DOMException("full", "QuotaExceededError");
        }
        cached.set(url, res);
      },
    };
  },
};
const missing = new Set();
const fetched = [];
globalThis.fetch = async (url, init) => {
  assert.equal(init.credentials, "same-origin");
  fetched.push(url);
  if (missing.has(url)) {
    return new Response("not here", { status: 404 });
  }
  let body = enc(url);
  if (url.endsWith("model.json")) {
    body = enc(JSON.stringify(VOICE));
  } else if (url.endsWith("chatterbox/tokenizer.json")) {
    body = enc(JSON.stringify(CB_TOKENIZER));
  } else if (url.endsWith("voice.bin")) {
    body = cbVoice();
  } else if (url.endsWith("unicode_indexer.json")) {
    body = enc(JSON.stringify(Array.from({ length: 123 }, (_, i) => i + 1)));
  } else if (url.endsWith("voice.json")) {
    body = enc(JSON.stringify(SUPERTONIC_VOICE));
  } else if (url.includes("broken")) {
    body = enc("broken model");
  }
  return new Response(body, { status: 200 });
};

// The worker's scope: what it posts back, and the WebGPU the browser offers (none at first).
const posted = [];
globalThis.self = {
  navigator: {},
  postMessage(m, transfer) {
    posted.push({ m, transfer });
  },
};
// The stand-ins stay until the end: WebGPU's build is imported when it is first needed.
await source("speechModel.worker.ts");

const send = (data) => self.onmessage({ data });
// next waits for the first message after `from` that matches.
async function next(match, from = 0) {
  for (let i = 0; i < 500; i++) {
    const hit = posted.slice(from).find(({ m }) => match(m));
    if (hit) {
      return hit;
    }
    await new Promise((r) => setImmediate(r));
  }
  throw new Error("no message came: " + JSON.stringify(posted.map((p) => p.m)));
}
const outcome = (id) => (m) => m.id === id && (m.kind === "ready" || m.kind === "failed");
const PIPER = "es_MX-test-high";

// adapter is a graphics card with the features given.
function adapter(features) {
  return {
    limits: { maxBindGroups: 4, maxBufferSize: 1024 },
    features: new Set(features),
    async requestDevice(o) {
      return { device: true, asked: o };
    },
  };
}

if (!cpuRun) {
  test("a Piper voice loads from the gateway, then from the page's copy, and speaks", async () => {
    putRefused.add("/v1/voice/models/es_MX-test-high/model.onnx");
    let from = posted.length;
    send({ kind: "warm", id: PIPER });
    const ready = await next(outcome(PIPER), from);
    assert.deepEqual(ready.m, { kind: "ready", id: PIPER, gpu: false, gpuError: undefined });
    const pcts = posted.slice(from).filter(({ m }) => m.kind === "progress").map(({ m }) => m.pct);
    assert.ok(pcts.length > 0, "its progress is told");
    assert.ok(pcts.every((p) => p >= 0), `no progress below 0: ${pcts}`);
    assert.ok(pcts.every((p) => p <= 100), `no progress above 100: ${pcts}`);
    assert.deepEqual(made.cpu.at(-1), { executionProviders: ["wasm"] }, "no weights apart");
    assert.deepEqual(phonemizerCalls, [
      {
        located: "piper_phonemize.wasm",
        data: "/v1/voice/models/runtime/piper_phonemize.data",
        wasm: "/v1/voice/models/runtime/piper_phonemize.wasm",
      },
    ]);
    assert.ok(cached.has("/v1/voice/models/es_MX-test-high/model.json"), "the copy is kept");
    assert.ok(!cached.has("/v1/voice/models/es_MX-test-high/model.onnx"), "one that does not fit is not");

    from = posted.length;
    send({ kind: "say", seq: 1, id: PIPER, text: "hola" });
    const said = await next((m) => m.seq === 1, from);
    assert.ok(said.m.wav instanceof ArrayBuffer);
    assert.deepEqual(said.transfer, [said.m.wav], "the audio is handed over, not copied");
    const samples = new Int16Array(said.m.wav.slice(44));
    assert.equal(samples.length, MODEL_OUTPUT.length);

    // Forgotten, it loads again: this time from the page's copy, but for the file that did not fit.
    send({ kind: "forget", id: PIPER });
    fetched.length = 0;
    from = posted.length;
    send({ kind: "warm", id: PIPER });
    assert.equal((await next(outcome(PIPER), from)).m.kind, "ready");
    assert.deepEqual(fetched, ["/v1/voice/models/es_MX-test-high/model.onnx"]);
  });

  test("a voice that cannot be had says why: the gateway's answer, or what the runtime threw", async () => {
    missing.add("/v1/voice/models/es_MX-gone-low/model.onnx");
    let from = posted.length;
    send({ kind: "warm", id: "es_MX-gone-low" });
    const failed = await next(outcome("es_MX-gone-low"), from);
    assert.deepEqual(failed.m, {
      kind: "failed",
      id: "es_MX-gone-low",
      error: "HTTP 404 /v1/voice/models/es_MX-gone-low/model.onnx",
    });

    from = posted.length;
    send({ kind: "say", seq: 2, id: "es_MX-broken-low", text: "hola" });
    assert.deepEqual((await next((m) => m.seq === 2, from)).m, { seq: 2, error: "bad model" });
  });

  test("Chatterbox needs WebGPU: none, no card, then a card the browser gives on a second ask", async () => {
    const CB = "chatterbox-default";
    let from = posted.length;
    send({ kind: "warm", id: CB });
    assert.deepEqual((await next(outcome(CB), from)).m, {
      kind: "failed",
      id: CB,
      error: "this browser has no WebGPU in its workers",
    });

    const asked = [];
    self.navigator.gpu = {
      async requestAdapter(o) {
        asked.push(o);
        return null;
      },
    };
    from = posted.length;
    send({ kind: "warm", id: CB });
    assert.deepEqual((await next(outcome(CB), from)).m, {
      kind: "failed",
      id: CB,
      error: "the browser gave no graphics card to use",
    });
    assert.deepEqual(asked, [{ powerPreference: "high-performance" }, undefined], "the fast card, then any");

    // The fast card is refused, any card is given: one without timestamps or half floats.
    const card = adapter([]);
    self.navigator.gpu = {
      async requestAdapter(o) {
        return o ? null : card;
      },
    };
    from = posted.length;
    send({ kind: "warm", id: CB });
    assert.deepEqual((await next(outcome(CB), from)).m, { kind: "ready", id: CB, gpu: true, gpuError: undefined });
    const opts = made.gpu.at(-1);
    assert.equal(opts.executionProviders[0].name, "webgpu");
    assert.deepEqual(opts.executionProviders[0].device.asked, {
      requiredLimits: {
        maxBindGroups: 4,
        maxComputeWorkgroupStorageSize: undefined,
        maxComputeWorkgroupsPerDimension: undefined,
        maxStorageBufferBindingSize: undefined,
        maxBufferSize: 1024,
        maxComputeInvocationsPerWorkgroup: undefined,
        maxComputeWorkgroupSizeX: undefined,
        maxComputeWorkgroupSizeY: undefined,
        maxComputeWorkgroupSizeZ: undefined,
      },
      requiredFeatures: [],
    });
    assert.equal(opts.externalData[0].path, "conditional_decoder.onnx_data", "each part with its weights");
    assert.ok(opts.externalData[0].data instanceof Uint8Array);
    assert.equal(globalThis.__covVoiceFake.gpu.env.logLevel, "error");
    assert.equal(globalThis.__covVoiceFake.gpu.env.wasm.numThreads, 1);

    from = posted.length;
    send({ kind: "say", seq: 3, id: CB, text: "Hola", tune: { noise: 1, noiseW: 1, lang: "es" } });
    assert.ok((await next((m) => m.seq === 3, from)).m.wav instanceof ArrayBuffer);
  });

  test("Supertonic runs on the fast card with its timestamps and half floats", async () => {
    const card = adapter(["shader-f16", "timestamp-query", "subgroups"]);
    self.navigator.gpu = {
      async requestAdapter(o) {
        assert.deepEqual(o, { powerPreference: "high-performance" });
        return card;
      },
    };
    const ST = "supertonic-F1";
    const from = posted.length;
    send({ kind: "warm", id: ST });
    assert.deepEqual((await next(outcome(ST), from)).m, { kind: "ready", id: ST, gpu: true, gpuError: undefined });
    assert.deepEqual(made.gpu.at(-1).executionProviders[0].device.asked.requiredFeatures, [
      "shader-f16",
      "subgroups",
      "timestamp-query",
    ]);
    assert.equal(made.gpu.at(-1).externalData, undefined, "no weights apart");
  });
} else {
  test("Supertonic that cannot load is tried again, and on the CPU says why WebGPU could not", async () => {
    const ST = "supertonic-M1";
    missing.add(RUNTIME_WASM);
    let from = posted.length;
    send({ kind: "warm", id: ST });
    assert.deepEqual((await next(outcome(ST), from)).m, {
      kind: "failed",
      id: ST,
      error: "HTTP 404 " + RUNTIME_WASM,
    });
    missing.clear();
    from = posted.length;
    send({ kind: "warm", id: ST });
    assert.deepEqual((await next(outcome(ST), from)).m, {
      kind: "ready",
      id: ST,
      gpu: false,
      gpuError: "this browser has no WebGPU in its workers",
    });
    assert.equal(globalThis.__covVoiceFake.cpu.env.wasm.numThreads, 1);
    assert.equal(globalThis.__covVoiceFake.cpu.env.wasm.proxy, false);
  });
}
