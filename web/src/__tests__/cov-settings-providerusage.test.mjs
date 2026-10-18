// What each provider consumed, in the sidebar's bottom menu: the rows, the bar against the busiest
// one, the tooltip, the cap, the periodic read and the gateways that have no such API.
// Run with: node --test web/src/__tests__/cov-settings-providerusage.test.mjs
import test, { mock } from "node:test";
import assert from "node:assert/strict";
import {
  installDom,
  load,
  fakeApi,
  json,
  text,
  settle,
} from "./cov-settings-helpers.mjs";

installDom();
const { ProviderUsagePanel, USAGE_POLL_MS, USAGE_ROWS } =
  await load("ProviderUsage.tsx");
const { getLLMUsage, usageTotal } = await load("llmApi.ts");
const es = (await import("../i18n.es.ts")).default;
const { h, render } = await import("preact");

const row = (id, over = {}) => ({
  id,
  name: id.toUpperCase(),
  default: false,
  input: 0,
  output: 0,
  cache_read: 0,
  cache_write: 0,
  calls: 0,
  ...over,
});
const ROUTE = "GET /v1/llm/usage";

// The panel reads on a timer for as long as it is mounted: the last test empties every root, or
// the process would wait for the next read.
const mounted = [];

async function mount(api, running = false) {
  const root = document.createElement("div");
  document.body.appendChild(root);
  mounted.push(root);
  render(h(ProviderUsagePanel, { api, running }), root);
  await settle();
  return root;
}
const q = (root, id) => root.querySelector(`[data-testid=${id}]`);

test("providers are listed busiest first, each with its tokens and a bar against the top one", async () => {
  const gw = fakeApi({
    [ROUTE]: () =>
      json(200, {
        providers: [
          row("openai", { input: 1000, output: 500, calls: 3 }),
          row("anthropic", {
            input: 10000,
            output: 2000,
            cache_read: 3000,
            cache_write: 1000,
            calls: 1,
          }),
          row("gemini"),
        ],
      }),
  });
  const root = await mount(gw.api);
  const names = [...root.querySelectorAll("li")].map((li) =>
    li.getAttribute("data-testid"),
  );
  assert.deepEqual(names, [
    "provider-usage-anthropic",
    "provider-usage-openai",
    "provider-usage-gemini",
  ]);
  assert.match(text(q(root, "provider-usage-anthropic")), /ANTHROPIC91%/);
  assert.match(text(q(root, "provider-usage-gemini")), /GEMINI0%/);
  const width = (id) =>
    q(root, id).querySelector(".provider-usage-fill").style.width;
  assert.equal(width("provider-usage-anthropic"), "91%");
  assert.equal(width("provider-usage-openai"), "9%");
  assert.equal(width("provider-usage-gemini"), "0%");
  assert.equal(
    q(root, "provider-usage-anthropic").title,
    "ANTHROPIC: 16k tokens · in 10k · out 2k · cache read 3k · cache write 1k · 1 call",
  );
  assert.match(q(root, "provider-usage-openai").title, /3 calls$/);
  assert.equal(q(root, "provider-usage-more"), null);
});

test("the estimated cost and the rate window are shown when the provider has them", async () => {
  const gw = fakeApi({
    [ROUTE]: () =>
      json(200, {
        providers: [
          row("anthropic", {
            input: 1000,
            cost_usd: 7.2,
            limits: { tokens: { limit: 40000, remaining: 39000 } },
          }),
          row("openai", { input: 10, cost_usd: 0.5, cost_partial: true }),
          row("gemini", {
            input: 5,
            limits: { requests: { limit: 5, remaining: 1 } },
          }),
        ],
      }),
  });
  const root = await mount(gw.api);
  assert.equal(
    text(q(root, "provider-usage-anthropic")),
    "ANTHROPIC99% · ≈$7.20",
  );
  assert.equal(text(q(root, "provider-usage-openai")), "OPENAI1% · ≈$0.50+");
  assert.equal(text(q(root, "provider-usage-gemini")), "GEMINI0%");
  assert.match(
    q(root, "provider-usage-anthropic").title,
    /39k of 40k tokens left in the rate window$/,
  );
  assert.doesNotMatch(q(root, "provider-usage-gemini").title, /rate window/);
});

test("only the busiest providers are named and the rest are counted", async () => {
  const many = Array.from({ length: USAGE_ROWS + 2 }, (_, i) =>
    row("p" + i, { input: 100 * (i + 1) }),
  );
  const gw = fakeApi({ [ROUTE]: () => json(200, { providers: many }) });
  const root = await mount(gw.api);
  assert.equal(root.querySelectorAll("li").length, USAGE_ROWS);
  assert.equal(text(q(root, "provider-usage-more")), "+2 more");
});

test("it says nothing without a provider, from an old gateway or when the read fails", async () => {
  for (const respond of [
    () => json(200, { providers: [] }),
    () => json(200, {}),
    () => json(404, { error: "no such route" }),
    () => new Error("offline"),
  ]) {
    const root = await mount(fakeApi({ [ROUTE]: respond }).api);
    assert.equal(root.textContent, "");
  }
});

test("the figures are read again when a run starts or ends and on a timer", async () => {
  let calls = 0;
  const gw = fakeApi({
    [ROUTE]: () => {
      calls += 1;
      return json(200, { providers: [row("openai", { input: calls })] });
    },
  });
  const timers = [];
  mock.method(globalThis, "setInterval", (fn, ms) => {
    timers.push([fn, ms]);
    return timers.length;
  });
  const cleared = [];
  mock.method(globalThis, "clearInterval", (id) => cleared.push(id));
  const root = document.createElement("div");
  document.body.appendChild(root);
  render(h(ProviderUsagePanel, { api: gw.api, running: false }), root);
  await settle();
  assert.match(q(root, "provider-usage-openai").title, /: 1 tokens/);
  assert.deepEqual(
    timers.map((t) => t[1]),
    [USAGE_POLL_MS],
  );
  timers[0][0]();
  await settle();
  assert.match(q(root, "provider-usage-openai").title, /: 2 tokens/);
  render(h(ProviderUsagePanel, { api: gw.api, running: true }), root);
  await settle();
  assert.match(q(root, "provider-usage-openai").title, /: 3 tokens/);
  assert.deepEqual(cleared, [1]);
  render(null, root);
  await settle();
  assert.deepEqual(cleared, [1, 2]);
  mock.restoreAll();
});

test("leaving the panel ignores a late answer", async () => {
  let release;
  const gw = fakeApi({
    [ROUTE]: () =>
      new Promise((r) => {
        release = () => r(json(200, { providers: [row("openai")] }));
      }),
  });
  const root = document.createElement("div");
  document.body.appendChild(root);
  render(h(ProviderUsagePanel, { api: gw.api, running: false }), root);
  await settle();
  render(null, root);
  release();
  await settle();
  assert.equal(root.textContent, "");
});

test("the api helpers add the tokens and read the list", async () => {
  assert.equal(
    usageTotal(
      row("x", { input: 1, output: 2, cache_read: 3, cache_write: 4 }),
    ),
    10,
  );
  assert.equal(
    await getLLMUsage(fakeApi({ [ROUTE]: () => json(200, {}) }).api).then(
      (v) => v?.length,
    ),
    0,
  );
});

test("every new string has its Spanish", async () => {
  const src = (await import("node:fs")).readFileSync(
    new URL("../ProviderUsage.tsx", import.meta.url),
    "utf8",
  );
  const found = [
    ...src.matchAll(
      /\b(?:t|tf|plural)\(\s*(?:[^"]*?,\s*)?"((?:[^"\\]|\\.)*)"/g,
    ),
  ];
  assert.ok(found.length >= 3);
  for (const m of found) {
    assert.ok(es[m[1]], `missing Spanish for: ${m[1]}`);
  }
  assert.ok(es["{n} calls"]);
  assert.ok(es["1 call"]);
});

test("nothing is left reading", () => {
  mounted.forEach((root) => render(null, root));
  assert.ok(mounted.every((root) => root.textContent === ""));
});
