// The scheduled tasks panel through the real useSchedules hook: the list with each task's
// countdown (mocked clock), pause, run and delete, and the form that creates a task.
// Run with: node --test web/src/__tests__/cov-settings-schedules.test.mjs
import test, { mock } from "node:test";
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
  byText,
} from "./cov-settings-helpers.mjs";

installDom();
const { useSchedules } = await load("useSchedules.ts");
const { ScheduledTasksModal } = await load("ScheduledTasksModal.tsx");
const { AppCtx } = await load("appStore.ts");
const { h, render } = await import("preact");

const NOW = Date.parse("2026-05-01T12:00:00Z");
const iso = (ms) => new Date(ms).toISOString();
const TASKS = [
  { id: "a", title: "Daily digest", task: "sum up", kind: "task", session_id: "s1", every: "1h0m0s", enabled: true, created: iso(NOW - 86400000), run_count: 3, next_run: iso(NOW + 90_000), last_outcome: "ok: 3 files" },
  { id: "b", title: "Late one", task: "x", kind: "plan", session_id: "s2", every: "24h", enabled: true, created: iso(NOW), run_count: 0, next_run: iso(NOW - 5_000) },
  { id: "c", title: "Paused", task: "x", kind: "task", session_id: "s3", every: "30m", enabled: false, created: iso(NOW - 3600000), last_run: iso(NOW - 60000), run_count: 1, next_run: iso(NOW + 1000) },
  { id: "d", title: "Paused new", task: "x", kind: "task", session_id: "s4", every: "2h", enabled: false, created: iso(NOW - 7200000), run_count: 0, next_run: "" },
  { id: "e", title: "No next", task: "x", kind: "task", session_id: "s5", every: "2h", enabled: true, created: iso(NOW), run_count: 0, next_run: "soon" },
];

function mount(api) {
  const toasts = [];
  const ref = {};
  function Harness() {
    const schedules = useSchedules(api, (t) => toasts.push(t));
    ref.s = schedules;
    return h(AppCtx.Provider, { value: { schedules } }, schedules.showScheduledTasks ? h(ScheduledTasksModal, null) : null);
  }
  const root = document.createElement("div");
  document.body.appendChild(root);
  render(h(Harness, null), root);
  return {
    root,
    toasts,
    ref,
    async open() {
      await settle();
      ref.s.openScheduledTasks();
      await settle();
    },
    row: (title) => [...root.querySelectorAll(".rounded-xl.border.p-3")].find((r) => text(r).startsWith(title)),
    async unmount() {
      render(null, root);
      root.remove();
      await settle();
    },
  };
}
async function press(el) {
  click(el);
  await settle();
}
const btn = (root, label) => byText(root, "button", label);

test("the panel lists the tasks with their countdown, which ticks only while it is open", async () => {
  mock.timers.enable({ apis: ["setInterval", "Date"], now: NOW });
  try {
    const f = fakeApi({ "GET /v1/schedules": json(200, { schedules: TASKS }) });
    const m = mount(f.api);
    await settle();
    assert.equal(f.calls.length, 0);
    await m.open();
    assert.deepEqual(f.calls.map((c) => c.path), ["/v1/schedules"]);
    assert.match(text(m.root), /Scheduled tasks/);
    assert.match(text(m.root), /Scheduled \(5\)/);
    const a = m.row("Daily digest");
    assert.match(text(a), /every 1h/);
    assert.equal(text(a.querySelector(".task-countdown")), "1:30");
    assert.match(text(a), /fires in 1:30/);
    assert.match(text(a), /ok: 3 files/);
    assert.equal(text(btn(a, "Pause")), "Pause");
    mock.timers.tick(1000);
    await settle();
    assert.equal(text(a.querySelector(".task-countdown")), "1:29");

    const b = m.row("Late one");
    assert.match(text(b), /due now/);
    assert.equal(b.querySelector(".task-countdown"), null);
    assert.match(text(b), /every 24h/);

    const c = m.row("Paused");
    assert.ok(text(c).includes("Paused — last " + new Date(NOW - 60000).toLocaleString()));
    assert.equal(c.querySelector(".task-countdown"), null);
    assert.ok(btn(c, "Resume"));
    const d = m.row("Paused new");
    assert.ok(text(d).includes("Paused — last " + new Date(NOW - 7200000).toLocaleString()));
    const e = m.row("No next");
    assert.doesNotMatch(text(e), /fires in|due now|Paused/);

    // Closed: the clock stops.
    await press(btn(m.root, "Close"));
    assert.equal(m.ref.s.showScheduledTasks, false);
    const before = m.ref.s.nowMs;
    mock.timers.tick(5000);
    await settle();
    assert.equal(m.ref.s.nowMs, before);
    await m.unmount();
  } finally {
    mock.timers.reset();
  }
});

test("an empty list, an odd answer, a refusal and a failure all show no task", async () => {
  for (const route of [
    json(200, { schedules: [] }),
    json(200, { schedules: "nope" }),
    json(503, { error: "off" }),
    new Error("offline"),
  ]) {
    const f = fakeApi({ "GET /v1/schedules": route });
    const m = mount(f.api);
    await m.open();
    assert.match(text(m.root), /Scheduled \(0\)/);
    assert.match(text(m.root), /No scheduled tasks yet/);
    await m.unmount();
  }
});

test("the ✕ and the backdrop close the panel", async () => {
  const f = fakeApi({ "GET /v1/schedules": json(200, { schedules: [] }) });
  const m = mount(f.api);
  await m.open();
  await press(m.root.querySelector("button[aria-label=Close]"));
  assert.equal(m.ref.s.showScheduledTasks, false);
  await m.open();
  await press(m.root.querySelector("[data-modal]"));
  assert.equal(m.ref.s.showScheduledTasks, false);
  await m.unmount();
});

test("run now, pause and delete each call the gateway and say how it went", async () => {
  let runAnswer = json(202, {});
  let listed = TASKS.slice(0, 1);
  const f = fakeApi({
    "GET /v1/schedules": () => json(200, { schedules: listed }),
    "POST /v1/schedules/a/run": () => runAnswer,
    "PATCH /v1/schedules/a": (_p, init) => {
      listed = [{ ...TASKS[0], enabled: JSON.parse(init.body).enabled }];
      return json(200, {});
    },
    "DELETE /v1/schedules/a": () => {
      listed = [];
      return json(200, {});
    },
  });
  const m = mount(f.api);
  await m.open();
  const row = () => m.row("Daily digest");

  await press(btn(row(), "Run now"));
  assert.deepEqual(m.toasts.at(-1), { message: "Task started", type: "success", detail: "It is running in s1." });
  runAnswer = json(409, { error: "already running" });
  await press(btn(row(), "Run now"));
  assert.deepEqual(m.toasts.at(-1), { message: "That task could not start", type: "error", detail: "already running" });
  runAnswer = new Response("x", { status: 409 });
  await press(btn(row(), "Run now"));
  assert.equal(m.toasts.at(-1).detail, undefined);
  // Another refusal says nothing.
  runAnswer = json(500, {});
  await press(btn(row(), "Run now"));
  assert.equal(m.toasts.length, 3);

  await press(btn(row(), "Pause"));
  assert.deepEqual(f.calls.find((c) => c.method === "PATCH").body, { enabled: false });
  assert.ok(btn(row(), "Resume"));
  await press(btn(row(), "Resume"));
  assert.deepEqual(f.calls.filter((c) => c.method === "PATCH")[1].body, { enabled: true });

  await press(btn(row(), "Delete"));
  assert.equal(row(), undefined);
  assert.match(text(m.root), /Scheduled \(0\)/);
  await m.unmount();
});

test("refused pause and delete leave the list as it was", async () => {
  const f = fakeApi({
    "GET /v1/schedules": json(200, { schedules: TASKS.slice(0, 1) }),
    "PATCH /v1/schedules/": json(404, {}),
    "DELETE /v1/schedules/": json(404, {}),
  });
  const m = mount(f.api);
  await m.open();
  const gets = () => f.calls.filter((c) => c.method === "GET").length;
  await press(btn(m.row("Daily digest"), "Pause"));
  await press(btn(m.row("Daily digest"), "Delete"));
  assert.equal(gets(), 1);
  assert.ok(m.row("Daily digest"));
  await m.unmount();
});

test("the form creates a task only with a title and an instruction, and clears itself", async () => {
  let created;
  const f = fakeApi({
    "GET /v1/schedules": () => json(200, { schedules: created ? [created] : [] }),
    "POST /v1/schedules": (_p, init) => {
      const b = JSON.parse(init.body);
      created = { ...TASKS[0], id: "n", title: b.title, every: b.every };
      return new Promise((r) => setTimeout(() => r(json(201, created)), 5));
    },
  });
  const m = mount(f.api);
  await m.open();
  const [title, every] = m.root.querySelectorAll(".tasks-form input");
  const body = m.root.querySelector(".tasks-form textarea");
  const kind = m.root.querySelector(".tasks-form select");
  const create = btn(m.root, "Create");
  assert.equal(every.value, "24h");
  assert.equal(kind.value, "task");
  assert.equal(create.disabled, true);
  type(title, "  Nightly  ");
  await settle();
  assert.equal(create.disabled, true);
  // Pressing it anyway (the hook's own guard) sends nothing.
  await m.ref.s.createSchedule();
  assert.equal(f.calls.filter((c) => c.method === "POST").length, 0);
  type(body, "  run the tests ");
  type(every, " 12h ");
  kind.value = "plan";
  kind.dispatchEvent(new window.Event("change", { bubbles: true }));
  await settle();
  assert.equal(create.disabled, false);
  click(create);
  await settle();
  assert.equal(text(btn(m.root, "Creating…")), "Creating…");
  await new Promise((r) => setTimeout(r, 10));
  await settle();
  assert.deepEqual(f.calls.find((c) => c.method === "POST").body, {
    title: "Nightly",
    task: "run the tests",
    kind: "plan",
    every: "12h",
  });
  assert.equal(title.value, "");
  assert.equal(body.value, "");
  assert.equal(every.value, " 12h ");
  assert.ok(m.row("Nightly"));
  assert.equal(text(btn(m.root, "Create")), "Create");
  await m.unmount();
});

test("a refused task is reported with the gateway's reason, and the form keeps its text", async () => {
  const answers = [json(400, { error: "bad cadence" }), new Response("x", { status: 500 })];
  const f = fakeApi({
    "GET /v1/schedules": json(200, { schedules: [] }),
    "POST /v1/schedules": () => answers.shift(),
  });
  const m = mount(f.api);
  await m.open();
  const [title, every] = m.root.querySelectorAll(".tasks-form input");
  type(title, "T");
  type(m.root.querySelector(".tasks-form textarea"), "do it");
  type(every, "often");
  await settle();
  await press(btn(m.root, "Create"));
  assert.deepEqual(m.toasts.at(-1), { message: "The task could not be created", type: "error", detail: "bad cadence" });
  assert.equal(title.value, "T");
  assert.equal(btn(m.root, "Create").disabled, false);
  await press(btn(m.root, "Create"));
  assert.equal(m.toasts.at(-1).detail, undefined);
  await m.unmount();
});

test("a create that cannot reach the gateway still frees the form", async () => {
  const f = fakeApi({
    "GET /v1/schedules": json(200, { schedules: [] }),
    "POST /v1/schedules": new Error("offline"),
  });
  const m = mount(f.api);
  await m.open();
  m.ref.s.setNewTaskTitle("T");
  m.ref.s.setNewTaskText("x");
  await settle();
  await assert.rejects(m.ref.s.createSchedule(), /offline/);
  await settle();
  assert.equal(m.ref.s.scheduledBusy, false);
  await m.unmount();
});
