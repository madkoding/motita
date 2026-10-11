import { useEffect, useMemo, useReducer, useRef, useState } from 'preact/hooks'
import { backdropClick } from './a11y'
import { t, tf } from './i18n'
import { useDialog } from './useDialog'
import { copyText } from './clipboard'
import {
  type Api, type GitAccount, type GitMethod, type ConnectRequest, GitError,
  connect, listAccounts, flowStatus, pasteCode, cancelFlow, pollFlow, reduceFlow, primaryMethod,
} from './gitApi'

const btn = 'min-h-[44px] px-4 rounded-xl font-semibold active:scale-95 transition-transform disabled:opacity-40 disabled:cursor-not-allowed'
const btnPrimary = btn + ' bg-accent text-white'
const btnGhost = 'min-h-[44px] px-4 rounded-xl border border-white/10 text-[#e8e8ea] active:scale-95 transition-transform'
const input = 'w-full px-3 py-2.5 rounded-xl bg-black/30 border border-white/10 text-[#e8e8ea] focus:outline-hidden focus:border-accent'
const linkBtn = 'text-sm text-accent underline underline-offset-2 hover:opacity-80'

export function Spinner({ size = 16 }: { size?: number }) {
  return (
    <svg class="animate-spin flex-none text-accent" width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="M21 12a9 9 0 1 1-6.219-8.56" />
    </svg>
  )
}

// A host the user typed in (self-hosted server): it is not in the accounts list until it is connected.
export interface SelfHosted { kind: string; host: string }

interface Props {
  api: Api
  // The account id to connect ('github', 'gitlab@git.corp.io'); without it the user picks a host.
  service?: string
  selfHosted?: SelfHosted
  onClose: () => void
  // Called once the host is connected, before the modal closes itself.
  onConnected: (service: string, account: string) => void
}

function errorText(e: unknown): string {
  if (e instanceof GitError) {
    if (e.status === 501) return t('This server cannot sign in to git hosts. Ask whoever runs it to enable login.')
    return e.message
  }
  return e instanceof Error ? e.message : String(e)
}

// GitConnectModal is the connect flow for one git host: one-click login (device code or browser
// redirect) when the gateway can do it, a pasted token always.
export function GitConnectModal({ api, service, selfHosted, onClose, onConnected }: Props) {
  const ref = useRef<HTMLDivElement>(null)
  useDialog(ref, onClose)
  const [accounts, setAccounts] = useState<GitAccount[] | null>(null)
  const [loadErr, setLoadErr] = useState('')
  const [picked, setPicked] = useState<string | undefined>(service)

  useEffect(() => {
    let live = true
    listAccounts(api).then(a => { if (live) setAccounts(a) }).catch(e => { if (live) { setLoadErr(errorText(e)); setAccounts([]) } })
    return () => { live = false }
  }, [])

  const account: GitAccount | null = useMemo(() => {
    if (selfHosted) {
      const found = accounts?.find(a => a.kind === selfHosted.kind && a.host === selfHosted.host)
      return found || { id: '', name: selfHosted.host, kind: selfHosted.kind, host: selfHosted.host, connected: false, methods: ['token'], oauth_ready: false }
    }
    return accounts?.find(a => a.id === picked) || null
  }, [accounts, picked, selfHosted])

  const title = account ? tf('Connect {host}', { host: account.name }) : t('Connect a git host')

  return (
    <div class="fixed inset-0 z-120 flex items-center justify-center bg-black/70 backdrop-blur-xs p-4" data-testid="git-connect-modal" role="presentation" onClick={backdropClick(onClose)}>
      <div ref={ref} data-dialog-root class="frosted rounded-2xl border border-white/10 w-full max-w-md max-h-[90vh] overflow-y-auto p-5 shadow-2xl" role="dialog" aria-modal="true" aria-label={title}>
        <div class="flex items-center gap-2 mb-4">
          <h2 class="flex-1 text-base font-semibold text-[#e8e8ea]">{title}</h2>
          <button class="p-1.5 rounded-lg hover:bg-white/5 text-[#9a9aaa]" aria-label={t('Close')} onClick={onClose}>×</button>
        </div>
        {accounts === null && <div class="flex items-center gap-2 text-sm text-[#9a9aaa]"><Spinner /> {t('Loading…')}</div>}
        {accounts !== null && !account && (
          <div class="space-y-2" role="list">
            {loadErr && <p class="text-sm text-danger" role="alert">{loadErr}</p>}
            <p class="text-sm text-[#9a9aaa]">{t('Which git host do you want to connect?')}</p>
            {accounts.map(a => (
              <button key={a.id} role="listitem" class="w-full flex items-center gap-2 px-3 py-2.5 rounded-xl border border-white/10 hover:bg-white/5 text-sm text-[#e8e8ea] text-left" onClick={() => setPicked(a.id)}>
                <span class="flex-1 min-w-0 truncate">{a.name} <span class="text-[#7a7a8c]">{a.host}</span></span>
                {a.connected && <span class="text-xs text-[#5fd08a]">{tf('Connected as {user}', { user: a.username || '' })}</span>}
              </button>
            ))}
          </div>
        )}
        {account && <ConnectPanel api={api} account={account} selfHosted={selfHosted} onClose={onClose} onConnected={onConnected} />}
      </div>
    </div>
  )
}

function ConnectPanel({ api, account, selfHosted, onClose, onConnected }: { api: Api; account: GitAccount; selfHosted?: SelfHosted; onClose: () => void; onConnected: (service: string, account: string) => void }) {
  const [state, dispatch] = useReducer(reduceFlow, { phase: 'idle' } as ReturnType<typeof reduceFlow>)
  const primary = primaryMethod(account)
  const [mode, setMode] = useState<'oauth' | 'token'>(primary === 'token' ? 'token' : 'oauth')
  const [note, setNote] = useState('')
  const flowId = useRef('')
  const doneRef = useRef(false)

  const base = (): ConnectRequest => (account.id ? { service: account.id } : { kind: account.kind, host: account.host })

  // Cancel a pending login when the modal goes away.
  useEffect(() => () => { if (flowId.current && !doneRef.current) void cancelFlow(api, flowId.current) }, [])

  const finish = (service: string, acct: string) => {
    doneRef.current = true
    flowId.current = ''
    onConnected(service, acct)
    onClose()
  }

  const start = async (method: GitMethod) => {
    setNote('')
    dispatch({ type: 'start' })
    try {
      const r = await connect(api, { ...base(), method })
      if (r.type === 'connected') return finish(r.service || account.id, r.account)
      flowId.current = r.flow.flow_id
      dispatch({ type: 'started', flow: r.flow })
    } catch (e) {
      if (e instanceof GitError && e.status === 502) {
        dispatch({ type: 'reset' })
        setMode('token')
        setNote(tf('{host} could not start the login: {err}. You can connect with a token instead.', { host: account.name, err: e.message }))
        return
      }
      dispatch({ type: 'failed', error: errorText(e) })
    }
  }

  // Poll while a login is pending.
  const pendingId = state.phase === 'pending' ? state.flow.flow_id : ''
  useEffect(() => {
    if (!pendingId) return
    let stop = false
    pollFlow({
      get: () => flowStatus(api, pendingId),
      sleep: (ms) => new Promise(r => setTimeout(r, ms)),
      cancelled: () => stop,
    }).then(st => {
      if (!st || stop) return
      dispatch({ type: 'poll', status: st })
      if (st.status === 'connected') { doneRef.current = true; finish(st.service || account.id, st.account || '') }
      else doneRef.current = true
    })
    return () => { stop = true }
  }, [pendingId])

  const retry = () => { flowId.current = ''; doneRef.current = false; dispatch({ type: 'reset' }) }

  if (state.phase === 'starting') {
    return <div class="flex items-center gap-2 text-sm text-[#9a9aaa]" role="status"><Spinner /> {tf('Contacting {host}…', { host: account.name })}</div>
  }
  if (state.phase === 'failed') {
    return (
      <div class="space-y-3">
        <p class="text-sm text-danger" role="alert">{t(state.error)}</p>
        <div class="flex flex-wrap gap-2 items-center">
          <button class={btnPrimary} onClick={retry}>{t('Retry')}</button>
          <button class={linkBtn} onClick={() => { retry(); setMode('token') }}>{t('Use a token instead')}</button>
        </div>
      </div>
    )
  }
  if (state.phase === 'pending') {
    const f = state.flow
    return (
      <div class="space-y-4">
        {f.method === 'device' ? <DevicePending flow={f} account={account} /> : <CodePending api={api} flow={f} account={account} />}
        <div class="flex items-center gap-2 text-sm text-[#9a9aaa]" role="status" aria-live="polite">
          <Spinner /> {tf('Waiting for you to approve in {host}…', { host: account.name })}
        </div>
        <div class="flex gap-2">
          <button class={btnGhost} onClick={() => { void cancelFlow(api, f.flow_id); flowId.current = ''; dispatch({ type: 'reset' }) }}>{t('Cancel')}</button>
        </div>
      </div>
    )
  }

  // idle: the method the user picks.
  const oauthMethod = account.methods.find(m => m === 'device' || m === 'code')
  const canOauth = !!oauthMethod && (account.oauth_ready || !!selfHosted)
  return (
    <div class="space-y-4">
      {note && <p class="text-sm text-warning" role="status">{note}</p>}
      {account.setup_hint && !account.oauth_ready && <p class="text-xs text-[#7a7a8c]">{account.setup_hint}</p>}
      {mode === 'oauth' && oauthMethod ? (
        <div class="space-y-3">
          <p class="text-sm text-[#9a9aaa]">{tf('Sign in with {host} to let motita clone your repositories. It only asks for repository access.', { host: account.name })}</p>
          <button class={btnPrimary + ' w-full'} data-autofocus onClick={() => start(oauthMethod)}>{tf('Connect {host}', { host: account.name })}</button>
          <button class={linkBtn} onClick={() => setMode('token')}>{t('Use a token instead')}</button>
        </div>
      ) : (
        <TokenForm api={api} account={account} base={base()} onDone={finish} />
      )}
      {mode === 'token' && canOauth && oauthMethod && (
        <button class={linkBtn} onClick={() => setMode('oauth')}>{t('Sign in with the browser instead')}</button>
      )}
    </div>
  )
}

function CopyButton({ text }: { text: string }) {
  const [done, setDone] = useState(false)
  return (
    <button class={btnGhost} aria-label={t('Copy code')} onClick={async () => {
      if (await copyText(text)) { setDone(true); setTimeout(() => setDone(false), 1800) }
    }}>{done ? t('Copied') : t('Copy')}</button>
  )
}

function DevicePending({ flow, account }: { flow: { url: string; code?: string }; account: GitAccount }) {
  return (
    <div class="space-y-3">
      <p class="text-sm text-[#9a9aaa]">{tf('Open {host}, enter this code and approve the access.', { host: account.name })}</p>
      <div class="flex items-center gap-2">
        <code class="flex-1 text-center text-2xl font-mono font-bold tracking-widest py-3 rounded-xl bg-black/40 border border-white/10 text-[#e8e8ea] select-all" data-testid="device-code" aria-label={t('Login code')}>{flow.code}</code>
        <CopyButton text={flow.code || ''} />
      </div>
      <a class={btnPrimary + ' flex items-center justify-center w-full'} href={flow.url} target="_blank" rel="noopener noreferrer" data-autofocus>{tf('Open {host}', { host: account.name })}</a>
    </div>
  )
}

function CodePending({ api, flow, account }: { api: Api; flow: { flow_id: string; url: string }; account: GitAccount }) {
  const [open, setOpen] = useState(false)
  const [text, setText] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const [sent, setSent] = useState(false)
  const send = async () => {
    setBusy(true); setErr('')
    try { await pasteCode(api, flow.flow_id, text.trim()); setSent(true) } catch (e) { setErr(errorText(e)) }
    setBusy(false)
  }
  return (
    <div class="space-y-3">
      <p class="text-sm text-[#9a9aaa]">{tf('Authorize motita in {host}. This page notices when you are done.', { host: account.name })}</p>
      <a class={btnPrimary + ' flex items-center justify-center w-full'} href={flow.url} target="_blank" rel="noopener noreferrer" data-autofocus>{tf('Open {host} to authorize', { host: account.name })}</a>
      <details open={open} onToggle={(e) => setOpen((e.target as HTMLDetailsElement).open)}>
        <summary class="text-sm text-accent cursor-pointer">{t('Browser on another machine? Paste the code or the URL it ended on')}</summary>
        <div class="mt-2 space-y-2">
          <textarea class={input + ' font-mono text-xs'} rows={3} value={text} onInput={(e) => setText((e.target as HTMLTextAreaElement).value)} aria-label={t('Authorization code or URL')} placeholder="…/callback?code=…" />
          {err && <p class="text-sm text-danger" role="alert">{err}</p>}
          {sent && <p class="text-sm text-[#5fd08a]" role="status">{t('Code sent. Finishing the login…')}</p>}
          <button class={btnGhost} disabled={busy || !text.trim()} onClick={send}>{t('Send code')}</button>
        </div>
      </details>
    </div>
  )
}

function TokenForm({ api, account, base, onDone }: { api: Api; account: GitAccount; base: ConnectRequest; onDone: (service: string, account: string) => void }) {
  const [token, setToken] = useState('')
  const [user, setUser] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const bb = account.kind === 'bitbucket'
  const ok = token.trim() && (!bb || user.trim())
  const submit = async (e: Event) => {
    e.preventDefault()
    if (!ok || busy) return
    setBusy(true); setErr('')
    try {
      const r = await connect(api, { ...base, method: 'token', token: token.trim(), username: bb ? user.trim() : undefined })
      if (r.type === 'connected') { onDone(r.service || account.id, r.account); return }
      setErr(t('Unexpected answer from the server.'))
    } catch (e2) { setErr(errorText(e2)) }
    setBusy(false)
  }
  return (
    <form class="space-y-3" onSubmit={submit}>
      <p class="text-sm text-[#9a9aaa]">
        {bb ? t('Bitbucket needs an app password, not your account password. Give it read access to repositories.') : t('Create a personal access token with permission to read your repositories (the "repo" or "read_repository" scope), then paste it here.')}
      </p>
      {account.token_url && <a class={linkBtn} href={account.token_url} target="_blank" rel="noopener noreferrer">{t('Create a token')} ↗</a>}
      {bb && (
        <label class="block">
          <span class="block text-sm text-[#9a9aaa] mb-1.5">{t('Account name')}</span>
          <input class={input} value={user} onInput={(e) => setUser((e.target as HTMLInputElement).value)} autocomplete="username" autocapitalize="off" spellcheck={false} />
        </label>
      )}
      <label class="block">
        <span class="block text-sm text-[#9a9aaa] mb-1.5">{bb ? t('App password') : t('Token')}</span>
        <input class={input + ' font-mono text-sm'} type="password" value={token} onInput={(e) => setToken((e.target as HTMLInputElement).value)} autocomplete="off" spellcheck={false} data-autofocus />
      </label>
      <p class="text-xs text-[#7a7a8c]">{t('The token is stored on this machine and used only to clone and list repositories.')}</p>
      {err && <p class="text-sm text-danger" role="alert">{err}</p>}
      <button type="submit" class={btnPrimary + ' w-full'} disabled={!ok || busy}>{busy ? t('Checking…') : t('Connect')}</button>
    </form>
  )
}
