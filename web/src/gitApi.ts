// Client for the gateway's git-host connection API, plus the pure logic around it (flow state
// machine, polling, repo-name derivation). No value imports: the node tests load this file as is.

export type Api = (path: string, init?: RequestInit) => Promise<Response>

export type GitMethod = 'device' | 'code' | 'token'

export interface GitAccount {
  id: string
  name: string
  kind: string
  host: string
  connected: boolean
  username?: string
  methods: GitMethod[]
  oauth_ready: boolean
  token_url?: string
  setup_hint?: string
}

export interface GitRepo {
  full_name: string
  description?: string
  clone_url: string
  web_url?: string
  private?: boolean
  default_branch?: string
  updated_at?: string
}

export interface ReposPage {
  repos: GitRepo[]
  more: boolean
  account?: string
}

export interface FlowStart {
  flow_id: string
  method: 'device' | 'code'
  service: string
  url: string
  code?: string
  expires_in?: number
  redirect_uri?: string
}

export interface FlowStatus {
  status: 'pending' | 'connected' | 'failed'
  service?: string
  account?: string
  error?: string
}

// GitError carries the HTTP status and, for a 409, the machine code and service.
export class GitError extends Error {
  status: number
  code?: string
  service?: string
  constructor(message: string, status: number, code?: string, service?: string) {
    super(message)
    this.status = status
    this.code = code
    this.service = service
  }
  get authRequired(): boolean {
    return this.status === 409 && this.code === 'git_auth_required'
  }
}

async function failure(res: Response): Promise<GitError> {
  const body = await res.json().catch(() => ({} as any))
  return new GitError(body.error || 'HTTP ' + res.status, res.status, body.code, body.service)
}

const JSON_HEADERS = { 'Content-Type': 'application/json' }

export async function listAccounts(api: Api): Promise<GitAccount[]> {
  const res = await api('/v1/git/accounts')
  if (!res.ok) throw await failure(res)
  const body = await res.json()
  return Array.isArray(body.accounts) ? body.accounts : []
}

export interface ConnectRequest {
  service?: string
  kind?: string
  host?: string
  method?: GitMethod
  token?: string
  username?: string
}

export type ConnectResult =
  | { type: 'connected'; account: string; service: string }
  | { type: 'flow'; flow: FlowStart }

export async function connect(api: Api, req: ConnectRequest): Promise<ConnectResult> {
  const res = await api('/v1/git/connect', { method: 'POST', headers: JSON_HEADERS, body: JSON.stringify(req) })
  if (!res.ok) throw await failure(res)
  const body = await res.json()
  if (res.status === 202 || body.flow_id) return { type: 'flow', flow: body as FlowStart }
  return { type: 'connected', account: body.account || '', service: body.service || req.service || '' }
}

export async function flowStatus(api: Api, id: string): Promise<FlowStatus> {
  const res = await api('/v1/git/flows/' + encodeURIComponent(id))
  if (!res.ok) throw await failure(res)
  return res.json()
}

export async function pasteCode(api: Api, id: string, code: string): Promise<void> {
  const res = await api('/v1/git/flows/' + encodeURIComponent(id) + '/paste', { method: 'POST', headers: JSON_HEADERS, body: JSON.stringify({ code }) })
  if (!res.ok) throw await failure(res)
}

export async function cancelFlow(api: Api, id: string): Promise<void> {
  try { await api('/v1/git/flows/' + encodeURIComponent(id), { method: 'DELETE' }) } catch { /* best effort */ }
}

export async function disconnect(api: Api, id: string): Promise<void> {
  const res = await api('/v1/git/accounts/' + encodeURIComponent(id), { method: 'DELETE' })
  if (!res.ok && res.status !== 204) throw await failure(res)
}

export async function listRepos(api: Api, service: string, q: string, page: number): Promise<ReposPage> {
  const qs = new URLSearchParams({ service, page: String(page) })
  if (q.trim()) qs.set('q', q.trim())
  const res = await api('/v1/git/repos?' + qs.toString())
  if (!res.ok) throw await failure(res)
  const body = await res.json()
  return { repos: Array.isArray(body.repos) ? body.repos : [], more: !!body.more, account: body.account }
}

// ── Flow state machine ──────────────────────────────────────────────────────

export type FlowState =
  | { phase: 'idle' }
  | { phase: 'starting' }
  | { phase: 'pending'; flow: FlowStart }
  | { phase: 'connected'; account: string; service: string }
  | { phase: 'failed'; error: string }

export type FlowEvent =
  | { type: 'start' }
  | { type: 'started'; flow: FlowStart }
  | { type: 'connected'; account: string; service: string }
  | { type: 'failed'; error: string }
  | { type: 'poll'; status: FlowStatus }
  | { type: 'reset' }

export function reduceFlow(s: FlowState, e: FlowEvent): FlowState {
  switch (e.type) {
    case 'reset': return { phase: 'idle' }
    case 'start': return { phase: 'starting' }
    case 'started': return { phase: 'pending', flow: e.flow }
    case 'connected': return { phase: 'connected', account: e.account, service: e.service }
    case 'failed': return { phase: 'failed', error: e.error }
    case 'poll': {
      if (s.phase !== 'pending') return s // a late answer after a terminal state changes nothing
      if (e.status.status === 'connected') return { phase: 'connected', account: e.status.account || '', service: e.status.service || s.flow.service }
      if (e.status.status === 'failed') return { phase: 'failed', error: e.status.error || 'The host did not authorize the login.' }
      return s
    }
  }
}

export interface PollOptions {
  get: () => Promise<FlowStatus>
  sleep: (ms: number) => Promise<void>
  cancelled: () => boolean
  intervalMs?: number
  maxErrors?: number
}

// pollFlow asks for the flow's status until it is terminal or cancelled. A few network errors in a
// row are tolerated (a flaky connection must not kill a login the user is completing in a browser).
// Returns the terminal status, or null when cancelled.
export async function pollFlow(o: PollOptions): Promise<FlowStatus | null> {
  const max = o.maxErrors ?? 5
  let errors = 0
  while (!o.cancelled()) {
    try {
      const st = await o.get()
      errors = 0
      if (st.status !== 'pending') return o.cancelled() ? null : st
    } catch (e) {
      if (o.cancelled()) return null
      if (e instanceof GitError && (e.status === 404 || e.status === 410)) return { status: 'failed', error: 'The login expired. Try again.' }
      if (++errors >= max) return { status: 'failed', error: e instanceof Error ? e.message : String(e) }
    }
    await o.sleep(o.intervalMs ?? 2000)
  }
  return null
}

// ── Pure helpers ────────────────────────────────────────────────────────────

// repoNameFromUrl: https://host/o/repo.git, git@host:o/repo.git, https://host/group/sub/repo -> repo
export function repoNameFromUrl(url: string): string {
  const m = url.trim().replace(/\/+$/, '').match(/(?:\/|:)([^/:]+?)(?:\.git)?$/)
  return m ? m[1] : ''
}

export function repoShortName(r: GitRepo): string {
  return repoNameFromUrl(r.clone_url) || r.full_name.split('/').pop() || ''
}

export function connectedAccounts(accts: GitAccount[]): GitAccount[] {
  return accts.filter(a => a.connected)
}

// mergeRepos appends a page to the list without duplicating a repo the gateway repeated.
export function mergeRepos(have: GitRepo[], more: GitRepo[]): GitRepo[] {
  const seen = new Set(have.map(r => r.clone_url))
  return have.concat(more.filter(r => !seen.has(r.clone_url)))
}

// primaryMethod: one-click OAuth when the gateway can do it, otherwise the token.
export function primaryMethod(a: GitAccount): GitMethod {
  const oauth = a.methods.find(m => m === 'device' || m === 'code')
  return a.oauth_ready && oauth ? oauth : 'token'
}

export type RelTime = { unit: 'now' | 'minute' | 'hour' | 'day' | 'month' | 'year'; n: number }

export function relativeTime(iso: string | undefined, now: number = Date.now()): RelTime | null {
  if (!iso) return null
  const then = Date.parse(iso)
  if (isNaN(then)) return null
  const s = Math.max(0, Math.floor((now - then) / 1000))
  if (s < 60) return { unit: 'now', n: 0 }
  if (s < 3600) return { unit: 'minute', n: Math.floor(s / 60) }
  if (s < 86400) return { unit: 'hour', n: Math.floor(s / 3600) }
  if (s < 86400 * 30) return { unit: 'day', n: Math.floor(s / 86400) }
  if (s < 86400 * 365) return { unit: 'month', n: Math.floor(s / (86400 * 30)) }
  return { unit: 'year', n: Math.floor(s / (86400 * 365)) }
}
