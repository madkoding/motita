// What each event of a run's stream does to the conversation on screen.
import { tf } from './i18n'
import { choose } from './choose'
import { hasReport, type TaskReport } from './Report'
import { sortAgents } from './appFormat'
import { classifyProgress, commandOf, splitOutput } from './appParse'
import { keyed } from './useRunState'
import type { RunState } from './useRunState'
import type { ChangeReport, PendingApproval, ShellEntry, StepView } from './appTypes'
import { AGENTS_PREFIX, SHELL_LOG_MAX, STEPS_MAX } from './appConst'

// StreamPayload is the data of one event, with every field an event of the stream can carry.
export interface StreamPayload {
  turn?: number
  dropped?: number
  pending_approval?: PendingApproval
  agents?: unknown
  text?: string
  result?: string
  changes?: ChangeReport
  artifacts?: { name: string; type: string; size: number }[]
  report?: TaskReport
  error?: string
}

// StreamContext is what the handlers act on.
export interface StreamContext {
  rs: RunState
  finish: () => void
}

// markTurn gives the last input of the user that has no checkpoint yet the one the run became.
function markTurn(rs: RunState, turn: number) {
  rs.setMessages(prev => {
    for (let i = prev.length - 1; i >= 0; i--) {
      if (prev[i].role !== 'user') { continue }
      if (prev[i].turn != null) { break }
      const next = prev.slice()
      next[i] = { ...prev[i], turn }
      return next
    }
    return prev
  })
}

// onAttached: which checkpoint this run is. A run that is replayed from the start rebuilds its
// steps from that replay, so what was loaded for it is dropped first: it would be doubled.
function onAttached({ rs }: StreamContext, payload: StreamPayload) {
  if (typeof payload.turn === 'number') {
    const turn: number = payload.turn
    rs.liveTurnRef.current = turn
    markTurn(rs, turn)
    if (rs.lastIdRef.current === 0) { rs.setStepsByTurn(prev => ({ ...prev, [turn]: [] })) }
  }
  const dropped = payload.dropped ?? 0
  if (dropped > 0) {
    rs.setMessages(prev => [...prev, {
      id: rs.nextId(), role: 'agent', kind: 'kind',
      text: tf('({n} event(s) were not kept while nothing was listening)', { n: dropped })
    }])
  }
  if (payload.pending_approval) { rs.setApproval(payload.pending_approval) }
  if (Array.isArray(payload.agents)) { rs.setAgents(sortAgents(payload.agents)) }
}

// settleLastCommand puts a command's result on the last command of the list still waiting for one.
function settleLastCommand<T extends { exit?: number | null }>(list: T[], isCommand: (x: T) => boolean, patch: Partial<T>): T[] | null {
  for (let i = list.length - 1; i >= 0; i--) {
    if (isCommand(list[i]) && list[i].exit == null) {
      const next = list.slice()
      next[i] = { ...list[i], ...patch }
      return next
    }
  }
  return null
}

// onOutput: a command's output is NOT a step of the conversation: the trail shows the
// command and whether it worked, and the terminal drawer keeps what it printed.
// The result lands on the command it belongs to.
function onOutput({ rs }: StreamContext, text: string) {
  const { exit, body } = splitOutput(text)
  const result = { exit: exit ?? 0, out: body }
  const liveTurn = rs.liveTurnRef.current
  if (liveTurn != null) {
    rs.setStepsByTurn(prev => {
      const next = settleLastCommand<StepView>(prev[liveTurn] ?? [], st => st.kind === 'command', result)
      if (!next) { return prev }
      return { ...prev, [liveTurn]: next }
    })
  }
  rs.setShellLog(prev => settleLastCommand<ShellEntry>(prev, () => true, result) ?? prev)
  rs.setLiveThought(null)
}

// stepKind is how a progress line is kept among the turn's steps.
function stepKind(kind: string): string {
  if (kind === 'command') { return 'command' }
  return choose(kind === 'thought', 'thought', 'step')
}

// onStep: a progress line that is a step of the turn (a command, a phase, a thought).
function onStep({ rs }: StreamContext, text: string, kind: string) {
  // A finished thought belongs to the thinking drawer, not to the chat: it is filed with
  // the turn's steps (where it is kept) and the live snapshot it grew from is done.
  if (kind !== 'thought') {
    rs.setActivity(text)
    rs.setActivityKind(kind)
  }
  const liveTurn = rs.liveTurnRef.current
  if (text && liveTurn != null) {
    const step = keyed({ kind: stepKind(kind), text, ...(choose(kind === 'command', { exit: null }, {})) })
    rs.setStepsByTurn(prev => ({ ...prev, [liveTurn]: [...(prev[liveTurn] ?? []), step].slice(-STEPS_MAX) }))
  }
  if (kind === 'command') {
    const cmd = commandOf(text)
    rs.setShellLog(prev => [...prev, { id: rs.nextId(), cmd, exit: null, out: '' }].slice(-SHELL_LOG_MAX))
  }
  rs.setLiveThought(null)
}

function onProgress(ctx: StreamContext, payload: StreamPayload) {
  const text: string = payload.text || ''
  if (text.startsWith(AGENTS_PREFIX)) {
    // The agent list sent as a plain line: a snapshot, never a step of the conversation.
    try { ctx.rs.setAgents(sortAgents(JSON.parse(text.slice(AGENTS_PREFIX.length)))) } catch { /* not a list */ }
    return
  }
  const kind = classifyProgress(text) ?? ''
  if (kind === 'output') {
    onOutput(ctx, text)
    return
  }
  onStep(ctx, text, kind)
}

function onDone({ rs, finish }: StreamContext, payload: StreamPayload) {
  rs.setActivity(null)
  if (payload.result) {
    const result = payload.result
    rs.setMessages(prev => [...prev, {
      id: rs.nextId(), role: 'agent', text: result, changes: payload.changes,
      artifacts: choose(Array.isArray(payload.artifacts) && payload.artifacts.length, payload.artifacts, undefined),
      report: choose(hasReport(payload.report), payload.report, undefined),
    }])
  }
  finish()
}

function onError({ rs, finish }: StreamContext, payload: StreamPayload) {
  rs.setActivity(null)
  if (payload.error) {
    const error = payload.error
    rs.setMessages(prev => [...prev, { id: rs.nextId(), role: 'agent', text: error, kind: 'error' }])
  }
  finish()
}

const HANDLERS: Record<string, (ctx: StreamContext, payload: StreamPayload) => void> = {
  attached: onAttached,
  // A snapshot of the run's agents: it replaces the one before it.
  agents: ({ rs }, payload) => rs.setAgents(sortAgents(payload.agents)),
  progress: onProgress,
  // Ephemeral: never replayed, never kept. See EventThinking in the gateway.
  thinking: ({ rs }, payload) => rs.setLiveThought(payload.text || null),
  approval: ({ rs }, payload) => {
    rs.setActivity(null)
    rs.setApproval(payload as PendingApproval)
  },
  done: onDone,
  error: onError,
}

// handleStreamEvent applies one event. Unknown events are ignored: a future server may add one.
export function handleStreamEvent(ctx: StreamContext, name: string, payload: StreamPayload) {
  const handler = HANDLERS[name]
  if (handler) { handler(ctx, payload) }
}
