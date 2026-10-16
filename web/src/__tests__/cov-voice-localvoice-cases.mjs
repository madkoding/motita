// localVoice.ts, the page's side of local voices: the gateway's catalog and the page's copies of
// its files, and the workers that say each sentence and tell how far a voice is from loaded.
// cov-voice-localvoice-lib.test.mjs runs these scenarios, and cov-voice-localvoice-nocache.test.mjs
// once more in a browser without Cache Storage.
import assert from "node:assert/strict";
import { setupDom, source, json } from "./cov-voice-helpers.mjs";

// The scenarios are collected here, not registered: each .test.mjs that runs them declares them
// with its own test() calls, in a process of its own (where a flag it sets first picks the
// variant), so a file node runs always holds its own tests.
export const cases = [];
const test = (...args) => cases.push(args);

setupDom("http://localhost/");
const noCache = globalThis.COV_VOICE_NO_CACHE === true;
const lv = await source("localVoice.ts");

const enc = (s) => new TextEncoder().encode(s).buffer;
const CATALOG = "/v1/voice/models";
// apiOf answers from a table of "METHOD path" (GET is the bare path) -> Response maker.
function apiOf(routes) {
  const calls = [];
  const api = async (path, init) => {
    const key = (init?.method ? init.method + " " : "") + path;
    calls.push(key);
    const r = routes[key];
    return r ? r() : json({ error: "not found" }, 404);
  };
  return { api, calls };
}
const model = (id, engine, installed = true) => ({ id, engine, installed });
const cat = (models) => ({
  runtime: { size: 1, installed: true },
  models,
  download: null,
});

// A stand-in worker: it records what it was asked, and the test answers for it.
const workers = [];
globalThis.Worker = class {
  constructor(url, opts) {
    assert.match(String(url), /speechModel\.worker\.ts$/);
    assert.equal(opts.type, "module");
    this.posted = [];
    workers.push(this);
  }
  postMessage(m) {
    this.posted.push(m);
  }
};
const says = (w) => w.posted.filter((m) => m.kind === "say");
const answer = (w, m, data) => w.onmessage({ data: { seq: m.seq, ...data } });

if (noCache) {
  test("without Cache Storage the catalog is read and a removal is the gateway's alone", async () => {
    delete globalThis.caches;
    const { api, calls } = apiOf({
      [CATALOG]: () => json(cat([model("a", "piper")])),
      "DELETE /v1/voice/models/a": () => new Response(null, { status: 204 }),
    });
    assert.deepEqual(
      (await lv.fetchCatalog(api)).models.map((m) => m.id),
      ["a"],
    );
    await lv.deleteVoice(api, "a");
    assert.deepEqual(calls, [CATALOG, "DELETE /v1/voice/models/a"]);
    assert.equal(lv.localVoicesSupported(), false);
  });
} else {
  test("the first catalog read drops the copies no voice of it uses, once", async () => {
    const page = "http://localhost/v1/voice/models/";
    const keys = [
      "old-voice/model.onnx",
      "runtime/ort.wasm",
      "a/model.onnx",
      "supertonic/vocoder.onnx",
      "a/ort-wasm-simd-threaded.asyncify.wasm",
      "runtime/ort-wasm-simd-threaded.asyncify.wasm",
    ]
      .map((f) => ({ url: page + f }))
      .concat([{ url: "http://localhost/elsewhere" }]);
    const dropped = [];
    globalThis.caches = {
      open: async (name) => {
        assert.equal(name, "motita-voices-v1");
        return {
          keys: async () => keys,
          delete: async (r) => dropped.push(r.url),
        };
      },
    };
    const { api } = apiOf({
      [CATALOG]: () => json(cat([model("a", "piper")])),
    });
    await lv.fetchCatalog(api);
    await new Promise((r) => setTimeout(r, 0));
    assert.deepEqual(dropped, [
      page + "old-voice/model.onnx",
      page + "a/ort-wasm-simd-threaded.asyncify.wasm",
    ]);
    await lv.fetchCatalog(api);
    await new Promise((r) => setTimeout(r, 0));
    assert.equal(dropped.length, 2, "once a page");
    assert.equal(lv.localVoicesSupported(), true);
  });

  test("the browser is asked to keep the copies: once a page, and only when it can be", async () => {
    assert.equal(await lv.askToKeep(undefined), false, "no storage manager");
    assert.equal(await lv.askToKeep({}), false, "one that cannot be asked");
    const asked = [];
    const storage = (kept, grant) => ({
      persisted: async () => kept,
      persist: async () => {
        asked.push(grant);
        if (grant instanceof Error) {
          throw grant;
        }
        return grant;
      },
    });
    assert.equal(
      await lv.askToKeep(storage(true, false)),
      true,
      "already kept: not asked again",
    );
    assert.equal(await lv.askToKeep(storage(false, true)), true, "granted");
    assert.equal(await lv.askToKeep(storage(false, false)), false, "refused");
    assert.equal(
      await lv.askToKeep(storage(false, new Error("no"))),
      false,
      "a failure is a refusal",
    );
    assert.deepEqual(asked.length, 3);
    assert.equal(lv.keepOnDisk(), lv.keepOnDisk(), "asked once a page");
  });

  test("whether the browser keeps the copies is read without asking", async () => {
    assert.equal(await lv.storageKept(undefined), false, "no storage manager");
    assert.equal(await lv.storageKept({}), false, "one that cannot say");
    assert.equal(await lv.storageKept({ persisted: async () => true }), true);
    assert.equal(await lv.storageKept({ persisted: async () => false }), false);
    assert.equal(
      await lv.storageKept({
        persisted: async () => {
          throw new Error("no");
        },
      }),
      false,
    );
    assert.equal(
      typeof (await lv.storageKept()),
      "boolean",
      "the browser's own, by default",
    );
  });

  test("the gateway's refusals are told in its words, or by their status", async () => {
    const { api } = apiOf({
      "POST /v1/voice/models/a": () => json({}, 202),
      "POST /v1/voice/models/b": () =>
        json({ error: "another voice is downloading" }, 409),
      "POST /v1/voice/models/c": () => json({}, 503),
      "POST /v1/voice/models/d": () => new Response("<html>", { status: 500 }),
      "/v1/voice/models/a/sample": () => new Response("RIFF", { status: 200 }),
    });
    await lv.installVoice(api, "a");
    await assert.rejects(
      lv.installVoice(api, "b"),
      /another voice is downloading/,
    );
    await assert.rejects(lv.installVoice(api, "c"), /HTTP 503/);
    await assert.rejects(lv.installVoice(api, "d"), /HTTP 500/);
    assert.equal(await (await lv.sampleOf(api, "a")).text(), "RIFF");
    await assert.rejects(lv.sampleOf(api, "zz"), /not found/);
    await assert.rejects(lv.fetchCatalog(api), /not found/);
    await assert.rejects(lv.deleteVoice(api, "zz"), /not found/);
  });

  test("a removed voice leaves the page's copy, with its engine's model when it was the last", async () => {
    const deleted = [];
    globalThis.caches = {
      open: async () => ({ delete: async (u) => deleted.push(u) }),
    };
    const gone = () => new Response(null, { status: 204 });
    const { api, calls } = apiOf({
      "DELETE /v1/voice/models/es-a": gone,
      "DELETE /v1/voice/models/chatterbox-a": gone,
      "DELETE /v1/voice/models/chatterbox-b": gone,
      [CATALOG]: () =>
        json(
          cat([
            model("chatterbox-b", "chatterbox", true),
            model("chatterbox-a", "chatterbox", false),
          ]),
        ),
    });
    await lv.deleteVoice(api, "es-a");
    assert.deepEqual(
      calls,
      ["DELETE /v1/voice/models/es-a"],
      "a Piper voice has no engine model to look up",
    );
    assert.equal(deleted.length, 4);
    deleted.length = 0;
    await lv.deleteVoice(api, "chatterbox-a");
    assert.equal(
      deleted.length,
      4,
      "another Chatterbox voice is installed: its model stays",
    );
    deleted.length = 0;
    const last = apiOf({
      "DELETE /v1/voice/models/chatterbox-b": gone,
      [CATALOG]: () => json(cat([])),
    });
    await lv.deleteVoice(last.api, "chatterbox-b");
    assert.deepEqual(deleted.slice(4), [
      "/v1/voice/models/chatterbox/tokenizer.json",
      "/v1/voice/models/chatterbox/embed_tokens.onnx",
      "/v1/voice/models/chatterbox/embed_tokens.onnx_data",
      "/v1/voice/models/chatterbox/language_model_q4.onnx",
      "/v1/voice/models/chatterbox/language_model_q4.onnx_data",
      "/v1/voice/models/chatterbox/conditional_decoder.onnx",
      "/v1/voice/models/chatterbox/conditional_decoder.onnx_data",
    ]);
  });

  test("loading a voice: its progress, its failure, asked again only when it failed", async () => {
    let changes = 0;
    const off = lv.onLoadState(() => changes++);
    lv.warmLocal("v1");
    const w = workers.at(-1);
    assert.deepEqual(w.posted, [{ kind: "warm", id: "v1" }]);
    lv.warmLocal("v1");
    assert.equal(w.posted.length, 1, "a voice loading is not asked for again");
    w.onmessage({ data: { kind: "progress", id: "v1", pct: 40 } });
    assert.deepEqual(lv.loadStateOf("v1"), { pct: 40, ready: false });
    w.onmessage({ data: { kind: "failed", id: "v1", error: "no memory" } });
    assert.deepEqual(lv.loadStateOf("v1"), {
      pct: 0,
      ready: false,
      error: "no memory",
    });
    lv.warmLocal("v1");
    assert.equal(w.posted.length, 2, "a voice that failed is asked for again");
    w.onmessage({
      data: { kind: "ready", id: "v1", gpu: true, gpuError: "slow card" },
    });
    assert.deepEqual(lv.loadStateOf("v1"), {
      pct: 100,
      ready: true,
      gpu: true,
      gpuError: "slow card",
    });
    lv.warmLocal("v1");
    assert.equal(w.posted.length, 2, "a ready voice is not loaded again");
    off();
    assert.equal(changes, 5);

    // Removing it tells the worker to forget it, and its state goes.
    globalThis.caches = { open: async () => ({ delete: async () => true }) };
    const { api } = apiOf({
      "DELETE /v1/voice/models/v1": () => new Response(null, { status: 204 }),
    });
    await lv.deleteVoice(api, "v1");
    assert.deepEqual(w.posted.at(-1), { kind: "forget", id: "v1" });
    assert.equal(lv.loadStateOf("v1"), undefined);
  });

  test("a sentence is said by the worker, in its language, and a late or failed one is dropped", async () => {
    const say = lv.localClip("v2", { noise: 0.5, noiseW: 1.5 });
    assert.equal(say.ahead, 1);
    const w = workers[0];
    w.posted.length = 0;
    const plain = say("Hola.", new AbortController().signal);
    assert.deepEqual(
      says(w)[0].tune,
      { noise: 0.5, noiseW: 1.5 },
      "no language: the tune as given",
    );
    answer(w, says(w)[0], { wav: enc("RIFF") });
    const blob = await plain;
    assert.equal(blob.type, "audio/wav");
    assert.equal(await blob.text(), "RIFF");

    const english = lv.localClip("v2")(
      "Hi.",
      new AbortController().signal,
      "en",
    );
    assert.deepEqual(says(w)[1].tune, { noise: 1, noiseW: 1, lang: "en" });
    answer(w, says(w)[1], { error: "the model failed" });
    await assert.rejects(english, /the model failed/);

    const bare = lv.localClip("v2")("Hey.", new AbortController().signal);
    assert.equal(says(w)[2].tune, undefined);
    answer(w, says(w)[2], { wav: enc("x") });
    await bare;

    // Aborted: dropped at once, and its audio, when it comes, goes nowhere.
    const ab = new AbortController();
    const late = say("Tarde.", ab.signal);
    ab.abort();
    await assert.rejects(late, { name: "AbortError" });
    answer(w, says(w)[3], { wav: enc("late") });
  });

  test("Supertonic on the CPU says sentences in helper workers; a dead one fails only its own", async () => {
    for (const w of workers) {
      w.onerror();
    }
    workers.length = 0;
    const cores = (n) =>
      Object.defineProperty(navigator, "hardwareConcurrency", {
        value: n,
        configurable: true,
      });
    cores(8);
    const id = "supertonic-F1";
    assert.equal(lv.lanes(id), lv.SUPERTONIC_LANES);
    assert.equal(lv.lanes("v1"), 1);
    lv.warmLocal(id);
    assert.equal(workers.length, 1, "the helpers wait for the first worker");
    workers[0].onmessage({ data: { kind: "ready", id } });
    assert.equal(workers.length, 2);
    assert.deepEqual(workers[1].posted, [{ kind: "warm", id }]);
    workers[1].onmessage({ data: { kind: "progress", id, pct: 5 } });
    assert.equal(
      lv.loadStateOf(id).ready,
      true,
      "a helper's loading is not shown",
    );

    const say = lv.localClip(id);
    assert.equal(say.ahead, 2);
    const signal = new AbortController().signal;
    const a = say("a", signal);
    const b = say("b", signal);
    assert.deepEqual(
      workers.map((w) => says(w).map((m) => m.text)),
      [["a"], ["b"]],
    );
    answer(workers[1], says(workers[1])[0], { wav: enc("b") });
    assert.equal(await (await b).text(), "b");
    // The helper's own sentences die with it; the first worker's wait.
    const c = say("c", signal);
    workers[1].onerror();
    await assert.rejects(c, /the local voice stopped/);
    assert.equal(
      lv.loadStateOf(id).ready,
      true,
      "a helper's death leaves the voice loaded",
    );
    answer(workers[0], says(workers[0])[0], { wav: enc("a") });
    assert.equal(await (await a).text(), "a");
    // A message about a removal goes to the live workers only.
    globalThis.caches = { open: async () => ({ delete: async () => true }) };
    const { api } = apiOf({
      "DELETE /v1/voice/models/es-x": () => new Response(null, { status: 204 }),
    });
    await lv.deleteVoice(api, "es-x");
    assert.deepEqual(workers[0].posted.at(-1), { kind: "forget", id: "es-x" });

    // The first worker dies: every voice it had is to be loaded again.
    workers[0].onerror();
    assert.equal(lv.loadStateOf(id), undefined);

    // Where the browser does not say how many cores it has, one worker does it all.
    Object.defineProperty(navigator, "hardwareConcurrency", {
      value: undefined,
      configurable: true,
    });
    assert.equal(lv.lanes(id), 1);
    const nav = Object.getOwnPropertyDescriptor(globalThis, "navigator");
    Object.defineProperty(globalThis, "navigator", {
      value: undefined,
      configurable: true,
    });
    assert.equal(lv.lanes(id), 1, "nor without a navigator");
    Object.defineProperty(globalThis, "navigator", nav);
  });
}
