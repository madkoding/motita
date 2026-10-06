// Tests for the pull request watch: when the user is told the CI passed or failed, that a failure
// is answered once per push, and that a session nobody opened a pull request from is left alone.
// Run with: node --test web/src/__tests__/
import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const p = await import('../prApi.ts')
const es = (await import('../i18n.es.ts')).default

const view = (state, rev, checks = []) => ({
  state: 'open', branch: 'motita/s1', pr: { number: 7, url: 'https://x/7' }, ci: { state, rev, checks },
})

test('a CI that passes after being followed says so once', () => {
  let w = p.armWatch(p.idleWatch)
  let r = p.stepWatch(w, view('pending', 'a'))
  assert.equal(r.verdict, null)
  r = p.stepWatch(r.watch, view('success', 'a'))
  assert.equal(r.verdict, 'passed')
  assert.equal(r.watch.watching, false)
  assert.equal(p.stepWatch(r.watch, view('success', 'a')).verdict, null, 'the next poll is quiet')
})

test('a failure is answered once per push and the watch stays on', () => {
  let w = p.armWatch(p.idleWatch)
  let r = p.stepWatch(w, view('failure', 'a'))
  assert.equal(r.verdict, 'failed')
  assert.equal(r.watch.watching, true)
  r = p.stepWatch(r.watch, view('failure', 'a'))
  assert.equal(r.verdict, null, 'same push, already handed to the agent')
  r = p.stepWatch(r.watch, view('pending', 'b'))
  r = p.stepWatch(r.watch, view('failure', 'b'))
  assert.equal(r.verdict, 'failed', 'a new push that fails again goes round again')
  r = p.stepWatch(r.watch, view('success', 'c'))
  assert.equal(r.verdict, 'passed')
})

test('a session nobody is following is never touched', () => {
  assert.equal(p.stepWatch(p.idleWatch, view('failure', 'a')).verdict, null)
  assert.equal(p.stepWatch(p.idleWatch, view('success', 'a')).verdict, null)
  assert.equal(p.stepWatch(p.armWatch(p.idleWatch), { state: 'none', branch: 'b' }).verdict, null)
})

test('a running CI turns the watch on by itself', () => {
  const r = p.stepWatch(p.idleWatch, view('pending', 'a'))
  assert.equal(r.watch.watching, true)
  assert.equal(p.stepWatch(r.watch, view('failure', 'a')).verdict, 'failed')
})

test('no CI at all gives up after a few polls instead of waiting for ever', () => {
  let r = { watch: p.armWatch(p.idleWatch), verdict: null }
  for (let i = 1; i < p.NONE_LIMIT; i++) {
    r = p.stepWatch(r.watch, view('none', ''))
    assert.equal(r.verdict, null)
  }
  r = p.stepWatch(r.watch, view('none', ''))
  assert.equal(r.verdict, 'no-ci')
  assert.equal(r.watch.watching, false)
})

test('without a revision a failure is told apart by its jobs', () => {
  const a = view('failure', '', [{ name: 'test', state: 'failure' }, { name: 'lint', state: 'success' }])
  const b = view('failure', '', [{ name: 'lint', state: 'failure' }])
  assert.notEqual(p.ciKey(a), p.ciKey(b))
})

test('polling goes on while the CI is followed or running, not otherwise', () => {
  assert.equal(p.shouldPoll(p.idleWatch, null), false)
  assert.equal(p.shouldPoll(p.armWatch(p.idleWatch), null), true)
  assert.equal(p.shouldPoll(p.idleWatch, view('pending', 'a')), true)
  assert.equal(p.shouldPoll(p.idleWatch, view('success', 'a')), false)
})

test('ciSummary counts the jobs that passed', () => {
  const v = view('pending', 'a', [{ name: 'a', state: 'success' }, { name: 'b', state: 'pending' }])
  assert.deepEqual(p.ciSummary(v), { passed: 1, total: 2 })
})

test('fixCI and getPR speak to the gateway and carry its refusals', async () => {
  const calls = []
  const api = async (path, init) => {
    calls.push([path, init?.method])
    if (path.endsWith('/fix')) return { ok: true, json: async () => ({ started: false, queued: true }) }
    return { ok: false, status: 409, json: async () => ({ error: 'not connected', code: 'git_auth_required', service: 'github' }) }
  }
  assert.deepEqual(await p.fixCI(api, 's 1'), { started: false })
  assert.deepEqual(calls[0], ['/v1/sessions/s%201/pr/fix', 'POST'])
  await assert.rejects(p.getPR(api, 's1'), e => e.status === 409 && e.code === 'git_auth_required' && e.service === 'github')
})

test('every string of the pull request bar has a Spanish translation', () => {
  const app = readFileSync(join(here, '..', 'App.tsx'), 'utf8')
  // The bar is two delimited stretches of App.tsx: the logic and the markup.
  let block = ''
  for (let at = app.indexOf('── Pull request bar'); at >= 0; at = app.indexOf('── Pull request bar', at + 1)) {
    block += app.slice(at, app.indexOf('── end of the pull request bar', at))
  }
  assert.ok(block.length > 1000, 'the bar is delimited in App.tsx')
  const keys = [...block.matchAll(/\bt[f]?\('((?:[^'\\]|\\.)*)'/g)].map(m => m[1].replace(/\\'/g, "'"))
  assert.ok(keys.length > 5)
  for (const k of keys) assert.ok(es[k], `missing Spanish for: ${k}`)
})
