import { useEffect, useRef, useState } from 'preact/hooks'
import { t, tf } from './i18n'
import { useDialog } from './useDialog'
import { type Api, type GitAccount, listAccounts, disconnect } from './gitApi'
import { GitConnectModal, type SelfHosted } from './GitConnect'

const inputCls = 'min-w-0 px-2 py-1.5 rounded-lg bg-black/30 border border-white/10 text-xs text-[#e8e8ea] focus:outline-none focus:border-accent'
const smallBtn = 'px-3 min-h-[32px] rounded-lg border border-white/10 text-xs text-[#e8e8ea] hover:bg-white/5 active:scale-95 transition-transform'

export interface GitConnectionsProps {
  api: Api
  // Bumped by the app after a host was connected elsewhere, so the list is read again.
  rev?: number
  onNotice: (message: string, error?: boolean) => void
  onClose: () => void
}

// GitConnectionsModal lists every git host with its state and does the whole job inside the
// dialog: connect, reconnect, disconnect and add a self-hosted server, without leaving the page.
export function GitConnectionsModal({ api, rev = 0, onNotice, onClose }: GitConnectionsProps) {
  const ref = useRef<HTMLDivElement>(null)
  useDialog(ref, onClose)
  const [accounts, setAccounts] = useState<GitAccount[] | null>(null)
  const [err, setErr] = useState('')
  const [confirm, setConfirm] = useState('')
  const [adding, setAdding] = useState(false)
  const [kind, setKind] = useState('gitlab')
  const [host, setHost] = useState('')
  const [reload, setReload] = useState(0)
  const [connecting, setConnecting] = useState<{ service?: string; selfHosted?: SelfHosted } | null>(null)

  useEffect(() => {
    let live = true
    listAccounts(api).then(a => { if (live) { setAccounts(a); setErr('') } }).catch(e => { if (live) { setErr(e instanceof Error ? e.message : String(e)); setAccounts([]) } })
    return () => { live = false }
  }, [rev, reload])

  const doDisconnect = async (a: GitAccount) => {
    try {
      await disconnect(api, a.id)
      onNotice(tf('Disconnected from {host}', { host: a.name }))
    } catch (e) {
      onNotice(e instanceof Error ? e.message : String(e), true)
    }
    setConfirm('')
    setReload(n => n + 1)
  }

  const cleanHost = host.trim().replace(/^https?:\/\//, '').replace(/\/+$/, '')
  return (
    <div class="fixed inset-0 z-[110] flex items-center justify-center bg-black/60 p-4" data-testid="git-connections-modal" onClick={onClose}>
      <div ref={ref} data-dialog-root class="frosted rounded-2xl border border-white/10 w-full max-w-md max-h-[90vh] overflow-y-auto p-5 space-y-3 shadow-2xl" role="dialog" aria-modal="true" aria-label={t('Git connections')} onClick={(e) => e.stopPropagation()}>
        <div class="flex items-center">
          <h2 class="flex-1 text-base font-semibold text-[#e8e8ea]">{t('Git connections')}</h2>
          <button class="text-[#9a9aaa] px-2" aria-label={t('Close')} onClick={onClose}>×</button>
        </div>
        <p class="text-xs text-[#7a7a8c]">{t('Connect a git host to pick your repositories and clone private ones.')}</p>
        {accounts === null && <p class="text-xs text-[#9a9aaa]" role="status">{t('Loading…')}</p>}
        {err && <p class="text-xs text-danger" role="alert">{err}</p>}
        <ul class="space-y-2" data-testid="git-providers">
          {accounts?.map(a => (
            <li key={a.id} data-provider={a.id} class="flex flex-wrap items-center gap-x-2 gap-y-1 text-sm text-[#e8e8ea] px-3 py-2 rounded-xl border border-white/10">
              <div class="flex-1 min-w-[9rem]">
                <div>{a.name} <span class="text-xs text-[#7a7a8c]">{a.host}</span></div>
                <div class={'text-xs ' + (a.connected ? 'text-[#5fd08a]' : 'text-[#7a7a8c]')}>
                  {a.connected ? tf('Connected as {user}', { user: a.username || '…' }) : t('Not connected')}
                </div>
              </div>
              {confirm === a.id ? (
                <>
                  <button class={smallBtn + ' text-danger'} onClick={() => doDisconnect(a)}>{t('Confirm')}</button>
                  <button class={smallBtn} onClick={() => setConfirm('')}>{t('Cancel')}</button>
                </>
              ) : a.connected ? (
                <>
                  <button class={smallBtn} aria-label={tf('Reconnect {host}', { host: a.name })} onClick={() => setConnecting({ service: a.id })}>{t('Reconnect')}</button>
                  <button class={smallBtn} aria-label={tf('Disconnect {host}', { host: a.name })} onClick={() => setConfirm(a.id)}>{t('Disconnect')}</button>
                </>
              ) : (
                <button class={smallBtn + ' bg-accent text-white border-transparent'} aria-label={tf('Connect {host}', { host: a.name })} onClick={() => setConnecting({ service: a.id })}>{t('Connect')}</button>
              )}
            </li>
          ))}
        </ul>
        {adding ? (
          <form class="space-y-2" onSubmit={(e) => { e.preventDefault(); if (cleanHost) { setConnecting({ selfHosted: { kind, host: cleanHost } }); setAdding(false); setHost('') } }}>
            <div class="flex gap-2">
              <select class={inputCls} value={kind} aria-label={t('Server type')} onChange={(e) => setKind((e.target as HTMLSelectElement).value)}>
                <option value="gitlab">GitLab</option>
                <option value="github">GitHub Enterprise</option>
                <option value="gitea">Gitea / Forgejo</option>
              </select>
              <input class={inputCls + ' flex-1'} value={host} placeholder="git.example.com" aria-label={t('Server address')} autocapitalize="off" spellcheck={false} onInput={(e) => setHost((e.target as HTMLInputElement).value)} />
            </div>
            <div class="flex gap-2">
              <button type="submit" class={smallBtn + ' bg-accent text-white border-transparent'} disabled={!cleanHost}>{t('Continue')}</button>
              <button type="button" class={smallBtn} onClick={() => setAdding(false)}>{t('Cancel')}</button>
            </div>
          </form>
        ) : (
          <button class={smallBtn} onClick={() => setAdding(true)}>{t('Add self-hosted server')}</button>
        )}
      </div>
      {connecting && (
        <GitConnectModal
          api={api}
          service={connecting.service}
          selfHosted={connecting.selfHosted}
          onClose={() => setConnecting(null)}
          onConnected={(_service, account) => { onNotice(tf('Connected as {user}', { user: account })); setReload(n => n + 1) }}
        />
      )}
    </div>
  )
}
