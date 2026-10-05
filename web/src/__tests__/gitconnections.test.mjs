// The git connections modal: lists providers (no the removed provider), and connecting happens inside it.
import test from 'node:test'
import assert from 'node:assert/strict'
import { JSDOM } from 'jsdom'
import { build } from 'esbuild'
import { mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join, dirname } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'

const dom = new JSDOM('<!doctype html><html><body></body></html>', { url: 'http://localhost/' })
globalThis.document = dom.window.document
globalThis.window = dom.window
globalThis.localStorage = dom.window.localStorage
globalThis.requestAnimationFrame = (cb) => setTimeout(() => cb(Date.now()), 0)
globalThis.cancelAnimationFrame = (id) => clearTimeout(id)

const here = dirname(fileURLToPath(import.meta.url))
const out = join(mkdtempSync(join(tmpdir(), 'gcm-')), 'bundle.mjs')
await build({
  stdin: {
    contents: "export { GitConnectionsModal } from './GitConnectionsModal.tsx'; export { h, render } from 'preact'",
    resolveDir: join(here, '..'),
    loader: 'ts',
  },
  outfile: out,
  bundle: true,
  format: 'esm',
  platform: 'node',
  jsx: 'automatic',
  jsxImportSource: 'preact',
  logLevel: 'silent',
})
const { GitConnectionsModal, h, render } = await import(pathToFileURL(out).href)

const accounts = [
  { id: 'github', name: 'GitHub', kind: 'github', host: 'github.com', connected: false },
  { id: 'gitlab', name: 'GitLab', kind: 'gitlab', host: 'gitlab.com', connected: true, username: 'octo' },
  { id: 'bitbucket', name: 'Bitbucket', kind: 'bitbucket', host: 'bitbucket.org', connected: false },
]
const full = accounts.map((a) => ({ ...a, methods: ['token'], oauth_ready: false }))
const api = async () => new Response(JSON.stringify({ accounts: full }), { status: 200, headers: { 'content-type': 'application/json' } })
const wait = () => new Promise((r) => setTimeout(r, 50))

test('the modal opens with the providers, none of them the removed provider', async () => {
  const root = document.createElement('div')
  document.body.appendChild(root)
  render(h(GitConnectionsModal, { api, onNotice: () => {}, onClose: () => {} }), root)
  await wait()
  assert.ok(root.querySelector('[data-testid="git-connections-modal"]'), 'the modal is rendered')
  const ids = [...root.querySelectorAll('[data-provider]')].map((e) => e.getAttribute('data-provider'))
  assert.ok(ids.includes('github') && ids.includes('gitlab'))
  assert.ok(!new RegExp('code' + 'berg', 'i').test(root.textContent) && !ids.some((i) => new RegExp('code' + 'berg', 'i').test(i)))
  render(null, root)
})

test('connecting opens the flow inside the modal, without navigating', async () => {
  const root = document.createElement('div')
  document.body.appendChild(root)
  render(h(GitConnectionsModal, { api, onNotice: () => {}, onClose: () => {} }), root)
  await wait()
  const before = window.location.href
  const btn = root.querySelector('[data-provider="github"] button')
  assert.ok(btn, 'a connect button exists')
  btn.click()
  await wait()
  assert.ok(root.querySelector('[data-testid="git-connections-modal"]'), 'the list stays open')
  assert.ok(root.querySelectorAll('[role="dialog"]').length >= 2, 'the connect flow opened on top of it')
  assert.equal(window.location.href, before, 'no navigation')
  render(null, root)
})
