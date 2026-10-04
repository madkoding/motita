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
