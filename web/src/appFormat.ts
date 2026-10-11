// How the web interface words and shapes what it shows: counts, times, ids, titles, the
// labels of each state. Pure functions of their arguments (and of the language).
import { t, tf, plural } from './i18n'
import { choose } from './choose'
import type { PRWatch } from './prApi'
import type { AgentInfo, AgentTokens, ConfigView, ProviderInfo, ScheduledTask, SessionInfo, SkillInfo, StepView } from './appTypes'
import { DAY_MS, DAY_S, HOUR_S, NEW_SESSION_TITLE, SHORT_ID_LEN } from './appConst'
import { durationOf } from './appParse'

const THOUSAND = 1000
const MILLION = 1_000_000
const TEN_THOUSAND = 10_000
const MINUTE_S = 60

export function tokenTotal(tok?: AgentTokens): number {
  if (!tok) { return 0 }
  return (tok.input || 0) + (tok.output || 0) + (tok.cache_read || 0) + (tok.cache_write || 0)
}

// formatTokens humanises a count the way a reader scans it: 950, 12.3k, 1.2M.
export function formatTokens(n: number): string {
  if (n < THOUSAND) { return String(n) }
  if (n < MILLION) { return (n / THOUSAND).toFixed(choose(n < TEN_THOUSAND, 1, 0)).replace(/\.0$/, '') + 'k' }
  return (n / MILLION).toFixed(1).replace(/\.0$/, '') + 'M'
}

export function tokenBreakdown(tok: AgentTokens): string {
  const parts = [tf('in {n}', { n: formatTokens(tok.input || 0) }), tf('out {n}', { n: formatTokens(tok.output || 0) })]
  if (tok.cache_read) { parts.push(tf('cache read {n}', { n: formatTokens(tok.cache_read) })) }
  if (tok.cache_write) { parts.push(tf('cache write {n}', { n: formatTokens(tok.cache_write) })) }
  return parts.join(' · ')
}

// sumTokens adds up what every agent of a run spent.
export function sumTokens(agents: AgentInfo[]): AgentTokens {
  return agents.reduce<AgentTokens>((acc, a) => ({
    input: acc.input + (a.tokens?.input || 0),
    output: acc.output + (a.tokens?.output || 0),
    cache_read: (acc.cache_read || 0) + (a.tokens?.cache_read || 0),
    cache_write: (acc.cache_write || 0) + (a.tokens?.cache_write || 0),
  }), { input: 0, output: 0, cache_read: 0, cache_write: 0 })
}

// formatElapsed: 8s, 3m 05s, 1h 02m.
export function formatElapsed(ms: number): string {
  const s = Math.max(0, Math.floor(ms / THOUSAND))
  if (s < MINUTE_S) { return s + 's' }
  const m = Math.floor(s / MINUTE_S)
  if (m < MINUTE_S) { return m + 'm ' + String(s % MINUTE_S).padStart(2, '0') + 's' }
  return Math.floor(m / MINUTE_S) + 'h ' + String(m % MINUTE_S).padStart(2, '0') + 'm'
}

// agentElapsed counts a running agent's clock from its start, so it ticks between snapshots; a
// finished one is the span it ran, or what the gateway measured when that is all there is.
export function agentElapsed(a: AgentInfo, now: number): number {
  const start = Date.parse(a.started)
  if (Number.isNaN(start)) { return a.elapsed_ms || 0 }
  if (a.state === 'running') { return Math.max(a.elapsed_ms || 0, now - start) }
  const end = Date.parse(a.finished || '')
  if (Number.isNaN(end)) { return a.elapsed_ms || 0 }
  return end - start
}

// sortAgents puts the main agent first, then the background agents in the order they started.
export function sortAgents(list: unknown): AgentInfo[] {
  if (!Array.isArray(list)) { return [] }
  return (list as AgentInfo[]).filter(a => a && typeof a.id === 'string').slice().sort((a, b) => {
    if (!!a.parent !== !!b.parent) { return choose(a.parent, 1, -1) }
    return (Date.parse(a.started) || 0) - (Date.parse(b.started) || 0)
  })
}

// agentMark is the mark an agent's row starts with: none while it runs, then how it ended.
export function agentMark(state: string): string | null {
  if (state === 'running') { return null }
  if (state === 'passed') { return '✓' }
  return choose(state === 'failed', '✗', '⊘')
}

// daysUntil is how many whole days remain until a moment, rounded up: "deleted in 1 days" is
// shown for the last day, never "0".
export function daysUntil(iso: string): number {
  return Math.ceil((new Date(iso).getTime() - Date.now()) / DAY_MS)
}

// shortID abbreviates a generated id the way git abbreviates a sha: the first
// seven characters. A full id is 25 characters and would be the widest thing in
// the sidebar while telling the reader nothing the first seven do not.
export function shortID(id: string): string {
  if (id.length > SHORT_ID_LEN) { return id.slice(0, SHORT_ID_LEN) }
  return id
}

// shortBranch keeps a branch name readable when it is one this program
// generated. `motita/<id>` is the session's OWN branch and the id is the only
// part that varies, so it is shown as `motita/<first 7>`; a branch the user
// chose is short and is shown exactly as it is.
export function shortBranch(b: string): string {
  const m = /^motita\/(.+)$/.exec(b)
  if (m && m[1].length > SHORT_ID_LEN) { return 'motita/' + shortID(m[1]) }
  return b
}

// sessionWorktreeChip decides whether a session's worktree needs saying at all.
//
// A session on its own branch IS in the worktree that branch names, so printing
// the id twice is one fact twice: `motita/s63bb92` beside `⌥ s63bb92`. The two
// differ in exactly one case - the user moved the session to another branch -
// and that is the case where the worktree is the only way to tell which session
// the branch belongs to.
export function sessionWorktreeChip(s: SessionInfo): string {
  if (!s.worktree || !s.branch) { return '' }
  if (s.branch === 'motita/' + s.worktree) { return '' }
  return shortID(s.worktree)
}

// formatUpdated renders when a session was last used, as dd/mm/yyyy :: HH:mm:ss.
//
// Seconds and the full year are carried even though a sidebar usually shows
// something shorter, because the user asked for this exact format and a
// timestamp is a fact: two sessions opened a minute apart are told apart by the
// seconds, and the year is what keeps a session restored from a backup from
// reading as if it were touched today.
export function formatUpdated(iso?: string): string {
  if (!iso) { return '' }
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) { return '' }
  const p = (n: number) => String(n).padStart(2, '0')
  return `${p(d.getDate())}/${p(d.getMonth() + 1)}/${d.getFullYear()} :: ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}

// fullUpdated is the exact time, for the tooltip. The row carries the compact
// form because that is what fits; hovering is how a user asks "when, exactly",
// and the answer must not be the truncated one they can already see.
export function fullUpdated(iso?: string): string {
  if (!iso) { return '' }
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) { return '' }
  return d.toLocaleString()
}

// changesTitle is what a change count means, spelled out: a bare number in a
// badge is read as fact, so the tooltip says what it counted.
export function changesTitle(n: number): string {
  return plural(n, '1 uncommitted change in this working tree', '{n} uncommitted changes in this working tree')
}

// The open conversation lives in the URL (?session=<id>) so a link, a reload or the
// back button returns to it. Query and not path: the gateway serves the page at one path.
export function sessionFromUrl(): string {
  try { return new URLSearchParams(window.location.search).get('session') || '' } catch { return '' }
}
export function writeSessionUrl(id: string, mode: 'push' | 'replace') {
  try {
    const u = new URL(window.location.href)
    if (u.searchParams.get('session') === id) { return }
    u.searchParams.set('session', id)
    const url = u.pathname + u.search + u.hash
    if (mode === 'push') { history.pushState(null, '', url) }
    else { history.replaceState(null, '', url) }
  } catch { /* ignore */ }
}

// ─── Scheduled tasks: reading a cadence in words, and a countdown ────────────
//
// `every` arrives from the gateway as a Go duration string (the DTO formats it),
// so the units are known: the tag reads "Every 24h", never a second, invented
// spelling of the same cadence.

// countdown is how long is left before the next run, as a chronometer:
// mm:ss under an hour, h:mm:ss under a day, days and hours beyond that. The
// seconds field is why this ticks at all - a countdown that only moved once a
// minute would look frozen on the run that matters, the imminent one. Past a day
// the seconds are dropped because they would be noise, and the panel re-renders
// on the same interval either way.
function pad(n: number): string {
  if (n < 10) { return '0' + n }
  return String(n)
}

function countdown(fromMs: number, nowMs: number): { text: string; past: boolean } {
  const delta = Math.round((fromMs - nowMs) / THOUSAND)
  const past = delta < 0
  const s = Math.abs(delta)
  let text: string
  if (s < HOUR_S) { text = Math.floor(s / MINUTE_S) + ':' + pad(s % MINUTE_S) }
  else if (s < DAY_S) { text = Math.floor(s / HOUR_S) + ':' + pad(Math.floor((s % HOUR_S) / MINUTE_S)) + ':' + pad(s % MINUTE_S) }
  else { text = Math.floor(s / DAY_S) + 'd ' + Math.floor((s % DAY_S) / HOUR_S) + 'h' }
  return { text, past }
}

// nextClockSkew is the skew assumed when nothing can be derived from the tasks:
// zero, so the countdown is exactly what the gateway's timestamps say. It only
// applies to a task set that is empty or unreadable, where the correction has
// nothing to correct.
const nextClockSkew = 0

// clockSkewOf is the gap between this browser's clock and the gateway's, as the tasks show it.
//
// The countdown is drawn between the BROWSER's clock and next_run, which the
// GATEWAY computed. When the two machines disagree the countdown lies by the
// difference ("4h" on a task that fires in a minute), and the gateway is the
// one holding the schedule store: this is the smallest correction that keeps
// the number honest.
export function clockSkewOf(tasks: ScheduledTask[]): number {
  let min = nextClockSkew
  for (const task of tasks) {
    const ts = Date.parse(task.next_run)
    if (Number.isNaN(ts)) { continue }
    min = Math.min(min, Date.now() - (ts - durationOf(task.every).ms))
  }
  return min
}

// taskCountdown answers how long until this task fires, as the panel shows it.
// A paused task counts down to nothing: its next_run is a projection, and a
// ticker over a task that will not fire would be a lie with a pulse. An overdue
// one says so, because "0s" is not the same fact as "it should have gone off".
export function taskCountdown(task: ScheduledTask, nowMs: number, skew: number) {
  if (!task.enabled) { return null }
  const ts = Date.parse(task.next_run)
  if (Number.isNaN(ts)) { return null }
  const c = countdown(ts - skew, nowMs)
  return { text: c.text, overdue: c.past }
}

// reasoningOf is the level a configuration is running: "off" when reasoning is disabled,
// whatever level string is stored beside it.
export function reasoningOf(c: ConfigView | null): string {
  if (!c) { return 'medium' }
  if (c.reasoning_enabled) { return (c.reasoning || 'medium') }
  return 'off'
}

// sessionTitle is what a session row and the header call a conversation. A session nobody has
// named yet carries the gateway's English placeholder, which is shown in the reader's language.
export function sessionTitle(s: SessionInfo): string {
  if (!s.title) { return s.id }
  if (s.title === NEW_SESSION_TITLE) { return t(NEW_SESSION_TITLE) }
  return s.title
}

// modelOptions is the list the Model select offers for a DRAFT provider, built from
// the two catalogues at hand:
//
//   - `live` is what /model-list published, and it describes the SAVED provider, not
//     the draft. It is used only while the draft still points there, because a live
//     list of another provider's models is worse than no list at all.
//   - `all` is /providers, which carries every provider's own catalogue, so a draft
//     provider that has not been saved yet still gets a usable menu.
//
// The draft's current value is appended when neither source knows it, so a model in
// use is never silently dropped from the menu it is being displayed in.
export function modelOptions(draftProvider: string, draftModel: string, savedProvider: string, live: string[], all: ProviderInfo[]): string[] {
  const out: string[] = []
  const seen = new Set<string>()
  const add = (m: string) => {
    if (!m || seen.has(m)) { return }
    seen.add(m)
    out.push(m)
  }
  if (draftProvider === savedProvider) { live.forEach(add) }
  ;(all.find(p => p.id === draftProvider)?.models || []).forEach(add)
  add(draftModel)
  return out
}

// firstModelOf is the model a provider switch lands on: the first of the catalogue
// the provider published. It exists so a switch never leaves the model empty, which
// the server would read as "leave unchanged" and would therefore keep a model the new
// provider may not serve.
export function firstModelOf(provider: string, all: ProviderInfo[]): string {
  return all.find(p => p.id === provider)?.models?.[0] || ''
}

const STATUS_LABEL: Record<string, string> = { added: 'new', modified: 'edited', deleted: 'removed', renamed: 'moved' }

// statusLabel is a changed file's status as the reader sees it: translated when it is a known one.
export function statusLabel(status: string): string {
  if (STATUS_LABEL[status]) { return t(STATUS_LABEL[status]) }
  return status
}

// patchNamed returns the item with the patch applied when it carries that name, the item itself
// otherwise.
export function patchNamed<T extends { name: string }>(item: T, name: string, patch: Partial<T>): T {
  if (item.name !== name) { return item }
  return { ...item, ...patch }
}

// filterSkills keeps the skills whose name, title or summary contain the (already lowercased)
// query; an empty query keeps them all.
export function filterSkills(skills: SkillInfo[], q: string): SkillInfo[] {
  if (q === '') { return skills }
  return skills.filter(s =>
    s.name.toLowerCase().includes(q) ||
    s.title.toLowerCase().includes(q) ||
    s.summary.toLowerCase().includes(q))
}

// chatTitle is the header of the chat: the open session's title, or the app's name.
export function chatTitle(s: SessionInfo | null | undefined): string {
  if (!s) { return 'Motita' }
  return sessionTitle(s)
}

// lastExit is the exit of a turn's last step, which the live command line shows.
export function lastExit(steps: StepView[]): number | null | undefined {
  if (!steps.length) { return null }
  return steps[steps.length - 1].exit
}

// branchChoices is the list a branch selector offers: the known branches, with the current one
// first when it is not among them.
export function branchChoices(branches: string[], current: string): string[] {
  if (branches.includes(current)) { return branches }
  return [current, ...branches]
}

// previewKind says which answer of the deletion preview to draw: still loading, a checkout that
// could not be inspected, the changes that would be lost, a clean checkout, or nothing yet.
export function previewKind(loading: boolean, p: { count: number; inspection_failed?: boolean } | null): string {
  if (loading) { return 'loading' }
  if (p?.inspection_failed) { return 'failed' }
  if (!p) { return 'none' }
  if (p.count > 0) { return 'changes' }
  return 'clean'
}

// mergedOffer is what the pull request bar offers once the pull request merged: the session that
// was opened from the updated branch, a new session to continue in, or nothing.
export function mergedOffer(w: PRWatch | undefined, s: SessionInfo | undefined): string {
  if (w?.status !== 'merged') { return '' }
  if (w.next) { return 'open' }
  if (s?.merged && s.continuable) { return 'continue' }
  return ''
}

// toastIcon is the icon a toast carries: its type's, a download for an update, a check otherwise.
export function toastIcon(type: string, update = false, info: { update_available?: boolean } | null = null): string {
  if (type === 'error' || type === 'warning') { return type }
  if (info?.update_available && update) { return 'update' }
  return 'done'
}

// artifactShape is how the artifact viewer draws a file: a PDF or an image from its URL, or its
// body as Markdown, as a sandboxed page or as plain text.
export function artifactShape(v: { type: string; url?: string }): string {
  if (v.url && v.type.startsWith('application/pdf')) { return 'pdf' }
  if (v.url) { return 'image' }
  if (v.type.startsWith('text/markdown')) { return 'markdown' }
  if (v.type.startsWith('text/html')) { return 'html' }
  return 'text'
}

// tagGroupClass is the colour of a slash-command tag, by the group it belongs to.
export function tagGroupClass(group: string): string {
  if (group === 'mode') { return 'bg-accent/20 text-accent' }
  if (group === 'action') { return 'bg-[#f0a040]/20 text-[#f0a040]' }
  if (group === 'session') { return 'bg-[#40f0a0]/20 text-[#40f0a0]' }
  return 'bg-[#a0a0f0]/20 text-[#a0a0f0]'
}

// podmanNote says what running a new project with podman will do, given what the host has.
export function podmanNote(ready: boolean, compose: boolean): string {
  if (!ready) {
    return t('podman is installed but did not answer (podman info failed), so it may not work until it is set up.')
  }
  if (compose) { return t('The project will be started in containers with podman.') }
  return t('The project will be started in containers with podman. podman compose was not found, so compose files will not work until it is installed.')
}

// deleteWarning is what the delete confirmation says will be lost, for a session (the default
// one is only cleared), a skill or a project.
export function deleteWarning(type: string, id: string): string {
  if (type === 'session') {
    if (id === 'default') {
      return t('This is the default session. Deleting it will clear its history and reset its title, but the session itself will remain.')
    }
    return t('This conversation will be permanently deleted. This cannot be undone.')
  }
  if (type === 'skill') {
    return t('This skill will be permanently deleted, with its usage history. This cannot be undone. A built-in procedure cannot be deleted.')
  }
  return t('This project, all its sessions and its folder will be permanently deleted. This cannot be undone.')
}

// toastTone picks the value that goes with a toast's type: one for an error, one for a
// warning and one for anything else.
export function toastTone<T>(type: string, error: T, warning: T, other: T): T {
  if (type === 'error') { return error }
  if (type === 'warning') { return warning }
  return other
}

// rowMenuStyle places a row's action menu at its viewport position, when it has one.
export function rowMenuStyle(pos: { top: number; left: number } | null): { top: string; left: string } | undefined {
  if (!pos) { return undefined }
  return { top: `${pos.top}px`, left: `${pos.left}px` }
}

// skillHeading is the skill library's title: the open skill's, or the library's own.
export function skillHeading(skill: SkillInfo | null): string {
  if (!skill) { return t('Skill library') }
  return skill.title || skill.name
}

// prWatchKeyOf is what changes when the pull request watch moves on: its status and attempts.
export function prWatchKeyOf(w: PRWatch | undefined): string {
  if (!w) { return '' }
  return w.status + ':' + w.attempts
}

// ciFailedLabel is the pull request bar's text for a red CI, saying what Motita does about it.
export function ciFailedLabel(w: PRWatch | undefined): string {
  if (w?.status === 'gave_up') { return tf('CI failed — Motita gave up after {n} attempts', { n: String(w.attempts) }) }
  if (w?.status === 'fixing') {
    return tf('CI failed — Motita is fixing it ({n}/{max})', { n: String(w.attempts), max: String(w.max) })
  }
  return t('CI failed')
}

// mergeBlockedLabel says why the host would not merge a pull request whose CI passed.
export function mergeBlockedLabel(code: string): string {
  if (code === 'blocked') { return t('Waiting for approvals or required checks') }
  if (code === 'conflict') { return t('Conflicts with the base branch') }
  if (code === 'draft') { return t('Still a draft') }
  return t('Out of date with the base branch')
}
