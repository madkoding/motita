import test from 'node:test'
import assert from 'node:assert/strict'
import { JSDOM } from 'jsdom'

const dom = new JSDOM('<!doctype html><html></html>', { url: 'http://localhost/' })
globalThis.document = dom.window.document
globalThis.localStorage = dom.window.localStorage
const m = await import('../settings.ts')

test('settings are applied and persisted', () => {
  assert.deepEqual(m.loadSettings(), m.DEFAULT_SETTINGS)
  m.saveSettings({ fontSize: 'small', density: 'compact' })
  assert.equal(document.documentElement.getAttribute('data-font-size'), 'small')
  assert.equal(document.documentElement.getAttribute('data-density'), 'compact')
  assert.deepEqual(JSON.parse(localStorage.getItem(m.SETTINGS_KEY)), { fontSize: 'small', density: 'compact' })
  document.documentElement.removeAttribute('data-density')
  m.initSettings()
  assert.equal(document.documentElement.getAttribute('data-density'), 'compact')
})

test('notifications: on by default, the switch is kept, and the state says what to show', () => {
  localStorage.clear()
  assert.equal(m.loadNotify(), true)
  m.saveNotify(false)
  assert.equal(m.loadNotify(), false)
  m.saveNotify(true)
  assert.equal(m.loadNotify(), true)
  assert.equal(m.notifyState(undefined, true), 'unsupported')
  assert.equal(m.notifyState('denied', true), 'blocked')
  assert.equal(m.notifyState('default', true), 'ask')
  assert.equal(m.notifyState('granted', true), 'on')
  assert.equal(m.notifyState('granted', false), 'off')
})
