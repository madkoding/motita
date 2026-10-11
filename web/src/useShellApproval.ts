// The commands a person types (`!ls`, `/shell ls`) and the approval panel: the question the agent
// or a typed command is waiting on, and the "allow every command" switch of a session.
import { useCallback, useEffect, useRef } from 'preact/hooks'
import { t } from './i18n'
import { choose } from './choose'
import { withSession, type SessionList } from './useSessionList'
import type { RunState } from './useRunState'
import type { Drawers } from './useDrawers'
import type { Api } from './prApi'
import type { SetStatus, SetToast } from './appTypes'
import { APPROVAL_ALL, APPROVAL_NO, APPROVAL_OPTIONS, JSON_HEADERS, NEEDS_APPROVAL, NO_RESULT, SESSIONS_PATH, SHELL_LOG_MAX } from './appConst'

interface Deps {
  api: Api
  rs: RunState
  lists: SessionList
  drawers: Drawers
  setState: SetStatus
  setToast: SetToast
}

// approvalMove is how far an arrow key moves the highlighted option, or null for any other key.
function approvalMove(key: string): number | null {
  if (key === 'ArrowRight' || key === 'ArrowDown') { return 1 }
  if (key === 'ArrowLeft' || key === 'ArrowUp') { return APPROVAL_OPTIONS - 1 }
  return null
}

export function useShellApproval({ api, rs, lists, drawers, setState, setToast }: Deps) {
  const { sessionRef, nextId, setShellLog, setApproval, approval, approvalSel, setApprovalSel, approvalRef } = rs
  const { setSessions } = lists

  // runShellLine runs a command the person typed (`!ls`, `/shell ls`) in the session's workspace,
  // under the same policy as the agent's commands. Its output goes to the terminal drawer, where
  // the agent's commands are. When the policy would ask, the question is the approval panel's,
  // and the answer comes back through answerApproval with a "shell:" id.
  const shellPendingRef = useRef<string | null>(null)
  const runShellLine = useCallback(async (command: string, approved = false) => {
    const sid = sessionRef.current
    const entryId = nextId()
    setShellLog(prev => [...prev, { id: entryId, cmd: command, exit: null, out: '' }].slice(-SHELL_LOG_MAX))
    drawers.showTerm()
    const settle = (exit: number, out: string) =>
      setShellLog(prev => prev.map(e => { if (e.id === entryId) { return { ...e, exit, out } } return e }))
    try {
      const res = await api(SESSIONS_PATH + encodeURIComponent(sid) + '/shell', {
        method: 'POST',
        headers: JSON_HEADERS,
        body: JSON.stringify({ command, approved }),
      })
      const body = await res.json().catch(() => ({}))
      if (!res.ok) {
        settle(NO_RESULT, body.error ?? '')
        setToast({ message: t('The command could not be run'), type: 'error', detail: body.error })
        return
      }
      if (body.needs_approval) {
        // Nothing ran: the line leaves the terminal and waits for the person's yes.
        setShellLog(prev => prev.filter(e => e.id !== entryId))
        shellPendingRef.current = command
        setApproval({ id: 'shell:' + entryId, question: body.reason || t(NEEDS_APPROVAL), command })
        return
      }
      const out = [body.output, body.error].filter(Boolean).join('\n')
      settle(choose(body.error && !body.output, NO_RESULT, (body.exit ?? 0)), out)
    } catch {
      settle(NO_RESULT, '')
      setToast({ message: t('The command could not be run'), type: 'error' })
    }
  }, [])

  // allowAll turns "allow every command" on for the session, as the panel's second answer does.
  const allowAll = async (sid: string) => {
    const res = await api(SESSIONS_PATH + sid + '/auto-approve', {
      method: 'POST',
      headers: JSON_HEADERS,
      body: JSON.stringify({ enabled: true }),
    }).catch(() => null)
    if (res?.ok) { setSessions(prev => withSession(prev, sid, { auto_approve: true })) }
  }

  // answerShell answers a typed command's question: it has no run to answer, so it is asked again
  // with the yes in hand.
  const answerShell = async (approve: boolean, all: boolean) => {
    const command = shellPendingRef.current
    shellPendingRef.current = null
    if (!approve || !command) { return }
    if (all) { await allowAll(sessionRef.current) }
    void runShellLine(command, true)
  }

  // answerApproval sends the approval response.
  //
  // `all` is "allow every command for the rest of this session": the gateway then
  // answers yes on the user's behalf until they take it back from the header.
  const answerApproval = useCallback(async (id: string, approve: boolean, all = false) => {
    setApproval(null)
    if (id.startsWith('shell:')) {
      await answerShell(approve, all)
      return
    }
    try {
      await api(SESSIONS_PATH + sessionRef.current + '/runs/approval', {
        method: 'POST',
        headers: JSON_HEADERS,
        body: JSON.stringify(choose(all, { id, approve, scope: 'session' }, { id, approve }))
      })
      if (all) {
        const sid = sessionRef.current
        setSessions(prev => withSession(prev, sid, { auto_approve: true }))
      }
    } catch {
      setState('could not answer the approval', true)
    }
  }, [runShellLine])

  const approvalId = approval?.id
  useEffect(() => {
    if (!approvalId) { return }
    setApprovalSel(0)
    approvalRef.current?.focus()
  }, [approvalId])

  // onApprovalKey: arrows move between the options, Enter takes the highlighted one.
  const onApprovalKey = (e: KeyboardEvent) => {
    if (!approval) { return }
    const move = approvalMove(e.key)
    if (move !== null) {
      e.preventDefault()
      setApprovalSel(i => (i + move) % APPROVAL_OPTIONS)
    } else if (e.key === 'Enter') {
      e.preventDefault()
      answerApproval(approval.id, approvalSel !== APPROVAL_NO, approvalSel === APPROVAL_ALL)
    } else {
      // Any other key is not the approval's: the browser handles it.
    }
  }

  // stopAutoApprove takes back "allow all commands": the next command is asked again.
  const stopAutoApprove = useCallback(async () => {
    const sid = sessionRef.current
    try {
      const res = await api(SESSIONS_PATH + sid + '/auto-approve', {
        method: 'POST',
        headers: JSON_HEADERS,
        body: JSON.stringify({ enabled: false })
      })
      if (!res.ok) { throw new Error('HTTP ' + res.status) }
      setSessions(prev => withSession(prev, sid, { auto_approve: false }))
    } catch {
      setState('could not stop allowing all commands', true)
    }
  }, [])

  return { runShellLine, answerApproval, onApprovalKey, stopAutoApprove }
}
