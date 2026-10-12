// Regression tests for session isolation in the web interface: a run belongs to ONE session, and
// the tab must be free to open another one while the agent works.
//
// The bug these pin: the tab kept ONE stream and ONE `running` flag for whatever conversation was
// on screen. Opening a new session while a run was in flight carried the old run's flag into it
// (its composer stayed locked, and a message typed there was queued on a session with no run), and
// the old run's events kept being drawn into the new conversation.
//
// The hooks import their siblings without extensions, which node cannot load on its own, so the
// real hooks are bundled with esbuild (already installed with vite) and driven in jsdom against a
// fake gateway. Run with: node --test web/src/__tests__/
import test from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { mkdtempSync, writeFileSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { JSDOM } from 'jsdom'
import { build } from 'esbuild'

const here = dirname(fileURLToPath(import.meta.url))
const src = join(here, '..')

const dom = new JSDOM('<!doctype html><html><body></body></html>', { url: 'http://localhost/' })
globalThis.window = dom.window
globalThis.document = dom.window.document
globalThis.history = dom.window.history
globalThis.localStorage = dom.window.localStorage
globalThis.CustomEvent = dom.window.CustomEvent
globalThis.requestAnimationFrame = (cb) => setTimeout(() => cb(Date.now()), 0)
globalThis.cancelAnimationFrame = (id) => clearTimeout(id)
try { globalThis.navigator ??= dom.window.navigator } catch { /* node already has one */ }

// The harness mounts the four hooks a run goes through, wired the way appStore wires them.
const harness = `
import { h, render } from 'preact'
import { useRunState } from './useRunState'
import { useSessionList } from './useSessionList'
import { useConversation } from './useConversation'
import { useStream } from './useStream'
import { isLive, openLink, cutLink } from './runLink'
export { isLive, openLink, cutLink }
export function mount(api, onStore) {
  const noop = () => {}
  function H() {
    const rs = useRunState()
    const lists = useSessionList(api, rs)
    const conv = useConversation({ api, rs, lists, setState: noop, setConfig: noop, requestModal: noop })
    const stream = useStream({ api, rs, lists, conv, setState: noop, setToast: noop })
    onStore({ rs, lists, conv, stream })
    return null
  }
  const root = document.createElement('div')
  document.body.appendChild(root)
  render(h(H, {}), root)
  return () => render(null, root)
}
`

const dir = mkdtempSync(join(tmpdir(), 'motita-sessions-test-'))
const out = join(dir, 'harness.mjs')
await build({
  stdin: { contents: harness, resolveDir: src, loader: 'ts', sourcefile: 'harness.ts' },
  bundle: true, format: 'esm', platform: 'browser', outfile: out, logLevel: 'silent',
  jsx: 'automatic', jsxImportSource: 'preact',
})
const H = await import(out)
rmSync(dir, { recursive: true, force: true })

const tick = (ms = 5) => new Promise(r => setTimeout(r, ms))

// sse is one stream the fake gateway answers with, pushed by the test frame by frame. It ends with
// an AbortError when the request's signal is aborted, as a real fetch body does.
function sse(signal) {
  let ctrl
  const enc = new TextEncoder()
  const body = new ReadableStream({ start(c) { ctrl = c } })
  let open = true
  signal?.addEventListener('abort', () => {
    if (open) { open = false; ctrl.error(new DOMException('aborted', 'AbortError')) }
  })
  return {
    response: new Response(body, { status: 200, headers: { 'Content-Type': 'text/event-stream' } }),
    send(event, data, id) {
      if (!open) { return false }
      ctrl.enqueue(enc.encode(`id: ${id}\nevent: ${event}\ndata: ${JSON.stringify(data)}\n\n`))
      return true
    },
    close() { if (open) { open = false; ctrl.close() } },
    fail() { if (open) { open = false; ctrl.error(new TypeError('network error')) } },
    get open() { return open },
  }
}

const json = (body, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

// fakeGateway holds two sessions, A and B. A run in A is held open by the test.
function fakeGateway() {
  const calls = []
  const streams = { task: {}, events: {} }
  const runningIn = new Set()
  const api = async (path, init = {}) => {
    const method = init.method || 'GET'
    calls.push(method + ' ' + path)
    let m
    if ((m = path.match(/^\/v1\/sessions\/(\w+)\/task$/)) && method === 'POST') {
      runningIn.add(m[1])
      const s = sse(init.signal)
      streams.task[m[1]] = s
      return s.response
    }
    if ((m = path.match(/^\/v1\/sessions\/(\w+)\/events/))) {
      if (!runningIn.has(m[1])) { return json({ error: 'no run' }, 404) }
      const s = sse(init.signal)
      streams.events[m[1]] = s
      return s.response
    }
    if ((m = path.match(/^\/v1\/sessions\/(\w+)\/queue$/))) { return json({}) }
    if ((m = path.match(/^\/v1\/sessions\/(\w+)\/run$/))) {
      return runningIn.has(m[1]) ? json({ outcome: '' }) : json({ error: 'none' }, 404)
    }
    if (path.endsWith('/messages')) { return json({ messages: [] }) }
    if (path.endsWith('/checkpoints')) { return json({ checkpoints: [] }) }
    if (path.endsWith('/agents')) { return json({ agents: [] }) }
    if (path.endsWith('/config')) { return json({}) }
    if (path === '/v1/sessions') {
      return json({ sessions: ['A', 'B'].map(id => ({ id, title: id, running: runningIn.has(id) })) })
    }
    return json({}, 404)
  }
  return { api, calls, streams, runningIn }
}

const texts = (rs) => rs.messages.map(m => m.text)

test('a run link only draws while it is current and its session is on screen', () => {
  const holder = { current: null }
  const a = H.openLink(holder, 'A')
  assert.equal(H.isLive(a, holder, 'A'), true)
  assert.equal(H.isLive(a, holder, 'B'), false, 'another session on screen: the stream is not drawn')
  const b = H.openLink(holder, 'B')
  assert.equal(a.ctrl.signal.aborted, true, 'opening a new link cuts the old one')
  assert.equal(H.isLive(a, holder, 'A'), false)
  assert.equal(H.cutLink(holder), 'B')
  assert.equal(b.ctrl.signal.aborted, true)
  assert.equal(holder.current, null)
  assert.equal(H.cutLink(holder), '', 'cutting nothing is harmless')
})

test('opening another session while a run is in flight leaves it idle, unmixed and able to send', async () => {
  const gw = fakeGateway()
  let store
  const unmount = H.mount(gw.api, s => { store = s })
  await tick()

  await store.lists.fetchSessions()
  await store.conv.switchSession('A', 'none')
  await tick()
  void store.stream.submit('task in A')
  await tick()
  const a = gw.streams.task.A
  assert.ok(a, 'the task was posted to A')
  a.send('attached', { turn: 0 }, 1)
  a.send('progress', { text: 'working on A' }, 2)
  await tick()
  assert.equal(store.rs.runningRef.current, true)
  assert.equal(store.rs.activity, 'working on A')

  // The reader opens session B while A is still working.
  await store.conv.switchSession('B', 'none')
  await tick()
  assert.equal(store.rs.sessionRef.current, 'B')
  assert.equal(store.rs.running, false, 'B is not running: its composer must not be locked')
  assert.equal(store.rs.runningRef.current, false)
  assert.equal(store.rs.activity, null, "A's activity is not shown over B")
  assert.equal(a.open, false, "A's stream was let go")
  assert.ok(!gw.calls.some(c => c.includes('/A/interrupt')), 'leaving A does not stop its run')
  assert.equal(store.lists.sessions.find(s => s.id === 'A')?.running, true, "A's row still shows it working")

  // Whatever A does now must not reach B's view.
  a.send('progress', { text: 'still A' }, 3)
  a.send('done', { result: 'answer of A' }, 4)
  await tick()
  assert.ok(!texts(store.rs).includes('answer of A'), "A's answer is not drawn in B")
  assert.notEqual(store.rs.activity, 'still A')

  // B can start its own run: a message sent there is a task of B, not a message queued on B.
  void store.stream.submit('task in B')
  await tick()
  assert.ok(gw.calls.includes('POST /v1/sessions/B/task'), 'B started a run of its own')
  assert.ok(!gw.calls.some(c => c.includes('/B/queue')), 'nothing was queued on B')
  const b = gw.streams.task.B
  b.send('done', { result: 'answer of B' }, 1)
  await tick()
  assert.deepEqual(texts(store.rs).filter(t => t.startsWith('answer')), ['answer of B'])
  assert.equal(store.rs.runningRef.current, false)
  unmount()
})

test('coming back to a session whose run is still in flight follows that run', async () => {
  const gw = fakeGateway()
  let store
  const unmount = H.mount(gw.api, s => { store = s })
  await tick()

  await store.conv.switchSession('A', 'none')
  void store.stream.submit('task in A')
  await tick()
  await store.conv.switchSession('B', 'none')
  await tick()
  await store.conv.switchSession('A', 'none')
  await tick()

  assert.ok(gw.calls.some(c => c.startsWith('GET /v1/sessions/A/events')), 'A is followed again')
  assert.equal(store.rs.runningRef.current, true, 'A shows its run')
  const ev = gw.streams.events.A
  ev.send('done', { result: 'answer of A' }, 1)
  await tick()
  assert.ok(texts(store.rs).includes('answer of A'), "A's answer lands in A")
  assert.equal(store.rs.runningRef.current, false)
  unmount()
})

test('a reconnect timer left by a session the reader left never follows the new one', async () => {
  const gw = fakeGateway()
  let store
  const unmount = H.mount(gw.api, s => { store = s })
  await tick()

  await store.conv.switchSession('A', 'none')
  void store.stream.submit('task in A')
  await tick()
  // A's connection drops: the tab schedules a reconnection.
  gw.streams.task.A.fail()
  await tick()
  await store.conv.switchSession('B', 'none')
  await tick(1200) // longer than RECONNECT_MS
  assert.ok(!gw.calls.some(c => c.startsWith('GET /v1/sessions/B/events')), 'B was never followed')
  unmount()
})
