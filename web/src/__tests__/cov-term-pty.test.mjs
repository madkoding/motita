// The interactive terminal (TermPty.tsx) with a stand-in screen and a stand-in WebSocket: what it
// connects to, what it sends, what it shows, and how it ends.
// Run with: node --test web/src/__tests__/cov-term-pty.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { setupDom, load, stub, mounter, flush } from "./cov-shell-helpers.mjs";

setupDom("https://motita.test/");

// The stand-in screen records what it is asked to do, and lets the test type and resize.
const views = [];
globalThis.__ptyFakeView = (host) => {
  const v = {
    host,
    written: [],
    fits: 0,
    focused: 0,
    disposed: false,
    data: null,
    resize: null,
    size: () => ({ cols: 90, rows: 20 }),
    write: (d) => v.written.push(new TextDecoder().decode(d)),
    onData: (fn) => (v.data = fn),
    onResize: (fn) => (v.resize = fn),
    fit: () => v.fits++,
    focus: () => v.focused++,
    dispose: () => (v.disposed = true),
  };
  views.push(v);
  return v;
};
stub(
  "./xtermView",
  "export const createView = (host) => globalThis.__ptyFakeView(host);",
);

// The stand-in WebSocket: the test answers for the gateway.
const sockets = [];
class FakeSocket {
  static OPEN = 1;
  constructor(url) {
    this.url = url;
    this.readyState = 1;
    this.sent = [];
    this.closed = false;
    sockets.push(this);
  }
  send(d) {
    this.sent.push(d);
  }
  close() {
    this.closed = true;
  }
}
globalThis.WebSocket = FakeSocket;
window.WebSocket = FakeSocket;

const { TermPty, terminalUrl } = await load("TermPty.tsx");
const { h } = await import("preact");
const { act } = await import("preact/test-utils");
const mount = await mounter();

async function show(props) {
  const ends = [];
  const m = await mount(
    h(TermPty, {
      machine: "",
      sid: "s1",
      open: true,
      onEnd: (r) => ends.push(r),
      ...props,
    }),
  );
  await act(() => flush(3));
  return { ...m, ends, view: views.at(-1), socket: sockets.at(-1) };
}

test("the address names the session, the screen's size and the machine it is on", () => {
  assert.equal(
    terminalUrl("", "a b", 80, 24),
    "wss://motita.test/v1/sessions/a%20b/terminal?cols=80&rows=24",
  );
  assert.equal(
    terminalUrl("pc", "s", 1, 2),
    "wss://motita.test/v1/peers/pc/proxy/v1/sessions/s/terminal?cols=1&rows=2",
  );
});

test("keys and resizes go out, output comes in, and the shell's exit ends it once", async () => {
  const { ends, view, socket, rerender, done } = await show({});
  assert.equal(socket.url, terminalUrl("", "s1", 90, 20));
  assert.equal(socket.binaryType, "arraybuffer");
  assert.equal(view.focused, 1);

  view.data("ls\n");
  view.resize(100, 30);
  assert.equal(new TextDecoder().decode(socket.sent[0]), "ls\n");
  assert.deepEqual(JSON.parse(socket.sent[1]), {
    type: "resize",
    cols: 100,
    rows: 30,
  });
  // A socket that is not open any more takes nothing.
  socket.readyState = 3;
  view.data("x");
  view.resize(1, 1);
  assert.equal(socket.sent.length, 2);

  socket.onmessage({ data: new TextEncoder().encode("total 0\n").buffer });
  assert.deepEqual(view.written, ["total 0\n"]);
  socket.onmessage({ data: "{not json" });
  socket.onmessage({ data: JSON.stringify({ type: "later" }) });
  assert.deepEqual(ends, []);

  // Opened again, the screen is refitted and focused; closed, it is left alone.
  await rerender(
    h(TermPty, { machine: "", sid: "s1", open: false, onEnd: () => {} }),
  );
  await rerender(
    h(TermPty, {
      machine: "",
      sid: "s1",
      open: true,
      onEnd: (r) => ends.push(r),
    }),
  );
  assert.equal(view.fits, 1);
  assert.equal(view.focused, 2);

  socket.onmessage({ data: JSON.stringify({ type: "exit", code: 0 }) });
  socket.onclose();
  assert.deepEqual(ends, [undefined]);
  await done();
  assert.ok(socket.closed && view.disposed);
});

test("a refusal ends it with the gateway's reason, or a plain one", async () => {
  const a = await show({});
  a.socket.onmessage({
    data: JSON.stringify({ type: "error", message: "the agent is working" }),
  });
  assert.deepEqual(a.ends, ["the agent is working"]);
  await a.done();

  const b = await show({});
  b.socket.onmessage({ data: JSON.stringify({ type: "error" }) });
  assert.deepEqual(b.ends, ["The terminal could not be opened"]);
  await b.done();

  const c = await show({});
  c.socket.onerror();
  assert.deepEqual(c.ends, ["The terminal could not be opened"]);
  await c.done();
});

test("unmounted before the screen loaded, it opens nothing", async () => {
  const before = sockets.length;
  const m = await mount(
    h(TermPty, { machine: "", sid: "s2", open: true, onEnd: () => {} }),
  );
  await m.done();
  await act(() => flush(3));
  assert.equal(sockets.length, before);
});
