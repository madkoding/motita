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
      page + "supertonic/vocoder.onnx",
      page + "a/ort-wasm-simd-threaded.asyncify.wasm",
    ]);
    await lv.fetchCatalog(api);
    await new Promise((r) => setTimeout(r, 0));
    assert.equal(dropped.length, 3, "once a page");
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

  test("a removed voice leaves the page's copy of its files, and the runtime stays", async () => {
    const deleted = [];
    globalThis.caches = {
      open: async () => ({ delete: async (u) => deleted.push(u) }),
    };
    const { api, calls } = apiOf({
      "DELETE /v1/voice/models/es-a": () => new Response(null, { status: 204 }),
    });
    await lv.deleteVoice(api, "es-a");
    assert.deepEqual(calls, ["DELETE /v1/voice/models/es-a"]);
    assert.deepEqual(deleted, [
      "/v1/voice/models/es-a/model.onnx",
      "/v1/voice/models/es-a/model.json",
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
    w.onmessage({ data: { kind: "ready", id: "v1" } });
    assert.deepEqual(lv.loadStateOf("v1"), { pct: 100, ready: true });
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

  test("a sentence is prepared for the voice's language, said by the worker, and a late or failed one is dropped", async () => {
    const say = lv.localClip("es_MX-claude-high", { noise: 0.5, noiseW: 1.5 });
    const w = workers[0];
    w.posted.length = 0;
    const plain = say(
      "El Sr. López tiene 25 años",
      new AbortController().signal,
    );
    assert.deepEqual(says(w)[0].tune, { noise: 0.5, noiseW: 1.5 });
    assert.equal(
      says(w)[0].text,
      "El Señor López tiene veinticinco años.",
      "the language of the voice, from its id",
    );
    answer(w, says(w)[0], { wav: enc("RIFF") });
    const blob = await plain;
    assert.equal(blob.type, "audio/wav");
    assert.equal(await blob.text(), "RIFF");

    const english = lv.localClip("es_MX-claude-high")(
      "Mr. Smith is 25",
      new AbortController().signal,
      "en",
    );
    assert.equal(says(w)[1].text, "Mister Smith is twenty-five.");
    assert.equal(says(w)[1].tune, undefined);
    answer(w, says(w)[1], { error: "the model failed" });
    await assert.rejects(english, /the model failed/);

    // Aborted: dropped at once, and its audio, when it comes, goes nowhere.
    const ab = new AbortController();
    const late = say("Tarde.", ab.signal);
    ab.abort();
    await assert.rejects(late, { name: "AbortError" });
    answer(w, says(w)[2], { wav: enc("late") });
  });

  test("a worker that dies fails what waits for it, and its voices are loaded again", async () => {
    lv.warmLocal("v9");
    const w = workers.at(-1);
    w.onmessage({ data: { kind: "ready", id: "v9" } });
    const say = lv.localClip("en_US-lessac-high");
    const waiting = say("Hello", new AbortController().signal);
    w.onerror();
    await assert.rejects(waiting, /the local voice stopped/);
    assert.equal(lv.loadStateOf("v9"), undefined, "it is loaded again");
    // The next sentence starts a worker of its own.
    const before = workers.length;
    const again = say("Again", new AbortController().signal);
    assert.equal(workers.length, before + 1);
    // A message about a removal goes to the live worker only.
    globalThis.caches = { open: async () => ({ delete: async () => true }) };
    const { api } = apiOf({
      "DELETE /v1/voice/models/es-x": () => new Response(null, { status: 204 }),
    });
    await lv.deleteVoice(api, "es-x");
    assert.deepEqual(workers.at(-1).posted.at(-1), {
      kind: "forget",
      id: "es-x",
    });
    // With no worker, there is nobody to tell.
    workers.at(-1).onerror();
    await assert.rejects(again, /the local voice stopped/);
    await lv.deleteVoice(api, "es-x");
  });
}
