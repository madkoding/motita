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
  // Whether the host would accept a merge now; asked once the CI has passed.
  merge?: { code: 'ok' | 'blocked' | 'conflict' | 'draft' | 'behind' | 'unknown' }
  // What the gateway is doing about the CI; absent when nothing.
  watch?: PRWatch
}

// mergeRefused: the host would turn a merge down for a reason the user can see and fix.
export function mergeRefused(v: PRView | null | undefined): boolean {
  const c = v?.merge?.code
  return c === 'blocked' || c === 'conflict' || c === 'draft'
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

// retryFix sends the agent to fix the CI now and has the gateway follow it again from the start:
// what "Try again" does once the gateway gave up. started is false when the agent was busy and the
// message was queued behind its turn.
export async function retryFix(api: Api, sid: string): Promise<{ started: boolean }> {
  const res = await api(base(sid) + '/fix', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' })
  if (!res.ok) throw await failure(res)
  const body = await res.json().catch(() => ({} as any))
  return { started: body.started === true }
}

// mergePR merges the pull request. The gateway refuses unless its CI has passed.
export async function mergePR(api: Api, sid: string): Promise<void> {
  const res = await api(base(sid) + '/merge', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' })
  if (!res.ok) throw await failure(res)
}

// ── What the gateway is doing about the CI ──────────────────────────────────

// The gateway follows the CI of a pull request by itself and reports it in the session list; this
// side only decides what is worth a toast.
export type WatchStatus = 'following' | 'fixing' | 'passed' | 'gave_up' | 'no_ci' | 'merged' | 'base_red'

export interface PRWatch {
  status: WatchStatus
  attempts: number
  max: number
  number?: number
}

export type Notice = 'fixing' | 'passed' | 'gave_up' | 'no_ci' | 'merged' | 'base_red' | null

// prNotice says what the user is to be told when the watch of a session moves from prev to cur.
// prev is undefined the first time the session is seen: what happened before the tab looked is not
// news, and a toast for it would be noise.
export function prNotice(prev: PRWatch | null | undefined, cur: PRWatch | null | undefined): Notice {
  if (prev === undefined || !cur) return null
  if (cur.status === 'fixing') {
    return prev?.status !== 'fixing' || cur.attempts > prev.attempts ? 'fixing' : null
  }
  if (cur.status === 'following') return null
  return prev?.status === cur.status ? null : cur.status
}

// shouldPoll: the bar asks the gateway about the CI only while the gateway is following it.
export function shouldPoll(w: PRWatch | null | undefined): boolean {
  return w?.status === 'following' || w?.status === 'fixing'
}

export function ciSummary(v: PRView): { passed: number; total: number } {
  const checks = v.ci?.checks ?? []
  return { passed: checks.filter(c => c.state === 'success').length, total: checks.length }
}
