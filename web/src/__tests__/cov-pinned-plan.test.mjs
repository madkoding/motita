// The pinned plan: the hook (usePinnedPlan.ts) and the bar above the composer (PinnedPlan.tsx),
// run together in jsdom against a fake gateway.
import test from "node:test";
import assert from "node:assert/strict";
import { setupDom, load, text } from "./cov-render-helpers.mjs";

setupDom();
const { usePinnedPlan, planPath } = await load("usePinnedPlan.ts");
const { PinnedPlanBar, firstStep } = await load("PinnedPlan.tsx");
const { AppCtx } = await load("appStore.ts");
const es = (await import("../i18n.es.ts")).default;
const { h, render } = await import("preact");
const { act } = await import("preact/test-utils");

const settle = async (n = 6) => {
  for (let i = 0; i < n; i++) {
    await act(() => new Promise((r) => setImmediate(r)));
  }
};
const json = (body, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

// gateway keeps a plan per session; mode makes it refuse ("status") or throw ("throw").
function gateway(plan = "") {
  const g = { plan, mode: "", calls: [] };
  g.api = async (url, init = {}) => {
    g.calls.push([init.method || "GET", url]);
    if (g.mode === "throw") {
      throw new Error("network");
    }
    if (g.mode === "status") {
      return json({ error: "no" }, 501);
    }
    if (init.method === "PUT") {
      g.plan = JSON.parse(init.body).plan.trim();
    }
    if (g.mode === "noplan") {
      return json({});
    }
    return json({ plan: g.plan });
  };
  return g;
}

async function mount(g, props) {
  const statuses = [];
  const hook = { current: null };
  function Harness(p) {
    const pinnedPlan = usePinnedPlan(g.api, p.sessionId, p.running, (m, bad) =>
      statuses.push([m, !!bad]),
    );
    hook.current = pinnedPlan;
    return h(AppCtx.Provider, { value: { pinnedPlan } }, h(PinnedPlanBar, {}));
  }
  const root = document.createElement("div");
  document.body.appendChild(root);
  await act(() => render(h(Harness, props), root));
  await settle();
  return {
    root,
    hook,
    statuses,
    q: (id) => root.querySelector(`[data-testid="${id}"]`),
    rerender: async (p) => {
      await act(() => render(h(Harness, p), root));
      await settle();
    },
    click: async (id) => {
      await act(() => root.querySelector(`[data-testid="${id}"]`).click());
      await settle();
    },
    done: async () => {
      await act(() => render(null, root));
      root.remove();
    },
  };
}

test("planPath and firstStep", () => {
  assert.equal(planPath("a b"), "/v1/sessions/a%20b/plan");
  assert.equal(firstStep("\n  \n 1. go \n2. back"), "1. go");
  assert.equal(firstStep(""), "");
});

test("the plan shows folded, opens to be changed, kept, dropped", async () => {
  const g = gateway("1. pack\n2. travel\n3. rest");
  const m = await mount(g, { sessionId: "s1", running: false });
  assert.match(text(m.q("pinned-plan-open")), /Active plan.*1\. pack/);
  await m.click("pinned-plan-open");
  const area = m.q("pinned-plan-text");
  assert.equal(area.value, "1. pack\n2. travel\n3. rest");
  area.value = "1. pack light\n2. travel\n3. rest";
  await act(() =>
    area.dispatchEvent(new window.Event("input", { bubbles: true })),
  );
  await m.click("pinned-plan-cancel");
  assert.equal(g.plan, "1. pack\n2. travel\n3. rest", "cancel keeps the plan");
  await m.click("pinned-plan-open");
  m.q("pinned-plan-text").value = "1. pack light\n2. go";
  await act(() =>
    m
      .q("pinned-plan-text")
      .dispatchEvent(new window.Event("input", { bubbles: true })),
  );
  await m.click("pinned-plan-save");
  assert.equal(g.plan, "1. pack light\n2. go");
  assert.match(text(m.q("pinned-plan-open")), /1\. pack light/);
  await m.click("pinned-plan-open");
  await m.click("pinned-plan-drop");
  assert.equal(g.plan, "");
  assert.equal(m.q("pinned-plan"), null);
  await m.done();
});

test("the plan is read again when a turn ends and when the conversation changes", async () => {
  const g = gateway("");
  const m = await mount(g, { sessionId: "s1", running: true });
  assert.equal(g.calls.length, 0, "not read while a turn runs");
  g.plan = "1. a\n2. b\n3. c";
  await m.rerender({ sessionId: "s1", running: false });
  assert.ok(m.q("pinned-plan"));
  await m.rerender({ sessionId: "", running: false });
  assert.equal(m.q("pinned-plan"), null, "no conversation, no plan");
  g.mode = "noplan";
  await m.rerender({ sessionId: "s2", running: false });
  assert.equal(m.hook.current.plan, "");
  assert.equal(await m.hook.current.save("1. x"), true);
  assert.equal(m.hook.current.plan, "", "an answer without a plan reads as none");
  await m.done();
});

test("a gateway that keeps no plan, or cannot be reached, shows none and says when a change failed", async () => {
  for (const mode of ["status", "throw"]) {
    const g = gateway("1. a\n2. b\n3. c");
    const m = await mount(g, { sessionId: "s1", running: false });
    assert.ok(m.q("pinned-plan"));
    g.mode = mode;
    await m.click("pinned-plan-open");
    await m.click("pinned-plan-save");
    assert.ok(m.q("pinned-plan-text"), "a failed save keeps the editor open");
    assert.deepEqual(m.statuses.at(-1), ["could not change the plan", true]);
    await m.hook.current.reload();
    await settle();
    assert.equal(m.q("pinned-plan"), null);
    await m.done();
  }
});

test("the plan's words have their Spanish", () => {
  for (const k of [
    "Active plan",
    "The plan the agent keeps word for word. Open it to change or drop it.",
    "Drop the plan",
    "could not change the plan",
  ]) {
    assert.ok(es[k], "missing Spanish for: " + k);
  }
});
