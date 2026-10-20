// The quality check of the models tab: the question before it spends quota, the progress, the
// verdict against the last run, and the notice that the model changed since the last check.
// Run with: node --test web/src/__tests__/cov-settings-evals.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import {
  installDom,
  load,
  fakeApi,
  json,
  settle,
  click,
  text,
  byText,
} from "./cov-settings-helpers.mjs";

installDom();
const { EvalSettings } = await load("EvalSettings.tsx");
const { costText, signed, loadEvals, runEvals, stopEvals } =
  await load("evalsApi.ts");
const { LLMError } = await load("llmApi.ts");
const { setLang } = await import("../i18n.ts").catch(() => ({}));
const { h, render } = await import("preact");

const ids = [
  "create-file",
  "fix-typo",
  "count-lines",
  "rename-file",
  "sort-lines",
];
const summary = (passed, steps = 7, cost = 0.05, known = true) => ({
  passed,
  total: 5,
  steps,
  tokens: 1000,
  cost_usd: cost,
  cost_known: known,
});
const run = (id, passes, sum) => ({
  id,
  at: "2026-10-20T12:00:00Z",
  provider: "openai",
  model: "gpt-5-mini",
  results: ids.map((x, i) => ({
    id: x,
    pass: passes[i],
    steps: 1,
    tokens: 10,
    cost_usd: 0,
    cost_known: true,
  })),
  summary: sum,
});
const idle = { running: false, done: 0, total: 5 };
const verdict = (over = {}) => ({
  status: "same",
  delta_passed: 0,
  delta_steps: 0,
  delta_cost_usd: 0,
  regressed: [],
  fixed: [],
  steps_up: false,
  cost_up: false,
  ...over,
});
const VIEW = {
  supported: true,
  cases: ids.map((id) => ({ id })),
  provider: "openai",
  model: "gpt-5-mini",
  stale: "",
  progress: idle,
  last: null,
  verdict: null,
  history: [],
};

async function mount(vnode) {
  const root = document.createElement("div");
  document.body.appendChild(root);
  render(vnode, root);
  await settle();
  return {
    root,
    async rerender(v) {
      render(v, root);
      await settle();
    },
    async unmount() {
      render(null, root);
      root.remove();
      await settle();
    },
  };
}
const props = (api, extra = {}) => ({
  api,
  onNotice: (m, e) => (props.notices.push([m, e]), undefined),
  watch: "a",
  ...extra,
});
props.notices = [];
const btn = (root, label) => byText(root, "button", label);
const asked = (f) => f.calls.map((c) => c.method + " " + c.path);

test("evalsApi: costs and changes read as text; the calls carry the confirmation", async () => {
  assert.equal(costText(0), "$0.00");
  assert.equal(costText(0.004), "<$0.01");
  assert.equal(costText(1.256), "$1.26");
  assert.equal(signed(3), "+3");
  assert.equal(signed(0), "0");
  assert.equal(signed(-2), "-2");
  const f = fakeApi({
    "GET /v1/evals": json(200, VIEW),
    "POST /v1/evals/run": json(202, idle),
    "POST /v1/evals/stop": json(409, { error: "none", code: "idle" }),
  });
  assert.equal((await loadEvals(f.api)).model, "gpt-5-mini");
  await runEvals(f.api);
  assert.deepEqual(f.calls[1].body, { confirm: true });
  await assert.rejects(
    stopEvals(f.api),
    (e) => e instanceof LLMError && e.code === "idle",
  );
  const bare = fakeApi({ "GET /v1/evals": new Response("x", { status: 500 }) });
  await assert.rejects(loadEvals(bare.api), (e) => e.message === "HTTP 500");
});

test("it stays hidden when the gateway has no quality check", async () => {
  const f = fakeApi({ "GET /v1/evals": json(404, { error: "no" }) });
  const m = await mount(h(EvalSettings, props(f.api)));
  assert.equal(m.root.querySelector("[data-testid=llm-evals]"), null);
  await m.unmount();
});

test("an answer that is not a check is treated as no check", async () => {
  const f = fakeApi({ "GET /v1/evals": json(200, { providers: [] }) });
  const m = await mount(h(EvalSettings, props(f.api)));
  assert.equal(m.root.querySelector("[data-testid=llm-evals]"), null);
  await m.unmount();
});

test("never checked: it says so, asks before it runs, and starts only when confirmed", async () => {
  let view = { ...VIEW, stale: "never" };
  const f = fakeApi({
    "GET /v1/evals": () => json(200, view),
    "POST /v1/evals/run": () => {
      view = {
        ...view,
        progress: { running: true, done: 0, total: 5, next: "create-file" },
      };
      return json(202, view.progress);
    },
  });
  const m = await mount(h(EvalSettings, props(f.api)));
  assert.match(text(m.root), /Quality check/);
  assert.match(text(m.root), /Model checked: gpt-5-mini/);
  assert.equal(
    m.root.querySelector("[data-testid=eval-stale]").dataset.stale,
    "never",
  );
  assert.equal(m.root.querySelector("[data-testid=eval-result]"), null);

  click(m.root.querySelector("[data-testid=eval-run]"));
  await settle();
  assert.match(
    text(m.root),
    /This runs 5 small tasks on gpt-5-mini and uses part of your quota/,
  );
  assert.ok(
    !asked(f).includes("POST /v1/evals/run"),
    "nothing runs before the answer",
  );
  click(btn(m.root, "Cancel"));
  await settle();
  assert.equal(m.root.querySelector("[data-testid=eval-confirm]"), null);

  click(m.root.querySelector("[data-testid=eval-run]"));
  await settle();
  click(btn(m.root, "Run the check"));
  await settle();
  assert.ok(asked(f).includes("POST /v1/evals/run"));
  assert.deepEqual(props.notices.at(-1), ["Check started", undefined]);
  assert.match(
    text(m.root.querySelector("[data-testid=eval-running]")),
    /Running task 1 of 5/,
  );
  await m.unmount();
});

test("a running check is followed and can be stopped", async () => {
  const realSet = globalThis.setInterval;
  const timers = [];
  globalThis.setInterval = (fn, ms) => (timers.push([fn, ms]), timers.length);
  const realClear = globalThis.clearInterval;
  const cleared = [];
  globalThis.clearInterval = (id) => cleared.push(id);
  try {
    let view = { ...VIEW, progress: { running: true, done: 5, total: 5 } };
    const f = fakeApi({
      "GET /v1/evals": () => json(200, view),
      "POST /v1/evals/stop": () => {
        view = { ...view, progress: { ...idle, stopped: true } };
        return json(202, view.progress);
      },
    });
    const m = await mount(h(EvalSettings, props(f.api)));
    assert.match(text(m.root), /Running task 5 of 5/);
    assert.equal(timers.length, 1);
    assert.equal(timers[0][1], 2000);
    const before = f.calls.length;
    timers[0][0]();
    await settle();
    assert.equal(f.calls.length, before + 1, "the poll asks again");
    click(btn(m.root, "Stop"));
    await settle();
    assert.ok(asked(f).includes("POST /v1/evals/stop"));
    assert.equal(m.root.querySelector("[data-testid=eval-running]"), null);
    assert.deepEqual(
      cleared.filter(Boolean),
      [1],
      "the poll ends with the check",
    );
    await m.unmount();
  } finally {
    globalThis.setInterval = realSet;
    globalThis.clearInterval = realClear;
  }
});

test("a worse run names the tasks that stopped working; a cost that is unknown says so", async () => {
  const view = {
    ...VIEW,
    stale: "model",
    last: run("b", [true, false, true, true, true], summary(4, 9, 0, false)),
    verdict: verdict({
      status: "worse",
      delta_passed: -1,
      delta_steps: 2,
      regressed: ["fix-typo"],
    }),
    history: [
      {
        id: "b",
        at: "2026-10-20T12:00:00Z",
        provider: "openai",
        model: "gpt-5-mini",
        summary: summary(4),
      },
      {
        id: "a",
        at: "2026-10-19T12:00:00Z",
        provider: "openai",
        model: "gpt-5",
        summary: summary(5),
      },
    ],
  };
  const f = fakeApi({ "GET /v1/evals": json(200, view) });
  const m = await mount(h(EvalSettings, props(f.api)));
  const r = m.root.querySelector("[data-testid=eval-result]");
  assert.equal(r.dataset.status, "worse");
  assert.match(text(r), /Worse than last time/);
  assert.match(text(r), /gpt-5-mini · 4 of 5 tasks passed \(-1\)/);
  assert.match(text(r), /Steps: 9 \(\+2\)/);
  assert.match(text(r), /Cost: unknown for this model/);
  const lines = [...r.querySelectorAll(".eval-task")];
  assert.equal(lines.length, 5);
  assert.match(text(lines[1]), /Fix a typo.*Stopped working.*Failed/);
  assert.match(text(lines[0]), /Create a file.*Passed/);
  assert.doesNotMatch(text(lines[0]), /Stopped working/);
  assert.match(text(r), /2026-10-19 · gpt-5 · 5 of 5/);
  assert.match(
    text(m.root.querySelector("[data-testid=eval-stale]")),
    /model changed/,
  );
  await m.unmount();
});

test("the other verdicts, a changed instruction and an old set of tasks", async () => {
  const cases = [
    [
      "better",
      "Better than last time",
      "prompt",
      /instructions the agent works with changed/,
    ],
    ["same", "About the same as last time", "cases", /tasks were updated/],
    ["first", "First check done", "", null],
    ["strange", "About the same as last time", "unheard-of", null],
  ];
  for (const [status, headline, stale, note] of cases) {
    const view = {
      ...VIEW,
      stale,
      last: run("a", [true, true, true, true, true], summary(5, 5, 0.5)),
      verdict: verdict({ status }),
      history: [],
    };
    const f = fakeApi({ "GET /v1/evals": json(200, view) });
    const m = await mount(h(EvalSettings, props(f.api)));
    const r = m.root.querySelector("[data-testid=eval-result]");
    assert.match(text(r), new RegExp(headline), status);
    assert.match(text(r), /Cost: about \$0\.50/);
    if (status === "first") {
      assert.doesNotMatch(text(r), /\(\+?0\)/, "nothing to compare with yet");
    }
    const s = m.root.querySelector("[data-testid=eval-stale]");
    if (note) {
      assert.match(text(s), note);
    } else {
      assert.equal(s, null);
    }
    await m.unmount();
  }
});

test("a task the page does not know is shown under its id", async () => {
  const last = run("a", [true, true, true, true, true], summary(5));
  last.results[0].id = "new-task";
  const f = fakeApi({
    "GET /v1/evals": json(200, { ...VIEW, last, verdict: verdict() }),
  });
  const m = await mount(h(EvalSettings, props(f.api)));
  assert.match(text(m.root.querySelector(".eval-task")), /new-task/);
  await m.unmount();
});

test("a computer without a shell, and a check that could not finish", async () => {
  const f = fakeApi({
    "GET /v1/evals": json(200, {
      ...VIEW,
      supported: false,
      stale: "never",
      progress: { ...idle, error: "down" },
    }),
  });
  const m = await mount(h(EvalSettings, props(f.api)));
  assert.match(
    text(m.root.querySelector("[data-testid=eval-unsupported]")),
    /needs a command line/,
  );
  assert.equal(m.root.querySelector("[data-testid=eval-run]"), null);
  assert.equal(m.root.querySelector("[data-testid=eval-stale]"), null);
  assert.match(
    text(m.root.querySelector("[role=alert]")),
    /The last check could not finish: down/,
  );
  await m.unmount();
});

test("a refused start tells why and keeps the page", async () => {
  const f = fakeApi({
    "GET /v1/evals": json(200, VIEW),
    "POST /v1/evals/run": json(409, {
      error: "connect OpenAI first: it has no key",
      code: "no_key",
    }),
  });
  const m = await mount(h(EvalSettings, props(f.api)));
  click(m.root.querySelector("[data-testid=eval-run]"));
  await settle();
  click(btn(m.root, "Run the check"));
  await settle();
  assert.match(text(m.root.querySelector("[role=alert]")), /no key/);
  assert.ok(m.root.querySelector("[data-testid=eval-run]"));
  await m.unmount();
});

test("a new model reloads the state: the page notices the last check is old", async () => {
  let view = { ...VIEW, model: "a", stale: "" };
  const f = fakeApi({ "GET /v1/evals": () => json(200, view) });
  const m = await mount(h(EvalSettings, props(f.api)));
  assert.equal(m.root.querySelector("[data-testid=eval-stale]"), null);
  view = { ...view, model: "b", stale: "model" };
  await m.rerender(h(EvalSettings, props(f.api, { watch: "b" })));
  assert.match(text(m.root), /Model checked: b/);
  assert.ok(m.root.querySelector("[data-testid=eval-stale]"));
  await m.unmount();
});

test("in Spanish", async () => {
  await setLang("es");
  try {
    const f = fakeApi({
      "GET /v1/evals": json(200, {
        ...VIEW,
        stale: "model",
        last: run("a", [true, true, true, true, true], summary(5)),
        verdict: verdict({ status: "better" }),
      }),
    });
    const m = await mount(h(EvalSettings, props(f.api)));
    assert.match(text(m.root), /Revisión de calidad/);
    assert.match(text(m.root), /Mejor que la última vez/);
    assert.match(text(m.root), /Crear un archivo/);
    await m.unmount();
  } finally {
    await setLang("en");
  }
});
