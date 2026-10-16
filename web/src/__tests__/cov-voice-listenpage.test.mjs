// Local speech input on the page's side (localListen.ts): the worker it talks to is a stand-in that
// records what it is sent, and the browser's audio decoding is one too.
import test from "node:test";
import assert from "node:assert/strict";
import { source } from "./cov-voice-helpers.mjs";

// The workers made, newest last, each with what it was sent.
const workers = [];
globalThis.Worker = class {
  constructor(url, opts) {
    this.url = String(url);
    this.opts = opts;
    this.sent = [];
    workers.push(this);
  }
  postMessage(m, transfer) {
    this.sent.push({ m, transfer });
  }
  terminate() {
    this.terminated = true;
  }
};
// The browser decodes a recording into two channels, 16 kHz.
const decoded = [];
globalThis.OfflineAudioContext = class {
  constructor(channels, length, rate) {
    Object.assign(this, { channels, length, rate });
  }
  async decodeAudioData(buf) {
    decoded.push({ rate: this.rate, bytes: buf.byteLength });
    const left = Float32Array.from([1, 0.5, 0]);
    const right = Float32Array.from([0, 0.5, 1]);
    return { length: 3, numberOfChannels: 2, getChannelData: (c) => (c === 0 ? left : right) };
  }
};
globalThis.caches = {};

const L = await source("localListen.ts");
const worker = () => workers.at(-1);
const answer = (m) => worker().onmessage({ data: m });

test("a model is loaded once, and its loading is followed until ready or failed", () => {
  // Nothing to forget before any worker.
  L.forgetListener("whisper-base");
  assert.equal(workers.length, 0);

  let told = 0;
  const off = L.onListenState(() => told++);
  L.warmListener("whisper-base");
  assert.equal(workers.length, 1);
  assert.match(worker().url, /speechListen\.worker\.ts$/);
  assert.deepEqual(worker().opts, { type: "module", name: "" });
  assert.deepEqual(worker().sent[0].m, { kind: "warm", id: "whisper-base" });
  assert.deepEqual(L.listenStateOf("whisper-base"), { pct: 0, ready: false });
  L.warmListener("whisper-base");
  assert.equal(worker().sent.length, 1, "one loading is enough");

  answer({ kind: "progress", id: "whisper-base", pct: 40 });
  assert.deepEqual(L.listenStateOf("whisper-base"), { pct: 40, ready: false });
  answer({ kind: "ready", id: "whisper-base", gpu: false });
  assert.deepEqual(L.listenStateOf("whisper-base"), { pct: 100, ready: true });
  answer({ kind: "ready", id: "whisper-base-gpu", gpu: true, gpuError: "x" });
  assert.deepEqual(L.listenStateOf("whisper-base-gpu"), { pct: 100, ready: true, gpu: true, gpuError: "x" });

  answer({ kind: "failed", id: "whisper-small", error: "HTTP 404" });
  assert.deepEqual(L.listenStateOf("whisper-small"), { pct: 0, ready: false, error: "HTTP 404" });
  L.warmListener("whisper-small");
  assert.deepEqual(worker().sent.at(-1).m, { kind: "warm", id: "whisper-small" }, "a failed one is tried again");

  L.forgetListener("whisper-base");
  assert.deepEqual(worker().sent.at(-1).m, { kind: "forget", id: "whisper-base" });
  assert.equal(L.listenStateOf("whisper-base"), undefined);
  assert.ok(told >= 6);
  off();
  const before = told;
  L.forgetListener("whisper-small");
  assert.equal(told, before, "no longer told");
});

test("a recording is decoded at 16 kHz, its channels mixed, and handed over to be written down", async () => {
  const blob = new Blob(["abcd"]);
  const p = L.hearLocal("whisper-tiny", blob, "es");
  for (let i = 0; i < 5 && !worker().sent.some(({ m }) => m.kind === "hear"); i++) {
    await new Promise((r) => setImmediate(r));
  }
  assert.deepEqual(decoded.at(-1), { rate: 16000, bytes: 4 });
  const hear = worker().sent.find(({ m }) => m.kind === "hear");
  assert.deepEqual([...hear.m.samples], [0.5, 0.5, 0.5]);
  assert.deepEqual(hear.transfer, [hear.m.samples.buffer], "the samples are handed over, not copied");
  assert.equal(hear.m.lang, "es");
  assert.ok(worker().sent.some(({ m }) => m.kind === "warm" && m.id === "whisper-tiny"), "loaded first");
  answer({ seq: 999, text: "nobody asked" });
  answer({ seq: hear.m.seq, text: "hola", lang: "es" });
  assert.equal(await p, "hola");

  const q = L.hearLocal("whisper-tiny", blob, "");
  await new Promise((r) => setImmediate(r));
  await new Promise((r) => setImmediate(r));
  answer({ seq: worker().sent.at(-1).m.seq, error: "bad model" });
  await assert.rejects(q, /bad model/);
});

test("a worker that stops fails what it was asked, forgets what it loaded, and is made again", async () => {
  const p = L.hearLocal("whisper-tiny", new Blob(["x"]), "");
  await new Promise((r) => setImmediate(r));
  await new Promise((r) => setImmediate(r));
  const first = worker();
  first.onerror();
  await assert.rejects(p, /the local listener stopped/);
  assert.equal(L.listenStateOf("whisper-tiny"), undefined);
  L.warmListener("whisper-tiny");
  assert.notEqual(worker(), first, "a new worker");
});

test("a worker that never answers is stopped in time, the clock restarting while its model loads", async (t) => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  let failed = null;
  const p = L.hearLocal("whisper-tiny", new Blob(["x"]), "").catch((e) => {
    failed = e;
  });
  for (let i = 0; i < 5 && !worker().sent.some(({ m }) => m.kind === "hear"); i++) {
    await new Promise((r) => setImmediate(r));
  }
  const stuck = worker();
  // Three samples are a second: the base time and one second's more.
  const ms = L.HEAR_BASE_MS + L.HEAR_PER_SECOND_MS;
  t.mock.timers.tick(ms - 1);
  answer({ kind: "progress", id: "whisper-tiny", pct: 50 });
  answer({ kind: "progress", id: "whisper-small", pct: 10 });
  t.mock.timers.tick(ms - 1);
  await new Promise((r) => setImmediate(r));
  assert.equal(failed, null, "still loading: still waiting");
  assert.equal(stuck.terminated, undefined);
  t.mock.timers.tick(1);
  await p;
  assert.match(String(failed), /The local listener took too long and was stopped\./);
  assert.equal(stuck.terminated, true);
  assert.equal(L.listenStateOf("whisper-tiny"), undefined, "what it loaded went with it");
  L.warmListener("whisper-tiny");
  assert.notEqual(worker(), stuck, "a new worker");
});

test("whether the browser can listen itself, and which models are for the graphics card", () => {
  assert.equal(L.localListenSupported(), true);
  const ctx = globalThis.OfflineAudioContext;
  delete globalThis.OfflineAudioContext;
  assert.equal(L.localListenSupported(), false);
  globalThis.OfflineAudioContext = ctx;
  assert.equal(L.forGPU("whisper-small-gpu"), true);
  assert.equal(L.forGPU("whisper-small"), false);
});

test("recordings are handed over one at a time, each clock starting when its turn comes", async (t) => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  const hears = () => worker().sent.filter(({ m }) => m.kind === "hear");
  const settled = async () => {
    for (let i = 0; i < 6; i++) {
      await new Promise((r) => setImmediate(r));
    }
  };
  let failed = null;
  const first = L.hearLocal("whisper-tiny", new Blob(["x"]), "");
  const second = L.hearLocal("whisper-tiny", new Blob(["y"]), "").catch((e) => {
    failed = e;
  });
  await settled();
  const ms = L.HEAR_BASE_MS + L.HEAR_PER_SECOND_MS;
  const sentFirst = hears().at(-1);
  t.mock.timers.tick(ms - 1);
  answer({ seq: sentFirst.m.seq, text: "uno", lang: "" });
  assert.equal(await first, "uno");
  await settled();
  assert.equal(hears().at(-1).m.seq, sentFirst.m.seq + 1, "the second goes once the first is answered");
  t.mock.timers.tick(ms - 1);
  await settled();
  assert.equal(failed, null, "its wait behind the first was not counted");
  t.mock.timers.tick(1);
  await second;
  assert.match(String(failed), /took too long/);
});
