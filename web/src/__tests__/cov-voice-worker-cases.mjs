// The local voices' worker (speechModel.worker.ts) on a stand-in worker scope: its files come from
// the page's copy or the gateway, and onnxruntime and the phonemizer are stand-ins.
// cov-voice-worker.test.mjs runs these scenarios.
import assert from "node:assert/strict";
import { source, stub } from "./cov-voice-helpers.mjs";
import {
  MODEL_OUTPUT,
  PHONEME_IDS,
  VOICE,
  enc,
  fakes,
} from "./localvoiceFakes.mjs";

// The scenarios are collected here, not registered: each .test.mjs that runs them declares them
// with its own test() calls, in a process of its own (where a flag it sets first picks the
// variant), so a file node runs always holds its own tests.
export const cases = [];
const test = (...args) => cases.push(args);

const RUNTIME_WASM = "/v1/voice/models/runtime/ort-wasm-simd-threaded.wasm";

// The stand-in onnxruntime: the shared fakes' sessions, made through InferenceSession.create.
const { deps } = fakes();
const ort = await deps.ort(enc(RUNTIME_WASM));
const made = { cpu: [] };
const session = (kind) => async (model, opts) => {
  made[kind].push(opts);
  if (new TextDecoder().decode(model).includes("broken")) {
    throw "bad model"; // onnxruntime rejects with what its WebAssembly threw: not an Error
  }
  return ort.createSession(model);
};
const phonemizerCalls = [];
globalThis.__covVoiceFake = {
  cpu: {
    env: { wasm: {} },
    Tensor: ort.Tensor,
    InferenceSession: { create: session("cpu") },
  },
  // The phonemizer module asks for its files the way Emscripten's does, then prints its answer.
  piper: async (o) => {
    phonemizerCalls.push({
      located: o.locateFile("piper_phonemize.wasm"),
      data: new TextDecoder().decode(
        o.getPreloadedPackage("piper_phonemize.data", 0),
      ),
      wasm: new TextDecoder().decode(o.wasmBinary),
    });
    o.printErr("espeak: a warning nobody reads");
    return {
      callMain() {
        o.print("[espeak] ready");
        o.print(
          JSON.stringify({
            phoneme_ids: PHONEME_IDS,
            phonemes: ["o", "l", "a"],
          }),
        );
      },
    };
  },
};
// The packages the worker imports are stand-in modules; the worker itself, and localVoiceCore.ts,
// are the sources as they are (loaded through tsx-hooks.mjs).
const fakeOrt = (kind) =>
  `const f = globalThis.__covVoiceFake.${kind};\nexport const env = f.env, Tensor = f.Tensor, InferenceSession = f.InferenceSession;\n`;
stub("onnxruntime-web/wasm", fakeOrt("cpu"));
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
// The gateway tags each file with its pinned hash: asked again with the same tag, it sends nothing.
const revalidated = [];
const pinned = new Map();
const tagOf = (url) => '"' + (pinned.get(url) ?? "sum") + '"';
let gatewayDown = false;
globalThis.fetch = async (url, init) => {
  assert.equal(init.credentials, "same-origin");
  if (gatewayDown) {
    throw new TypeError("Failed to fetch");
  }
  if (init.headers["If-None-Match"] === tagOf(url) && !missing.has(url)) {
    revalidated.push(url);
    return new Response(null, { status: 304 });
  }
  fetched.push(url);
  if (missing.has(url)) {
    return new Response("not here", { status: 404 });
  }
  let body = enc(url);
  if (url.endsWith("model.json")) {
    body = enc(JSON.stringify(VOICE));
  } else if (url.includes("broken")) {
    body = enc("broken model");
  }
  return new Response(body, { status: 200, headers: { ETag: tagOf(url) } });
};

// The worker's scope: what it posts back.
const posted = [];
globalThis.self = {
  navigator: {},
  postMessage(m, transfer) {
    posted.push({ m, transfer });
  },
};
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
const outcome = (id) => (m) =>
  m.id === id && (m.kind === "ready" || m.kind === "failed");
const PIPER = "es_MX-test-high";

test("a Piper voice loads from the gateway, then from the page's copy, and speaks", async () => {
  putRefused.add("/v1/voice/models/es_MX-test-high/model.onnx");
  let from = posted.length;
  send({ kind: "warm", id: PIPER });
  const ready = await next(outcome(PIPER), from);
  assert.deepEqual(ready.m, { kind: "ready", id: PIPER });
  const pcts = posted
    .slice(from)
    .filter(({ m }) => m.kind === "progress")
    .map(({ m }) => m.pct);
  assert.ok(pcts.length > 0, "its progress is told");
  assert.ok(
    pcts.every((p) => p >= 0),
    `no progress below 0: ${pcts}`,
  );
  assert.ok(
    pcts.every((p) => p <= 100),
    `no progress above 100: ${pcts}`,
  );
  assert.deepEqual(
    made.cpu.at(-1),
    { executionProviders: ["wasm"] },
    "no weights apart",
  );
  assert.deepEqual(phonemizerCalls, [
    {
      located: "piper_phonemize.wasm",
      data: "/v1/voice/models/runtime/piper_phonemize.data",
      wasm: "/v1/voice/models/runtime/piper_phonemize.wasm",
    },
  ]);
  assert.ok(
    cached.has("/v1/voice/models/es_MX-test-high/model.json"),
    "the copy is kept",
  );
  assert.ok(
    !cached.has("/v1/voice/models/es_MX-test-high/model.onnx"),
    "one that does not fit is not",
  );

  from = posted.length;
  send({ kind: "say", seq: 1, id: PIPER, text: "hola" });
  const said = await next((m) => m.seq === 1, from);
  assert.ok(said.m.wav instanceof ArrayBuffer);
  assert.deepEqual(
    said.transfer,
    [said.m.wav],
    "the audio is handed over, not copied",
  );
  const samples = new Int16Array(said.m.wav.slice(44));
  assert.equal(samples.length, MODEL_OUTPUT.length);

  // Forgotten, it loads again: this time from the page's copy, but for the file that did not fit.
  send({ kind: "forget", id: PIPER });
  fetched.length = 0;
  from = posted.length;
  send({ kind: "warm", id: PIPER });
  assert.equal((await next(outcome(PIPER), from)).m.kind, "ready");
  assert.deepEqual(fetched, ["/v1/voice/models/es_MX-test-high/model.onnx"]);
  assert.ok(
    revalidated.includes("/v1/voice/models/es_MX-test-high/model.json"),
    "the copy is asked for with its tag",
  );
});

test("the page's copy outlives an update: kept while the gateway pins it, fetched anew when it does not", async () => {
  const json = "/v1/voice/models/es_MX-test-high/model.json";
  // A newer catalog pins another file under the same name: its bytes come again.
  pinned.set(json, "newer");
  send({ kind: "forget", id: PIPER });
  fetched.length = 0;
  let from = posted.length;
  send({ kind: "warm", id: PIPER });
  assert.equal((await next(outcome(PIPER), from)).m.kind, "ready");
  assert.ok(fetched.includes(json), "a file pinned anew is fetched again");
  assert.equal(
    cached.get(json).headers.get("ETag"),
    '"newer"',
    "and kept with its new tag",
  );

  // Without the gateway (offline, or it restarts), the copy is read as it is.
  gatewayDown = true;
  send({ kind: "forget", id: PIPER });
  from = posted.length;
  send({ kind: "warm", id: PIPER });
  const offline = (await next(outcome(PIPER), from)).m;
  gatewayDown = false;
  assert.equal(
    offline.kind,
    "failed",
    "the file that never fit has no copy to fall back to",
  );
  assert.match(offline.error, /Failed to fetch/);
  putRefused.clear();
  send({ kind: "forget", id: PIPER });
  from = posted.length;
  send({ kind: "warm", id: PIPER });
  assert.equal((await next(outcome(PIPER), from)).m.kind, "ready");
  gatewayDown = true;
  send({ kind: "forget", id: PIPER });
  from = posted.length;
  send({ kind: "warm", id: PIPER });
  const kept = (await next(outcome(PIPER), from)).m;
  gatewayDown = false;
  assert.equal(kept.kind, "ready", "every file read from the copy");

  // A file the gateway no longer has (removed from another device) is read from the copy too.
  missing.add(json);
  send({ kind: "forget", id: PIPER });
  from = posted.length;
  send({ kind: "warm", id: PIPER });
  assert.equal((await next(outcome(PIPER), from)).m.kind, "ready");
  missing.delete(json);
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
  assert.deepEqual((await next((m) => m.seq === 2, from)).m, {
    seq: 2,
    error: "bad model",
  });
});
