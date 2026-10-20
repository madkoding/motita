// The friction journal's page side: its client, and the Journal tab that reads the week back and
// can forget it.
// Run with: node --test web/src/__tests__/friction.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import {
  installDom,
  load,
  fakeApi,
  json,
  text,
  settle,
  click,
  byText,
} from "./cov-settings-helpers.mjs";

installDom();
const { FrictionSettings } = await load("FrictionSettings.tsx");
const fr = await import("../frictionApi.ts");
const es = (await import("../i18n.es.ts")).default;
const { h, render } = await import("preact");

const WEEK = {
  days: 7,
  total: 3,
  counts: { error: 2, retry: 1, denied: 0, flagged: 0 },
  repeated: [{ kind: "error", text: "boom", count: 2 }],
  flagged: [],
  text: "In the last 7 days, this got in the way (3 in all):\n- Runs that ended with a failure: 2",
};

async function mount(api) {
  const root = document.createElement("div");
  document.body.appendChild(root);
  render(h(FrictionSettings, { api }), root);
  await settle();
  return {
    root,
    async unmount() {
      render(null, root);
      root.remove();
      await settle();
    },
  };
}

test("the client reads the week, and a gateway without a journal is null", async () => {
  const ok = fakeApi({ "GET /v1/friction": json(200, WEEK) });
  assert.deepEqual(await fr.getFriction(ok.api, "es"), WEEK);
  assert.equal(ok.calls[0].path, "/v1/friction?lang=es");
  const old = fakeApi({ "GET /v1/friction": json(404, {}) });
  assert.equal(await fr.getFriction(old.api, "en"), null);
  const bad = fakeApi({ "GET /v1/friction": json(500, { error: "disk" }) });
  await assert.rejects(fr.getFriction(bad.api, "en"), /disk/);
  const bare = fakeApi({
    "GET /v1/friction": new Response("x", { status: 502 }),
  });
  await assert.rejects(fr.getFriction(bare.api, "en"), /HTTP 502/);
});

test("the client clears the journal and flags an answer", async () => {
  const f = fakeApi({
    "DELETE /v1/friction": new Response(null, { status: 204 }),
    "POST /v1/sessions/a%2Fb/friction": new Response(null, { status: 204 }),
  });
  await fr.clearFriction(f.api);
  await fr.flagAnswer(f.api, "a/b", 3, "wrong");
  assert.deepEqual(f.calls[1].body, { turn: 3, note: "wrong" });
  const bad = fakeApi({
    "DELETE /v1/friction": json(500, { error: "stuck" }),
    "POST /v1/sessions/s/friction": json(404, { error: "no turn" }),
  });
  await assert.rejects(fr.clearFriction(bad.api), /stuck/);
  await assert.rejects(fr.flagAnswer(bad.api, "s", 1, ""), /no turn/);
});

test("the tab says what it keeps, shows the week, and clears it after a confirmation", async () => {
  let week = WEEK;
  const f = fakeApi({
    "GET /v1/friction": () => json(200, week),
    "DELETE /v1/friction": () => {
      week = {
        ...WEEK,
        total: 0,
        text: "Nothing got in the way in the last 7 days.",
      };
      return new Response(null, { status: 204 });
    },
  });
  const m = await mount(f.api);
  assert.match(text(m.root), /on this computer only/);
  assert.match(
    text(m.root.querySelector("[data-testid=friction-summary]")),
    /Runs that ended with a failure: 2/g,
  );

  // Cancel leaves it alone; confirming clears and reads the quiet week.
  click(m.root.querySelector("[data-testid=friction-clear]"));
  await settle();
  const confirm = m.root.querySelector("[data-testid=friction-confirm]");
  assert.match(text(confirm), /Clear the whole journal\?/);
  click(byText(confirm, "button", "Cancel"));
  await settle();
  assert.equal(m.root.querySelector("[data-testid=friction-confirm]"), null);
  assert.equal(f.calls.filter((c) => c.method === "DELETE").length, 0);

  click(m.root.querySelector("[data-testid=friction-clear]"));
  await settle();
  click(
    byText(
      m.root.querySelector("[data-testid=friction-confirm]"),
      "button",
      "Clear the journal",
    ),
  );
  await settle();
  assert.equal(f.calls.filter((c) => c.method === "DELETE").length, 1);
  assert.match(
    text(m.root.querySelector("[data-testid=friction-summary]")),
    /Nothing got in the way/,
  );
  assert.equal(
    m.root.querySelector("[data-testid=friction-clear]"),
    null,
    "nothing left to clear",
  );
  await m.unmount();
});

test("a journal that cannot be cleared says so", async () => {
  const f = fakeApi({
    "GET /v1/friction": json(200, WEEK),
    "DELETE /v1/friction": json(500, { error: "read-only disk" }),
  });
  const m = await mount(f.api);
  click(m.root.querySelector("[data-testid=friction-clear]"));
  await settle();
  click(
    byText(
      m.root.querySelector("[data-testid=friction-confirm]"),
      "button",
      "Clear the journal",
    ),
  );
  await settle();
  assert.match(
    text(m.root.querySelector("[data-testid=friction-error]")),
    /read-only disk/,
  );
  assert.equal(m.root.querySelector("[data-testid=friction-confirm]"), null);
  await m.unmount();
});

test("loading, an older gateway and a read that fails each have their line", async () => {
  let release;
  const slow = fakeApi({
    "GET /v1/friction": () =>
      new Promise((r) => (release = () => r(json(200, WEEK)))),
  });
  const m = await mount(slow.api);
  assert.equal(text(m.root), "Loading…");
  release();
  await settle();
  assert.match(text(m.root), /Runs that ended with a failure: 2/g);
  await m.unmount();

  const old = await mount(fakeApi({ "GET /v1/friction": json(404, {}) }).api);
  assert.match(
    text(old.root.querySelector("[data-testid=friction-missing]")),
    /older than this page/,
  );
  await old.unmount();

  const broken = await mount(
    fakeApi({ "GET /v1/friction": new Error("offline") }).api,
  );
  assert.match(
    text(broken.root.querySelector("[data-testid=friction-missing]")),
    /offline/,
  );
  await broken.unmount();

  // Leaving before the read ends changes nothing.
  const quick = fakeApi({ "GET /v1/friction": () => json(200, WEEK) });
  const gone = await mount(quick.api);
  await gone.unmount();
  const fails = fakeApi({ "GET /v1/friction": new Error("late") });
  const left = await mount(fails.api);
  await left.unmount();
});

test("every new sentence of the page has its Spanish", () => {
  for (const k of [
    "Journal",
    "Loading…",
    "This gateway has no journal: it is older than this page.",
    "Clear the journal",
    "Clear the whole journal?",
    "Everything written so far is deleted, not just this week.",
    "This was not right",
    "What went wrong? (optional)",
    "Send",
    "Noted, thanks",
    "It stays in the journal on this computer; nothing is sent anywhere.",
    "could not note it",
  ]) {
    assert.ok(es[k], `no Spanish for ${k}`);
  }
});
