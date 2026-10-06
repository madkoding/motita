// Pull request and CI of a session: the client of the gateway's /pr endpoints, and the pure rules
// that decide when the user is told something and when the agent is sent to fix the CI. No value
// imports: the node tests load this file as is.

export type Api = (path: string, init?: RequestInit) => Promise<Response>

export type CIState = 'pending' | 'success' | 'failure' | 'none'

export interface CICheck {
  name: string
  state: CIState
  url?: string
  detail?: string
}

export interface PRView {
  // 'none' while the branch has no open pull request, 'open' once it has.
  state: 'none' | 'open'
  branch: string
  pr?: { number: number; url: string; title?: string }
  ci?: { state: CIState; checks: CICheck[]; rev?: string }
}

// PRError carries the HTTP status and the machine code of a refusal (git_auth_required,
// pr_no_remote, pr_no_host, pr_exists).
export class PRError extends Error {
  status: number
  code?: string
  service?: string
  pr?: PRView['pr']
  constructor(message: string, status: number, code?: string, service?: string, pr?: PRView['pr']) {
    super(message)
    this.status = status
    this.code = code
    this.service = service
    this.pr = pr
  }
}

async function failure(res: Response): Promise<PRError> {
  const body = await res.json().catch(() => ({} as any))
  return new PRError(body.error || 'HTTP ' + res.status, res.status, body.code, body.service, body.pr)
}

const base = (sid: string) => '/v1/sessions/' + encodeURIComponent(sid) + '/pr'

export async function getPR(api: Api, sid: string): Promise<PRView> {
  const res = await api(base(sid))
  if (!res.ok) throw await failure(res)
  return res.json()
}

// fixCI hands the failing CI to the agent. started is false when the agent was busy and the
// message was queued behind its turn.
export async function fixCI(api: Api, sid: string): Promise<{ started: boolean }> {
  const res = await api(base(sid) + '/fix', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' })
  if (!res.ok) throw await failure(res)
  const body = await res.json().catch(() => ({} as any))
  return { started: body.started === true }
}

// ── The watch ───────────────────────────────────────────────────────────────

// A pull request on a host that reports its checks late answers "none" for a moment after it is
// opened; a repository with no CI answers "none" for ever. This many polls in a row say which.
export const NONE_LIMIT = 6

export interface Watch {
  // watching is on from the moment the user opens the pull request (or a poll sees the CI
  // running) until the CI passes: a session opened on a red CI from last week is not touched.
  watching: boolean
  noneCount: number
  passedKey?: string
  fixedKey?: string
}

export const idleWatch: Watch = { watching: false, noneCount: 0 }

// armWatch is what opening a pull request does: from now on its CI is followed.
export const armWatch = (w: Watch): Watch => ({ ...w, watching: true, noneCount: 0 })

export type Verdict = 'passed' | 'failed' | 'no-ci' | null

// ciKey identifies one run of the CI, so a failure is answered once per push and not once per
// poll. A host that gives no revision is told apart by which jobs failed.
export function ciKey(v: PRView): string {
  const ci = v.ci
  if (!ci) return ''
  if (ci.rev) return ci.rev
  return ci.checks.filter(c => c.state === 'failure').map(c => c.name).sort().join(',')
}

// stepWatch folds one answer of the gateway into the watch and says what, if anything, the user
// must be told: the CI passed (they can merge), failed (the agent is sent to fix it), or there is
// no CI to wait for.
export function stepWatch(w: Watch, v: PRView): { watch: Watch; verdict: Verdict } {
  if (v.state !== 'open' || !v.ci) return { watch: w, verdict: null }
  const key = ciKey(v)
  switch (v.ci.state) {
    case 'pending':
      return { watch: { ...w, watching: true, noneCount: 0 }, verdict: null }
    case 'success':
      if (!w.watching || w.passedKey === key) return { watch: w, verdict: null }
      return { watch: { ...w, watching: false, noneCount: 0, passedKey: key }, verdict: 'passed' }
    case 'failure':
      if (!w.watching || w.fixedKey === key) return { watch: w, verdict: null }
      return { watch: { ...w, noneCount: 0, fixedKey: key }, verdict: 'failed' }
    default: {
      if (!w.watching) return { watch: w, verdict: null }
      const noneCount = w.noneCount + 1
      if (noneCount >= NONE_LIMIT) return { watch: { ...w, watching: false, noneCount: 0 }, verdict: 'no-ci' }
      return { watch: { ...w, noneCount }, verdict: null }
    }
  }
}

// shouldPoll: the CI is worth asking about while it is being followed or is still running.
export function shouldPoll(w: Watch, v: PRView | null): boolean {
  if (w.watching) return true
  return v?.state === 'open' && v.ci?.state === 'pending'
}

export function ciSummary(v: PRView): { passed: number; total: number } {
  const checks = v.ci?.checks ?? []
  return { passed: checks.filter(c => c.state === 'success').length, total: checks.length }
}
