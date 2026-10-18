// How much of each provider's plan is used, in the sidebar's bottom menu: the order, each quota as a
// bar or a ring, the quotas that state only where the usage stands, the providers that report none,
// the closed summary, the tooltip, the cap, the periodic read and the gateways that have no such API.
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
const { ProviderUsagePanel, USAGE_POLL_MS, USAGE_ROWS, formatReset } =
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

// The panel starts closed; most tests look at what it shows open, so they open it first.
async function mount(api, running = false, open = true) {
  if (open) {
    localStorage.setItem("motita.ui.usageOpen", "open");
  } else {
    localStorage.removeItem("motita.ui.usageOpen");
  }
  const root = document.createElement("div");
  document.body.appendChild(root);
  mounted.push(root);
  render(h(ProviderUsagePanel, { api, running }), root);
  await settle();
  return root;
}
const q = (root, id) => root.querySelector(`[data-testid=${id}]`);

const H = 3600_000;
const at = (ms) => new Date(Date.now() + ms).toISOString();
// tip is a provider's tooltip: on its row when closed, on its name when open.
const tip = (root, id) =>
  q(root, id).title || q(root, id).querySelector("[title]").title;
const quota = (root, id, name) =>
  q(root, id).querySelector(`[data-testid=provider-quota-${name}]`);

test("providers with a quota come first, the most used first, then the rest by tokens", async () => {
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
          row("ollama", {
            limits: { quotas: [{ name: "month", used_percent: 22 }] },
          }),
          row("codex", {
            limits: { quotas: [{ name: "week", used_percent: 60 }] },
          }),
          row("claude", {
            limits: {
              quotas: [{ name: "week", used_percent: 0, status: "warning" }],
            },
          }),
        ],
      }),
  });
  const root = await mount(gw.api);
  const names = [...root.querySelectorAll(".provider-block")].map((li) =>
    li.getAttribute("data-testid"),
  );
  assert.deepEqual(names, [
    "provider-usage-claude",
    "provider-usage-codex",
    "provider-usage-ollama",
    "provider-usage-anthropic",
  ]);
  assert.equal(
    tip(root, "provider-usage-anthropic"),
    "ANTHROPIC: 16k tokens · in 10k · out 2k · cache read 3k · cache write 1k · 1 call",
  );
  assert.equal(text(q(root, "provider-usage-more")), "+1 more");
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
            calls: 3,
            limits: { requests: { limit: 5, remaining: 1 } },
          }),
        ],
      }),
  });
  const root = await mount(gw.api);
  assert.match(
    tip(root, "provider-usage-anthropic"),
    /39k of 40k tokens left in the rate window$/,
  );
  assert.doesNotMatch(tip(root, "provider-usage-gemini"), /rate window/);
  assert.match(tip(root, "provider-usage-gemini"), /3 calls$/);
});

test("each quota is a line with its window, a bar, the share and when it starts over", async () => {
  const gw = fakeApi({
    [ROUTE]: () =>
      json(200, {
        providers: [
          row("codex", {
            limits: {
              quotas: [
                {
                  name: "session",
                  used_percent: 93.4,
                  window_minutes: 300,
                  resets_at: at(H + 5 * 60_000 - 1000),
                },
                {
                  name: "week",
                  used_percent: 41,
                  resets_at: at(3 * 24 * H - 1000),
                },
                {
                  name: "month",
                  used_percent: -4,
                  resets_at: at(13 * 24 * H + 9 * H - 1000),
                },
              ],
            },
          }),
          row("claude", {
            limits: {
              quotas: [
                { name: "week", used_percent: 130, resets_at: at(-1000) },
                {
                  name: "session",
                  used_percent: 5,
                  window_minutes: 7,
                  resets_at: at(20_000),
                },
              ],
            },
          }),
        ],
      }),
  });
  const root = await mount(gw.api);
  const session = quota(root, "provider-usage-codex", "session");
  assert.equal(text(session), "5 h93%resets in 1 h 5 min");
  assert.ok(session.querySelector(".provider-usage-fill.warn"));
  assert.equal(
    session.querySelector(".provider-usage-fill").style.width,
    "93%",
  );
  const week = quota(root, "provider-usage-codex", "week");
  assert.equal(text(week), "Week41%resets in 3 d");
  assert.equal(week.querySelector(".warn"), null);
  assert.equal(
    text(quota(root, "provider-usage-codex", "month")),
    "Month0%resets in 13 d 9 h",
  );
  // A window that started over says nothing; one of an odd length is just "Session".
  const claude = q(root, "provider-usage-claude");
  assert.equal(claude.querySelectorAll(".provider-quota").length, 1);
  assert.equal(
    text(quota(root, "provider-usage-claude", "session")),
    "Session5%resets in 1 min",
  );
});

test("a quota that states only where the usage stands says so, with no bar", async () => {
  const gw = fakeApi({
    [ROUTE]: () =>
      json(200, {
        providers: [
          row("claude", {
            limits: {
              quotas: [
                {
                  name: "session",
                  used_percent: 0,
                  status: "ok",
                  window_minutes: 300,
                  resets_at: at(2 * H + 10 * 60_000 - 1000),
                },
                { name: "week", used_percent: 0, status: "warning" },
              ],
            },
          }),
        ],
      }),
  });
  const root = await mount(gw.api);
  const session = quota(root, "provider-usage-claude", "session");
  assert.equal(text(session), "5 hBelow the warning levelresets in 2 h 10 min");
  assert.equal(session.querySelector(".provider-usage-bar"), null);
  const week = quota(root, "provider-usage-claude", "week");
  assert.equal(text(week), "WeekNear the limit");
  assert.ok(week.querySelector(".provider-quota-status.warning"));
});

test("a provider that reports no quota says so, with the tokens it moved", async () => {
  const gw = fakeApi({
    [ROUTE]: () =>
      json(200, {
        providers: [row("qwen", { input: 15000 }), row("gemini")],
      }),
  });
  const root = await mount(gw.api);
  const note = (id) =>
    text(q(root, id).querySelector("[data-testid=provider-usage-noquota]"));
  assert.equal(note("provider-usage-qwen"), "No quota reported · 15k tokens");
  assert.equal(note("provider-usage-gemini"), "No quota reported");
  assert.equal(root.querySelector(".provider-quotas"), null);
});

test("the gauge style draws a ring per quota and follows the setting", async () => {
  const gw = fakeApi({
    [ROUTE]: () =>
      json(200, {
        providers: [
          row("a", {
            limits: {
              quotas: [
                {
                  name: "week",
                  used_percent: 75,
                  resets_at: at(4 * 24 * H + 3 * H - 1000),
                },
                { name: "month", used_percent: 0 },
              ],
            },
          }),
          row("b", {
            limits: {
              quotas: [
                {
                  name: "session",
                  used_percent: 0,
                  status: "ok",
                  window_minutes: 300,
                  resets_at: at(H - 1000),
                },
                { name: "week", used_percent: 0, status: "warning" },
              ],
            },
          }),
        ],
      }),
  });
  localStorage.setItem("motita.ui.usageStyle", "gauge");
  const root = await mount(gw.api);
  const week = quota(root, "provider-usage-a", "week");
  assert.equal(text(week), "75%Week4 d 3 h");
  assert.equal(week.title, "resets in 4 d 3 h");
  assert.match(
    week.querySelector(".provider-usage-arc").getAttribute("stroke-dasharray"),
    /^70\.68\d* 94\.24\d*$/,
  );
  // An empty quota draws no arc, not a dot.
  const month = quota(root, "provider-usage-a", "month");
  assert.equal(month.querySelector(".provider-usage-arc"), null);
  assert.equal(month.title, "");
  const ok = quota(root, "provider-usage-b", "session");
  assert.equal(text(ok), "OK5 h1 h 0 min");
  assert.equal(ok.title, "Below the warning level · resets in 1 h 0 min");
  const warn = quota(root, "provider-usage-b", "week");
  assert.equal(text(warn), "!Week");
  assert.equal(warn.title, "Near the limit");
  assert.equal(root.querySelector(".provider-usage-fill"), null);
  localStorage.setItem("motita.ui.usageStyle", "bars");
  window.dispatchEvent(new Event("motita-usage-style"));
  await settle();
  assert.equal(root.querySelectorAll(".provider-usage-fill").length, 2);
  assert.equal(root.querySelector(".provider-usage-arc"), null);
  localStorage.removeItem("motita.ui.usageStyle");
});

test("it starts closed with each provider and its most used quota, and the title opens it", async () => {
  const gw = fakeApi({
    [ROUTE]: () =>
      json(200, {
        providers: [
          row("a", {
            limits: {
              quotas: [
                { name: "week", used_percent: 40 },
                { name: "session", used_percent: 93, window_minutes: 300 },
              ],
            },
          }),
          row("m", {
            limits: { quotas: [{ name: "month", used_percent: 22 }] },
          }),
          row("c", {
            limits: {
              quotas: [
                { name: "session", used_percent: 0, status: "ok" },
                { name: "week", used_percent: 0, status: "ok" },
              ],
            },
          }),
          row("w", {
            limits: {
              quotas: [
                { name: "session", used_percent: 0, status: "ok" },
                { name: "week", used_percent: 0, status: "warning" },
              ],
            },
          }),
        ],
      }),
  });
  const root = await mount(gw.api, false, false);
  assert.equal(text(q(root, "provider-usage-a")), "A5 h93%");
  assert.ok(q(root, "provider-usage-a").querySelector(".warn"));
  assert.equal(text(q(root, "provider-usage-m")), "MMonth22%");
  assert.equal(text(q(root, "provider-usage-c")), "CSessionOK");
  assert.equal(text(q(root, "provider-usage-w")), "WWeekNear the limit");
  assert.equal(root.querySelector(".provider-usage-bar"), null);
  assert.match(q(root, "provider-usage-a").title, /^A: /);
  const toggle = q(root, "provider-usage-toggle");
  assert.equal(toggle.getAttribute("aria-expanded"), "false");
  toggle.click();
  await settle();
  assert.equal(toggle.getAttribute("aria-expanded"), "true");
  assert.ok(root.querySelector(".provider-usage-fill"));
  assert.equal(localStorage.getItem("motita.ui.usageOpen"), "open");
  toggle.click();
  await settle();
  assert.equal(root.querySelector(".provider-usage-fill"), null);
  assert.equal(localStorage.getItem("motita.ui.usageOpen"), "closed");
});

test("closed, a provider without a quota shows a dash", async () => {
  const gw = fakeApi({
    [ROUTE]: () => json(200, { providers: [row("qwen", { input: 9 })] }),
  });
  const root = await mount(gw.api, false, false);
  const value = q(root, "provider-usage-qwen").querySelector(
    ".provider-summary-value",
  );
  assert.equal(text(value), "—");
  assert.equal(value.title, "No quota reported");
});

test("the time to a reset reads in days, hours and minutes", () => {
  assert.equal(formatReset(0), "1 min");
  assert.equal(formatReset(59 * 60_000), "59 min");
  assert.equal(formatReset(2 * H), "2 h 0 min");
  assert.equal(formatReset(24 * H), "1 d");
  assert.equal(formatReset(321 * H + 27 * 60_000), "13 d 9 h");
});

test("the open or closed state survives a storage that refuses", async () => {
  const S = await load("settings.ts");
  const proto = window.Storage.prototype;
  const { getItem, setItem } = proto;
  proto.getItem = proto.setItem = () => {
    throw new Error("blocked");
  };
  const closed = S.loadUsageOpen();
  S.saveUsageOpen(true);
  Object.assign(proto, { getItem, setItem });
  assert.equal(closed, false);
});

test("only the busiest providers are named and the rest are counted", async () => {
  const many = Array.from({ length: USAGE_ROWS + 2 }, (_, i) =>
    row("p" + i, { input: 100 * (i + 1) }),
  );
  const gw = fakeApi({ [ROUTE]: () => json(200, { providers: many }) });
  const root = await mount(gw.api);
  assert.equal(root.querySelectorAll(".provider-block").length, USAGE_ROWS);
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
  assert.match(tip(root, "provider-usage-openai"), /: 1 tokens/);
  assert.deepEqual(
    timers.map((t) => t[1]),
    [USAGE_POLL_MS],
  );
  timers[0][0]();
  await settle();
  assert.match(tip(root, "provider-usage-openai"), /: 2 tokens/);
  render(h(ProviderUsagePanel, { api: gw.api, running: true }), root);
  await settle();
  assert.match(tip(root, "provider-usage-openai"), /: 3 tokens/);
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
