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

test("the rate window is in the tooltip when the provider reported one", async () => {
  const gw = fakeApi({
    [ROUTE]: () =>
      json(200, {
        providers: [
          row("anthropic", {
            input: 1000,
            limits: { tokens: { limit: 40000, remaining: 39000 } },
          }),
          row("gemini", {
            input: 5,
            limits: { requests: { limit: 5, remaining: 1 } },
          }),
        ],
      }),
  });
  const root = await mount(gw.api);
  assert.equal(text(q(root, "provider-usage-anthropic")), "ANTHROPIC100%");
  assert.match(
    q(root, "provider-usage-anthropic").title,
    /39k of 40k tokens left in the rate window$/,
  );
  assert.doesNotMatch(q(root, "provider-usage-gemini").title, /rate window/);
});

test("the gauge style draws a ring per provider and follows the setting", async () => {
  const gw = fakeApi({
    [ROUTE]: () =>
      json(200, {
        providers: [row("a", { input: 300 }), row("b", { input: 100 })],
      }),
  });
  localStorage.setItem("motita.ui.usageStyle", "gauge");
  const root = await mount(gw.api);
  assert.equal(text(q(root, "provider-usage-a")), "75%A");
  assert.equal(root.querySelectorAll(".provider-usage-arc").length, 2);
  assert.equal(root.querySelector(".provider-usage-fill"), null);
  const dash = q(root, "provider-usage-a")
    .querySelector(".provider-usage-arc")
    .getAttribute("stroke-dasharray");
  assert.match(dash, /^70\.68\d* 94\.24\d*$/);
  localStorage.setItem("motita.ui.usageStyle", "bars");
  window.dispatchEvent(new Event("motita-usage-style"));
  await settle();
  assert.equal(root.querySelectorAll(".provider-usage-fill").length, 2);
  assert.equal(root.querySelector(".provider-usage-arc"), null);
  localStorage.removeItem("motita.ui.usageStyle");
});

test("the plan's session and weekly quotas are shown with their reset, and expired ones are not", async () => {
  const soon = new Date(Date.now() + 2 * 3600_000 + 60_000).toISOString();
  const gone = new Date(Date.now() - 1000).toISOString();
  const gw = fakeApi({
    [ROUTE]: () =>
      json(200, {
        providers: [
          row("codex", {
            input: 100,
            limits: {
              quotas: [
                {
                  name: "session",
                  used_percent: 37.4,
                  window_minutes: 300,
                  resets_at: soon,
                },
                { name: "week", used_percent: 95.2 },
              ],
            },
          }),
          row("claude", {
            input: 50,
            limits: {
              quotas: [
                { name: "session", used_percent: 130, resets_at: gone },
                { name: "session", used_percent: 5, window_minutes: 7 },
              ],
            },
          }),
          row("gemini", { input: 1 }),
        ],
      }),
  });
  const root = await mount(gw.api);
  const session = q(root, "provider-quota-codex-session");
  assert.match(text(session), /CODEX · 5 h37%resets in 2h 0\dm/);
  assert.match(session.title, /^resets in 2h/);
  const week = q(root, "provider-quota-codex-week");
  assert.match(text(week), /CODEX · Week95%$/);
  assert.equal(week.title, "");
  assert.ok(week.querySelector(".provider-usage-fill.warn"));
  assert.equal(session.querySelector(".warn"), null);
  // A window that started over says nothing; one of an odd length is just "Session".
  const claude = q(root, "provider-quota-claude-session");
  assert.match(text(claude), /CLAUDE · Session5%/);
  assert.equal(
    root.querySelectorAll("[data-testid^=provider-quota-]").length,
    3,
  );
  assert.equal(
    text(q(root, "provider-usage-noquota")),
    "No quota reported: GEMINI",
  );
});

test("no quota list and no note when every provider reports one or none does", async () => {
  const none = await mount(
    fakeApi({
      [ROUTE]: () => json(200, { providers: [row("a", { input: 1 })] }),
    }).api,
  );
  assert.equal(q(none, "provider-quotas"), null);
  assert.equal(text(q(none, "provider-usage-noquota")), "No quota reported: A");
  const all = await mount(
    fakeApi({
      [ROUTE]: () =>
        json(200, {
          providers: [
            row("a", {
              input: 1,
              limits: {
                quotas: [
                  { name: "week", used_percent: -4 },
                  { name: "session", used_percent: 3 },
                ],
              },
            }),
          ],
        }),
    }).api,
  );
  assert.equal(q(all, "provider-usage-noquota"), null);
  assert.match(text(q(all, "provider-quota-a-week")), /A · Week0%/);
  assert.match(text(q(all, "provider-quota-a-session")), /A · Session3%/);
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

test("the usage style falls back to bars and survives a storage that refuses", async () => {
  const S = await load("settings.ts");
  localStorage.setItem(S.USAGE_STYLE_KEY, "nonsense");
  assert.equal(S.loadUsageStyle(), "bars");
  const proto = window.Storage.prototype;
  const { getItem, setItem } = proto;
  proto.getItem = proto.setItem = () => {
    throw new Error("blocked");
  };
  const style = S.loadUsageStyle();
  S.saveUsageStyle("gauge");
  Object.assign(proto, { getItem, setItem });
  assert.equal(style, "bars");
  localStorage.removeItem(S.USAGE_STYLE_KEY);
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
