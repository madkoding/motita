// Tests for the git connection logic: the login flow state machine and its polling, repo-name
// derivation, list merging, and that every string the new screens use has a Spanish translation.
// Run with: node --test web/src/__tests__/
import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const src = (f) => readFileSync(join(here, '..', f), 'utf8')
const g = await import('../gitApi.ts')
const es = (await import('../i18n.es.ts')).default

const flow = { flow_id: 'f1', method: 'device', service: 'github', url: 'https://github.com/login/device', code: 'ABCD-1234' }

test('flow state machine: start, pending, connected', () => {
  let s = { phase: 'idle' }
  s = g.reduceFlow(s, { type: 'start' })
  assert.equal(s.phase, 'starting')
  s = g.reduceFlow(s, { type: 'started', flow })
  assert.equal(s.phase, 'pending')
  assert.equal(g.reduceFlow(s, { type: 'poll', status: { status: 'pending' } }), s)
  s = g.reduceFlow(s, { type: 'poll', status: { status: 'connected', account: 'octo' } })
  assert.deepEqual(s, { phase: 'connected', account: 'octo', service: 'github' })
  // a late poll answer cannot undo a terminal state
  assert.equal(g.reduceFlow(s, { type: 'poll', status: { status: 'failed', error: 'x' } }), s)
})

test('flow state machine: failure and reset', () => {
  let s = g.reduceFlow({ phase: 'pending', flow }, { type: 'poll', status: { status: 'failed', error: 'denied' } })
  assert.deepEqual(s, { phase: 'failed', error: 'denied' })
  s = g.reduceFlow({ phase: 'pending', flow }, { type: 'poll', status: { status: 'failed' } })
  assert.ok(s.error.length > 0)
  assert.deepEqual(g.reduceFlow(s, { type: 'reset' }), { phase: 'idle' })
})

test('pollFlow stops on a terminal state', async () => {
  const answers = [{ status: 'pending' }, { status: 'pending' }, { status: 'connected', account: 'octo' }]
  let calls = 0
  const r = await g.pollFlow({ get: async () => answers[calls++], sleep: async () => {}, cancelled: () => false })
  assert.equal(r.status, 'connected')
  assert.equal(calls, 3)
})

test('pollFlow stops when cancelled and survives a few errors', async () => {
  let stop = false, n = 0
  const r = await g.pollFlow({ get: async () => { n++; if (n === 2) stop = true; return { status: 'pending' } }, sleep: async () => {}, cancelled: () => stop })
  assert.equal(r, null)
  let k = 0
  const ok = await g.pollFlow({ get: async () => { if (k++ < 2) throw new Error('net'); return { status: 'failed', error: 'no' } }, sleep: async () => {}, cancelled: () => false })
  assert.equal(ok.error, 'no')
})

test('pollFlow gives up after repeated errors and on an unknown flow', async () => {
  const r = await g.pollFlow({ get: async () => { throw new Error('down') }, sleep: async () => {}, cancelled: () => false, maxErrors: 3 })
  assert.equal(r.status, 'failed')
  assert.equal(r.error, 'down')
  const gone = await g.pollFlow({ get: async () => { throw new g.GitError('x', 404) }, sleep: async () => {}, cancelled: () => false })
  assert.equal(gone.status, 'failed')
})

test('repoNameFromUrl derives the folder name', () => {
  assert.equal(g.repoNameFromUrl('https://github.com/user/repo.git'), 'repo')
  assert.equal(g.repoNameFromUrl('git@github.com:user/repo.git'), 'repo')
  assert.equal(g.repoNameFromUrl('https://gitlab.com/group/sub/repo/'), 'repo')
  assert.equal(g.repoNameFromUrl('https://git.corp.io/a/b.c.git'), 'b.c')
  assert.equal(g.repoNameFromUrl(''), '')
  assert.equal(g.repoShortName({ full_name: 'o/name', clone_url: '' }), 'name')
})

test('mergeRepos appends without duplicates; connectedAccounts filters', () => {
  const a = { full_name: 'o/a', clone_url: 'u/a' }, b = { full_name: 'o/b', clone_url: 'u/b' }
  assert.deepEqual(g.mergeRepos([a], [a, b]), [a, b])
  assert.deepEqual(g.connectedAccounts([{ id: 'x', connected: true }, { id: 'y', connected: false }]).map(x => x.id), ['x'])
})

test('primaryMethod prefers oauth only when the gateway is ready', () => {
  const base = { id: 'github', methods: ['device', 'token'], oauth_ready: true }
  assert.equal(g.primaryMethod(base), 'device')
  assert.equal(g.primaryMethod({ ...base, oauth_ready: false }), 'token')
  assert.equal(g.primaryMethod({ ...base, methods: ['token'] }), 'token')
  assert.equal(g.primaryMethod({ ...base, methods: ['code', 'token'] }), 'code')
})

test('relativeTime buckets', () => {
  const now = Date.parse('2026-01-01T00:00:00Z')
  const at = (s) => new Date(now - s * 1000).toISOString()
  assert.equal(g.relativeTime(at(5), now).unit, 'now')
  assert.deepEqual(g.relativeTime(at(300), now), { unit: 'minute', n: 5 })
  assert.deepEqual(g.relativeTime(at(7200), now), { unit: 'hour', n: 2 })
  assert.deepEqual(g.relativeTime(at(86400 * 3), now), { unit: 'day', n: 3 })
  assert.deepEqual(g.relativeTime(at(86400 * 90), now), { unit: 'month', n: 3 })
  assert.deepEqual(g.relativeTime(at(86400 * 800), now), { unit: 'year', n: 2 })
  assert.equal(g.relativeTime('garbage', now), null)
  assert.equal(g.relativeTime(undefined, now), null)
})

test('api calls hit the documented endpoints', async () => {
  const seen = []
  const api = async (path, init) => {
    seen.push([init?.method || 'GET', path, init?.body])
    if (path.startsWith('/v1/git/connect')) return new Response(JSON.stringify(flow), { status: 202 })
    if (path.startsWith('/v1/git/repos')) return new Response(JSON.stringify({ code: 'git_auth_required', service: 'github' }), { status: 409 })
    return new Response(null, { status: 204 })
  }
  const r = await g.connect(api, { service: 'github', method: 'device' })
  assert.equal(r.type, 'flow')
  await g.cancelFlow(api, 'f 1')
  await g.disconnect(api, 'gitlab@git.corp.io')
  await assert.rejects(g.listRepos(api, 'github', ' hi ', 2), (e) => e.authRequired && e.service === 'github')
  assert.deepEqual(seen.map(s => s[0] + ' ' + s[1]), [
    'POST /v1/git/connect', 'DELETE /v1/git/flows/f%201', 'DELETE /v1/git/accounts/gitlab%40git.corp.io', 'GET /v1/git/repos?service=github&page=2&q=hi',
  ])
})

test('every string in the git screens has a Spanish translation', () => {
  const keys = new Set()
  for (const f of ['GitConnect.tsx', 'RepoPicker.tsx', 'Settings.tsx']) {
    for (const m of src(f).matchAll(/\bt[fc]?\(\s*'((?:[^'\\]|\\.)*)'/g)) keys.add(m[1].replace(/\\'/g, "'"))
  }
  for (const k of ['Choose from my repositories', 'Connect GitHub, GitLab or Bitbucket to pick one of your repositories', 'The login expired. Try again.', 'The host did not authorize the login.']) keys.add(k)
  assert.ok(keys.size > 40)
  const missing = [...keys].filter(k => !es[k])
  assert.deepEqual(missing, [])
  // placeholders survive translation
  for (const k of keys) {
    const ph = (s) => (s.match(/\{\w+\}/g) || []).sort().join()
    assert.equal(ph(es[k]), ph(k), k)
  }
})
