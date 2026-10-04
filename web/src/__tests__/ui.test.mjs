// Tests for the UI changes that settings.test.mjs does not cover: the
// new-session spinner, and the global CSS the settings and the design system
// rely on. Run with: node --test web/src/__tests__/
import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { JSDOM } from 'jsdom'

const here = dirname(fileURLToPath(import.meta.url))
const css = readFileSync(join(here, '..', 'index.css'), 'utf8')

// ── The new-session button's spinner ────────────────────────────────────────
// Creating a session in a project makes the gateway pull/fetch the repo first,
// which is not instant. The button must show a spinner while that runs and go
// back to the plus when it is done.

const dom = new JSDOM('<!doctype html><html><body></body></html>', { url: 'http://localhost/' })
globalThis.document = dom.window.document
globalThis.window = dom.window
globalThis.requestAnimationFrame = (cb) => setTimeout(() => cb(Date.now()), 0)
globalThis.cancelAnimationFrame = (id) => clearTimeout(id)

const { h, render } = await import('preact')
const { NewSessionButton } = await import('../NewSessionButton.ts')

test('the new-session button shows a spinner while loading and the plus when idle', async () => {
  const root = document.createElement('div')
  document.body.appendChild(root)

  render(h(NewSessionButton, { loading: true, onClick: () => {}, label: 'New session' }), root)
  await Promise.resolve()
  assert.ok(root.querySelector('.btn-spinner'), 'a spinner is shown while the session is being created')
  assert.equal(root.querySelector('svg'), null, 'the plus is replaced by the spinner')
  assert.equal(root.querySelector('button').disabled, true, 'a second click is refused while it runs')

  render(h(NewSessionButton, { loading: false, onClick: () => {}, label: 'New session' }), root)
  await Promise.resolve()
  assert.equal(root.querySelector('.btn-spinner'), null, 'no spinner once the session exists')
  assert.ok(root.querySelector('svg'), 'the plus is back')
  assert.equal(root.querySelector('button').disabled, false, 'the button is clickable again')
})

// ── The global CSS the settings and the design system rely on ───────────────
// jsdom does not apply an external stylesheet, so these check the rules are
// present and shaped the way the components expect.

test('font size and density are applied globally, not only to the chat', () => {
  assert.match(css, /html\[data-font-size="small"\]\s*\{\s*font-size:/, 'small scales the root font size')
  assert.match(css, /html\[data-font-size="large"\]\s*\{\s*font-size:/, 'large scales the root font size')
  assert.ok(css.includes('html[data-density="compact"] .session-row'), 'density reaches the sidebar rows')
  assert.ok(css.includes('html[data-density="compact"] .term-screen'), 'density reaches the drawers')
})

test('every button carries the design system diagonal corners', () => {
  assert.match(css, /button:not\(\.auto-approve-pill\)[^{]*\{[^}]*clip-path:\s*polygon/, 'a global button rule cuts the corners')
})

test('the right-edge tabs stay above an open panel', () => {
  assert.match(css, /\.term-drawer\s*\{[^}]*z-index:\s*13/, 'a closed drawer is above the open one')
  assert.match(css, /\.term-drawer\.is-open\s*\{[^}]*z-index:\s*12/, 'the open drawer sits below the closed tabs')
})

test('each drawer window border uses its own colour', () => {
  assert.match(css, /\.term-panel\s*\{[^}]*border:\s*1px solid rgba\(var\(--tx-rgb\)/, 'the panel border is the drawer colour')
  assert.ok(!/\.term-panel[^{]*\{[^}]*border:\s*1px solid var\(--neon-cyan\)/.test(css), 'no fixed cyan border is forced on the panels')
})
