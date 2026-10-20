// The Upkeep tab: the weekly check, the draft pull requests, their limit and the "Run now" buttons.
// Run with: node --test web/src/__tests__/cov-settings-autopilot.test.mjs
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
  type,
} from "./cov-settings-helpers.mjs";

installDom();
const { AutopilotSettings } = await load("AutopilotSettings.tsx");
const es = (await import("../i18n.es.ts")).default;
const { h, render } = await import("preact");

const VIEW = {
  weekly_audit: false,
  auto_pr: false,
  max_open_prs: 3,
  can_save: true,
  audit: null,
  fixes: null,
};

async function mount(fake) {
  const root = document.createElement("div");
  document.body.appendChild(root);
  render(h(AutopilotSettings, { api: fake.api }), root);
  await settle();
  return root;
}

// gateway keeps the saved settings and answers a started routine with a new last run.
function gateway(initial, refuse) {
  let state = { ...initial };
  return fakeApi({
    "GET /v1/autopilot/run": () => json(405, {}),
    "GET /v1/autopilot": () => json(200, state),
    "PUT /v1/autopilot": (_p, init) => {
      if (refuse) {
        return json(400, { error: refuse });
      }
      state = { ...state, ...JSON.parse(init.body) };
      return json(200, state);
    },
    "POST /v1/autopilot/run": (_p, init) => {
      if (refuse) {
        return json(409, { error: refuse });
      }
      const what = JSON.parse(init.body).what;
      state = {
        ...state,
        [what]: {
          last_run: "2026-10-18T09:30:00Z",
          next_run: "",
          last_outcome: "started",
        },
      };
      return json(202, state);
    },
  });
}

const radio = (root, name, value) =>
  [...root.querySelectorAll(`input[name=${name}]`)].find(
    (r) => r.value === value,
  );
async function pick(root, name, value) {
  const r = radio(root, name, value);
  r.checked = true;
  r.dispatchEvent(new window.Event("change", { bubbles: true }));
  await settle();
}
const q = (root, id) => root.querySelector(`[data-testid=${id}]`);

test("both switches start off, so the buttons and the limit are disabled", async () => {
  const root = await mount(gateway(VIEW));
  assert.ok(radio(root, "settings-autopilot-audit", "off").checked);
  assert.ok(radio(root, "settings-autopilot-prs", "off").checked);
  assert.equal(q(root, "autopilot-run-audit").disabled, true);
  assert.equal(q(root, "autopilot-run-fixes").disabled, true);
  assert.equal(q(root, "autopilot-limit").disabled, true);
  assert.equal(q(root, "autopilot-status-audit"), null);
  assert.doesNotMatch(text(root), /cannot be changed here/);
});

test("each switch saves alone and turns its button and the limit on", async () => {
  const gw = gateway(VIEW);
  const root = await mount(gw);
  await pick(root, "settings-autopilot-audit", "on");
  assert.equal(q(root, "autopilot-run-audit").disabled, false);
  assert.equal(q(root, "autopilot-limit").disabled, true);
  await pick(root, "settings-autopilot-prs", "on");
  assert.equal(q(root, "autopilot-limit").disabled, false);
  await pick(root, "settings-autopilot-prs", "off");
  const puts = gw.calls.filter((c) => c.method === "PUT").map((c) => c.body);
  assert.deepEqual(puts, [
    { weekly_audit: true },
    { auto_pr: true },
    { auto_pr: false },
  ]);
});

test("the limit is saved as a whole number from 1 to 10", async () => {
  const gw = gateway({ ...VIEW, auto_pr: true });
  const root = await mount(gw);
  for (const typed of ["5", "0", "99", "4.4", ""]) {
    type(q(root, "autopilot-limit"), typed);
    await settle();
  }
  const puts = gw.calls.filter((c) => c.method === "PUT").map((c) => c.body);
  assert.deepEqual(
    puts.map((p) => p.max_open_prs),
    [5, 1, 10, 4, 1],
  );
});

test("Run now starts that routine and shows when it ran and how it went", async () => {
  const gw = gateway({
    ...VIEW,
    weekly_audit: true,
    auto_pr: true,
    fixes: {
      last_run: "2026-10-10T08:00:00Z",
      next_run: "2026-10-17T08:00:00Z",
      last_outcome: "",
    },
  });
  const root = await mount(gw);
  const fixes = text(q(root, "autopilot-status-fixes"));
  assert.match(fixes, /Last round of fixes/);
  assert.doesNotMatch(fixes, / · $/);
  assert.match(fixes, /Next: /);
  click(q(root, "autopilot-run-audit"));
  await settle();
  assert.deepEqual(
    gw.calls.filter((c) => c.method === "POST").map((c) => c.body),
    [{ what: "audit" }],
  );
  assert.match(
    text(q(root, "autopilot-status-audit")),
    /Last check: .* · started/,
  );
  click(q(root, "autopilot-run-fixes"));
  await settle();
  assert.equal(gw.calls.filter((c) => c.method === "POST").length, 2);
});

test("a routine with only a next date, or an unreadable one, says only what it can", async () => {
  const root = await mount(
    gateway({
      ...VIEW,
      weekly_audit: true,
      audit: {
        last_run: "",
        next_run: "2026-10-25T08:00:00Z",
        last_outcome: "",
      },
      fixes: { last_run: "nonsense", next_run: "", last_outcome: "x" },
    }),
  );
  assert.match(text(q(root, "autopilot-status-audit")), /^Next: /);
  assert.equal(q(root, "autopilot-status-fixes"), null);
});

test("a refusal is shown and the settings stay as they were", async () => {
  const root = await mount(gateway({ ...VIEW, weekly_audit: true }, "no file"));
  await pick(root, "settings-autopilot-prs", "on");
  assert.match(text(q(root, "autopilot-error")), /no file/);
  click(q(root, "autopilot-run-audit"));
  await settle();
  assert.match(text(q(root, "autopilot-error")), /no file/);
  assert.ok(radio(root, "settings-autopilot-prs", "off").checked);
});

test("without a configuration file nothing can be changed, and the page says why", async () => {
  const root = await mount(
    gateway({ ...VIEW, weekly_audit: true, can_save: false }),
  );
  assert.equal(radio(root, "settings-autopilot-audit", "on").disabled, true);
  assert.match(text(root), /cannot be changed here/);
});

test("the section is hidden on a gateway without the API and reports a failed load", async () => {
  const missing = await mount(fakeApi({ "GET /v1/autopilot": json(404, {}) }));
  assert.equal(missing.innerHTML, "");
  const failing = await mount(
    fakeApi({ "GET /v1/autopilot": json(500, { error: "boom" }) }),
  );
  assert.match(text(failing), /boom/);
  assert.equal(failing.querySelector("input"), null);
});

test("an answer that arrives after the dialog closed is dropped", async () => {
  let release;
  const gate = new Promise((r) => (release = r));
  const fake = fakeApi({
    "GET /v1/autopilot": async () => {
      await gate;
      return json(200, VIEW);
    },
  });
  const root = document.createElement("div");
  document.body.appendChild(root);
  render(h(AutopilotSettings, { api: fake.api }), root);
  render(null, root);
  release();
  await settle();
  assert.equal(root.innerHTML, "");
});

test("an error with no readable body is reported by its status", async () => {
  const root = await mount(
    fakeApi({
      "GET /v1/autopilot": json(200, { ...VIEW, weekly_audit: true }),
      "PUT /v1/autopilot": new Response("<html>", { status: 502 }),
    }),
  );
  await pick(root, "settings-autopilot-prs", "on");
  assert.match(text(q(root, "autopilot-error")), /HTTP 502/);
});

test("the Spanish words read plainly", () => {
  for (const k of [
    "Upkeep",
    "Weekly check",
    "Draft pull requests",
    "Most open pull requests",
    "Last check: {date}",
    "Last round of fixes: {date}",
    "Next: {date}",
    "Run the weekly check now",
    "Look for small fixes now",
  ]) {
    assert.ok(es[k], k);
    assert.notEqual(es[k], k);
  }
  assert.doesNotMatch(Object.values(es).join(" "), /\bautopilot\b/i);
});
