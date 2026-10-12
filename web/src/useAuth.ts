// Starting the page: the credential (traded from the link, or typed in the blocking modal), the
// first load of the lists and the last conversation, and the language of the interface.
import { useCallback, useEffect, useState } from 'preact/hooks'
import { t, setLang, resolveLang, type LangSetting } from './i18n'
import { choose } from './choose'
import { authGate, exchange, submitToken } from './appAuth'
import { sessionFromUrl } from './appFormat'
import type { RunState } from './useRunState'
import type { SessionList } from './useSessionList'
import type { Conversation } from './useConversation'
import type { Api } from './prApi'
import type { SessionInfo, SetStatus, SetToast, SlashCommand } from './appTypes'
import { AUTH_NEEDED_EVENT, JSON_HEADERS, NOT_CONNECTED, STORAGE_KEY } from './appConst'

export type AuthState = 'checking' | 'needed' | 'ok'

// lastSessionOf is the conversation to open: the one the address names, else the one used last,
// else the default one.
function lastSessionOf(list: SessionInfo[]): string {
  let lastId = ''
  try { lastId = localStorage.getItem(STORAGE_KEY) || '' } catch { /* ignore */ }
  const fromUrl = sessionFromUrl()
  if (fromUrl && list.some(s => s.id === fromUrl)) { lastId = fromUrl }
  const exists = list.some(s => s.id === lastId)
  return choose(exists, lastId, 'default')
}

// langSettingOf reads the gateway's ui.language: anything it does not name is the browser's.
function langSettingOf(language: unknown): LangSetting {
  return choose(language === 'en' || language === 'es', language as LangSetting, 'auto')
}

interface Deps {
  api: Api
  rs: RunState
  lists: SessionList
  conv: Conversation
  setState: SetStatus
  setSlashCommands: (list: SlashCommand[]) => void
}

export function useAuth({ api, rs, lists, conv, setState, setSlashCommands }: Deps) {
  const { fetchProjects, fetchSessions } = lists
  const { switchSession } = conv
  // Auth gate: 'checking' while the initial probe is in flight, 'needed' when
  // the browser has no valid credential, 'ok' when it does. The blocking modal
  // is rendered whenever authState !== 'ok'.
  const [authState, setAuthState] = useState<AuthState>('checking')
  const [authInput, setAuthInput] = useState('')
  const [authError, setAuthError] = useState('')
  const [authBusy, setAuthBusy] = useState(false)

  // loadAll loads everything the page shows, as if it had just started: the projects, the slash
  // commands for autocomplete, the sessions, and the last-used session (or the default one).
  const loadAll = async () => {
    try {
      await fetchProjects()
      try {
        const res = await api('/v1/commands')
        const data = await res.json()
        setSlashCommands(data.commands || [])
      } catch { /* non-fatal */ }
      const list = await fetchSessions()
      await switchSession(lastSessionOf(list), 'replace')
      setState('ready')
    } catch {
      setState(NOT_CONNECTED, true)
    }
  }

  // Start: exchange token, check auth, load sessions, pick up the last session or default.
  useEffect(() => {
    ;(async () => {
      // First, try to exchange the fragment token (from the gateway link).
      try { await exchange() } catch { /* ignore */ }
      // Then check whether the browser now holds a valid credential.
      const ok = await authGate()
      if (!ok) {
        setAuthState('needed')
        return
      }
      setAuthState('ok')
      await loadAll()
    })()
    // Cleanup reconnect timer on unmount.
    return () => {
      if (rs.reconnectTimer.current) { clearTimeout(rs.reconnectTimer.current) }
    }
  }, [fetchProjects, fetchSessions, switchSession])

  // Listen for auth-needed events dispatched by api() on 401. The modal
  // reappears and local state is reset so nothing stale is shown.
  useEffect(() => {
    const handler = () => {
      setAuthState('needed')
      setAuthInput('')
      setAuthError('')
      rs.setMessages([])
      lists.setSessions([])
      lists.setProjects([])
      setState(NOT_CONNECTED, true)
    }
    window.addEventListener(AUTH_NEEDED_EVENT, handler)
    return () => window.removeEventListener(AUTH_NEEDED_EVENT, handler)
  }, [])

  // submitAuth is called when the user enters a token in the blocking modal.
  const submitAuth = useCallback(async () => {
    const token = authInput.trim()
    if (!token || authBusy) { return }
    setAuthBusy(true)
    setAuthError('')
    const ok = await submitToken(token)
    if (!ok) {
      setAuthError(t('That token was rejected. Try again.'))
      setAuthBusy(false)
      return
    }
    // Erase the token from the URL fragment if it was there.
    history.replaceState(null, '', window.location.pathname + window.location.search)
    setAuthState('ok')
    setAuthInput('')
    setAuthBusy(false)
    // Now load everything as if the app had just started.
    await loadAll()
  }, [authInput, authBusy, fetchProjects, fetchSessions, switchSession])

  return { authState, authInput, setAuthInput, authError, authBusy, submitAuth }
}

// useLanguage follows the language setting: the browser's until the gateway's can be read, then
// the gateway's ui.language, which applies to all of motita.
export function useLanguage(api: Api, authState: AuthState, setToast: SetToast) {
  // The ui.language setting as the gateway stores it, and whether this gateway has the endpoint at
  // all: an older one answers 404, and then the setting is hidden and the browser's language is used.
  const [uiLangSetting, setUiLangSetting] = useState<LangSetting>('auto')
  const [uiLangAvailable, setUiLangAvailable] = useState(false)

  // The language before anything is known: the browser's. It is what the token prompt is shown in,
  // since the gateway's setting can only be read once the browser holds a credential.
  useEffect(() => { void setLang(resolveLang('auto')) }, [])

  // Then the gateway's ui.language, which applies to all of motita: this page, the terminal, every
  // browser. `auto` still means "the browser's language" here, not the gateway machine's locale.
  useEffect(() => {
    if (authState !== 'ok') { return undefined }
    let cancelled = false
    ;(async () => {
      try {
        const res = await api('/v1/ui')
        if (!res.ok || cancelled) { return }
        const data = await res.json()
        const setting = langSettingOf(data.language)
        setUiLangSetting(setting)
        setUiLangAvailable(true)
        await setLang(resolveLang(setting))
      } catch { /* an older gateway: the browser's language stands */ }
    })()
    return () => { cancelled = true }
  }, [authState])

  // changeLanguage saves the setting for all of motita and switches this page at once.
  const changeLanguage = useCallback(async (setting: LangSetting) => {
    const before = uiLangSetting
    setUiLangSetting(setting)
    await setLang(resolveLang(setting))
    try {
      const res = await api('/v1/ui', {
        method: 'PUT',
        headers: JSON_HEADERS,
        body: JSON.stringify({ language: setting }),
      })
      if (!res.ok) {
        const err = await res.json().catch(() => ({}))
        throw new Error(err.error || 'HTTP ' + res.status)
      }
    } catch (e) {
      setUiLangSetting(before)
      await setLang(resolveLang(before))
      setToast({ message: t('Could not save the language'), type: 'error', detail: String(e) })
    }
  }, [uiLangSetting])

  return { uiLangSetting, uiLangAvailable, changeLanguage }
}

export type Auth = ReturnType<typeof useAuth>
export type Language = ReturnType<typeof useLanguage>
