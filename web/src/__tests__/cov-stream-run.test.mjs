// A run as useStream drives it: the task is sent, the server-sent events are read and drawn, a
// dropped stream is followed again, a refusal is explained, a run left behind keeps going in the
// gateway, and messages sent to a run in flight are queued or interrupt it. The hook runs over the
// real run state and session list, against a fake gateway that answers with streams the test
// writes.
import test, { mock } from "node:test";
import assert from "node:assert/strict";
import { installDom, source, renderHook, res } from "./cov-stream-helpers.mjs";

installDom();
window.matchMedia = () => ({ matches: false });
const { useRunState } = await source("useRunState.ts");
const { useSessionList } = await source("useSessionList.ts");
const { useStream } = await source("useStream.ts");

const enc = new TextEncoder();

// body is a response body the test writes to: push(text), end(), fail(error).
function body() {
  let ctrl;
  const stream = new ReadableStream({
    start(c) {
      ctrl = c;
    },
  });
  return {
    stream,
    push: (text) => ctrl.enqueue(enc.encode(text)),
    end: () => ctrl.close(),
    fail: (e) => ctrl.error(e),
  };
}
const ev = (event, data, id) =>
  (id ? `id: ${id}\n` : "") +
  (event ? `event: ${event}\n` : "") +
  `data: ${typeof data === "string" ? data : JSON.stringify(data)}\n\n`;
const streamRes = (b) => res(200, null, { body: b.stream });
const tick = async (n = 10) => {
  for (let i = 0; i < n; i++) {
    await Promise.resolve();
  }
};

// mount runs the hook. routes maps "METHOD path" to a handler returning a response.
async function mount(routes = {}) {
  const calls = [];
  const api = async (path, init = {}) => {
    const key = (init.method || "GET") + " " + path;
    calls.push({ key, body: init.body, signal: init.signal });
    const route = routes[key];
    if (!route) {
      throw new Error("no route " + key);
    }
    return route(init);
  };
  routes["GET /v1/sessions"] ??= () => res(200, { sessions: [] });
  const conv = {
    checkpoints: [],
    agents: [],
    transcripts: 0,
    switched: [],
    loadCheckpoints: async (sid) => conv.checkpoints.push(sid),
    loadAgents: async (sid) => conv.agents.push(sid),
    transcript: async () => {
      conv.transcripts++;
    },
    switchSessionRef: {
      current: async (...args) => {
        conv.switched.push(args);
      },
    },
    followRef: { current: null },
    detachRef: { current: null },
  };
  const states = [];
  const toasts = [];
  const h = await renderHook(() => {
    const rs = useRunState();
    const lists = useSessionList(api, rs);
    const stream = useStream({
      api,
      rs,
      lists,
      conv,
      setState: (...a) => states.push(a),
      setToast: (t) => toasts.push(t),
    });
    return { rs, lists, stream };
  });
  const cur = () => h.result.current;
  await h.act(() => {
    cur().rs.sessionRef.current = "s1";
    cur().rs.setSessionId("s1");
    cur().lists.setSessions([{ id: "s1" }, { id: "s2" }]);
  });
  return { h, cur, api, calls, routes, conv, states, toasts };
}

test("a task is sent and its stream is drawn until the answer", async () => {
  const b = body();
  const m = await mount({ "POST /v1/sessions/s1/run": () => streamRes(b) });
  const { h, cur, conv, states } = m;
  assert.equal(conv.followRef.current, cur().stream.followReconnect);
  assert.equal(conv.detachRef.current, cur().stream.detach);
  let done;
  await h.act(async () => {
    done = cur().stream.submit("hello", undefined, "shown text");
    await tick();
  });
  assert.equal(cur().rs.running, true);
  assert.equal(cur().lists.sessions[0].running, true);
  assert.deepEqual(cur().rs.messages[0], {
    id: cur().rs.messages[0].id,
    role: "user",
    text: "hello",
    shown: "shown text",
  });
  assert.equal(m.calls[0].body, '{"task":"hello"}');
  await h.act(async () => {
    // A frame split over two chunks, an id that cannot be read, a frame with no data and one with
    // no event name, and data that is not JSON.
    b.push(ev("attached", { turn: 1 }, "1") + "id: 2\nevent: progr");
    b.push("ess\ndata: " + JSON.stringify({ text: "running: ls" }) + "\n\n");
    b.push("id: x\nevent: progress\ndata: " + JSON.stringify({ text: "planning..." }) + "\n\n");
    b.push("event: ping\n\n");
    b.push(ev("progress", { text: "running: pwd" }));
    b.push(ev("progress", { text: "output (exit 0):\n/" }));
    b.push(ev(null, "plain words"));
    b.push(ev("answer", { text: "Hi" }, "5"));
    await tick();
  });
  assert.equal(cur().rs.lastIdRef.current, 5);
  assert.deepEqual(
    cur().rs.stepsByTurn[1].map((s) => s.text),
    ["running: ls", "planning...", "running: pwd"],
  );
  assert.equal(cur().rs.messages[1].text, "plain words");
  assert.equal(cur().rs.messages[2].draft, true);
  await h.act(async () => {
    b.push(ev("done", { result: "Hi there" }, "6"));
    b.end();
    await done;
  });
  assert.equal(cur().rs.running, false);
  assert.equal(cur().rs.messages[2].text, "Hi there");
  assert.equal(cur().rs.messages[2].draft, undefined);
  // The command that never reported a result is closed as "no result"; the other keeps its own.
  assert.deepEqual(
    cur().rs.shellLog.map((e) => [e.cmd, e.exit]),
    [
      ["ls", -999],
      ["pwd", 0],
    ],
  );
  assert.deepEqual(conv.checkpoints, ["s1"]);
  assert.deepEqual(conv.agents, ["s1"]);
  assert.deepEqual(states, [["working"], ["ready"]]);
  // The list read after the answer no longer has the run in flight: nothing stays masked.
  assert.equal(cur().lists.answeredHere("s1"), false);
  assert.ok(m.calls.some((c) => c.key === "GET /v1/sessions"), "the list is read again");
  await h.unmount();
});

test("a stream that ends without its last blank line, or without a terminal event, still finishes", async () => {
  const b = body();
  const m = await mount({ "POST /v1/merge": () => streamRes(b) });
  const { h, cur, states } = m;
  let done;
  await h.act(async () => {
    done = cur().stream.submit("Integrate", "/v1/merge");
    await tick();
  });
  await h.act(async () => {
    b.push(ev("answer", { text: "partial" }));
    b.push("event: progress\ndata: " + JSON.stringify({ text: "tail" }));
    b.end();
    await done;
  });
  assert.equal(cur().rs.activity, null, "finish clears the activity the tail set");
  assert.equal(cur().rs.messages.length, 1, "the unfinished draft is dropped");
  assert.equal(cur().rs.running, false);
  assert.deepEqual(states.at(-1), ["ready"]);
  assert.equal(m.calls[0].key, "POST /v1/merge");

  // Whitespace left in the buffer is not an event.
  const b2 = body();
  m.routes["POST /v1/sessions/s1/run"] = () => streamRes(b2);
  await h.act(async () => {
    done = cur().stream.submit("again");
    await tick();
  });
  await h.act(async () => {
    b2.push(ev("done", { result: "ok" }) + "  \n");
    b2.end();
    await done;
  });
  assert.equal(cur().rs.messages.at(-1).text, "ok");
  await h.unmount();
});

test("a refused turn says why, and says how to continue an integrated session", async () => {
  const m = await mount({
    "POST /v1/sessions/s1/run": () => res(409, { error: "the session has already been integrated" }),
  });
  const { h, cur, toasts, states } = m;
  // submit reads the list it was made with: the conversation's loaders are new, so it is made
  // again over the list that knows the session can be continued.
  await h.act(() => {
    m.conv.loadCheckpoints = async (sid) => m.conv.checkpoints.push(sid);
    cur().lists.setSessions([{ id: "s1", continuable: true }]);
  });
  await h.act(() => cur().stream.submit("x"));
  assert.equal(toasts[0].message, "This session is already integrated");
  assert.equal(toasts[0].type, "warning");
  assert.match(toasts[0].detail, /Continue in the session menu/);
  assert.deepEqual(states.at(-2), ["the session has already been integrated", true]);
  assert.equal(cur().rs.running, false);

  // Integrated but not continuable, a refusal without a reason, and one that is not JSON.
  await h.act(() => {
    m.conv.loadCheckpoints = async (sid) => m.conv.checkpoints.push(sid);
    cur().lists.setSessions([{ id: "s1" }]);
  });
  await h.act(() => cur().stream.submit("x"));
  m.routes["POST /v1/sessions/s1/run"] = () => res(500, {});
  await h.act(() => cur().stream.submit("x"));
  m.routes["POST /v1/sessions/s1/run"] = () => res(502, new Error("html"));
  await h.act(() => cur().stream.submit("x"));
  assert.deepEqual(
    toasts.slice(1).map((t) => [t.message, t.type]),
    [
      ["the session has already been integrated", "error"],
      ["the gateway refused the turn", "error"],
      ["the gateway refused the turn", "error"],
    ],
  );
  // The gateway cannot be reached at all.
  m.routes["POST /v1/sessions/s1/run"] = () => {
    throw new Error("offline");
  };
  await h.act(() => cur().stream.submit("x"));
  assert.deepEqual(states.at(-2), ["the gateway refused the turn", true]);
  assert.equal(cur().rs.running, false);
  await h.unmount();
});

test("leaving a session lets go of its run, which keeps going in the gateway", async () => {
  let answer;
  const m = await mount({
    "POST /v1/sessions/s1/run": (init) =>
      new Promise((resolve, reject) => {
        answer = resolve;
        init.signal.addEventListener("abort", () => reject(new Error("aborted")));
      }),
  });
  const { h, cur, states } = m;
  let done;
  await h.act(async () => {
    done = cur().stream.submit("long task");
    await tick();
  });
  await h.act(async () => {
    cur().rs.reconnectTimer.current = setTimeout(() => assert.fail("must not fire"), 50);
    cur().rs.setActivity("busy");
    cur().stream.detach();
    await done;
  });
  assert.equal(cur().rs.reconnectTimer.current, null);
  assert.equal(cur().rs.running, false);
  assert.equal(cur().rs.activity, null);
  assert.equal(cur().rs.runSessionRef.current, "");
  assert.equal(cur().lists.sessions[0].running, true, "its row keeps the spinner");
  assert.deepEqual(states, [["working"]], "nothing failed");
  // Detaching with nothing running does nothing more.
  await h.act(() => cur().stream.detach());
  assert.equal(cur().lists.sessions[0].running, true);

  // A run whose request answered after the reader left is not drawn.
  m.routes["POST /v1/sessions/s1/run"] = () =>
    new Promise((resolve) => {
      answer = resolve;
    });
  await h.act(async () => {
    done = cur().stream.submit("again");
    await tick();
  });
  await h.act(async () => {
    cur().rs.sessionRef.current = "s2";
    answer(res(500, {}));
    await done;
  });
  assert.equal(m.toasts.length, 0, "no refusal is shown in the other session");
  await h.unmount();
});

test("detach uses the stream's own session when the run's is not known", async () => {
  const m = await mount({});
  const { h, cur } = m;
  await h.act(() => cur().lists.setSessions([{ id: "s1" }, { id: "s2" }]));
  // A run followed (not started) by this tab: only the link knows its session.
  await h.act(() => {
    cur().rs.runningRef.current = true;
    cur().rs.linkRef.current = { sid: "s2", ctrl: new AbortController() };
  });
  await h.act(() => cur().stream.detach());
  assert.deepEqual(
    cur().lists.sessions.map((s) => [s.id, s.running]),
    [
      ["s1", undefined],
      ["s2", true],
    ],
  );
  // With neither, no row is touched.
  await h.act(() => {
    cur().rs.runningRef.current = true;
  });
  await h.act(() => cur().stream.detach());
  assert.equal(cur().rs.runningRef.current, false);
  assert.equal(cur().lists.sessions[0].running, undefined);
  await h.unmount();
});

test("events of a stream that is no longer the one on screen are dropped", async () => {
  const b = body();
  const m = await mount({ "POST /v1/sessions/s1/run": () => streamRes(b) });
  const { h, cur } = m;
  let done;
  await h.act(async () => {
    done = cur().stream.submit("x");
    await tick();
  });
  // A tail that arrives without its blank line, then the reader leaves before the stream ends.
  await h.act(async () => {
    b.push("event: answer\ndata: " + JSON.stringify({ text: "late" }));
    await tick();
  });
  await h.act(async () => {
    cur().rs.sessionRef.current = "s2";
    b.end();
    await done;
  });
  assert.equal(cur().rs.messages.length, 1, "the tail is not drawn");
  assert.equal(cur().rs.running, true, "and the run is not finished here");

  // A chunk that arrives after the reader left cancels the read.
  const b2 = body();
  m.routes["POST /v1/sessions/s1/run"] = () => streamRes(b2);
  await h.act(() => {
    cur().rs.sessionRef.current = "s1";
  });
  await h.act(async () => {
    done = cur().stream.submit("y");
    await tick();
  });
  await h.act(async () => {
    cur().rs.sessionRef.current = "s2";
    b2.push(ev("answer", { text: "late" }));
    await done;
  });
  assert.equal(cur().rs.messages.some((x) => x.text === "late"), false);
  await h.unmount();
});

test("a stream that breaks is followed again from the last event read", async () => {
  mock.timers.enable({ apis: ["setTimeout"] });
  try {
    const b = body();
    const m = await mount({ "POST /v1/sessions/s1/run": () => streamRes(b) });
    const { h, cur, states, conv } = m;
    let done;
    await h.act(async () => {
      done = cur().stream.submit("x");
      await tick();
    });
    await h.act(async () => {
      b.push(ev("attached", { turn: 3 }, "7"));
      b.fail(new Error("connection reset"));
      await done;
    });
    assert.deepEqual(states.at(-1), ["reconnecting", true]);
    assert.ok(cur().rs.reconnectTimer.current);

    // The reconnection is refused once, then the gateway cannot be reached, then it answers.
    let attempt = 0;
    const b2 = body();
    m.routes["GET /v1/sessions/s1/events?from=7"] = () => {
      attempt++;
      if (attempt === 1) {
        return res(503, {});
      }
      if (attempt === 2) {
        throw new Error("offline");
      }
      return streamRes(b2);
    };
    await h.act(async () => {
      mock.timers.tick(1000);
      await tick();
    });
    assert.equal(attempt, 1);
    await h.act(async () => {
      mock.timers.tick(1000);
      await tick();
    });
    assert.equal(attempt, 2);
    await h.act(async () => {
      mock.timers.tick(1000);
      await tick();
    });
    assert.equal(attempt, 3);
    await h.act(async () => {
      b2.push(ev("done", { result: "back" }, "8"));
      b2.end();
      await tick(20);
    });
    assert.equal(cur().rs.messages.at(-1).text, "back");
    assert.equal(cur().rs.running, false);
    // The stream ended after the run finished: nothing is retried.
    const calls = m.calls.length;
    await h.act(async () => {
      mock.timers.tick(5000);
      await tick();
    });
    assert.equal(m.calls.filter((c) => c.key.includes("/events")).length, 3);
    assert.ok(m.calls.length >= calls);
    // With nothing running, following is a no-op.
    await h.act(() => cur().stream.followReconnect());
    assert.equal(m.calls.filter((c) => c.key.includes("/events")).length, 3);
    assert.equal(conv.transcripts, 0);
    await h.unmount();
  } finally {
    mock.timers.reset();
  }
});

test("a run the gateway no longer has is finished and the transcript is read", async () => {
  const m = await mount({});
  const { h, cur, conv } = m;
  await h.act(() => {
    cur().rs.runningRef.current = true;
    cur().rs.setRunning(true);
  });
  m.routes["GET /v1/sessions/s1/events?from=0"] = () => res(404, {});
  await h.act(() => cur().stream.followReconnect());
  assert.equal(cur().rs.running, false);
  assert.equal(conv.transcripts, 1);

  // The run ended while the request was out: nothing more to do.
  await h.act(() => {
    cur().rs.runningRef.current = true;
  });
  m.routes["GET /v1/sessions/s1/events?from=0"] = () => {
    cur().rs.runningRef.current = false;
    return res(404, {});
  };
  await h.act(() => cur().stream.followReconnect());
  assert.equal(conv.transcripts, 1);

  // The reader left while the request was out: the answer is not used, and nothing retries.
  await h.act(() => {
    cur().rs.runningRef.current = true;
  });
  m.routes["GET /v1/sessions/s1/events?from=0"] = () => {
    cur().rs.sessionRef.current = "s2";
    return res(404, {});
  };
  await h.act(() => cur().stream.followReconnect());
  assert.equal(conv.transcripts, 1);
  m.routes["GET /v1/sessions/s2/events?from=0"] = () => {
    cur().rs.sessionRef.current = "s1";
    throw new Error("offline");
  };
  await h.act(() => cur().stream.followReconnect());
  assert.equal(cur().rs.reconnectTimer.current, null, "a stale stream is not retried");

  // A body that cannot be read counts as a broken stream, and is retried.
  mock.timers.enable({ apis: ["setTimeout"] });
  try {
    m.routes["GET /v1/sessions/s1/events?from=0"] = () => res(200, null, { body: null });
    const states = m.states.length;
    await h.act(() => cur().stream.followReconnect());
    assert.deepEqual(m.states.slice(states), [["reconnecting", true]]);
    await h.act(() => cur().stream.detach());
    mock.timers.tick(5000);
  } finally {
    mock.timers.reset();
  }
  await h.unmount();
});

test("a stream that breaks after the reader left is not followed", async () => {
  const b = body();
  const m = await mount({ "POST /v1/sessions/s1/run": () => streamRes(b) });
  const { h, cur, states } = m;
  let done;
  await h.act(async () => {
    done = cur().stream.submit("x");
    await tick();
  });
  await h.act(async () => {
    cur().rs.sessionRef.current = "s2";
    b.fail(new Error("reset"));
    await done;
  });
  assert.deepEqual(states, [["working"]]);
  assert.equal(cur().rs.reconnectTimer.current, null);
  await h.unmount();
});

test("messages sent to a run in flight are queued, or interrupt it, and the next run is followed", async () => {
  mock.timers.enable({ apis: ["setTimeout"] });
  try {
    const m = await mount({
      "POST /v1/sessions/s1/queue": () => res(200, {}),
      "POST /v1/sessions/s1/interrupt": () => res(200, {}),
    });
    const { h, cur, conv, toasts } = m;
    await h.act(() => cur().stream.sendWhileRunning("also this", false));
    await h.act(() => cur().stream.sendWhileRunning("stop and do this", true));
    assert.deepEqual(
      m.calls.map((c) => [c.key, c.body]),
      [
        ["POST /v1/sessions/s1/queue", '{"task":"also this"}'],
        ["POST /v1/sessions/s1/interrupt", '{"task":"stop and do this"}'],
      ],
    );
    assert.equal(cur().rs.handoffRef.current, true);
    // The run ends: the conversation is read again twice, to follow the run the gateway starts.
    await h.act(() => cur().stream.finish());
    assert.equal(cur().rs.handoffRef.current, false);
    mock.timers.tick(400);
    assert.deepEqual(conv.switched, [["s1", "none", true]]);
    // By the second reload a run is already followed: it is not reloaded again.
    cur().rs.runningRef.current = true;
    mock.timers.tick(1400);
    assert.equal(conv.switched.length, 1);
    cur().rs.runningRef.current = false;

    // The reader moved to another session before the reload: nothing is reloaded.
    await h.act(() => cur().stream.sendWhileRunning("more", false));
    await h.act(() => cur().stream.finish());
    cur().rs.sessionRef.current = "s2";
    mock.timers.tick(2000);
    assert.equal(conv.switched.length, 1);
    cur().rs.sessionRef.current = "s1";
    // No conversation to reload into yet.
    await h.act(() => cur().stream.sendWhileRunning("more", false));
    await h.act(() => cur().stream.finish());
    conv.switchSessionRef.current = null;
    mock.timers.tick(2000);
    assert.equal(conv.switched.length, 1);

    // Refused or unreachable: said, and nothing is followed.
    m.routes["POST /v1/sessions/s1/queue"] = () => res(409, {});
    m.routes["POST /v1/sessions/s1/interrupt"] = () => {
      throw new Error("offline");
    };
    await h.act(() => cur().stream.sendWhileRunning("x", false));
    await h.act(async () => {
      cur().stream.sendWhileRunning("y", true);
      await tick();
    });
    assert.deepEqual(toasts, [
      { message: "the gateway refused the turn", type: "error" },
      { message: "the gateway refused the turn", type: "error" },
    ]);
    assert.equal(cur().rs.handoffRef.current, false);

    // Stop sends an interrupt with nothing queued; a failure to reach the gateway is silent.
    await h.act(() => cur().stream.stopRun());
    assert.deepEqual(m.calls.at(-1), { ...m.calls.at(-1), key: "POST /v1/sessions/s1/interrupt", body: "{}" });
    m.routes["POST /v1/sessions/s1/interrupt"] = () => Promise.reject(new Error("offline"));
    await h.act(() => cur().stream.stopRun());
    assert.equal(toasts.length, 2);
    await h.unmount();
  } finally {
    mock.timers.reset();
  }
});
