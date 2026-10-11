// What a person does to a session from the sidebar: create one, rename it, delete it, and continue
// a merged one in a new session.
import { useCallback, useEffect, useState } from 'preact/hooks'
import { t } from './i18n'
import { choose } from './choose'
import { withSession, type SessionList } from './useSessionList'
import type { RunState } from './useRunState'
import type { Conversation } from './useConversation'
import type { Api } from './prApi'
import type { SessionInfo, SetStatus, SetToast } from './appTypes'
import { CREATE_SESSION_EVENT, HTTP_NO_CONTENT, HTTP_OK, JSON_HEADERS, NEW_SESSION_TITLE, SESSIONS_PATH, SESSIONS_URL } from './appConst'

interface Deps {
  api: Api
  rs: RunState
  lists: SessionList
  conv: Conversation
  setState: SetStatus
  setToast: SetToast
}

// deleteRefused says whether the gateway refused a deletion: 204 is deleted, 200 is the default
// session reset instead of removed.
function deleteRefused(res: Response): boolean {
  return !res.ok && res.status !== HTTP_NO_CONTENT && res.status !== HTTP_OK
}

// nextAfterDelete is the conversation to show once the one on screen was deleted: the default
// session when it was the one reset (or none is left), the most recent remaining one otherwise.
function nextAfterDelete(status: number, list: SessionInfo[], id: string): string {
  if (status === HTTP_OK) { return 'default' }
  const last = list.find(s => s.id !== id)
  return last?.id ?? 'default'
}

export function useSessionActions({ api, rs, lists, conv, setState, setToast }: Deps) {
  const { sessionRef } = rs
  const { fetchSessions, setSessions } = lists
  const { switchSession } = conv
  const [renamingId, setRenamingId] = useState<string | null>(null)
  const [renameValue, setRenameValue] = useState('')
  // Which "+" is creating a session right now: '' for the free-standing button, a
  // project id for a project's button, null when nothing is running. Creating a
  // session in a project makes the gateway pull/fetch the repo first, which is not
  // instant, so the button shows a spinner and refuses a second click while it runs.
  const [creatingSession, setCreatingSession] = useState<string | null>(null)

  // createSession opens a new conversation and switches to it.
  const createSession = useCallback(async (projectId?: string) => {
    setCreatingSession(projectId ?? '')
    try {
      const body = choose(projectId, JSON.stringify({ project_id: projectId }), '{}')
      const res = await api(SESSIONS_URL, {
        method: 'POST',
        headers: JSON_HEADERS,
        body,
      })
      if (!res.ok) {
        const err = await res.json().catch(() => ({}))
        setState(err.error || 'could not create a session', true)
        return
      }
      const data: SessionInfo = await res.json()
      await fetchSessions()
      await switchSession(data.id)
    } catch {
      setState('could not create a session', true)
    } finally {
      setCreatingSession(null)
    }
  }, [fetchSessions, switchSession])

  // Listen for the custom event from createProject to auto-create a session.
  useEffect(() => {
    const handler = (e: Event) => {
      const detail = (e as CustomEvent).detail
      if (detail?.project_id) {
        createSession(detail.project_id)
      }
    }
    window.addEventListener(CREATE_SESSION_EVENT, handler)
    return () => window.removeEventListener(CREATE_SESSION_EVENT, handler)
  }, [createSession])

  // deleteSession removes a conversation from the gateway and the sidebar.
  //
  // `discard` carries the user's answer to the question the modal asked. It is
  // sent as `?force=1`, and the gateway treats it as the ONLY thing that may
  // destroy uncommitted work: without it, a session whose checkout holds changes
  // is refused and the work survives.
  const deleteSession = useCallback(async (id: string, discard = false) => {
    try {
      const res = await api(SESSIONS_PATH + id + (choose(discard, '?force=1', '')), { method: 'DELETE' })
      if (deleteRefused(res)) {
        const err = await res.json().catch(() => ({}))
        setState(err.error || 'could not delete the session', true)
        return
      }
      // Remove the deleted session from the local state IMMEDIATELY, before
      // the refetch. A 3-second polling interval (or a run's deferred
      // saveSession resurrecting the file) can slip a stale list in between
      // the DELETE and fetchSessions, making the session flicker back into
      // the sidebar. For 200 (default reset) we keep the row but clear its
      // title locally so the placeholder shows without waiting for the refetch.
      if (res.status === HTTP_NO_CONTENT) {
        setSessions(prev => prev.filter(s => s.id !== id))
      } else {
        setSessions(prev => withSession(prev, id, { title: NEW_SESSION_TITLE }))
      }
      const list = await fetchSessions()
      // If we deleted the active session, switch to the most recent remaining one; a default
      // session that was reset reloads its now-empty transcript.
      if (id === sessionRef.current) {
        await switchSession(nextAfterDelete(res.status, list, id))
      }
    } catch {
      setState('could not delete the session', true)
    }
  }, [fetchSessions, switchSession])

  // renameSession changes the title of a conversation.
  const renameSession = useCallback(async (id: string, title: string) => {
    const trimmed = title.trim()
    if (!trimmed) { return }
    try {
      const res = await api(SESSIONS_PATH + id, {
        method: 'PATCH',
        headers: JSON_HEADERS,
        body: JSON.stringify({ title: trimmed })
      })
      if (!res.ok) {
        const err = await res.json().catch(() => ({}))
        setState(err.error || 'could not rename the session', true)
        return
      }
      const updated: SessionInfo = await res.json()
      setSessions(prev => prev.map(s => choose(s.id === updated.id, updated, s)))
    } catch {
      setState('could not rename the session', true)
    }
    setRenamingId(null)
  }, [])

  // continueSession creates a fresh session from a merged one, pulling the
  // project's base branch first so the new work starts from the latest state.
  const continueSession = useCallback(async (s: SessionInfo) => {
    try {
      const res = await api(SESSIONS_PATH + s.id + '/continue', {
        method: 'POST',
        headers: JSON_HEADERS,
        body: '{}',
      })
      if (!res.ok) {
        const data = await res.json().catch(() => ({}))
        setToast({ message: t('Could not continue this session'), type: 'error', detail: data.error || ('HTTP ' + res.status) })
        return
      }
      const next: SessionInfo = await res.json()
      await fetchSessions()
      await switchSession(next.id)
      setToast({ message: t('Continued in a new session'), type: 'success', detail: next.title })
    } catch (err) {
      setToast({ message: t('Could not reach the gateway'), type: 'error', detail: String(err) })
    }
  }, [fetchSessions, switchSession])

  // startRename puts a session's row in rename mode, with its title in the field.
  const startRename = (id: string, title: string) => {
    setRenamingId(id)
    setRenameValue(title)
  }

  return {
    renamingId, setRenamingId, renameValue, setRenameValue, startRename, creatingSession,
    createSession, deleteSession, renameSession, continueSession,
  }
}

export type SessionActions = ReturnType<typeof useSessionActions>
